#!/usr/bin/env bash
# check-operator-prompt-sites.test.sh
#
# Self-test for check-operator-prompt-sites.sh (ADR-085 BROWSER-FR-029/
# FR-029a mechanical guard, wave B9). A guard that cannot fail is no guard
# (docs/internal/false-green-patterns.md) — this builds scratch fixture
# trees under a temp directory, points the guard at each via REPO_ROOT (same
# pattern as check-no-orphan-turn-watchdog.test.sh), and NEVER touches the
# real repo tree. Per R-19b, every case below exercises the guard against at
# least one planted offender AND at least one clean tree, and prints the
# guard's observed exit code for each.
#
# Covers:
#   1. A clean fixture with exactly 3 OperatorPrompt=true sites and 6
#      PublishInbound call sites — exits 0.
#   2. A FOURTH OperatorPrompt=true site planted — CAUGHT (exit 1).
#   3. Only 2 OperatorPrompt=true sites (one removed) — CAUGHT (exit 1).
#   4. OperatorPrompt=true mentioned only inside a `//` comment — NOT counted
#      (does not change the 3/6 verdict).
#   5. OperatorPrompt=true inside a _test.go file — NOT counted.
#   6. A SEVENTH PublishInbound call site planted — CAUGHT (exit 1).
#   7. The PublishInbound func DEFINITION line itself (no leading dot) is
#      NEVER counted as a call site.
#   8. A tree missing the required pkg/ directory — exits 2, never a silent
#      green.
#
# Exit code: 0 if all assertions pass, 1 if any assertion fails.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARD_SCRIPT="${SCRIPT_DIR}/check-operator-prompt-sites.sh"

PASS=0
FAIL=0
ERRORS=()

