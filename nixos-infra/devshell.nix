{ pkgs }:
pkgs.mkShell {
  packages = [
    pkgs.awscli2
    pkgs.nixos-anywhere
    pkgs.nixos-rebuild
  ];

  shellHook = ''
    # Used to poke at the NixOS cache. Login with `aws sso login`
    export AWS_CONFIG_FILE=$PWD/aws-config
    export AWS_PROFILE=nixos-archeologist
  '';
}
