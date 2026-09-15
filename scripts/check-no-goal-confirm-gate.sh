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
# Scope: hand-written Go and TS sources under pkg/, cmd/, src/ only — no
# internal/ (this repo has none; see the existence check below, mirroring
# check-no-fail-closed-backfill.sh's sibling guard). Excluded within scope:
# this script, docs/, generated artifacts, node_modules, .git, and
# comment-only mentions (retirement comments are the sanctioned way to
# reference these names).
#
# Review-round-1 finding #13: siblings (check-no-fail-closed-backfill.sh)
# refuse to report green when their scan dirs are missing (exit 2) — this
# script used to grep a nonexistent internal/ dir with suppressed stderr and
# a masking `|| true`, so a broken checkout (wrong cwd, a renamed package, a
# partial clone missing pkg/ or cmd/ or src/ entirely) would scan NOTHING
# and still print "OK", the exact false-green trap this repo's own
# false-green-patterns.md warns about. Mirrors the sibling's existence-check
# pattern exactly.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-goal-confirm-gate: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg cmd src; do
  if [ ! -d "$d" ]; then
    echo "check-no-goal-confirm-gate: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

SYMBOLS='confirmPendingGoal|IsGoalConfirm|confirmGoalAliases|ConfirmGoalWord|proposeGoalAmendment|buildGoalPendingNote|useGoalCompilingIndicator'

# Collect candidate hits in hand-written sources.
hits=$(grep -rnE "($SYMBOLS)" \
  --include='*.go' --include='*.ts' --include='*.tsx' \
  pkg/ cmd/ src/ 2>/dev/null \
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
