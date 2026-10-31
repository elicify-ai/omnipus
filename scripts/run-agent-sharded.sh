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
# Hardened per the run-test review of 2026-10-30 (review-6b-tests.md):
#   - HIGH 1: every shard runs with the PACKAGE DIRECTORY as its working
#     directory, mirroring `go test` semantics. `go test` runs a package's
#     tests from the package dir; a `go test -c` binary started from the
#     repo root does not, and pkg/agent has tests reading relative paths
#     (e.g. context_todos_q2_test.go's testdata/todo_rule_q2.txt). The
#     binary path and every -test.* flag we pass are absolute/flag-only.
#   - HIGH 2: the partition step can no longer fail silently. The old
#     `mapfile -t REGEXES < <(python3 ...)` ignored the partitioner's exit
#     status (a process substitution's status is never the mapped
#     command's, even under set -euo pipefail): a failed partition meant
#     zero shards, zero tests, and a green exit. The partitioner output is
#     now captured, its exit checked, and the shard count asserted.
#   - HIGH 3: after the run, the partitioner's `verify` subcommand asserts
#     every runnable listed test has a top-level --- PASS/FAIL/SKIP result
#     line in the combined log; a missing result fails the run. The
#     partitioner partitions and verify expects the SAME runnable set
#     (Test*/Fuzz*/Example*; Benchmark* is listed by -test.list but never
#     runs without -test.bench — see the partitioner's docstring for the
#     fixture evidence).
#   - MEDIUM 5: all temp paths live under a unique `mktemp -d` (the fixed
#     /tmp/agent.test, /tmp/agent-shard-N.log and /tmp/agent-combined.log
#     collided across two invocations on one runner) and are cleaned up on
#     exit; the combined log is what reaches stdout / --out.
#   - MEDIUM 4 (CGO parity): the compile keeps CGO_ENABLED=0 for non-race
#     builds. The review suspected a drift from "runner default"; in fact
#     the pre-sharding step ran under the test-shards JOB env, which pins
#     CGO_ENABLED=0 for every unit shard (pr.yml, test-shards job env), so
#     the effective value never changed — and pkg/agent has no cgo-gated
#     files at all (-test.list is identical under CGO_ENABLED=0 and =1,
#     /workspace/logs/t6/ci4-agent-cgo-parity.log). Keeping the explicit
#     0 makes the script independent of the ambient environment.
#
# --race mode (for the "Go Race Tests (agent)" job): CGO_ENABLED=1 and
# -race on the compile, mirroring run-gateway-sharded.sh --race; -race
# requires cgo. All other behavior (partition, per-shard cwd, completeness
# check) is identical to the unit mode.
#
# Exit code and output are suitable for the workflow step's existing
# carve-outs (DATA RACE, --- FAIL) and flake filter. The agent steps carry
# no guard-failure carve-out: pkg/agent's TestMain emits no GUARD FAILURE
# token (that is pkg/gateway's hygiene guard), so none exists to preserve.
#
# Usage:
#   scripts/run-agent-sharded.sh --shards 4 --timeout 10m --tags goolm,stdjson
#   scripts/run-agent-sharded.sh --race --shards 2 --timeout 2700s --tags goolm,stdjson
#
# Outputs the combined test log to stdout. Exits non-zero on real failure.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SHARDS=4
TIMEOUT="10m"
TAGS="goolm,stdjson"
OUT_LOG=""
BINARY=""
RACE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --shards) SHARDS="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    --tags) TAGS="$2"; shift 2 ;;
    --out) OUT_LOG="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --race) RACE=1; shift ;;
    *) echo "unknown arg: $1" >&2; exit 64 ;;
  esac
done

cd "$ROOT"

# Unique per-invocation workspace (MEDIUM 5); everything we create lives and
# dies here. A caller-supplied --binary path is outside it and survives.
WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/agent-sharded.XXXXXX")"
trap 'rm -rf "$WORKDIR"' EXIT

if [ -z "$BINARY" ]; then
  BINARY="$WORKDIR/agent.test"
fi

echo "=== agent sharded: compile test binary ===" >&2
COMPILE_START=$(date +%s)
if [ "$RACE" -eq 1 ]; then
  # -race requires cgo; force CGO_ENABLED=1 exactly like the workflow's race
  # steps and run-gateway-sharded.sh --race do.
  CGO_ENABLED=1 go test -c -race -tags "$TAGS" -o "$BINARY" ./pkg/agent/ 2>&1
else
  CGO_ENABLED=0 go test -c -tags "$TAGS" -o "$BINARY" ./pkg/agent/ 2>&1
fi
COMPILE_END=$(date +%s)
echo "compile: $((COMPILE_END-COMPILE_START))s" >&2

echo "=== agent sharded: partition ${SHARDS} ways ===" >&2
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

PIDS=()
LOGS=()
RUN_START=$(date +%s)
for i in "${!REGEXES[@]}"; do
  LOG="$WORKDIR/shard-${i}.log"
  LOGS+=("$LOG")
  echo "shard $i: starting (regex len ${#REGEXES[$i]})" >&2
  (
    set +e
    # HIGH 1: run from the package directory — `go test` runs a package's
    # tests with the package dir as cwd; the compiled binary must too.
    cd "$ROOT/pkg/agent" || exit 1
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
COMBINED="$WORKDIR/combined.log"
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
  else
    # A shard that produced no log at all is a hard failure, not silence.
    echo "FATAL: shard $i produced no log file" >&2
    ANY_FAIL=1
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
  printf 'FAIL\tgithub.com/elicify-ai/omnipus/pkg/agent\t%ss\n' "$((RUN_END - RUN_START))"
fi

# HIGH 3: completeness — every runnable listed test must have a result.
# Runs AFTER the combined log reaches stdout/--out so a failing check still
# leaves the full output for the workflow's carve-outs to inspect.
if ! python3 scripts/gateway-test-shards.py verify --binary "$BINARY" --log "$COMBINED"; then
  echo "FATAL: completeness check failed — the shards did not produce a result for every runnable listed test" >&2
  exit 1
fi

if [ "$ANY_FAIL" -ne 0 ]; then
  exit 1
fi
exit 0
