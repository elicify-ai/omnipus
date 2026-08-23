// browser_ws_test.go — ADR-038 gateway WebSocket handler tests
// (pkg/gateway/browser_ws.go).
//
// Covers the trust-boundary logic that does NOT require a live Chromium:
// auth handshake parity with the chat WS, the LiveViewEnabled config gate,
// browser_attach against an agent with no registered BrowserManager,
// gateway.validate_inbound schema rejection, and the take-control config
// gate + audit trail.
//
// The take-control tests call BrowserWSHandler.handleControl directly
// (white-box, same package) instead of driving a full attach → control round
// trip over the wire: a REAL browser_control{take} success requires
// handleAttach to have already succeeded, which in turn requires a real,
// running Chromium tab (mgr.Live().Attach resolves the session's tab via
// BrowserManager.Session, launching Chrome if needed — see live.go's attach;
// video for the panel is carried exclusively by WebRTC, ADR-061) — that full
// attach round trip is reserved for the Playwright UAT pass (issue tracked
// separately), not a unit test. handleControl's own logic (the
// TakeControlEnabled gate, the control-lock allow/deny outcome, and the
// audit emission) is pure LiveViewRegistry state — no chromedp call — so
// driving it directly still exercises the real, unmodified method.
//
// Mirrors the existing chat-WS test harness (websocket_test.go,
// wave3_bearer_auth_test.go, cancel_audit_test.go): httptest.Server +
// gorilla/websocket.Dial + a real first-frame auth handshake.
//
// Traces to: docs/internal/architecture/ADR-038-live-interactive-browser-panel.md

package gateway

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browserWSSkipIfNoBrowser mirrors pkg/tools/browser/browser_e2e_test.go's
// skipIfNoBrowser convention (that helper lives in an internal test file of
// a different package, so it isn't importable here — duplicating this tiny
// probe avoids adding a cross-package test dependency for one helper): skip
// in CI unless explicitly opted in, and probe each candidate Chromium/Chrome
// binary with --version rather than trusting exec.LookPath alone, since
// Ubuntu ships a snap stub at /usr/bin/chromium-browser that exits 1 on
// launch. Used by the handful of tests in this file that need a REAL
// attach() round trip against a running Chromium (everything else in this
// file deliberately avoids that — see the file's header comment) — those tests
// SKIP here (this devpod has no working Chromium binary) but run for real
// wherever a working browser is available.
func browserWSSkipIfNoBrowser(t *testing.T) {
	t.Helper()
	if os.Getenv("CI") != "" && os.Getenv("OMNIPUS_BROWSER_E2E") == "" {
		t.Skip("skipping browser E2E test in CI — set OMNIPUS_BROWSER_E2E=1 to enable")
	}
	for _, name := range []string{"chromium-browser", "chromium", "google-chrome", "google-chrome-stable"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		probe := exec.Command(path, "--version")
		if probe.Run() == nil {
			return // real browser found
		}
	}
	t.Skip("skipping browser E2E test: no working Chromium/Chrome binary found in PATH")
}

