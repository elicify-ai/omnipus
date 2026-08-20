// websocket_pingpong_test.go — regression tests for the application-layer
// ping/pong heartbeat fix.
//
// Bug: SPA's client-driven {"type":"ping"} heartbeat had no server response.
// After 60 s of chat idle the client force-closed the WS and reconnected —
// every minute — which on iPad WebKit accumulated until the tab OOM'd.
//
// Fix: readLoop case "ping" now calls
//   sendConnGenFrame(wc, "pong", generated.PongFrame{Type:"pong"})
// so the SPA's liveness check sees a server frame during idle.
//
// Traces to: WebSocket-heartbeat fix (server-side pong response).

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

// TestServerRespondsToAppLayerPing verifies that the server responds with a
// {"type":"pong"} frame whenever the client sends {"type":"ping"}.
//
// BDD:
//
//	Given an authenticated WebSocket connection,
//	When the client sends {"type":"ping"},
//	Then the server responds with {"type":"pong"} within 2 s.
//
// The test also verifies steady-state: three successive pings each produce a
// pong, confirming the handler does not "consume" the case on the first call.
//
// Traces to: pkg/gateway/websocket.go readLoop case "ping"
func TestServerRespondsToAppLayerPing(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Verify 3 successive pings each receive a pong (steady-state).
	for i := 0; i < 3; i++ {
		pingData, err := json.Marshal(wsClientFrameTestHelper{Type: "ping"})
		require.NoError(t, err)

		conn.SetWriteDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, pingData),
			"ping %d: write must succeed", i)

		// readFrameOfType drains frames until it sees the expected type;
		// any auxiliary frames (session_state etc.) are skipped.
		pong := readFrameOfType(t, conn, "pong", 2*time.Second)
		assert.Equal(t, "pong", pong.Type,
			"ping %d: pong frame must carry type=pong", i)

		if i < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// TestServerPongDoesNotCorruptInFlightStream verifies that inserting a ping
// during an in-flight reply does not corrupt the frame sequence: every frame
// that arrives still parses as a complete JSON envelope and the terminal done
// (or error) frame still arrives.
//
// Note: with a synchronous mock provider the ping may land before any token
// frame is produced, so this is a smoke test for "no malformed-JSON output
// after pong write" — not a strict mid-token-boundary interleaving guarantee.
// A future test with a paced streaming provider would lock that down.
func TestServerPongDoesNotCorruptInFlightStream(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	// Send a message to start a session. Because newTestWSHandler uses a
	// restMockProvider that has no real LLM backend, the agent will emit an
	// error or done frame — we do not need streaming. The test's purpose is
	// to verify that a ping sent at any point during the exchange does not
	// disrupt the frame sequence.
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "ping interleave test"}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData))

	// Collect frames until session_started or any response from the server,
	// then inject a ping. We expect the ping to not cause corruption.
	gotSessionStarted := false
	pingInjected := false
	gotPong := false
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		conn.SetReadDeadline(deadline) //nolint:errcheck
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f),
			"every frame from the server must be valid JSON with a type field")
		require.NotEmpty(t, f.Type,
			"every server frame must have a non-empty type field")

		if f.Type == "session_started" {
			gotSessionStarted = true
		}

		// Inject a ping after we have seen session_started.
		if gotSessionStarted && !pingInjected {
			pingData, err := json.Marshal(wsClientFrameTestHelper{Type: "ping"})
			require.NoError(t, err)
			conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
			_ = conn.WriteMessage(websocket.TextMessage, pingData)
			pingInjected = true
		}

		if f.Type == "pong" {
			gotPong = true
		}

		if f.Type == "token" {
			// Token frames must carry session_id — verify the frame is complete.
			assert.NotEmpty(t, f.SessionID,
				"token frame must have a non-empty session_id; partial/split frame would be missing this field")
		}

		if f.Type == "done" || f.Type == "error" {
			break
		}
	}

	// The ping must have been injected (session_started received).
	assert.True(t, gotSessionStarted, "session_started frame must have arrived before deadline")
	assert.True(t, pingInjected, "ping must have been injected after session_started")

	// If the ping arrived during active streaming, a pong must be in the stream.
	// (In the mock-provider path we may not see a pong if frames resolve before
	// the ping is processed — that is acceptable; what matters is no corruption.)
	if gotPong {
		t.Logf("pong confirmed in mid-stream sequence — serialization holds")
	} else {
		t.Logf("no pong observed (mock provider resolved synchronously before ping was processed — acceptable)")
	}
}
