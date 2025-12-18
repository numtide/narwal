{
  pkgs,
  perSystem,
  ...
}:
let

  inherit (pkgs) lib;

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

  postgres-init = pkgs.writeShellApplication {
    name = "postgres-init";
    runtimeInputs = [ pkgs.postgresql_17 ];
    text =
      let

        initdb = {
          args = [
            "--locale=en_US.UTF-8"
            "--encoding=UTF8"
          ];
          scripts = [
            # create some databases on startup
            "${./devshell/init.sql}"
          ];
        };

      in
      ''
            [ -d "$PGDATA" ] && echo "Postgres data dir exists, exiting" && exit 0

            mkdir -p "$PGDATA"

            eval 'initdb --username="$PGUSER" --pwfile=<(printf "%s\n" "$PGPASS") ${lib.concatStringsSep " " initdb.args}'

            cat >> "$PGDATA/postgresql.conf" <<EOF
                port = $PGPORT
                listen_addresses = '$PGLISTEN'
                unix_socket_directories = '$PGHOST'

                # these settings are to speed up local dev and should not be used in production

                wal_level = minimal
                max_wal_senders = 0
                archive_mode = off
                max_wal_size = 10GB  # increase from 1GB to improve import performance
                checkpoint_timeout = 30min
                maintenance_work_mem = 2GB
        EOF

            echo "CREATE DATABASE ''${PGUSER:-$(id -nu)};" | postgres --single -E postgres

            # execute init scripts
            ${lib.concatStringsSep "\n" (
              map (script: "postgres --single -E postgres < ${script}") initdb.scripts
            )}
      '';
  };
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

      # local dev services
      process-compose
      postgresql_17
      postgres-init
      localstack
      perSystem.self.awslocal

      # tooling
      awscli2
      curl
      goose # db migrations
      graphviz
      parquet-tools
      pqrs # parquet inspection
      sqlc # type-safe queries
      openssl
      badger
      duckdb
      # rust tooling
      cargo
      clippy
      rustfmt
    ])
    ++ [
      clickhouse-local-wrapped
    ];

  shellHook = ''
    # this is only needed for hermetic builds
    unset GO_NO_VENDOR_CHECKS GOSUMDB GOPROXY GOFLAGS
  '';
})
