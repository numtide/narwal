{ pkgs, ... }:
let
  # Concatenate all SQL UDF files into a single init file at build time
  duckdb-sql-init = pkgs.runCommand "duckdb-sql-init" { } ''
    cat ${../../duckdb}/*.sql > $out
  '';
in
pkgs.writeShellScriptBin "duckdb-local" ''
  exec ${pkgs.duckdb}/bin/duckdb \
    -init "${duckdb-sql-init}" \
    "$@"
''
