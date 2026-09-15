#!/usr/bin/env bash
# check-single-verdict-projection.test.sh
#
# Proof-of-failure companion for check-single-verdict-projection.sh (ADR-086
# GOAL-FR-036 / ADR-084 JUDGE-FR-076 step 2, C-03). A guard that cannot go
# red manufactures the green it was meant to withhold
# (docs/internal/false-green-patterns.md), so this builds scratch fixture
# trees under a temp directory, points the guard at each via REPO_ROOT, and
# NEVER touches the real repo tree.
#
# Modelled on scripts/check-no-orphan-turn-watchdog.test.sh — the repository's
# one guard companion that actually plants offenders, named as the template by
# the delivery plan's R-19(a) — and satisfying R-19(b): every case exercises
# the guard against a planted offender or a clean tree and PRINTS the guard's
# observed exit code.
#
# R-19(c) requires this guard to be a CARDINALITY check, red on zero as well
# as on two. Both rows are here: tests 2 and 3 are the zero cases, test 1 is
# the two case.
#
# Covers:
#
#   CAUGHT (guard must exit 1)
#    1. TWO files assigning task.CritMet / task.CritUnmet — a second writer.
#    2. ZERO assignments with the canonical file still present but gutted.
#    3. ZERO assignments with pkg/agent/verdict_projection.go deleted.
#    4. Exactly one writer, but in the WRONG file.
#    5. A file named verdict_status_projection.go exists (even carrying no
#       assignment of its own).
#    6. A non-comment source reference to the retired verdict_status_projection
#       name.
#
#   NOT CAUGHT (guard must exit 0)
#    7. verdict_status_projection named only inside a comment — which is how
#       pkg/agent/verdict_projection.go's own header documents this rule.
#    8. _test.go files assigning the constants freely.
#    9. pkg/task/criterion.go's constant DECLARATIONS and the
#       `case CritPending, CritMet, CritUnmet:` validity switch.
#   10. Comparisons: `if c.Status == task.CritMet`.
#   11. A clean tree: one writer, in the canonical file.
#
#   CANNOT RUN (guard must exit 2, never a silent green)
#   12. A tree missing a required scan directory.
#   13. A forced grep failure (an invalid ERE injected through the guard's
#       test-only CHECK_SINGLE_VERDICT_PROJECTION_PATTERN_OVERRIDE).
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-single-verdict-projection.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/single-verdict-projection-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

CANONICAL_BODY='
package agent

func projectVerdictOntoCriteria(criteria []task.AcceptanceCriterion, verdict *task.JudgeVerdict) []task.AcceptanceCriterion {
	out := make([]task.AcceptanceCriterion, len(criteria))
	copy(out, criteria)
	for i := range out {
		if met(out[i].ID, verdict) {
			out[i].Status = task.CritMet
		} else {
			out[i].Status = task.CritUnmet
		}
	}
	return out
}
'

setup_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
}

# Every fixture tree needs the directories the guard requires to exist, or it
# would correctly refuse to run (exit 2) rather than scan nothing. By default
# the canonical projection file is planted too, so each test isolates the one
# thing it is about.
setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{pkg/agent,cmd,src}
  setup_fixture "pkg/agent/verdict_projection.go" "$CANONICAL_BODY"
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

echo "=== check-single-verdict-projection self-test (ADR-086 GOAL-FR-036, C-03) ==="
echo ""

# --- Test 1: a SECOND writer — CAUGHT --------------------------------------

echo "Test 1: a second file assigning the met/unmet constants is caught"
setup_skeleton
setup_fixture "pkg/agent/judge_writeback.go" '
package agent

