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
#    - allow_blocking_question
#
# 3. Borrowed parent addresses (internal channel sharing):
#    - parentTS.channel
#    - parentTS.chatID
#
# 4. Parent durable identities (deleted by WP-A):
#    - ParentDurableKey
#
# 5. Child provenance (deleted by WP-A):
#    - ProducingSessionID (scoped to pkg/agent/events.go and pkg/gateway/websocket_forward.go only)
#
# 6. Sibling notifier (deleted by WP-C):
#    - notifyParentIfAllSiblingsDone
#
# 7. Nested replay (deleted by WP-A):
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

# Patterns for deleted symbols (regex alternation)
BANNED_PATTERNS=(
  'newEphemeralSession'
  'maxEphemeralHistorySize'
  'ephemeralSessionStore'
  'executeSync'
  'DelegationModeAwait'
  'allow_blocking_question'
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

# Special handling for ProducingSessionID: only flag if found outside the two
# scoped files where it legitimately appears in comments about deletion
PRODUCING_SESSION_HITS="$(grep -rnE 'ProducingSessionID' \
  --include='*.go' \
  --exclude='*_test.go' \
  pkg cmd 2>/dev/null \
  | grep -v 'pkg/agent/events.go:' \
  | grep -v 'pkg/gateway/websocket_forward.go:' \
  || true)"

# Combine all hits
ALL_HITS="$HITS"
if [ -n "$PRODUCING_SESSION_HITS" ]; then
  ALL_HITS="$(printf '%s\n%s\n' "$ALL_HITS" "$PRODUCING_SESSION_HITS")"
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
