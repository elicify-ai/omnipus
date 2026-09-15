#!/usr/bin/env bash
# check-operator-prompt-sites.sh — ADR-085 BROWSER-FR-029 / FR-029a mechanical
# guard (wave B9, docs/internal/specs/adr-084-086-joint-delivery-plan.md §3,
# row B9; C-77; R-19b).
#
# FR-029/FR-029a's correctness is a property of an ASSIGNMENT SET, not of a
# helper being called: the browser-wheel release decision lives entirely in
# where `bus.InboundMessage.OperatorPrompt` is set `true`, and where
# `bus.MessageBus.PublishInbound` is called at all. A Go unit test already
# pins both of these (pkg/gateway/browser_release_sites_test.go's
# TestOperatorPromptField_AssignmentPartitionIsPinned, B123's own territory)
# — this script is the SAME two counts, verified independently and mechanically,
# so a bad merge that silently drops or weakens that Go test still fails the
# build (the repository's own rule: "a note in CLAUDE.md tells a human — it
# does not stop `git merge`", and neither does a Go test that a merge can
# delete along with the code it was guarding).
#
# Two counts, both from the joint delivery plan's own text:
#
#   1. `bus.InboundMessage.OperatorPrompt` is assigned the literal `true` at
#      EXACTLY THREE non-test sites in pkg/: pkg/gateway/websocket.go,
#      pkg/gateway/sse.go, pkg/channels/base.go::HandleMessage. Never at
#      pkg/gateway/ws_ask_user.go, pkg/agent/async_notifier.go, or
#      pkg/agent/loop.go (the goal-loop follow-up re-injection) — a fourth
#      site or a missing site are both failures (C-77's exact wording: "fails
#      if OperatorPrompt is assigned true at any number of non-test sites
#      other than exactly three").
#   2. `bus.MessageBus.PublishInbound` is CALLED (never its own `func`
#      definition in pkg/bus/bus.go) at EXACTLY SIX non-test sites in pkg/
#      (BROWSER-FR-029a's own text: "the PublishInbound census is exactly six
#      non-test call sites, so a new publish site cannot appear
#      un-classified"). Verified today: pkg/agent/async_notifier.go,
#      pkg/agent/loop.go (the goal-loop follow-up — this ONE call site is
#      legitimate and must stay; the point is that no SEVENTH appears
#      unclassified), pkg/gateway/sse.go, pkg/gateway/websocket.go,
#      pkg/gateway/ws_ask_user.go, pkg/channels/base.go.
#
# Per C-77: do NOT scan pkg/gateway/websocket.go's ADR-057 FR-089 "W5 audit
# classification artefact" comment block for OperatorPrompt assignments. That
# whole block is `//`-prefixed prose (verified: every line from its anchor
# comment to the next non-comment declaration is a `//` line), so the
# standard comment strip below already excludes it — this script ALSO
# excludes it by explicit line range as a second, independent safeguard, in
# case a future revision of that block ever mixes in an unprefixed line.
#
# Scope: pkg/ only (C-77's own words), hand-written *.go, excluding *_test.go,
# excluding this script's own sibling files (n/a — .sh, not .go), excluding
# generated/ and node_modules/. Comment-only mentions (a `//` line naming
# OperatorPrompt or PublishInbound in prose) are never counted — matches the
# established convention in check-no-orphan-turn-watchdog.sh.
#
# Exit: 0 clean (both counts exact), 1 a count is wrong (offender sites
# printed), 2 the check itself could not run (wrong cwd, missing directory,
# a grep failure).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-operator-prompt-sites: cannot cd to $REPO_ROOT" >&2; exit 2; }

if [ ! -d pkg ]; then
  echo "check-operator-prompt-sites: expected directory 'pkg' not found under $REPO_ROOT" >&2
  echo "  (wrong cwd or a partial checkout — refusing to report a green verdict" >&2
  echo "   for a tree this script never actually scanned)" >&2
  exit 2
fi

STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-operator-prompt-sites-stderr.XXXXXX")"
trap 'rm -f "$STDERR_FILE"' EXIT

run_grep() {
  # $1 = pattern, remaining = grep args (dirs/files). Captures stdout,
  # treats grep exit >1 as a hard failure (never swallowed into "no matches").
  local pattern="$1"; shift
  local out status
  out=$(grep -rnE "$pattern" --include='*.go' "$@" 2>"$STDERR_FILE")
  status=$?
  if [ "$status" -gt 1 ]; then
    echo "check-operator-prompt-sites: grep failed (exit $status) scanning: $*" >&2
    cat "$STDERR_FILE" >&2
    exit 2
  fi
  printf '%s' "$out"
}

