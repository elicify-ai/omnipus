package gateway

// ws_server_capabilities_test.go — RED (#1428 step 2 option C, qa-lead):
// server_capabilities is the FIRST application frame on every authenticated
// WebSocket connection (S-11 / R2-M9, spec §4.3.6), and an authentication
// refusal still sends error-then-1008 with NO capabilities frame (SV-20).
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence):
//   - SPEC-C-server-binds-send-to-chat-r8.md §4.3.6: "Enqueued on `wc` in
//     ServeHTTP immediately after `authenticateWS` returns true and before
//     `h.sessions[chatID] = wc` (F-aa), so no event forwarder, broadcast,
//     attach replay, `session_state` or pong can precede it. Sent to browser
//     and non-browser connections. Authentication failures send nothing new."
//   - §6.1 SV-15: "Fresh connection's first frame is `server_capabilities`,
//     also with an attach replay pending and with a broadcast fired by
//     another connection during admission (barrier in the test between
//     authentication and publication)."
//   - §6.1 SV-20: "Bad credential → the connection receives `error` then
//     close 1008, and no `server_capabilities`."
//   - Contract commit 32802e2a6, ServerCapabilitiesFrame.yaml: required
//     {type: "server_capabilities", send_origin: true}, no other properties.
//
// RED STATE (F-aa as-is): the connection's first frame today is
// `session_state` (websocket.go::ServeHTTP's emitSessionState one-shot) and
// no server_capabilities frame exists anywhere on the wire — every SV-15 row
// fails at its first-frame assertion naming what actually arrived first.
// SV-20 is the paired guard: the refusal sequence already exists
// (classifyWSAuthRefusal → refuseWSAuth → writeCloseAuthFailedWithReason)
// and must keep its shape when capabilities land; it dies at GREEN only if a
// capabilities frame is ever allowed to precede or replace the refusal.
//
// The read deadlines below are FAILSAFEs (house rule: never a latency
// assertion), sized to survive disk hiccups; ordering, not timing, is the
// correctness claim.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// sv15ReadDeadline is the read failsafe shared by every row.
const sv15ReadDeadline = 15 * time.Second

// capabilitiesTestUser is the single configured human account the browser
// rows authenticate as.
const capabilitiesTestUser = "sv15-browser-user"

// newCapabilitiesHandler builds the production handler for the NON-BROWSER
// rows: dev-mode frame auth, no accounts configured — the install shape every
// existing frame-auth test uses (bearerAccountsConfigured must be false for
// the dev bypass branch to admit the auth frame). The browser rows use
// newSV15BrowserServer instead, whose account hash and cookie share one mint.
func newCapabilitiesHandler(t *testing.T) (*WSHandler, *bus.MessageBus) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	return handler, msgBus
}

// dialBrowserWS dials the chat WS endpoint as a BROWSER connection (spec §1):
// Origin header present + valid omnipus-session cookie on the upgrade
// request, no auth frame afterwards.
func dialBrowserWS(t *testing.T, srv *httptest.Server, cookieValue string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/chat/ws"
	header := http.Header{}
	header.Set("Origin", srv.URL) // loopback origin: admitted by wsCheckOrigin
	header.Set("Cookie", middleware.SessionCookieName+"="+cookieValue)
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, httpResp, err := dialer.Dial(wsURL, header)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err, "SETUP: browser-kind dial must upgrade")
	return conn
}

// readFirstApplicationFrame reads exactly one text frame and returns its
// decoded type plus the raw payload. The first frame is the whole claim.
func readFirstApplicationFrame(t *testing.T, conn *websocket.Conn) (string, []byte) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(sv15ReadDeadline)),
		"SETUP: arm the read failsafe")
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err, "the connection must deliver a first application frame")
	var peek wsTypeOnly
	require.NoError(t, json.Unmarshal(raw, &peek), "the first frame must be valid JSON")
	return peek.Type, raw
}

// requireServerCapabilitiesFirst asserts the decoded first frame IS the
// capabilities frame with its contract shape (ServerCapabilitiesFrame.yaml:
// exactly {type, send_origin:true}).
func requireServerCapabilitiesFirst(t *testing.T, raw []byte, what string) {
	t.Helper()
	var frame generated.ServerCapabilitiesFrame
	require.NoError(t, json.Unmarshal(raw, &frame),
		"%s: the first application frame must decode as ServerCapabilitiesFrame", what)
	require.Equal(t, "server_capabilities", frame.Type,
		"%s: SV-15 violated: the first application frame must be server_capabilities", what)
	require.True(t, frame.SendOrigin,
		"%s: server_capabilities.send_origin must be true (the contract const)", what)
}

