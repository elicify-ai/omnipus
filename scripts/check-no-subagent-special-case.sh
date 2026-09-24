#!/usr/bin/env bash
# check-no-subagent-special-case.sh
#
# Regression guard for ADR-091: after the five building packages (WP-A through WP-E)
# are done, this package proves nothing of the old subagent mechanism is left in
# production code. A merge from a branch cut before ADR-091 can resurrect deleted
# surfaces as ordinary, conflict-free additions — git has no idea the deletion was
# deliberate. This script blocks reintroduction of the forbidden symbols.
#
# WHAT WAS REMOVED AND WHY IT MUST NOT RETURN
#
# ADR-091 replaces the subagent mechanism (a session steered by another session
# with special internal plumbing) with steered sessions (explicit hand-offs through
# normal message/tool channels). The following symbols were deleted and must never
# reappear in production code:
#
# 1. The ring (ephemeral session storage):
#    - newEphemeralSession
#    - maxEphemeralHistorySize
#    - ephemeralSessionStore
#
# 2. Wait-inline (blocking delegation):
#    - executeSync
#    - DelegationModeAwait
#    - allow_blocking_question, with ONE deliberate, founder-approved exception:
#      pkg/tools/delegate_run.go's validateRequest must name the literal in
#      order to refuse it ("invalid_argument: allow_blocking_question"). A
#      tool cannot refuse an argument it is not allowed to name, so this
#      script path-scopes the ban to exclude that single rejection site —
#      see ALLOW_BLOCKING_QUESTION_HITS below for the exact mechanism, which
#      mirrors the ProducingSessionID path-scoping already used in this file.
#
# 3. Borrowed parent addresses (internal channel sharing):
#    - parentTS.channel
#    - parentTS.chatID
#
# 4. The old parent linkage on the lifecycle record:
#    - ParentDurableKey (replaced by the steered-by edge, SteeredBy)
#
# 5. The relabelled-frame workaround:
#    - ProducingSessionID, banned in pkg/agent/events.go and
#      pkg/gateway/websocket_forward.go (the retired payloads and their readers;
#      every frame now carries its own session_id)
#
# 6. The sibling notifier:
#    - notifyParentIfAllSiblingsDone (every child now wakes its parent itself)
#
# 7. Nested replay of a child's steps inside the parent:
#    - emitNestedToolCalls
#
# WHAT IS ALLOWED
#
# - Comments and historical notes naming these symbols in prose. This script matches
#   only a live definition or call. A line whose trimmed content starts with "//"
#   is treated as a comment and ignored.
# - Media-presence checks (persistToolResult, recordToolCompletion, deliverToolOutput)
#   used legitimately for persistence and replay decisions. These check for
#   IsMediaToolResult or similar, not the deleted mechanism.
# - Test files (production code only; tests may have negative fixtures).
# - Job-category literals like "subagent" (the channel usage is deleted; the
#   job-category name survives in documentation and config examples).
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-subagent-special-case: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg cmd src; do
  if [ ! -d "$d" ]; then
    echo "check-no-subagent-special-case: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

# Patterns for deleted symbols (regex alternation).
#
# allow_blocking_question is deliberately NOT in this list — see the
# ALLOW_BLOCKING_QUESTION_HITS section below, which bans it everywhere
# except the one file allowed to name it.
BANNED_PATTERNS=(
  'newEphemeralSession'
  'maxEphemeralHistorySize'
  'ephemeralSessionStore'
  'executeSync'
  'DelegationModeAwait'
  'parentTS\.channel'
  'parentTS\.chatID'
  'ParentDurableKey'
  'notifyParentIfAllSiblingsDone'
  'emitNestedToolCalls'
)

# Join patterns with |
PATTERN="$(IFS='|'; echo "${BANNED_PATTERNS[*]}")"

# Search in production code (exclude test files)
HITS="$(grep -rnE "$PATTERN" \
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yaml' \
  --exclude='*_test.go' --exclude='*.test.ts' --exclude='*.test.tsx' \
  pkg cmd src 2>/dev/null \
  || true)"

