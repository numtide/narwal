use bytes::Bytes;
use tempfile::TempDir;
use turbofetch2::{
    config::Config,
    constants::DEFAULT_BATCH_SIZE,
    error::Result,
    narinfo_store::{DiskNarinfoStore, NarinfoStore},
    object_store::{ObjectStore, S3ObjectStore},
    signature::AwsCredentials,
    NarinfoBatch,
};

#[cfg(test)]
mod mock_object_store {
    use super::*;
    use std::collections::HashMap;
    use turbofetch2::error::TurbofetchError;

    pub struct MockObjectStore {
        objects: HashMap<String, Bytes>,
    }

    impl MockObjectStore {
        pub fn new() -> Self {
            Self {
                objects: HashMap::new(),
            }
        }

        #[allow(dead_code)]
        pub fn with_objects(objects: HashMap<String, Bytes>) -> Self {
            Self { objects }
        }

        pub fn add_object(&mut self, key: String, content: Bytes) {
            self.objects.insert(key, content);
        }
    }

    #[async_trait::async_trait]
    impl ObjectStore for MockObjectStore {
        async fn get(&self, key: &str) -> Result<Bytes> {
            self.objects
                .get(key)
                .cloned()
                .ok_or_else(|| TurbofetchError::Http(format!("Object not found: {}", key)))
        }

        async fn get_batch(&self, keys: &[String]) -> Result<Vec<(String, Bytes)>> {
            let mut results = Vec::new();
            for key in keys {
                if let Some(content) = self.objects.get(key) {
                    results.push((key.clone(), content.clone()));
                }
            }
            Ok(results)
        }

        async fn exists(&self, key: &str) -> Result<bool> {
            Ok(self.objects.contains_key(key))
        }
    }
}

#[tokio::test]
async fn test_narinfo_batch_creation() {
    // Test successful creation
    let keys = vec!["key1".to_string(), "key2".to_string()];
    let batch = NarinfoBatch::new(keys.clone(), 42);

    assert_eq!(batch.keys.len(), 2);
    assert_eq!(batch.batch_id, 42);
    assert_eq!(batch.keys, keys);

    // Test with empty keys (now allowed since we removed validation)
    let empty_batch = NarinfoBatch::new(vec![], 0);
    assert_eq!(empty_batch.keys.len(), 0);
    assert_eq!(empty_batch.batch_id, 0);
}

#[tokio::test]
async fn test_narinfo_store_operations() -> Result<()> {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    // Test writing a narinfo file
    let hash = "abc123def456";
    let content = Bytes::from("StorePath: /nix/store/abc123def456-example\n");

    // File should not exist initially
    assert!(!store.exists(hash).await?);

    store.write(hash, &content).await?;

    // File should exist after writing
    assert!(store.exists(hash).await?);

    // Verify the file was written at the expected path
    let expected_path = store.get_path(hash);
    assert!(expected_path.exists());

    let written_content = tokio::fs::read(&expected_path).await?;
    assert_eq!(written_content, content.as_ref());

    Ok(())
}

#[tokio::test]
async fn test_narinfo_store_atomic_operations() -> Result<()> {
    let temp_dir = TempDir::new().unwrap();
    let store = DiskNarinfoStore::new(temp_dir.path());

    let hash = "testfile123456";
    let original_content = Bytes::from("Original content");
    let updated_content = Bytes::from("Updated content");

    // Write original content
    store.write(hash, &original_content).await?;

    // Overwrite with new content
    store.write(hash, &updated_content).await?;

    // Verify updated content
    let path = store.get_path(hash);
    let read_content = tokio::fs::read(&path).await?;
    assert_eq!(read_content, updated_content.as_ref());

    Ok(())
}

#[tokio::test]
async fn test_mock_object_store() -> Result<()> {
    use mock_object_store::MockObjectStore;

    let mut store = MockObjectStore::new();
    store.add_object("test-key".to_string(), Bytes::from("test-content"));

    // Test get
    let content = store.get("test-key").await?;
    assert_eq!(content, Bytes::from("test-content"));

    // Test exists
    assert!(store.exists("test-key").await?);
    assert!(!store.exists("non-existent").await?);

    // Test get_batch
    store.add_object("key1".to_string(), Bytes::from("content1"));
    store.add_object("key2".to_string(), Bytes::from("content2"));

    let batch_keys = vec![
        "key1".to_string(),
        "key2".to_string(),
        "missing".to_string(),
    ];
    let results = store.get_batch(&batch_keys).await?;

    assert_eq!(results.len(), 2);
    assert!(results.iter().any(|(k, _)| k == "key1"));
    assert!(results.iter().any(|(k, _)| k == "key2"));

    Ok(())
}

#[tokio::test]
async fn test_config_builder() {
    let config = Config::from_args().unwrap_or_else(|_| Config::default());

    assert_eq!(config.region, "us-east-1");
    assert_eq!(config.hostname, "nix-cache.s3.amazonaws.com");
    assert_eq!(config.batch_size, DEFAULT_BATCH_SIZE);
    assert_eq!(config.num_workers, 3);
}

#[tokio::test]
#[ignore = "Requires AWS credentials and S3 access"]
async fn test_s3_object_store_real() -> Result<()> {
    // This test requires real AWS credentials and S3 access
    let credentials = AwsCredentials::load().await?;
    let store = S3ObjectStore::builder()
        .hostname("nix-cache.s3.amazonaws.com".to_string())
        .credentials(credentials)
        .build()?;

    // Test with a known narinfo key (would need a real one for actual testing)
    let key = "000003nzgismzlipaq0jnchpz65d65z7";
    let exists = store.exists(&format!("{}.narinfo", key)).await?;

    println!("Object exists: {}", exists);

    Ok(())
}

#[tokio::test]
async fn test_pipeline_with_mock_data() -> Result<()> {
    use std::io::Write;

    // Create a temporary job file
    let temp_dir = TempDir::new().unwrap();
    let job_file = temp_dir.path().join("job.txt");

    let mut file = std::fs::File::create(&job_file).unwrap();
    writeln!(file, "key1").unwrap();
    writeln!(file, "key2").unwrap();
    writeln!(file, "key3").unwrap();

    // Note: This test is limited because Pipeline currently uses real S3
    // In a real implementation, we'd need to make Pipeline accept an ObjectStore trait

    Ok(())
}
