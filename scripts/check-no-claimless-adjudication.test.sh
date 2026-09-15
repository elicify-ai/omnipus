#!/usr/bin/env bash
# check-no-claimless-adjudication.test.sh
#
# Self-test for check-no-claimless-adjudication.sh (ADR-084 revision-9 D13 /
# JUDGE-FR-095, FR-097 mechanical guard, wave G6). A guard that cannot fail
# is no guard (docs/internal/false-green-patterns.md) — this builds scratch
# fixture trees under a temp directory, points the guard at them via
# REPO_ROOT (the same pattern as check-no-orphan-turn-watchdog.test.sh and
# check-no-unable-to-verify-outcome.test.sh), and NEVER touches the real
# scripts/ or pkg/agent/ trees.
#
# Every fixture invokes the guard through an actual `bash` subprocess (never
# a direct `grep` call in this test's own shell) — this repo's dev
# environment can carry an interactive-shell `grep` wrapper that behaves
# differently from the plain grep any subprocess or CI actually gets, and a
# fixture asserted only against the wrapper would prove nothing about the
# guard CI will run.
#
# Covers:
#
#   1. Each of the four retired FR-014b symbols
#      (goalZeroOutputTripleHolds, sessionHasTranscriptOutputSince,
#      goalZeroEvidenceRecords, goalHasTranscriptOutputSince) reappearing as
#      a bare definition in pkg/agent/ — CAUGHT, individually.
#   2. The retired claimless-adjudication log line ("firing claimless
#      adjudication after quiet window") reappearing in pkg/agent/ —
#      CAUGHT.
#   3. Comment-only mentions of every guarded name/phrase (Go `//` and
#      block-comment `*`/`/*` continuation lines) — NOT caught, mirroring
#      how goal_triggers.go/goal_triggers_test.go/
#      goal_keeper_repairs_test.go/goal_flow_integration_test.go/
#      verifier_adjudication.go reference these names today.
#   4. The KEPT, differently-named test helper
#      `primeGoalZeroOutputTripleFalse` (E13 deliberately retained it so
#      wave E12's goal_flow_integration_test.go keeps compiling) —
#      NOT caught, proving no accidental substring collision with the
#      guarded `goalZeroOutputTripleHolds`.
#   5. A hit outside pkg/agent/ (e.g. pkg/task/) — NOT caught (guard is
#      scoped to pkg/agent/ only, per its own Scope note).
#   6. A clean tree with none of the above — exits 0.
#   7. A tree missing pkg/agent/ — exits 2, never a silent green.
#   8. A forced grep failure (an invalid ERE injected via the script's
#      test-only CHECK_NO_CLAIMLESS_ADJUDICATION_PATTERN_OVERRIDE env var)
#      is reported as exit 2 with the grep error on stderr — NOT swallowed
#      into a false "OK".
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD_SCRIPT="${SCRIPT_DIR}/check-no-claimless-adjudication.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/claimless-adjudication-guard-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

# The guard requires pkg/agent/ to exist, or it correctly refuses to run
# (exit 2) rather than silently scanning nothing.
setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR/pkg/agent"
  cat > "$TMP_DIR/pkg/agent/goal_triggers.go" <<'GOEOF'
package agent

// settleGoalNormally is JUDGE-FR-095/FR-097's unified push ladder (D13):
// dispatch a bounded continue-push. Past the budget this function is a
// no-op — the goal is left quiet.
func (al *AgentLoop) settleGoalNormally() {
}
GOEOF
}

setup_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
}

run_guard() {
  REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1
}

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$actual" -eq "$expected" ]]; then
    echo "  PASS [$label]: exit $actual (expected $expected)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: exit $actual (expected $expected)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected exit $expected, got $actual")
  fi
}

