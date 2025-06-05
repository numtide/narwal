#!/usr/bin/env bash
# shellcheck disable=SC2154,SC1091
set -euo pipefail

# shellcheck source=env.sh
source "$(dirname "$0")"/env.sh

nixos-anywhere --flake ".#$hostname" "root@$fqdn" "$@"
