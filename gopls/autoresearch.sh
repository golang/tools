#!/bin/bash
# Runs the synthetic syntax-error memory-spike benchmark and emits METRIC lines.
#
# Primary metric: peak_heap_GB (lower is better) -- the transient heap
# high-water mark during the re-type-check of the invalidated closure.
set -euo pipefail

cd "$(dirname "$0")"

# Fast precheck (<2s): catch compile errors before paying for the full run.
go vet ./internal/cache/ ./internal/test/integration/bench/ >/dev/null 2>&1

rm -rf /tmp/synthcache
LOG=$(mktemp)
SYNTH_DIR=/tmp/synthmod GOPLSCACHE=/tmp/synthcache \
  go test ./internal/test/integration/bench/ \
  -run='^$' -bench=BenchmarkSynthSyntaxErrorSpike -benchtime=1x -timeout=20m \
  >"$LOG" 2>&1 || { echo "BENCH FAILED"; tail -40 "$LOG"; exit 1; }

# The result line looks like:
#   BenchmarkSynthSyntaxErrorSpike-11  1  2447496291 ns/op  0.7463 churn_GB/op \
#     0 gc_cycles/op  2039603200 peak_heap_bytes  ... settled_inuse_bytes
line=$(grep -E "^BenchmarkSynthSyntaxErrorSpike-" "$LOG" | tail -1)
if [ -z "$line" ]; then echo "NO RESULT LINE"; tail -40 "$LOG"; exit 1; fi

# Extract "<number> <name>" pairs robustly by scanning fields.
emit() { # $1 = bench-column name, $2 = METRIC name, $3 = divisor
  local v
  v=$(echo "$line" | awk -v k="$1" '{for(i=1;i<=NF;i++) if($i==k) print $(i-1)}')
  if [ -n "$v" ]; then
    echo "METRIC $2=$(awk -v x="$v" -v d="$3" 'BEGIN{printf "%.4f", x/d}')"
  fi
}

emit peak_heap_bytes      peak_heap_GB    1e9
emit churn_GB/op          churn_GB        1     # already in GB
emit settled_inuse_bytes  settled_GB      1e9
emit peak_sys_bytes       peak_sys_GB     1e9

rm -f "$LOG"
