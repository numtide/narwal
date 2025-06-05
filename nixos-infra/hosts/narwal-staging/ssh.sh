#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=env.sh
source "$(dirname "$0")"/env.sh

# shellcheck disable=SC2029
ssh "root@$fqdn" "$@"