func writeBack(c *task.AcceptanceCriterion, met bool) {
	if met {
		c.Status = task.CritMet
		return
	}
	c.Status = task.CritUnmet
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "two-writers-exit" 1 "$EXIT_CODE"
assert_output_contains "two-writers-count" "2 files assign" "$OUTPUT"
assert_output_contains "two-writers-name" "judge_writeback.go" "$OUTPUT"

# --- Test 2: ZERO assignments, canonical file gutted — CAUGHT --------------

echo ""
echo "Test 2: the canonical file gutted to a no-op (zero assignments) is caught"
setup_skeleton
setup_fixture "pkg/agent/verdict_projection.go" '
package agent

func projectVerdictOntoCriteria(criteria []task.AcceptanceCriterion, verdict *task.JudgeVerdict) []task.AcceptanceCriterion {
	return criteria
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "zero-gutted-exit" 1 "$EXIT_CODE"
assert_output_contains "zero-gutted-reason" "ZERO files assign" "$OUTPUT"
assert_output_contains "zero-gutted-detail" "no longer writes either constant" "$OUTPUT"

# --- Test 3: ZERO assignments, canonical file deleted — CAUGHT -------------

echo ""
echo "Test 3: the canonical file deleted outright is caught"
setup_skeleton
rm -f "$TMP_DIR/pkg/agent/verdict_projection.go"
setup_fixture "pkg/agent/unrelated.go" '
package agent

func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "zero-missing-exit" 1 "$EXIT_CODE"
assert_output_contains "zero-missing-detail" "does not exist at all" "$OUTPUT"

# --- Test 4: one writer, WRONG file — CAUGHT -------------------------------

echo ""
echo "Test 4: the single writer living in the wrong file is caught"
setup_skeleton
rm -f "$TMP_DIR/pkg/agent/verdict_projection.go"
setup_fixture "pkg/agent/task_executor.go" "$CANONICAL_BODY"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "wrong-file-exit" 1 "$EXIT_CODE"
assert_output_contains "wrong-file-reason" "wrong file" "$OUTPUT"
assert_output_contains "wrong-file-expected" "pkg/agent/verdict_projection.go" "$OUTPUT"

# --- Test 5: the retired FILENAME is back — CAUGHT -------------------------

echo ""
echo "Test 5: a file named verdict_status_projection.go is caught"
setup_skeleton
setup_fixture "pkg/agent/verdict_status_projection.go" '
package agent

func placeholder() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "retired-file-exit" 1 "$EXIT_CODE"
assert_output_contains "retired-file-finding" "verdict_status_projection.go" "$OUTPUT"
assert_output_contains "retired-file-reason" "must NEVER exist" "$OUTPUT"

# --- Test 6: the retired NAME referenced in code — CAUGHT ------------------

echo ""
echo "Test 6: a non-comment reference to verdict_status_projection is caught"
setup_skeleton
setup_fixture "pkg/agent/loader.go" '
package agent

var projectionSource = "verdict_status_projection"
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "retired-name-exit" 1 "$EXIT_CODE"
assert_output_contains "retired-name-finding" "loader.go" "$OUTPUT"

# --- Test 7: the retired name in a COMMENT — NOT caught --------------------

echo ""
echo "Test 7: the retired name inside a comment is NOT caught"
setup_skeleton
setup_fixture "pkg/agent/verdict_projection.go" "
// verdict_projection.go is the SINGLE verdict -> criterion-status writer.
// Per C-03 this is the ONE file: pkg/agent/verdict_status_projection.go
// MUST NEVER exist.
${CANONICAL_BODY}
"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "comment-name-exit" 0 "$EXIT_CODE"
assert_output_contains "comment-name-ok" "OK: exactly one" "$OUTPUT"

# --- Test 8: _test.go assignments — NOT caught -----------------------------

echo ""
echo "Test 8: assignments inside _test.go files are NOT counted"
setup_skeleton
setup_fixture "pkg/agent/verdict_projection_test.go" '
package agent

func TestProjection(t *testing.T) {
	want := []task.AcceptanceCriterion{{ID: "c1", Status: task.CritMet}}
	got := projectVerdictOntoCriteria(in, v)
	got[0].Status = task.CritUnmet
	_ = want
}
'
setup_fixture "pkg/task/criterion_test.go" '
package task

func TestStatuses(t *testing.T) {
	s := CritMet
	s = CritUnmet
	_ = s
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "test-files-exit" 0 "$EXIT_CODE"

# --- Test 9: the constant declarations and the validity switch — NOT caught -

echo ""
echo "Test 9: pkg/task/criterion.go's const block and validity switch are NOT counted"
setup_skeleton
setup_fixture "pkg/task/criterion.go" '
package task

type CriterionStatus string

const (
	CritPending CriterionStatus = "pending"
	CritMet     CriterionStatus = "met"
	CritUnmet   CriterionStatus = "unmet"
)

func IsValidCriterionStatus(s CriterionStatus) bool {
	switch s {
	case CritPending, CritMet, CritUnmet:
		return true
	}
	return false
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "const-decl-exit" 0 "$EXIT_CODE"
assert_output_contains "const-decl-ok" "OK: exactly one" "$OUTPUT"

# --- Test 10: comparisons — NOT caught -------------------------------------

echo ""
echo "Test 10: comparisons against the constants are NOT counted as writers"
setup_skeleton
setup_fixture "pkg/gateway/rest_tasks.go" '
package gateway

func countMet(cs []task.AcceptanceCriterion) int {
	n := 0
	for _, c := range cs {
		if c.Status == task.CritMet {
			n++
		}
		if c.Status != task.CritUnmet {
			continue
		}
	}
	return n
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "comparison-exit" 0 "$EXIT_CODE"

# --- Test 11: a clean tree — exits 0 ---------------------------------------

echo ""
echo "Test 11: a clean tree (one writer, canonical file) exits 0"
setup_skeleton
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clean-tree-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-tree-ok" "OK: exactly one verdict" "$OUTPUT"

# --- Test 12: a tree missing a required directory — exits 2 ----------------

echo ""
echo "Test 12: a tree missing a required directory exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/pkg"
# Deliberately omit pkg/agent, cmd and src.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-dir-exit" 2 "$EXIT_CODE"
assert_output_not_contains "missing-dir-not-false-ok" "OK: exactly one" "$OUTPUT"

# --- Test 13: a forced grep failure — exits 2, not a false "OK" ------------

echo ""
echo "Test 13: a grep failure (invalid ERE) is reported as exit 2, not swallowed"
setup_skeleton
OUTPUT=$(REPO_ROOT="$TMP_DIR" CHECK_SINGLE_VERDICT_PROJECTION_PATTERN_OVERRIDE='(unbalanced' bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "grep-failure-exit" 2 "$EXIT_CODE"
assert_output_not_contains "grep-failure-not-false-ok" "OK: exactly one" "$OUTPUT"

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
