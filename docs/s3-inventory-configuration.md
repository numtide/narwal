# AWS S3 Inventory Configuration for Narwal

This document explains how AWS S3 Inventory is configured for the Nix binary cache to enable efficient garbage collection with Narwal.

## Overview

AWS S3 Inventory provides scheduled reports about objects and their metadata for the `nix-cache` S3 bucket. These inventory reports are essential for Narwal to index the nearly billion objects in the cache without expensive LIST operations.

## Production Configuration (from NixOS Infrastructure)

The actual S3 inventory is configured in the [NixOS infrastructure terraform](https://github.com/NixOS/infra/blob/main/terraform/cache_inventory.tf):

### 1. Inventory Destination Bucket

```hcl
resource "aws_s3_bucket" "cache_inventory" {
  provider = aws.us
  bucket   = "nix-cache-inventory"

  lifecycle_rule {
    enabled = true

    # Only keep the last 30 days
    expiration {
      days = 30
    }
  }
}
```

### 2. S3 Bucket Inventory Resource

```hcl
resource "aws_s3_bucket_inventory" "cache_inventory" {
  provider = aws.us

  bucket = aws_s3_bucket.cache.id
  name   = "nix-cache-inventory"

  included_object_versions = "Current"

  optional_fields = [
    "ETag",
    "LastModifiedDate",
    "Size",
    "StorageClass",
  ]

  schedule {
    frequency = "Daily"
  }

  destination {
    bucket {
      account_id = "080433136561"
      format     = "Parquet"
      bucket_arn = aws_s3_bucket.cache_inventory.arn
    }
  }
}
```

## Configuration Details

### Source Bucket

- **Bucket**: `nix-cache` (the main Nix binary cache)
- **Region**: US (typically us-east-1)

### Destination Bucket

- **Bucket**: `nix-cache-inventory`
- **Prefix**: The inventory files are stored with prefix `nix-cache/nix-cache-inventory/`
- **Lifecycle**: Files are automatically deleted after 30 days to control storage costs

### Inventory Settings

- **Schedule**: Daily (generates once per day at approximately 01:00 UTC)
- **Format**: Parquet (efficient columnar format for big data processing)
- **Object Versions**: Current only (excludes previous versions)
- **Optional Fields Included**:
    - `ETag`: Object checksum for integrity verification
    - `LastModifiedDate`: Timestamp when object was last modified
    - `Size`: Object size in bytes
    - `StorageClass`: Storage class (STANDARD, STANDARD_IA, etc.)

## File Structure

The inventory creates the following structure in the destination bucket:

```
s3://nix-cache-inventory/
└── nix-cache/
    └── nix-cache-inventory/
        ├── 2025-06-03T01-00Z/         # Daily report directory
        │   ├── manifest.json           # Metadata about this inventory
        │   └── manifest.checksum       # MD5 checksum of manifest
        ├── 2025-06-04T01-00Z/
        │   └── ...
        └── data/                       # Actual inventory data
            ├── {uuid1}.parquet
            ├── {uuid2}.parquet
            └── ... (~516 files per day)
```

## Manifest File Format

Each daily inventory includes a `manifest.json` file:

```json
{
  "sourceBucket": "nix-cache",
  "destinationBucket": "arn:aws:s3:::nix-cache-inventory",
  "version": "2016-11-30",
  "creationTimestamp": "1748826000000",
  "fileFormat": "Parquet",
  "fileSchema": "message s3.inventory {
    required binary bucket (STRING);
    required binary key (STRING);
    optional int64 size;
    optional int64 last_modified_date (TIMESTAMP_MILLIS);
    optional binary e_tag (STRING);
    optional binary storage_class (STRING);
  }",
  "files": [
    {
      "key": "nix-cache/nix-cache-inventory/data/{uuid}.parquet",
      "size": 176058964,
      "MD5checksum": "55bf7c536ac7b6de3c79ff4520a0fa7d"
    }
  ]
}
```

## Parquet Schema

The Parquet files contain records with the following schema:

| Field                | Type             | Required | Description                             |
| -------------------- | ---------------- | -------- | --------------------------------------- |
| `bucket`             | STRING           | Yes      | Source bucket name (always "nix-cache") |
| `key`                | STRING           | Yes      | Object key/path                         |
| `size`               | INT64            | No       | Object size in bytes                    |
| `last_modified_date` | TIMESTAMP_MILLIS | No       | Last modification timestamp             |
| `e_tag`              | STRING           | No       | Object ETag for integrity               |
| `storage_class`      | STRING           | No       | Storage class                           |

## Scale and Performance

- **Daily inventory size**: ~65GB compressed Parquet files
- **Number of files**: ~516 Parquet files per inventory
- **Total objects indexed**: ~982 million
- **Download time**: ~80 minutes without parallelization
- **Processing includes**:
    - ~275 million `.narinfo` files
    - NAR archives (`.nar`, `.nar.xz`, `.nar.bz2`)
    - Build logs (`.drv.bz2`)
    - File listings (`.ls`)

## IAM Permissions

The S3 inventory service requires write permissions to the destination bucket. For reading inventory files, the ["archeologist" IAM policy](https://github.com/NixOS/infra/blob/main/terraform-iam/archeologist.tf) provides:

```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": ["s3:List*", "s3:Get*"],
            "Resource": [
                "arn:aws:s3:::nix-cache/*",
                "arn:aws:s3:::nix-cache-inventory/*",
                "arn:aws:s3:::nix-cache-log/*"
            ]
        }
    ]
}
```

## Using Inventory with Narwal

### 1. List Available Reports

```bash
narwal inventory list-reports
```

### 2. Get Manifest for a Specific Report

```bash
narwal inventory manifest --report 2025-06-03T01-00Z
```

### 3. Download Inventory Files

```bash
# Download all parquet files for a report
narwal inventory download --report 2025-06-03T01-00Z --workarea ./data

# Download with custom settings
narwal inventory download \
  --bucket nix-cache-inventory \
  --prefix nix-cache/nix-cache-inventory \
  --report 2025-06-03T01-00Z \
  --workarea ./data
```

### 4. Process with turbofetch2

After downloading inventory files, use turbofetch2 to extract narinfo files:

```bash
turbofetch2 --parquet-dir ./data/nix-cache-inventory/data/2025-06-03T01-00Z/
```

### 5. Import into Database

```bash
narwal inventory import --report 2025-06-03T01-00Z
```

## Cost Considerations

- **Storage**: Minimal due to 30-day expiration (~2TB total)
- **S3 API costs**:
    - Inventory generation: Included in S3 pricing
    - GET requests: $0.0004 per 1,000 requests
- **Data transfer**: $0.09/GB (65GB × $0.09 = ~$5.85 per download)
- **Total estimated cost for initial import**: ~$158 (including narinfo downloads)

## Setting Up S3 Inventory for a New Bucket

If you need to set up inventory for a different bucket:

1. Create a destination bucket for inventory files
2. Configure the inventory using AWS Console, CLI, or Terraform:

```bash
aws s3api put-bucket-inventory-configuration \
  --bucket SOURCE_BUCKET \
  --id my-inventory \
  --inventory-configuration '{
    "Destination": {
      "S3BucketDestination": {
        "Bucket": "arn:aws:s3:::DEST_BUCKET",
        "Format": "Parquet"
      }
    },
    "IsEnabled": true,
    "Id": "my-inventory",
    "IncludedObjectVersions": "Current",
    "OptionalFields": ["Size", "LastModifiedDate", "ETag", "StorageClass"],
    "Schedule": {
      "Frequency": "Daily"
    }
  }'
```

3. Wait 24-48 hours for the first inventory to be generated
4. Configure appropriate IAM permissions for reading the inventory

## Best Practices

1. **Use lifecycle rules** on the inventory bucket to automatically delete old reports
2. **Monitor costs** - inventory generation and storage can add up for large buckets
3. **Process incrementally** - use the daily reports to update your index rather than full re-imports
4. **Verify manifest checksums** before processing inventory files
5. **Use parallel downloads** when fetching inventory files to reduce download time

## Troubleshooting

### No Inventory Files Generated

- Check IAM permissions for the S3 service to write to destination bucket
- Verify the inventory configuration is enabled
- Wait 24-48 hours for initial inventory generation

### Missing Optional Fields

- Ensure optional fields are specified in the inventory configuration
- Some objects may not have all optional fields (e.g., very old objects)

### Performance Issues

- Use parallel processing for downloading and parsing Parquet files
- Consider using EC2 instances in the same region to reduce transfer costs
- Process files in batches to manage memory usage

## References

- [AWS S3 Inventory Documentation](https://docs.aws.amazon.com/AmazonS3/latest/userguide/storage-inventory.html)
- [NixOS Infrastructure Repository](https://github.com/NixOS/infra)
- [Narwal Documentation](https://github.com/numtide/narwal)
