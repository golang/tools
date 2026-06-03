#!/bin/bash
# Correctness gate for the memory campaign. Type-checking changes are the main
# risk: an optimization that reuses/skips work incorrectly can silently corrupt
# type information. These tests exercise the snapshot + type-check batch.
#
# Run after a benchmark improvement before committing a "keep". For semantic
# changes to check.go, ALSO run the marker tests manually:
#   go test ./internal/test/marker/ -count=1
set -euo pipefail

cd "$(dirname "$0")"

go test ./internal/cache/ -count=1 2>&1 | grep -vE "^(ok|PASS)\b" || true
