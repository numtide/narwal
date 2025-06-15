use crate::{
    constants::HASH_PREFIX_LENGTH,
    error::{Result, TurbofetchError},
};
use bytes::Bytes;
use std::path::{Path, PathBuf};
use tempfile::NamedTempFile;
use tokio::fs as tokio_fs;

/// Trait for narinfo storage operations
#[async_trait::async_trait]
pub trait NarinfoStore: Send + Sync {
    /// Check if a narinfo file exists for the given hash
    async fn exists(&self, hash: &str) -> Result<bool>;

    /// Write a narinfo file with the given content
    async fn write(&self, hash: &str, content: &Bytes) -> Result<()>;

    /// Get the full path for a narinfo file (for debugging/logging)
    fn get_path(&self, hash: &str) -> PathBuf;
}

/// Disk-based narinfo store with atomic write operations
pub struct DiskNarinfoStore {
    base_dir: PathBuf,
}

impl DiskNarinfoStore {
    pub fn new(base_dir: impl Into<PathBuf>) -> Self {
        Self {
            base_dir: base_dir.into(),
        }
    }

    /// Get the directory path for a given hash
    fn get_dir_path(&self, hash: &str) -> PathBuf {
        let prefix = &hash[..HASH_PREFIX_LENGTH.min(hash.len())];
        self.base_dir.join(prefix)
    }

    /// Get the full file path for a narinfo
    fn get_file_path(&self, hash: &str) -> PathBuf {
        let prefix = &hash[..HASH_PREFIX_LENGTH.min(hash.len())];
        self.base_dir.join(prefix).join(format!("{}.narinfo", hash))
    }

    /// Write data to a file atomically by using a temporary file and renaming it.
    /// This ensures that readers never see partial writes.
    async fn write_atomic(&self, target_path: &Path, content: &[u8]) -> Result<()> {
        let parent_dir = target_path.parent().ok_or_else(|| {
            TurbofetchError::Io(std::io::Error::new(
                std::io::ErrorKind::InvalidInput,
                "Target path has no parent directory",
            ))
        })?;

        // Create temporary file in the same directory as the target
        let temp_file = NamedTempFile::new_in(parent_dir).map_err(TurbofetchError::from)?;
        let temp_path = temp_file.path();

        // Write content to temporary file
        tokio_fs::write(&temp_path, content)
            .await
            .map_err(TurbofetchError::from)?;

        // Atomically move temporary file to target location
        tokio_fs::rename(&temp_path, target_path)
            .await
            .map_err(TurbofetchError::from)?;

        // Don't delete the temp file automatically since we've moved it
        temp_file.keep().map_err(|e| {
            TurbofetchError::Io(std::io::Error::new(
                std::io::ErrorKind::Other,
                format!("Failed to keep temp file: {}", e),
            ))
        })?;

        Ok(())
    }
}

#[async_trait::async_trait]
impl NarinfoStore for DiskNarinfoStore {
    async fn exists(&self, hash: &str) -> Result<bool> {
        let path = self.get_file_path(hash);
        match tokio_fs::metadata(&path).await {
            Ok(_) => Ok(true),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
            Err(e) => Err(TurbofetchError::from(e)),
        }
    }

    async fn write(&self, hash: &str, content: &Bytes) -> Result<()> {
        let dir_path = self.get_dir_path(hash);

        // Create directory if it doesn't exist
        tokio_fs::create_dir_all(&dir_path)
            .await
            .map_err(TurbofetchError::from)?;

        // Write narinfo file atomically
        let file_path = self.get_file_path(hash);
        self.write_atomic(&file_path, content).await?;

        Ok(())
    }

    fn get_path(&self, hash: &str) -> PathBuf {
        self.get_file_path(hash)
    }
}

/// Builder for DiskNarinfoStore
pub struct DiskNarinfoStoreBuilder {
    base_dir: Option<PathBuf>,
}

impl DiskNarinfoStoreBuilder {
    pub fn new() -> Self {
        Self { base_dir: None }
    }

    pub fn base_dir(mut self, dir: impl Into<PathBuf>) -> Self {
        self.base_dir = Some(dir.into());
        self
    }

    pub fn build(self) -> Result<DiskNarinfoStore> {
        let base_dir = self
            .base_dir
            .ok_or_else(|| TurbofetchError::Config("base_dir is required".to_string()))?;

        Ok(DiskNarinfoStore { base_dir })
    }
}

impl Default for DiskNarinfoStoreBuilder {
    fn default() -> Self {
        Self::new()
    }
}

impl DiskNarinfoStore {
    pub fn builder() -> DiskNarinfoStoreBuilder {
        DiskNarinfoStoreBuilder::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use bytes::Bytes;
    use tempfile::TempDir;

    #[tokio::test]
    async fn test_disk_narinfo_store_builder() {
        // Test successful build
        let temp_dir = TempDir::new().unwrap();
        let store = DiskNarinfoStore::builder()
            .base_dir(temp_dir.path())
            .build();

        assert!(store.is_ok());

        // Test missing base_dir
        let store = DiskNarinfoStore::builder().build();
        assert!(store.is_err());
    }

    #[tokio::test]
    async fn test_exists_and_write() {
        let temp_dir = TempDir::new().unwrap();
        let store = DiskNarinfoStore::new(temp_dir.path());

        let hash = "abcdefghijklmnop";

        // Check that file doesn't exist initially
        assert!(!store.exists(hash).await.unwrap());

        // Write narinfo
        let content = Bytes::from("test content");
        store.write(hash, &content).await.unwrap();

        // Check that file now exists
        assert!(store.exists(hash).await.unwrap());

        // Verify path structure
        let path = store.get_path(hash);
        assert!(path.exists());
        assert_eq!(
            path.to_str().unwrap(),
            temp_dir
                .path()
                .join("abcde")
                .join("abcdefghijklmnop.narinfo")
                .to_str()
                .unwrap()
        );
    }

    #[tokio::test]
    async fn test_write_creates_directories() {
        let temp_dir = TempDir::new().unwrap();
        let store = DiskNarinfoStore::new(temp_dir.path());

        let hash = "xyzabc123456789";
        let content = Bytes::from("test content");

        // Write narinfo (should create directory)
        store.write(hash, &content).await.unwrap();

        // Check that directory was created
        let dir_path = temp_dir.path().join("xyzab");
        assert!(dir_path.exists());
        assert!(dir_path.is_dir());
    }
}