# Drop full-line `//`, `#`, `*`, `/*` comments (retirement/explanatory prose
# naming these symbols is expected and not a violation), drop test files,
# drop generated/vendor noise.
strip_comments_and_noise() {
  awk -F: '
    NF >= 3 {
      file = $1
      line = ""
      for (i = 3; i <= NF; i++) line = line (i > 3 ? ":" : "") $i
      sub(/^[[:space:]]+/, "", line)
      if (line ~ /^\/\//) next
      if (line ~ /^\*/) next
      if (line ~ /^\/\*/) next
      if (line ~ /^#/) next
      print $0
    }
  ' | grep -v '_test\.go:' \
    | grep -v '/generated/' \
    | grep -v '_generated' \
    | grep -v 'node_modules/'
}

# --- Defensive second exclusion: the ADR-057 FR-089 artefact block --------
# Compute [anchor, first-non-comment-line) in pkg/gateway/websocket.go, if
# that file and anchor exist, and drop any surviving hit whose line number
# falls in that range. A no-op today (the block is 100% comment lines and
# the strip above already removes it) — kept as an explicit, named safeguard
# per C-77's instruction not to scan that block.
artefact_block_range() {
  local f="pkg/gateway/websocket.go"
  [ -f "$f" ] || return 0
  awk '
    /ADR-057 FR-089 — W5 audit classification artefact/ { start = NR; next }
    start && !/^[[:space:]]*\/\// { print start": "NR; exit }
  ' "$f"
}

exclude_artefact_block() {
  local range start end
  range="$(artefact_block_range)"
  if [ -z "$range" ]; then
    cat
    return
  fi
  start="${range%%:*}"
  end="${range##*:}"
  awk -F: -v start="$start" -v end="$end" '
    {
      if ($1 == "pkg/gateway/websocket.go" && $2+0 >= start && $2+0 < end) next
      print
    }
  '
}

# --- Count 1: OperatorPrompt assigned literal true -------------------------

OP_PATTERN='OperatorPrompt[[:space:]]*[:=][[:space:]]*true'
op_hits="$(run_grep "$OP_PATTERN" pkg | strip_comments_and_noise | exclude_artefact_block)"
op_count=0
if [ -n "$op_hits" ]; then
  op_count=$(printf '%s\n' "$op_hits" | grep -c .)
fi

# --- Count 2: PublishInbound CALLED (never the func definition) -----------

PI_PATTERN='\.PublishInbound\('
pi_hits="$(run_grep "$PI_PATTERN" pkg | strip_comments_and_noise | exclude_artefact_block)"
pi_count=0
if [ -n "$pi_hits" ]; then
  pi_count=$(printf '%s\n' "$pi_hits" | grep -c .)
fi

fail=0

echo "=== check-operator-prompt-sites ==="
echo ""
echo "OperatorPrompt assigned true: $op_count site(s) (want exactly 3)"
if [ -n "$op_hits" ]; then
  printf '%s\n' "$op_hits" | sed 's/^/  /'
fi
if [ "$op_count" -ne 3 ]; then
  fail=1
fi

echo ""
echo "PublishInbound call sites: $pi_count (want exactly 6)"
if [ -n "$pi_hits" ]; then
  printf '%s\n' "$pi_hits" | sed 's/^/  /'
fi
if [ "$pi_count" -ne 6 ]; then
  fail=1
fi

echo ""
if [ "$fail" -ne 0 ]; then
  echo "ERROR: BROWSER-FR-029/FR-029a's assignment partition is no longer exactly" >&2
  echo "3 OperatorPrompt=true sites and 6 PublishInbound call sites (see counts" >&2
  echo "above). This is the fail-closed browser-wheel release discriminator —" >&2
  echo "a new, un-classified site here silently changes when the wheel releases." >&2
  echo "See docs/internal/specs/browser-control-handover-spec.md FR-029/FR-029a" >&2
  echo "and pkg/gateway/browser_release_sites_test.go." >&2
  exit 1
fi

echo "OK: exactly 3 OperatorPrompt=true sites and 6 PublishInbound call sites."
exit 0
