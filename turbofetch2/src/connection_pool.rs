use crate::{
    error::{Result, TurbofetchError},
    http_client::S3Client,
    signature::AwsCredentials,
};
use bytes::Bytes;
use std::sync::Arc;
use tokio::sync::{mpsc, oneshot, Mutex};

pub struct ConnectionPool {
    request_sender: mpsc::Sender<ConnectionRequest>,
}

struct ConnectionRequest {
    response: oneshot::Sender<PooledConnection>,
}

impl ConnectionPool {
    pub fn new(
        hostname: String,
        buffer_size: usize,
        pool_size: usize,
        tls_config: Option<Arc<rustls::ClientConfig>>,
    ) -> Result<Self> {
        let (request_sender, request_receiver) = mpsc::channel(pool_size * 2);
        let (return_sender, return_receiver) = mpsc::channel(pool_size);

        let return_sender_clone = return_sender.clone();

        // Spawn the connection pool manager in a separate task
        tokio::spawn(async move {
            if let Err(e) = Self::run_pool_manager(
                hostname,
                buffer_size,
                pool_size,
                tls_config,
                request_receiver,
                return_receiver,
                return_sender_clone,
            )
            .await
            {
                tracing::error!("Connection pool manager failed: {}", e);
            }
        });

        Ok(ConnectionPool { request_sender })
    }

    pub async fn get_connection(&self) -> Result<PooledConnection> {
        let (response_sender, response_receiver) = oneshot::channel();

        self.request_sender
            .send(ConnectionRequest {
                response: response_sender,
            })
            .await
            .map_err(|_| TurbofetchError::Http("Connection pool is closed".to_string()))?;

        response_receiver
            .await
            .map_err(|_| TurbofetchError::Http("Failed to get connection from pool".to_string()))
    }

    async fn run_pool_manager(
        hostname: String,
        buffer_size: usize,
        pool_size: usize,
        tls_config: Option<Arc<rustls::ClientConfig>>,
        mut request_receiver: mpsc::Receiver<ConnectionRequest>,
        mut return_receiver: mpsc::Receiver<Arc<Mutex<S3Client>>>,
        return_sender: mpsc::Sender<Arc<Mutex<S3Client>>>,
    ) -> Result<()> {
        tracing::info!("Starting connection pool with {} connections", pool_size);

        // Create pre-established connections
        let mut available_connections = Vec::with_capacity(pool_size);

        for i in 0..pool_size {
            tracing::debug!("Creating connection {} of {}", i + 1, pool_size);

            let mut builder = S3Client::builder()
                .hostname(hostname.clone())
                .buffer_size(buffer_size);

            if let Some(ref config) = tls_config {
                builder = builder.tls_config(config.clone());
            }

            let mut client = builder.build()?;

            // Pre-establish the connection
            client.ensure_connected().await?;

            available_connections.push(Arc::new(Mutex::new(client)));
        }

        tracing::info!("Connection pool established with {} connections", pool_size);

        loop {
            tokio::select! {
                // Handle requests for connections
                Some(request) = request_receiver.recv() => {
                    if let Some(conn) = available_connections.pop() {
                        let pooled_conn = PooledConnection::new(conn.clone(), return_sender.clone());
                        if request.response.send(pooled_conn).is_err() {
                            // Request was cancelled, return connection to pool
                            available_connections.push(conn);
                            tracing::debug!("Request cancelled, connection returned to pool");
                        } else {
                            tracing::debug!("Connection handed out, {} remaining", available_connections.len());
                        }
                    } else {
                        // No connections available
                        tracing::warn!("No connections available in pool");
                        drop(request.response);
                    }
                }

                // Handle returned connections - always close and discard them
                Some(returned_conn) = return_receiver.recv() => {
                    // Explicitly disconnect and drop the returned connection
                    {
                        let mut conn = returned_conn.lock().await;
                        conn.disconnect();
                    }
                    drop(returned_conn);
                    tracing::debug!("Returned connection closed and discarded");

                    // Create a new connection to maintain pool size
                    let mut builder = S3Client::builder()
                        .hostname(hostname.clone())
                        .buffer_size(buffer_size);

                    if let Some(ref config) = tls_config {
                        builder = builder.tls_config(config.clone());
                    }

                    match builder.build() {
                        Ok(mut new_client) => {
                            match new_client.ensure_connected().await {
                                Ok(_) => {
                                    available_connections.push(Arc::new(Mutex::new(new_client)));
                                    tracing::debug!("Created new connection, {} available", available_connections.len());
                                }
                                Err(e) => {
                                    tracing::error!("Failed to establish new connection: {}", e);
                                }
                            }
                        }
                        Err(e) => {
                            tracing::error!("Failed to create new connection: {}", e);
                        }
                    }
                }

                else => {
                    tracing::info!("Connection pool shutting down");
                    break;
                }
            }
        }

        Ok(())
    }
}

// A wrapper that automatically returns connections to the pool
pub struct PooledConnection {
    conn: Option<Arc<Mutex<S3Client>>>,
    return_sender: mpsc::Sender<Arc<Mutex<S3Client>>>,
}

impl PooledConnection {
    pub fn new(
        conn: Arc<Mutex<S3Client>>,
        return_sender: mpsc::Sender<Arc<Mutex<S3Client>>>,
    ) -> Self {
        Self {
            conn: Some(conn),
            return_sender,
        }
    }

    pub async fn fetch_batch(
        &self,
        keys: &[String],
        credentials: &AwsCredentials,
        max_retries: usize,
    ) -> Result<Vec<(String, Bytes)>> {
        let conn = self
            .conn
            .as_ref()
            .ok_or_else(|| TurbofetchError::Http("Connection already returned".to_string()))?;

        let mut client = conn.lock().await;
        client.fetch_batch(keys, credentials, max_retries).await
    }
}

impl Drop for PooledConnection {
    fn drop(&mut self) {
        if let Some(conn) = self.conn.take() {
            let return_sender = self.return_sender.clone();
            tokio::spawn(async move {
                if let Err(e) = return_sender.send(conn).await {
                    tracing::warn!("Failed to return connection to pool: {}", e);
                }
            });
        }
    }
}
