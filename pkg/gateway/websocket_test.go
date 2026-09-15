package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			// An explicitly registered agent. handleChatMessage REFUSES a chat
			// frame it cannot resolve an agent for rather than publishing a
			// message owned by nobody, and there is no implicit "main" sentinel
			// to resolve to any more (ADR-064) — with an empty roster nothing
			// reaches the bus and these tests wait out their timeout.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
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

// busDeliveryTimeout bounds how long a test waits for the WS handler to publish
// an inbound message to the bus.
//
// This is a FAILSAFE, not a latency assertion. These tests assert that a frame
// IS delivered to the bus, never that it is delivered quickly. The publish is
// preceded by session minting, which performs several fsync-backed atomic
// writes (fileutil.WriteFileAtomic calls tmpFile.Sync()), so the wall-clock
// cost of the path tracks whatever else is hammering the disk and scheduler at
// the time — it is not a property of the code under test.
//
// The previous 3s budget turned every one of these into a wall-clock timing
// test. It fired on the CI worker's contended full-suite run (pkg/gateway,
// 2026-07-28) for TestWSHandlerMessagePublishedToBus and
// TestWSHandlerMessageMediaThreadedToBus even though the handler had published
// normally: no "ws: failed to publish message", no "ws: could not create
// session" and no "ws: auth read failed" appeared anywhere in that run's log,
// and every early return in handleChatMessage logs before returning.
const busDeliveryTimeout = 30 * time.Second

// awaitInboundMessage waits for the next message the WS handler publishes to
// msgBus and returns it, failing the test if none ever arrives. what names the
// expectation so a genuine non-delivery says which one broke.
//
// It reads the bus DIRECTLY rather than through a relay goroutine. The inbound
// channel is buffered (bus.defaultBusBufferSize == 64) and these tests are its
// only consumer — the agent loop is constructed but never Start()ed — so a
// message published before this call is already waiting in the buffer. There is
// nothing to miss by subscribing "late", which is why no goroutine is needed.
//
// The relay-goroutine form this replaces was actively harmful. It armed a
// SECOND timer that started BEFORE the frame was written, while the caller's
// timer started after, so the relay always expired first. On a slow box the
// relay could give up and exit while the message was still in flight; the
// caller then reported "message was not published to bus" for a message that
// HAD been published, blaming the production publish path for what was purely
// a harness artifact. Worse, whenever both of the relay's select cases were
// ready at once Go chose between them at random, so it could discard a message
// that had arrived comfortably within budget.
func awaitInboundMessage(t *testing.T, msgBus *bus.MessageBus, what string) bus.InboundMessage {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		return msg
	case <-time.After(busDeliveryTimeout):
		t.Fatalf("%s: no message reached the bus within %s", what, busDeliveryTimeout)
		return bus.InboundMessage{} // unreachable: t.Fatalf ends the goroutine.
	}
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
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.TextMessage, data)
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
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness

	// Send valid auth frame.
	authFrame := wsClientFrameTestHelper{Type: "auth", Token: testToken}
	authData, err := json.Marshal(authFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, authData))

	// After valid auth, send a message — must succeed.
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "hello after auth"}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.TextMessage, msgData)
	assert.NoError(t, err, "message send must succeed after valid auth")
}

// TestWSHandlerInvalidAuth verifies that with OMNIPUS_BEARER_TOKEN set,
// sending the wrong token results in an error frame and connection close.
// BDD: Given OMNIPUS_BEARER_TOKEN is "secret",
// When the client sends {"type":"auth","token":"wrong"},
// Then the server sends an error frame and closes the connection.
// Traces to: wave5b-system-agent-spec.md — Scenario: WebSocket invalid auth (E5)
//
// GAP 1 (D5 test-coverage gate): asserts EXACT equality against the shared
// wsAuthErrInvalidToken constant (websocket.go), not a loose substring. Prior
// to this fix the assertion checked for "unauthorized" — a string the D5 fix
// (commit b764a484) removed from the wire in favor of a human-readable
// message, which had gone undetected because nobody re-ran this test after
// the copy change. Comparing to the constant itself means a future edit that
// changes the copy in websocket.go but forgets the identical browser_ws.go
// mirror call site (or vice versa) fails exactly one of the two tests that
// reference it — see TestBrowserWS_Auth_InvalidToken_ClosesWithPolicyViolation
// in browser_ws_test.go for the mirrored assertion.
func TestWSHandlerInvalidAuth(t *testing.T) {
	const testToken = "ws-correct-token"
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	t.Setenv("OMNIPUS_BEARER_TOKEN", testToken)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness

	// Send wrong token.
	authFrame := wsClientFrameTestHelper{Type: "auth", Token: "wrong-token"}
	authData, err := json.Marshal(authFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, authData))

	// Read the error frame sent before the server closes.
	_, resp, err := conn.ReadMessage()
	require.NoError(t, err, "must receive error frame before connection closes")
	var frame replayFrameDecoder
	require.NoError(t, json.Unmarshal(resp, &frame))
	assert.Equal(t, "error", frame.Type)
	assert.Equal(t, wsAuthErrInvalidToken, frame.Message,
		"chat WS invalid-token error must carry the shared wsAuthErrInvalidToken constant verbatim")

	// After error frame, connection must be closed.
	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "connection must be closed after invalid auth")
}

