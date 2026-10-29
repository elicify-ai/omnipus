#!/usr/bin/env bash
#
# run-gateway-sharded.sh — compile-once, run-sharded gateway tests.
#
# Used by the gateway shard in .github/workflows/pr.yml.
#
# Compiles ./pkg/gateway once, partitions the test list into N shards
# (deterministic by test name hash), runs the shards in parallel, and
# merges results with the same carve-outs the non-sharded path uses.
#
# Exit code and output are suitable for the existing flake-filter blocks.
#
# Usage:
#   scripts/run-gateway-sharded.sh --shards 4 --timeout 75m --tags goolm,stdjson
#
# Outputs the combined test log to stdout. Exits non-zero on real failure.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SHARDS=4
TIMEOUT="75m"
TAGS="goolm,stdjson"
OUT_LOG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --shards) SHARDS="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    --tags) TAGS="$2"; shift 2 ;;
    --out) OUT_LOG="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 64 ;;
  esac
done

cd "$ROOT"

# Ensure SPA stub exists (gateway browser tests need it)
mkdir -p pkg/gateway/spa/assets
printf '<!doctype html>\n' > pkg/gateway/spa/index.html || true
touch pkg/gateway/spa/assets/.keep || true

echo "=== gateway sharded: compile test binary ===" >&2
COMPILE_START=$(date +%s)
CGO_ENABLED=0 go test -c -tags "$TAGS" -o /tmp/gateway.test ./pkg/gateway/ 2>&1
COMPILE_END=$(date +%s)
echo "compile: $((COMPILE_END-COMPILE_START))s" >&2

echo "=== gateway sharded: partition ${SHARDS} ways ===" >&2
# Use the python partitioner; it emits one regex per line
mapfile -t REGEXES < <(python3 scripts/gateway-test-shards.py list "$SHARDS" --binary /tmp/gateway.test)

# Run shards in parallel
PIDS=()
LOGS=()
for i in "${!REGEXES[@]}"; do
  LOG="/tmp/gw-shard-${i}.log"
  LOGS+=("$LOG")
  echo "shard $i: starting (regex len ${#REGEXES[$i]})" >&2
  # Each shard gets its own -timeout budget; the overall job still has headroom.
  # We run with -p 1 inside the shard (same discipline as the non-sharded path).
  (
    set +e
    /tmp/gateway.test -test.v -test.timeout "$TIMEOUT" -test.parallel 1 -test.run "${REGEXES[$i]}" >"$LOG" 2>&1
    echo "SHARD_EXIT=$?" >> "$LOG"
  ) &
  PIDS+=($!)
done

echo "=== gateway sharded: waiting for ${#PIDS[@]} shards ===" >&2
for pid in "${PIDS[@]}"; do
  wait "$pid" || true
done

# Merge logs in shard order (deterministic for flake filter)
COMBINED="/tmp/gw-combined.log"
: > "$COMBINED"
ANY_FAIL=0
for i in "${!LOGS[@]}"; do
  LOG="${LOGS[$i]}"
  if [ -f "$LOG" ]; then
    # Annotate shard boundaries for diagnostics
    echo "=== SHARD $i START ===" >> "$COMBINED"
    cat "$LOG" >> "$COMBINED"
    echo "=== SHARD $i END ===" >> "$COMBINED"
    # Detect shard-level failure (non-zero exit recorded at end of log)
    if grep -q '^SHARD_EXIT=[^0]' "$LOG" 2>/dev/null; then
      ANY_FAIL=1
    fi
    # Also surface bare FAIL lines and DATA RACE for the carve-outs
    if grep -aq 'DATA RACE' "$LOG" 2>/dev/null; then
      echo "DATA RACE seen in shard $i" >&2
      ANY_FAIL=1
    fi
  fi
done

# If any shard wrote a package-level FAIL line, treat as failure for the filter
if grep -aq '^FAIL[[:space:]]' "$COMBINED" 2>/dev/null; then
  ANY_FAIL=1
fi

if [ -n "$OUT_LOG" ]; then
  cp "$COMBINED" "$OUT_LOG"
fi

cat "$COMBINED"

# Propagate failure so the workflow step fails and the flake filter runs
if [ "$ANY_FAIL" -ne 0 ]; then
  exit 1
fi
exit 0
