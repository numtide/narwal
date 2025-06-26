#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=env.sh disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"/env.sh

if [[ $# = 0 ]]; then
  set -- switch
fi

# shellcheck disable=SC2154
nixos-rebuild --flake ".#$hostname" --target-host "root@$fqdn" "$@"
