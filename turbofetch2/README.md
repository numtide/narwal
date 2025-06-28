# turbofetch2

A high-performance bulk S3 object fetcher for downloading narinfo files from the Nix cache.

## Usage

```bash
turbofetch2 --parquet-dir <DIR> [options]
```

### Required Arguments

- `--parquet-dir <DIR>` - Directory containing S3 inventory parquet files (downloaded by narwal)

### Options

- `--region <REGION>` - AWS region (default: us-east-1)
- `--hostname <HOST>` - S3 hostname (default: nix-cache.s3.amazonaws.com)
- `--output-dir <DIR>` - Output directory for narinfo files (default: narinfo)
- `--workers <N>` - Number of worker threads (default: varies by system)
- `--batch-size <N>` - Batch size for processing (default: varies)
- `--log-level <LEVEL>` - Log level: error, warn, info, debug, trace (default: info)
- `--help` - Show help message

## Example

```bash
# Process parquet files from narwal download
turbofetch2 --parquet-dir /path/to/parquet/files

# Use more workers and debug logging
turbofetch2 --parquet-dir /path/to/parquet/files --workers 16 --log-level debug

# Save to a different directory
turbofetch2 --parquet-dir /path/to/parquet/files --output-dir /path/to/output
```

## Integration with Narwal

This tool is designed to work with parquet files downloaded by the narwal inventory tool:

```bash
# First, download S3 inventory data with narwal
narwal inventory download --bucket nix-cache-inventory --prefix data/ --report 2025-06-03T01-00Z

# Then process the downloaded parquet files with turbofetch2
turbofetch2 --parquet-dir /path/to/workarea/bucket/data/2025-06-03T01-00Z/
```

## Features

- Parallel downloading with configurable worker threads
- Atomic file writes to prevent partial reads
- Automatic retry on failures
- Enhanced progress tracking with real-time statistics
- Skips already downloaded files

Based on Edef's https://git.snix.dev/snix/snix/contrib/turbofetch
