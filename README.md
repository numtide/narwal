# Narwal

The [S3] bucket behind https://cache.nixos.org contains more than 1 billion files requiring more than 600 TB of storage.
This project is yet another attempt at garbage collecting that behemoth.

To achieve this, we are looking to build the simplest solution possible.

## High-level Overview

So our plan looks like this:

1. Setup a [Postgres] DB.
2. Index the current state of the S3 bucket by leaning on the [S3 Inventory Service].
3. Index new uploads to the S3 bucket via [S3 Notification Events] over [SQS].
4. Develop GC strategies with the indexed and real-time data available in Postgres.
5. Pause Hydra, apply the GC strategy, unpause Hydra.
6. Do this on an ongoing basis and not once every 20 years.

Before removing _anything_ from https://cache.nixos.org, we want to be extra sure it's not going to cause any issues.

To that end we will be working closely with the NixOS Infra team as we get the initial indexing up and running.
And as we begin developing GC strategies, we will work closely with the Hydra team and anyone else
interested in helping us to develop and test them.

We want as many eyes as possible on this before we pull the trigger.

## Progress

- [x] Setup a Postgres DB.
- [ ] Index the current state of the S3 bucket by leaning on the [S3 Inventory Service] (In Progress).
- [ ] Index new uploads to the S3 bucket via [S3 Notification Events] over [SQS] (In Progress).
- [ ] Develop GC strategies with the indexed and real-time data available in Postgres (In Progress).
- [ ] Pause Hydra, apply the GC strategy, unpause Hydra.
- [ ] Do this on an ongoing basis and not once every 20 years.

[S3 Inventory Service]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/storage-inventory.html
[S3 Notification Events]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/EventNotifications.html
[S3]: https://aws.amazon.com/s3/
[SQS]: https://aws.amazon.com/sqs/
[Postgres]: https://www.postgresql.org/
