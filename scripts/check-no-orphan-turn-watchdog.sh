#!/usr/bin/env bash
# check-no-orphan-turn-watchdog.sh — ADR-082 D1/D7 mechanical guard.
#
# The ADR-045 orphaned-foreground-turn watchdog is deleted in full
# (greenfield, operator directive 2026-09-08): a turn never depends on a UI
# connection (P1), so the mechanism that ended turns based on "no UI/user
# connection" no longer exists at all — not disabled, not defaulted to 0,
# REMOVED, per docs/internal/architecture/ADR-082-ui-independent-turns-and-
# session-bound-streaming.md §5. The ADR-057 cancel state machine
# (RequestCancel, InterruptSessionHard) is untouched and remains the only
# way a turn ends early, and only on an explicit Stop/cancel surface.
#
# A merge from a pre-ADR-082 branch can resurrect the deleted symbols as
# ordinary, conflict-free additions — this script fails the build when any
# of them reappears as a definition or non-comment reference. Mirrors
# scripts/check-no-goal-confirm-gate.sh (ADR-081) and
# scripts/check-no-fail-closed-backfill.sh (ADR-077).
#
# Guarded names (ADR-082 §5's guard list):
#   ArmOrphanForegroundTurnWatch, DisarmOrphanForegroundTurnWatch,
#   fireOrphanForegroundTurnWatch, reapOrphanForegroundTurn,
#   sessionStillOrphaned, hasLiveCriticalDelegate,
#   getActiveRootTurnStateForSession, OrphanedTurnGraceSeconds,
#   DefaultOrphanedTurnGraceSeconds, EffectiveOrphanedTurnGraceSeconds,
#   GatewayOrphanedTurnGraceSeconds, OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS,
#   EventTurnOrphanTimeout, turn.orphan_timeout.
#
# Explicitly NOT guarded (kept — a DIFFERENT mechanism that also shares the
# word "orphan"): the subagent-span forwarder watchdog
# (startOrphanWatchdog / orphanWatchdogTimeout / orphanWatchdogMaxRechecks,
# pkg/gateway/websocket.go — synthesizes a closing span frame when a parent
# turn ends before all its subagent spans close) and SubTurnOrphan (an
# EventKind naming a different condition entirely). None of those four names
# is a substring of any guarded name above (verified: "OrphanWatchdog" vs.
# "sessionStillOrphaned"/"hasLiveCriticalDelegate"/etc. share no common
# substring the regex below could accidentally catch) — this script must
# never flag them.
#
# Scope: hand-written Go, TypeScript, YAML and shell sources under pkg/,
# cmd/, scripts/, .github/, deploy/, tests/, src/, and Makefile — every
# location the ADR-082 deletion inventory names a surviving reference to
# clean up. Excluded within scope: this script itself, docs/, generated
# artifacts, node_modules, .git, and comment-only mentions (retirement
# comments are the sanctioned way to reference these names in prose).
#
# One further sanctioned exception, mirroring check-no-fail-closed-
# backfill.sh's EXCLUDE_FILE precedent: pkg/config/orphan_grace_key_ignored_
# test.go (T-14) legitimately asserts, via reflect.StructField.Name ==
# "OrphanedTurnGraceSeconds" and a json-tag comparison against
# "orphaned_turn_grace_seconds", that GatewayConfig carries NEITHER any more
# — a string-literal ABSENCE check, not a definition or reference. That
# string literal is indistinguishable from a live reference to this script's
# line-based regex, so the file is excluded by name rather than by
# introducing string-literal-aware parsing that would blind the guard to a
# genuine reintroduction disguised as a string.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-orphan-turn-watchdog: cannot cd to $REPO_ROOT" >&2; exit 2; }

SCAN_DIRS=(pkg cmd scripts .github deploy tests src)
for d in "${SCAN_DIRS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "check-no-orphan-turn-watchdog: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done
if [ ! -f Makefile ]; then
  echo "check-no-orphan-turn-watchdog: expected file 'Makefile' not found under $REPO_ROOT" >&2
  exit 2
fi

SYMBOLS='ArmOrphanForegroundTurnWatch|DisarmOrphanForegroundTurnWatch|fireOrphanForegroundTurnWatch|reapOrphanForegroundTurn|sessionStillOrphaned|hasLiveCriticalDelegate|getActiveRootTurnStateForSession|OrphanedTurnGraceSeconds|OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS|EventTurnOrphanTimeout|turn\.orphan_timeout'
# Note: OrphanedTurnGraceSeconds alone already matches its three
# mixed-case-prefixed Go variants (Default.../Effective.../
# Gateway...OrphanedTurnGraceSeconds) as substrings — deliberately, one
# alternative catches all three Go names without three separate (and
# therefore easier to typo/drift) alternatives. It does NOT, however, match
# the ALL-CAPS env var OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS (grep is
# case-sensitive and "ORPHANED" != "Orphaned"), so that env var name is its
# own separate, explicit alternative.

# Two passes: extension-filtered recursion over the scan dirs (--include only
# applies to files grep finds by recursing into a directory — it does NOT
# filter an explicitly-named file argument like Makefile below, which has no
# extension at all), plus a plain, unfiltered scan of Makefile itself.
dir_hits=$(grep -rnE "$SYMBOLS" \
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yml' \
  --include='*.yaml' --include='*.sh' \
  "${SCAN_DIRS[@]}" 2>/dev/null || true)
makefile_hits=$(grep -nE "$SYMBOLS" Makefile 2>/dev/null | sed 's#^#Makefile:#' || true)

hits=$(printf '%s\n%s\n' "$dir_hits" "$makefile_hits" \
  | grep -v '/generated/' \
  | grep -v '_generated' \
  | grep -v 'node_modules/' \
  | grep -v '^scripts/check-no-orphan-turn-watchdog\.sh:' \
  | grep -v '^scripts/check-no-orphan-turn-watchdog\.test\.sh:' \
  | grep -v '^pkg/config/orphan_grace_key_ignored_test\.go:' \
  || true)

# Drop comment-only lines: Go/TS/shell `//`/`#` line comments and block-
# comment continuation lines starting with `*`. A retirement comment naming
# a symbol in prose is the desired outcome, not a violation.
violations=$(echo "$hits" | awk -F: '
  NF >= 3 {
    line = ""
    for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
    sub(/^[[:space:]]+/, "", line)
    if (line ~ /^\/\//) next
    if (line ~ /^\*/) next
    if (line ~ /^\/\*/) next
    if (line ~ /^#/) next
    print $0
  }' | grep -v '^$' || true)

if [ -n "$violations" ]; then
  echo "ERROR: retired ADR-045 orphan-foreground-turn watchdog symbol(s) present in hand-written source:" >&2
  echo "" >&2
  echo "$violations" >&2
  echo "" >&2
  echo "These were deleted in full by ADR-082 D1 (superseding ADR-045) — not disabled, REMOVED." >&2
  echo "If this came from a merge, resolve by KEEPING THE DELETION — see" >&2
  echo "CLAUDE.md 'Retired surfaces' and docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md." >&2
  exit 1
fi

echo "OK: no retired ADR-045 orphan-foreground-turn watchdog symbols found."
