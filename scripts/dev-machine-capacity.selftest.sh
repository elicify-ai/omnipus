#!/usr/bin/env bash
# scripts/dev-machine-capacity.selftest.sh
#
# Self-check for scripts/dev-machine-capacity.sh (L10 task: "a self-check
# that proves the monitor returns HOLD under strict thresholds and OK under
# loose ones"). Runs against a scratch ledger directory under $(mktemp -d)
# -- never the real coordination ledger -- and against the real machine's
# memory/disk/CPU (there is no portable way to fake those without root, so
# the monitor's own env-var thresholds are pushed to the extremes instead:
# an impossibly high floor forces HOLD, a zero floor forces OK for that
# signal). Prints PASS/FAIL per case; exits 1 if any case did not behave as
# specified.
#
# USAGE: scripts/dev-machine-capacity.selftest.sh

set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/dev-machine-capacity.sh"

TMP="$(mktemp -d /tmp/omnipus-capacity-selftest.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

pass_count=0
fail_count=0
fail=0

record() {
  # record <case> <expected: OK|HOLD> <output>
  case_name="$1"; expect="$2"; out="$3"
  verdict_line="$(printf '%s\n' "$out" | grep '^CAPACITY:' | head -1)"
  if printf '%s' "$verdict_line" | grep -q "^CAPACITY: $expect"; then
    echo "PASS: $case_name ($verdict_line)"; pass_count=$((pass_count+1))
  else
    echo "FAIL: $case_name (expected CAPACITY: $expect, got: ${verdict_line:-<no verdict line printed>})"
    fail_count=$((fail_count+1)); fail=1
  fi
}

LEDGER="$TMP/coordination"
mkdir -p "$LEDGER/squads"

COMMON_ENV="DEV_CAPACITY_CPU_SAMPLES=1 DEV_CAPACITY_LEDGER_DIR=$LEDGER"

# ---- case: loose thresholds -> OK ------------------------------------------
out="$(env $COMMON_ENV \
  DEV_CAPACITY_MIN_FREE_MEM_GB=0 \
  DEV_CAPACITY_MIN_FREE_DISK_GB=0 \
  DEV_CAPACITY_MAX_DISPATCHES=999999 \
  "$SCRIPT" 2>&1)"
record "loose thresholds" "OK" "$out"

# ---- case: strict memory+disk thresholds -> HOLD ---------------------------
out="$(env $COMMON_ENV \
  DEV_CAPACITY_MIN_FREE_MEM_GB=999999 \
  DEV_CAPACITY_MIN_FREE_DISK_GB=999999 \
  "$SCRIPT" 2>&1)"
record "strict memory+disk thresholds" "HOLD" "$out"

# ---- case: active-dispatch ceiling alone -> OK (Round 18: advisory only, never holds alone) ----
for n in 1 2 3; do printf 'squad=s%s | status=in-flight | last-updated=2026-09-25T00:00:00Z by selftest\n' "$n" > "$LEDGER/squads/s$n.md"; done
out="$(env $COMMON_ENV \
  DEV_CAPACITY_MIN_FREE_MEM_GB=0 \
  DEV_CAPACITY_MIN_FREE_DISK_GB=0 \
  DEV_CAPACITY_MAX_DISPATCHES=2 \
  "$SCRIPT" 2>&1)"
record "active-dispatch ceiling exceeded alone (3 in-flight > 2) never holds by itself" "OK" "$out"

# ---- case: active-dispatch ceiling exceeded + a hard hold -> HOLD, dispatch count folded into the reason ----
out="$(env $COMMON_ENV \
  DEV_CAPACITY_MIN_FREE_MEM_GB=999999 \
  DEV_CAPACITY_MIN_FREE_DISK_GB=0 \
  DEV_CAPACITY_MAX_DISPATCHES=2 \
  "$SCRIPT" 2>&1)"
record "active-dispatch ceiling exceeded alongside a memory HOLD" "HOLD" "$out"
printf '%s\n' "$out" | grep -q "active dispatches: 3 in-flight > 2 ceiling" \
  && echo "PASS: dispatch count folded into the reason text" || { echo "FAIL: dispatch count missing from HOLD reason"; fail_count=$((fail_count+1)); fail=1; }

# ---- case: active-dispatch count under ceiling -> OK -----------------------
out="$(env $COMMON_ENV \
  DEV_CAPACITY_MIN_FREE_MEM_GB=0 \
  DEV_CAPACITY_MIN_FREE_DISK_GB=0 \
  DEV_CAPACITY_MAX_DISPATCHES=10 \
  "$SCRIPT" 2>&1)"
record "active-dispatch count under ceiling (3 <= 10)" "OK" "$out"

# ---- case: a commented-out template line must never count ------------------
printf '# squad=template-example | status=in-flight | last-updated=2026-09-25T00:00:00Z by nobody\n' > "$LEDGER/squads/commented-only.md"
out="$(env $COMMON_ENV \
  DEV_CAPACITY_MIN_FREE_MEM_GB=0 \
  DEV_CAPACITY_MIN_FREE_DISK_GB=0 \
  DEV_CAPACITY_MAX_DISPATCHES=3 \
  "$SCRIPT" 2>&1)"
record "commented-out row does not raise the in-flight count above the real 3" "OK" "$out"
rm -f "$LEDGER/squads/commented-only.md"

# ---- case: an unmeasurable hard signal exits 2, not a false OK -------------
out="$(env $COMMON_ENV DEV_CAPACITY_OS_OVERRIDE=UnknownTestOS "$SCRIPT" 2>&1)"
code=$?
if [ "$code" -eq 2 ]; then
  echo "PASS: unmeasurable platform exits 2, not a silent OK (exit=$code)"; pass_count=$((pass_count+1))
else
  echo "FAIL: unmeasurable platform (expected exit=2, got exit=$code): $out"; fail_count=$((fail_count+1)); fail=1
fi

echo
echo "[selftest] $pass_count passed, $fail_count failed"
exit "$fail"
