#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=env.sh
source "$(dirname "$0")"/env.sh

if [[ $# = 0 ]]; then
  set -- switch
fi

nixos-rebuild --flake ".#$hostname" --target-host "root@$fqdn" "$@"
