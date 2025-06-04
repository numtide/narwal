Some notes and ideas on where to take this. The idea is to keep building on top, until we have a fully self-hostable, fully open-source solution that works at scale. We don't want to just build a product, but also to evolve Nix itself to solve some of existing issues.

# Roadmap

## Add a HTTP read frontend

The current impleentation only supports uploads. We also want to respond to queries from the clients.

## Add integrity checks for existing caches

Once we have all the content, we can look for dangling references, incorrectly-formatted files, etc...

## Smart HTTP queries

When the client wants to check what files exists in a binary cache, the current approach is to set a HEAD request for each of the items. When the list is long, the overhead of the HTTP headers and roundtrip for each request can be fairly slow. The actual number depends on the RTT between the client and the cache.

What we propose is to add a new endpoint, just like the smart git protocol, to query N items at once.

This will require a RFC, and changing the Nix clients as well.

Ref: https://git-scm.com/docs/protocol-v2

## Smart upload protocol

We want to change the Nix client and protocol, to handle smart uploads. The current Nix-copy-closure isn't very smart, which leads to slow uploads.

The end-result would be similar to what the `cachix upload` or `attic upload` tools are doing, but baked directly into Nix. This would require changing the HTTP Store protocol to allow upload sessions, so a closure can be uploaded at once, and validated at the very end.

Multi-part uploads sessions that contain the nar and narinfo

More details TBD.

## Nix profiles / GC roots protocol

Local nix clients can record "profiles", which are names, with generations, mapping to store paths. And those acts as GC roots.

We want to extend the remote store protocol to add support for those, so clients can both upload and pin store paths.

## Support for deploy tools

When deploying system closures, we want to make sure the closure doesn't get deleted before it reaches the intended destination.

This is an extension of the Nix profiles support, but with a timeout on the GC root. That way the deploy tool can upload content to the binary cache, trigger/schedule the deploy.

## Support for Federation

The NixOS community currently is relyant on a single cache: https://cache.nixos.org.

We envision a model where multiple institutions would be rebuilding nixpkgs, and providing their own binary caches.

The client would then contact multiple caches, and only download entires that have the same content-addressable output.

This would distribute the client load from a central location, to multiple ones. And also encourage users to fix build reproducibility issues.

## Support for replication

Add an endpoint that provides a stream of new cache entries, to make replication easier.

## Support for auth

Add standard enterprise support for authentication. Basic auth, Oauth, etc...

## /metrics endpoint

## Content de-duplication

Again, extend the protocol to allow more incremental uploads and downloads.

See Snix.

## Abstract storage providers

Allow for other backends than the Postgres/S3 combo.

## Better signing keys

Have a system where we can easily rotate keys.

## Compliance (SLSA4, ...)

Extend the narinfo to also hold information on what builder uploaded the file.

Auditing
...

# Research

- Attic
- Docker registries
- Styx
