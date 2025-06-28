use parquet::file::reader::{FileReader, SerializedFileReader};
use parquet::record::RowAccessor;
use std::fs::File;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use tokio::sync::{mpsc, Semaphore};
use tokio::task::JoinHandle;
use tracing::{debug, info};

use crate::{NarinfoBatch, Result, TurbofetchError};

/// Configuration for parallel parquet reading
pub struct ParquetReaderConfig {
    pub batch_size: usize,
    pub max_parallel_files: usize,
}

impl Default for ParquetReaderConfig {
    fn default() -> Self {
        Self {
            batch_size: 50,
            max_parallel_files: 4,
        }
    }
}

/// Stats for tracking progress
#[derive(Default)]
pub struct ParquetReaderStats {
    pub total_records: AtomicUsize,
    pub narinfo_count: AtomicUsize,
    pub skipped_count: AtomicUsize,
    pub files_processed: AtomicUsize,
}

/// Result of counting parquet records
#[derive(Debug)]
pub struct ParquetCountResult {
    pub total_records: usize,
    pub narinfo_count: usize,
    pub file_count: usize,
}

/// Reads parquet files from a directory and sends batches of narinfo keys
pub async fn parquet_reader(
    parquet_dir: &Path,
    batch_sender: mpsc::Sender<NarinfoBatch>,
    config: ParquetReaderConfig,
    store: Arc<dyn crate::narinfo_store::NarinfoStore>,
    progress: Arc<crate::ProgressTracker>,
) -> Result<()> {
    info!("Reading parquet files from directory: {:?}", parquet_dir);

    // Find all parquet files in the directory
    let parquet_files = find_parquet_files(parquet_dir)?;
    if parquet_files.is_empty() {
        return Err(TurbofetchError::Io(std::io::Error::new(
            std::io::ErrorKind::NotFound,
            format!("No parquet files found in directory: {:?}", parquet_dir),
        )));
    }

    info!(
        "Found {} parquet files to process with {} parallel readers",
        parquet_files.len(),
        config.max_parallel_files
    );

    let stats = Arc::new(ParquetReaderStats::default());
    let batch_id_counter = Arc::new(AtomicUsize::new(0));
    let semaphore = Arc::new(Semaphore::new(config.max_parallel_files));

    // Create a channel for collecting batches from workers
    let (worker_tx, mut worker_rx) = mpsc::channel::<NarinfoBatch>(config.max_parallel_files * 2);

    // Spawn a task to forward batches from workers to the main batch_sender
    let batch_forwarder = tokio::spawn(async move {
        while let Some(batch) = worker_rx.recv().await {
            if batch_sender.send(batch).await.is_err() {
                break;
            }
        }
    });

    // Process files in parallel
    let mut handles: Vec<JoinHandle<Result<()>>> = Vec::new();

    for (file_idx, parquet_file) in parquet_files.into_iter().enumerate() {
        let permit = semaphore.clone().acquire_owned().await.unwrap();
        let worker_tx = worker_tx.clone();
        let store = store.clone();
        let progress = progress.clone();
        let stats = stats.clone();
        let batch_id_counter = batch_id_counter.clone();
        let batch_size = config.batch_size;

        let handle = tokio::spawn(async move {
            let _permit = permit;
            process_parquet_file(
                file_idx,
                parquet_file,
                worker_tx,
                batch_size,
                store,
                progress,
                stats,
                batch_id_counter,
            )
            .await
        });

        handles.push(handle);
    }

    // Wait for all workers to complete
    for handle in handles {
        handle.await.map_err(|e| {
            TurbofetchError::Io(std::io::Error::new(
                std::io::ErrorKind::Other,
                format!("Worker task panicked: {}", e),
            ))
        })??;
    }

    // Close the worker channel
    drop(worker_tx);

    // Wait for the batch forwarder to complete
    batch_forwarder.await.map_err(|e| {
        TurbofetchError::Io(std::io::Error::new(
            std::io::ErrorKind::Other,
            format!("Batch forwarder panicked: {}", e),
        ))
    })?;

    let narinfo_count = stats.narinfo_count.load(Ordering::Relaxed);
    let skipped_count = stats.skipped_count.load(Ordering::Relaxed);

    info!(
        "Parallel parquet reader completed - processed {} files, {} total records, found {} narinfo files, skipped {} already downloaded",
        stats.files_processed.load(Ordering::Relaxed),
        stats.total_records.load(Ordering::Relaxed),
        narinfo_count,
        skipped_count
    );

    Ok(())
}

