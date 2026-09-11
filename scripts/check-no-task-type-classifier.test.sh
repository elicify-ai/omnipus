#!/usr/bin/env bash
# check-no-task-type-classifier.test.sh
#
# Self-test for check-no-task-type-classifier.sh (ADR-084 revision 9 §Q /
# FR-110 regression guard). A guard that cannot fail is no guard
# (docs/internal/false-green-patterns.md) — this builds a scratch fixture
# tree under a temp directory, points the lint script at it via REPO_ROOT
# (same pattern as check-no-orphan-turn-watchdog.test.sh and
# check-no-inferred-check.test.sh), and NEVER touches the real repo tree.
#
# Covers:
#
#   1. A planted function NAME that claims to classify ("func
#      IsCodingTask(...)") — CAUGHT.
#   2. A planted function whose NAME is unrelated but whose DOC COMMENT
#      makes the classification claim ("// decides whether the goal is a
#      coding task") — CAUGHT (the oracle's "or doc comment" half).
#   3. The reverse word order ("// returns true if this criterion is task
#      coding") — CAUGHT.
#   4. A "coding_goal" spelling using an underscore join — CAUGHT.
#   5. The real, legitimate, SANCTIONED shape this design actually ships —
#      a test function named with "NonCodingGoal" (mirroring the real
#      pkg/agent/judge_noncoding_goal_adr084_test.go) proving a non-coding
#      goal is decided WITHOUT a classifier — NOT caught. This is the
#      guard's own most important negative case: the literal substring
#      "codinggoal" appears inside "noncodinggoal", and a naive substring
#      match would wrongly flag the very test that proves FR-110 is met.
#   6. A doc comment mentioning "coding" and "task" only because they
#      legitimately co-occur in unrelated prose ("encoding" / "decoding"),
#      attached to a func — NOT caught.
#   7. A doc comment that discusses "coding tasks" but is attached to a
#      `type`/struct field, never to a `func` line (mirrors the real
#      pkg/routing/classifier.go and pkg/routing/features.go, an unrelated
#      LLM-routing "classifier" that predates and has nothing to do with
#      ADR-084) — NOT caught, because this guard is function-scoped only.
#   8. A comment block separated from the func by a blank line (breaking
#      Go's doc-comment adjacency convention) — NOT caught, because it is
#      no longer that function's doc comment.
#   9. A clean tree with nothing of the sort anywhere — exits 0.
#  10. A tree missing a required scan directory — exits 2, never a silent
#      green (the check itself could not run).
#  11. Zero discovered .go files (an empty/broken scan) — exits 2, never a
#      silent green over nothing scanned.
#  12. A forced awk failure (a syntactically invalid awk program injected
#      via the script's test-only
#      CHECK_NO_TASK_TYPE_CLASSIFIER_AWK_OVERRIDE env var) is reported as
#      exit 2 with the awk error on stderr — NOT swallowed into a false
#      "OK: no task-type classifier found."
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-no-task-type-classifier.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/no-task-type-classifier-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{pkg,cmd,tests}
}

setup_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
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

echo "=== check-no-task-type-classifier self-test (ADR-084 rev 9 §Q / FR-110) ==="
echo ""

# --- Test 1: planted classifier by NAME — CAUGHT ---

echo "Test 1: a function named IsCodingTask is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture1.go" '
package agent

