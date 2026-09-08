#!/usr/bin/env bash
# check-no-orphan-turn-watchdog.test.sh
#
# Self-test for check-no-orphan-turn-watchdog.sh (ADR-082 D1/D7 regression
# guard). A guard that cannot fail is no guard (docs/internal/
# false-green-patterns.md) — this builds a scratch fixture tree under a temp
# directory, points the lint script at it via REPO_ROOT (same pattern as
# check-no-tool-error-from-status.test.sh and
# check-no-handwritten-wire-types.test.sh), and NEVER touches the real repo
# tree.
#
# Covers:
#
#   1. A planted definition of a guarded symbol (ArmOrphanForegroundTurnWatch)
#      in a non-comment Go line — CAUGHT.
#   2. A planted call site of a different guarded symbol
#      (hasLiveCriticalDelegate) — CAUGHT.
#   3. The env var name (OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS) in a
#      non-comment shell line (mirrors the real pr.yml/runci.sh shape) —
#      CAUGHT.
#   4. The audit event string ("turn.orphan_timeout") in a non-comment Go
#      line — CAUGHT.
#   5. The SAME symbol names, but only inside `//` (Go/TS) and `#` (shell)
#      comments — NOT caught (retirement comments are the sanctioned way to
#      reference these names in prose).
#   6. The four explicitly KEPT, different names that share the word
#      "orphan" (startOrphanWatchdog, orphanWatchdogTimeout,
#      orphanWatchdogMaxRechecks, SubTurnOrphan) in non-comment code — NOT
#      caught (they are a different, still-live mechanism per ADR-082 §5).
#   7. A clean tree with none of the guarded symbols anywhere — exits 0.
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LINT_SCRIPT="${SCRIPT_DIR}/check-no-orphan-turn-watchdog.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/orphan-turn-watchdog-lint-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

# Every fixture tree needs the directories the lint script requires to exist
# (its own existence check, mirroring check-no-fail-closed-backfill.sh's
# sibling pattern) plus a Makefile, or it would correctly refuse to run
# (exit 2) rather than silently scanning nothing.
setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{pkg,cmd,scripts,.github,deploy,tests,src}
  printf 'all:\n\t@true\n' > "$TMP_DIR/Makefile"
}

setup_fixture() {
  local subpath="$1"
  local content="$2"
  local fpath="${TMP_DIR}/${subpath}"
  mkdir -p "$(dirname "$fpath")"
  printf '%s\n' "$content" > "$fpath"
}

assert_exit_code() {
  local label="$1" expected="$2" actual="$3"
  if [[ "$actual" -eq "$expected" ]]; then
    echo "  PASS [$label]: exit $actual (expected $expected)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: exit $actual (expected $expected)"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected exit $expected, got $actual")
  fi
}

assert_output_contains() {
  local label="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  PASS [$label]: output contains '$needle'"
    PASS=$((PASS + 1))
  else
    echo "  FAIL [$label]: output does NOT contain '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to contain '$needle'")
  fi
}

assert_output_not_contains() {
  local label="$1" needle="$2" haystack="$3"
  if echo "$haystack" | grep -qF "$needle"; then
    echo "  FAIL [$label]: output unexpectedly contains '$needle'"
    FAIL=$((FAIL + 1))
    ERRORS+=("[$label] expected output to NOT contain '$needle'")
  else
    echo "  PASS [$label]: output does not contain '$needle'"
    PASS=$((PASS + 1))
  fi
}

echo "=== check-no-orphan-turn-watchdog self-test (ADR-082 D1/D7) ==="
echo ""

# --- Test 1: planted definition of ArmOrphanForegroundTurnWatch — CAUGHT ---

echo "Test 1: a planted ArmOrphanForegroundTurnWatch definition is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture1.go" '
package agent