assert_output_contains() {
  local label="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_output_not_contains() {
  local label="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

echo "=== check-no-claimless-adjudication self-test (ADR-084 revision-9 D13) ==="
echo ""

# --- Test 1: each of the four retired symbols, individually caught ---

echo "Test 1a: goalZeroOutputTripleHolds reappearing is caught"
setup_skeleton
setup_fixture "pkg/agent/goal_triggers.go" '
package agent

func goalZeroOutputTripleHolds() bool {
	return false
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "symbol-1-exit" 1 "$EXIT_CODE"
assert_output_contains "symbol-1-finding" "goalZeroOutputTripleHolds" "$OUTPUT"

echo ""
echo "Test 1b: sessionHasTranscriptOutputSince reappearing is caught"
setup_skeleton
setup_fixture "pkg/agent/goal_triggers.go" '
package agent

func sessionHasTranscriptOutputSince() bool {
	return false
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "symbol-2-exit" 1 "$EXIT_CODE"
assert_output_contains "symbol-2-finding" "sessionHasTranscriptOutputSince" "$OUTPUT"

echo ""
echo "Test 1c: goalZeroEvidenceRecords reappearing is caught"
setup_skeleton
setup_fixture "pkg/agent/goal_triggers.go" '
package agent

func goalZeroEvidenceRecords() int {
	return 0
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "symbol-3-exit" 1 "$EXIT_CODE"
assert_output_contains "symbol-3-finding" "goalZeroEvidenceRecords" "$OUTPUT"

echo ""
echo "Test 1d: goalHasTranscriptOutputSince reappearing is caught"
setup_skeleton
setup_fixture "pkg/agent/goal_triggers.go" '
package agent

func goalHasTranscriptOutputSince() bool {
	return false
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "symbol-4-exit" 1 "$EXIT_CODE"
assert_output_contains "symbol-4-finding" "goalHasTranscriptOutputSince" "$OUTPUT"

# --- Test 2: the retired claimless-adjudication log line — CAUGHT ---

echo ""
echo "Test 2: the retired claimless-adjudication log line reappearing is caught"
setup_skeleton
setup_fixture "pkg/agent/goal_triggers.go" '
package agent

func (al *AgentLoop) settleGoalNormally() {
	logger.InfoCF("agent", "goal idle settle: firing claimless adjudication after quiet window", nil)
	al.runGoalAdjudication(nil, nil, "", "", nil, nil, "", nil)
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "log-line-exit" 1 "$EXIT_CODE"
assert_output_contains "log-line-finding" "firing claimless adjudication after quiet window" "$OUTPUT"

# --- Test 3: comment-only mentions — NOT caught ---

echo ""
echo "Test 3: comment-only mentions of every guarded name/phrase are NOT caught"
setup_skeleton
setup_fixture "pkg/agent/goal_triggers.go" '
package agent

// The triple itself (goalZeroOutputTripleHolds), its evidence/diff/output
// terms (goalZeroEvidenceRecords, goalHasTranscriptOutputSince,
// sessionHasTranscriptOutputSince) and the watermark-priming helper this
// suite used to need are deleted outright, not merely unreferenced.
//
// The old log line read "goal idle settle: firing claimless adjudication
// after quiet window" and is gone along with the call it described.
/*
 * Block comment continuation also mentioning goalZeroOutputTripleHolds and
 * firing claimless adjudication after quiet window — still just prose.
 */
func noop() {}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "comment-only-exit" 0 "$EXIT_CODE"
assert_output_contains "comment-only-ok" "OK:" "$OUTPUT"

# --- Test 4: the KEPT helper primeGoalZeroOutputTripleFalse — NOT caught ---

echo ""
echo "Test 4: the kept, differently-named helper primeGoalZeroOutputTripleFalse is NOT caught"
setup_skeleton
setup_fixture "pkg/agent/goal_triggers_test.go" '
package agent

import "testing"

// primeGoalZeroOutputTripleFalse seeds a prior ADJUDICABLE transcript entry.
// KEPT — wave E12'"'"'s goal_flow_integration_test.go still calls it.
func primeGoalZeroOutputTripleFalse(t *testing.T) {
	t.Helper()
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "kept-helper-exit" 0 "$EXIT_CODE"
assert_output_contains "kept-helper-ok" "OK:" "$OUTPUT"

# --- Test 5: a hit outside pkg/agent/ — NOT caught (out of scope) ---

echo ""
echo "Test 5: a hit outside pkg/agent/ (e.g. pkg/task/) is out of the guard's scope and NOT caught"
setup_skeleton
setup_fixture "pkg/task/stray.go" '
package task

func goalZeroOutputTripleHolds() bool {
	return false
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "out-of-scope-exit" 0 "$EXIT_CODE"
assert_output_not_contains "out-of-scope-not-flagged" "stray.go" "$OUTPUT"

# --- Test 6: a clean tree — exits 0 ---

echo ""
echo "Test 6: a clean tree with none of the guarded shapes anywhere exits 0"
setup_skeleton
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "clean-tree-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-tree-ok-message" "OK:" "$OUTPUT"

# --- Test 7: a tree missing pkg/agent/ refuses to report green ---

echo ""
echo "Test 7: a tree missing pkg/agent/ exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1); EXIT_CODE=$?
assert_exit_code "missing-dir-exit" 2 "$EXIT_CODE"

# --- Test 8: a forced grep failure is a hard failure, not a false "OK" ---

echo ""
echo "Test 8: a grep failure (invalid ERE) is reported as exit 2, not swallowed as 'no matches'"
setup_skeleton
OUTPUT=$(REPO_ROOT="$TMP_DIR" CHECK_NO_CLAIMLESS_ADJUDICATION_PATTERN_OVERRIDE='(unbalanced' bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "grep-failure-exit" 2 "$EXIT_CODE"
assert_output_not_contains "grep-failure-not-false-ok" "OK: no retired" "$OUTPUT"

# --- Summary ----------------------------------------------------------------

echo ""
echo "─────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [[ "$FAIL" -gt 0 ]]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]}"; do
    echo "  - $e"
  done
  exit 1
fi

echo "All assertions passed."
exit 0