// browserFrameDecoder is a test-only decode target for server→client
// browser-live frames (browser_status / error). Not a wire-format type —
// mirrors replayFrameDecoder's convention (websocket.go) but adds the
// browser_status-specific "state"/"controller" fields that decoder doesn't
// carry.
type browserFrameDecoder struct { // not-wire-format: decode-only test assertion target, never emitted over the WebSocket connection.
	Type       string `json:"type"`
	State      string `json:"state,omitempty"`
	Message    string `json:"message,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Controller string `json:"controller,omitempty"`
	// ControlledByOther is a pointer (ADR-039 UAT BE-1) so tests can
	// distinguish "field absent" from "field present and false" — the wire
	// field is `omitempty`, so a plain bool would silently conflate the two.
	ControlledByOther *bool `json:"controlled_by_other,omitempty"`
	// ControlOnly (B1, 7-reviewer finding) is set true only on a
	// control-ownership-only broadcast (handleAttach's onControl callback) —
	// never on a real lifecycle frame such as the initial attach response.
	// Pointer for the same "absent vs. false" reason as ControlledByOther.
	ControlOnly *bool `json:"control_only,omitempty"`
}

// newBrowserWSTestHandler builds a BrowserWSHandler backed by a minimal
// AgentLoop, mirroring newTestWSHandler (websocket_test.go) for the chat
// socket. mutate, when non-nil, is applied to the config before the
// AgentLoop is constructed. LiveViewEnabled/TakeControlEnabled default to
// true (the ADR-038 operator defaults) unless mutate overrides them.
func newBrowserWSTestHandler(t *testing.T, mutate func(cfg *config.Config)) (*BrowserWSHandler, *agent.AgentLoop) {
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
			// An explicitly registered agent. Several tests built on this
			// harness call GetRegistry().GetDefaultAgent() and require a
			// non-nil result. That used to be satisfied by the implicit "main"
			// sentinel, which is gone (ADR-064) — GetDefaultAgent now returns
			// nil for an empty roster rather than inventing an agent.
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	cfg.Tools.Browser.LiveViewEnabled = true
	cfg.Tools.Browser.TakeControlEnabled = true
	if mutate != nil {
		mutate(cfg)
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newBrowserWSHandler(al, "")
	return handler, al
}

// dialBrowserTestWS dials /api/v1/browser/ws on srv.
func dialBrowserTestWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/browser/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, httpResp, err := dialer.Dial(wsURL, nil)
	if httpResp != nil {
		httpResp.Body.Close()
	}
	require.NoError(t, err, "browser WebSocket dial must succeed")
	return conn
}

// writeBrowserAuthFrame sends {"type":"auth","token":token} as the
// connection's first frame.
func writeBrowserAuthFrame(t *testing.T, conn *websocket.Conn, token string) {
	t.Helper()
	data, err := json.Marshal(generated.AuthFrame{Type: string(generated.WsFrameTypeAuth), Token: token})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
}

// readBrowserFrame reads one frame off conn and decodes it.
func readBrowserFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) browserFrameDecoder {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout)) //nolint:errcheck
	_, raw, err := conn.ReadMessage()
	require.NoError(t, err, "must read a frame")
	var f browserFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	return f
}

// readBrowserStatusFrame reads frames off conn, skipping any non-
// browser_status frame, until a browser_status frame arrives or timeout
// elapses. Needed for the browserWSSkipIfNoBrowser-gated full-round-trip
// tests below: a browser_tabs frame (and, once the SPA's own WebRTC offer
// lands, browser_webrtc_* frames) can legitimately interleave ahead of the
// browser_status frame a test is actually asserting on, so a naive single
// readBrowserFrame call would flake.
func readBrowserStatusFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) browserFrameDecoder {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("no browser_status frame received within timeout")
		}
		f := readBrowserFrame(t, conn, remaining)
		if f.Type == "browser_status" {
			return f
		}
	}
}

// assertBrowserConnProceeds sends a browser_attach for an agent that is
// guaranteed not to have a registered BrowserManager and asserts a clean
// browser_status(error) response comes back — proof the connection survived
// authentication and readLoop is dispatching frames normally (as opposed to
// having been closed by an auth failure).
func assertBrowserConnProceeds(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	attach := generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   "connection-proceeds-probe-agent",
		SessionId: "probe-session",
	}
	data, err := json.Marshal(attach)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data),
		"write must succeed on a live, authenticated connection")

	resp := readBrowserFrame(t, conn, 3*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State, "probe agent has no registered manager, so attach must fail cleanly")
}

// ---------------------------------------------------------------------------
// Auth handshake
// ---------------------------------------------------------------------------

// TestBrowserWS_Auth_DevModeBypass_ConnectionProceeds verifies that with no
// Users/CLIToken/env token configured and DevModeBypass=true, any auth frame
// is accepted and the connection proceeds to normal frame dispatch.
// BDD: Given no accounts are configured and dev_mode_bypass=true,
// When the client sends {"type":"auth","token":"dev-token"},
// Then the connection is accepted and subsequent frames are processed.
func TestBrowserWS_Auth_DevModeBypass_ConnectionProceeds(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	sendWSAuthFrameDevMode(t, conn)
	assertBrowserConnProceeds(t, conn)
}

// TestBrowserWS_Auth_ValidUserToken_ConnectionProceeds verifies that a bearer
// token matching a Gateway.Users bcrypt hash authenticates the browser-live
// socket, mirroring authenticateWS's identity resolution.
// BDD: Given Gateway.Users holds one account with a bcrypt-hashed token,
// When the client authenticates with that account's raw token,
// Then the connection is accepted and subsequent frames are processed.
func TestBrowserWS_Auth_ValidUserToken_ConnectionProceeds(t *testing.T) {
	token := "omnipus_" + strings.Repeat("7", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	require.NoError(t, err)

	handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.Users = []config.UserConfig{
			{Username: "browser-ws-user", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash)}}},
		}
	})
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	writeBrowserAuthFrame(t, conn, token)
	assertBrowserConnProceeds(t, conn)
}

// TestBrowserWS_Auth_ValidCLIToken_ConnectionProceeds verifies that a bearer
// token matching Gateway.CLIToken (the synthetic "cli" identity, checked
// after the Users loop) also authenticates the browser-live socket.
// BDD: Given Gateway.CLIToken holds a bcrypt-hashed token and no Users exist,
// When the client authenticates with the CLI token,
// Then the connection is accepted and subsequent frames are processed.
func TestBrowserWS_Auth_ValidCLIToken_ConnectionProceeds(t *testing.T) {
	token := "omnipus_" + strings.Repeat("9", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	require.NoError(t, err)

	handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.CLIToken = &config.TokenEntry{Hash: config.BcryptHash(hash)}
	})
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	writeBrowserAuthFrame(t, conn, token)
	assertBrowserConnProceeds(t, conn)
}

// TestBrowserWS_Auth_InvalidToken_ClosesWithPolicyViolation verifies that a
// wrong bearer token against a configured account is rejected with an error
// frame followed by a 1008 (policy violation) close — never silently
// admitted, never a generic close.
// BDD: Given Gateway.Users holds one account,
// When the client authenticates with a token that matches no account,
// Then the server sends {"type":"error",...} and closes with code 1008.
//
// GAP 1 (D5 test-coverage gate): asserts EXACT equality against the shared
// wsAuthErrInvalidToken constant (websocket.go), not a loose substring. Prior
// to this fix the assertion checked for "unauthorized" — a string the D5 fix
// (commit b764a484) removed from the wire in favor of a human-readable
// message, which had gone undetected because nobody re-ran this test after
// the copy change. See TestWSHandlerInvalidAuth (websocket_test.go) for the
// mirrored chat-WS assertion against the same constant — a partial revert
// that restores the old literal in only ONE of the two handlers now fails
// exactly that handler's test while leaving the other green.
func TestBrowserWS_Auth_InvalidToken_ClosesWithPolicyViolation(t *testing.T) {
	token := "omnipus_" + strings.Repeat("8", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	require.NoError(t, err)

	handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.Users = []config.UserConfig{
			{Username: "browser-ws-user", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash)}}},
		}
	})
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	writeBrowserAuthFrame(t, conn, "totally-wrong-token")

	f := readBrowserFrame(t, conn, 3*time.Second)
	assert.Equal(t, "error", f.Type)
	assert.Equal(t, wsAuthErrInvalidToken, f.Message,
		"browser WS invalid-token error must carry the shared wsAuthErrInvalidToken constant verbatim")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, _, err = conn.ReadMessage()
	require.Error(t, err, "connection must be closed after invalid auth")
	var closeErr *websocket.CloseError
	require.True(t, errors.As(err, &closeErr), "expected a websocket.CloseError, got %T: %v", err, err)
	assert.Equal(t, websocket.ClosePolicyViolation, closeErr.Code,
		"invalid bearer token must close with 1008 policy violation")
}

// TestBrowserWS_Auth_NonAuthFirstFrame_Rejected verifies that a first frame
// which isn't {"type":"auth",...} is rejected outright, even under
// dev_mode_bypass — the auth handshake is mandatory regardless of what the
// eventual outcome would have been.
// BDD: Given the socket is freshly connected,
// When the client's first frame is browser_attach (not auth),
// Then the server sends an error frame naming the required envelope and the
// connection is not usable afterward.
//
// GAP 1 (D5 test-coverage gate): asserts EXACT equality against the shared
// wsAuthErrBadFirstFrame constant (websocket.go). The prior assertion
// (Contains(f.Message, "auth")) no longer matched the D5 friendly-copy fix
// ("Your session expired — reload the page to reconnect." does not contain
// "auth") and was silently red until this fix. See
// TestWSHandlerAuth_BadFirstFrame_UsesSharedConstant (websocket_test.go) for
// the mirrored chat-WS assertion against the same constant.
func TestBrowserWS_Auth_NonAuthFirstFrame_Rejected(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	attach := generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   "some-agent",
		SessionId: "s1",
	}
	data, err := json.Marshal(attach)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	f := readBrowserFrame(t, conn, 3*time.Second)
	assert.Equal(t, "error", f.Type)
	assert.Equal(t, wsAuthErrBadFirstFrame, f.Message,
		"browser WS bad-first-frame error must carry the shared wsAuthErrBadFirstFrame constant verbatim")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "connection must not remain usable after a non-auth first frame")
}

// TestBrowserWS_Auth_NoUsersConfigured_UsesSharedConstant verifies that when
// no accounts, no CLI token, and no OMNIPUS_BEARER_TOKEN are configured, and
// dev_mode_bypass is explicitly off, the browser-live handshake is rejected
// with the shared wsAuthErrNoUsers constant rather than silently admitted —
// mirroring authenticateWS's fail-closed behavior exactly, per this
// function's own doc comment.
// BDD: Given Gateway.Users is empty, Gateway.CLIToken is nil,
// OMNIPUS_BEARER_TOKEN is unset, and dev_mode_bypass=false,
// When the client sends any {"type":"auth","token":"..."} frame,
// Then the server sends {"type":"error","message":wsAuthErrNoUsers} and
// closes the connection.
// Traces to: GAP 1 (D5 test-coverage gate) — browser_ws.go:383-395. No
// existing test previously exercised this branch for the browser-live
// socket at all (only TestBrowserWS_Auth_DevModeBypass_ConnectionProceeds
// covered the dev_mode_bypass=true sibling path).
func TestBrowserWS_Auth_NoUsersConfigured_UsesSharedConstant(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.DevModeBypass = false // explicit: fail closed, not the dev-mode fallback.
	})
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	writeBrowserAuthFrame(t, conn, "any-token-nothing-is-configured")

	f := readBrowserFrame(t, conn, 3*time.Second)
	assert.Equal(t, "error", f.Type)
	assert.Equal(t, wsAuthErrNoUsers, f.Message,
		"browser WS no-users-configured error must carry the shared wsAuthErrNoUsers constant verbatim")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, _, err := conn.ReadMessage()
	assert.Error(t, err, "connection must be closed when no auth identity is configured at all")
}

// ---------------------------------------------------------------------------
// LiveViewEnabled config gate (D6)
// ---------------------------------------------------------------------------

// TestBrowserWS_LiveViewDisabled_SendsErrorAndCloses verifies that the
// LiveViewEnabled gate is checked AFTER authentication (so an unauthenticated
// probe cannot learn the feature's state) and rejects with a
// browser_status(error) frame, then closes — no attach is ever possible.
// BDD: Given tools.browser.live_view_enabled=false,
// When an authenticated client connects,
// Then the server sends {"type":"browser_status","state":"error",...} naming
// the config key and closes the connection.
func TestBrowserWS_LiveViewDisabled_SendsErrorAndCloses(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.LiveViewEnabled = false
	})
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	sendWSAuthFrameDevMode(t, conn)

	resp := readBrowserFrame(t, conn, 3*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "live_view_enabled")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, _, err := conn.ReadMessage()
	assert.Error(t, err, "connection must be closed when live view is disabled — no attach is possible")
}

// ---------------------------------------------------------------------------
// browser_attach: no registered BrowserManager for the target agent
// ---------------------------------------------------------------------------

// TestBrowserWS_Attach_NoBrowserManagerForAgent verifies that attaching to an
// agent id with no registered BrowserManager (BrowserManagerForAgent
// returns ok=false) produces a session-scoped browser_status(error) frame —
// never a panic, never a silent drop — and that the connection stays open
// and keeps dispatching frames correctly afterward.
// BDD: Given no agent named "agent-that-does-not-exist" exists in the
// AgentLoop's browser-manager map,
// When the client sends browser_attach{agent_id:"agent-that-does-not-exist"},
// Then the server responds with browser_status{state:"error"} naming the
// agent id, and a second, different missing-agent attach produces a
// second, distinct error.
func TestBrowserWS_Attach_NoBrowserManagerForAgent(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck

	sendWSAuthFrameDevMode(t, conn)

	attach := generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   "agent-that-does-not-exist",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(attach)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	resp := readBrowserFrame(t, conn, 3*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Equal(t, "sess-1", resp.SessionID, "error must echo back the client session id")
	assert.Contains(t, resp.Message, "agent-that-does-not-exist")
	assert.Contains(t, resp.Message, "no browser manager")

	// Differentiation + "connection stays open": a SECOND, DIFFERENT missing
	// agent id must produce a fresh, distinct error, not a dropped connection
	// or a stale cached response.
	attach2 := generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   "another-missing-agent",
		SessionId: "sess-2",
	}
	data2, err := json.Marshal(attach2)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data2))

	resp2 := readBrowserFrame(t, conn, 3*time.Second)
	assert.Equal(t, "browser_status", resp2.Type)
	assert.Equal(t, "error", resp2.State)
	assert.Equal(t, "sess-2", resp2.SessionID)
	assert.Contains(t, resp2.Message, "another-missing-agent")
	assert.NotEqual(t, resp.Message, resp2.Message,
		"two different missing agent ids must produce two different error messages")
}

// ---------------------------------------------------------------------------
// gateway.validate_inbound schema validation (ADR-038 finding #3)
// ---------------------------------------------------------------------------

// TestBrowserWS_ValidateInbound_RejectsMalformedInputFrame verifies that with
// gateway.validate_inbound=true, a browser_input frame with modifiers outside
// the schema's [0,15] bound is dropped and reported as browser_status(error)
// naming the failing schema — and that the SAME malformed frame with
// validate_inbound=false is silently dropped (no live view attached, so
// handleInput's nil-mgr guard returns with no response at all), proving the
// earlier error genuinely came from schema validation and not some other
// check.
// BDD: Given validate_inbound=true,
// When the client sends browser_input{kind:"mouse_move",modifiers:999},
// Then the server responds browser_status{state:"error"} naming
// "BrowserInputFrame" and "schema validation failed".
func TestBrowserWS_ValidateInbound_RejectsMalformedInputFrame(t *testing.T) {
	badMods := 999
	malformed := generated.BrowserInputFrame{
		Type:      string(generated.WsFrameTypeBrowserInput),
		Kind:      "mouse_move",
		Modifiers: &badMods,
	}
	data, err := json.Marshal(malformed)
	require.NoError(t, err)

	t.Run("validate_inbound=true rejects out-of-range modifiers", func(t *testing.T) {
		handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
			cfg.Gateway.ValidateInbound = true
		})
		t.Cleanup(handler.Wait)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		conn := dialBrowserTestWS(t, srv)
		t.Cleanup(func() { _ = conn.Close() })
		conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
		sendWSAuthFrameDevMode(t, conn)

		require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

		resp := readBrowserFrame(t, conn, 3*time.Second)
		assert.Equal(t, "browser_status", resp.Type)
		assert.Equal(t, "error", resp.State)
		assert.Contains(t, resp.Message, "BrowserInputFrame")
		assert.Contains(t, resp.Message, "schema validation failed")
	})

	t.Run(
		"validate_inbound=false lets the same frame through to handleInput, which silently no-ops",
		func(t *testing.T) {
			handler, _ := newBrowserWSTestHandler(t, func(cfg *config.Config) {
				cfg.Gateway.ValidateInbound = false
			})
			t.Cleanup(handler.Wait)
			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)

			conn := dialBrowserTestWS(t, srv)
			t.Cleanup(func() { _ = conn.Close() })
			conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
			sendWSAuthFrameDevMode(t, conn)

			require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

			// No live view is attached (state.mgr is nil), so handleInput's
			// nil-mgr guard returns without any response. If ANY frame arrives
			// here, schema validation (not something else) must have produced
			// the previous subtest's error.
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)) //nolint:errcheck
			_, _, readErr := conn.ReadMessage()
			assert.Error(t, readErr, "no frame should arrive when validate_inbound=false and no live view is attached")
		},
	)
}

// ---------------------------------------------------------------------------
// Take-control config gate + audit trail (ADR-038 D6, handleControl unit-level)
// ---------------------------------------------------------------------------

// newBrowserWSHandlerWithAudit builds a BrowserWSHandler backed by an
// AgentLoop with audit logging enabled, returning the handler, the loop, and
// the directory audit.jsonl is written to. Mirrors
// newCancelTestWSHandler's (cancel_audit_test.go) workspace/audit-dir layout
// convention: workspace = tmpDir/workspace, so the audit logger writes to
// tmpDir/system/audit.jsonl.
func newBrowserWSHandlerWithAudit(t *testing.T) (*BrowserWSHandler, *agent.AgentLoop, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	auditDir := filepath.Join(tmpDir, "system")

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			Mode:     config.SandboxModeOff,
			AuditLog: true,
		},
	}
	cfg.Tools.Browser.LiveViewEnabled = true
	cfg.Tools.Browser.TakeControlEnabled = true

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newBrowserWSHandler(al, "")
	return handler, al, auditDir
}

// newControlTestFixtures builds a bare browserWSConn + browserConnState
// pair, backed by a real *browser.BrowserManager that has NEVER had
// ensureStarted() triggered (no Session() call has been made) — so
// handleControl's TakeControl/ReleaseControl calls are pure in-memory
// LiveViewRegistry state, requiring no Chromium and no network.
func newControlTestFixtures(t *testing.T) (*browserWSConn, *browserConnState) {
	t.Helper()
	browserCfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	mgr, err := browser.NewBrowserManager(browserCfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	wc := &browserWSConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	state := &browserConnState{mgr: mgr, sessionID: "control-test-session"}
	return wc, state
}

// readWCFrame reads one frame directly off a browserWSConn's sendCh
// (bypassing the network entirely — handleControl's sendCriticalGen only
// touches sendCh/doneCh, never the underlying *websocket.Conn).
func readWCFrame(t *testing.T, wc *browserWSConn, timeout time.Duration) browserFrameDecoder {
	t.Helper()
	select {
	case data := <-wc.sendCh:
		var f browserFrameDecoder
		require.NoError(t, json.Unmarshal(data, &f))
		return f
	case <-time.After(timeout):
		t.Fatal("no frame sent to wc.sendCh within timeout")
	}
	return browserFrameDecoder{}
}

// newTabActionTestFixtures mirrors newControlTestFixtures exactly (a bare
// browserWSConn + browserConnState backed by a real, never-started
// *browser.BrowserManager) but scopes OMNIPUS_HOME/ProfileDir to a t.TempDir()
// and points ExecPath at a path that deliberately does not exist. Unlike
// SwitchTab/CloseTab (pure in-memory lookups, no CDP), OpenTab calls
// ensureStarted() — resolveExecPath's very first step is a synchronous
// os.Stat on cfg.ExecPath when it's set (manager.go), so a bogus path fails
// fast and deterministically ("browser: cannot locate chromium: ...") with
// no real Chromium launch, no network, and no dependency on whether this
// host happens to have a (possibly broken, e.g. an Ubuntu snap-stub)
// chromium binary on PATH.
func newTabActionTestFixtures(t *testing.T) (*browserWSConn, *browserConnState) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	browserCfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	browserCfg.ExecPath = filepath.Join(tmpDir, "no-such-chromium-binary")
	mgr, err := browser.NewBrowserManager(browserCfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)
	wc := &browserWSConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	state := &browserConnState{mgr: mgr, sessionID: "tab-action-test-session"}
	return wc, state
}

// marshalTabActionFrame builds a raw browser_tab_action frame body.
func marshalTabActionFrame(t *testing.T, action string, index *int) []byte {
	t.Helper()
	data, err := json.Marshal(generated.BrowserTabActionFrame{
		Type:   string(generated.WsFrameTypeBrowserTabAction),
		Action: action,
		Index:  index,
	})
	require.NoError(t, err)
	return data
}

// ---------------------------------------------------------------------------
// browser_tab_action (ADR-041 D3/D4, F5 gateway coverage)
// ---------------------------------------------------------------------------

// TestBrowserWS_HandleTabAction_NotAttached_Rejected mirrors
// TestBrowserWS_HandleControl_NotAttached_Rejected: the "attach before
// managing tabs" gate must fire before any manager state is consulted.
func TestBrowserWS_HandleTabAction_NotAttached_Rejected(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc := &browserWSConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	state := &browserConnState{} // zero value: mgr==nil, sessionID=="" — never attached

	handler.handleTabAction(wc, state, "viewer1", marshalTabActionFrame(t, "switch", intPtr(0)))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "attach before managing tabs")
}

// TestBrowserWS_HandleTabAction_Switch_MissingIndex_Rejected verifies index
// is required for "switch" and the rejection never reaches the manager (an
// out-of-range/nil index dereference must never panic).
func TestBrowserWS_HandleTabAction_Switch_MissingIndex_Rejected(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	handler.handleTabAction(wc, state, "viewer1", marshalTabActionFrame(t, "switch", nil))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "index is required for switch")
}

// TestBrowserWS_HandleTabAction_Close_MissingIndex_Rejected mirrors the
// switch case for "close".
func TestBrowserWS_HandleTabAction_Close_MissingIndex_Rejected(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	handler.handleTabAction(wc, state, "viewer1", marshalTabActionFrame(t, "close", nil))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "index is required for close")
}

// TestBrowserWS_HandleTabAction_Switch_DispatchesToManager_NoSessionErrors
// proves "switch" reaches the REAL BrowserManager.SwitchTab (not a stub, not
// a silent drop): against a manager that has no browsing context for
// browser.DefaultSessionID yet, the real error SwitchTab returns
// ("no active session") comes back as browser_status(error) — never a panic.
// (Numeric out-of-range coverage against a REAL tab set lives in
// pkg/tools/browser/tabs_test.go's TestSwitchTab_OutOfRange_ReturnsError,
// which needs a real tab and is covered at that layer; this test proves the
// gateway handler's forwarding, not SwitchTab's own index arithmetic.)
func TestBrowserWS_HandleTabAction_Switch_DispatchesToManager_NoSessionErrors(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	handler.handleTabAction(wc, state, "viewer1", marshalTabActionFrame(t, "switch", intPtr(0)))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Equal(t, state.sessionID, resp.SessionID)
	assert.Contains(t, resp.Message, "no active session",
		"the error must be SwitchTab's own — proof the handler actually called into the manager")
}

// TestBrowserWS_HandleTabAction_Close_DispatchesToManager_NoSessionErrors
// mirrors the switch case for "close" (BrowserManager.CloseTab).
func TestBrowserWS_HandleTabAction_Close_DispatchesToManager_NoSessionErrors(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	handler.handleTabAction(wc, state, "viewer1", marshalTabActionFrame(t, "close", intPtr(0)))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "no active session",
		"the error must be CloseTab's own — proof the handler actually called into the manager")
}

// TestBrowserWS_HandleTabAction_Open_DispatchesToManager_ExecPathError
// proves "open" reaches the REAL BrowserManager.OpenTab: against a manager
// whose ExecPath deliberately points at a nonexistent binary (no Chromium
// dependency, fails fast via os.Stat inside resolveExecPath), OpenTab's own
// "cannot locate chromium" error comes back as browser_status(error).
func TestBrowserWS_HandleTabAction_Open_DispatchesToManager_ExecPathError(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newTabActionTestFixtures(t)

	handler.handleTabAction(wc, state, "viewer1", marshalTabActionFrame(t, "open", nil))

	resp := readWCFrame(t, wc, 5*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "cannot locate chromium",
		"the error must be OpenTab's own (via ensureStarted/resolveExecPath) — proof the handler "+
			"actually called into the manager, not a stub")
}

// TestBrowserWS_HandleTabAction_UnknownAction_Rejected verifies an
// unrecognized action string is rejected with a descriptive error rather
// than silently ignored or treated as switch/close/open.
func TestBrowserWS_HandleTabAction_UnknownAction_Rejected(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	handler.handleTabAction(wc, state, "viewer1", marshalTabActionFrame(t, "bogus-action", nil))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "unknown action")
}

// ---------------------------------------------------------------------------
// F3 (7-reviewer MAJOR): handleTabAction's control-lock gate.
//
// Before this fix, handleTabAction had NO control-lock check at all — any
// merely-attached (non-controlling) viewer could switch/close/open tabs out
// from under whoever actually holds control. The fix mirrors
// dispatchInput's gate (LiveView.dispatchInput's "lv.controller == viewerID"
// check, reached here via the SAME read-only accessor handleControl already
// uses: LiveViewRegistry.Controller): a tab action is honored only when the
// acting viewer holds control, or nobody does (idle).
// ---------------------------------------------------------------------------

// TestBrowserWS_HandleTabAction_ControlGate_OtherViewerControls_Rejected
// proves a viewer that is NOT the controller is rejected BEFORE the request
// ever reaches the manager: the response is the gate's own message, not
// SwitchTab's "no active session" error the sibling dispatch tests above
// prove the manager itself would return — the only way to distinguish "the
// gate blocked it" from "the gate let it through and the manager failed" is
// the exact error text, which is asserted here.
func TestBrowserWS_HandleTabAction_ControlGate_OtherViewerControls_Rejected(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	require.True(t, state.mgr.Live().TakeControl(browser.DefaultSessionID, "controlling-viewer"),
		"test setup: first take must succeed on an uncontrolled session")

	handler.handleTabAction(wc, state, "other-viewer", marshalTabActionFrame(t, "switch", intPtr(0)))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "take control first",
		"a non-controller's tab action must be rejected by the gate itself, never reach the manager")
	assert.NotContains(t, resp.Message, "no active session",
		"the manager's own error must never appear — the gate must block before dispatch")

	assert.Equal(t, "controlling-viewer", state.mgr.Live().Controller(browser.DefaultSessionID),
		"a rejected tab action must not disturb the existing controller")
}

// TestBrowserWS_HandleTabAction_ControlGate_IdleAllowsDispatch proves an
// uncontrolled (idle) session lets ANY attached viewer's tab action through
// to the manager — the gate only blocks when a DIFFERENT viewer already
// holds control, never merely "nobody has taken control yet".
func TestBrowserWS_HandleTabAction_ControlGate_IdleAllowsDispatch(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	require.False(t, state.mgr.Live().IsControlled(browser.DefaultSessionID), "precondition: uncontrolled")

	handler.handleTabAction(wc, state, "any-viewer", marshalTabActionFrame(t, "switch", intPtr(0)))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "no active session",
		"an idle session must let the tab action reach the real manager (SwitchTab's own error), "+
			"not be blocked by the control gate")
}

// TestBrowserWS_HandleTabAction_ControlGate_SelfControllerAllowsDispatch
// proves the viewer that itself holds control is always allowed through.
func TestBrowserWS_HandleTabAction_ControlGate_SelfControllerAllowsDispatch(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)

	require.True(t, state.mgr.Live().TakeControl(browser.DefaultSessionID, "driving-viewer"),
		"test setup: take control must succeed on an uncontrolled session")

	handler.handleTabAction(wc, state, "driving-viewer", marshalTabActionFrame(t, "close", intPtr(0)))

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "no active session",
		"the controller's own tab action must reach the real manager (CloseTab's own error), "+
			"not be blocked by the gate it itself satisfies")
}

// marshalControlFrame builds a raw browser_control frame body for the given action.
func marshalControlFrame(t *testing.T, action string) []byte {
	t.Helper()
	data, err := json.Marshal(generated.BrowserControlFrame{
		Type:   string(generated.WsFrameTypeBrowserControl),
		Action: action,
	})
	require.NoError(t, err)
	return data
}

// readBrowserAuditRecords parses every audit.Record row from auditDir/audit.jsonl.
func readBrowserAuditRecords(t *testing.T, auditDir string) []audit.Record {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(auditDir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var records []audit.Record
	for _, line := range splitCancelAuditTestLines(data) {
		if len(line) == 0 {
			continue
		}
		var r audit.Record
		if json.Unmarshal(line, &r) == nil && r.Event != "" {
			records = append(records, r)
		}
	}
	return records
}

// lastBrowserAuditRecord polls until at least one record with the given
// event name has been written, returning the most recent one.
func lastBrowserAuditRecord(t *testing.T, auditDir, event string) audit.Record {
	t.Helper()
	var last audit.Record
	require.Eventually(t, func() bool {
		for _, r := range readBrowserAuditRecords(t, auditDir) {
			if r.Event == event {
				last = r
			}
		}
		return last.Event == event
	}, 2*time.Second, 20*time.Millisecond, "expected audit event %q to be written", event)
	return last
}

// TestBrowserWS_HandleControl_NotAttached_Rejected verifies the "attach
// before requesting control" gate fires before any manager/session state is
// consulted.
func TestBrowserWS_HandleControl_NotAttached_Rejected(t *testing.T) {
	handler, al, _ := newBrowserWSHandlerWithAudit(t)
	wc := &browserWSConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	state := &browserConnState{} // zero value: mgr==nil, sessionID=="" — never attached

	handler.handleControl(wc, state, "viewer1", "user1", marshalControlFrame(t, "take"), al.GetConfig())

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "attach before requesting control")
}

// TestBrowserWS_HandleControl_TakeControlDisabled_DeniedAndAudited verifies
// the operator kill-switch (tools.browser.take_control_enabled=false) denies
// a take request with a browser_status(error) frame and audits the denial at
// WARN with reason=take_control_disabled — and that the denied attempt
// leaves the session genuinely uncontrolled (real state, not a canned
// response).
// BDD: Given take_control_enabled=false and an attached, uncontrolled session,
// When the client sends browser_control{action:"take"},
// Then the server responds browser_status{state:"error"} and audit.jsonl
// gains a browser.live.control_taken WARN record with reason=take_control_disabled.
func TestBrowserWS_HandleControl_TakeControlDisabled_DeniedAndAudited(t *testing.T) {
	handler, al, auditDir := newBrowserWSHandlerWithAudit(t)
	al.GetConfig().Tools.Browser.TakeControlEnabled = false

	wc, state := newControlTestFixtures(t)

	handler.handleControl(wc, state, "viewer-disabled", "user-a", marshalControlFrame(t, "take"), al.GetConfig())

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "take-control is disabled")

	rec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserLiveControlTaken)
	assert.Equal(t, audit.SeverityWarn, rec.Severity)
	assert.Equal(t, "take_control_disabled", rec.Fields["reason"])
	assert.Equal(t, "user-a", rec.Fields["user"])
	assert.Equal(t, "viewer-disabled", rec.Fields["viewer_id"])
	assert.Equal(t, state.sessionID, rec.Fields["session_id"])

	assert.False(t, state.mgr.Live().IsControlled(browser.DefaultSessionID),
		"a denied take must not leave the session controlled")
}

// TestBrowserWS_HandleControl_AlreadyControlled_DeniedAndAudited verifies
// v1's cooperative, no-preemption policy: a take request against a session
// someone else already controls is denied and audited at WARN with
// reason=already_controlled, and the original controller is unaffected.
func TestBrowserWS_HandleControl_AlreadyControlled_DeniedAndAudited(t *testing.T) {
	handler, al, auditDir := newBrowserWSHandlerWithAudit(t)
	wc, state := newControlTestFixtures(t)

	require.True(t, state.mgr.Live().TakeControl(browser.DefaultSessionID, "other-viewer"),
		"test setup: first take must succeed on an uncontrolled session")

	handler.handleControl(wc, state, "viewer-latecomer", "user-b", marshalControlFrame(t, "take"), al.GetConfig())

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "another viewer already controls")

	rec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserLiveControlTaken)
	assert.Equal(t, audit.SeverityWarn, rec.Severity)
	assert.Equal(t, "already_controlled", rec.Fields["reason"])
	assert.Equal(t, "viewer-latecomer", rec.Fields["viewer_id"])

	assert.Equal(t, "other-viewer", state.mgr.Live().Controller(browser.DefaultSessionID),
		"a denied take-over attempt must not disturb the existing controller")
}

// TestBrowserWS_HandleControl_TakeThenRelease_AllowedAndAudited verifies the
// happy path end to end: take succeeds (real LiveViewRegistry state change +
// INFO audit reason=take), then release actually clears the lock (real state
// change, not a canned response) and is audited at INFO.
func TestBrowserWS_HandleControl_TakeThenRelease_AllowedAndAudited(t *testing.T) {
	handler, al, auditDir := newBrowserWSHandlerWithAudit(t)
	wc, state := newControlTestFixtures(t)

	require.False(t, state.mgr.Live().IsControlled(browser.DefaultSessionID), "precondition: uncontrolled")

	handler.handleControl(wc, state, "viewer-ok", "user-c", marshalControlFrame(t, "take"), al.GetConfig())

	takeResp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", takeResp.Type)
	assert.Equal(t, "controlling", takeResp.State)
	assert.Equal(t, "user-c", takeResp.Controller)

	assert.True(t, state.mgr.Live().IsControlled(browser.DefaultSessionID))
	assert.Equal(t, "viewer-ok", state.mgr.Live().Controller(browser.DefaultSessionID))

	takeRec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserLiveControlTaken)
	assert.Equal(t, audit.SeverityInfo, takeRec.Severity)
	assert.Equal(t, "take", takeRec.Fields["reason"])

	handler.handleControl(wc, state, "viewer-ok", "user-c", marshalControlFrame(t, "release"), al.GetConfig())

	releaseResp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", releaseResp.Type)
	assert.Equal(t, "released", releaseResp.State)

	assert.False(t, state.mgr.Live().IsControlled(browser.DefaultSessionID),
		"release must actually clear the control lock, not just report success")

	releaseRec := lastBrowserAuditRecord(t, auditDir, audit.EventBrowserLiveControlReleased)
	assert.Equal(t, audit.SeverityInfo, releaseRec.Severity)
	assert.Equal(t, "viewer-ok", releaseRec.Fields["viewer_id"])
}

// TestBrowserWS_HandleControl_UnknownAction_Rejected verifies an unrecognized
// action string is rejected with a descriptive error rather than silently
// ignored or treated as either take or release.
func TestBrowserWS_HandleControl_UnknownAction_Rejected(t *testing.T) {
	handler, al, _ := newBrowserWSHandlerWithAudit(t)
	wc, state := newControlTestFixtures(t)

	handler.handleControl(wc, state, "viewer1", "user1", marshalControlFrame(t, "bogus-action"), al.GetConfig())

	resp := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", resp.Type)
	assert.Equal(t, "error", resp.State)
	assert.Contains(t, resp.Message, "unknown action")

	assert.False(t, state.mgr.Live().IsControlled(browser.DefaultSessionID),
		"an unknown action must not grant control as a side effect")
}

// ---------------------------------------------------------------------------
// Real-input-error throttle is content-aware (7-reviewer LOW finding)
// ---------------------------------------------------------------------------

// TestBrowserWS_HandleInput_ThrottleIsContentAware verifies the
// minInputErrorInterval cooldown only suppresses a REPEATED, IDENTICAL
// failure message — never a genuinely different one, even inside the same
// window. Uses a "mouse_move" frame (not "navigate" — B4 exempts navigate
// from this cooldown entirely, see TestBrowserWS_HandleInput_Navigate_
// AlwaysBypassesThrottle below) as the high-frequency kind this cooldown
// actually exists for. Producing two truly distinct dispatchInput failures
// needs an attached, real CDP tab (see this file's header comment on why
// full attach+screencast is reserved for the browserWSSkipIfNoBrowser-gated
// suite), so the "different message" half of this test pre-seeds
// browserConnState.lastInputErrorMessage with a synthetic prior value —
// isolating the throttle's own content-comparison logic (handleInput's
// throttled-frame.Kind/message/interval condition) from dispatchInput's
// specific failure text.
// BDD: Given a real dispatch failure (control taken but never attached, so
// dispatchInput fails closed at "session is not attached" every time),
// When the SAME failure repeats immediately (within minInputErrorInterval),
// Then only the first browser_status(error) frame is sent.
// And when the connection's last-recorded message differs from the new
// failure's own message,
// Then the new frame is sent immediately, even inside the same window.
func TestBrowserWS_HandleInput_ThrottleIsContentAware(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)
	require.True(t, state.mgr.Live().TakeControl(browser.DefaultSessionID, "viewer1"),
		"test setup: take control WITHOUT a real attach, so dispatchInput fails deterministically "+
			"and identically every call ('session is not attached', tabCtx nil) — no CDP/browser needed")

	mmX, mmY := 10.0, 20.0
	moveFrame, err := json.Marshal(generated.BrowserInputFrame{
		Type: string(generated.WsFrameTypeBrowserInput), Kind: "mouse_move", X: &mmX, Y: &mmY,
	})
	require.NoError(t, err)

	// First call: nothing recorded yet, must send immediately.
	handler.handleInput(wc, state, "viewer1", moveFrame)
	first := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", first.Type)
	assert.Equal(t, "error", first.State)
	assert.NotEmpty(t, first.Message)

	// Second call, immediately after (well inside minInputErrorInterval),
	// with the IDENTICAL underlying failure — must be throttled (no frame).
	handler.handleInput(wc, state, "viewer1", moveFrame)
	select {
	case f := <-wc.sendCh:
		t.Fatalf("an identical repeated error must still be throttled inside minInputErrorInterval, got: %s", f)
	case <-time.After(300 * time.Millisecond):
		// expected: nothing sent
	}

	// Simulate the connection's last-sent message having been something
	// else (in production this would be a genuinely different failure, e.g.
	// a stray mouse-move right after the tab died a different way) — the
	// throttle must compare against the CURRENT failure's own message and
	// send immediately, not stay suppressed just because it landed inside
	// the same 2s window.
	state.lastInputErrorMessage = "a completely different prior failure message"
	handler.handleInput(wc, state, "viewer1", moveFrame)
	third := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", third.Type)
	assert.Equal(t, "error", third.State)
	assert.NotEqual(t, "a completely different prior failure message", third.Message,
		"the frame sent must carry the NEW dispatch failure's own message, not the pre-seeded one")
}

// TestBrowserWS_HandleInput_Navigate_AlwaysBypassesThrottle is the regression
// test for B4 (7-reviewer finding): a user-initiated navigate error must
// always surface, even a rapid identical resubmit of the same blocked URL —
// unlike TestBrowserWS_HandleInput_ThrottleIsContentAware's mouse_move case
// above, TWO byte-identical navigate-blocked errors sent back-to-back must
// BOTH produce a browser_status(error) frame. Without this exemption, the
// SPA (which clears its error banner optimistically on every navigate
// submit) would leave the user looking at no error at all after resubmitting
// the exact same bad URL within minInputErrorInterval.
// BDD: Given a real dispatch failure on a "navigate" input (control taken
// but never attached, so dispatchInput fails closed identically every call),
// When the SAME navigate is resubmitted immediately (well inside
// minInputErrorInterval), Then BOTH calls produce a browser_status(error)
// frame — the cooldown that suppresses a repeated mouse_move failure must
// never apply to "navigate".
func TestBrowserWS_HandleInput_Navigate_AlwaysBypassesThrottle(t *testing.T) {
	handler, _ := newBrowserWSTestHandler(t, nil)
	wc, state := newControlTestFixtures(t)
	require.True(t, state.mgr.Live().TakeControl(browser.DefaultSessionID, "viewer1"),
		"test setup: take control WITHOUT a real attach, so dispatchInput fails deterministically "+
			"and identically every call ('session is not attached', tabCtx nil) — no CDP/browser needed")

	navURL := "http://8.8.8.8/"
	navigateFrame, err := json.Marshal(generated.BrowserInputFrame{
		Type: string(generated.WsFrameTypeBrowserInput), Kind: "navigate", Url: &navURL,
	})
	require.NoError(t, err)

	handler.handleInput(wc, state, "viewer1", navigateFrame)
	first := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", first.Type)
	assert.Equal(t, "error", first.State)
	assert.NotEmpty(t, first.Message)

	// Immediate, byte-identical resubmit — well inside minInputErrorInterval.
	// A non-navigate kind would be throttled here (see the sibling test
	// above); navigate must NOT be.
	handler.handleInput(wc, state, "viewer1", navigateFrame)
	second := readWCFrame(t, wc, 2*time.Second)
	assert.Equal(t, "browser_status", second.Type)
	assert.Equal(t, "error", second.State)
	assert.Equal(t, first.Message, second.Message,
		"both calls dispatch the identical failing navigate — the message content is expected to match")
}

// ---------------------------------------------------------------------------
// Full WS round trip: browser_attach → browser_control{take} →
// browser_input{navigate} (ADR-039 D-A2, 7-reviewer MEDIUM finding)
// ---------------------------------------------------------------------------

// TestBrowserWS_Input_Navigate_SSRFBlocked_FullRoundTrip is a real,
// end-to-end (real headless Chromium, real WS socket, real attach)
// regression guard for ADR-039 D-A2's live-WS navigate SSRF gate: attach to
// a real agent's browser manager, take control, then send
// browser_input{kind:"navigate", url:"http://127.0.0.1/"} over the wire, and
// confirm a browser_status(error) naming the block comes back — proving
// handleInput's wire-to-engine URL translation (`if frame.Url != nil {
// in.URL = *frame.Url }`) reaches dispatchInput's SSRF gate end to end, not
// just via the pkg/tools/browser-internal LiveView tests (live_test.go),
// which stub out CDP entirely and never touch this handler's own JSON
// decode/field-mapping line.
//
// Unlike every other test in this file (see the file's header comment), this
// one genuinely needs handleAttach's mgr.Live().Attach() to succeed, which
// resolves a real Chromium tab (BrowserManager.Session) — so it is gated by
// browserWSSkipIfNoBrowser exactly like
// pkg/tools/browser's own skipIfNoBrowser-gated E2E suite: SKIP (never a
// false pass, never a hang) wherever no working Chromium/Chrome binary is
// available (e.g. this devpod's Ubuntu snap-stub chromium-browser), REAL
// coverage everywhere one is.
// BDD: Given a real agent with a registered, real BrowserManager,
// When the client attaches, takes control, then sends
// browser_input{kind:"navigate", url:"http://127.0.0.1/"},
// Then the server responds browser_status{state:"error"} naming the SSRF
// block, and the CDP navigate is never actually dispatched.
func TestBrowserWS_Input_Navigate_SSRFBlocked_FullRoundTrip(t *testing.T) {
	browserWSSkipIfNoBrowser(t)

	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.Headless = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.PageTimeoutSec = 30
		cfg.Tools.Browser.MaxTabs = 5
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")
	agentID := defaultAgent.ID
	mgr, ok := al.BrowserManagerForAgent(agentID)
	require.True(t, ok, "registerSharedTools must have registered a browser manager for the default agent")
	t.Cleanup(mgr.Shutdown)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(20 * time.Second)) //nolint:errcheck

	sendWSAuthFrameDevMode(t, conn)

	attach := generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   agentID,
		SessionId: "nav-round-trip-session",
	}
	attachData, err := json.Marshal(attach)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, attachData))

	// Use the skip-until-found reader: browser_tabs (and, once WebRTC
	// negotiates, browser_webrtc_* frames) can legitimately interleave ahead
	// of browser_status on a REAL browser — see readBrowserStatusFrame's doc
	// comment. A naive readBrowserFrame here asserted an ordering the
	// protocol does not guarantee.
	attachResp := readBrowserStatusFrame(t, conn, 20*time.Second)
	require.Equal(t, "browser_status", attachResp.Type)
	require.Equal(t, "attached", attachResp.State,
		"attach must succeed against a real headless Chromium: %+v", attachResp)

	control := generated.BrowserControlFrame{Type: string(generated.WsFrameTypeBrowserControl), Action: "take"}
	controlData, err := json.Marshal(control)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, controlData))

	controlResp := readBrowserStatusFrame(t, conn, 5*time.Second)
	require.Equal(t, "browser_status", controlResp.Type)
	require.Equal(t, "controlling", controlResp.State)

	navURL := "http://127.0.0.1/"
	navigate := generated.BrowserInputFrame{
		Type: string(generated.WsFrameTypeBrowserInput),
		Kind: "navigate",
		Url:  &navURL,
	}
	navigateData, err := json.Marshal(navigate)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, navigateData))

	navResp := readBrowserFrame(t, conn, 5*time.Second)
	assert.Equal(t, "browser_status", navResp.Type)
	assert.Equal(t, "error", navResp.State)
	assert.Contains(t, navResp.Message, "navigate blocked",
		"a loopback navigate URL must be refused by the SSRF gate: %+v", navResp)
}

