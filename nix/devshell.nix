{ pkgs, perSystem, ... }:
perSystem.self.nix-binary-cache.overrideAttrs (old: {
  env = old.env // {
    GOROOT = "${old.passthru.go}/share/go";
  };

  nativeBuildInputs =
    old.nativeBuildInputs
    ++ (with pkgs; [
      delve
      pprof
      gotools
      golangci-lint
    ]);

  shellHook = ''
    # this is only needed for hermetic builds
    unset GO_NO_VENDOR_CHECKS GOSUMDB GOPROXY GOFLAGS
  '';
})
