#!/usr/bin/env bash
#
# run-agent-sharded.sh — compile-once, run-sharded pkg/agent tests.
#
# Used by the agent shard in .github/workflows/pr.yml (#1487).
#
# Same mechanism as run-gateway-sharded.sh: compile once with `go test -c`,
# partition the test list deterministically (SHA-256 of the test name % N),
# run N copies of the binary in parallel with disjoint -run regexes, and
# merge logs in shard order. The partitioner scripts/gateway-test-shards.py
# is package-agnostic (it only reads a compiled binary's -test.list output);
# only its name is historical. This is a separate script rather than a
# parameterized gateway runner because the two differ in package, binary
# path and stubs (the gateway runner creates the SPA embed stub; pkg/agent
# needs none), and the gateway shard's proven path must not move while it
# awaits its own cut CI measurement.
#
# Exit code and output are suitable for the workflow step's existing
# carve-outs (DATA RACE, --- FAIL, guard failure) and flake filter.
#
# Usage:
#   scripts/run-agent-sharded.sh --shards 4 --timeout 10m --tags goolm,stdjson
#
# Outputs the combined test log to stdout. Exits non-zero on real failure.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SHARDS=4
TIMEOUT="10m"
TAGS="goolm,stdjson"
OUT_LOG=""
BINARY="/tmp/agent.test"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --shards) SHARDS="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    --tags) TAGS="$2"; shift 2 ;;
    --out) OUT_LOG="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    *) echo "unknown arg: $1" >&2; exit 64 ;;
  esac
done

cd "$ROOT"

echo "=== agent sharded: compile test binary ===" >&2
COMPILE_START=$(date +%s)
CGO_ENABLED=0 go test -c -tags "$TAGS" -o "$BINARY" ./pkg/agent/ 2>&1
COMPILE_END=$(date +%s)
echo "compile: $((COMPILE_END-COMPILE_START))s" >&2

echo "=== agent sharded: partition ${SHARDS} ways ===" >&2
mapfile -t REGEXES < <(python3 scripts/gateway-test-shards.py list "$SHARDS" --binary "$BINARY")

PIDS=()
LOGS=()
for i in "${!REGEXES[@]}"; do
  LOG="/tmp/agent-shard-${i}.log"
  LOGS+=("$LOG")
  echo "shard $i: starting (regex len ${#REGEXES[$i]})" >&2
  (
    set +e
    "$BINARY" -test.v -test.timeout "$TIMEOUT" -test.parallel 1 -test.run "${REGEXES[$i]}" >"$LOG" 2>&1
    echo "SHARD_EXIT=$?" >> "$LOG"
  ) &
  PIDS+=($!)
done

echo "=== agent sharded: waiting for ${#PIDS[@]} shards ===" >&2
for pid in "${PIDS[@]}"; do
  wait "$pid" || true
done

# Merge logs in shard order (deterministic for the flake filter)
COMBINED="/tmp/agent-combined.log"
: > "$COMBINED"
ANY_FAIL=0
for i in "${!LOGS[@]}"; do
  LOG="${LOGS[$i]}"
  if [ -f "$LOG" ]; then
    echo "=== SHARD $i START ===" >> "$COMBINED"
    cat "$LOG" >> "$COMBINED"
    echo "=== SHARD $i END ===" >> "$COMBINED"
    if grep -q '^SHARD_EXIT=[^0]' "$LOG" 2>/dev/null; then
      ANY_FAIL=1
    fi
    if grep -aq 'DATA RACE' "$LOG" 2>/dev/null; then
      echo "DATA RACE seen in shard $i" >&2
      ANY_FAIL=1
    fi
  fi
done

# A package-level FAIL line anywhere is a failure for the filter
if grep -aq '^FAIL[[:space:]]' "$COMBINED" 2>/dev/null; then
  ANY_FAIL=1
fi

if [ -n "$OUT_LOG" ]; then
  cp "$COMBINED" "$OUT_LOG"
fi

cat "$COMBINED"

if [ "$ANY_FAIL" -ne 0 ]; then
  exit 1
fi
exit 0
