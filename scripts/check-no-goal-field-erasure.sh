#!/usr/bin/env bash
# check-no-goal-field-erasure.sh — ADR-086 GOAL-FR-027/FR-028 mechanical guard.
#
# Wave G7 of the ADR-084/085/086 joint delivery
# (docs/internal/specs/adr-084-086-joint-delivery-plan.md §3, row G7:
# "Guard 1 fails if clearGoal-style field-zeroing gains a non-test call
# site"; goal-entity spec §8 "New regression tests required", bullet 2).
#
# ─── What was deleted, and why a guard is needed ────────────────────────────
# Before ADR-086 a goal was fourteen fields hung on a chat session's meta
# (session.UnifiedMeta's Goal* group, persisted as goal.json's u5GoalFile),
# and ENDING a goal meant ZEROING those fields — pkg/agent/goal_loop.go's
# clearGoal wrote empty strings and zeros over the condition, the round
# counters, the criteria JSON and the routing group, and the goal was simply
# gone. GOAL-FR-027 replaces that with a STATUS TRANSITION on a retained
# record (pkg/goal/status.go::Terminate): "the record MUST survive with its
# criteria, their final statuses, the verdict, the reason and any handover".
# GOAL-FR-028 requires the field-zeroing itself be deleted as a terminal
# mechanism and all eight call sites converted to transitions.
#
# The verdict -> criterion-status projection (GOAL-FR-036,
# pkg/agent/verdict_projection.go) writes per-criterion outcomes INTO the
# record the old code deleted one statement later. So an erasure that comes
# back does not merely lose history — it silently empties the exact list the
# operator's "make goal progress visible" depends on, on the met path, where
# visibility matters most (goal spec Row 12's rationale).
#
# Every branch in this delivery was cut before or alongside this guard, so a
# `git merge` from a pre-ADR-086 branch can re-add the erasure as an
# ordinary, conflict-free addition. This script fails the build when it
# does. Resolve such a conflict by KEEPING THE DELETION (CLAUDE.md, "Retired
# surfaces — do NOT reintroduce"; delivery plan §6 rule 8).
#
# ─── The three clauses ──────────────────────────────────────────────────────
#
# CLAUSE A — the retired session-meta goal-field group must not return to
#   pkg/session. Scanned names are the exact members wave S6 deleted from
#   UnifiedMeta / u5GoalFile, plus the four helpers that carried them:
#   u5GoalFile, u5GoalFromMeta, u5ReadGoalFile, u5WriteGoalLocked;
#   GoalCondition, GoalRoundsUsed, GoalMaxRounds, GoalLatestReason,
#   GoalStartedAt, GoalLastActivityAt, GoalCriteriaJSON,
#   GoalQuestionRoundsUsed, GoalZeroOutputPushes, GoalRouteChannel,
#   GoalRouteChatID, GoalRouteSessionKey, GoalRouteAgentID; and their
#   on-disk json tags (goal_condition, goal_rounds_used, goal_max_rounds,
#   goal_latest_reason, goal_started_at, goal_last_activity_at,
#   goal_criteria, goal_question_rounds_used, goal_zero_output_pushes,
#   goal_route_channel, goal_route_chat_id, goal_route_session_key,
#   goal_route_agent_id).
#
#   Deliberately NOT in clause A's list, though it was a member of the
#   deleted group: the bare name `GoalID`. It is the goal record's own
#   primary key in pkg/goal and pkg/agent, and a future legitimate
#   session-side reference to a goal's id (pkg/session/lifecycle.go already
#   carries `GoalRef`) must not be mistaken for the retired meta field. The
#   other thirteen names identify the group unambiguously on their own.
#
#   Clause A is scoped to pkg/session because two of its names are live,
#   legitimate identifiers ELSEWHERE: config.PlanningConfig.GoalMaxRounds
#   (the one global Settings -> Performance budget, D-D/D-E) and
#   DefaultGoalMaxRounds are real and must keep compiling. Scoping by
#   directory, not by exemption, is what keeps those out of range.
#
# CLAUSE B — a goal-field ZEROING assignment must not appear in non-test Go
#   anywhere under pkg/ or cmd/: `<expr>.Goal<Field> = ""|0|nil|false`, or
#   the same shape as a struct-literal entry `Goal<Field>: ""|0|nil|false`.
#   This is the erasure statement itself, caught wherever it is written —
#   including a re-point at some other struct that grew a Goal* field. It
#   does not match a comparison (`== 0`, `!= ""`): the character before the
#   `=` may not be `=`, `!`, `<` or `>`.
#
# CLAUSE C — the retained goal record's must-survive fields must not be
#   zeroed, in non-test Go under pkg/goal/ and pkg/agent/goal_*.go (the
#   terminal paths: clearGoal, terminateGoalRecordByID, Goal.Terminate,
#   goalIdleExpirySweep, runGoalAdjudication). Fields: Criteria, DoD,
#   Prompt, TerminalHistory, SupersededCriteria — assigned nil, "", 0 or an
#   empty composite literal. Assigning a real value to any of them is
#   normal and is NOT flagged (goal_compile.go and pkg/goal/criteria.go do
#   it on every compile); only the zero shapes are.
#
#   Two fields of the record are deliberately EXCLUDED from clause C:
#   LatestReason and LatestVerdict. pkg/goal/status.go::Reactivate zeroes
#   both, legitimately, when a task-owned goal re-enters active on a task
#   re-run (R-04) — and it does so AFTER appending the prior run's state,
#   reason, verdict and counters to TerminalHistory. That is retention, not
#   erasure, and flagging it would make this guard red on a clean tree.
#
# ─── Scope and exclusions ───────────────────────────────────────────────────
# Hand-written, non-test Go only. `_test.go` files are out of scope by
# construction: FR-028's requirement is that the erasure has no NON-TEST
# call site, and the tests that assert the old shape is gone must be free to
# name it. Generated artifacts, node_modules, .git, docs/ and scripts/ are
# outside the scan roots entirely.
#
# Comment-only lines are not violations: a retirement comment naming the old
# fields in prose is the sanctioned way to record what went away, and both
# pkg/session/unified.go and pkg/session/daypartition.go carry exactly such
# a comment today. Detection is leading-token based (a line whose first
# non-space characters are `//`, `*`, `/*` or `#`), the same rule
# check-no-orphan-turn-watchdog.sh uses. A violation hidden behind code on
# the same line as a trailing comment is therefore still caught, and a
# trailing comment quoting an offender would be reported — erring toward a
# false RED, never a false green.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
#
# Every grep's exit status is captured directly rather than swallowed with
# `|| true`: grep exits 0 (match), 1 (no match — the expected common case),
# or >1 (a real failure: invalid regex, unreadable file). Treating all three
# as "no matches" is the false-green pattern documented in
# docs/internal/false-green-patterns.md; anything >1 is a hard exit 2 here.
#
# TEST-ONLY OVERRIDE: CHECK_NO_GOAL_FIELD_ERASURE_PATTERN_OVERRIDE, if set,
# replaces clause A's pattern. It exists solely so
# check-no-goal-field-erasure.test.sh can inject a deliberately invalid ERE
# and assert this script reports exit 2 rather than a false "OK". Never set
# it in CI, the Makefile or pr.yml.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-goal-field-erasure: cannot cd to $REPO_ROOT" >&2; exit 2; }