func IsCodingTask(g *Goal) bool {
	return false
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "name-classifier-exit" 1 "$EXIT_CODE"
assert_output_contains "name-classifier-finding" "fixture1.go" "$OUTPUT"

# --- Test 2: planted classifier by DOC COMMENT only — CAUGHT ---

echo ""
echo "Test 2: an unrelated-named function whose doc comment claims to classify is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture2.go" '
package agent

// route decides whether the goal is a coding task and picks a rubric
// accordingly.
func route(g *Goal) string {
	return ""
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "doc-classifier-exit" 1 "$EXIT_CODE"
assert_output_contains "doc-classifier-finding" "fixture2.go" "$OUTPUT"

# --- Test 3: reverse word order — CAUGHT ---

echo ""
echo "Test 3: reverse order ('task coding') in a doc comment is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture3.go" '
package agent

// classify returns true if this criterion is task coding in nature.
func classify(c *Criterion) bool {
	return false
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "reverse-order-exit" 1 "$EXIT_CODE"
assert_output_contains "reverse-order-finding" "fixture3.go" "$OUTPUT"

# --- Test 4: underscore-joined spelling — CAUGHT ---

echo ""
echo "Test 4: an underscore-joined coding_goal name is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture4.go" '
package agent

func is_coding_goal(g *Goal) bool {
	return false
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "underscore-exit" 1 "$EXIT_CODE"
assert_output_contains "underscore-finding" "fixture4.go" "$OUTPUT"

# --- Test 5: the real sanctioned NonCodingGoal test shape — NOT caught ---

echo ""
echo "Test 5: a NonCodingGoal test function (the real sanctioned shape) is NOT caught"
setup_skeleton
setup_fixture "pkg/agent/fixture5_test.go" '
package agent

import "testing"

// TestNonCodingGoal_EmailCriterion_DecidedAtTierOne proves a send_email
// criterion resolves with zero shell executions — no classifier needed.
func TestNonCodingGoal_EmailCriterion_DecidedAtTierOne(t *testing.T) {
	t.Skip("fixture")
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "noncoding-goal-exit" 0 "$EXIT_CODE"
assert_output_not_contains "noncoding-goal-not-flagged" "fixture5_test.go" "$OUTPUT"

# --- Test 6: encoding/decoding co-occurring with "task" — NOT caught ---

echo ""
echo "Test 6: legitimate encoding/decoding prose co-occurring with 'task' is NOT caught"
setup_skeleton
setup_fixture "pkg/agent/fixture6.go" '
package agent

// runEncodingTask base64-encodes the attachment before the upload task
// proceeds; unrelated to any goal or criterion classification.
func runEncodingTask(b []byte) []byte {
	return b
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "encoding-task-exit" 0 "$EXIT_CODE"
assert_output_not_contains "encoding-task-not-flagged" "fixture6.go" "$OUTPUT"

# --- Test 7: "coding tasks" attached to a type/struct, not a func — NOT caught ---

echo ""
echo "Test 7: 'coding tasks' prose attached to a type declaration (not a func) is NOT caught"
setup_skeleton
setup_fixture "pkg/routing/fixture7.go" '
package routing

// RuleClassifier scores message complexity for model routing.
// code block present: 0.40 — coding tasks need the heavy model
type RuleClassifier struct{}

// Score computes the complexity score for the given feature set.
func (c *RuleClassifier) Score(f int) float64 {
	return 0
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "routing-classifier-exit" 0 "$EXIT_CODE"
assert_output_not_contains "routing-classifier-not-flagged" "fixture7.go" "$OUTPUT"

# --- Test 8: a blank line breaks doc-comment adjacency — NOT caught ---

echo ""
echo "Test 8: a comment separated from the func by a blank line is NOT caught"
setup_skeleton
setup_fixture "pkg/agent/fixture8.go" '
package agent

// This paragraph mentions a coding task classifier only to say ADR-084
// forbids one; it is not attached to the function below.

func unrelated() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "blank-line-break-exit" 0 "$EXIT_CODE"
assert_output_not_contains "blank-line-break-not-flagged" "fixture8.go" "$OUTPUT"

# --- Test 9: a clean tree with nothing of the sort anywhere — exits 0 ---

echo ""
echo "Test 9: a clean tree with no classifier anywhere exits 0"
setup_skeleton
setup_fixture "pkg/agent/fixture9.go" '
package agent

func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clean-tree-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-tree-ok-message" "OK:" "$OUTPUT"

# --- Test 10: a tree missing a required scan directory refuses to report green ---

echo ""
echo "Test 10: a tree missing a required directory exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/pkg"
# Deliberately omit cmd/ and tests/.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-dir-exit" 2 "$EXIT_CODE"

# --- Test 11: zero discovered .go files exits 2, never a silent green ---

echo ""
echo "Test 11: a scan that discovers zero .go files exits 2 (never a silent green)"
setup_skeleton
# pkg/, cmd/, tests/ all exist but are empty — no *.go files anywhere.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "zero-files-exit" 2 "$EXIT_CODE"

# --- Test 12: a forced awk failure is a hard failure, not a false "OK" -----

echo ""
echo "Test 12: an awk program failure is reported as exit 2, not swallowed as 'OK'"
setup_skeleton
setup_fixture "pkg/agent/fixture12.go" '
package agent

func noop() {}
'
BROKEN_AWK="$TMP_DIR/broken.awk"
printf '%s\n' 'BEGIN { this is not valid awk syntax ((( }' > "$BROKEN_AWK"
OUTPUT=$(REPO_ROOT="$TMP_DIR" CHECK_NO_TASK_TYPE_CLASSIFIER_AWK_OVERRIDE="$BROKEN_AWK" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "awk-failure-exit" 2 "$EXIT_CODE"
assert_output_not_contains "awk-failure-not-false-ok" "OK: no task-type classifier" "$OUTPUT"
# The override file itself must survive the run — the guard must never
# delete a fixture file it did not create.
if [ -f "$BROKEN_AWK" ]; then
  echo "  PASS [awk-override-file-survives]: override file was not deleted by the guard's own cleanup"
  PASS=$((PASS + 1))
else
  echo "  FAIL [awk-override-file-survives]: override file was deleted by the guard's own cleanup"
  FAIL=$((FAIL + 1))
  ERRORS+=("[awk-override-file-survives] the guard deleted a caller-supplied override file")
fi

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
