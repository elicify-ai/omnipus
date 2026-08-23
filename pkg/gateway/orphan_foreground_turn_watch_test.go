// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// orphan_foreground_turn_watch_test.go — gateway-level wiring coverage for
// the orphan-foreground-turn watchdog (ADR-045, pkg/agent/orphan_watch.go).
//
// Not to be confused with the PRE-EXISTING, unrelated "orphan watchdog"
// tested in orphan_watchdog_liveness_test.go (pkg/gateway's own
// startOrphanWatchdog/orphanWatchdogTimeout) — that mechanism synthesizes a
// fabricated subagent_end{status:"interrupted"} frame for a delegation SPAN
// the event forwarder believes has gone quiet; it never touches or cancels
// any turn. ADR-045's watchdog is a completely different, agent-package
// mechanism that actually bounds and hard-aborts an abandoned ROOT turn.
// These new tests are prefixed TestOrphanForegroundTurnWatch_ (not
// TestOrphanWatchdog_) to keep the two mechanisms unambiguous at a glance,
// while both prefixes still match the shared `-run '^TestOrphan'` gate.
//
// The escalation mechanics themselves (graceful -> turn-scoped-hard ->
// turn-scoped-detach, Critical-descendant survival, re-arm, disable) are
// already exhaustively covered by pkg/agent/orphan_watch_test.go. These
// tests cover only the GATEWAY'S OWN decision of WHEN to call
// Arm/DisarmOrphanForegroundTurnWatch:
//   - last watcher disconnecting arms the watch
//   - another connection still watching the same session suppresses arming
//   - a reattach (handleAttachSession) disarms a pending watch

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// orphanWatchAssertTimeout is the ceiling for the arm/disarm state-change
// require.Eventually assertions and the turn-start waits below. It is
// deliberately generous: these assert that an event-driven state DOES flip
// (arm on last-connection teardown, disarm on reattach/continuation), not that
// it flips within a few seconds. The ci go-test gate runs -p4 on 8 vCPUs
// (GOMAXPROCS pinned to 2, so ~1:1 threads:cores), but a residual scheduling
// spike can still delay the goroutine that flips the flag past a tight 3s
// window — the source of the intermittent orphan-watch flake seen 2026-07-17.
// The package -timeout is the real hang backstop.
const orphanWatchAssertTimeout = 15 * time.Second

// newOrphanForegroundTurnTestWSHandler builds a WSHandler backed by a real
// *agent.AgentLoop whose LLM calls block (via blockingCancelProvider, shared
// with cancel_audit_test.go in this package) until explicitly canceled — so a
// turn stays genuinely registered in the agent loop's active-turn state while
// a test manipulates WebSocket connections around it. graceSeconds is wired
// straight into config.GatewayConfig.OrphanedTurnGraceSeconds.
//
// Starts the agent loop's Run goroutine (mirroring newCancelTestWSHandler /
// newStreamingTestWSHandler in this package) — without it, PublishInbound
// sends to the bus but nothing consumes it, and blockingCancelProvider.Chat
// is never entered at all.
func newOrphanForegroundTurnTestWSHandler(
	t *testing.T,
	graceSeconds int,
) (*WSHandler, *agent.AgentLoop, *blockingCancelProvider) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	bp := newBlockingCancelProvider()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host:                     "127.0.0.1",
			Port:                     8080,
			DevModeBypass:            true,
			OrphanedTurnGraceSeconds: &graceSeconds,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "blocking-cancel-provider"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent: no implicit "main" sentinel remains
			// for chat dispatch to resolve to (ADR-064).
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, bp)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run exited: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel() // unblocks blockingCancelProvider.Chat's <-ctx.Done()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Logf("agent loop Run did not exit within 3s of cancel")
		}
	})
	time.Sleep(20 * time.Millisecond) // give the loop time to start reading from the bus

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	return handler, al, bp
}

// startOrphanTestTurn mints a session on conn via a message frame and blocks
// until the underlying (blocking) provider confirms the turn is genuinely
// in flight (registered as an active turn, not yet finished).
func startOrphanTestTurn(t *testing.T, conn *websocket.Conn, bp *blockingCancelProvider) string {
	t.Helper()
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start blocking turn for orphan-watch gateway test"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", orphanWatchAssertTimeout)
	require.NotEmpty(t, started.SessionID)

	select {
	case <-bp.ready:
	case <-time.After(orphanWatchAssertTimeout):
		t.Fatal("BLOCKED: blockingCancelProvider never entered Chat — turn did not start in time")
	}
	return started.SessionID
}

// countChatIDsMappedToSessionForTest counts how many entries in
// h.sessionIDs currently map to sessionID — used to confirm both
// connections in the multi-tab test are registered before exercising the
// teardown race.
func countChatIDsMappedToSessionForTest(h *WSHandler, sessionID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, sid := range h.sessionIDs {
		if sid == sessionID {
			count++
		}
	}
	return count
}

// TestOrphanForegroundTurnWatch_LastConnectionTeardown_Arms proves the
// gateway's ServeHTTP teardown defer arms the orphan watchdog when the
// closing connection was the ONLY one watching its session.
func TestOrphanForegroundTurnWatch_LastConnectionTeardown_Arms(t *testing.T) {
	handler, al, bp := newOrphanForegroundTurnTestWSHandler(
		t,
		30,
	) // long grace — this test only checks the ARMED state, never lets it fire
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() }) // defensive: safe even after the explicit Close() below
	sendWSAuthFrameDevMode(t, conn)

	sessionID := startOrphanTestTurn(t, conn, bp)
	require.False(t, al.HasOrphanWatchArmed(sessionID), "must not be armed while the connection is still open")

	// Close the only connection watching this session.
	require.NoError(t, conn.Close())

	require.Eventually(
		t,
		func() bool { return al.HasOrphanWatchArmed(sessionID) },
		orphanWatchAssertTimeout,
		20*time.Millisecond,
		"closing the LAST connection watching a session must arm the orphan-foreground-turn watchdog",
	)
}