// TestServerCapabilities_SV15_FirstFrameOnEveryConnection drives SV-15:
// the first application frame on a fresh connection is server_capabilities,
// on both connection kinds, with an attach replay pending, and under
// broadcast pressure from another connection during admission.
func TestServerCapabilities_SV15_FirstFrameOnEveryConnection(t *testing.T) {
	t.Run("non-browser connection", func(t *testing.T) {
		handler, _ := newCapabilitiesHandler(t)
		t.Cleanup(handler.Wait)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		conn := dialTestWS(t, srv)
		t.Cleanup(func() { _ = conn.Close() })
		sendWSAuthFrameDevMode(t, conn)

		frameType, raw := readFirstApplicationFrame(t, conn)
		require.Equal(t, "server_capabilities", frameType,
			"SV-15 violated (F-aa as-is): the non-browser connection's first frame is %q "+
				"(raw: %s), want server_capabilities as the very first application frame", frameType, raw)
		requireServerCapabilitiesFirst(t, raw, "non-browser")
	})

	t.Run("browser cookie connection", func(t *testing.T) {
		// The account hash and the dialing cookie must come from the same
		// mint, so this row builds its own server (newSV15BrowserServer).
		srv2, cookie := newSV15BrowserServer(t)
		conn := dialBrowserWS(t, srv2, cookie)
		t.Cleanup(func() { _ = conn.Close() })

		frameType, raw := readFirstApplicationFrame(t, conn)
		require.Equal(t, "server_capabilities", frameType,
			"SV-15 violated (F-aa as-is): the browser connection's first frame is %q "+
				"(raw: %s), want server_capabilities as the very first application frame", frameType, raw)
		requireServerCapabilitiesFirst(t, raw, "browser")
	})

	t.Run("attach replay pending", func(t *testing.T) {
		handler, _ := newCapabilitiesHandler(t)
		t.Cleanup(handler.Wait)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		// A session with history exists BEFORE the connecting client dials:
		// its attach replay is pending from the client's perspective the
		// moment it authenticates.
		_, sidFrame := issue1090SendSessionless(t, handler, "sv15-owner", "sv15-attach-original",
			"SV-15 attach row: pre-existing session with history", "sv15-attach-id-1")
		sid := issue1090StartedSessionID(t, sidFrame)
		require.NotEmpty(t, sid, "SETUP: the pre-existing session must mint")

		conn := dialTestWS(t, srv)
		t.Cleanup(func() { _ = conn.Close() })
		sendWSAuthFrameDevMode(t, conn)

		frameType, raw := readFirstApplicationFrame(t, conn)
		require.Equal(t, "server_capabilities", frameType,
			"SV-15 violated (attach-pending variant): with an attach replay pending, the first "+
				"frame is %q (raw: %s), want server_capabilities before any replay or session_state", frameType, raw)
		requireServerCapabilitiesFirst(t, raw, "attach-pending")

		// The attach itself still answers after the capabilities frame: bind
		// and read until the catch-up completes — ordering, not timing.
		require.NoError(t, conn.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid}))
		sawCapabilities := false
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(sv15ReadDeadline)))
		for {
			_, rawFrame, readErr := conn.ReadMessage()
			require.NoError(t, readErr, "the attach answer must arrive")
			var peek wsTypeOnly
			require.NoError(t, json.Unmarshal(rawFrame, &peek))
			if peek.Type == "server_capabilities" {
				sawCapabilities = true
			}
			if peek.Type == "catch_up_complete" || peek.Type == "replay_done" || peek.Type == "session_state" {
				require.True(t, sawCapabilities || frameType == "server_capabilities",
					"SV-15: every attach-answer frame must follow the capabilities frame")
				if peek.Type == "catch_up_complete" {
					break
				}
			}
		}
	})

	t.Run("broadcast pressure during admission", func(t *testing.T) {
		handler, _ := newCapabilitiesHandler(t)
		t.Cleanup(handler.Wait)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		// A second connection is already chatting while the fresh connection
		// goes through admission, so events are firing while it authenticates.
		// SV-15's deterministic form freezes admission with a barrier between
		// authenticateWS and publication — that barrier is a GREEN-side
		// deliverable (the production enqueue point does not expose a hook
		// yet); this row drives the same wire with live pressure and asserts
		// the same first-frame oracle. The RED failure below is deterministic
		// either way (today's first frame is session_state, unconditionally).
		_, sidFrame := issue1090SendSessionless(t, handler, "sv15-owner", "sv15-pressure-original",
			"SV-15 pressure row: session the broadcaster chats into", "sv15-pressure-id-1")
		sid := issue1090StartedSessionID(t, sidFrame)
		require.NotEmpty(t, sid, "SETUP: the broadcaster's session must mint")

		broadcaster := dialTestWS(t, srv)
		t.Cleanup(func() { _ = broadcaster.Close() })
		sendWSAuthFrameDevMode(t, broadcaster)
		stopBroadcasting := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; ; i++ {
				select {
				case <-stopBroadcasting:
					return
				default:
				}
				frame := generated.MessageFrame{
					Type:      "message",
					AgentId:   strPtr("mia"),
					SessionId: &sid,
					Content:   "SV-15 admission pressure tick",
				}
				raw, err := json.Marshal(frame)
				if err != nil {
					return
				}
				_ = broadcaster.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if err := broadcaster.WriteMessage(websocket.TextMessage, raw); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
		t.Cleanup(func() { close(stopBroadcasting); <-done })

		conn := dialTestWS(t, srv)
		t.Cleanup(func() { _ = conn.Close() })
		sendWSAuthFrameDevMode(t, conn)

		frameType, raw := readFirstApplicationFrame(t, conn)
		require.Equal(t, "server_capabilities", frameType,
			"SV-15 violated (broadcast-pressure variant): with another connection broadcasting during "+
				"admission, the fresh connection's first frame is %q (raw: %s), want server_capabilities — "+
				"no event forwarder, broadcast or session_state may precede it", frameType, raw)
		requireServerCapabilitiesFirst(t, raw, "broadcast-pressure")
	})
}

