#!/usr/bin/env bash
set -ex

TMPDIR=$(mktemp -d)
trap 'rm -rf $TMPDIR' EXIT

clickhouse-local \
    --path "$TMPDIR" \
    --queries-file /home/brian/Development/com/github/numtide/narwal/bin/daily-summary.sql
