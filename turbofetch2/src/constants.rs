/// Default batch size for processing narinfo keys
pub const DEFAULT_BATCH_SIZE: usize = 50;

/// Default number of concurrent HTTP processors
pub const DEFAULT_NUM_WORKERS: usize = 3;

/// Default buffer size for network operations (512KB)
pub const DEFAULT_BUFFER_SIZE: usize = 512 * 1024;

/// Default maximum number of retries for failed requests
pub const DEFAULT_MAX_RETRIES: usize = 3;

/// Channel buffer size for batch processing
pub const BATCH_CHANNEL_SIZE: usize = 100;

/// Channel buffer size for results
pub const RESULT_CHANNEL_SIZE: usize = 50;

/// Hash prefix length for directory organization
pub const HASH_PREFIX_LENGTH: usize = 5;
