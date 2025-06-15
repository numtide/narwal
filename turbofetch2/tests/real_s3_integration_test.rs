use std::path::{Path, PathBuf};
use tempfile::TempDir;
use tokio::fs;
use turbofetch2::{config::Config, error::Result, pipeline::Pipeline, signature::AwsCredentials};

/// Check if AWS credentials are available
async fn has_aws_credentials() -> bool {
    match AwsCredentials::load().await {
        Ok(_) => true,
        Err(_) => false,
    }
}

/// Helper to create a test config with custom parameters
fn create_test_config(batch_size: usize, num_workers: usize) -> Config {
    Config {
        region: "us-east-1".to_string(),
        hostname: "nix-cache.s3.amazonaws.com".to_string(),
        output_dir: PathBuf::from("test-output"),
        batch_size,
        num_workers,
        buffer_size: 512 * 1024,
        max_retries: 3,
        log_level: tracing::Level::INFO,
        job_file: PathBuf::from("test.txt"),
    }
}

/// Helper to count files in a directory recursively
async fn count_files_in_dir(dir: &Path) -> std::io::Result<usize> {
    let mut count = 0;
    let mut entries = fs::read_dir(dir).await?;

    while let Some(entry) = entries.next_entry().await? {
        let path = entry.path();
        if path.is_dir() {
            count += Box::pin(count_files_in_dir(&path)).await?;
        } else {
            count += 1;
        }
    }

    Ok(count)
}

/// Helper to verify narinfo file content
async fn verify_narinfo_file(path: &Path) -> Result<bool> {
    let content = fs::read_to_string(path)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    // Basic validation - narinfo files should contain these fields
    Ok(content.contains("StorePath:")
        && content.contains("URL:")
        && content.contains("Compression:"))
}

#[tokio::test]
async fn test_small_batch_real_s3() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir = TempDir::new().unwrap();
    let job_file = Path::new("tests/fixtures/small-batch.txt");

    // Create config with small batch size
    let mut config = create_test_config(2, 1);
    config.output_dir = temp_dir.path().to_path_buf();

    // Load credentials
    let credentials = AwsCredentials::load_with_region(&config.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    // Run pipeline
    let pipeline = Pipeline::new(config, credentials);
    let written_count = pipeline.process_job_file(job_file).await?;

    // Verify results
    assert_eq!(written_count, 5, "Should have written 5 narinfo files");

    let file_count = count_files_in_dir(temp_dir.path()).await.unwrap();
    assert_eq!(file_count, 5, "Should have 5 files on disk");

    // Verify a specific file
    let test_file = temp_dir
        .path()
        .join("00000")
        .join("000003nzgismzlipaq0jnchpz65d65z7.narinfo");
    assert!(test_file.exists(), "First narinfo file should exist");
    assert!(
        verify_narinfo_file(&test_file).await?,
        "Narinfo file should be valid"
    );

    Ok(())
}

#[tokio::test]
async fn test_medium_batch_parallel_workers() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir = TempDir::new().unwrap();
    let job_file = Path::new("tests/fixtures/medium-batch.txt");

    // Create config with multiple workers
    let mut config = create_test_config(5, 3);
    config.output_dir = temp_dir.path().to_path_buf();

    let credentials = AwsCredentials::load_with_region(&config.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    let pipeline = Pipeline::new(config, credentials);
    let written_count = pipeline.process_job_file(job_file).await?;

    assert_eq!(written_count, 20, "Should have written 20 narinfo files");

    // Verify directory structure
    let dirs = fs::read_dir(temp_dir.path()).await.unwrap();
    let dir_count = dirs.count().await;
    assert!(dir_count > 0, "Should have created subdirectories");

    Ok(())
}

#[tokio::test]
async fn test_large_batch_with_retries() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir = TempDir::new().unwrap();
    let job_file = Path::new("tests/fixtures/large-batch.txt");

    // Create config optimized for larger batches
    let mut config = create_test_config(10, 3);
    config.output_dir = temp_dir.path().to_path_buf();
    config.max_retries = 5; // More retries for resilience

    let credentials = AwsCredentials::load_with_region(&config.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    let pipeline = Pipeline::new(config, credentials);
    let written_count = pipeline.process_job_file(job_file).await?;

    assert_eq!(written_count, 60, "Should have written 60 narinfo files");

    Ok(())
}

#[tokio::test]
async fn test_batch_size_edge_cases() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir = TempDir::new().unwrap();
    let job_file = Path::new("tests/fixtures/small-batch.txt");

    // Test with batch size of 1 (each key in its own batch)
    let mut config = create_test_config(1, 1);
    config.output_dir = temp_dir.path().to_path_buf();

    let credentials = AwsCredentials::load_with_region(&config.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    let pipeline = Pipeline::new(config, credentials);
    let written_count = pipeline.process_job_file(job_file).await?;

    assert_eq!(
        written_count, 5,
        "Should process all files even with batch size 1"
    );

    Ok(())
}

