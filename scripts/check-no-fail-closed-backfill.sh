#!/usr/bin/env bash
# check-no-fail-closed-backfill.sh
#
# Regression guard for ADR-077: tool policy is exactly TWO layers (the
# reconciled global ceiling IS the default; sparse per-agent overrides only
# tighten) and no fail-closed per-agent "deny" backfill exists between them.
# This script fails the build if either retired mechanism comes back.
#
# WHY A SCRIPT AND NOT JUST A NOTE
#
# A note in CLAUDE.md or an ADR tells a human. It does not stop `git merge`.
# Branches cut before this change still contain
# config.RepairIncompleteToolPolicyCoverage, config.ValidateAgentOwnToolPolicyCoverage,
# and their wiring into pkg/gateway/gateway.go; merging or rebasing any of
# them re-adds these functions and call sites as an ordinary, conflict-free
# addition — git has no idea the deletion was deliberate. This repo's own
# CLAUDE.md already documents the same failure mode for the JPEG screencast
# path and the Command Center / Schedules UI ("a merge can resurrect these
# files/surfaces — always resolve by keeping the deletion"). This is the
# mechanical half of that rule for ADR-077.
#
# WHAT WAS REMOVED AND WHY IT MUST NOT RETURN
#
# config.RepairIncompleteToolPolicyCoverage used to backfill any (agent, tool)
# pair with no policy entry to an explicit per-agent "deny" before
# ValidateToolPolicyCoverage was enforced. config.ValidateAgentOwnToolPolicyCoverage
# used to report — at boot ERROR — every agent riding the global ceiling for a
# tool it never mentioned. Both were fail-closed "safety" mechanisms that, in
# practice, reintroduced a code-branch default: the reconciled global ceiling
# (config.ReconcileToolPolicyCeiling, ADR-076) is now always complete for the
# static catalog, so the backfill's premise (a genuine both-sides gap) is
# gone, and it could only misfire — silently denying a newly-shipped "allow"
# tool. The operator ratified the two-layer model explicitly: "there must be
# no safety, there must be always the global setting which is the default.
# remove the safety." See ADR-077 in full.
#
# WHAT IS ALLOWED
#
# - Comments (including this file's own text, and retirement/historical notes
#   in pkg/config/validate.go, pkg/gateway/gateway.go, and elsewhere) that
#   name the retired symbols in prose. This script matches only a live
#   definition or call — `<Symbol>(` — and drops any line whose trimmed
#   content is a `//` comment.
# - pkg/gateway/tool_policy_boot_validation_test.go, which legitimately
#   discusses the retired symbols by name in its own header comment
#   documenting the rename (Guard 2's own guarded test file).
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-fail-closed-backfill: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg cmd; do
  if [ ! -d "$d" ]; then
    echo "check-no-fail-closed-backfill: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

# A re-added DEFINITION (func RepairIncompleteToolPolicyCoverage(...) or
# func ValidateAgentOwnToolPolicyCoverage(...)) and a re-added CALL
# (RepairIncompleteToolPolicyCoverage(...) or
# ValidateAgentOwnToolPolicyCoverage(...)) both match "<Symbol>(" — a function
# definition's identifier is immediately followed by "(" exactly like a call
# site is, so one pattern catches both shapes.
PATTERN='RepairIncompleteToolPolicyCoverage\(|ValidateAgentOwnToolPolicyCoverage\('

# The sanctioned exception: this file legitimately names both retired symbols
# in its own header comment documenting the ADR-077 rename/retirement.
EXCLUDE_FILE="pkg/gateway/tool_policy_boot_validation_test.go"

HITS="$(grep -rnE "$PATTERN" \
  --include='*.go' \
  pkg cmd 2>/dev/null \
  | grep -v ":${EXCLUDE_FILE}:" \
  | grep -vE "^${EXCLUDE_FILE}:" \
  || true)"

# Drop comment-only lines: a retirement comment naming the symbol in prose is
# the desired outcome, not a violation.
OFFENDERS="$(printf '%s\n' "$HITS" \
  | grep -v '^\s*$' \
  | awk -F: '{ line=""; for (i=3; i<=NF; i++) line = line (i>3 ? ":" : "") $i;
               sub(/^[ \t]+/, "", line);
               if (line ~ /^\/\//) next;
               print }' \
  || true)"

if [ -n "$OFFENDERS" ]; then
  echo "check-no-fail-closed-backfill: FOUND a retired fail-closed tool-policy backfill mechanism in code:" >&2
  echo "" >&2
  printf '%s\n' "$OFFENDERS" >&2
  echo "" >&2
  echo "config.RepairIncompleteToolPolicyCoverage and config.ValidateAgentOwnToolPolicyCoverage" >&2
  echo "were deleted deliberately (ADR-077). Tool policy is exactly two layers: the" >&2
  echo "reconciled global ceiling (config.ReconcileToolPolicyCeiling) IS the default;" >&2
  echo "sparse per-agent overrides only tighten. There is no fail-closed backfill" >&2
  echo "between them." >&2
  echo "" >&2
  echo "If you hit this after a merge or rebase from an older branch: that branch" >&2
  echo "predates the removal and git re-added the code as an ordinary addition." >&2
  echo "RESOLVE BY KEEPING THE DELETION — do not 'fix the conflict' by restoring it." >&2
  echo "" >&2
  echo "If you genuinely need to reintroduce a fail-closed per-agent backfill, that" >&2
  echo "reverses an accepted, operator-ratified ADR: write the superseding ADR first," >&2
  echo "then update this guard in the same commit." >&2
  exit 1
fi

echo "check-no-fail-closed-backfill: OK (no fail-closed per-agent tool-policy backfill present; two-layer model per ADR-077)"
exit 0
