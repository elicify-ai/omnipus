//go:build !cgo

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
// handleAttach to have already succeeded, which in turn requires a live CDP
// screencast (mgr.Live().Attach → chromedp.Run(StartScreencast)) — that full
// attach+screencast round trip is reserved for the Playwright UAT pass
// (issue tracked separately), not a unit test. handleControl's own logic
// (the TakeControlEnabled gate, the control-lock allow/deny outcome, and the
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
	"net/http/httptest"
	"os"
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
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
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
	assert.Contains(t, strings.ToLower(f.Message), "unauthorized")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, _, err = conn.ReadMessage()
	require.Error(t, err, "connection must be closed after invalid auth")
	closeErr, ok := err.(*websocket.CloseError)
	require.True(t, ok, "expected a websocket.CloseError, got %T: %v", err, err)
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
	assert.Contains(t, f.Message, "auth")

	conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
	_, _, err = conn.ReadMessage()
	assert.Error(t, err, "connection must not remain usable after a non-auth first frame")
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

	t.Run("validate_inbound=false lets the same frame through to handleInput, which silently no-ops", func(t *testing.T) {
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
	})
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
				Workspace: workspaceDir,
				ModelName: "test-model",
				MaxTokens: 4096,
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
