//! turbofetch is a high-performance bulk S3 object fetcher.
//!
//! It operates on a local job file consisting of nixbase32-encoded narinfo
//! hashes, one per line. Each hash represents a narinfo file in the source
//! S3 bucket (nix-cache).
//!
//! Each run of turbofetch processes the entire job file and writes each
//! narinfo file to the local filesystem, organized by 5-character hash
//! prefixes into subdirectories for efficient storage and lookup.
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
    let pipeline = Pipeline::new(config.clone(), credentials);

    // Process the job file
    pipeline.process_job_file(&config.job_file).await?;

    Ok(())
}
