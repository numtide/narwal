use bytes::Bytes;
use std::path::Path;
use tokio::{
    fs as tokio_fs,
    io::{self, AsyncBufReadExt, BufReader},
    sync::mpsc,
};

pub use buffer::Buffer;
mod buffer;

pub mod config;
pub mod connection_pool;
pub mod constants;
pub mod error;
pub mod http_client;
pub mod narinfo_store;
pub mod object_store;
pub mod pipeline;
pub mod progress;
pub mod signature;

// Re-export for testing and main binary
pub use config::Config;
pub use error::{Result, TurbofetchError};
pub use pipeline::Pipeline;
pub use progress::ProgressTracker;
pub use signature::AwsCredentials;

#[derive(Debug, Clone)]
pub struct NarinfoBatch {
    pub keys: Vec<String>,
    pub batch_id: usize,
}

impl NarinfoBatch {
    /// Create a new NarinfoBatch
    pub fn new(keys: Vec<String>, batch_id: usize) -> Self {
        Self { keys, batch_id }
    }
}

#[derive(Debug)]
pub struct FetchedBatch {
    pub results: Vec<(String, Bytes)>,
    pub batch_id: usize,
}

// HTTP-related utility functions moved to http_client.rs

/// HTTP processor that uses a connection pool
pub async fn http_processor_pooled(
    batch_receiver: std::sync::Arc<tokio::sync::Mutex<mpsc::Receiver<NarinfoBatch>>>,
    result_sender: mpsc::Sender<FetchedBatch>,
    credentials: &AwsCredentials,
    connection_pool: std::sync::Arc<crate::connection_pool::ConnectionPool>,
    worker_id: usize,
    progress: std::sync::Arc<ProgressTracker>,
) -> io::Result<()> {
    loop {
        // Try to receive a batch from the shared receiver
        let batch = {
            let mut receiver = batch_receiver.lock().await;
            receiver.recv().await
        };

        match batch {
            Some(batch) => {
                tracing::debug!(
                    "HTTP processor {} received batch {}",
                    worker_id,
                    batch.batch_id
                );
                let batch_size = batch.keys.len();

                // Update progress - starting batch
                progress.start_batch(worker_id, batch.batch_id, batch_size);

                // Get a connection from the pool
                let conn = connection_pool
                    .get_connection()
                    .await
                    .map_err(|e| io::Error::new(io::ErrorKind::Other, e.to_string()))?;

                // Use the pooled connection to fetch the batch
                match conn
                    .fetch_batch(
                        &batch.keys,
                        credentials,
                        crate::constants::DEFAULT_MAX_RETRIES,
                    )
                    .await
                {
                    Ok(results) => {
                        let downloaded_count = results.len();
                        let fetched_batch = FetchedBatch {
                            results,
                            batch_id: batch.batch_id,
                        };

                        if result_sender.send(fetched_batch).await.is_err() {
                            return Err(io::Error::new(
                                io::ErrorKind::BrokenPipe,
                                "Disk writer channel closed",
                            ));
                        }

                        // Update progress - batch completed
                        progress
                            .complete_batch(worker_id, batch.batch_id, batch_size, downloaded_count)
                            .await;

                        tracing::debug!(
                            "HTTP processor {} completed batch {}",
                            worker_id,
                            batch.batch_id
                        );
                    }
                    Err(e) => {
                        tracing::error!(
                            "Worker {} failed to fetch batch {} after retries: {}",
                            worker_id,
                            batch.batch_id,
                            e
                        );
                        return Err(io::Error::new(io::ErrorKind::Other, e.to_string()));
                    }
                }
                // Connection is automatically returned to pool when conn is dropped
            }
            None => {
                // Channel is closed, no more batches to process
                tracing::info!("HTTP processor {} completed - no more batches", worker_id);
                break;
            }
        }
    }

    Ok(())
}

/// Disk writer: receives fetched batches and writes them to disk
pub async fn disk_writer(
    mut result_receiver: mpsc::Receiver<FetchedBatch>,
    store: std::sync::Arc<dyn crate::narinfo_store::NarinfoStore>,
    progress: std::sync::Arc<ProgressTracker>,
) -> io::Result<usize> {
    let mut written_count = 0;

    while let Some(batch) = result_receiver.recv().await {
        tracing::debug!(
            "Disk writer received batch {} with {} results",
            batch.batch_id,
            batch.results.len()
        );

        for (hash, content) in batch.results {
            let content_size = content.len() as u64;

            store
                .write(&hash, &content)
                .await
                .map_err(|e| io::Error::new(io::ErrorKind::Other, e.to_string()))?;

            written_count += 1;

            // Update progress with bytes downloaded
            progress.add_bytes_downloaded(content_size);
            progress.increment_downloaded(1);
        }

        tracing::debug!("Disk writer completed batch {}", batch.batch_id);
    }

    tracing::info!(
        "Disk writer completed, total files written: {}",
        written_count
    );
    Ok(written_count)
}

// These functions have been replaced by the S3Client in http_client.rs

/// Job reader: reads the job file and sends batches to the HTTP processors
pub async fn job_reader(
    job_file: &Path,
    batch_sender: mpsc::Sender<NarinfoBatch>,
    batch_size: usize,
    store: std::sync::Arc<dyn crate::narinfo_store::NarinfoStore>,
    progress: std::sync::Arc<ProgressTracker>,
) -> io::Result<()> {
    let metadata = tokio_fs::metadata(job_file).await?;
    let total_size = metadata.len();
    tracing::info!(
        "Reading job file of {} bytes ({:.2} GB)",
        total_size,
        total_size as f64 / 1_000_000_000.0
    );

    let file = tokio_fs::File::open(job_file).await?;
    let reader = BufReader::new(file);
    let mut lines = reader.lines();

    let mut batch = Vec::with_capacity(batch_size);
    let mut batch_id = 0;
    let mut skipped_count = 0usize;
    let mut total_lines = 0usize;

    while let Some(line) = lines.next_line().await? {
        let key = line.trim().to_string();
        if !key.is_empty() {
            total_lines += 1;

            // Check if narinfo already exists
            match store.exists(&key).await {
                Ok(true) => {
                    skipped_count += 1;
                    progress.increment_skipped(1);
                    continue;
                }
                Ok(false) => {
                    batch.push(key);
                }
                Err(e) => {
                    return Err(io::Error::new(
                        io::ErrorKind::Other,
                        format!("Failed to check if narinfo exists: {}", e),
                    ));
                }
            }
        }

        if batch.len() == batch_size {
            let narinfo_batch = NarinfoBatch::new(batch.clone(), batch_id);

            if batch_sender.send(narinfo_batch).await.is_err() {
                return Err(io::Error::new(
                    io::ErrorKind::BrokenPipe,
                    "HTTP processor channel closed",
                ));
            }

            batch.clear();
            batch_id += 1;
        }
    }

    // Process remaining keys
    if !batch.is_empty() {
        let narinfo_batch = NarinfoBatch::new(batch, batch_id);

        if batch_sender.send(narinfo_batch).await.is_err() {
            return Err(io::Error::new(
                io::ErrorKind::BrokenPipe,
                "HTTP processor channel closed",
            ));
        }
    }

    // Update total items with actual count
    progress.update_total_items(total_lines);

    tracing::info!(
        "Job reader completed - read {} batches, skipped {} already downloaded files",
        batch_id + 1,
        skipped_count
    );
    Ok(())
}
