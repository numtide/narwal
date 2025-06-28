use std::path::PathBuf;
use std::sync::Arc;
use tempfile::TempDir;
use tokio::sync::mpsc;
use turbofetch2::narinfo_store::{DiskNarinfoStore, NarinfoStore};
use turbofetch2::parquet_reader::{parquet_reader, ParquetReaderConfig};
use turbofetch2::progress::ProgressTracker;
use turbofetch2::NarinfoBatch;

#[tokio::test]
async fn test_parquet_reader_small_batch() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;
    let progress = Arc::new(ProgressTracker::new(5)); // 5 narinfo files in small-batch

    // Use the small-batch.parquet fixture
    let parquet_dir = PathBuf::from("tests/fixtures");
    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

    // Copy small-batch.parquet to a temp directory to test with just one file
    let test_dir = TempDir::new().unwrap();
    std::fs::copy(
        parquet_dir.join("small-batch.parquet"),
        test_dir.path().join("small-batch.parquet"),
    )
    .unwrap();

    let reader_handle = tokio::spawn({
        let parquet_path = test_dir.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress_clone = Arc::clone(&progress);
        async move {
            let config = ParquetReaderConfig {
                batch_size: 3,
                max_parallel_files: 1,
            };
            parquet_reader(&parquet_path, tx, config, store_clone, progress_clone).await
        }
    });

    // Collect all batches
    let mut all_keys = Vec::new();
    while let Some(batch) = rx.recv().await {
        all_keys.extend(batch.keys);
    }

    reader_handle.await.unwrap().unwrap();

    // The small batch has 5 narinfo hashes
    assert_eq!(all_keys.len(), 5);

    // Check that we got the expected hashes
    let expected_hashes = vec![
        "000003nzgismzlipaq0jnchpz65d65z7",
        "00000c8rdrjqkdxjpm5wrhl6sspapbmn",
        "00000jipsrr2nfgn36w04xg2z6hnr7y0",
        "00000lkv8yz6bsccrp03ppq71mzjdcpv",
        "00000vg29cfg9p0p3r8fpd0p9haf5dbj",
    ];

    for hash in expected_hashes {
        assert!(
            all_keys.contains(&hash.to_string()),
            "Missing hash: {}",
            hash
        );
    }
}

#[tokio::test]
async fn test_parquet_reader_mixed_content() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;
    let progress = Arc::new(ProgressTracker::new(3)); // 3 narinfo files in mixed-content

    // Use the mixed-content.parquet fixture
    let parquet_dir = PathBuf::from("tests/fixtures");
    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

    // Copy mixed-content.parquet to a temp directory
    let test_dir = TempDir::new().unwrap();
    std::fs::copy(
        parquet_dir.join("mixed-content.parquet"),
        test_dir.path().join("mixed-content.parquet"),
    )
    .unwrap();

    let reader_handle = tokio::spawn({
        let parquet_path = test_dir.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress_clone = Arc::clone(&progress);
        async move {
            let config = ParquetReaderConfig {
                batch_size: 10,
                max_parallel_files: 1,
            };
            parquet_reader(&parquet_path, tx, config, store_clone, progress_clone).await
        }
    });

    // Collect all batches
    let mut all_keys = Vec::new();
    while let Some(batch) = rx.recv().await {
        all_keys.extend(batch.keys);
    }

    reader_handle.await.unwrap().unwrap();

    // The mixed content file has 3 narinfo files (out of 6 total)
    assert_eq!(all_keys.len(), 3);

    // Check that we only got narinfo hashes
    assert!(all_keys.contains(&"hash1".to_string()));
    assert!(all_keys.contains(&"hash3".to_string()));
    assert!(all_keys.contains(&"hash4".to_string()));

    // Make sure non-narinfo files were filtered out
    assert!(!all_keys.contains(&"hash2".to_string())); // This was a .nar file
}

#[tokio::test]
async fn test_parquet_reader_skip_existing() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;
    let progress = Arc::new(ProgressTracker::new(5)); // 5 narinfo files total, 2 will be skipped

    // Pre-create some narinfo files
    let existing_hashes = vec![
        "000003nzgismzlipaq0jnchpz65d65z7",
        "00000jipsrr2nfgn36w04xg2z6hnr7y0",
    ];
    for hash in &existing_hashes {
        store
            .write(hash, &bytes::Bytes::from("existing content"))
            .await
            .unwrap();
    }

    // Use the small-batch.parquet fixture
    let parquet_dir = PathBuf::from("tests/fixtures");
    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

    let test_dir = TempDir::new().unwrap();
    std::fs::copy(
        parquet_dir.join("small-batch.parquet"),
        test_dir.path().join("small-batch.parquet"),
    )
    .unwrap();

    let reader_handle = tokio::spawn({
        let parquet_path = test_dir.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress_clone = Arc::clone(&progress);
        async move {
            let config = ParquetReaderConfig {
                batch_size: 10,
                max_parallel_files: 1,
            };
            parquet_reader(&parquet_path, tx, config, store_clone, progress_clone).await
        }
    });

    // Collect all batches
    let mut all_keys = Vec::new();
    while let Some(batch) = rx.recv().await {
        all_keys.extend(batch.keys);
    }

    reader_handle.await.unwrap().unwrap();

    // Should only have 3 keys (5 total - 2 existing)
    assert_eq!(all_keys.len(), 3);

    // Make sure existing files were skipped
    for hash in &existing_hashes {
        assert!(
            !all_keys.contains(&hash.to_string()),
            "Existing hash should be skipped: {}",
            hash
        );
    }
}

#[tokio::test]
async fn test_parquet_reader_multiple_files() {
    let temp_dir = TempDir::new().unwrap();
    let store = Arc::new(DiskNarinfoStore::new(temp_dir.path())) as Arc<dyn NarinfoStore>;
    let progress = Arc::new(ProgressTracker::new(8)); // 5 + 3 narinfo files from both files

    // Copy multiple parquet files to test directory
    let test_dir = TempDir::new().unwrap();
    let parquet_dir = PathBuf::from("tests/fixtures");

    std::fs::copy(
        parquet_dir.join("small-batch.parquet"),
        test_dir.path().join("small-batch.parquet"),
    )
    .unwrap();

    std::fs::copy(
        parquet_dir.join("mixed-content.parquet"),
        test_dir.path().join("mixed-content.parquet"),
    )
    .unwrap();

    let (tx, mut rx) = mpsc::channel::<NarinfoBatch>(10);

    let reader_handle = tokio::spawn({
        let parquet_path = test_dir.path().to_path_buf();
        let store_clone = Arc::clone(&store);
        let progress_clone = Arc::clone(&progress);
        async move {
            let config = ParquetReaderConfig {
                batch_size: 5,
                max_parallel_files: 2,
            };
            parquet_reader(&parquet_path, tx, config, store_clone, progress_clone).await
        }
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
