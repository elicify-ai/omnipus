#!/usr/bin/env bash
# check-no-goal-field-erasure.test.sh
#
# Proof-of-failure companion for check-no-goal-field-erasure.sh (ADR-086
# GOAL-FR-027/FR-028). A guard that cannot go red manufactures the green it
# was meant to withhold (docs/internal/false-green-patterns.md), so this
# builds scratch fixture trees under a temp directory, points the guard at
# each via REPO_ROOT, and NEVER touches the real repo tree.
#
# Modelled on scripts/check-no-orphan-turn-watchdog.test.sh — the one guard
# companion in this repository that actually plants offenders (the delivery
# plan's R-19(a) names it as the template) — and it satisfies R-19(b): every
# case below exercises the guard against a planted offender or a clean tree
# and PRINTS the guard's observed exit code for each.
#
# Covers, clause by clause:
#
#   CAUGHT (guard must exit 1)
#    1. Clause A — u5GoalFile and the retired Goal* meta group re-declared in
#       pkg/session non-test Go.
#    2. Clause A — the retired on-disk json tags (goal_condition,
#       goal_route_chat_id) alone, with no Go identifier.
#    3. Clause B — clearGoal-style zeroing: meta.GoalCondition = "",
#       meta.GoalRoundsUsed = 0, in pkg/agent.
#    4. Clause B — the same erasure written as struct-literal entries
#       (GoalCondition: "", GoalCriteriaJSON: "").
#    5. Clause C — a terminal path emptying the retained record's criteria
#       (cur.Criteria = nil, cur.DoD = []task.AcceptanceCriterion{}) in
#       pkg/agent/goal_loop.go.
#    6. Clause C — the same inside pkg/goal (g.Prompt = "").
#
#   NOT CAUGHT (guard must exit 0) — each of these is legitimate today, and
#   flagging any of them would make the guard red on a clean tree:
#    7. The retirement comments pkg/session/unified.go and daypartition.go
#       actually carry, naming every deleted field in prose.
#    8. config.PlanningConfig.GoalMaxRounds and DefaultGoalMaxRounds outside
#       pkg/session — the one global Settings -> Performance budget (D-D/D-E).
#    9. pkg/goal/status.go::Reactivate's legitimate reset of LatestReason and
#       LatestVerdict, taken AFTER appending the prior run to TerminalHistory.
#   10. Real-value assignments to Criteria/DoD/Prompt (goal_compile.go and
#       pkg/goal/criteria.go do this on every compile).
#   11. Comparisons: `rec.GoalRef == ""`, `cfg.GoalMaxRounds == 0`,
#       `g.Criteria == nil` — none is an assignment.
#   12. A _test.go file containing every offending shape — FR-028's rule is
#       "no NON-TEST call site", and the tests asserting the old shape is
#       gone must be free to name it.
#   13. A clean tree with none of the shapes anywhere.
#
#   CANNOT RUN (guard must exit 2, never a silent green)
#   14. A tree missing a required scan directory.
#   15. A forced grep failure (an invalid ERE injected through the guard's
#       test-only CHECK_NO_GOAL_FIELD_ERASURE_PATTERN_OVERRIDE) is reported
#       as exit 2 with grep's error on stderr — not swallowed into a false
#       "OK: no goal-field erasure".
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-no-goal-field-erasure.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/goal-field-erasure-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

# Every fixture tree needs the directories the guard requires to exist, or it
# would correctly refuse to run (exit 2) rather than scan nothing.
setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{pkg/session,pkg/goal,pkg/agent,cmd}
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

echo "=== check-no-goal-field-erasure self-test (ADR-086 GOAL-FR-027/FR-028) ==="
echo ""

# --- Test 1: clause A, the retired meta group re-declared — CAUGHT ----------

echo "Test 1: u5GoalFile and the Goal* meta group back in pkg/session is caught"
setup_skeleton
setup_fixture "pkg/session/unified_meta_files.go" '
package session

