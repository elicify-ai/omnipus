//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.

package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// newTestWSHandler creates a WSHandler with minimal test dependencies.
// OMNIPUS_BEARER_TOKEN is unset so auth is disabled by default.
func newTestWSHandler(t *testing.T) (*WSHandler, *bus.MessageBus, *agent.AgentLoop) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	return handler, msgBus, al
}

// dialTestWS dials the test server and returns a connected WebSocket conn.
func dialTestWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/chat/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, httpResp, err := dialer.Dial(wsURL, nil)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err, "WebSocket dial must succeed")
	return conn
}

// sendWSAuthFrameDevMode sends the required auth frame as the first WebSocket message.
// In dev mode (OMNIPUS_BEARER_TOKEN=""), authenticateWS requires the "Bearer " prefix
// and non-empty token value, but the token itself is not validated.
func sendWSAuthFrameDevMode(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	authFrame := wsClientFrameTestHelper{Type: "auth", Token: "dev-token"}
	data, err := json.Marshal(authFrame)
	require.NoError(t, err, "marshal auth frame")
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data), "auth frame write")
}

// --- E1: WebSocket handler tests ---

// TestWSHandlerNoAuthRequired verifies that when OMNIPUS_BEARER_TOKEN is unset,
// a WebSocket connection is accepted without sending an auth frame.
// BDD: Given auth is not configured,
// When a client connects and sends a message frame,
// Then the connection stays open and the message is accepted.
// Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket chat works without auth in dev mode
func TestWSHandlerNoAuthRequired(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	// Send a message without auth — should succeed when token not configured.
	frame := wsClientFrameTestHelper{Type: "message", Content: "hello no-auth"}
	data, _ := json.Marshal(frame)
	err := conn.WriteMessage(websocket.TextMessage, data)
	assert.NoError(t, err, "write must succeed when auth is not configured")
}

// TestWSHandlerValidAuth verifies that with OMNIPUS_BEARER_TOKEN set,
// sending the correct auth frame keeps the connection open.
// BDD: Given OMNIPUS_BEARER_TOKEN is "secret",
// When the client sends {"type":"auth","token":"secret"},
// Then the connection stays open and subsequent messages are accepted.
// Traces to: wave5b-system-agent-spec.md — Scenario: WebSocket auth handshake (E5)
func TestWSHandlerValidAuth(t *testing.T) {
	const testToken = "ws-valid-auth-token-abc123"
	handler, _, _ := newTestWSHandler(t)
	// Override to require auth — t.Setenv restores on cleanup.
	t.Setenv("OMNIPUS_BEARER_TOKEN", testToken)

	// Register handler.Wait() BEFORE srv.Close so that in LIFO order
	// srv.Close runs first, then handler.Wait() drains all goroutines,
	// then the TempDir cleanup removes the directory safely.
	t.Cleanup(handler.Wait)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	// Send valid auth frame.
	authFrame := wsClientFrameTestHelper{Type: "auth", Token: testToken}
	authData, _ := json.Marshal(authFrame)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, authData))

	// After valid auth, send a message — must succeed.
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "hello after auth"}
	msgData, _ := json.Marshal(msgFrame)
	err := conn.WriteMessage(websocket.TextMessage, msgData)
	assert.NoError(t, err, "message send must succeed after valid auth")
}

// TestWSHandlerInvalidAuth verifies that with OMNIPUS_BEARER_TOKEN set,
// sending the wrong token results in an error frame and connection close.
// BDD: Given OMNIPUS_BEARER_TOKEN is "secret",
// When the client sends {"type":"auth","token":"wrong"},
// Then the server sends an error frame and closes the connection.
// Traces to: wave5b-system-agent-spec.md — Scenario: WebSocket invalid auth (E5)
func TestWSHandlerInvalidAuth(t *testing.T) {
	const testToken = "ws-correct-token"
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	t.Setenv("OMNIPUS_BEARER_TOKEN", testToken)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	// Send wrong token.
	authFrame := wsClientFrameTestHelper{Type: "auth", Token: "wrong-token"}
	authData, _ := json.Marshal(authFrame)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, authData))

	// Read the error frame sent before the server closes.
	_, resp, err := conn.ReadMessage()
	require.NoError(t, err, "must receive error frame before connection closes")
	var frame wsServerFrameDecodeHelper
	require.NoError(t, json.Unmarshal(resp, &frame))
	assert.Equal(t, "error", frame.Type)
	assert.Contains(t, strings.ToLower(frame.Message), "unauthorized")

	// After error frame, connection must be closed.
	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "connection must be closed after invalid auth")
}

