{
  pkgs,
  flake,
  ...
}:
pkgs.callPackage ./package.nix {

  # there's no good way of tying in the version to a git tag or branch
  # so for simplicity's sake we set the version as the commit revision hash
  # we remove the `-dirty` suffix to avoid a lot of unnecessary rebuilds in local dev
  versionSuffix = "-${pkgs.lib.removeSuffix "-dirty" (flake.shortRev or flake.dirtyShortRev)}";
}
