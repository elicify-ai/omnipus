#!/usr/bin/env bash
# check-no-subagent-special-case.test.sh — proof-of-failure companion for
# check-no-subagent-special-case.sh (ADR-091 WP-F FR-F-003). Each case builds a
# minimal throwaway tree, injects one line, runs the guard against it and checks
# the exit code (and, for a banned name, that the guard names it).
#
# Exit: 0 all cases pass, 1 a case failed, 2 the test itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD="$SCRIPT_DIR/check-no-subagent-special-case.sh"
[ -f "$GUARD" ] || { echo "guard not found: $GUARD" >&2; exit 2; }

WORK="$(mktemp -d)" || exit 2
trap 'rm -rf "$WORK"' EXIT

pass=0
fail=0

# case <name> <expected-exit> <relative-file> <line> [<name-that-must-appear>]
case_run() {
  local name="$1" want="$2" rel="$3" line="$4" must="${5:-}"
  local tree="$WORK/$name"
  mkdir -p "$tree/pkg" "$tree/cmd" "$tree/src" "$(dirname "$tree/$rel")"
  printf '%s\n' "$line" > "$tree/$rel"
  local out got
  out="$(REPO_ROOT="$tree" bash "$GUARD" 2>&1)"
  got=$?
  if [ "$got" -ne "$want" ]; then
    echo "FAIL [$name]: exit $got, want $want"; fail=$((fail + 1)); return
  fi
  if [ -n "$must" ] && ! printf '%s' "$out" | grep -q -- "$must"; then
    echo "FAIL [$name]: output does not name '$must'"; fail=$((fail + 1)); return
  fi
  echo "PASS [$name]: exit $got"; pass=$((pass + 1))
}

# The real tree is clean.
out="$(bash "$GUARD" 2>&1)"; got=$?
if [ "$got" -eq 0 ]; then echo "PASS [real-tree-clean]: exit 0"; pass=$((pass + 1)); else echo "FAIL [real-tree-clean]: exit $got"; echo "$out"; fail=$((fail + 1)); fi

# Every banned name fails the guard and is named in its output.
case_run ring-new        1 pkg/agent/x.go 'func newEphemeralSession() {}'              newEphemeralSession
case_run ring-max        1 pkg/agent/x.go 'const maxEphemeralHistorySize = 50'        maxEphemeralHistorySize
case_run ring-store      1 pkg/agent/x.go 'type ephemeralSessionStore struct{}'       ephemeralSessionStore
case_run wait-sync       1 pkg/tools/x.go 'func (d *DelegateTool) executeSync() {}'   executeSync
case_run wait-mode       1 pkg/config/x.go 'const DelegationModeAwait = "await"'      DelegationModeAwait
case_run wait-arg-ts     1 src/lib/x.ts   'export const allow_blocking_question = 1;' allow_blocking_question
case_run addr-channel    1 pkg/agent/x.go 'ch := parentTS.channel'                    'parentTS.channel'
case_run addr-chat       1 pkg/agent/x.go 'id := parentTS.chatID'                     'parentTS.chatID'
case_run parent-key      1 pkg/session/x.go 'ParentDurableKey string'                 ParentDurableKey
case_run producing-ev    1 pkg/agent/events.go 'ProducingSessionID session.SessionID' ProducingSessionID
case_run producing-fwd   1 pkg/gateway/websocket_forward.go 'id := p.ProducingSessionID' ProducingSessionID
case_run sibling         1 pkg/agent/x.go 'func notifyParentIfAllSiblingsDone() {}'   notifyParentIfAllSiblingsDone
case_run nested-replay   1 pkg/gateway/x.go 'func emitNestedToolCalls() {}'           emitNestedToolCalls

# What the guard must NOT ban.
case_run comment-ok          0 pkg/agent/x.go '// the old executeSync path is gone'
case_run test-file-ok        0 pkg/agent/x_test.go 'func executeSync() {}'
case_run producing-elsewhere 0 pkg/other/x.go 'ProducingSessionID string'
case_run media-predicate-ok  0 pkg/agent/x.go 'if IsMediaToolResult(r) { persistToolResult(r) }'

echo "Results: $pass passed, $fail failed"
[ "$fail" -eq 0 ] || exit 1
exit 0
