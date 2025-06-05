{
  description = "Narwal NixOS infra";

  # Add all your dependencies here
  inputs = {
    narwal.url = "path:..";

    blueprint.follows = "narwal/blueprint";
    nixpkgs.follows = "narwal/nixpkgs";
    systems.follows = "narwal/systems";

    srvos = {
      url = "github:nix-community/srvos";
      inputs.nixpkgs.follows = "narwal/nixpkgs";
    };

    disko = {
      url = "github:nix-community/disko";
      inputs.nixpkgs.follows = "narwal/nixpkgs";
    };
  };

  # Load the blueprint
  outputs = inputs: inputs.blueprint { inherit inputs; };
}
