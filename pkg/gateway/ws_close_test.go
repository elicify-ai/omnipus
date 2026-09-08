// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_close_test.go — T-15 (S-13, FR-001/FR-002), ADR-082 D1/D7.
//
// The ADR-045 orphaned-foreground-turn watchdog is deleted in full: closing
// the LAST WebSocket connection watching a session no longer arms anything
// at all — there is no timer, no goroutine, and no config-driven grace
// period left to arm one with (pkg/agent/orphan_watch.go and its whole
// AgentLoop.orphanWatches field are gone). A turn's execution never depended
// on a UI connection to begin with (P1, ADR-082 §1) — this test proves the
// gateway side of that: tearing down the only connection watching an
// in-flight turn changes nothing about that turn.
package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestNoOrphanWatchArmedOnClose is T-15: "Given a webchat turn streaming on
// connection A, When A closes (and A was the ONLY connection watching its
// session — the exact condition that used to arm the ADR-045 watchdog),
// Then no timer/goroutine is registered and the turn's context is not
// cancelled after close plus a short wait."
//
// Uses newCancelTestWSHandler (cancel_audit_test.go, shared in this
// package) — its blockingCancelProvider blocks Chat on <-ctx.Done(), so the
// turn's TurnCancelHook.IsAlive() staying true after the connection closes
// and a wait elapses is direct, behavioral proof the turn's context was
// never touched: if anything had canceled it (armed or otherwise), the
// blocking provider's Chat call would have returned, the turn would have
// finished, and IsAlive() would flip false.
func TestNoOrphanWatchArmedOnClose(t *testing.T) {
	handler, _, _, bp := newCancelTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	al := handler.agentLoop

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// Start a real turn — the blocking provider parks inside Chat until its
	// context is canceled, so the turn stays genuinely registered
	// (activeTurnStates) and alive for as long as this test lets it.
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start blocking turn for ws-close test"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	select {
	case <-bp.ready:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("BLOCKED: blockingCancelProvider never entered Chat — turn did not start in time")
	}

	hook := al.GetActiveTurnHookForSession(sessionID)
	require.NotNil(t, hook, "precondition: the turn must be registered and findable before the connection closes")
	require.True(t, hook.IsAlive(), "precondition: the turn must be alive before the connection closes")

	// Close the ONLY connection watching this session — exactly the
	// last-watcher-teardown condition that used to arm
	// ArmOrphanForegroundTurnWatch. There is no arm call left to make; this
	// close now does nothing but ordinary connection bookkeeping.
	require.NoError(t, conn.Close())

	// Give the server's teardown defer (handleWS's per-connection cleanup)
	// time to run, and then some — long enough that a real ADR-045 watchdog
	// (whose shortest observed test grace elsewhere in this codebase's
	// history was 1s) would have fired if anything still armed it.
	time.Sleep(1500 * time.Millisecond)

	require.True(t, hook.IsAlive(),
		"the turn must still be alive after its only watching connection closed and a wait elapsed — "+
			"ADR-082 D1 deleted the mechanism that used to cancel it here; a turn never depends on a UI connection")

	stillHook := al.GetActiveTurnHookForSession(sessionID)
	require.NotNil(t, stillHook,
		"the turn must still be resolvable via GetActiveTurnHookForSession after the connection closed — "+
			"nothing should have deregistered or reaped it")
	require.True(t, stillHook.IsAlive())
}
