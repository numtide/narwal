use bytes::Bytes;
use std::io::Write;
use std::sync::Arc;
use tempfile::{NamedTempFile, TempDir};
use tokio::sync::mpsc;
use turbofetch2::narinfo_store::{DiskNarinfoStore, NarinfoStore};
use turbofetch2::progress::ProgressTracker;
use turbofetch2::{job_reader, NarinfoBatch};

#[tokio::test]
async fn test_job_reader_skips_existing_files() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;

    // Create some existing narinfo files
    let existing_hashes = vec!["existing1234567890abcdef", "existing9876543210fedcba"];

    for hash in &existing_hashes {
        store
            .write(hash, &Bytes::from("existing content"))
            .await
            .unwrap();
    }

    // Create job file with both existing and new hashes
    let mut job_file = NamedTempFile::new().unwrap();
    writeln!(job_file, "existing1234567890abcdef").unwrap();
    writeln!(job_file, "newfile1234567890abcdef").unwrap();
    writeln!(job_file, "existing9876543210fedcba").unwrap();
    writeln!(job_file, "newfile9876543210fedcba").unwrap();
    writeln!(job_file, "anothernew123456789abc").unwrap();
    job_file.flush().unwrap();

    // Set up channel
    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

    // Run job reader
    let reader_handle = tokio::spawn({
        let job_path = job_file.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress = Arc::new(ProgressTracker::new(100)); // Small file size for test
        async move { job_reader(&job_path, tx, 3, store_clone, progress).await }
    });

    // Collect all batches
    let mut all_keys = Vec::new();
    while let Some(batch) = rx.recv().await {
        all_keys.extend(batch.keys);
    }

    // Wait for reader to complete
    reader_handle.await.unwrap().unwrap();

    // Verify only new files were sent
    assert_eq!(all_keys.len(), 3);
    assert!(all_keys.contains(&"newfile1234567890abcdef".to_string()));
    assert!(all_keys.contains(&"newfile9876543210fedcba".to_string()));
    assert!(all_keys.contains(&"anothernew123456789abc".to_string()));

    // Verify existing files were not sent
    assert!(!all_keys.contains(&"existing1234567890abcdef".to_string()));
    assert!(!all_keys.contains(&"existing9876543210fedcba".to_string()));
}

#[tokio::test]
async fn test_job_reader_empty_lines_and_whitespace() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;

    // Create job file with empty lines and whitespace
    let mut job_file = NamedTempFile::new().unwrap();
    writeln!(job_file, "hash1234567890abcdef").unwrap();
    writeln!(job_file, "").unwrap(); // empty line
    writeln!(job_file, "   ").unwrap(); // whitespace only
    writeln!(job_file, "  hash9876543210fedcba  ").unwrap(); // padded with spaces
    writeln!(job_file, "").unwrap(); // another empty line
    job_file.flush().unwrap();

    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

    let reader_handle = tokio::spawn({
        let job_path = job_file.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress = Arc::new(ProgressTracker::new(100)); // Small file size for test
        async move { job_reader(&job_path, tx, 10, store_clone, progress).await }
    });

    let mut all_keys = Vec::new();
    while let Some(batch) = rx.recv().await {
        all_keys.extend(batch.keys);
    }

    reader_handle.await.unwrap().unwrap();

    // Should only have 2 valid hashes (trimmed)
    assert_eq!(all_keys.len(), 2);
    assert_eq!(all_keys[0], "hash1234567890abcdef");
    assert_eq!(all_keys[1], "hash9876543210fedcba");
}

#[tokio::test]
async fn test_job_reader_batch_size() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;

    // Create job file with many hashes
    let mut job_file = NamedTempFile::new().unwrap();
    for i in 0..10 {
        writeln!(job_file, "hash{:02}1234567890abcdef", i).unwrap();
    }
    job_file.flush().unwrap();

    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);
    let batch_size = 3;

    let reader_handle = tokio::spawn({
        let job_path = job_file.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress = Arc::new(ProgressTracker::new(300)); // Estimate for 10 items
        async move { job_reader(&job_path, tx, batch_size, store_clone, progress).await }
    });

    let mut batch_count = 0;
    let mut total_keys = 0;
    while let Some(batch) = rx.recv().await {
        batch_count += 1;
        total_keys += batch.keys.len();

        // All batches except possibly the last should have batch_size items
        if batch_count < 4 {
            assert_eq!(batch.keys.len(), batch_size);
        }
    }

    reader_handle.await.unwrap().unwrap();

    assert_eq!(batch_count, 4); // 10 items / 3 per batch = 4 batches
    assert_eq!(total_keys, 10);
}

#[tokio::test]
async fn test_job_reader_all_files_exist() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;

    // Create existing files
    let hashes = vec!["hash1", "hash2", "hash3"];
    for hash in &hashes {
        store.write(hash, &Bytes::from("content")).await.unwrap();
    }

    // Create job file with only existing hashes
    let mut job_file = NamedTempFile::new().unwrap();
    for hash in &hashes {
        writeln!(job_file, "{}", hash).unwrap();
    }
    job_file.flush().unwrap();

    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

    let reader_handle = tokio::spawn({
        let job_path = job_file.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress = Arc::new(ProgressTracker::new(100)); // Small file size for test
        async move { job_reader(&job_path, tx, 10, store_clone, progress).await }
    });

    // Should receive no batches since all files exist
    let batch = rx.recv().await;
    assert!(
        batch.is_none(),
        "Should not receive any batches when all files exist"
    );

    reader_handle.await.unwrap().unwrap();
}
