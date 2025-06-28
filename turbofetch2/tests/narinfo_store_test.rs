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

    // Verify file is in correct location (2-layer: ab/cd/)
    let expected_path = temp_dir
        .path()
        .join("ab")
        .join("cd")
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

    // Test files with different prefixes (2-layer structure)
    let test_cases = vec![
        ("aaaa1234567890", "content1"),      // aa/aa/
        ("abcd1234567890", "content2"),      // ab/cd/
        ("aaaa9876543210", "content3"),      // aa/aa/ (same dir as first)
        ("abxy1234567890", "content4"),      // ab/xy/
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

    // Verify directory structure (2-layer)
    assert!(temp_dir.path().join("aa").is_dir());
    assert!(temp_dir.path().join("ab").is_dir());
    assert!(temp_dir.path().join("aa").join("aa").is_dir());
    assert!(temp_dir.path().join("ab").join("cd").is_dir());
    assert!(temp_dir.path().join("ab").join("xy").is_dir());

    // Verify correct number of files in each directory
    let aa_aa_files: Vec<_> = std::fs::read_dir(temp_dir.path().join("aa").join("aa"))
        .unwrap()
        .collect();
    assert_eq!(aa_aa_files.len(), 2);

    let ab_cd_files: Vec<_> = std::fs::read_dir(temp_dir.path().join("ab").join("cd"))
        .unwrap()
        .collect();
    assert_eq!(ab_cd_files.len(), 1);
    
    let ab_xy_files: Vec<_> = std::fs::read_dir(temp_dir.path().join("ab").join("xy"))
        .unwrap()
        .collect();
    assert_eq!(ab_xy_files.len(), 1);
}

#[tokio::test]
async fn test_narinfo_store_exists_nonexistent() {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    // Test various nonexistent files
    assert!(!store.exists("nonexistent123").await.unwrap());
    assert!(!store.exists("xyz1234567890abcdef").await.unwrap());

    // Even if directory exists, file should not
    std::fs::create_dir_all(temp_dir.path().join("xy").join("z1")).unwrap();
    assert!(!store.exists("xyz1234567890abcdef").await.unwrap());
}

#[tokio::test]
async fn test_narinfo_store_short_hash() {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    // Test with hash shorter than full prefix length
    let short_hash = "abc";
    let content = Bytes::from("short hash content");

    store.write(short_hash, &content).await.unwrap();
    assert!(store.exists(short_hash).await.unwrap());

    // Verify it's in the right place (ab/c/ for 3-char hash)
    let expected_path = temp_dir.path().join("ab").join("c").join("abc.narinfo");
    assert!(expected_path.exists());
    
    // Test with even shorter hash
    let very_short_hash = "a";
    let content2 = Bytes::from("very short hash content");
    
    store.write(very_short_hash, &content2).await.unwrap();
    assert!(store.exists(very_short_hash).await.unwrap());
    
    // Verify it's in the right place (a/ for 1-char hash)
    let expected_path2 = temp_dir.path().join("a").join("a.narinfo");
    assert!(expected_path2.exists());
}