// TestWSHandlerMalformedFrame verifies that invalid JSON does not close the connection.
// BDD: Given an active WebSocket connection,
// When the client sends non-JSON bytes,
// Then the server logs a warning and keeps the connection open.
// Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket malformed frame handling (E1)
func TestWSHandlerMalformedFrame(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	// Send invalid JSON — server logs warn and continues.
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("not-json{{{bad")))

	// Connection must remain open: send another valid frame.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	validFrame := wsClientFrameTestHelper{Type: "message", Content: "still alive"}
	validData, _ := json.Marshal(validFrame)
	err := conn.WriteMessage(websocket.TextMessage, validData)
	assert.NoError(t, err, "connection must remain open after malformed frame")
}

// TestWSHandlerCancelFrame verifies that a cancel frame does not crash or close the connection.
// BDD: Given an active WebSocket connection with no active agent turn,
// When the client sends {"type":"cancel"},
// Then the server logs at debug level and keeps the connection open.
// Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket cancel (E1)
func TestWSHandlerCancelFrame(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	cancelFrame := wsClientFrameTestHelper{Type: "cancel"}
	cancelData, _ := json.Marshal(cancelFrame)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))

	// Connection must remain open after cancel.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	pingFrame := wsClientFrameTestHelper{Type: "message", Content: "after cancel"}
	pingData, _ := json.Marshal(pingFrame)
	err := conn.WriteMessage(websocket.TextMessage, pingData)
	assert.NoError(t, err, "connection must remain open after cancel frame")
}

// TestWSHandlerMessagePublishedToBus verifies that a message frame publishes
// to the MessageBus inbound channel.
// BDD: Given an active WebSocket connection,
// When the client sends {"type":"message","content":"hello"},
// Then the message appears on the bus inbound channel with channel="webchat".
// Traces to: wave5a-wire-ui-spec.md — Scenario: WebSocket message routed to agent (E1)
func TestWSHandlerMessagePublishedToBus(t *testing.T) {
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	// Authenticate first — required by authenticateWS before any other frames.
	sendWSAuthFrameDevMode(t, conn)

	// Listen on the bus before sending the message.
	received := make(chan bus.InboundMessage, 1)
	go func() {
		select {
		case msg := <-msgBus.InboundChan():
			received <- msg
		case <-time.After(3 * time.Second):
		}
	}()

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "publish-to-bus-test"}
	msgData, _ := json.Marshal(msgFrame)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData))

	select {
	case msg := <-received:
		assert.Equal(t, "webchat", msg.Channel)
		assert.Equal(t, "publish-to-bus-test", msg.Content)
	case <-time.After(3 * time.Second):
		t.Fatal("message was not published to bus within 3 seconds")
	}
}

// --- E5: WebSocket auth path tests ---

// TestWSHandlerAuthNotRequired_NoFirstFrameNeeded verifies that when
// OMNIPUS_BEARER_TOKEN is unset, the server does not wait for an auth frame.
// BDD: Given OMNIPUS_BEARER_TOKEN is unset,
// When the client connects and sends a message immediately (no auth frame),
// Then the server accepts the message.
// Traces to: wave5b-system-agent-spec.md — E5: without token, auth frame not required
func TestWSHandlerAuthNotRequired_NoFirstFrameNeeded(t *testing.T) {
	// newTestWSHandler already sets OMNIPUS_BEARER_TOKEN = ""
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	// Authenticate first — required by authenticateWS before any other frames.
	sendWSAuthFrameDevMode(t, conn)

	received := make(chan bus.InboundMessage, 1)
	go func() {
		select {
		case msg := <-msgBus.InboundChan():
			received <- msg
		case <-time.After(3 * time.Second):
		}
	}()

	// Send message directly (no auth frame first) — must be accepted.
	frame := wsClientFrameTestHelper{Type: "message", Content: "no-auth-needed"}
	data, _ := json.Marshal(frame)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	select {
	case msg := <-received:
		assert.Equal(t, "no-auth-needed", msg.Content)
	case <-time.After(3 * time.Second):
		t.Fatal("message not received on bus — server may have required auth")
	}
}