// TestOrphanForegroundTurnWatch_AnotherConnectionStillWatching_NotArmed
// proves the multi-tab-safety check: when a SECOND connection is still
// attached to the same session, closing the FIRST (originating) connection
// must NOT arm the watchdog — a sibling tab must never have its live turn
// interrupted just because another tab closed.
func TestOrphanForegroundTurnWatch_AnotherConnectionStillWatching_NotArmed(t *testing.T) {
	handler, al, bp := newOrphanForegroundTurnTestWSHandler(t, 30)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn1 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn1.Close() }) // defensive: safe even after the explicit Close() below
	sendWSAuthFrameDevMode(t, conn1)
	sessionID := startOrphanTestTurn(t, conn1, bp)

	// A second connection attaches to the SAME session (e.g. a second open tab).
	conn2 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn2.Close() })
	sendWSAuthFrameDevMode(t, conn2)
	attachFrame := wsClientFrameTestHelper{Type: "attach_session", SessionID: sessionID}
	attachData, err := json.Marshal(attachFrame)
	require.NoError(t, err)
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, attachData))

	// Give the server a brief window to process the attach before closing conn1
	// — handleAttachSession registers h.sessionIDs[chatID2]=sessionID near the
	// very start of the handler, well before it starts streaming replay.
	require.Eventually(t, func() bool {
		return countChatIDsMappedToSessionForTest(handler, sessionID) >= 2
	}, orphanWatchAssertTimeout, 20*time.Millisecond, "both connections must be mapped to sessionID before the teardown race is exercised")

	require.NoError(t, conn1.Close())

	// Negative assertion held over a real time window — proves the watchdog
	// is never armed while conn2 remains attached.
	require.Never(t, func() bool { return al.HasOrphanWatchArmed(sessionID) }, 1*time.Second, 20*time.Millisecond,
		"closing one of TWO connections watching the same session must NOT arm the watchdog — conn2 is still watching")
}

// TestOrphanForegroundTurnWatch_Reattach_Disarms proves that
// handleAttachSession cancels a pending watchdog as soon as a live
// connection reattaches to the orphaned session (the common
// refresh/reconnect case).
func TestOrphanForegroundTurnWatch_Reattach_Disarms(t *testing.T) {
	handler, al, bp := newOrphanForegroundTurnTestWSHandler(t, 30)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn1 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn1.Close() }) // defensive: safe even after the explicit Close() below
	sendWSAuthFrameDevMode(t, conn1)
	sessionID := startOrphanTestTurn(t, conn1, bp)

	require.NoError(t, conn1.Close())
	require.Eventually(
		t,
		func() bool { return al.HasOrphanWatchArmed(sessionID) },
		orphanWatchAssertTimeout,
		20*time.Millisecond,
		"precondition: closing the last connection must arm the watchdog",
	)

	// Reattach via a fresh connection.
	conn2 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn2.Close() })
	sendWSAuthFrameDevMode(t, conn2)
	attachFrame := wsClientFrameTestHelper{Type: "attach_session", SessionID: sessionID}
	attachData, err := json.Marshal(attachFrame)
	require.NoError(t, err)
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, attachData))

	require.Eventually(
		t,
		func() bool { return !al.HasOrphanWatchArmed(sessionID) },
		orphanWatchAssertTimeout,
		20*time.Millisecond,
		"reattaching (handleAttachSession) must disarm the pending orphan-foreground-turn watchdog",
	)
}

// TestOrphanForegroundTurnWatch_ChatMessageContinuation_Disarms covers a gap
// flagged by pr-test-analyzer on the original ADR-045 gate: the
// session-CONTINUATION branch of handleChatMessage (websocket.go, a message
// frame carrying an EXISTING session_id) also calls
// DisarmOrphanForegroundTurnWatch — a separate code path from
// handleAttachSession's own disarm call, covered by
// TestOrphanForegroundTurnWatch_Reattach_Disarms above. Only the
// attach_session path had regression coverage; this test exercises the
// OTHER disarm call site so a future refactor that drops one but not the
// other is caught.
func TestOrphanForegroundTurnWatch_ChatMessageContinuation_Disarms(t *testing.T) {
	handler, al, bp := newOrphanForegroundTurnTestWSHandler(t, 30)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn1 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn1.Close() }) // defensive: safe even after the explicit Close() below
	sendWSAuthFrameDevMode(t, conn1)
	sessionID := startOrphanTestTurn(t, conn1, bp)

	require.NoError(t, conn1.Close())
	require.Eventually(
		t,
		func() bool { return al.HasOrphanWatchArmed(sessionID) },
		orphanWatchAssertTimeout,
		20*time.Millisecond,
		"precondition: closing the last connection must arm the watchdog",
	)

	// Reconnect via a FRESH connection and send a "message" frame carrying
	// the EXISTING session_id — this exercises handleChatMessage's session-
	// CONTINUATION branch (not attach_session), which must ALSO disarm the
	// pending watch.
	conn2 := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn2.Close() })
	sendWSAuthFrameDevMode(t, conn2)
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "continuing this session", SessionID: sessionID}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, msgData))

	require.Eventually(
		t,
		func() bool { return !al.HasOrphanWatchArmed(sessionID) },
		orphanWatchAssertTimeout,
		20*time.Millisecond,
		"sending a message with an existing session_id (handleChatMessage's continuation branch) must disarm the pending watchdog",
	)
}