// TestWSHandlerAuth_BadFirstFrame_UsesSharedConstant verifies that a
// non-"auth" first frame (no session cookie present, so the frame path is
// reached) is rejected with the shared wsAuthErrBadFirstFrame constant, and
// that the connection is unusable afterward.
// BDD: Given a freshly dialed chat WS connection with no cookie,
// When the client's first frame is {"type":"message",...} (not "auth"),
// Then the server sends {"type":"error","message":wsAuthErrBadFirstFrame}
// and the connection cannot be used for further frames.
// Traces to: GAP 1 (D5 test-coverage gate) — websocket.go:770-779.
func TestWSHandlerAuth_BadFirstFrame_UsesSharedConstant(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness

	// First frame is a "message" frame, not the required "auth" envelope.
	badFirst := wsClientFrameTestHelper{Type: "message", Content: "no auth frame sent"}
	badData, err := json.Marshal(badFirst)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, badData))

	_, resp, err := conn.ReadMessage()
	require.NoError(t, err, "must receive error frame for a non-auth first frame")
	var frame replayFrameDecoder
	require.NoError(t, json.Unmarshal(resp, &frame))
	assert.Equal(t, "error", frame.Type)
	assert.Equal(t, wsAuthErrBadFirstFrame, frame.Message,
		"chat WS bad-first-frame error must carry the shared wsAuthErrBadFirstFrame constant verbatim")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "connection must be closed after a bad first frame")
}

// TestWSHandlerAuth_NoUsersConfigured_UsesSharedConstant verifies that when
// no accounts, no CLI token, and no OMNIPUS_BEARER_TOKEN are configured, and
// dev_mode_bypass is explicitly off, the handshake is rejected with the
// shared wsAuthErrNoUsers constant rather than silently admitted.
// BDD: Given Gateway.Users is empty, Gateway.CLIToken is nil,
// OMNIPUS_BEARER_TOKEN is unset, and dev_mode_bypass=false,
// When the client sends any {"type":"auth","token":"..."} frame,
// Then the server sends {"type":"error","message":wsAuthErrNoUsers} and
// closes the connection — fail closed, not fail open.
// Traces to: GAP 1 (D5 test-coverage gate) — websocket.go:813-831. No existing
// test previously exercised this branch at all (verified: only the
// DevModeBypass=true / no-auth path was covered by
// TestWSHandlerNoAuthRequired).
func TestWSHandlerAuth_NoUsersConfigured_UsesSharedConstant(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 8080,
			DevModeBypass: false, // explicit: fail closed, not the dev-mode fallback.
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness

	authFrame := wsClientFrameTestHelper{Type: "auth", Token: "any-token-nothing-is-configured"}
	authData, err := json.Marshal(authFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, authData))

	_, resp, err := conn.ReadMessage()
	require.NoError(t, err, "must receive error frame before connection closes")
	var frame replayFrameDecoder
	require.NoError(t, json.Unmarshal(resp, &frame))
	assert.Equal(t, "error", frame.Type)
	assert.Equal(t, wsAuthErrNoUsers, frame.Message,
		"chat WS no-users-configured error must carry the shared wsAuthErrNoUsers constant verbatim")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "connection must be closed when no auth identity is configured at all")
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
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	validFrame := wsClientFrameTestHelper{Type: "message", Content: "still alive"}
	validData, err := json.Marshal(validFrame)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.TextMessage, validData)
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
	cancelData, err := json.Marshal(cancelFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, cancelData))

	// Connection must remain open after cancel.
	conn.SetWriteDeadline(time.Now().Add(1 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness
	pingFrame := wsClientFrameTestHelper{Type: "message", Content: "after cancel"}
	pingData, err := json.Marshal(pingFrame)
	require.NoError(t, err)
	err = conn.WriteMessage(websocket.TextMessage, pingData)
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

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "publish-to-bus-test"}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData))

	msg := awaitInboundMessage(t, msgBus, "message frame must reach the bus")
	assert.Equal(t, "webchat", msg.Channel)
	assert.Equal(t, "publish-to-bus-test", msg.Content)
}

