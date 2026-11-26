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
# Hardened per the run-test review of 2026-10-30 (review-6b-tests.md) —
# the same fixes run-agent-sharded.sh carries; see that script's header
# for the full rationale:
#   - HIGH 1: shards run from the package directory (pkg/gateway has tests
#     reading ../../vite.config.ts and ../providers/catalog/testdata/...),
#     mirroring `go test` cwd semantics; binary and -test.* paths absolute.
#   - HIGH 2: the partition step is checked (exit status and shard count);
#     a failed partition is fatal instead of a green zero-shard run.
#   - HIGH 3: post-run completeness check via the partitioner's `verify`
#     subcommand (every runnable listed test must have a top-level result).
#   - MEDIUM 5: unique mktemp workspace instead of fixed /tmp paths.
#   - MEDIUM 4 (CGO parity): CGO_ENABLED=0 kept for the non-race compile.
#     The pre-sharding step ran under the test-shards JOB env which pins
#     CGO_ENABLED=0 for the unit shards (pr.yml, test-shards job env), so
#     the effective value on CI never changed. Stated difference vs the
#     pre-sharding step: this script's forced 0 additionally compiles the
#     three `//go:build !cgo` test files (browser_webrtc_qa_test.go,
#     rest_agent_reload_race_test.go, rest_more_reload_race_test.go) that
#     a CGO_ENABLED=1 compile would exclude — i.e. the sharded path runs a
#     superset. That is the landed #1487 part-1 behavior and is left as is.
#
# Exit code and output are suitable for the existing flake-filter blocks.
#
# Usage:
#   scripts/run-gateway-sharded.sh --shards 4 --timeout 75m --tags goolm,stdjson
#   scripts/run-gateway-sharded.sh --shards 2 --timeout 3600s --tags goolm,stdjson --race
#
# Outputs the combined test log to stdout. Exits non-zero on real failure.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SHARDS=4
TIMEOUT="75m"
TAGS="goolm,stdjson"
OUT_LOG=""
RACE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --shards) SHARDS="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    --tags) TAGS="$2"; shift 2 ;;
    --out) OUT_LOG="$2"; shift 2 ;;
    --race) RACE=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 64 ;;
  esac
done

cd "$ROOT"

# Unique per-invocation workspace (MEDIUM 5); cleaned up on exit.
WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/gateway-sharded.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT
BINARY="$WORKDIR/gateway.test"

# Ensure SPA stub exists (gateway browser tests need it)
mkdir -p pkg/gateway/spa/assets
printf '<!doctype html>\n' > pkg/gateway/spa/index.html || true
touch pkg/gateway/spa/assets/.keep || true

echo "=== gateway sharded: compile test binary ===" >&2
COMPILE_START=$(date +%s)
if [ "$RACE" -eq 1 ]; then
  # -race requires cgo; force CGO_ENABLED=1 exactly like the race step does.
  CGO_ENABLED=1 go test -c -race -tags "$TAGS" -o "$BINARY" ./pkg/gateway/ 2>&1
else
  CGO_ENABLED=0 go test -c -tags "$TAGS" -o "$BINARY" ./pkg/gateway/ 2>&1
fi
COMPILE_END=$(date +%s)
echo "compile: $((COMPILE_END-COMPILE_START))s" >&2

echo "=== gateway sharded: partition ${SHARDS} ways ===" >&2
# HIGH 2: capture the partitioner output and CHECK it. The old process
# substitution swallowed a failing partitioner and produced a green
# zero-shard run.
if ! PARTITION_OUT="$(python3 scripts/gateway-test-shards.py list "$SHARDS" --binary "$BINARY")"; then
  echo "FATAL: test partitioner failed; refusing to run zero shards (old behavior: green exit with no tests)" >&2
  exit 1
fi
mapfile -t REGEXES <<< "$PARTITION_OUT"
if [ "${#REGEXES[@]}" -ne "$SHARDS" ]; then
  echo "FATAL: partitioner returned ${#REGEXES[@]} shard regexes, expected $SHARDS" >&2
  exit 1
fi

# Run shards in parallel
PIDS=()
LOGS=()
RUN_START=$(date +%s)
for i in "${!REGEXES[@]}"; do
  LOG="$WORKDIR/shard-${i}.log"
  LOGS+=("$LOG")
  echo "shard $i: starting (regex len ${#REGEXES[$i]})" >&2
  # Each shard gets its own -timeout budget; the overall job still has headroom.
  # We run with -p 1 inside the shard (same discipline as the non-sharded path).
  (
    set +e
    # HIGH 1: run from the package directory — `go test` runs a package's
    # tests with the package dir as cwd; the compiled binary must too.
    cd "$ROOT/pkg/gateway" || exit 1
    "$BINARY" -test.v -test.timeout "$TIMEOUT" -test.parallel 1 -test.run "${REGEXES[$i]}" >"$LOG" 2>&1
    echo "SHARD_EXIT=$?" >> "$LOG"
  ) &
  PIDS+=($!)
done

echo "=== gateway sharded: waiting for ${#PIDS[@]} shards ===" >&2
for pid in "${PIDS[@]}"; do
  wait "$pid" || true
done

# Merge logs in shard order (deterministic for flake filter)
COMBINED="$WORKDIR/combined.log"
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
  else
    # A shard that produced no log at all is a hard failure, not silence.
    echo "FATAL: shard $i produced no log file" >&2
    ANY_FAIL=1
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

# Output contract for the workflow steps' carve-outs: a `go test` invocation
# ends a failing package with `FAIL\t<pkg import path>\t<duration>`, and the
# steps' flake filters extract the package FROM that line
# (`grep -aE '^FAIL[[:space:]]' | awk '{print $2}'`). A test binary run
# prints only a bare `FAIL` — no package path — so without this line every
# sharded step's flake filter (and its isolated re-run) is dead code: a
# hang/contention failure would exit without ever being re-classified. Emit
# the same summary shape on failure so the existing filters keep working
# unchanged (run-test review round 2, finding b).
if [ "$ANY_FAIL" -ne 0 ]; then
  RUN_END=$(date +%s)
  printf 'FAIL\tgithub.com/elicify-ai/omnipus/pkg/gateway\t%ss\n' "$((RUN_END - RUN_START))"
fi

# HIGH 3: completeness — every runnable listed test must have a result.
# Runs AFTER the combined log reaches stdout/--out so a failing check still
# leaves the full output for the workflow's carve-outs to inspect.
if ! python3 scripts/gateway-test-shards.py verify --binary "$BINARY" --log "$COMBINED"; then
  echo "FATAL: completeness check failed — the shards did not produce a result for every runnable listed test" >&2
  exit 1
fi

# Propagate failure so the workflow step fails and the flake filter runs
if [ "$ANY_FAIL" -ne 0 ]; then
  exit 1
fi
exit 0