type u5GoalFile struct {
	GoalCondition  string `json:"goal_condition,omitempty"`
	GoalRoundsUsed int    `json:"goal_rounds_used,omitempty"`
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clauseA-group-exit" 1 "$EXIT_CODE"
assert_output_contains "clauseA-group-finding" "unified_meta_files.go" "$OUTPUT"
assert_output_contains "clauseA-group-reason" "retired session-meta goal-field group" "$OUTPUT"

# --- Test 2: clause A, the json tags alone — CAUGHT -------------------------

echo ""
echo "Test 2: the retired on-disk json tags alone are caught"
setup_skeleton
setup_fixture "pkg/session/relapse.go" '
package session

type sidecar struct {
	A string `json:"goal_route_chat_id,omitempty"`
	B string `json:"goal_latest_reason,omitempty"`
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clauseA-tags-exit" 1 "$EXIT_CODE"
assert_output_contains "clauseA-tags-finding" "relapse.go" "$OUTPUT"

# --- Test 3: clause B, clearGoal-style zeroing — CAUGHT ---------------------

echo ""
echo "Test 3: clearGoal-style field zeroing in pkg/agent is caught"
setup_skeleton
setup_fixture "pkg/agent/goal_loop.go" '
package agent

func (al *AgentLoop) clearGoal(meta *Meta) {
	meta.GoalCondition = ""
	meta.GoalRoundsUsed = 0
	meta.GoalCriteriaJSON = ""
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clauseB-assign-exit" 1 "$EXIT_CODE"
assert_output_contains "clauseB-assign-finding" "goal_loop.go" "$OUTPUT"
assert_output_contains "clauseB-assign-reason" "goal-field ZEROING" "$OUTPUT"

# --- Test 4: clause B, the erasure as struct-literal entries — CAUGHT -------

echo ""
echo "Test 4: the same erasure written as struct-literal entries is caught"
setup_skeleton
setup_fixture "pkg/agent/wipe.go" '
package agent

func wipe(s *store, id string) error {
	return s.SetMeta(id, Patch{
		GoalCondition:    "",
		GoalCriteriaJSON: "",
		GoalRoundsUsed:   0,
	})
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clauseB-literal-exit" 1 "$EXIT_CODE"
assert_output_contains "clauseB-literal-finding" "wipe.go" "$OUTPUT"

# --- Test 5: clause C, the retained record's criteria emptied — CAUGHT ------

echo ""
echo "Test 5: emptying the retained record's criteria in a terminal path is caught"
setup_skeleton
setup_fixture "pkg/agent/goal_loop.go" '
package agent

func terminate(cur *goal.Goal) {
	cur.Criteria = nil
	cur.DoD = []task.AcceptanceCriterion{}
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clauseC-agent-exit" 1 "$EXIT_CODE"
assert_output_contains "clauseC-agent-finding" "goal_loop.go" "$OUTPUT"
assert_output_contains "clauseC-agent-reason" "must-survive field" "$OUTPUT"

# --- Test 6: clause C, inside pkg/goal — CAUGHT -----------------------------

echo ""
echo "Test 6: emptying the record's prompt inside pkg/goal is caught"
setup_skeleton
setup_fixture "pkg/goal/status.go" '
package goal

func (g *Goal) Terminate() {
	g.Prompt = ""
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clauseC-goal-exit" 1 "$EXIT_CODE"
assert_output_contains "clauseC-goal-finding" "status.go" "$OUTPUT"

# --- Test 7: retirement comments — NOT caught ------------------------------

echo ""
echo "Test 7: retirement comments naming every deleted field are NOT caught"
setup_skeleton
setup_fixture "pkg/session/unified.go" '
package session

// ADR-086 GOAL-FR-005: every goal field that used to live here (GoalID,
// GoalCondition, GoalRoundsUsed, GoalMaxRounds, GoalLatestReason,
// GoalStartedAt, GoalLastActivityAt, GoalCriteriaJSON,
// GoalQuestionRoundsUsed, GoalZeroOutputPushes, GoalRouteChannel,
// GoalRouteChatID, GoalRouteSessionKey, GoalRouteAgentID) is gone, together
// with u5GoalFile, u5GoalFromMeta, u5ReadGoalFile and u5WriteGoalLocked.
// The goal is its own stored record now.
type UnifiedMeta struct {
	SessionID string `json:"session_id"`
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "comments-exit" 0 "$EXIT_CODE"
assert_output_contains "comments-ok" "OK:" "$OUTPUT"

# --- Test 8: the live global budget field outside pkg/session — NOT caught --

echo ""
echo "Test 8: config.Planning.GoalMaxRounds outside pkg/session is NOT caught"
setup_skeleton
setup_fixture "pkg/config/validator.go" '
package config

func normalize(cfg *Config) {
	if cfg.Planning.GoalMaxRounds == 0 {
		cfg.Planning.GoalMaxRounds = DefaultGoalMaxRounds
	}
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "global-budget-exit" 0 "$EXIT_CODE"

# --- Test 9: Reactivate's legitimate reset — NOT caught ---------------------

echo ""
echo "Test 9: Reactivate's reset of LatestReason/LatestVerdict is NOT caught"
setup_skeleton
setup_fixture "pkg/goal/status.go" '
package goal

func (g *Goal) Reactivate(sessionID string, now time.Time) error {
	g.TerminalHistory = append(g.TerminalHistory, TerminalHistoryEntry{
		State:   g.State,
		Verdict: g.LatestVerdict,
	})
	g.LatestReason = ""
	g.LatestVerdict = nil
	return nil
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "reactivate-exit" 0 "$EXIT_CODE"

# --- Test 10: real-value assignments to the guarded fields — NOT caught -----

echo ""
echo "Test 10: real-value assignments to Criteria/DoD/Prompt are NOT caught"
setup_skeleton
setup_fixture "pkg/agent/goal_compile.go" '
package agent

func apply(g *goal.Goal, normCriteria, normDoD []task.AcceptanceCriterion, condition string) {
	g.Criteria = normCriteria
	g.DoD = normDoD
	g.Prompt = condition
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "real-value-exit" 0 "$EXIT_CODE"

# --- Test 11: comparisons are not assignments — NOT caught -----------------

echo ""
echo "Test 11: comparisons against zero values are NOT caught"
setup_skeleton
setup_fixture "pkg/agent/boot_sweep.go" '
package agent

func skip(rec *Record, cfg *Config, g *goal.Goal) bool {
	if rec.GoalRef == "" {
		return true
	}
	if cfg.GoalMaxRounds == 0 {
		return true
	}
	return g.Criteria == nil
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "comparison-exit" 0 "$EXIT_CODE"

# --- Test 12: a _test.go file carrying every offending shape — NOT caught ---

echo ""
echo "Test 12: the same shapes inside a _test.go file are NOT caught"
setup_skeleton
setup_fixture "pkg/agent/goal_loop_test.go" '
package agent

func TestOldShapeIsGone(t *testing.T) {
	meta.GoalCondition = ""
	meta.GoalRoundsUsed = 0
	cur.Criteria = nil
	_ = u5GoalFile{}
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "test-file-exit" 0 "$EXIT_CODE"

# --- Test 13: a clean tree — exits 0 ---------------------------------------

echo ""
echo "Test 13: a clean tree exits 0"
setup_skeleton
setup_fixture "pkg/agent/goal_loop.go" '
package agent

func (al *AgentLoop) clearGoal(goalID string, state generated.GoalState, note string) error {
	return terminateGoalRecordByID(goalID, state, note)
}
'
setup_fixture "pkg/goal/status.go" '
package goal

func (g *Goal) Terminate(state generated.GoalState, reason string, now time.Time) error {
	g.State = state
	g.TerminalReason = reason
	g.LastActivityAt = now
	return nil
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clean-tree-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-tree-ok" "OK: no goal-field erasure" "$OUTPUT"

# --- Test 14: a tree missing a required directory — exits 2 ----------------

echo ""
echo "Test 14: a tree missing a required directory exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/pkg"
# Deliberately omit pkg/session, pkg/goal, pkg/agent and cmd.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-dir-exit" 2 "$EXIT_CODE"
assert_output_not_contains "missing-dir-not-false-ok" "OK: no goal-field erasure" "$OUTPUT"

# --- Test 15: a forced grep failure — exits 2, not a false "OK" ------------

echo ""
echo "Test 15: a grep failure (invalid ERE) is reported as exit 2, not swallowed"
setup_skeleton
setup_fixture "pkg/agent/clean.go" '
package agent

func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" CHECK_NO_GOAL_FIELD_ERASURE_PATTERN_OVERRIDE='(unbalanced' bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "grep-failure-exit" 2 "$EXIT_CODE"
assert_output_not_contains "grep-failure-not-false-ok" "OK: no goal-field erasure" "$OUTPUT"

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
