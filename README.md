# Narwal

The [S3] bucket behind https://cache.nixos.org contains more than **1 billion files** requiring more than **600 TB** of storage.
This project is yet another attempt at garbage collecting that behemoth.

## History

We started out in the summer of 2025 building a write-through proxy that would sit between [Hydra] and the [S3] bucket
during upload, parsing Narinfo files and storing the metadata in a [Postgres] db.

Combined with an historical import process based on the [S3 Inventory Service], this would have allowed a real-time
view of every store path within the cache and how they related to each other.
From there, we could develop GC strategies.

We got pretty far along this path before a pause due to other commitments. When we returned to finish it, we
quickly realised that a rewrite of the [Hydra Queue Runner] would introduce architectural changes that would mean
a write-through proxy was no longer appropriate.

So we shifted gears and adapted the approach to work with [S3 Notification Events] instead to track changes to the
bucket.

This lasted little more than a week before [Simon Hauser] pointed out in the bi-weekly queue runner meeting that
"Hydra should have all this state".

## Current Status

We are currently investigating the assertion made by Simon.
So far it seems that Hydra does indeed have a record of _99.5%_ of the store paths ever uploaded to the cache.

What it does not have (to the best of our understanding) is knowledge of how those paths relate to each other.
We are currently investigating what it would take to import that history and maintain it going forward.

In parallel, we have begun interrogating the inventory data and downloaded Narinfos we already have to see if there are any
quick wins.

A proper write-up of those findings will be published in the near-future, along with the underlying datasets
so that others can verify them and perhaps identify other opportunities.

> [!NOTE]
> This repository still retains some of the server functionality we developed, but is now mostly focused on inventory
> analysis and export.

## Simple GC

The Simple GC command removes store paths from an S3-based binary cache. It reads a list of GC targets from a
parquet file and deletes the corresponding `.narinfo` and `.nar` files from S3.

### Usage

```bash
narwal gc simple <input_file> <output_file> [flags]
```

**Arguments:**

- `input_file` - Parquet file containing `NarInfoRecord` entries identifying store paths to delete
- `output_file` - Parquet file where removal results will be written

### AWS Authentication

The command requires AWS credentials to be configured in the usual ways.

We have been using the profile defined in `./aws-config` for development and testing:

```env
export AWS_CONFIG_FILE=$PWD/aws-config
export AWS_PROFILE=nixos-archeologist
```

After running `aws sso login` you can use the `narwal gc simple` command as normal.

### Input Format

The input file must be a parquet file with the `NarInfoRecord` schema found in `pkgs/inventory/types.go`.

From each record, we construct two S3 keys to delete:

- `<hash>.narinfo` - The narinfo itself
- `nar/<file_hash>.nar<compression>` - The compressed NAR archive it refers to

### Output Format

The output parquet file contains `RemovalRecord` entries:

| Field        | Description                                                  |
| ------------ | ------------------------------------------------------------ |
| `key`        | S3 object key that was targeted                              |
| `store_path` | Full Nix store path (e.g., `/nix/store/abc...-hello-2.12.1`) |
| `not_found`  | `true` if the object didn't exist in S3                      |
| `error`      | Error message if deletion failed                             |

> [!NOTE]  
> A `NoSuchKey` error is not considered a failure.
> Multiple narinfos may refer to the same NAR archive, with the NAR being removed early in the process and later
> attempts to remove it failing with `NoSuchKey`.
> It also means it is safe to retry removal several times with the same or evolving input file.

### Stats Output

On completion, the command outputs JSON stats to stdout:

```json
{
    "targets": {
        "nar_infos": 5000,
        "missing_in_s3": {
            "nars": 12,
            "nar_infos": 5
        }
    },
    "removals": {
        "nars": 5000,
        "nar_infos": 5000,
        "errors": 0
    }
}
```

The command exits with a non-zero status if:

- Any objects were missing in S3 (possible prior deletion or data inconsistency)
- Any removal errors occurred

### Dry Run Mode

Use `--dry-run` to verify which files exist without deleting them:

```bash
narwal gc simple targets.parquet results.parquet --dry-run --s3.bucket my-cache
```

In dry-run mode, the command checks for the presence of each target file using HEAD requests.
Missing files are reported in the output and stats, allowing you to identify data inconsistencies
before performing actual deletions.

[Hydra]: https://hydra.nixos.org/
[Hydra Queue Runner]: https://github.com/NixOS/hydra/tree/master/hydra-queue-runner
[Simon Hauser]: https://github.com/Conni2461
[S3 Inventory Service]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/storage-inventory.html
[S3 Notification Events]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/EventNotifications.html
[S3]: https://aws.amazon.com/s3/
[SQS]: https://aws.amazon.com/sqs/
[Postgres]: https://www.postgresql.org/
