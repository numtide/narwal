use crate::{
    buffer::Buffer,
    error::{Result, TurbofetchError},
    signature::{create_signed_request, AwsCredentials},
};
use bytes::Bytes;
use rustls_pki_types::ServerName;
use std::sync::Arc;
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWriteExt},
    net::TcpStream,
};
use tokio_rustls::client::TlsStream;

/// A persistent HTTP client for S3 operations
pub struct S3Client {
    connection: Option<Connection>,
    config: Arc<rustls::ClientConfig>,
    hostname: String,
    buffer_size: usize,
}

/// Builder for S3Client
pub struct S3ClientBuilder {
    hostname: Option<String>,
    buffer_size: Option<usize>,
    tls_config: Option<Arc<rustls::ClientConfig>>,
}

impl S3ClientBuilder {
    pub fn new() -> Self {
        Self {
            hostname: None,
            buffer_size: None,
            tls_config: None,
        }
    }

    pub fn hostname(mut self, hostname: String) -> Self {
        self.hostname = Some(hostname);
        self
    }

    pub fn buffer_size(mut self, size: usize) -> Self {
        self.buffer_size = Some(size);
        self
    }

    pub fn tls_config(mut self, config: Arc<rustls::ClientConfig>) -> Self {
        self.tls_config = Some(config);
        self
    }

    pub fn build(self) -> Result<S3Client> {
        let hostname = self
            .hostname
            .ok_or_else(|| TurbofetchError::Config("hostname is required".to_string()))?;

        let buffer_size = self.buffer_size.unwrap_or(512 * 1024);

        let config = self.tls_config.unwrap_or_else(|| {
            let mut root_cert_store = rustls::RootCertStore::empty();
            root_cert_store.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());

            let mut config = rustls::ClientConfig::builder()
                .with_root_certificates(root_cert_store)
                .with_no_client_auth();

            config.resumption = rustls::client::Resumption::default();
            Arc::new(config)
        });

        Ok(S3Client {
            connection: None,
            config,
            hostname,
            buffer_size,
        })
    }
}

impl Default for S3ClientBuilder {
    fn default() -> Self {
        Self::new()
    }
}

struct Connection {
    read: tokio::io::ReadHalf<TlsStream<TcpStream>>,
    write: tokio::io::WriteHalf<TlsStream<TcpStream>>,
    buffer: Buffer,
}

impl S3Client {
    pub fn builder() -> S3ClientBuilder {
        S3ClientBuilder::new()
    }

    pub fn new(hostname: String, buffer_size: usize) -> Result<Self> {
        Self::builder()
            .hostname(hostname)
            .buffer_size(buffer_size)
            .build()
    }

    pub async fn ensure_connected(&mut self) -> Result<()> {
        if self.connection.is_none() {
            self.connect().await?;
        }
        Ok(())
    }

    async fn connect(&mut self) -> Result<()> {
        tracing::debug!("Establishing TLS connection to {}", self.hostname);

        let stream = TcpStream::connect(format!("{}:443", self.hostname))
            .await
            .map_err(TurbofetchError::from)?;

        let domain = ServerName::try_from(self.hostname.clone())
            .map_err(|e| TurbofetchError::Parse(e.to_string()))?
            .to_owned();

        let connector = tokio_rustls::TlsConnector::from(self.config.clone());
        let tls_stream = connector
            .connect(domain, stream)
            .await
            .map_err(|e| TurbofetchError::Http(format!("TLS connection failed: {}", e)))?;

        let (read, write) = tokio::io::split(tls_stream);

        self.connection = Some(Connection {
            read,
            write,
            buffer: Buffer::new(self.buffer_size),
        });

        tracing::debug!("Successfully established TLS connection");
        Ok(())
    }

    pub fn disconnect(&mut self) {
        self.connection = None;
        tracing::debug!("Disconnected from server");
    }

    pub async fn fetch_batch(
        &mut self,
        keys: &[String],
        credentials: &AwsCredentials,
        max_retries: usize,
    ) -> Result<Vec<(String, Bytes)>> {
        let mut retry_count = 0;

        loop {
            match self.try_fetch_batch(keys, credentials).await {
                Ok(results) => return Ok(results),
                Err(e) => {
                    if retry_count >= max_retries {
                        return Err(e);
                    }

                    tracing::warn!(
                        "Fetch failed (attempt {}/{}): {}",
                        retry_count + 1,
                        max_retries,
                        e
                    );

                    self.disconnect();
                    retry_count += 1;
                }
            }
        }
    }