# Required directories. A missing one means a wrong cwd, a renamed package
# or a partial checkout — refuse to report green for a tree never scanned.
REQUIRED_DIRS=(pkg pkg/session pkg/goal pkg/agent cmd)
for d in "${REQUIRED_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-no-goal-field-erasure: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

# ─── Patterns ───────────────────────────────────────────────────────────────

# Clause A: the retired session-meta goal-field group (identifiers + json tags).
CLAUSE_A_PATTERN='u5GoalFile|u5GoalFromMeta|u5ReadGoalFile|u5WriteGoalLocked|GoalCondition|GoalRoundsUsed|GoalMaxRounds|GoalLatestReason|GoalStartedAt|GoalLastActivityAt|GoalCriteriaJSON|GoalQuestionRoundsUsed|GoalZeroOutputPushes|GoalRouteChannel|GoalRouteChatID|GoalRouteSessionKey|GoalRouteAgentID|goal_condition|goal_rounds_used|goal_max_rounds|goal_latest_reason|goal_started_at|goal_last_activity_at|goal_criteria|goal_question_rounds_used|goal_zero_output_pushes|goal_route_channel|goal_route_chat_id|goal_route_session_key|goal_route_agent_id'
CLAUSE_A_PATTERN="${CHECK_NO_GOAL_FIELD_ERASURE_PATTERN_OVERRIDE:-$CLAUSE_A_PATTERN}"