/// Process a single parquet file
#[allow(clippy::too_many_arguments)]
async fn process_parquet_file(
    file_idx: usize,
    parquet_file: PathBuf,
    batch_sender: mpsc::Sender<NarinfoBatch>,
    batch_size: usize,
    store: Arc<dyn crate::narinfo_store::NarinfoStore>,
    progress: Arc<crate::ProgressTracker>,
    stats: Arc<ParquetReaderStats>,
    batch_id_counter: Arc<AtomicUsize>,
) -> Result<()> {
    info!(
        "Worker {} processing parquet file: {}",
        file_idx,
        parquet_file.display()
    );

    // Use blocking task for file I/O
    let reader = tokio::task::spawn_blocking(move || -> Result<SerializedFileReader<File>> {
        let file = File::open(&parquet_file).map_err(TurbofetchError::Io)?;
        let reader = SerializedFileReader::new(file).map_err(|e| {
            TurbofetchError::Io(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                format!("Failed to read parquet file: {}", e),
            ))
        })?;
        Ok(reader)
    })
    .await
    .map_err(|e| {
        TurbofetchError::Io(std::io::Error::new(
            std::io::ErrorKind::Other,
            format!("Blocking task panicked: {}", e),
        ))
    })??;

    let mut batch = Vec::with_capacity(batch_size);
    let mut local_records = 0usize;
    let mut local_narinfo = 0usize;

    // Process rows in blocking context
    let row_iter = reader.get_row_iter(None).map_err(|e| {
        TurbofetchError::Io(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            format!("Failed to create row iterator: {}", e),
        ))
    })?;

    for row_result in row_iter {
        let row = row_result.map_err(|e| {
            TurbofetchError::Io(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                format!("Failed to read row: {}", e),
            ))
        })?;

        local_records += 1;

        // Extract the key field
        if let Ok(key) = row.get_string(1) {
            let key_str = key.to_string();

            // Check if this is a narinfo file
            if key_str.ends_with(".narinfo") {
                if let Some(hash) = extract_narinfo_hash(&key_str) {
                    local_narinfo += 1;

                    // Check if narinfo already exists
                    match store.exists(&hash).await {
                        Ok(true) => {
                            stats.skipped_count.fetch_add(1, Ordering::Relaxed);
                            progress.increment_skipped(1);
                            continue;
                        }
                        Ok(false) => {
                            batch.push(hash);
                        }
                        Err(e) => {
                            return Err(TurbofetchError::Io(std::io::Error::new(
                                std::io::ErrorKind::Other,
                                format!("Failed to check if narinfo exists: {}", e),
                            )));
                        }
                    }
                }
            }
        }

        // Send batch when full
        if batch.len() == batch_size {
            let batch_id = batch_id_counter.fetch_add(1, Ordering::Relaxed);
            let narinfo_batch = NarinfoBatch::new(batch.clone(), batch_id);

            if batch_sender.send(narinfo_batch).await.is_err() {
                return Err(TurbofetchError::Io(std::io::Error::new(
                    std::io::ErrorKind::BrokenPipe,
                    "Batch channel closed",
                )));
            }

            batch.clear();
        }

        // Update progress periodically
        if local_records % 10000 == 0 {
            debug!(
                "Worker {} processed {} records, found {} narinfo files",
                file_idx, local_records, local_narinfo
            );
        }
    }

    // Send remaining batch
    if !batch.is_empty() {
        let batch_id = batch_id_counter.fetch_add(1, Ordering::Relaxed);
        let narinfo_batch = NarinfoBatch::new(batch, batch_id);

        if batch_sender.send(narinfo_batch).await.is_err() {
            return Err(TurbofetchError::Io(std::io::Error::new(
                std::io::ErrorKind::BrokenPipe,
                "Batch channel closed",
            )));
        }
    }

    // Update stats
    stats
        .total_records
        .fetch_add(local_records, Ordering::Relaxed);
    stats
        .narinfo_count
        .fetch_add(local_narinfo, Ordering::Relaxed);
    stats.files_processed.fetch_add(1, Ordering::Relaxed);

    info!(
        "Worker {} completed processing file with {} records, {} narinfo files",
        file_idx, local_records, local_narinfo
    );

    Ok(())
}

