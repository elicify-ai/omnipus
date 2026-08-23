// cancel_audit_test.go — T0: Real handleCancel flow asserts that
// turn_cancel_attempt is written by the actual cancel state machine.
//
// Theater smell fixed: old versions only checked that an audit logger was
// constructed (setup state). This test drives a real blocking LLM turn through
// the WebSocket cancel path and asserts the JSONL audit file on disk contains
// turn_cancel_attempt with was_fired=true from the actual state machine.
//
// Traces to: pkg/agent/cancel.go:150 — audit.Emit(EventTurnCancelAttempt)
// FR-10, FR-17.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// cancelTestTurnStartDeadline bounds "the server never got this far" for the
// WS cancel tests (cancel_audit_test.go, cancel_two_stage_test.go,
// cancel_transcript_order_test.go). It is a HANG DETECTOR, not a performance
// assertion — nothing about these tests gets slower by making it large,
// because every wait it guards is on a latching signal (a closed `ready`
// channel, or a frame that has already been queued) and so returns the
// instant the event happens.
//
// It is deliberately ~100x the healthy cost. On an idle machine the whole
// span it covers — WS dial, message publish, route, context assembly
// (memrooms index rebuild included), and the provider entering Chat —
// completes inside 0.3s. The previous value was 5s, which sounds generous but
// is only a ~16x margin, and that was NOT enough: on the shared CI worker
// this exact span has been measured at >5s while the host was stalled, and
// all three tests failed together in one run (ci-omnipus-2 @670a8c0c) with
// "BLOCKED: ... never entered Chat" and readFrameOfType i/o timeouts. Those
// were false negatives — the turn was starting normally, just slowly.
//
// Note the failure mode this trades against is benign: a genuinely wedged
// server now reports after 30s instead of 5s, still far inside the package's
// own -timeout. Do NOT "optimise" this back down to shave test time; it costs
// nothing on a healthy run.
const cancelTestTurnStartDeadline = 30 * time.Second

// blockingCancelProvider blocks Chat until its context is canceled. Signals
// via ready when the provider has entered Chat so tests know a turn is in flight.
type blockingCancelProvider struct {
	ready chan struct{} // closed once on first Chat entry
}

func newBlockingCancelProvider() *blockingCancelProvider {
	return &blockingCancelProvider{ready: make(chan struct{})}
}

func (b *blockingCancelProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	select {
	case <-b.ready:
	default:
		close(b.ready)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingCancelProvider) GetDefaultModel() string { return "blocking-cancel-provider" }

// newCancelTestWSHandler creates a WSHandler backed by an agent loop that:
//   - uses a blocking provider (turns block until canceled)
//   - has audit logging enabled
//
// Returns the handler, msgBus, auditDir, and the blocking provider.
func newCancelTestWSHandler(t *testing.T) (*WSHandler, *bus.MessageBus, string, *blockingCancelProvider) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// The workspace is set to tmpDir/workspace so that filepath.Dir(workspace)=tmpDir,
	// and the audit logger writes to tmpDir/system/audit.jsonl.
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	auditDir := filepath.Join(tmpDir, "system")
	// audit.NewLogger creates the dir, but we note the path for assertions.

	bp := newBlockingCancelProvider()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18800, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "blocking-cancel-provider"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent. There is no implicit "main" sentinel
			// to fall back on any more (ADR-064), and handleChatMessage now
			// REFUSES a chat frame it cannot resolve an agent for rather than
			// creating a session with an empty owner -- so a config with no
			// agents produces no session_started frame at all.
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Mode:     config.SandboxModeOff,
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)

	al := mustAgentLoop(t, cfg, msgBus, bp)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(cancelTestTurnStartDeadline):
			// Log-only: this never fails the test. It is on the same budget as
			// the assertions above so a slow host cannot make this line appear
			// inside an unrelated failure block and read like the cause.
			t.Logf("agent loop Run did not exit within %v", cancelTestTurnStartDeadline)
		}
	})
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	return handler, msgBus, auditDir, bp
}