# Clause B: a Goal* field zeroed, as a field assignment or a literal entry.
#
# A comparison is NOT matched, and no negative lookahead is needed to keep it
# out: the pattern requires the zero literal to follow the FIRST `=`, so
# `== 0` fails (after that `=` comes another `=`, not `0`) and `!= ""` /
# `>= 0` fail earlier still (the first non-space character after the field
# name is `!` or `>`, not `=`).
#
# No `^` or `$` appears inside a group anywhere in these patterns: GNU grep
# treats a mid-pattern anchor as an anchor and BSD grep (the default on
# macOS, where this guard is also run by hand) may treat it as a literal, so
# the two would disagree about what the guard even scans for. The cost is a
# missing trailing-delimiter requirement — `= 0x1F` would be read as `= 0` —
# which over-matches rather than under-matches, and no Goal* field in the
# tree is a type a hex literal could be assigned to.
ZERO_RHS='(""|0|nil|false)'
CLAUSE_B_ASSIGN_PATTERN="\\.Goal[A-Z][A-Za-z0-9_]*[[:space:]]*=[[:space:]]*${ZERO_RHS}"
CLAUSE_B_LITERAL_PATTERN="[^[:alnum:]_.]Goal[A-Z][A-Za-z0-9_]*:[[:space:]]*${ZERO_RHS}[[:space:]]*[,}]"

# Clause C: the retained record's must-survive fields zeroed. The second
# alternative covers an empty composite literal
# (`[]task.AcceptanceCriterion{}`, `[]TerminalHistoryEntry{}`).
CLAUSE_C_FIELDS='Criteria|DoD|Prompt|TerminalHistory|SupersededCriteria'
CLAUSE_C_PATTERN="\\.(${CLAUSE_C_FIELDS})[[:space:]]*=[[:space:]]*(nil|\"\"|0|\\[\\][A-Za-z0-9_.]*\\{[[:space:]]*\\})"

# ─── Helpers ────────────────────────────────────────────────────────────────

GREP_STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-goal-field-erasure-stderr.XXXXXX")"
trap 'rm -f "$GREP_STDERR_FILE"' EXIT

