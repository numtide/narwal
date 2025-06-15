use crate::{
    error::{Result, TurbofetchError},
    signature::AwsCredentials,
};
use bytes::Bytes;

/// Trait for object storage operations
#[async_trait::async_trait]
pub trait ObjectStore: Send + Sync {
    /// Fetch a single object by key
    async fn get(&self, key: &str) -> Result<Bytes>;

    /// Fetch multiple objects by keys in a batch
    async fn get_batch(&self, keys: &[String]) -> Result<Vec<(String, Bytes)>>;

    /// Check if an object exists
    async fn exists(&self, key: &str) -> Result<bool>;
}

/// S3 implementation of ObjectStore
pub struct S3ObjectStore {
    hostname: String,
    credentials: AwsCredentials,
    buffer_size: usize,
}

impl S3ObjectStore {
    pub fn new(hostname: String, credentials: AwsCredentials) -> Result<Self> {
        Ok(Self {
            hostname,
            credentials,
            buffer_size: 512 * 1024,
        })
    }

    pub fn builder() -> S3ObjectStoreBuilder {
        S3ObjectStoreBuilder::new()
    }
}

#[async_trait::async_trait]
impl ObjectStore for S3ObjectStore {
    async fn get(&self, key: &str) -> Result<Bytes> {
        let results = self.get_batch(&[key.to_string()]).await?;
        results
            .into_iter()
            .next()
            .map(|(_, bytes)| bytes)
            .ok_or_else(|| TurbofetchError::Http(format!("Object not found: {}", key)))
    }

    async fn get_batch(&self, keys: &[String]) -> Result<Vec<(String, Bytes)>> {
        // Create a new client for each batch (since fetch_batch requires &mut self)
        let mut client =
            crate::http_client::S3Client::new(self.hostname.clone(), self.buffer_size)?;

        client.fetch_batch(keys, &self.credentials, 3).await
    }

    async fn exists(&self, key: &str) -> Result<bool> {
        match self.get(key).await {
            Ok(_) => Ok(true),
            Err(TurbofetchError::Http(msg)) if msg.contains("not found") => Ok(false),
            Err(e) => Err(e),
        }
    }
}

/// Builder for S3ObjectStore
pub struct S3ObjectStoreBuilder {
    hostname: Option<String>,
    credentials: Option<AwsCredentials>,
    buffer_size: Option<usize>,
}

impl S3ObjectStoreBuilder {
    pub fn new() -> Self {
        Self {
            hostname: None,
            credentials: None,
            buffer_size: None,
        }
    }

    pub fn hostname(mut self, hostname: String) -> Self {
        self.hostname = Some(hostname);
        self
    }

    pub fn credentials(mut self, credentials: AwsCredentials) -> Self {
        self.credentials = Some(credentials);
        self
    }

    pub fn buffer_size(mut self, size: usize) -> Self {
        self.buffer_size = Some(size);
        self
    }

    pub fn build(self) -> Result<S3ObjectStore> {
        let hostname = self
            .hostname
            .ok_or_else(|| TurbofetchError::Config("hostname is required".to_string()))?;
        let credentials = self
            .credentials
            .ok_or_else(|| TurbofetchError::Config("credentials are required".to_string()))?;

        let buffer_size = self.buffer_size.unwrap_or(512 * 1024);

        Ok(S3ObjectStore {
            hostname,
            credentials,
            buffer_size,
        })
    }
}

impl Default for S3ObjectStoreBuilder {
    fn default() -> Self {
        Self::new()
    }
}
