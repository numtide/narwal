{
  pkgs,
  ...
}:
pkgs.python3Packages.callPackage ./package.nix { }
