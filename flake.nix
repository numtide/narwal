{
  description = "Nix Binary Cache";

  # Add all your dependencies here
  inputs = {

    blueprint = {
      url = "github:numtide/blueprint";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.systems.follows = "systems";
    };

    nixpkgs.url = "github:NixOS/nixpkgs?ref=nixos-unstable";

    systems.url = "github:nix-systems/default-linux";

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  # Load the blueprint
  outputs =
    inputs:
    inputs.blueprint {
      prefix = "nix/";
      inherit inputs;
    };
}