TMP_DIR=$(mktemp -d /tmp/operator-prompt-sites-test.XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

setup_skeleton() {
  rm -rf "${TMP_DIR:?}"
  mkdir -p "$TMP_DIR/pkg"
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

# A clean, minimal 3-site/6-call fixture, reused as the base for every test.
plant_clean_baseline() {
  setup_fixture "pkg/gateway/websocket.go" '
package gateway

func handle(h *WSHandler, msg bus.InboundMessage) {
	msg = bus.InboundMessage{
		OperatorPrompt: true,
	}
	if err := h.msgBus.PublishInbound(pubCtx, msg); err != nil {
		return
	}
}
'
  setup_fixture "pkg/gateway/sse.go" '
package gateway

func handleSSE(h *WSHandler, msg bus.InboundMessage) {
	msg = bus.InboundMessage{
		OperatorPrompt: true,
	}
	if err := h.msgBus.PublishInbound(r.Context(), msg); err != nil {
		return
	}
}
'
  setup_fixture "pkg/channels/base.go" '
package channels

func HandleMessage(c *Base, msg bus.InboundMessage) {
	msg = bus.InboundMessage{
		OperatorPrompt: true,
	}
	if err := c.bus.PublishInbound(ctx, msg); err != nil {
		return
	}
}
'
  setup_fixture "pkg/gateway/ws_ask_user.go" '
package gateway

func (d *Dispatcher) DispatchResume(ctx context.Context, msg bus.InboundMessage) error {
	return d.msgBus.PublishInbound(ctx, msg)
}
'
  setup_fixture "pkg/agent/async_notifier.go" '
package agent

func notify(n *AsyncNotifier) {
	publishErr = n.loop.bus.PublishInbound(pubCtx, bus.InboundMessage{})
}
'
  setup_fixture "pkg/agent/loop.go" '
package agent

func (al *AgentLoop) runAgentLoop() {
	if pubErr := al.bus.PublishInbound(ctx, followUp); pubErr != nil {
		return
	}
}
'
  setup_fixture "pkg/bus/bus.go" '
package bus

func (mb *MessageBus) PublishInbound(ctx context.Context, msg InboundMessage) error {
	return nil
}
'
}

echo "=== check-operator-prompt-sites self-test (ADR-085 BROWSER-FR-029/FR-029a) ==="
echo ""

echo "Test 1: a clean 3-site/6-call fixture exits 0"
setup_skeleton
plant_clean_baseline
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "clean-exit" 0 "$EXIT_CODE"
assert_contains "clean-ok" "OK:" "$OUTPUT"

echo ""
echo "Test 2: a FOURTH OperatorPrompt=true site is caught"
setup_skeleton
plant_clean_baseline
setup_fixture "pkg/agent/rogue_release.go" '
package agent

func rogueRelease() bus.InboundMessage {
	return bus.InboundMessage{
		OperatorPrompt: true,
	}
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "fourth-site-exit" 1 "$EXIT_CODE"
assert_contains "fourth-site-finding" "rogue_release.go" "$OUTPUT"
assert_contains "fourth-site-count" "4 site(s)" "$OUTPUT"

echo ""
echo "Test 3: only 2 OperatorPrompt=true sites (one removed) is caught"
setup_skeleton
plant_clean_baseline
setup_fixture "pkg/channels/base.go" '
package channels

func HandleMessage(c *Base, msg bus.InboundMessage) {
	msg = bus.InboundMessage{}
	if err := c.bus.PublishInbound(ctx, msg); err != nil {
		return
	}
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "missing-site-exit" 1 "$EXIT_CODE"
assert_contains "missing-site-count" "2 site(s)" "$OUTPUT"

echo ""
echo "Test 4: OperatorPrompt=true mentioned only in a // comment is not counted"
setup_skeleton
plant_clean_baseline
setup_fixture "pkg/agent/prose.go" '
package agent

// A fourth site would read OperatorPrompt: true here, but this is prose.
func noop() {}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "comment-only-exit" 0 "$EXIT_CODE"
assert_contains "comment-only-count" "3 site(s)" "$OUTPUT"

echo ""
echo "Test 5: OperatorPrompt=true inside a _test.go file is not counted"
setup_skeleton
plant_clean_baseline
setup_fixture "pkg/agent/rogue_test.go" '
package agent

func TestRogue(t *testing.T) {
	msg := bus.InboundMessage{OperatorPrompt: true}
	_ = msg
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "test-file-exit" 0 "$EXIT_CODE"
assert_contains "test-file-count" "3 site(s)" "$OUTPUT"

echo ""
echo "Test 6: a SEVENTH PublishInbound call site is caught"
setup_skeleton
plant_clean_baseline
setup_fixture "pkg/agent/rogue_publish.go" '
package agent

func rogue(b *bus.MessageBus, msg bus.InboundMessage) {
	_ = b.PublishInbound(context.Background(), msg)
}
'
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "seventh-call-exit" 1 "$EXIT_CODE"
assert_contains "seventh-call-finding" "rogue_publish.go" "$OUTPUT"
assert_contains "seventh-call-count" "7" "$OUTPUT"

echo ""
echo "Test 7: the PublishInbound func definition itself is never counted"
setup_skeleton
plant_clean_baseline
# plant_clean_baseline already includes the func definition in pkg/bus/bus.go;
# assert its exact line is absent from the printed call-site list.
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "definition-not-call-exit" 0 "$EXIT_CODE"
if echo "$OUTPUT" | grep -q 'func (mb \*MessageBus) PublishInbound'; then
  echo "  FAIL [definition-not-counted]: func definition line appeared in call-site output"
  FAIL=$((FAIL + 1))
  ERRORS+=("[definition-not-counted] func definition line must never be counted as a call site")
else
  echo "  PASS [definition-not-counted]: func definition line absent from call-site output"
  PASS=$((PASS + 1))
fi

echo ""
echo "Test 8: a tree missing pkg/ exits 2 (never a silent green)"
rm -rf "${TMP_DIR:?}"
mkdir -p "$TMP_DIR"
OUTPUT=$(REPO_ROOT="$TMP_DIR" bash "$GUARD_SCRIPT" 2>&1)
EXIT_CODE=$?
echo "  observed exit=$EXIT_CODE"
assert_exit_code "missing-pkg-exit" 2 "$EXIT_CODE"

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
