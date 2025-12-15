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

[Hydra]: https://hydra.nixos.org/
[Hydra Queue Runner]: https://github.com/NixOS/hydra/tree/master/hydra-queue-runner
[Simon Hauser]: https://github.com/Conni2461
[S3 Inventory Service]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/storage-inventory.html
[S3 Notification Events]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/EventNotifications.html
[S3]: https://aws.amazon.com/s3/
[SQS]: https://aws.amazon.com/sqs/
[Postgres]: https://www.postgresql.org/