// newSV15BrowserServer builds a handler+server whose account hash and cookie
// come from the SAME mint, so the browser row authenticates for real.
func newSV15BrowserServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cookie, hash, err := middleware.MintSessionToken()
	require.NoError(t, err, "SETUP: mint cookie + hash pair")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 8080, DevModeBypass: true,
			Users: []config.UserConfig{
				{Username: capabilitiesTestUser, SessionTokenHash: config.BcryptHash(hash)},
			},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, cookie
}

// TestServerCapabilities_SV20_AuthRejectionErrorThenClose1008 is SV-20's
// guard: a bad credential receives the error frame FIRST, then the 1008
// policy-violation close, and NEVER a server_capabilities frame (§4.3.6:
// "Authentication failures send nothing new"). This sequence already exists
// (classifyWSAuthRefusal → refuseWSAuth); the test pins it so a GREEN that
// enqueues capabilities on the refusal path dies here.
func TestServerCapabilities_SV20_AuthRejectionErrorThenClose1008(t *testing.T) {
	srv, _ := newSV15BrowserServer(t)

	// A browser-kind handshake carrying a cookie that matches no configured
	// account: classifyWSAuthRefusal's stale_session_cookie branch refuses
	// immediately (a browser cannot follow up with an auth frame).
	staleCookie := "sv20-cookie-that-matches-no-configured-account"
	conn := dialBrowserWS(t, srv, staleCookie)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(sv15ReadDeadline)),
		"SETUP: arm the read failsafe")

	// 1. The first thing written is the human-readable error frame.
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err, "the refusal must write the error frame before closing")
	var errFrame generated.ErrorFrame
	require.NoError(t, json.Unmarshal(raw, &errFrame), "the refusal's first write must decode as ErrorFrame")
	require.Equal(t, "error", errFrame.Type,
		"SV-20 violated: the bad-credential connection's first write must be an error frame, got %q", errFrame.Type)
	require.NotEmpty(t, errFrame.Message, "SV-20: the error frame must carry the human-readable reason")

	// 2. Then the close, code 1008 (ClosePolicyViolation).
	_, _, err = conn.ReadMessage()
	require.Error(t, err, "after the error frame the server must close")
	require.True(t, websocket.IsCloseError(err, websocket.ClosePolicyViolation),
		"SV-20 violated: the refusal must close with 1008 (ClosePolicyViolation), got %v", err)

	// 3. And nothing capabilities-shaped ever preceded the error frame —
	// already proven by step 1's exact first-frame decode; the explicit
	// negative is stated for the audit trail.
	require.NotContains(t, string(raw), "server_capabilities",
		"SV-20: an authentication refusal must never carry a server_capabilities frame")
}
