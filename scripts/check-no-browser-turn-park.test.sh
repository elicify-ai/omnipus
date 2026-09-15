#!/usr/bin/env bash
# check-no-browser-turn-park.test.sh
#
# Self-test for check-no-browser-turn-park.sh (ADR-085 Explicit Non-
# Behaviors mechanical guard, wave B9). A guard that cannot fail is no guard
# (docs/internal/false-green-patterns.md) — this builds scratch fixture
# trees under a temp directory, points the guard at each via REPO_ROOT (same
# pattern as check-no-orphan-turn-watchdog.test.sh), and NEVER touches the
# real repo tree. Per R-19b, every case below exercises the guard against at
# least one planted offender AND at least one clean tree, and prints the
# guard's observed exit code for each.
#
# Covers:
#   1. A clean tree with none of the three shapes anywhere — exits 0.
#   2. Clause (a): a planted "Take control" button string in a non-test
#      production file under src/components/browser/ — CAUGHT.
#   3. Clause (a): the same three strings, but ONLY inside a `//` comment
#      (the sanctioned "this used to exist" retirement note) — NOT caught.
#   4. Clause (a): the same three strings inside a `.test.tsx` file (the
#      existing "never renders a Take control button" assertion shape) —
#      NOT caught.
#   5. Clause (b): a planted resume-dispatcher-shaped identifier
#      (ResumeBrowserWheel) in pkg/agent/ — CAUGHT.
#   6. Clause (b): ordinary ForLLM prose saying "the operator resumes your
#      browser driving" (two separate words, not one identifier) — NOT
#      caught.
#   7. Clause (b): an unrelated Resume-suffixed symbol with no browser/
#      wheel/handover token (e.g. ResumePlansOwnedBy) — NOT caught.
#   8. Clause (c): a planted call to InterruptSessionHard inside
#      pkg/tools/browser/ — CAUGHT.
#   9. Clause (c): a planted call to RequestCancel inside
#      pkg/agent/browser_deferral.go — CAUGHT.
#   10. Clause (c): the same four names, but only inside a `//` comment —
#       NOT caught.
#   11. Clause (c): RequestCancel called from a pkg/agent/ file OTHER than
#       browser_deferral.go — NOT caught (out of this guard's scoped pair
#       of locations; the ADR-057 cancel machinery lives there legitimately).
#   12. A tree missing a required directory — exits 2, never a silent green.
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD_SCRIPT="${SCRIPT_DIR}/check-no-browser-turn-park.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/browser-turn-park-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR"/{src/components/browser,pkg/agent,pkg/tools/browser}
}