// TestBrowserWS_Control_ControlledByOther_BroadcastsToSecondConnection is the
// gateway-level, full-round-trip regression guard for ADR-039 UAT finding
// BE-1 ("two viewers of the same live browser session disagree about who's
// driving"): two REAL WebSocket connections attach to the SAME agent/session
// (mirroring a docked panel + a pop-out window), connection A takes control,
// and connection B — which never asked for anything — must receive an
// unprompted browser_status frame with controlled_by_other=true. Releasing
// must broadcast controlled_by_other=false back to B. This complements the
// no-CDP-needed LiveView-level tests in pkg/tools/browser/live_test.go
// (TestLiveView_TakeReleaseControl_BroadcastsToOtherViewers) by proving the
// gateway's onControl wiring (handleAttach, browser_ws.go) actually reaches
// a second live WS connection end to end — same rationale as the sibling
// navigate-SSRF full-round-trip test above.
//
// Gated exactly like that sibling test: needs a real Chromium tab
// (handleAttach's mgr.Live().Attach() call), so it SKIPs wherever no working
// Chromium/Chrome binary is available and runs for real everywhere one is.
// BDD: Given connection A and connection B both attached to the same
// (agent, session), When A sends browser_control{action:"take"}, Then B
// (which sent nothing) receives browser_status{controlled_by_other:true};
// When A then sends browser_control{action:"release"}, Then B receives
// browser_status{controlled_by_other:false}.
func TestBrowserWS_Control_ControlledByOther_BroadcastsToSecondConnection(t *testing.T) {
	browserWSSkipIfNoBrowser(t)

	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.Headless = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.PageTimeoutSec = 30
		cfg.Tools.Browser.MaxTabs = 5
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")
	agentID := defaultAgent.ID
	mgr, ok := al.BrowserManagerForAgent(agentID)
	require.True(t, ok, "registerSharedTools must have registered a browser manager for the default agent")
	t.Cleanup(mgr.Shutdown)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	connA := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = connA.Close() })
	connA.SetReadDeadline(time.Now().Add(20 * time.Second)) //nolint:errcheck
	sendWSAuthFrameDevMode(t, connA)

	connB := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = connB.Close() })
	connB.SetReadDeadline(time.Now().Add(20 * time.Second)) //nolint:errcheck
	sendWSAuthFrameDevMode(t, connB)

	attachFrame := func(sessionID string) []byte {
		data, err := json.Marshal(generated.BrowserAttachFrame{
			Type: string(generated.WsFrameTypeBrowserAttach), AgentId: agentID, SessionId: sessionID,
		})
		require.NoError(t, err)
		return data
	}

	require.NoError(t, connA.WriteMessage(websocket.TextMessage, attachFrame("controlled-by-other-session")))
	attachRespA := readBrowserStatusFrame(t, connA, 20*time.Second)
	require.Equal(
		t,
		"attached",
		attachRespA.State,
		"conn A attach must succeed against a real headless Chromium: %+v",
		attachRespA,
	)
	require.NotNil(t, attachRespA.ControlledByOther)
	assert.False(t, *attachRespA.ControlledByOther, "conn A attaches first — nobody controls yet")
	assert.Nil(
		t,
		attachRespA.ControlOnly,
		"the initial attach response is a real lifecycle frame — control_only must not be set (B1)",
	)

	require.NoError(t, connB.WriteMessage(websocket.TextMessage, attachFrame("controlled-by-other-session")))
	attachRespB := readBrowserStatusFrame(t, connB, 20*time.Second)
	require.Equal(
		t,
		"attached",
		attachRespB.State,
		"conn B attach (piggyback on A's screencast) must succeed: %+v",
		attachRespB,
	)
	require.NotNil(t, attachRespB.ControlledByOther)
	assert.False(t, *attachRespB.ControlledByOther, "still nobody controls at conn B's attach time")
	assert.Nil(
		t,
		attachRespB.ControlOnly,
		"the initial attach response is a real lifecycle frame — control_only must not be set (B1)",
	)

	control := func(action string) []byte {
		data, err := json.Marshal(
			generated.BrowserControlFrame{Type: string(generated.WsFrameTypeBrowserControl), Action: action},
		)
		require.NoError(t, err)
		return data
	}

	require.NoError(t, connA.WriteMessage(websocket.TextMessage, control("take")))
	takeRespA := readBrowserStatusFrame(t, connA, 5*time.Second)
	require.Equal(t, "controlling", takeRespA.State, "conn A's own take response: %+v", takeRespA)

	broadcastToB := readBrowserStatusFrame(t, connB, 5*time.Second)
	require.NotNil(
		t,
		broadcastToB.ControlledByOther,
		"conn B must receive an unprompted controlled_by_other broadcast: %+v",
		broadcastToB,
	)
	assert.True(t, *broadcastToB.ControlledByOther, "conn B never took control — it must be told someone else did")
	require.NotNil(
		t,
		broadcastToB.ControlOnly,
		"a take/release fan-out frame must be flagged control_only (B1): %+v",
		broadcastToB,
	)
	assert.True(
		t,
		*broadcastToB.ControlOnly,
		"conn B's frame carries no real lifecycle/error meaning — the SPA must not treat it as a status change",
	)

	require.NoError(t, connA.WriteMessage(websocket.TextMessage, control("release")))
	releaseRespA := readBrowserStatusFrame(t, connA, 5*time.Second)
	require.Equal(t, "released", releaseRespA.State, "conn A's own release response: %+v", releaseRespA)
	assert.Nil(
		t,
		releaseRespA.ControlOnly,
		"conn A's own direct browser_status response is a real lifecycle frame, not a broadcast (B1)",
	)

	broadcastToB2 := readBrowserStatusFrame(t, connB, 5*time.Second)
	require.NotNil(t, broadcastToB2.ControlledByOther)
	assert.False(t, *broadcastToB2.ControlledByOther, "conn B must be told the lock was freed")
	require.NotNil(
		t,
		broadcastToB2.ControlOnly,
		"the release fan-out frame must also be flagged control_only (B1): %+v",
		broadcastToB2,
	)
	assert.True(t, *broadcastToB2.ControlOnly)
}

