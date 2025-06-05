# NixOS infra code

Code specific to the NixOS infra and that will eventually be merged with the
github:NixOS/infra repo.

## Hosts

### narwal-staging

Hetzner SX65 storage box located in FSN1-DC1.

We wanted to keep it close to mimas (hydra.nixos.org) to reduce latency for
HEAD requests.

Eventually this host will be moved to the NixOS Foundation Hetzner account.