#[tokio::test]
async fn test_empty_job_file() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir = TempDir::new().unwrap();

    // Create empty job file
    let job_file = temp_dir.path().join("empty.txt");
    fs::write(&job_file, "").await.unwrap();

    let mut config = create_test_config(10, 2);
    config.output_dir = temp_dir.path().to_path_buf();

    let credentials = AwsCredentials::load_with_region(&config.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    let pipeline = Pipeline::new(config, credentials);
    let written_count = pipeline.process_job_file(&job_file).await?;

    assert_eq!(written_count, 0, "Should handle empty job file gracefully");

    Ok(())
}

#[tokio::test]
async fn test_job_file_with_blank_lines() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir = TempDir::new().unwrap();

    // Create job file with blank lines
    let job_file = temp_dir.path().join("with-blanks.txt");
    let content = "000003nzgismzlipaq0jnchpz65d65z7\n\n00000c8rdrjqkdxjpm5wrhl6sspapbmn\n   \n00000jipsrr2nfgn36w04xg2z6hnr7y0\n";
    fs::write(&job_file, content).await.unwrap();

    let mut config = create_test_config(5, 1);
    config.output_dir = temp_dir.path().to_path_buf();

    let credentials = AwsCredentials::load_with_region(&config.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    let pipeline = Pipeline::new(config, credentials);
    let written_count = pipeline.process_job_file(&job_file).await?;

    assert_eq!(written_count, 3, "Should skip blank lines");

    Ok(())
}

#[tokio::test]
async fn test_concurrent_pipelines() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir1 = TempDir::new().unwrap();
    let temp_dir2 = TempDir::new().unwrap();

    let job_file1 = Path::new("tests/fixtures/small-batch.txt");
    let job_file2 = Path::new("tests/fixtures/medium-batch.txt");

    let mut config1 = create_test_config(5, 2);
    config1.output_dir = temp_dir1.path().to_path_buf();

    let mut config2 = create_test_config(5, 2);
    config2.output_dir = temp_dir2.path().to_path_buf();

    let credentials1 = AwsCredentials::load_with_region(&config1.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;
    let credentials2 = credentials1.clone();

    let pipeline1 = Pipeline::new(config1, credentials1);
    let pipeline2 = Pipeline::new(config2, credentials2);

    // Run both pipelines concurrently
    let (result1, result2) = tokio::join!(
        pipeline1.process_job_file(job_file1),
        pipeline2.process_job_file(job_file2)
    );

    let written_count1 = result1?;
    let written_count2 = result2?;

    assert_eq!(written_count1, 5, "First pipeline should write 5 files");
    assert_eq!(written_count2, 20, "Second pipeline should write 20 files");

    Ok(())
}

#[tokio::test]
async fn test_verify_file_organization() -> Result<()> {
    if !has_aws_credentials().await {
        eprintln!("Skipping test: AWS credentials not available");
        return Ok(());
    }

    let temp_dir = TempDir::new().unwrap();
    let job_file = Path::new("tests/fixtures/medium-batch.txt");

    let mut config = create_test_config(10, 2);
    config.output_dir = temp_dir.path().to_path_buf();

    let credentials = AwsCredentials::load_with_region(&config.region)
        .await
        .map_err(turbofetch2::error::TurbofetchError::from)?;

    let pipeline = Pipeline::new(config, credentials);
    pipeline.process_job_file(job_file).await?;

    // Check that files are organized by prefix
    let prefix_00000 = temp_dir.path().join("00000");
    let prefix_00001 = temp_dir.path().join("00001");
    let prefix_00002 = temp_dir.path().join("00002");

    assert!(prefix_00000.exists(), "Should have 00000 prefix directory");
    assert!(prefix_00001.exists(), "Should have 00001 prefix directory");
    assert!(prefix_00002.exists(), "Should have 00002 prefix directory");

    // Count files in each prefix directory
    let count_00000 = fs::read_dir(&prefix_00000).await.unwrap().count().await;
    let count_00001 = fs::read_dir(&prefix_00001).await.unwrap().count().await;
    let count_00002 = fs::read_dir(&prefix_00002).await.unwrap().count().await;

    assert!(count_00000 > 0, "Should have files in 00000 directory");
    assert!(count_00001 > 0, "Should have files in 00001 directory");
    assert!(count_00002 > 0, "Should have files in 00002 directory");

    Ok(())
}

// Extension trait to count entries in a ReadDir
trait ReadDirExt {
    async fn count(self) -> usize;
}

impl ReadDirExt for fs::ReadDir {
    async fn count(mut self) -> usize {
        let mut count = 0;
        while self.next_entry().await.unwrap().is_some() {
            count += 1;
        }
        count
    }
}
