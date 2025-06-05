{
  inputs,
  perSystem,
  pkgs,
  ...
}:
{
  imports = [
    inputs.narwal.nixosModules.narwal
  ];

  config = {
    environment.systemPackages = [
      perSystem.narwal.default
      pkgs.awscli2
    ];

    services.narwal = {
      enable = true;
      package = perSystem.narwal.default;
      nginx = {
        enable = true;
        serverName = "narwal-staging.numtide.com";
      };
    };
  };
}
