#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=env.sh disable=SC1091
source "$(dirname "$0")"/env.sh

# shellcheck disable=SC2029 disable=SC2154
ssh "root@$fqdn" "$@"
