{ pkgs, ... }:
let
  # Wrap clickhouse-local to use $PRJ_DATA_DIR/clickhouse with UDFs from project
  # Concatenate all SQL UDF files into a single init file at build time
  clickhouse-sql-init = pkgs.runCommand "clickhouse-sql-init" { } ''
    cat ${../../clickhouse/user_defined}/*.sql > $out
  '';
in
pkgs.writeShellScriptBin "clickhouse-local" ''
  CH_DATA_DIR=$(mktemp -d)

  # Generate config with absolute paths
  cat > "$CH_DATA_DIR/config.xml" <<EOF
  <clickhouse>
      <user_scripts_path>${../../clickhouse/user_scripts}</user_scripts_path>
      <user_defined_executable_functions_config>${../../clickhouse/user_defined}/*.xml</user_defined_executable_functions_config>
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
''