/// Find all parquet files in a directory
fn find_parquet_files(dir: &Path) -> Result<Vec<PathBuf>> {
    let mut parquet_files = Vec::new();

    let entries = std::fs::read_dir(dir).map_err(TurbofetchError::Io)?;

    for entry in entries {
        let entry = entry.map_err(TurbofetchError::Io)?;
        let path = entry.path();

        if path.is_file() {
            if let Some(extension) = path.extension() {
                if extension == "parquet" {
                    parquet_files.push(path);
                }
            }
        }
    }

    parquet_files.sort();
    Ok(parquet_files)
}

/// Extract narinfo hash from S3 key
/// Example: "nar/abc123def456.narinfo" -> "abc123def456"
fn extract_narinfo_hash(key: &str) -> Option<String> {
    if let Some(filename) = key.split('/').next_back() {
        if filename.ends_with(".narinfo") {
            return Some(filename.trim_end_matches(".narinfo").to_string());
        }
    }
    None
}

/// Count total records and narinfo files in all parquet files
pub async fn count_parquet_records(parquet_dir: &Path) -> Result<ParquetCountResult> {
    info!("Analyzing parquet files in directory: {:?}", parquet_dir);

    // Find all parquet files in the directory
    let parquet_files = find_parquet_files(parquet_dir)?;
    if parquet_files.is_empty() {
        return Err(TurbofetchError::Io(std::io::Error::new(
            std::io::ErrorKind::NotFound,
            format!("No parquet files found in directory: {:?}", parquet_dir),
        )));
    }

    let file_count = parquet_files.len();
    info!("Found {} parquet files to analyze", file_count);

    let total_records = Arc::new(AtomicUsize::new(0));
    let narinfo_count = Arc::new(AtomicUsize::new(0));
    let semaphore = Arc::new(Semaphore::new(4)); // Limit concurrent file reads

    let mut handles = Vec::new();

    for (idx, parquet_file) in parquet_files.into_iter().enumerate() {
        let permit = semaphore.clone().acquire_owned().await.unwrap();
        let total_records = total_records.clone();
        let narinfo_count = narinfo_count.clone();

        let handle = tokio::spawn(async move {
            let _permit = permit;

            // Use blocking task for file I/O
            let result = tokio::task::spawn_blocking(move || -> Result<(usize, usize)> {
                let file = File::open(&parquet_file).map_err(TurbofetchError::Io)?;
                let reader = SerializedFileReader::new(file).map_err(|e| {
                    TurbofetchError::Io(std::io::Error::new(
                        std::io::ErrorKind::InvalidData,
                        format!("Failed to read parquet file: {}", e),
                    ))
                })?;

                let mut local_records = 0usize;
                let mut local_narinfo = 0usize;

                // Get row iterator
                let row_iter = reader.get_row_iter(None).map_err(|e| {
                    TurbofetchError::Io(std::io::Error::new(
                        std::io::ErrorKind::InvalidData,
                        format!("Failed to create row iterator: {}", e),
                    ))
                })?;

                for row_result in row_iter {
                    let row = row_result.map_err(|e| {
                        TurbofetchError::Io(std::io::Error::new(
                            std::io::ErrorKind::InvalidData,
                            format!("Failed to read row: {}", e),
                        ))
                    })?;

                    local_records += 1;

                    // Extract the key field
                    if let Ok(key) = row.get_string(1) {
                        let key_str = key.to_string();
                        if key_str.ends_with(".narinfo") {
                            local_narinfo += 1;
                        }
                    }
                }

                debug!(
                    "File {} contains {} records ({} narinfo files)",
                    idx, local_records, local_narinfo
                );

                Ok((local_records, local_narinfo))
            })
            .await
            .map_err(|e| {
                TurbofetchError::Io(std::io::Error::new(
                    std::io::ErrorKind::Other,
                    format!("Blocking task panicked: {}", e),
                ))
            })??;

            total_records.fetch_add(result.0, Ordering::Relaxed);
            narinfo_count.fetch_add(result.1, Ordering::Relaxed);

            Ok::<(), TurbofetchError>(())
        });

        handles.push(handle);
    }

    // Wait for all counting tasks to complete
    for handle in handles {
        handle.await.map_err(|e| {
            TurbofetchError::Io(std::io::Error::new(
                std::io::ErrorKind::Other,
                format!("Counting task panicked: {}", e),
            ))
        })??;
    }

    let result = ParquetCountResult {
        total_records: total_records.load(Ordering::Relaxed),
        narinfo_count: narinfo_count.load(Ordering::Relaxed),
        file_count,
    };

    info!(
        "Analysis complete: {} files, {} total records, {} narinfo files",
        result.file_count, result.total_records, result.narinfo_count
    );

    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::narinfo_store::{DiskNarinfoStore, NarinfoStore};
    use crate::progress::ProgressTracker;
    use tempfile::TempDir;

    #[tokio::test]
    async fn test_parquet_reader() {
        let temp_dir = TempDir::new().unwrap();
        let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;
        let progress = Arc::new(ProgressTracker::new(8)); // 5 + 3 narinfo files expected

        // Copy test fixtures to temp directory
        let test_dir = TempDir::new().unwrap();
        let fixtures_dir = std::path::Path::new("tests/fixtures");

        std::fs::copy(
            fixtures_dir.join("small-batch.parquet"),
            test_dir.path().join("file1.parquet"),
        )
        .unwrap();

        std::fs::copy(
            fixtures_dir.join("mixed-content.parquet"),
            test_dir.path().join("file2.parquet"),
        )
        .unwrap();

        let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

        let config = ParquetReaderConfig {
            batch_size: 3,
            max_parallel_files: 2,
        };

        let reader_handle = tokio::spawn({
            let parquet_path = test_dir.path().to_path_buf();
            let store_clone = Arc::clone(&store);
            let progress_clone = Arc::clone(&progress);
            async move { parquet_reader(&parquet_path, tx, config, store_clone, progress_clone).await }
        });

        // Collect all batches
        let mut all_keys = Vec::new();
        while let Some(batch) = rx.recv().await {
            all_keys.extend(batch.keys);
        }

        reader_handle.await.unwrap().unwrap();

        // Should have 5 from small-batch + 3 from mixed-content = 8 total
        assert_eq!(all_keys.len(), 8);
    }
    
    #[tokio::test]
    async fn test_count_parquet_records() {
        // Test counting with single file
        let test_dir = TempDir::new().unwrap();
        let fixtures_dir = std::path::Path::new("tests/fixtures");
        
        std::fs::copy(
            fixtures_dir.join("small-batch.parquet"),
            test_dir.path().join("small-batch.parquet"),
        )
        .unwrap();
        
        let count_result = count_parquet_records(test_dir.path()).await.unwrap();
        assert_eq!(count_result.file_count, 1);
        assert_eq!(count_result.narinfo_count, 5);
        assert_eq!(count_result.total_records, 5); // All records in small-batch are narinfo
        
        // Test counting with multiple files
        let test_dir2 = TempDir::new().unwrap();
        
        std::fs::copy(
            fixtures_dir.join("small-batch.parquet"),
            test_dir2.path().join("file1.parquet"),
        )
        .unwrap();
        
        std::fs::copy(
            fixtures_dir.join("mixed-content.parquet"),
            test_dir2.path().join("file2.parquet"),
        )
        .unwrap();
        
        let count_result2 = count_parquet_records(test_dir2.path()).await.unwrap();
        assert_eq!(count_result2.file_count, 2);
        assert_eq!(count_result2.narinfo_count, 8); // 5 + 3
        assert_eq!(count_result2.total_records, 11); // 5 + 6 (mixed has 6 total)
    }
}
