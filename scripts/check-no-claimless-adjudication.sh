#!/usr/bin/env bash
# check-no-claimless-adjudication.sh — ADR-084 revision-9 D13 / JUDGE-FR-095,
# FR-097 mechanical guard (wave G6,
# docs/internal/specs/adr-084-086-joint-delivery-plan.md, C-13, T2's row).
#
# ADR-084 revision 9 D13 retires claimless idle adjudication in full: a
# `met` claim (via the goal_claim tool or the GOAL_STATUS marker) is now the
# SOLE adjudication trigger (JUDGE-FR-095). A quiet or stalled RECORDED goal
# gets the keeper's bounded continue-push instead — never a Judge call,
# never a round consumed (JUDGE-FR-097). Wave E13 (this delivery, round 6)
# performed the removal: it deleted the FR-014b zero-adjudicable-output
# triple predicate `goalZeroOutputTripleHolds`, the session-scoped output
# helper `sessionHasTranscriptOutputSince`, and their two evidence/diff
# helper terms `goalZeroEvidenceRecords` / `goalHasTranscriptOutputSince`
# outright (not merely stopped calling them), and rewrote
# `settleGoalNormally` (pkg/agent/goal_triggers.go) so the one remaining
# claimless call site — `runGoalAdjudication(…, claimText: "")`, logged as
# "goal idle settle: firing claimless adjudication after quiet window" — no
# longer exists.
#
# Per C-13's resolution: "the negative half (the two symbols are
# unreferenced) moves out of Go and into a guard script, because a Go test
# asserting a symbol is unreferenced is a source-text scan and cannot
# survive a merge." This script IS that guard. A merge from a
# pre-D13/pre-E13 branch, or an agent that re-derives the old triple logic
# without reading the withdrawal, can resurrect any of the four symbols or
# the claimless call as an ordinary, conflict-free addition — this script
# fails the build when that happens.
#
# ─── What this guards, precisely ───────────────────────────────────────────
#   1. The four retired FR-014b symbols, as a definition OR a non-comment
#      reference anywhere under pkg/agent/:
#        goalZeroOutputTripleHolds, sessionHasTranscriptOutputSince,
#        goalZeroEvidenceRecords, goalHasTranscriptOutputSince.
#      (KEPT and NOT guarded: `primeGoalZeroOutputTripleFalse`, a
#      differently-named test helper E13 deliberately retained — see its own
#      doc comment in pkg/agent/goal_triggers_test.go — so that wave E12's
#      pkg/agent/goal_flow_integration_test.go, outside E13's write-set,
#      keeps compiling. It shares no substring with any guarded name above
#      that this regex could accidentally catch.)
#   2. The retired claimless-adjudication log line, the exact text D13's own
#      removal note (judge spec §C22) names as "the whole of D13's
#      removal": "firing claimless adjudication after quiet window".
#
# ─── Scope ──────────────────────────────────────────────────────────────────
# pkg/agent/ only (recursive) — every one of the four symbols and the log
# line lives, or lived, exclusively in that package tree; verified today: a
# repo-wide grep for all four symbols returns hits only under pkg/agent/.
# Excluded within scope: this script itself, its .test.sh companion,
# generated/ dirs, node_modules, .git, and comment-only mentions (a
# retirement comment naming a symbol in prose — including this file's own
# header, and the doc comments in goal_triggers.go/goal_triggers_test.go/
# goal_keeper_repairs_test.go/goal_flow_integration_test.go/
# verifier_adjudication.go that explain the withdrawal — is the sanctioned
# way to reference these names, not a violation).
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
#
# F3-style hardening (mirrors check-no-orphan-turn-watchdog.sh /
# check-no-unable-to-verify-outcome.sh): the scan grep's exit status is
# captured directly, never swallowed with `2>/dev/null || true`. grep exits
# 0 (match), 1 (no match — the expected common case), or >1 (a real
# failure). Any grep exit >1 is a hard failure of the check itself (exit 2),
# never a silently-swallowed "no matches".
#
# TEST-ONLY OVERRIDE: CHECK_NO_CLAIMLESS_ADJUDICATION_PATTERN_OVERRIDE, if
# set, replaces the SYMBOLS pattern below. It exists solely so this script's
# .test.sh companion can inject a deliberately invalid ERE to force a real
# grep exit>1 and assert this script reports it as exit 2, not a false "OK".
# Never set in CI/Makefile/pr.yml — production runs always use the real
# pattern.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-claimless-adjudication: cannot cd to $REPO_ROOT" >&2; exit 2; }

