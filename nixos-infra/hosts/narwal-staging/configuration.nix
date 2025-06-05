{ inputs, pkgs, ... }:
{
  imports = [
    "${inputs.disko}/module.nix"
    ./disko.nix
    inputs.srvos.nixosModules.hardware-hetzner-online-amd
    inputs.srvos.nixosModules.mixins-nginx
    inputs.srvos.nixosModules.server
  ];

  config = {
    nixpkgs.hostPlatform = "x86_64-linux";

    networking.hostName = "narwal-staging";

    systemd.network.networks."10-uplink".networkConfig.Address = "2a01:4f8:10a:3598::2/64";

    users.users.root.openssh.authorizedKeys.keyFiles = [
      ../../users/zimbatm.keys
      ../../users/brianmcgee.keys
    ];

    environment.systemPackages = [
      pkgs.htop
      pkgs.kitty.terminfo
      pkgs.tmux
    ];

    system.stateVersion = "25.05";
  };
}
