#!/usr/bin/env bash
# check-no-unable-to-verify-outcome.test.sh
#
# Self-test for check-no-unable-to-verify-outcome.sh (ADR-084 revision-9 §10
# withdrawal guard, wave G2). A guard that cannot fail is no guard
# (docs/internal/false-green-patterns.md) — this builds scratch fixture
# trees under a temp directory, points the guard at them via REPO_ROOT (the
# same pattern as check-no-orphan-turn-watchdog.test.sh and
# check-no-goal-confirm-gate.sh's sibling guards), and NEVER touches the
# real scripts/, pkg/ or src/ trees.
#
# Every fixture invokes the guard through an actual `bash` subprocess
# (never a direct `grep` call in this test's own shell) — this repo's dev
# environment can carry an interactive-shell `grep` wrapper (ugrep, aliased
# for tool ergonomics) that behaves differently from the plain grep any
# subprocess or CI actually gets, and a fixture asserted only against the
# wrapper would prove nothing about the guard CI will run. This distinction
# is exactly what caught a real bug while writing this file (Test 5's
# original pattern silently matched nothing under real BSD/GNU grep's
# bracket-expression handling of escaped `[`/`]` inside a character class —
# fixed to a plain `\S+` match).
#
# Covers:
#
#   1. A literal `unable_to_verify` in a contract YAML — CAUGHT.
#   2. A literal `unable_to_verify` in pkg/api/generated/ — CAUGHT (the
#      guard does not special-case a "generated is exempt" rule for this
#      specific value; ADR-084 §10 forbids it everywhere it could surface).
#   3. A literal `unable_to_verify` in src/, outside src/components/ (the
#      widened-scope case) — CAUGHT.
#   4. A fourth CriterionStatus-typed constant in pkg/task/ — CAUGHT,
#      regardless of what the fourth value is spelled.
#   5. An Outcome field added to a pkg/task/ struct, in two type shapes
#      (bare and slice) — CAUGHT (slice is the regression case for the
#      bracket-expression bug this file's header describes).
#   6. The three named, permanent negative-oracle regression tests
#      (pkg/task/criterion_test.go, pkg/task/verdict_adr084_test.go,
#      src/lib/api/criterionStatusEnum.test.ts) carrying the literal string
#      as their own test fixture — NOT caught (they exist to prove the
#      value is rejected; flagging them would make the lock's own test trip
#      the guard that shares its requirement).
#   7. A DIFFERENT, non-excluded test file carrying the same literal string
#      — CAUGHT (the exclusion is by exact path, not by `_test.go` suffix).
#   8. The same symbols, but only inside `//` and `#` comments — NOT caught.
#   9. A clean tree with none of the above — exits 0.
#  10. A tree missing a required scan directory — exits 2, never a silent
#      green.
#  11. A forced grep failure (an invalid ERE injected via the script's
#      test-only CHECK_NO_UNABLE_TO_VERIFY_OUTCOME_PATTERN_OVERRIDE env var)
#      is reported as exit 2 with the grep error on stderr — NOT swallowed
#      into a false "OK".
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD_SCRIPT="${SCRIPT_DIR}/check-no-unable-to-verify-outcome.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/unable-to-verify-outcome-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

# Every fixture tree needs the five directories the guard requires to exist,
# or it would correctly refuse to run (exit 2) rather than silently scanning
# nothing.
setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{contracts,pkg/api/generated,src/lib/api/generated,src,pkg/task}
  cat > "$TMP_DIR/pkg/task/criterion.go" <<'GOEOF'
package task

type CriterionStatus string

const (
	CritPending CriterionStatus = "pending"
	CritMet     CriterionStatus = "met"
	CritUnmet   CriterionStatus = "unmet"
)
GOEOF
  cat > "$TMP_DIR/pkg/task/verdict.go" <<'GOEOF'
package task

