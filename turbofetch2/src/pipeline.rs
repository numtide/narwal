use crate::{
    config::Config,
    connection_pool::ConnectionPool,
    constants::{BATCH_CHANNEL_SIZE, DEFAULT_BUFFER_SIZE, RESULT_CHANNEL_SIZE},
    error::Result,
    narinfo_store::{DiskNarinfoStore, NarinfoStore},
    parquet_reader::{count_parquet_records, parquet_reader, ParquetReaderConfig},
    progress::ProgressTracker,
    signature::AwsCredentials,
    {disk_writer, http_processor_pooled, FetchedBatch, NarinfoBatch},
};
use std::sync::Arc;
use tokio::sync::mpsc;

pub struct Pipeline {
    config: Config,
    credentials: AwsCredentials,
}

impl Pipeline {
    pub fn new(config: Config, credentials: AwsCredentials) -> Self {
        Self {
            config,
            credentials,
        }
    }

    pub async fn process_parquet_files(&self) -> Result<usize> {
        // First, count the records in all parquet files
        let count_result = count_parquet_records(&self.config.parquet_dir).await?;
        
        // Create progress tracker with accurate total count
        let progress = Arc::new(ProgressTracker::new(count_result.narinfo_count as u64));

        // Create narinfo store
        let store =
            Arc::new(DiskNarinfoStore::new(&self.config.output_dir)) as Arc<dyn NarinfoStore>;

        // Create channels with appropriate buffer sizes
        let (batch_sender, batch_receiver) = mpsc::channel::<NarinfoBatch>(BATCH_CHANNEL_SIZE);
        let (result_sender, result_receiver) = mpsc::channel::<FetchedBatch>(RESULT_CHANNEL_SIZE);

        // Create connection pool
        let connection_pool = Arc::new(ConnectionPool::new(
            self.config.hostname.clone(),
            DEFAULT_BUFFER_SIZE,
            self.config.num_workers * 2,
            None,
        )?);

        // Spawn reader task
        let reader_handle = {
            let parquet_dir = self.config.parquet_dir.clone();
            let batch_size = self.config.batch_size;
            let store_clone = Arc::clone(&store);
            let progress_clone = Arc::clone(&progress);
            let parallel_readers = self.config.max_parallel_files.max(1); // Ensure at least 1 reader

            tokio::spawn(async move {
                let reader_config = ParquetReaderConfig {
                    batch_size,
                    max_parallel_files: parallel_readers,
                };

                parquet_reader(
                    &parquet_dir,
                    batch_sender,
                    reader_config,
                    store_clone,
                    progress_clone,
                )
                .await
            })
        };

        // Spawn HTTP processor tasks
        let mut processor_handles = Vec::new();
        let batch_receiver = std::sync::Arc::new(tokio::sync::Mutex::new(batch_receiver));

        for i in 0..self.config.num_workers {
            let batch_receiver_clone = batch_receiver.clone();
            let result_sender = result_sender.clone();
            let credentials = self.credentials.clone();
            let connection_pool_clone = connection_pool.clone();
            let progress_clone = Arc::clone(&progress);

            processor_handles.push(tokio::spawn(async move {
                http_processor_pooled(
                    batch_receiver_clone,
                    result_sender,
                    &credentials,
                    connection_pool_clone,
                    i,
                    progress_clone,
                )
                .await
            }));
        }

        // Drop the original sender to ensure proper channel closure
        drop(result_sender);

        // Spawn disk writer task
        let writer_handle = {
            let progress_clone = Arc::clone(&progress);
            tokio::spawn(async move { disk_writer(result_receiver, store, progress_clone).await })
        };

        // Wait for reader to finish
        reader_handle
            .await
            .map_err(|e| format!("Reader task panicked: {}", e))??;

        // Wait for all HTTP processors to finish
        for (i, handle) in processor_handles.into_iter().enumerate() {
            handle
                .await
                .map_err(|e| format!("HTTP processor {} panicked: {}", i, e))??;
        }

        // Wait for disk writer to finish and get the count
        let written_count = writer_handle
            .await
            .map_err(|e| format!("Disk writer task panicked: {}", e))??;

        // Finish the progress display
        progress.finish();

        Ok(written_count)
    }
}