if [ ! -d pkg/agent ]; then
  echo "check-no-claimless-adjudication: expected directory 'pkg/agent' not found under $REPO_ROOT" >&2
  echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
  echo "   verdict for a tree this script never actually scanned)" >&2
  exit 2
fi

SYMBOLS='goalZeroOutputTripleHolds|sessionHasTranscriptOutputSince|goalZeroEvidenceRecords|goalHasTranscriptOutputSince|firing claimless adjudication after quiet window'

# Test-only override — see the hardening note above the Exit line.
SYMBOLS="${CHECK_NO_CLAIMLESS_ADJUDICATION_PATTERN_OVERRIDE:-$SYMBOLS}"

GREP_STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-no-claimless-adjudication-stderr.XXXXXX")"
trap 'rm -f "$GREP_STDERR_FILE"' EXIT

# Recursive, extension-filtered scan of pkg/agent/ only (see Scope above).
# Exit status captured directly: 0 (matched) and 1 (no matches — the normal,
# expected outcome) both proceed to the filtering step below; anything >1 is
# a real grep failure (bad regex, unreadable file, ...) and is a hard
# failure of the check itself, never a silently-swallowed "no matches".
dir_hits=$(grep -rnE "$SYMBOLS" --include='*.go' pkg/agent 2>"$GREP_STDERR_FILE")
dir_status=$?
if [ "$dir_status" -gt 1 ]; then
  echo "check-no-claimless-adjudication: grep failed while scanning pkg/agent (exit $dir_status)" >&2
  cat "$GREP_STDERR_FILE" >&2
  exit 2
fi

hits=$(printf '%s\n' "$dir_hits" \
  | grep -v '/generated/' \
  | grep -v 'node_modules/' \
  | grep -v '^scripts/check-no-claimless-adjudication\.sh:' \
  | grep -v '^scripts/check-no-claimless-adjudication\.test\.sh:' \
  || true)

# Drop comment-only lines: Go `//` line comments and block-comment
# continuation lines starting with `*` or `/*`. A retirement comment naming
# a symbol in prose is the sanctioned way to reference it, not a violation —
# this is exactly how goal_triggers.go, goal_triggers_test.go,
# goal_keeper_repairs_test.go, goal_flow_integration_test.go and
# verifier_adjudication.go reference these names today.
violations=$(echo "$hits" | awk -F: '
  NF >= 3 {
    line = ""
    for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
    sub(/^[[:space:]]+/, "", line)
    if (line ~ /^\/\//) next
    if (line ~ /^\*/) next
    if (line ~ /^\/\*/) next
    print $0
  }' | grep -v '^$' || true)

if [ -n "$violations" ]; then
  echo "ERROR: retired ADR-084 revision-9 D13 claimless-adjudication symbol(s) or log line present:" >&2
  echo "" >&2
  echo "$violations" >&2
  echo "" >&2
  echo "ADR-084 revision 9 D13 retires claimless idle adjudication in full — a met claim" >&2
  echo "(goal_claim tool or GOAL_STATUS marker) is the sole adjudication trigger" >&2
  echo "(JUDGE-FR-095). goalZeroOutputTripleHolds, sessionHasTranscriptOutputSince," >&2
  echo "goalZeroEvidenceRecords and goalHasTranscriptOutputSince were deleted outright by" >&2
  echo "wave E13, and settleGoalNormally's rewrite removed the last claimless" >&2
  echo "runGoalAdjudication call site (JUDGE-FR-097). If this came from a merge, resolve" >&2
  echo "by KEEPING THE DELETION — see CLAUDE.md 'Retired surfaces' and" >&2
  echo "docs/internal/specs/adr-084-086-joint-delivery-plan.md C-13." >&2
  exit 1
fi

echo "OK: no retired ADR-084 revision-9 D13 claimless-adjudication symbols or log line found under pkg/agent."
