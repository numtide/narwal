use crate::constants::{
    DEFAULT_BATCH_SIZE, DEFAULT_BUFFER_SIZE, DEFAULT_MAX_RETRIES, DEFAULT_NUM_WORKERS,
};
use std::path::PathBuf;

#[derive(Debug, Clone)]
pub struct Config {
    pub region: String,
    pub hostname: String,
    pub output_dir: PathBuf,
    pub batch_size: usize,
    pub num_workers: usize,
    pub buffer_size: usize,
    pub max_retries: usize,
    pub log_level: tracing::Level,
    pub parquet_dir: PathBuf,
    pub max_parallel_files: usize,
}

impl Default for Config {
    fn default() -> Self {
        Self {
            region: "us-east-1".to_string(),
            hostname: "nix-cache.s3.amazonaws.com".to_string(),
            output_dir: PathBuf::from("narinfo"),
            batch_size: DEFAULT_BATCH_SIZE,
            num_workers: DEFAULT_NUM_WORKERS,
            buffer_size: DEFAULT_BUFFER_SIZE,
            max_retries: DEFAULT_MAX_RETRIES,
            log_level: tracing::Level::INFO,
            parquet_dir: PathBuf::new(),
            max_parallel_files: 4,
        }
    }
}

impl Config {
    fn print_help(program_name: &str) {
        eprintln!("Usage: {} --parquet-dir <DIR> [options]", program_name);
        eprintln!();
        eprintln!("Required:");
        eprintln!("  --parquet-dir <DIR>     Directory containing S3 inventory parquet files");
        eprintln!();
        eprintln!("Options:");
        eprintln!("  --region <REGION>       AWS region (default: us-east-1)");
        eprintln!("  --hostname <HOST>       S3 hostname (default: nix-cache.s3.amazonaws.com)");
        eprintln!(
            "  --output-dir <DIR>      Output directory for narinfo files (default: narinfo)"
        );
        eprintln!(
            "  --workers <N>           Number of worker threads (default: {})",
            DEFAULT_NUM_WORKERS
        );
        eprintln!(
            "  --batch-size <N>        Batch size for processing (default: {})",
            DEFAULT_BATCH_SIZE
        );
        eprintln!(
            "  --log-level <LEVEL>     Log level: error, warn, info, debug, trace (default: info)"
        );
        eprintln!(
            "  --max-parallel-files <N> Maximum parallel parquet file readers (default: 4, minimum: 1)"
        );
        eprintln!("  --help                  Show this help message");
    }

    fn parse_log_level(level: &str) -> Result<tracing::Level, String> {
        match level.to_lowercase().as_str() {
            "error" => Ok(tracing::Level::ERROR),
            "warn" => Ok(tracing::Level::WARN),
            "info" => Ok(tracing::Level::INFO),
            "debug" => Ok(tracing::Level::DEBUG),
            "trace" => Ok(tracing::Level::TRACE),
            _ => Err(format!(
                "Invalid log level: {}. Valid options: error, warn, info, debug, trace",
                level
            )),
        }
    }

    fn parse_arg_value(args: &[String], i: &mut usize, arg_name: &str) -> Result<String, String> {
        *i += 1;
        if *i < args.len() {
            Ok(args[*i].clone())
        } else {
            Err(format!("{} requires a value", arg_name))
        }
    }

    pub fn from_args() -> Result<Self, String> {
        let args: Vec<String> = std::env::args().collect();

        // Check for help flag anywhere in arguments
        if args.len() < 2 || args.iter().any(|arg| arg == "--help" || arg == "-h") {
            Self::print_help(&args[0]);
            return Err("".to_string());
        }

        let mut config = Config::default();
        let mut parquet_dir_provided = false;

        // Parse arguments
        let mut i = 1;
        while i < args.len() {
            match args[i].as_str() {
                "--parquet-dir" => {
                    let path = Self::parse_arg_value(&args, &mut i, "--parquet-dir")?;
                    config.parquet_dir = PathBuf::from(path);
                    parquet_dir_provided = true;
                }
                "--region" => {
                    config.region = Self::parse_arg_value(&args, &mut i, "--region")?;
                }
                "--hostname" => {
                    config.hostname = Self::parse_arg_value(&args, &mut i, "--hostname")?;
                }
                "--output-dir" => {
                    let dir = Self::parse_arg_value(&args, &mut i, "--output-dir")?;
                    config.output_dir = PathBuf::from(dir);
                }
                "--workers" => {
                    let value = Self::parse_arg_value(&args, &mut i, "--workers")?;
                    config.num_workers = value.parse().map_err(|_| {
                        format!("--workers must be a positive number, got: {}", value)
                    })?;
                    if config.num_workers == 0 {
                        return Err("--workers must be greater than 0".to_string());
                    }
                }
                "--batch-size" => {
                    let value = Self::parse_arg_value(&args, &mut i, "--batch-size")?;
                    config.batch_size = value.parse().map_err(|_| {
                        format!("--batch-size must be a positive number, got: {}", value)
                    })?;
                    if config.batch_size == 0 {
                        return Err("--batch-size must be greater than 0".to_string());
                    }
                }
                "--log-level" => {
                    let level = Self::parse_arg_value(&args, &mut i, "--log-level")?;
                    config.log_level = Self::parse_log_level(&level)?;
                }
                "--max-parallel-files" => {
                    let value = Self::parse_arg_value(&args, &mut i, "--max-parallel-files")?;
                    config.max_parallel_files = value.parse().map_err(|_| {
                        format!(
                            "--max-parallel-files must be a positive number, got: {}",
                            value
                        )
                    })?;
                    if config.max_parallel_files == 0 {
                        return Err("--max-parallel-files must be at least 1".to_string());
                    }
                }
                _ => {
                    return Err(format!(
                        "Unknown option: {}\n\nRun with --help for usage information",
                        args[i]
                    ))
                }
            }
            i += 1;
        }

        if !parquet_dir_provided {
            return Err(
                "Missing required argument: --parquet-dir\n\nRun with --help for usage information"
                    .to_string(),
            );
        }

        Ok(config)
    }
}