func (al *AgentLoop) ArmOrphanForegroundTurnWatch(sessionID string, grace int) {
	_ = sessionID
	_ = grace
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "planted-arm-exit" 1 "$EXIT_CODE"
assert_output_contains "planted-arm-finding" "fixture1.go" "$OUTPUT"

# --- Test 2: planted call site of hasLiveCriticalDelegate — CAUGHT ---

echo ""
echo "Test 2: a planted hasLiveCriticalDelegate call is caught"
setup_skeleton
setup_fixture "pkg/agent/fixture2.go" '
package agent

func check(al *AgentLoop, sid string) bool {
	return al.hasLiveCriticalDelegate(sid)
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "planted-call-exit" 1 "$EXIT_CODE"
assert_output_contains "planted-call-finding" "fixture2.go" "$OUTPUT"

# --- Test 3: env var name in a non-comment shell line — CAUGHT ---

echo ""
echo "Test 3: OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS in a shell run line is caught"
setup_skeleton
setup_fixture "deploy/ci-worker/fixture3.sh" '
#!/usr/bin/env bash
OMNIPUS_HOME="$home" OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=20 /tmp/omnipus-ci start
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "planted-envvar-exit" 1 "$EXIT_CODE"
assert_output_contains "planted-envvar-finding" "fixture3.sh" "$OUTPUT"

# --- Test 4: audit event string in a non-comment Go line — CAUGHT ---

echo ""
echo "Test 4: the turn.orphan_timeout audit event string is caught"
setup_skeleton
setup_fixture "pkg/audit/fixture4.go" '
package audit

const EventTurnOrphanTimeout = "turn.orphan_timeout"
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "planted-event-exit" 1 "$EXIT_CODE"
assert_output_contains "planted-event-finding" "fixture4.go" "$OUTPUT"

# --- Test 5: same symbols, but only inside comments — NOT caught ---

echo ""
echo "Test 5: guarded symbols mentioned only in // and # comments are NOT caught"
setup_skeleton
setup_fixture "pkg/agent/fixture5.go" '
package agent

// ArmOrphanForegroundTurnWatch, DisarmOrphanForegroundTurnWatch,
// fireOrphanForegroundTurnWatch, reapOrphanForegroundTurn,
// sessionStillOrphaned, hasLiveCriticalDelegate,
// getActiveRootTurnStateForSession, OrphanedTurnGraceSeconds, and
// EventTurnOrphanTimeout (turn.orphan_timeout) were deleted by ADR-082 D1.
func noop() {}
'
setup_fixture "deploy/ci-worker/fixture5.sh" '
#!/usr/bin/env bash
# OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS used to be set here (ADR-045);
# ADR-082 D1 deleted the watchdog it configured.
echo "no-op"
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "comment-only-exit" 0 "$EXIT_CODE"
assert_output_not_contains "comment-only-not-flagged" "fixture5.go" "$OUTPUT"
assert_output_not_contains "comment-only-shell-not-flagged" "fixture5.sh" "$OUTPUT"

# --- Test 6: the four explicitly KEPT names are NOT caught ---

echo ""
echo "Test 6: the kept subagent-span-forwarder watchdog names are NOT caught"
setup_skeleton
setup_fixture "pkg/gateway/fixture6.go" '
package gateway

import "time"

var orphanWatchdogTimeout = 60 * time.Second
var orphanWatchdogMaxRechecks = 15

func startOrphanWatchdog() {}

const SubTurnOrphan = "sub_turn.orphan"
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "kept-names-exit" 0 "$EXIT_CODE"
assert_output_not_contains "kept-names-not-flagged" "fixture6.go" "$OUTPUT"

# --- Test 7: a clean tree with nothing guarded anywhere — exits 0 ---

echo ""
echo "Test 7: a clean tree with no guarded symbols anywhere exits 0"
setup_skeleton
setup_fixture "pkg/agent/fixture7.go" '
package agent

func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "clean-tree-exit" 0 "$EXIT_CODE"
assert_output_contains "clean-tree-ok-message" "OK:" "$OUTPUT"

# --- Test 8: a tree missing a required scan directory refuses to report green ---

echo ""
echo "Test 8: a tree missing a required directory exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/pkg"
# Deliberately omit cmd/, scripts/, .github/, deploy/, tests/, src/, Makefile.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$LINT_SCRIPT" 2>&1)
EXIT_CODE=$?
assert_exit_code "missing-dir-exit" 2 "$EXIT_CODE"

# --- Summary ----------------------------------------------------------------

echo ""
echo "─────────────────────────────────────────"
echo "Results: ${PASS} passed, ${FAIL} failed"

if [[ "$FAIL" -gt 0 ]]; then
  echo ""
  echo "Failures:"
  for e in "${ERRORS[@]}"; do
    echo "  - $e"
  done
  exit 1
fi

echo "All assertions passed."
exit 0