// readBrowserWebRTCStateFrame mirrors readBrowserStatusFrame's skip-until-
// found convention (see its doc comment) for a browser_webrtc_state frame:
// browser_tabs frames may legitimately interleave ahead of it (asynchronous
// to the attach response), so a single bare ReadMessage call would flake
// exactly like a naive readBrowserFrame call would for browser_status.
func readBrowserWebRTCStateFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) webrtcStateFrameDecoder {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("no browser_webrtc_state frame received within timeout")
		}
		conn.SetReadDeadline(time.Now().Add(remaining)) //nolint:errcheck
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "must read a frame")
		var probe wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &probe))
		if probe.Type != string(generated.WsFrameTypeBrowserWebrtcState) {
			continue
		}
		var f webrtcStateFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f))
		return f
	}
}

// TestBrowserWS_Attach_AnnouncesWebRTCAvailabilityOnSameConnection is the QA
// regression-wave item 1 guard: fix-wave A added
// TestWebrtcUnavailableReason_GateLadder and
// TestWebrtcUnavailableReason_AgreesAcrossBothCallers
// (browser_webrtc_fixwave_test.go), which prove webrtcUnavailableReason's
// classification logic in isolation — but neither ever calls handleAttach,
// so no test proved the actual CALL SITE (handleAttach's own
// "h.announceWebRTCAvailability(...)" line at the end of browser_ws.go's
// handleAttach) fires at all. Without it, the SPA's WebRTC upgrade never
// starts (see announceWebRTCAvailability's own doc comment: "the SPA's state
// machine only sends its browser_webrtc_offer after receiving
// available:true... without this announcement neither side ever moves").
//
// Mirrors TestBrowserWS_Control_ControlledByOther_BroadcastsToSecondConnection's
// scaffolding (real headless Chromium, browserWSSkipIfNoBrowser-gated): a
// single connection attaches for real, and this asserts a
// browser_webrtc_state frame follows the browser_status{state:"attached"}
// response on the SAME connection — with no browser_webrtc_offer EVER sent,
// so any browser_webrtc_state frame observed here can only be handleAttach's
// own unprompted announcement, never handleWebRTCOffer's offer-time
// sendWebRTCState call (that path is already covered end-to-end by
// TestWebRTCEndToEndInProcess, pkg/gateway/browser_webrtc_e2e_test.go).
// WebRTCEnabled is deliberately left at its zero-value default (disabled) —
// this test only cares that the announcement fires at all, not what its
// available/reason fields say; that classification logic is
// TestWebrtcUnavailableReason_GateLadder's job.
// BDD: Given a connection attaches to a live agent session, When the attach
// succeeds, Then a browser_webrtc_state frame follows on the SAME
// connection without any browser_webrtc_offer ever being sent.
func TestBrowserWS_Attach_AnnouncesWebRTCAvailabilityOnSameConnection(t *testing.T) {
	browserWSSkipIfNoBrowser(t)

	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.Headless = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.PageTimeoutSec = 30
		cfg.Tools.Browser.MaxTabs = 5
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test fixture must seed at least one agent")
	agentID := defaultAgent.ID
	mgr, ok := al.BrowserManagerForAgent(agentID)
	require.True(t, ok, "registerSharedTools must have registered a browser manager for the default agent")
	t.Cleanup(mgr.Shutdown)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetReadDeadline(time.Now().Add(20 * time.Second)) //nolint:errcheck
	sendWSAuthFrameDevMode(t, conn)

	attachFrame, err := json.Marshal(generated.BrowserAttachFrame{
		Type: string(generated.WsFrameTypeBrowserAttach), AgentId: agentID, SessionId: "announce-session",
	})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, attachFrame))

	attachResp := readBrowserStatusFrame(t, conn, 20*time.Second)
	require.Equal(t, "attached", attachResp.State,
		"attach must succeed against a real headless Chromium: %+v", attachResp)

	stateFrame := readBrowserWebRTCStateFrame(t, conn, 20*time.Second)
	require.Equal(t, string(generated.WsFrameTypeBrowserWebrtcState), stateFrame.Type)
	require.Equal(t, "announce-session", stateFrame.SessionID,
		"the announcement must carry THIS connection's session id")
}
