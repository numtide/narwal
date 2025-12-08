#!/usr/bin/env bash
set -ex

# Use a temporary directory for ClickHouse local data
TMPDIR=$(mktemp -d)
trap 'rm -rf $TMPDIR' EXIT

clickhouse-local \
    --path "$TMPDIR" \
    --queries-file /home/brian/Development/com/github/numtide/narwal/bin/compare-inventory.sql
