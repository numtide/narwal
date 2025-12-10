{
  pkgs,
  perSystem,
  ...
}:
let

  inherit (pkgs) lib;

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

                max_wal_size = 10GB  # increase from 1GB to improve import performance
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
      pqrs # parquet inspection
      sqlc # type-safe queries
      openssl
      badger
      clickhouse

      # rust tooling
      cargo
      clippy
      rustfmt
    ]);

  shellHook = ''
    # this is only needed for hermetic builds
    unset GO_NO_VENDOR_CHECKS GOSUMDB GOPROXY GOFLAGS
  '';
})