type CriterionVerdict struct {
	Met bool `json:"met"`
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

echo "=== check-no-unable-to-verify-outcome self-test (ADR-084 revision-9 §10) ==="
echo ""

# --- Test 1: literal in a contract YAML — CAUGHT ---

echo "Test 1: a literal unable_to_verify in a contract YAML is caught"
setup_skeleton
setup_fixture "contracts/components/schemas/AcceptanceCriterion.yaml" '
status:
  enum: [pending, met, unmet, unable_to_verify]
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "contracts-hit-exit" 1 "$EXIT_CODE"
assert_output_contains "contracts-hit-finding" "AcceptanceCriterion.yaml" "$OUTPUT"

# --- Test 2: literal in pkg/api/generated/ — CAUGHT ---

echo ""
echo "Test 2: a literal unable_to_verify in pkg/api/generated/ is caught"
setup_skeleton
setup_fixture "pkg/api/generated/types.go" '
package generated

const CriterionStatusUnableToVerify = "unable_to_verify"
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "generated-hit-exit" 1 "$EXIT_CODE"
assert_output_contains "generated-hit-finding" "types.go" "$OUTPUT"

# --- Test 3: literal in src/, outside src/components/ — CAUGHT ---

echo ""
echo "Test 3: a literal unable_to_verify in src/store/ (outside src/components) is caught"
setup_skeleton
setup_fixture "src/store/chat.ts" "
const CRITERION_UNABLE = 'unable_to_verify'
"
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "src-widened-hit-exit" 1 "$EXIT_CODE"
assert_output_contains "src-widened-hit-finding" "chat.ts" "$OUTPUT"

# --- Test 4: a fourth CriterionStatus constant — CAUGHT ---

echo ""
echo "Test 4: a fourth CriterionStatus constant is caught, whatever it is spelled"
setup_skeleton
setup_fixture "pkg/task/criterion.go" '
package task

type CriterionStatus string

const (
	CritPending CriterionStatus = "pending"
	CritMet     CriterionStatus = "met"
	CritUnmet   CriterionStatus = "unmet"
	CritReview  CriterionStatus = "review"
)
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "fourth-const-exit" 1 "$EXIT_CODE"
assert_output_contains "fourth-const-finding" "CriterionStatus no longer carries exactly the three locked values" "$OUTPUT"
assert_output_contains "fourth-const-shows-review" 'CritReview' "$OUTPUT"

# --- Test 5: an Outcome field, two type shapes — CAUGHT ---

echo ""
echo "Test 5a: a bare-string Outcome field on a pkg/task/ struct is caught"
setup_skeleton
setup_fixture "pkg/task/verdict.go" '
package task

type CriterionVerdict struct {
	Met     bool   `json:"met"`
	Outcome string `json:"outcome"`
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "outcome-bare-exit" 1 "$EXIT_CODE"
assert_output_contains "outcome-bare-finding" "an Outcome field has been added" "$OUTPUT"

echo ""
echo "Test 5b: a slice-typed Outcome field is ALSO caught (the bracket-expression regression case)"
setup_skeleton
setup_fixture "pkg/task/verdict.go" '
package task

type CriterionVerdict struct {
	Met     bool     `json:"met"`
	Outcome []string `json:"outcome"`
}
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "outcome-slice-exit" 1 "$EXIT_CODE"
assert_output_contains "outcome-slice-finding" "an Outcome field has been added" "$OUTPUT"

# --- Test 6: the three named negative-oracle test files — NOT caught ---

echo ""
echo "Test 6: the three permanent negative-oracle regression tests are NOT flagged"
setup_skeleton
setup_fixture "pkg/task/criterion_test.go" '
package task

import "testing"

func TestNoFourthCriterionStatusValue(t *testing.T) {
	if IsValidCriterionStatus(CriterionStatus("unable_to_verify")) {
		t.Fatal("unable_to_verify must not be a valid CriterionStatus")
	}
}
'
setup_fixture "pkg/task/verdict_adr084_test.go" '
package task

import "testing"

func TestEvidenceSourceRejectsUnableToVerify(t *testing.T) {
	if IsValidVerdictEvidenceSource("unable_to_verify") {
		t.Fatal("unable_to_verify must not be a valid evidence source")
	}
}
'
setup_fixture "src/lib/api/criterionStatusEnum.test.ts" "
import { describe, it, expect } from 'vitest'
describe('lock', () => {
  it('rejects unable_to_verify', () => {
    expect(parseStatus('unable_to_verify').success).toBe(false)
  })
})
"
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "named-exclusions-exit" 0 "$EXIT_CODE"
assert_output_contains "named-exclusions-ok" "OK:" "$OUTPUT"

# --- Test 7: a DIFFERENT, non-excluded test file with the same string — CAUGHT ---

echo ""
echo "Test 7: a different, non-excluded test file carrying the literal string is caught"
setup_skeleton
setup_fixture "pkg/task/store_test.go" '
package task

var legacyValue = "unable_to_verify"
'
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "non-excluded-test-exit" 1 "$EXIT_CODE"
assert_output_contains "non-excluded-test-finding" "store_test.go" "$OUTPUT"

# --- Test 8: comment-only mentions — NOT caught ---

echo ""
echo "Test 8: comment-only mentions of the withdrawn value are NOT caught"
setup_skeleton
setup_fixture "pkg/task/note.go" '
package task

// unable_to_verify was withdrawn in full by ADR-084 revision 9 (D2a).
// The three-value lock stands: pending, met, unmet.
func noop() {}
'
setup_fixture "src/lib/note.ts" "
// unable_to_verify no longer exists anywhere in the SPA.
export const noop = () => {}
"
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "comment-only-exit" 0 "$EXIT_CODE"
assert_output_not_contains "comment-only-not-flagged-go" "note.go" "$OUTPUT"
assert_output_not_contains "comment-only-not-flagged-ts" "note.ts" "$OUTPUT"

# --- Test 9: a clean tree — exits 0 ---

echo ""
echo "Test 9: a clean tree with none of the guarded shapes anywhere exits 0"
setup_skeleton
OUTPUT=$(run_guard); EXIT_CODE=$?
assert_exit_code "clean-tree-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-tree-ok-message" "OK:" "$OUTPUT"

# --- Test 10: a tree missing a required scan directory refuses to report green ---

echo ""
echo "Test 10: a tree missing a required directory exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/pkg/task"
# Deliberately omit contracts/, pkg/api/generated/, src/lib/api/generated/, src/.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1); EXIT_CODE=$?
assert_exit_code "missing-dir-exit" 2 "$EXIT_CODE"

# --- Test 11 (F3): a forced grep failure is a hard failure, not a false "OK" ---

echo ""
echo "Test 11 (F3): a grep failure (invalid ERE) is reported as exit 2, not swallowed as 'no matches'"
setup_skeleton
OUTPUT=$(REPO_ROOT="$TMP_DIR" CHECK_NO_UNABLE_TO_VERIFY_OUTCOME_PATTERN_OVERRIDE='(unbalanced' bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "grep-failure-exit" 2 "$EXIT_CODE"
assert_output_not_contains "grep-failure-not-false-ok" "OK: no withdrawn" "$OUTPUT"

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