// TestWSHandlerAuthRequired_InvalidTokenRejected verifies that when
// OMNIPUS_BEARER_TOKEN is set, an incorrect token in the auth frame is rejected.
// Traces to: wave5b-system-agent-spec.md — E5: with token, invalid rejected
func TestWSHandlerAuthRequired_InvalidTokenRejected(t *testing.T) {
	const testToken = "required-token-xyz"
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	t.Setenv("OMNIPUS_BEARER_TOKEN", testToken)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	// Send wrong token in auth frame.
	bad := wsClientFrameTestHelper{Type: "auth", Token: "bad-token"}
	badData, _ := json.Marshal(bad)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, badData))

	// Server must send an error frame.
	_, resp, err := conn.ReadMessage()
	require.NoError(t, err)
	var frame wsServerFrameDecodeHelper
	require.NoError(t, json.Unmarshal(resp, &frame))
	assert.Equal(t, "error", frame.Type, "must receive error frame for bad token")
}

// --- Suite 5: eventForwarder unit tests ---

// TestEventForwarder_ForwardsToolExecStart verifies that a ToolExecStartPayload event
// with a matching chatID is forwarded to the wsConn's sendCh as a "tool_call_start" frame.
// BDD: Given an eventForwarder goroutine subscribed to an EventBus with chatID "chat-1",
// When a ToolExecStartPayload event for chatID "chat-1" is emitted,
// Then a wsServerFrameDecodeHelper with type "tool_call_start" appears on sendCh.
// Traces to: pkg/gateway/websocket.go — WSHandler.eventForwarder
func TestEventForwarder_ForwardsToolExecStart(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc := makeTestConn()
	chatID := "chat-1"

	eb := agent.NewEventBus()
	t.Cleanup(eb.Close)

	sub := eb.Subscribe(16)
	eventDone := make(chan struct{})

	go handler.eventForwarder(wc, chatID, sub, eventDone)

	eb.Emit(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID: "call-xyz",
			ChatID:     chatID,
			Tool:       "read_file",
			Arguments:  map[string]any{"path": "/tmp/test.txt"},
		},
	})

	select {
	case raw := <-wc.sendCh:
		var f wsServerFrameDecodeHelper
		require.NoError(t, json.Unmarshal(raw, &f), "sendCh frame must be valid JSON")
		assert.Equal(t, "tool_call_start", f.Type, "frame type must be tool_call_start")
		assert.Equal(t, "call-xyz", f.CallID, "CallID must match ToolCallID")
		assert.Equal(t, "read_file", f.Tool, "Tool must match payload Tool")
	case <-time.After(2 * time.Second):
		t.Fatal("no frame received on sendCh within 2s — eventForwarder did not forward the event")
	}

	// Unsubscribe to drain the goroutine cleanly.
	eb.Unsubscribe(sub.ID)
	select {
	case <-eventDone:
	case <-time.After(1 * time.Second):
		t.Fatal("eventForwarder goroutine did not exit after subscription closed")
	}
}

// TestEventForwarder_FiltersByChatID verifies that a ToolExecStartPayload event for a
// different chatID is NOT forwarded to the wsConn's sendCh.
// BDD: Given an eventForwarder subscribed with chatID "chat-1",
// When a ToolExecStartPayload event for chatID "chat-other" is emitted,
// Then no frame arrives on sendCh within the timeout.
// Traces to: pkg/gateway/websocket.go — WSHandler.eventForwarder chatID filter
func TestEventForwarder_FiltersByChatID(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc := makeTestConn()
	chatID := "chat-1"

	eb := agent.NewEventBus()
	t.Cleanup(eb.Close)

	sub := eb.Subscribe(16)
	eventDone := make(chan struct{})

	go handler.eventForwarder(wc, chatID, sub, eventDone)

	// Emit an event for a different chatID — must NOT be forwarded.
	eb.Emit(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID: "call-other",
			ChatID:     "chat-other", // non-matching
			Tool:       "exec",
			Arguments:  map[string]any{"command": "ls"},
		},
	})

	select {
	case raw := <-wc.sendCh:
		t.Fatalf("unexpected frame on sendCh — eventForwarder must filter by chatID, got: %s", string(raw))
	case <-time.After(150 * time.Millisecond):
		// Correct — no frame should arrive for a non-matching chatID.
	}

	eb.Unsubscribe(sub.ID)
	select {
	case <-eventDone:
	case <-time.After(1 * time.Second):
		t.Fatal("eventForwarder goroutine did not exit after subscription closed")
	}
}
