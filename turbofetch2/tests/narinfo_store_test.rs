use bytes::Bytes;
use tempfile::TempDir;
use tokio;
use turbofetch2::narinfo_store::{DiskNarinfoStore, NarinfoStore};

#[tokio::test]
async fn test_narinfo_store_write_and_exists() {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    let hash = "abcdefghijklmnopqrstuvwxyz123456";
    let content = Bytes::from("test narinfo content");

    // Initially file should not exist
    assert!(
        !store.exists(hash).await.unwrap(),
        "File should not exist initially"
    );

    // Write the file
    store.write(hash, &content).await.unwrap();

    // Now file should exist
    assert!(
        store.exists(hash).await.unwrap(),
        "File should exist after writing"
    );

    // Verify file is in correct location
    let expected_path = temp_dir
        .path()
        .join("abcde")
        .join(format!("{}.narinfo", hash));
    assert!(expected_path.exists(), "File should be at expected path");

    // Verify content
    let read_content = std::fs::read_to_string(&expected_path).unwrap();
    assert_eq!(read_content, "test narinfo content");
}

#[tokio::test]
async fn test_narinfo_store_atomic_write() {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    let hash = "atomictest123456789abcdef";
    let content1 = Bytes::from("original content");
    let content2 = Bytes::from("updated content");

    // Write original content
    store.write(hash, &content1).await.unwrap();
    assert!(store.exists(hash).await.unwrap());

    // Overwrite with new content
    store.write(hash, &content2).await.unwrap();

    // Verify new content
    let path = store.get_path(hash);
    let read_content = std::fs::read_to_string(&path).unwrap();
    assert_eq!(read_content, "updated content");
}

#[tokio::test]
async fn test_narinfo_store_multiple_prefixes() {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    // Test files with different prefixes
    let test_cases = vec![
        ("aaaaa1234567890", "content1"),
        ("bbbbb1234567890", "content2"),
        ("aaaaa9876543210", "content3"), // Same prefix as first
    ];

    // Write all files
    for (hash, content) in &test_cases {
        store.write(hash, &Bytes::from(*content)).await.unwrap();
    }

    // Verify all exist
    for (hash, _) in &test_cases {
        assert!(
            store.exists(hash).await.unwrap(),
            "File {} should exist",
            hash
        );
    }

    // Verify directory structure
    assert!(temp_dir.path().join("aaaaa").is_dir());
    assert!(temp_dir.path().join("bbbbb").is_dir());

    // Verify correct number of files in each directory
    let aaaaa_files: Vec<_> = std::fs::read_dir(temp_dir.path().join("aaaaa"))
        .unwrap()
        .collect();
    assert_eq!(aaaaa_files.len(), 2);

    let bbbbb_files: Vec<_> = std::fs::read_dir(temp_dir.path().join("bbbbb"))
        .unwrap()
        .collect();
    assert_eq!(bbbbb_files.len(), 1);
}

#[tokio::test]
async fn test_narinfo_store_exists_nonexistent() {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    // Test various nonexistent files
    assert!(!store.exists("nonexistent123").await.unwrap());
    assert!(!store.exists("xyz1234567890abcdef").await.unwrap());

    // Even if directory exists, file should not
    std::fs::create_dir_all(temp_dir.path().join("xyz12")).unwrap();
    assert!(!store.exists("xyz1234567890abcdef").await.unwrap());
}

#[tokio::test]
async fn test_narinfo_store_short_hash() {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    // Test with hash shorter than prefix length
    let short_hash = "abc";
    let content = Bytes::from("short hash content");

    store.write(short_hash, &content).await.unwrap();
    assert!(store.exists(short_hash).await.unwrap());

    // Verify it's in the right place
    let expected_path = temp_dir.path().join("abc").join("abc.narinfo");
    assert!(expected_path.exists());
}
