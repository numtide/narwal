# turbofetch2

A high-performance bulk S3 object fetcher for downloading narinfo files from the Nix cache.

## Usage

```bash
turbofetch2 --job-file <FILE> [options]
```

### Required Arguments

- `--job-file <FILE>` - File containing nixbase32-encoded narinfo hashes to fetch (one per line)

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
# Download narinfo files with default settings
turbofetch2 --job-file narinfo-hashes.txt

# Use more workers and debug logging
turbofetch2 --job-file narinfo-hashes.txt --workers 16 --log-level debug

# Save to a different directory
turbofetch2 --job-file narinfo-hashes.txt --output-dir /path/to/output
```

## Features

- Parallel downloading with configurable worker threads
- Atomic file writes to prevent partial reads
- Automatic retry on failures
- Enhanced progress tracking with real-time statistics
- Skips already downloaded files

Based on Edef's https://git.snix.dev/snix/snix/contrib/turbofetch
