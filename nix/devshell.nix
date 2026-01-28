{
  pkgs,
  perSystem,
  ...
}:
perSystem.self.narwal.overrideAttrs (old: {
  env = old.env // {
    GOROOT = "${old.passthru.go}/share/go";
  };

  doChecks = false;

  nativeBuildInputs =
    old.nativeBuildInputs
    ++ old.nativeCheckInputs
    ++ (with pkgs; [
      # go
      delve
      pprof
      gotools
      golangci-lint
      enumer

      # LSPs
      nil

      # tooling
      awscli2
      curl
      graphviz
      parquet-tools
      pqrs # parquet inspection
      openssl
      badger
      duckdb
      sqlc
    ])
    ++ [
      perSystem.self.duckdb
    ];

  shellHook = ''
    # this is only needed for hermetic builds
    unset GO_NO_VENDOR_CHECKS GOSUMDB GOPROXY GOFLAGS
  '';
})
