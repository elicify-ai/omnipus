// cancel_two_stage_test.go — T6: Two-stage cancel timer (graceful → hard).
//
// Theater smell fixed: old test simulated the timer with time.AfterFunc(10ms)
// on a fakeTurnHook — it never called the real cancel state machine. The timer
// ran in the test goroutine with no agent loop involved.
//
// This version:
//  1. Starts a real turn backed by a provider that ignores context cancellation
//     (so the turn survives the 3s graceful window and the hard abort fires)
//  2. Issues a real cancel via RequestCancel
//  3. Observes the "graceful" and "hard" cancel_stage frames emitted by
//     WSHandler.handleCancel → hooks.SendStageFrame
//
// NOTE: This test waits for the real 3-second hard-abort timer. Total test
// wall time is ~4 seconds. This is intentional — using the real timer proves
// the state machine actually fires the second stage, not a fake one.
//
// Traces to: pkg/agent/cancel.go:228-250 — Phase B 3-second AfterFunc.

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ironProvider ignores context cancellation and blocks for a fixed duration.
// It has a shutdownCh that the test can close to unblock the provider
// goroutine during cleanup — preventing goroutine leaks detected by goleak.
type ironProvider struct {
	blockDur   time.Duration
	ready      chan struct{}
	shutdownCh chan struct{}
}

func newIronProvider(blockDur time.Duration) *ironProvider {
	return &ironProvider{
		blockDur:   blockDur,
		ready:      make(chan struct{}),
		shutdownCh: make(chan struct{}),
	}
}

// Shutdown unblocks any in-flight Chat call by closing the shutdownCh.
// Call this from test cleanup to prevent goroutine leaks.
func (p *ironProvider) Shutdown() {
	select {
	case <-p.shutdownCh:
	default:
		close(p.shutdownCh)
	}
}

func (p *ironProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	// Signal readiness regardless of ctx state.
	select {
	case <-p.ready:
	default:
		close(p.ready)
	}
	// Ignore ctx.Done — block for blockDur so the hard-abort timer fires.
	// shutdownCh allows test cleanup to unblock us, preventing goroutine leaks.
	select {
	case <-time.After(p.blockDur):
		return &providers.LLMResponse{Content: "survived", ToolCalls: []providers.ToolCall{}}, nil
	case <-p.shutdownCh:
		return nil, context.Canceled
	}
}

func (p *ironProvider) GetDefaultModel() string { return "iron-provider" }

// readCancelStageFrames drains WebSocket frames for at most `timeout` and
// returns all cancel_stage frame stage strings in order.
func readCancelStageFrames(conn *websocket.Conn, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	var stages []string
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline) //nolint:errcheck
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f struct {
			Type  string `json:"type"`
			Stage string `json:"stage"`
		}
		if json.Unmarshal(raw, &f) == nil && f.Type == "cancel_stage" {
			stages = append(stages, f.Stage)
		}
	}
	return stages
}

// TestCancel_TwoStageTimer_GracefulThenHard (T6) — verifies that after a
// cancel on an ongoing turn, the state machine emits "graceful" immediately
// and "hard" ~3 seconds later when the turn does not self-terminate.
//
// Theater smell: old test faked both timer firings with 10ms AfterFuncs and
// a fakeTurnHook. Neither the agent loop nor the real turn was involved.
//
// This version drives a real blocking turn (ironProvider) that ignores
// graceful cancellation, so the 3s hard-abort phase is forced to fire.
// The test observes the real cancel_stage frames from the WebSocket.
func TestCancel_TwoStageTimer_GracefulThenHard(t *testing.T) {
	// Not parallel — this test blocks for ~4 seconds waiting for the real timer.
	// Running it parallel could exhaust the test timeout budget when combined
	// with other slow tests. Use -timeout 60s or higher when running this package.

	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// ironProvider blocks for 20 seconds, well past the 3+5=8s hard+detach window.
	ip := newIronProvider(20 * time.Second)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18802, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "iron-provider"},
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
			Mode: config.SandboxModeOff,
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, ip)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run: %v", err)
		}
	}()
	// t.Cleanup runs LIFO: the LAST-registered cleanup fires FIRST.
	// ip.Shutdown must fire FIRST (unblocks the iron provider so al.Run can exit),
	// then the cancel/wait cleanup runs second to drain the loop goroutine.
	// Register cancel/wait BEFORE ip.Shutdown so ip.Shutdown fires first.
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(cancelTestTurnStartDeadline):
			// Log-only: never fails the test. Kept on the shared budget so a
			// slow host cannot surface this line inside an unrelated failure
			// block and read like the cause.
			t.Logf("agent loop Run did not exit within %v", cancelTestTurnStartDeadline)
		}
	})
	t.Cleanup(ip.Shutdown)
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// Start a real turn.
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start iron turn"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	// Wait until the iron provider is inside Chat. ip.ready is CLOSED (not
	// sent to) on first Chat entry, so this select cannot miss the signal
	// however late it is armed — the deadline only bounds a genuine hang.
	// See cancelTestTurnStartDeadline.
	select {
	case <-ip.ready:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("BLOCKED: ironProvider never entered Chat")
	}

	// Fire cancel.
	cancelFrame := wsClientFrameTestHelper{Type: "cancel", SessionID: sessionID}
	cancelData, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))

	// Collect all cancel_stage frames for 5 seconds (enough to see graceful + hard).
	stages := readCancelStageFrames(conn, 5*time.Second)

	// ASSERT: "graceful" must be the first stage.
	require.GreaterOrEqual(t, len(stages), 1,
		"must receive at least one cancel_stage frame; got none")
	assert.Equal(t, "graceful", stages[0],
		"first cancel_stage must be 'graceful'")

	// ASSERT: "hard" must appear after "graceful" (fires at t+3s).
	// Allow up to 5 seconds for the hard stage to arrive.
	var sawHard bool
	for _, s := range stages {
		if s == "hard" {
			sawHard = true
			break
		}
	}
	if !sawHard {
		// If not seen yet, wait a little longer (up to 4.5s total from cancel).
		stages2 := readCancelStageFrames(conn, 3*time.Second)
		for _, s := range stages2 {
			if s == "hard" {
				sawHard = true
				break
			}
		}
	}
	assert.True(t, sawHard,
		"'hard' cancel_stage must fire ~3s after 'graceful' when the turn does not self-terminate; "+
			"all stages seen: %v", stages)

	// Verify ordering: if both stages arrived, graceful must precede hard.
	var gracefulIdx, hardIdx int = -1, -1
	allStages := append(stages, readCancelStageFrames(conn, 0)...)
	for i, s := range allStages {
		if s == "graceful" && gracefulIdx == -1 {
			gracefulIdx = i
		}
		if s == "hard" && hardIdx == -1 {
			hardIdx = i
		}
	}
	if gracefulIdx >= 0 && hardIdx >= 0 {
		assert.Less(t, gracefulIdx, hardIdx,
			"'graceful' stage must arrive before 'hard' stage")
	}

	// Cancel the context to stop the iron provider and let the test exit.
	cancel()
}