# ProducingSessionID is banned only in the two files that carried the retired
# payload and its readers (ADR-091 WP-F FR-F-003); elsewhere the name is not ours.
PRODUCING_SESSION_HITS=""
for f in pkg/agent/events.go pkg/gateway/websocket_forward.go; do
  if [ -f "$f" ]; then
    hit="$(grep -nE 'ProducingSessionID' "$f" | sed "s|^|$f:|" || true)"
    [ -n "$hit" ] && PRODUCING_SESSION_HITS="$(printf '%s\n%s' "$PRODUCING_SESSION_HITS" "$hit")"
  fi
done

# allow_blocking_question: founder-approved narrowing (ADR-091 fix round, RX-CI
# lane), mirroring the ProducingSessionID path-scoping immediately above but
# inverted. ProducingSessionID is banned ONLY inside a fixed, named set of
# files; allow_blocking_question is banned EVERYWHERE except a fixed, named
# rejection site: pkg/tools/delegate_run.go's validateRequest, which returns
# "invalid_argument: allow_blocking_question" for a caller that still sends
# the deleted argument. A tool has to NAME an argument in order to REFUSE it,
# so a strict zero-occurrence rule for this one symbol is unsatisfiable by
# construction — the rejection site would always trip it. That is also why a
# string-concatenation trick ("allow_" + "blocking_question") to hide the
# literal from this exact guard is explicitly rejected: hiding code from our
# own guard is worse than a guard with one pinned, documented exception.
# tests/adr091/residual_audit_test.go independently pins this to exactly one
# occurrence, in exactly this file — see its "allow_blocking_question refused
# by name, not accepted anywhere" case.
ALLOW_BLOCKING_QUESTION_HITS="$(grep -rnE 'allow_blocking_question' \
  --include='*.go' --include='*.ts' --include='*.tsx' --include='*.yaml' \
  --exclude='*_test.go' --exclude='*.test.ts' --exclude='*.test.tsx' \
  pkg cmd src 2>/dev/null \
  | grep -v -E '^pkg/tools/delegate_run\.go:' \
  || true)"

# Combine all hits
ALL_HITS="$HITS"
if [ -n "$PRODUCING_SESSION_HITS" ]; then
  ALL_HITS="$(printf '%s\n%s\n' "$ALL_HITS" "$PRODUCING_SESSION_HITS")"
fi
if [ -n "$ALLOW_BLOCKING_QUESTION_HITS" ]; then
  ALL_HITS="$(printf '%s\n%s\n' "$ALL_HITS" "$ALLOW_BLOCKING_QUESTION_HITS")"
fi

# Drop comment-only lines: a retirement comment naming the symbol in prose is
# the desired outcome, not a violation.
OFFENDERS="$(printf '%s\n' "$ALL_HITS" \
  | grep -v '^\s*$' \
  | awk -F: '{ line=""; for (i=3; i<=NF; i++) line = line (i>3 ? ":" : "") $i;
               sub(/^[ \t]+/, "", line);
               if (line ~ /^\/\//) next;
               print }' \
  || true)"

if [ -n "$OFFENDERS" ]; then
  echo "check-no-subagent-special-case: FOUND deleted ADR-091 symbols in production code:" >&2
  echo "" >&2
  printf '%s\n' "$OFFENDERS" >&2
  echo "" >&2
  echo "ADR-091 deletes the subagent mechanism (steered sessions replace it)." >&2
  echo "The above symbols were removed and must not reappear in production code." >&2
  echo "" >&2
  echo "If you hit this after a merge or rebase from an older branch: that branch" >&2
  echo "was cut before ADR-091 and still contains the old code. The deletion was" >&2
  echo "deliberate. Keep the deletion — do not merge these symbols back in." >&2
  echo "" >&2
  echo "See docs/internal/architecture/ADR-091-steered-sessions-replace-subagents.md" >&2
  echo "" >&2
  exit 1
fi

exit 0