    async fn try_fetch_batch(
        &mut self,
        keys: &[String],
        credentials: &AwsCredentials,
    ) -> Result<Vec<(String, Bytes)>> {
        self.ensure_connected().await?;

        let conn = self.connection.as_mut().unwrap();

        // Build all requests
        let mut request_parts = Vec::new();
        for key in keys {
            let path = format!("/{}.narinfo", key);
            let signed_request = create_signed_request(&path, credentials, &self.hostname)
                .map_err(|e| TurbofetchError::Aws(format!("Failed to sign request: {}", e)))?;
            request_parts.push(signed_request);
        }

        let request = request_parts.join("").into_bytes();

        // Send all requests at once
        conn.write
            .write_all(&request)
            .await
            .map_err(TurbofetchError::from)?;

        // Read all responses
        let mut results = Vec::new();
        for key in keys {
            match Self::parse_response(&mut conn.read, &mut conn.buffer).await {
                Ok(content) => {
                    results.push((key.clone(), content));
                }
                Err(e) => {
                    tracing::error!("Failed to fetch {}: {}", key, e);
                    return Err(e);
                }
            }
        }

        Ok(results)
    }

    async fn parse_response(
        reader: &mut tokio::io::ReadHalf<TlsStream<TcpStream>>,
        buffer: &mut Buffer,
    ) -> Result<Bytes> {
        use std::mem::MaybeUninit;

        let body_len = loop {
            let mut headers = [MaybeUninit::uninit(); 16];
            let mut response = httparse::Response::new(&mut []);
            let status = httparse::ParserConfig::default()
                .parse_response_with_uninit_headers(&mut response, buffer.data(), &mut headers)
                .map_err(|e| TurbofetchError::Parse(e.to_string()))?;

            if let httparse::Status::Complete(n) = status {
                buffer.consume(n);

                let code = response.code.unwrap();
                if code != 200 {
                    let headers_str = response
                        .headers
                        .iter()
                        .map(|h| format!("{}: {}", h.name, String::from_utf8_lossy(h.value)))
                        .collect::<Vec<_>>()
                        .join(", ");

                    let mut error_body = String::new();
                    if let Ok(remaining_data) = std::str::from_utf8(buffer.data()) {
                        error_body = remaining_data.chars().take(500).collect();
                    }

                    return Err(TurbofetchError::Http(format!(
                        "HTTP response {} - Headers: {} - Body preview: {}",
                        code, headers_str, error_body
                    )));
                }

                break Self::get_content_length(response.headers)?;
            }

            Self::slurp(buffer, reader).await?;
        };

        let buf_len = buffer.space().len() + buffer.data().len();

        if body_len > buf_len as u64 {
            return Err(TurbofetchError::Http(
                "HTTP response body does not fit in buffer".to_string(),
            ));
        }

        let body_len = body_len as usize;

        while buffer.data().len() < body_len {
            Self::slurp(buffer, reader).await?;
        }

        let data = buffer.data();
        let body = Bytes::copy_from_slice(&data[..body_len]);
        buffer.consume(body_len);

        Ok(body)
    }

    fn get_content_length(headers: &[httparse::Header]) -> Result<u64> {
        for header in headers {
            if header.name == "Transfer-Encoding" {
                return Err(TurbofetchError::Http(
                    "Transfer-Encoding is unsupported".to_string(),
                ));
            }

            if header.name == "Content-Length" {
                return std::str::from_utf8(header.value)
                    .ok()
                    .and_then(|v| v.parse().ok())
                    .ok_or_else(|| TurbofetchError::Parse("invalid Content-Length".to_string()));
            }
        }

        Err(TurbofetchError::Http("Content-Length missing".to_string()))
    }

    async fn slurp(buffer: &mut Buffer, sock: &mut (impl AsyncRead + Unpin)) -> Result<()> {
        match buffer.space() {
            [] => Err(TurbofetchError::Http("buffer filled".to_string())),
            buf => {
                let n = sock.read(buf).await?;
                if n == 0 {
                    return Err(TurbofetchError::Http("unexpected EOF".to_string()));
                }
                buffer.commit(n);
                Ok(())
            }
        }
    }
}
