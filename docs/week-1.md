# Progress Report - Week 1

Let's try something else; a top-bottom approach to GC. Apply some wishful thinking, fill the blanks, until it works.

We start with the following assumptions:

1. The only architectural change we need to make is to place our service between Hydra and the Cache, for the uploads. We can intercept the uploads, index changes to the bucket, and make the GC operations atomic.
2. Postgres is performant enough to hold metadata for the billion entries of the cache.

Brian prepared the skaffoding for the repo, and then we met in the outskirts of Dublin for 3 days to hack on this thing.

Brian's focus was on building out the core while I was mostly looking at infrastructure. The idea is that we can each split our work and meet in the middle.

## Brian

Brian created the following things:

- Classified all the types of files we're getting from nix-copy-closure. There might be more types of files we discover in the current cache index later.
- Created a Postgres schema for the metadata, and GC roots.
- Created a nix-copy-closure compatible HTTP server. The idea is that when Hydra uploads something, it gets forwarded to S3 and we record the entry in Postgres. Narinfo files also get parsed to extract the references.
- Implemented the bloomfilter GC we had in mind. It's pretty cool, we can hold a billion references in 4GB of RAM. Then all items not in the bloomfilter can be confidently removed. See https://hur.st/bloomfilter/?n=1000000000&p=1.0E-7&m=&k=
- TODO: Did I miss anything else Brian?

In 3 days Brian created a backward-compatible binary cache with GC. Not something that we roll out in production, but still pretty cool.

## Jonas

Before digging into anything, I spent some time reviewing the previous work. A
lot of good tools have been built at https://git.snix.dev/snix/snix/src/branch/canon/contrib

On the NixOS AWS account, I fixed the archivist profile. It's a IAM profile that gives read-only access so Brian and I can poke at the S3 buckets safely.

Then created a small tool that downloads reports from AWS S3 Inventory. Those contain a snapshot of all the files in the s3://nix-cache bucket for a given day, which is going to be useful to hydrate the Postgres database.

Then deployed a new SX65 box on Hetzner in Germany. Put the box in the same DC as Hydra. Because we have an index of all the files that exists, we should be able to answer HEAD requests with much better latancy than the current Germany <-> us-east-1 roundtrip. Once we're happy with the setup, we can also transfer to box ownership to the NixOS Foundation easily.

Then from the box, downloaded a report. That's 65GB of 516 Parquet files. We get the full index of the cache for $6.0 of egress fees. We will need to run a couple of those for the initial import. Or later if the DB crashes. But still relatively cheap.

And then from there I spent some time playing with Clickhouse. Clickhouse is pretty incredible. There is almost a billion of files in the bucket. A quarter of those are narinfo files. It finds the biggest narinfo from a billion entries in 30s. Then answer is `kr0n0kfb7is092ykpxmwds4jjmkbsgf6.narinfo` by the way. At a whopping 472.82 KiB for a "closure-info" derivation.

## What's next

We have a working system. We're ready to import the metadata and load the Postgres instance. That will allow us to validate our second assumption.

I still have work to do to load the release channel and put those into GC roots in the DB. Luckily Edef already did most of the work there with her fetchroots tool.

Point (1) is still in the air. If it doesn't work out we will have to shutdown Hydra for the duration of the GC. Not ideal but still acceptable if it happens once every few months.

We still have a lot of testing to do before we, and the infra team, feel comfortable running this in production.
