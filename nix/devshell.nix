{
  pkgs,
  inputs,
  perSystem,
  ...
}:
let

  # Wrap clickhouse-local to use $PRJ_DATA_DIR/clickhouse with UDFs from project
  # Concatenate all SQL UDF files into a single init file at build time
  clickhouse-sql-init = pkgs.runCommand "clickhouse-sql-init" { } ''
    cat ${../clickhouse/user_defined}/*.sql > $out
  '';

  clickhouse-local-wrapped = pkgs.writeShellScriptBin "clickhouse-local" ''
    CH_DATA_DIR="$PRJ_DATA_DIR/clickhouse"
    mkdir -p "$CH_DATA_DIR"

    # Generate config with absolute paths
    cat > "$CH_DATA_DIR/config.xml" <<EOF
    <clickhouse>
        <user_scripts_path>${../clickhouse/user_scripts}</user_scripts_path>
        <user_defined_executable_functions_config>${../clickhouse/user_defined}/*.xml</user_defined_executable_functions_config>
    </clickhouse>
    EOF

    # If user passes --query or -q, run non-interactively with init file first
    # Otherwise, run interactively
    if [[ "$*" == *"--query"* ]] || [[ "$*" == *"-q"* ]]; then
      exec ${pkgs.clickhouse}/bin/clickhouse-local \
        --path="$CH_DATA_DIR" \
        --config-file="$CH_DATA_DIR/config.xml" \
        --queries-file="${clickhouse-sql-init}" \
        "$@"
    else
      exec ${pkgs.clickhouse}/bin/clickhouse-local \
        --path="$CH_DATA_DIR" \
        --config-file="$CH_DATA_DIR/config.xml" \
        --queries-file="${clickhouse-sql-init}" \
        --interactive \
        "$@"
    fi
  '';
in
perSystem.self.narwal.overrideAttrs (old: {
  env = old.env // {
    GOROOT = "${old.passthru.go}/share/go";
  };

  doChecks = false;

  nativeBuildInputs =
    old.nativeBuildInputs
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
      postgresql
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

      clickhouse-local-wrapped
    ];

  shellHook = ''
    # this is only needed for hermetic builds
    unset GO_NO_VENDOR_CHECKS GOSUMDB GOPROXY GOFLAGS

    export HYDRA_SRC=${inputs.hydra}

    # sqlc has a bug when referring to absolute paths for schemas
    # this is a workaround for now
    ln -sf ${inputs.hydra}/src/sql/hydra.sql "$PRJ_ROOT/pkg/hydra/hydra.sql"
  '';
})
