//! turbofetch is a high-performance bulk S3 object fetcher.
//!
//! It operates on S3 inventory parquet files downloaded by the narwal tool.
//! The parquet files contain metadata about all objects in the S3 bucket,
//! from which turbofetch extracts narinfo file paths and fetches them.
//!
//! Each run of turbofetch processes all parquet files in the specified
//! directory and writes each narinfo file to the local filesystem,
//! organized in a 2-layer directory structure using the first 4 characters
//! of the hash (2+2) for efficient storage and lookup.
//!
//! Files are written atomically using temporary files to ensure readers
//! never see partial writes. The application fails fast on any error,
//! ensuring data consistency.

use turbofetch2::{AwsCredentials, Config, Pipeline};

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Parse command line arguments first to get log level
    let config = match Config::from_args() {
        Ok(config) => config,
        Err(e) => {
            if !e.is_empty() {
                eprintln!("{}", e);
            }
            std::process::exit(1);
        }
    };

    // Initialize logging with the configured level
    // Write to stderr to avoid interfering with progress bars
    tracing_subscriber::fmt()
        .with_max_level(config.log_level)
        .with_target(false)
        .with_writer(std::io::stderr)
        .init();

    // Load AWS credentials
    let credentials = AwsCredentials::load_with_region(&config.region).await?;

    // Create and run pipeline
    let pipeline = Pipeline::new(config, credentials);

    // Process the parquet files
    pipeline.process_parquet_files().await?;

    Ok(())
}
