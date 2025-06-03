# Narwal

The S3 bucket behind https://cache.nixos.org contains almost a billion files, taking 528 TB of storage. This project is yet another attempt at garbage collecting that beast.

To achieve this, we are looking to build the simplest solution we can think of for the problem that works.

## High-level implementation plan

In order to make garbage collection atomic, we have to intercept uploads. Otherwise there is always a risk for narinfos to be uploaded, that reference items that we just removed from the bucket.

So our plan looks like this:
1. Setup a Postgres DB
2. Have one fat server that receives and indexes the uploads into Postgres, and forwards them to S3.
3. Build an importer tool that traverses the S3 bucket, and indexes all the information into Postgres.
4. Build another tool that can record GC roots into the DB. This will be used to mark releases. (TODO: add more details)

One we have that, we can have a periodic garbage collector that pauses the uploads, traverses the DB and removes all dangling content.

## S3 importer tool

The importer tool downloads the Parquet files from the s3 inventory bucket, then for each type of file does something different.

For the narinfo, it downloads and parses them, to collect the references.

* /nix-cache-info => do not GC :)
* dwarffs?
    * https://github.com/NixOS/infra/issues/484
* `nar/<nar-hash>.nar[.compression]`
* `log/<drv-hash>-<name>.drv` files
* `<drv-hash>.narinfo` files
* `<drv-hash>[-<name>].ls[.xz]` files
* TODO: Did I miss anything?
    * `.drv`?

Then everything gets recorded into Postgres.

## Fastly importer tool

In order to cross-reference the GC with access patterns, we will also write a little tool that imports the Fastly logs and record accesses.

## Upload interceptor

It's going to be a stupid HTTP server with a shared Basic Auth token. Whenever the client uploads to it, we stream the content to S3, and index it in the DB.

Whenever the lock is held in the DB, we let the current uploads finish, and hang on all the new uploads.

## Garbage collector

Whenever the garbage collector is executed, it creates a lock on the DB and prevents new uploads to happen.

After every run, we also record the GC size, list of items, how long it took, and put that back in another Postgres table. And potentially forward it to a metrics / alerts server.

There are two algorithms we have in mind:

### Incremental approach

We can query all items that have no back-references, and delete them.

Once the iteration is done, it should reveal new items with no back-references. Loop until you get zero items to collect.

Back-references are also GC roots. And any item younger than X also counts as a GC root.

The advantage of this approach is that it creates short iterations where the lock needs to be held. The disadvantage is that we have to hold a back-references index, or make those queries expensives. We don't know yet.

### Mark and sweep approach

This is a more traditional apporach used by in-memory garbage collectors.

Go trough all the items, and mark those you want to keep. We're thinking of using a bloom filter to keep that list reasonable in size in memory. Then that creates a negative lookup on all the items that will be planned to delete.

The advantage of this approach is that you can more easily see the results of a dry-run. You get the full plan for deletion.

The disadvantage is that it will be slower to delete the big chunk of items at once.

## Long term archival

Our idea here is simple:

1. Enable bucket versioning
2. Add a lifecycle rule that moves the larger deleted objects to Glacier, and keep the small ones around.
3. DELETE all the objects we want to GC

That way it's still possible to restore those. Until we are really sure we want to delete them.

## Rollout

Before removing anything from cache.nixos.org, we want to be extra sure it's not going to cause any issues. This is not something that we should do alone, and would like to get the help of one or two appointed community members to overlook and validate our efforts.

We will need to have read-only access to s3://nix-cache and s3://nix-cache-inventory. And a big enough EC2 instance to deploy Postgres and the future build interceptor.

The first order of business is to start indexing the bucket. We estimate, based on the previous experiments, that those costs will be in the ballpark of $1000.-. In order for us to not be stuck on that, we set aside budget to re-imburse the NixOS Foundation.

From here on, we can run all sorts of queries. Potentially give read-only access to people interested?

The second part is to re-configure Hydra to upload trough the box. Check that it works well, and that the indexing works well.

Then re-configure the S3 bucket to enable versioning (this is an irreversible operation).

Once everything is up and synching, we generate dry-run plans, inspect them manually.

Once we feel confident, run the actual GC.

## Known risks

* We might run out of steam if the rollout takes too long. I think we should create some sort of schedule to avoid that.
* Is it possible to configure Hydra with a split upload/download cache?
* We might find out that Postgres is too slow for that task. I don't think it's very likely, but let's see.
* We might find out that the upload pauses are causing too much disruption.

## Future work

These things are on our mind, but explicitly out of scope for the first version:

1. It's possible to reduce the pause during Garbage Collection by taking more fancy approach. For now, Hydra will have to wait a bit.
2. Once we have the indices in the DB, it becomes possible to build a smart cache, where you can query N items at once, just like the git smart HTTP protocol. For now, we only intercept the uploads and not the downloads.
3. Content deduplication. We are looking at snix for that type of work.
4. Replace Postgres with something more optimised for the graph type of queries we want to make.

https://pad.lassul.us/narwal-future

## Notes

nix copy options

Generic:

compression = `xz`, `bzip2`, `gzip`, `zstd`, or `none`
write-nar-listing=true
index-debug-info=true

S3:

ls-compression
log-compression
narinfo-compression
