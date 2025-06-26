#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=env.sh disable=SC1091
source "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")"/env.sh

# shellcheck disable=SC2029 disable=SC2154
ssh "root@$fqdn" "$@"