// TestWSHandlerMessageMediaThreadedToBus is the #254 regression test: media://
// refs on a message frame must be threaded into the inbound message's Media
// field so the agent loop resolves them into multimodal content blocks.
// Non-media:// strings must be dropped (never forwarded into LLM content).
func TestWSHandlerMessageMediaThreadedToBus(t *testing.T) {
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	msgFrame := wsClientFrameTestHelper{
		Type:    "message",
		Content: "look at this image",
		// One valid ref, one bogus string that must be dropped.
		Media: []string{"media://abc123", "not-a-media-ref"},
	}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData))

	msg := awaitInboundMessage(t, msgBus, "media-bearing message frame must reach the bus")
	assert.Equal(t, "look at this image", msg.Content)
	// #254: the valid ref is threaded; the bogus one is dropped.
	require.Equal(t, []string{"media://abc123"}, msg.Media,
		"only well-formed media:// refs must reach the agent loop")
}

// TestWSHandlerMessageMediaOnly_NotDropped verifies that a message frame with
// empty Content but a real Media attachment (e.g. an image or file sent with
// no caption) is NOT silently dropped. Regression test for the guard at
// pkg/gateway/websocket.go's message-frame case: `if f.Content == ""` was
// fixed to `if f.Content == "" && len(f.Media) == 0` so a media-only message
// still threads through to the bus/agent loop instead of being discarded.
// BDD: Given an active, authenticated WebSocket connection,
// When the client sends {"type":"message","content":"","media":["media://abc123"]},
// Then the message still reaches the bus with the media ref intact.
func TestWSHandlerMessageMediaOnly_NotDropped(t *testing.T) {
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	msgFrame := wsClientFrameTestHelper{
		Type:    "message",
		Content: "",
		Media:   []string{"media://abc123"},
	}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData))

	msg := awaitInboundMessage(t, msgBus,
		"media-only message must not be silently dropped")
	assert.Equal(t, "", msg.Content)
	require.Equal(t, []string{"media://abc123"}, msg.Media,
		"a media-only message (empty content) must still thread its media ref to the bus")
}

// TestWSHandlerMessageMediaBogusRef_IncreasesDropCount verifies that a
// non-media:// ref in the message frame's "media" array increments the
// inbound dropped counter (observable metric) and does NOT reach the bus.
//
// Traces to: #254 bogus-ref drop (MAJOR); G3 — counter must be asserted.
func TestWSHandlerMessageMediaBogusRef_IncreasesDropCount(t *testing.T) {
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// Send a message with ONLY bogus refs — no valid media:// refs.
	// Also send "media://" (empty ID) which ParseMediaRef rejects.
	const bogusCount = 3
	msgFrame := wsClientFrameTestHelper{
		Type:    "message",
		Content: "text with bad media",
		Media:   []string{"not-a-ref", "http://example.com/file.jpg", "media://"},
	}
	msgData, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msgData))

	// The message reached the bus but Media must be empty. Waiting for the bus
	// message is also what makes the inboundDropped assertion below race-free:
	// handleChatMessage increments that counter for every rejected ref BEFORE
	// it publishes, so once the message is in hand the counter is already
	// final. This replaces a bare time.Sleep(150ms), which asserted nothing and
	// only happened to be long enough.
	msg := awaitInboundMessage(t, msgBus, "message with only bogus media refs must still reach the bus")
	assert.Empty(t, msg.Media, "bogus refs must not reach the bus Media field")

	// Assert the per-connection inboundDropped counter.
	// wsConnChatIDsForTest gives us the chatID of the active connection so we can
	// retrieve the wsConn and load the atomic counter directly.
	chatIDs := wsConnChatIDsForTest(handler)
	require.NotEmpty(t, chatIDs, "must have at least one active connection")

	var totalDropped int32
	for _, cid := range chatIDs {
		if d := wsConnDroppedForTest(handler, cid); d > 0 {
			totalDropped += d
		}
	}
	assert.EqualValues(t, bogusCount, totalDropped,
		"inboundDropped counter must equal the number of bogus refs sent (%d)", bogusCount)
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

	// Send message directly (no auth frame first) — must be accepted.
	frame := wsClientFrameTestHelper{Type: "message", Content: "no-auth-needed"}
	data, err := json.Marshal(frame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	msg := awaitInboundMessage(t, msgBus,
		"message must reach the bus — server may have required auth")
	assert.Equal(t, "no-auth-needed", msg.Content)
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
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) // errcheck rationale (out of errcheck scope; kept as documentation): test websocket conn deadline; a failure here only affects test timing, not correctness

	// Send wrong token in auth frame.
	bad := wsClientFrameTestHelper{Type: "auth", Token: "bad-token"}
	badData, err := json.Marshal(bad)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, badData))

	// Server must send an error frame.
	_, resp, err := conn.ReadMessage()
	require.NoError(t, err)
	var frame replayFrameDecoder
	require.NoError(t, json.Unmarshal(resp, &frame))
	assert.Equal(t, "error", frame.Type, "must receive error frame for bad token")
}
