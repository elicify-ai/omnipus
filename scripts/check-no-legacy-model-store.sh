#!/usr/bin/env bash
# check-no-legacy-model-store.sh
#
# Regression guard for session-core U2 / DEL-12 (spec C-ARCHIVE Decision D): the
# runtime has ONE model-content store, the addressed archive, and rebuilds
# nothing from the UI transcript. The retired surfaces below must not return as
# live definitions or calls (DEL-10/DEL-12: there is ONE shared session store and
# no JSONL/SessionManager fallback, no per-agent store accessor):
#
#   - transcript -> model-history hydration (hydrateAgentHistory,
#     HydrateAgentHistoryFromTranscript, hydrateOneAgent)
#   - SessionStore.SetHistory / TruncateHistory / MarkHydrated
#   - the dense lifetime snapshot field `.Archive` read by agent code is covered
#     by the type system (WindowView has no such field), not by this script.
#
# A merge from a branch cut before the removal can resurrect these as ordinary
# conflict-free additions; resolve by keeping the deletion.
#
# Matches only a live definition or call (`<Symbol>(`); lines whose trimmed
# content is a `//` comment are ignored, and so are calls of memory.Store's
# context-taking TruncateHistory (the legacy JSONL store, pkg/memory's concern). Test files are scanned too: a fixture
# seeding through a retired method would re-introduce the call site.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-legacy-model-store: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg cmd; do
  [ -d "$d" ] || { echo "check-no-legacy-model-store: expected directory '$d' not found" >&2; exit 2; }
done

# Instrument self-check: the scan must be able to see a symbol that is present.
if ! grep -rqE --include='*.go' 'func \(us \*UnifiedStore\) ReadArchive\(' pkg/session; then
  echo "check-no-legacy-model-store: self-check failed - a known symbol was not found; refusing to report green" >&2
  exit 2
fi

PATTERN='(hydrateAgentHistory|HydrateAgentHistoryFromTranscript|hydrateOneAgent|\.MarkHydrated|\.SetHistory|\.TruncateHistory|GetAgentStore|getLegacyAgentStore|taskSessionStore|initSessionStore|NewJSONLBackend|NewJSONLStore|session\.NewSessionManager)\('
offenders=$(grep -rnE --include='*.go' "$PATTERN" pkg cmd 2>/dev/null \
  | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' \
  | grep -v 'TruncateHistory(context\.' )
if [ -n "$offenders" ]; then
  echo "check-no-legacy-model-store: retired model-store surface found (session-core U2 DEL-12):" >&2
  echo "$offenders" >&2
  exit 1
fi
exit 0
