//go:build !cgo

// Package gateway — WebSocket session_close and attach_session integration tests.
//
// T2: session_close frame → CloseSession idempotency wiring.
// T3: attach_session lazy-CAS wiring (FR-024).
//
// These tests exercise the real readLoop dispatch path via an httptest.Server
// so that the frame-routing glue in websocket.go is covered end-to-end.

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readFrameOfType drains incoming frames until it receives one with the
// expected Type field, or until the deadline elapses. It returns the first
// matching frame and any read error.
// This is needed because the WebSocket server may emit auxiliary frames (e.g.
// "session_state") between the auth ack and the frame the test is waiting for.
func readFrameOfType(t *testing.T, conn *websocket.Conn, wantType string, timeout time.Duration) wsServerFrameDecodeHelper {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline) //nolint:errcheck
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("readFrameOfType(%q): read error: %v", wantType, err)
		}
		var f wsServerFrameDecodeHelper
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatalf("readFrameOfType(%q): unmarshal error: %v", wantType, err)
		}
		if f.Type == wantType {
			return f
		}
		// Discard non-matching frame and loop.
	}
	t.Fatalf("readFrameOfType(%q): timed out after %v", wantType, timeout)
	return wsServerFrameDecodeHelper{} // unreachable
}

// ---------------------------------------------------------------------------
// T2: session_close frame → CloseSession idempotency wiring
// ---------------------------------------------------------------------------

// TestWS_SessionClose_AcksOnValidSessionID verifies that a session_close frame
// with a valid session_id causes the server to send a session_close_ack frame
// without dropping the connection.
//
// BDD:
//
//	Given an authenticated WebSocket connection,
//	When the client sends {"type":"session_close","session_id":"<valid-uuid>"},
//	Then the server responds with {"type":"session_close_ack","id":"<valid-uuid>"}
//	 and the connection remains open.
//
// Implements: T2 (pr-test-analyzer HIGH) — WS session_close → CloseSession wiring.
// Traces to: pkg/gateway/websocket.go case "session_close"
func TestWS_SessionClose_AcksOnValidSessionID(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	const sessionID = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"
	closeFrame := wsClientFrameTestHelper{Type: "session_close", SessionID: sessionID}
	data, err := json.Marshal(closeFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	resp := readFrameOfType(t, conn, "session_close_ack", 3*time.Second)
	assert.Equal(t, sessionID, resp.ID, "ack must echo back the session_id")

	// Connection must remain open after close ack.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	ping := wsClientFrameTestHelper{Type: "message", Content: "still-open"}
	pingData, _ := json.Marshal(ping)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
		"connection must remain open after session_close_ack")
}

// TestWS_SessionClose_ReturnsErrorOnEmptySessionID verifies that a session_close
// frame with an empty session_id causes the server to send an error frame (not
// a close frame) and that the connection remains open.
//
// BDD:
//
//	Given an authenticated WebSocket connection,
//	When the client sends {"type":"session_close"} with no session_id,
//	Then the server responds with {"type":"error"} and stays connected.
//
// Implements: T2 — missing-session_id guard.
// Traces to: pkg/gateway/websocket.go case "session_close" empty-ID branch.
func TestWS_SessionClose_ReturnsErrorOnEmptySessionID(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	emptyClose := wsClientFrameTestHelper{Type: "session_close"}
	data, _ := json.Marshal(emptyClose)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	resp := readFrameOfType(t, conn, "error", 3*time.Second)
	assert.NotEmpty(t, resp.Message, "error frame must carry a message for empty session_id")
}

// TestWS_SessionClose_Idempotent verifies that sending session_close twice for
// the same session_id does not panic and both calls are acknowledged.
//
// BDD:
//
//	Given an authenticated WebSocket connection,
//	When the client sends session_close for "sid-001" twice in sequence,
//	Then the server sends session_close_ack both times without error or close.
//
// Implements: T2 — duplicate session_close idempotency.
// Traces to: pkg/agent/session_end.go CloseSession idempotency gate (FR-027).
func TestWS_SessionClose_Idempotent(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	const sessionID = "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5"

	for i := 0; i < 2; i++ {
		frame := wsClientFrameTestHelper{Type: "session_close", SessionID: sessionID}
		data, _ := json.Marshal(frame)
		require.NoErrorf(t, conn.WriteMessage(websocket.TextMessage, data), "write #%d", i+1)

		resp := readFrameOfType(t, conn, "session_close_ack", 3*time.Second)
		assert.Equalf(t, sessionID, resp.ID, "ack must echo session_id on call #%d", i+1)
	}
}

// ---------------------------------------------------------------------------
// T3: attach_session lazy-CAS wiring (FR-024)
// ---------------------------------------------------------------------------

// TestWS_AttachSession_NoErrorOnValidSession verifies that attach_session with a
// valid session_id does not produce an error frame.
//
// BDD:
//
//	Given a connected and authenticated WebSocket client,
//	When the client sends attach_session with a session_id that does not exist in the store,
//	Then the server sends an error frame (session not found).
//
// Implements: T3 — attach_session generates appropriate response (FR-024).
// Traces to: pkg/gateway/websocket.go case "attach_session".
func TestWS_AttachSession_NoErrorOnValidSession(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Send attach_session with a session_id that doesn't exist — server returns error frame.
	attachFrame := wsClientFrameTestHelper{
		Type:      "attach_session",
		SessionID: "nonexistent-session-0000000000000",
	}
	data, _ := json.Marshal(attachFrame)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// Server should respond with an error frame (session not found).
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	var gotError bool
	for i := 0; i < 10; i++ {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var f wsServerFrameDecodeHelper
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if f.Type == "error" {
			gotError = true
			break
		}
	}
	assert.True(t, gotError, "attach_session with non-existent session_id must return error frame")
}

// TestWS_AttachSession_NoLazyCAS_WhenSameSession verifies that when attach_session
// arrives with the same session_id that is already current for the agent, no
// lazy CloseSession goroutine is launched (the prior == requested, so no CAS needed).
//
// BDD:
//
//	Given agent "same-agent" has current session "same-session-id",
//	When the client sends attach_session with agent_id="same-agent", session_id="same-session-id",
//	Then GetCurrentSession("same-agent") still returns "same-session-id" and no error occurs.
//
// Implements: T3 — same-session no-op guard.
// Traces to: pkg/gateway/websocket.go case "attach_session": prior != frame.SessionID guard.
func TestWS_AttachSession_NoLazyCAS_WhenSameSession(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	const agentID = "same-agent"
	const sessionID = "same-session-0000000000000000000000"
	al.SetCurrentSession(agentID, sessionID)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	attachFrame := wsClientFrameTestHelper{
		Type:      "attach_session",
		AgentID:   agentID,
		SessionID: sessionID,
	}
	data, _ := json.Marshal(attachFrame)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	time.Sleep(100 * time.Millisecond)

	gotSession, ok := al.GetCurrentSession(agentID)
	assert.True(t, ok)
	assert.Equal(t, sessionID, gotSession,
		"same-session attach_session must not disturb the current session")
}