# drop_comment_lines reads `path:line:content` records on stdin and drops the
# ones whose content begins with a comment token.
drop_comment_lines() {
  awk -F: '
    NF >= 3 {
      line = ""
      for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
      sub(/^[[:space:]]+/, "", line)
      if (line ~ /^\/\//) next
      if (line ~ /^\*/) next
      if (line ~ /^\/\*/) next
      if (line ~ /^#/) next
      print $0
    }'
}

# scan <clause-label> <pattern> <outfile> <path>...  — writes the non-test,
# non-comment hits to <outfile> and returns 0; returns 2 if grep itself
# failed.
#
# The hits go to a FILE rather than to stdout because the caller would
# otherwise have to run this in a command substitution — and an `exit 2`
# inside `$(...)` terminates only the subshell, leaving the parent to finish
# and print "OK". That is precisely the false green this guard's exit-2 path
# exists to prevent, and the self-test's forced-grep-failure case caught it
# here before it ever ran in CI.
scan() {
  local label="$1" pattern="$2" outfile="$3"
  shift 3
  local raw status
  : > "$outfile"
  : > "$GREP_STDERR_FILE"
  raw=$(grep -rnE "$pattern" --include='*.go' "$@" 2>"$GREP_STDERR_FILE")
  status=$?
  if [ "$status" -gt 1 ]; then
    echo "check-no-goal-field-erasure: grep failed while scanning for $label (exit $status)" >&2
    cat "$GREP_STDERR_FILE" >&2
    return 2
  fi
  [ -z "$raw" ] && return 0
  printf '%s\n' "$raw" \
    | grep -v '_test\.go:' \
    | grep -v '/generated/' \
    | grep -v 'node_modules/' \
    | drop_comment_lines \
    | grep -v '^$' > "$outfile"
  return 0
}

# run_scan is scan plus the hard stop: any grep failure ends the whole
# script with exit 2 in the PARENT shell, never a swallowed subshell exit.
HITS_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-goal-field-erasure-hits.XXXXXX")"
trap 'rm -f "$GREP_STDERR_FILE" "$HITS_FILE"' EXIT

run_scan() {
  scan "$@"
  local rc=$?
  if [ "$rc" -ne 0 ]; then
    exit 2
  fi
}

# ─── Clause A ───────────────────────────────────────────────────────────────

run_scan "clause A (retired session-meta goal-field group)" "$CLAUSE_A_PATTERN" "$HITS_FILE" pkg/session
A_HITS="$(cat "$HITS_FILE")"

# ─── Clause B ───────────────────────────────────────────────────────────────

run_scan "clause B (Goal* field zeroing, assignment)" "$CLAUSE_B_ASSIGN_PATTERN" "$HITS_FILE" pkg cmd
B_ASSIGN_HITS="$(cat "$HITS_FILE")"
run_scan "clause B (Goal* field zeroing, struct literal)" "$CLAUSE_B_LITERAL_PATTERN" "$HITS_FILE" pkg cmd
B_LITERAL_HITS="$(cat "$HITS_FILE")"
B_HITS=$(printf '%s\n%s\n' "$B_ASSIGN_HITS" "$B_LITERAL_HITS" | grep -v '^$')

# ─── Clause C ───────────────────────────────────────────────────────────────
#
# pkg/goal in full, plus pkg/agent's goal-owning files only. task_executor.go's
# `t.Criteria = projected` is a TASK's criteria list being written with a real
# value, not a goal record being emptied, and nothing outside the goal files
# performs a terminal transition.
C_PATHS=(pkg/goal)
while IFS= read -r f; do
  [ -n "$f" ] && C_PATHS+=("$f")
done < <(find pkg/agent -maxdepth 1 -name 'goal_*.go' ! -name '*_test.go' 2>/dev/null | sort)

run_scan "clause C (retained goal-record field zeroing)" "$CLAUSE_C_PATTERN" "$HITS_FILE" "${C_PATHS[@]}"
C_HITS="$(cat "$HITS_FILE")"

# ─── Verdict ────────────────────────────────────────────────────────────────

FOUND=0

if [ -n "$A_HITS" ]; then
  FOUND=1
  echo "ERROR: the retired session-meta goal-field group is back in pkg/session (ADR-086 GOAL-FR-005/FR-027):" >&2
  echo "" >&2
  echo "$A_HITS" >&2
  echo "" >&2
  echo "  A goal is its own stored record (pkg/goal.Store), not fourteen fields on a session's" >&2
  echo "  meta. Wave S6 deleted u5GoalFile and the Goal* group outright. If this came from a" >&2
  echo "  merge, resolve by KEEPING THE DELETION." >&2
  echo "" >&2
fi

if [ -n "$B_HITS" ]; then
  FOUND=1
  echo "ERROR: clearGoal-style goal-field ZEROING found in non-test Go (ADR-086 GOAL-FR-027/FR-028):" >&2
  echo "" >&2
  echo "$B_HITS" >&2
  echo "" >&2
  echo "  Ending a goal is a STATUS TRANSITION on a retained record — pkg/goal/status.go::Terminate," >&2
  echo "  reached through pkg/agent/goal_loop.go::terminateGoalRecordByID — never an erasure." >&2
  echo "  The record must survive with its criteria, their final statuses, the verdict, the reason" >&2
  echo "  and any handover, because the verdict -> criterion-status projection (GOAL-FR-036," >&2
  echo "  pkg/agent/verdict_projection.go) writes into exactly those lists." >&2
  echo "" >&2
fi

if [ -n "$C_HITS" ]; then
  FOUND=1
  echo "ERROR: a retained goal record's must-survive field is being zeroed (ADR-086 GOAL-FR-027):" >&2
  echo "" >&2
  echo "$C_HITS" >&2
  echo "" >&2
  echo "  Criteria, DoD, Prompt, TerminalHistory and SupersededCriteria must survive a goal's" >&2
  echo "  ending. Writing a real value to any of them is fine; emptying one is the erasure" >&2
  echo "  ADR-086 deleted. (LatestReason and LatestVerdict are NOT guarded here — pkg/goal/" >&2
  echo "  status.go::Reactivate resets both on a task re-run, after appending the prior run to" >&2
  echo "  TerminalHistory. That is retention, not erasure.)" >&2
  echo "" >&2
fi

if [ "$FOUND" -ne 0 ]; then
  echo "See docs/internal/architecture/ADR-086-goal-as-a-first-class-entity.md and" >&2
  echo "docs/internal/specs/goal-entity-spec.md (GOAL-FR-027, GOAL-FR-028)." >&2
  exit 1
fi

echo "OK: no goal-field erasure — every goal ending is a status transition on a retained record."