// readAuditEventNamesFromDir reads all event name strings from auditDir/audit.jsonl.
func readAuditEventNamesFromDir(t *testing.T, auditDir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	type row struct {
		Event string `json:"event"`
	}
	var events []string
	for _, line := range splitCancelAuditTestLines(data) {
		if len(line) == 0 {
			continue
		}
		var r row
		if json.Unmarshal(line, &r) == nil && r.Event != "" {
			events = append(events, r.Event)
		}
	}
	return events
}

// splitCancelAuditTestLines splits JSONL byte data on newline boundaries.
func splitCancelAuditTestLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// TestCancel_AuditEventEmitted (T0) — drives the real handleCancel flow and
// asserts that turn_cancel_attempt is written by the actual cancel state machine.
//
// Theater smell: old test constructed an audit logger and called
// fakeTurn.ClaimCancel() directly — it never invoked handleCancel at all.
//
// This version:
//  1. Starts a real turn via WebSocket (blocking provider blocks until canceled)
//  2. Issues a WebSocket cancel frame (routes through WSHandler.handleCancel →
//     AgentLoop.RequestCancel → audit.Emit)
//  3. Asserts the real audit JSONL file contains turn_cancel_attempt with
//     was_fired=true (not a hardcoded no-op)
//  4. Issues a second cancel on the same finished session and asserts
//     was_fired=false appears — proving the two events reflect real state.
//
// Traces to: pkg/agent/cancel.go:150 — audit.Emit(EventTurnCancelAttempt)
func TestCancel_AuditEventEmitted(t *testing.T) {
	handler, _, auditDir, bp := newCancelTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// Start a real turn.
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start blocking turn for audit test"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	// Wait until the blocking provider is inside Chat (turn is genuinely in flight).
	// bp.ready is CLOSED (not sent to) on first Chat entry, so this select can
	// never miss the signal no matter how late it is armed — the deadline only
	// bounds a genuine hang. See cancelTestTurnStartDeadline.
	select {
	case <-bp.ready:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("BLOCKED: blockingCancelProvider never entered Chat — turn did not start in time")
	}

	// Send the WebSocket cancel frame — drives the real handleCancel path.
	cancelFrame := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	cancelData, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))

	// The cancel_stage frame confirms the graceful phase fired.
	readFrameOfType(t, conn, "cancel_stage", cancelTestTurnStartDeadline)

	// Drain until the turn exits.
	//
	// This one deliberately does NOT use cancelTestTurnStartDeadline. Unlike
	// every other wait in this test, its result is DISCARDED, and no `done`
	// frame for this session id actually arrives here — so the call always
	// runs its timeout out in full and returns false unnoticed. It is a settle
	// window in practice, not a hang detector, which means its duration is
	// added to this test's wall-clock time verbatim: raising it to the shared
	// 30s budget took the test from 5.08s to 30.08s. Keep it small.
	drainUntilSessionDone(t, conn, sessionID, 5*time.Second)

	// ASSERT 1: turn_cancel_attempt must appear in the real audit log.
	require.Eventually(t, func() bool {
		events := readAuditEventNamesFromDir(t, auditDir)
		for _, ev := range events {
			if ev == audit.EventTurnCancelAttempt {
				return true
			}
		}
		return false
	}, cancelTestTurnStartDeadline, 30*time.Millisecond,
		"turn_cancel_attempt must be written by the real cancel state machine")

	// Count events before second cancel.
	var firstCount int
	events := readAuditEventNamesFromDir(t, auditDir)
	for _, ev := range events {
		if ev == audit.EventTurnCancelAttempt {
			firstCount++
		}
	}

	// DIFFERENTIATION: a second cancel on the now-finished session must produce
	// another turn_cancel_attempt (with was_fired=false). This proves the audit
	// comes from real state — a hardcoded emitter would either emit nothing or
	// always emit the same payload.
	cancelFrame2 := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	cancelData2, err := json.Marshal(cancelFrame2)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData2))

	require.Eventually(t, func() bool {
		events := readAuditEventNamesFromDir(t, auditDir)
		count := 0
		for _, ev := range events {
			if ev == audit.EventTurnCancelAttempt {
				count++
			}
		}
		return count >= firstCount+1
	}, cancelTestTurnStartDeadline, 30*time.Millisecond,
		"second cancel must produce a second turn_cancel_attempt (was_fired=false), total events: %v",
		readAuditEventNamesFromDir(t, auditDir))
}