setup_fixture() {
  local subpath="$1" content="$2"
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

assert_contains() {
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

assert_not_contains() {
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

echo "=== check-no-browser-turn-park self-test (ADR-085 Explicit Non-Behaviors) ==="
echo ""

echo "Test 1: a clean tree with none of the shapes exits 0"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() {
  return <div>live view</div>
}
'
setup_fixture "pkg/agent/browser_deferral.go" '
package agent

func isBrowserControlGatedTool(name string) bool { return false }
'
setup_fixture "pkg/tools/browser/tools.go" '
package browser

func controlledResult() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "clean-exit" 0 "$EXIT_CODE"
assert_contains "clean-ok" "OK:" "$OUTPUT"

echo ""
echo "Test 2: clause (a) — a planted 'Take control' button string is caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() {
  return <button>Take control</button>
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "toggle-string-exit" 1 "$EXIT_CODE"
assert_contains "toggle-string-finding" "BrowserLiveView.tsx" "$OUTPUT"
assert_contains "toggle-string-clause" "Clause (a)" "$OUTPUT"

echo ""
echo "Test 3: clause (a) — the strings inside a // comment are NOT caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
// The old explicit Take control / Release control / Hand to agent toggle
// was removed by ADR-040 D1/D2.
export function BrowserLiveView() {
  return <div>live view</div>
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "toggle-comment-exit" 0 "$EXIT_CODE"

echo ""
echo "Test 4: clause (a) — the strings inside a .test.tsx assertion are NOT caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() {
  return <div>live view</div>
}
'
setup_fixture "src/components/browser/BrowserLiveView.controlToggle.test.tsx" '
it("never renders a Take control / Release control / Hand to agent button", () => {
  expect(screen.queryByText("Take control")).not.toBeInTheDocument()
})
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "toggle-test-file-exit" 0 "$EXIT_CODE"

echo ""
echo "Test 5: clause (b) — a planted ResumeBrowserWheel identifier is caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() { return <div/> }
'
setup_fixture "pkg/agent/rogue_resume.go" '
package agent

func ResumeBrowserWheel(sessionID string) {
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "resume-dispatcher-exit" 1 "$EXIT_CODE"
assert_contains "resume-dispatcher-finding" "rogue_resume.go" "$OUTPUT"
assert_contains "resume-dispatcher-clause" "Clause (b)" "$OUTPUT"

echo ""
echo "Test 6: clause (b) — ordinary prose ('resumes your browser driving') is NOT caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() { return <div/> }
'
setup_fixture "pkg/agent/browser_deferral.go" '
package agent

const note = "The operator resumes your browser driving by sending a new message."
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "resume-prose-exit" 0 "$EXIT_CODE"

echo ""
echo "Test 7: clause (b) — an unrelated Resume-suffixed symbol is NOT caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() { return <div/> }
'
setup_fixture "pkg/agent/plan_engine.go" '
package agent

func (pe *PlanEngine) ResumePlansOwnedBy(agentID string) error { return nil }
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "resume-unrelated-exit" 0 "$EXIT_CODE"

echo ""
echo "Test 8: clause (c) — a planted InterruptSessionHard call in pkg/tools/browser/ is caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() { return <div/> }
'
setup_fixture "pkg/tools/browser/rogue_take.go" '
package browser

func rogueTake(al AgentLoopLike, sessionID string) {
	al.InterruptSessionHard(sessionID, ScopeSoft, "operator took the wheel")
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "park-browser-pkg-exit" 1 "$EXIT_CODE"
assert_contains "park-browser-pkg-finding" "rogue_take.go" "$OUTPUT"
assert_contains "park-browser-pkg-clause" "Clause (c)" "$OUTPUT"

echo ""
echo "Test 9: clause (c) — a planted RequestCancel call in browser_deferral.go is caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() { return <div/> }
'
setup_fixture "pkg/agent/browser_deferral.go" '
package agent

func rogueDeferral(al *AgentLoop, sessionID string) {
	al.RequestCancel(context.Background(), sessionID, "user", "webchat", "operator took the wheel")
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "park-deferral-file-exit" 1 "$EXIT_CODE"
assert_contains "park-deferral-file-finding" "browser_deferral.go" "$OUTPUT"

echo ""
echo "Test 10: clause (c) — the names inside a // comment are NOT caught"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() { return <div/> }
'
setup_fixture "pkg/tools/browser/notes.go" '
package browser

// A browser take-over must never call RequestCancel, RequestCancelForSession,
// RequestCancelByChannelChat or InterruptSessionHard.
func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "park-comment-exit" 0 "$EXIT_CODE"

echo ""
echo "Test 11: clause (c) — RequestCancel elsewhere in pkg/agent/ is out of this guard's scope"
setup_skeleton
setup_fixture "src/components/browser/BrowserLiveView.tsx" '
export function BrowserLiveView() { return <div/> }
'
setup_fixture "pkg/agent/cancel.go" '
package agent

func (al *AgentLoop) RequestCancel(ctx context.Context, sessionID, userID, channel, reason string) error {
	return nil
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "park-out-of-scope-exit" 0 "$EXIT_CODE"

echo ""
echo "Test 12: a tree missing a required directory exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR/pkg/agent"
# Deliberately omit src/components/browser and pkg/tools/browser.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
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
