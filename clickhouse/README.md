# ClickHouse Custom Functions

This directory contains custom User-Defined Functions (UDFs) for ClickHouse that help when working with Narinfos
exported to Parquet format.

## Directory Structure

```
clickhouse/
├── user_defined/       # SQL and XML function definitions
│   ├── function_nixbase32.xml
│   ├── function_nixbase32_wrapper.sql
│   └── function_narurl.sql
└── user_scripts/       # Executable scripts called by UDFs
    └── nixbase32
```

## Functions

### `nixbase32(bytes) -> String`

Converts binary data to Nix's base32 encoding format.

**Usage:**

```sql
SELECT nixbase32(file_hash) FROM narinfos;
```

To reduce the size of Parquet exports, nix hashes are stored as binary blobs rather than their `nixbase32`
representation. This function can be used to convert them to the more human-readable format.

This is a wrapper around `nixbase32_hex` that first converts binary to hex. This is necessary because ClickHouse
does not support binary literals.

### `nixbase32_hex(hex_string) -> String`

Converts a hex-encoded hash to Nix's base32 format. Auto-detects the hash algorithm based on input length:

| Input Length | Algorithm | Output Length |
| ------------ | --------- | ------------- |
| 40 chars     | SHA1      | 32 chars      |
| 64 chars     | SHA256    | 52 chars      |

**Usage:**

```sql
SELECT nixbase32_hex('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855');
```

### `narURL(file_hash, compression) -> String`

Generates a NAR URL path from a file hash and compression type.

Note: The original URL field was removed from the export, since it can be reconstructed from the hash and compression
type.

**Usage:**

```sql
SELECT narURL(file_hash, compression) FROM narinfos;
-- Returns: 'nar/<nixbase32_hash>.nar[.compression]'
```

**Compression mapping:**

- `''` or `'none'` → `.nar`
- `'bzip2'` → `.nar.bz2`
- Other values → `.nar.<compression>` (e.g., `.nar.xz`, `.nar.zstd`)

## Installation

1. Copy `user_scripts/` to your ClickHouse `user_scripts` directory
2. Copy `user_defined/` to your ClickHouse `user_defined` directory
3. Ensure the `nixbase32` script is executable and `nix` is in the PATH
4. Restart ClickHouse or reload functions

The executable UDF requires the `nix` command to be available for hash conversion.
