{
  pkgs,
  inputs,
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
      perSystem.self.clickhouse-local
    ];

  shellHook = ''
    # this is only needed for hermetic builds
    unset GO_NO_VENDOR_CHECKS GOSUMDB GOPROXY GOFLAGS

    export HYDRA_SRC=${inputs.hydra}

    # sqlc has a bug when referring to absolute paths for schemas
    # this is a workaround for now
    ln -sf ${inputs.hydra}/src/sql/hydra.sql "$PRJ_ROOT/pkg/queries/hydra.sql"
  '';
})
