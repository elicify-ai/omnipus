#!/usr/bin/env bash
# check-no-goal-confirm-gate.sh — ADR-081 D9 mechanical guard.
#
# The /goal confirm-gate machinery was deleted in full (greenfield, operator
# directive 2026-09-07): goals activate instantly, the working agent authors
# the goal record via set_goal, and steering replaces confirmation. A merge
# from a pre-ADR-081 branch can resurrect the deleted symbols as ordinary,
# conflict-free additions — this script fails the build when any of the seven
# guarded names reappears as a definition or non-comment reference.
#
# Guarded names (FR-023b, work-first-goal-flow-spec.md):
#   confirmPendingGoal, IsGoalConfirm, confirmGoalAliases, ConfirmGoalWord,
#   proposeGoalAmendment, buildGoalPendingNote, useGoalCompilingIndicator
#
# Scope: hand-written Go and TS sources. Excluded: this script, docs/,
# generated artifacts, node_modules, .git, and comment-only mentions
# (retirement comments are the sanctioned way to reference these names).
set -euo pipefail

cd "$(dirname "$0")/.."

SYMBOLS='confirmPendingGoal|IsGoalConfirm|confirmGoalAliases|ConfirmGoalWord|proposeGoalAmendment|buildGoalPendingNote|useGoalCompilingIndicator'

# Collect candidate hits in hand-written sources.
hits=$(grep -rnE "($SYMBOLS)" \
  --include='*.go' --include='*.ts' --include='*.tsx' \
  pkg/ cmd/ internal/ src/ 2>/dev/null \
  | grep -v '/generated/' \
  | grep -v '_generated' \
  | grep -v 'node_modules/' \
  || true)

# Drop comment-only lines (Go // and TS //; block-comment lines starting with *).
violations=$(echo "$hits" | awk -F: '
  NF >= 3 {
    line = ""
    for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
    sub(/^[[:space:]]+/, "", line)
    if (line ~ /^\/\// || line ~ /^\*/ || line ~ /^\/\*/) next
    print $0
  }' | grep -v '^$' || true)

if [ -n "$violations" ]; then
  echo "ERROR: retired ADR-081 confirm-gate symbol(s) present in hand-written source:" >&2
  echo "$violations" >&2
  echo "" >&2
  echo "These were deleted by ADR-081 (work-first goal flow, greenfield)." >&2
  echo "If this came from a merge, resolve by KEEPING the deletion — see" >&2
  echo "CLAUDE.md 'Retired surfaces' and docs/internal/architecture/ADR-081-work-first-goal-flow.md." >&2
  exit 1
fi

echo "OK: no retired goal confirm-gate symbols found."
