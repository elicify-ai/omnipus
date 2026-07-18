//go:build !cgo

// browser_webrtc_test.go — ADR-047 / wave-plan W2-A gateway WebRTC signaling
// tests (pkg/gateway/browser_webrtc.go). Mirrors browser_ws_test.go's
// convention of calling handler methods directly (white-box, same package)
// for logic that doesn't require a live Chromium — handleWebRTCOffer's gate
// ladder is pure config/capability/registry logic below the
// "start the encoder page" boundary, so it's fully testable without CDP.
//
// Traces to: docs/internal/architecture/ADR-047-live-browser-webrtc.md

package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// fakeRelay is a test double for browser.RelaySession — this package's own
// copy (pkg/tools/browser's fakeRelay in capture_session_test.go is
// unexported and package-private), mirroring wave-plan W2-A's "fake
// webrtc.Session behind a narrow interface you define at the consumption
// point."
type fakeRelay struct {
	mu            sync.Mutex
	closedViewers []string
}

func (f *fakeRelay) HandleIngestOffer(sdp string) (string, error) { return "ingest-answer", nil }
func (f *fakeRelay) HandleViewerOffer(viewerID, sdp string) (string, error) {
	return "viewer-answer-" + viewerID, nil
}
func (f *fakeRelay) CloseViewer(viewerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedViewers = append(f.closedViewers, viewerID)
}
func (f *fakeRelay) SignalRecapture()                  {}
func (f *fakeRelay) SendToViewer(string, []byte) error { return nil }
func (f *fakeRelay) Stats() webrtc.Stats               { return webrtc.Stats{} }
func (f *fakeRelay) Close() error                      { return nil }

// fakeEncoderStarter never touches real chromedp — it just counts
// invocations and either returns a bare cancelable context or startErr.
func fakeEncoderStarter(callCount *int32, startErr error) browser.EncoderStarter {
	return func(context.Context, *browser.BrowserManager, string, string) (context.Context, context.CancelFunc, error) {
		atomic.AddInt32(callCount, 1)
		if startErr != nil {
			return nil, nil, startErr
		}
		ctx, cancel := context.WithCancel(context.Background())
		return ctx, cancel, nil
	}
}

// webrtcStateFrameDecoder is a test-only decode target for
// browser_webrtc_state frames — not a wire-format type (Constraint #8 is
// about production pkg/gateway structs; this never round-trips over the
// wire, only decodes what the handler already marshaled via the generated
// type).
type webrtcStateFrameDecoder struct { // not-wire-format: decode-only test assertion target.
	Type      string `json:"type"`
	Available bool   `json:"available"`
	Active    *bool  `json:"active,omitempty"`
	HasAudio  *bool  `json:"has_audio,omitempty"`
	Reason    string `json:"reason,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// newTestBrowserWSConn builds a bare browserWSConn with no real
// *websocket.Conn — safe for handleWebRTCOffer, which only ever writes via
// sendCriticalGen/sendFrameGen (sendCh), never touches wc.conn directly.
func newTestBrowserWSConn() *browserWSConn {
	return &browserWSConn{
		sendCh: make(chan []byte, 8),
		doneCh: make(chan struct{}),
	}
}

// drainOneFrame reads and decodes exactly one frame off wc.sendCh.
func drainOneFrame(t *testing.T, wc *browserWSConn) json.RawMessage {
	t.Helper()
	select {
	case data := <-wc.sendCh:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame on sendCh")
		return nil
	}
}

func decodeWebRTCState(t *testing.T, raw json.RawMessage) webrtcStateFrameDecoder {
	t.Helper()
	var f webrtcStateFrameDecoder
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, string(generated.WsFrameTypeBrowserWebrtcState), f.Type)
	return f
}

func TestHandleWebRTCOffer_GateLadder_DisabledByConfig(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = false
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig())

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available, "webrtc_enabled=false must report available=false")
	require.Equal(t, "disabled", got.Reason)
	require.Empty(t, state.webrtcAgentID, "no viewer must be registered on the disabled path")
}

func TestHandleWebRTCOffer_GateLadder_InvalidFrame(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	var state browserConnState
	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", []byte("not json"), al.GetConfig())

	raw := drainOneFrame(t, wc)
	var f struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
}

func TestHandleWebRTCOffer_GateLadder_MissingFields(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		SessionId: "sess-1",
		// AgentId and Sdp deliberately omitted.
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig())

	raw := drainOneFrame(t, wc)
	var f struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
}

func TestHandleWebRTCOffer_GateLadder_UnknownAgent(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
	})
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   "agent-that-does-not-exist",
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig())

	raw := drainOneFrame(t, wc)
	var f struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
}

func TestHandleWebRTCOffer_GateLadder_NotCapable(t *testing.T) {
	// ProfileDir MUST be isolated under t.TempDir(): InstallRoot() is derived
	// from it (InstallRootForProfileDir), and leaving it unset would resolve
	// to this POD's real $OMNIPUS_HOME/browser/chromium — a real, persistent,
	// cross-test-shared directory that may already have a full-Chrome build
	// installed from prior E2E work, making this "not capable" assertion
	// flaky/false depending on unrelated pod state.
	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)
	// No full-Chrome build installed under mgr.InstallRoot() in this test's
	// tmp dir — ClassifyVideoCapability must report not_capable.
	require.False(t, browser.ClassifyVideoCapability(mgr.InstallRoot()).Capable)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig())

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available)
	require.Equal(t, "not_capable", got.Reason)
	require.Empty(t, state.webrtcAgentID)
}

// TestHandleWebRTCOffer_CapableButLaunchFails plants a fake (non-functional,
// merely executable-bit-set) "chrome" binary at the exact path
// ClassifyVideoCapability/findInstalledBuild look for, so the capability
// gate passes (Capable=true) — exercising the ladder's final branch
// ("ensure the capture session, then HandleViewerOffer") — WITHOUT a real
// Chromium: mgr.Session (called from defaultEncoderStarter, inside
// cs.Start) then tries to actually LAUNCH that fake binary over the CDP
// pipe, which fails fast (not a real Chrome process), so the session
// reports state=error rather than hanging. This is the closest unit-level
// proxy for the "ok" gate-ladder branch reachable without a real browser
// (real success is covered by the wave-3 Playwright e2e pass on the pod).
func TestHandleWebRTCOffer_CapableButLaunchFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux")
	}
	// ProfileDir isolation is DOUBLY load-bearing here (see the identical
	// note on TestHandleWebRTCOffer_GateLadder_NotCapable): this test also
	// WRITES a fake "chrome" binary into InstallRoot() below — without
	// isolation that write would permanently pollute this pod's real
	// $OMNIPUS_HOME/browser/chromium directory for every other test/process
	// on the box, not just leak state within this test run.
	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	fakeBin := filepath.Join(fakeBinDir, "chrome")
	require.NoError(t, os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	require.True(t, browser.ClassifyVideoCapability(installRoot).Capable, "capability gate must now report Capable=true")

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-1",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-1", "user-1", data, al.GetConfig())

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available, "a launch failure must degrade to available=false, never break the JPEG fallback")
	require.Equal(t, "error", got.Reason)
}

func TestHandleWebRTCOffer_DetachClearsViewerState(t *testing.T) {
	handler, al := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	// Fabricate a webrtc-attached state (as if handleWebRTCOffer had
	// succeeded) using a fake CaptureSession, and verify detachWebRTCViewer
	// tears it down cleanly — this is the readLoop defer / browser_detach
	// cleanup path (browser_ws.go), tested here without a live relay.
	var calls int32
	relay := &fakeRelay{}
	cs, err := browser.NewCaptureSessionWithDeps(nil, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	cs.AddViewer("viewer-1")

	state := browserConnState{webrtcAgentID: defaultAgent.ID, webrtcCapture: cs}
	handler.detachWebRTCViewer(&state, "viewer-1")

	require.Empty(t, state.webrtcAgentID)
	require.Nil(t, state.webrtcCapture)
	require.Equal(t, 0, cs.ViewerCount())
	relay.mu.Lock()
	closed := append([]string(nil), relay.closedViewers...)
	relay.mu.Unlock()
	require.Equal(t, []string{"viewer-1"}, closed)
}

// ---------------------------------------------------------------------------
// Input mapping parity (wave-plan W2-A item 4)
// ---------------------------------------------------------------------------

func TestBrowserInputFrameToLiveInput_TableParity(t *testing.T) {
	f64 := func(v float64) *float64 { return &v }
	str := func(v string) *string { return &v }
	i := func(v int) *int { return &v }

	cases := []struct {
		name  string
		frame generated.BrowserInputFrame
		want  browser.LiveInput
	}{
		{
			name:  "mouse_move with coords",
			frame: generated.BrowserInputFrame{Kind: "mouse_move", X: f64(12.5), Y: f64(30)},
			want:  browser.LiveInput{Kind: "mouse_move", X: 12.5, Y: 30, HasXY: true},
		},
		{
			name:  "mouse_down with button",
			frame: generated.BrowserInputFrame{Kind: "mouse_down", X: f64(1), Y: f64(2), Button: str("left")},
			want:  browser.LiveInput{Kind: "mouse_down", X: 1, Y: 2, HasXY: true, Button: "left"},
		},
		{
			name:  "wheel with deltas, no coords",
			frame: generated.BrowserInputFrame{Kind: "wheel", DeltaX: f64(-5), DeltaY: f64(10)},
			want:  browser.LiveInput{Kind: "wheel", DeltaX: -5, DeltaY: 10, HasXY: false},
		},
		{
			name: "key_down with key/code/keycode/modifiers",
			frame: generated.BrowserInputFrame{
				Kind: "key_down", Key: str("Enter"), Code: str("Enter"),
				KeyCode: i(13), Modifiers: i(2),
			},
			want: browser.LiveInput{Kind: "key_down", Key: "Enter", Code: "Enter", KeyCode: 13, Modifiers: 2},
		},
		{
			name:  "text",
			frame: generated.BrowserInputFrame{Kind: "text", Text: str("hello")},
			want:  browser.LiveInput{Kind: "text", Text: "hello"},
		},
		{
			name:  "navigate with url, no coords",
			frame: generated.BrowserInputFrame{Kind: "navigate", Url: str("https://example.com")},
			want:  browser.LiveInput{Kind: "navigate", URL: "https://example.com"},
		},
		{
			name:  "coordinate omitted entirely (HasXY must stay false, not silently 0,0)",
			frame: generated.BrowserInputFrame{Kind: "mouse_up"},
			want:  browser.LiveInput{Kind: "mouse_up", HasXY: false},
		},
		{
			name:  "explicit 0,0 coordinates ARE HasXY=true (distinguishable from omitted)",
			frame: generated.BrowserInputFrame{Kind: "mouse_move", X: f64(0), Y: f64(0)},
			want:  browser.LiveInput{Kind: "mouse_move", X: 0, Y: 0, HasXY: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := browserInputFrameToLiveInput(tc.frame)
			require.Equal(t, tc.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// captureRegistry
// ---------------------------------------------------------------------------

func TestCaptureRegistry_SetFindRemove(t *testing.T) {
	reg := newCaptureRegistry()

	var calls int32
	relayA := &fakeRelay{}
	csA, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", relayA, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	relayB := &fakeRelay{}
	csB, err := browser.NewCaptureSessionWithDeps(nil, "agent-b", relayB, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)

	reg.set("agent-a", csA)
	reg.set("agent-b", csB)

	gotID, gotCS := reg.findByToken(csA.TokenHex())
	require.Equal(t, "agent-a", gotID)
	require.Same(t, csA, gotCS)

	gotID, gotCS = reg.findByToken(csB.TokenHex())
	require.Equal(t, "agent-b", gotID)
	require.Same(t, csB, gotCS)

	gotID, gotCS = reg.findByToken("0000")
	require.Empty(t, gotID)
	require.Nil(t, gotCS)

	// removeIfCurrent with a stale reference must not remove the live entry.
	stale, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.removeIfCurrent("agent-a", stale)
	gotID, gotCS = reg.findByToken(csA.TokenHex())
	require.Equal(t, "agent-a", gotID)
	require.Same(t, csA, gotCS)

	reg.removeIfCurrent("agent-a", csA)
	gotID, gotCS = reg.findByToken(csA.TokenHex())
	require.Empty(t, gotID)
	require.Nil(t, gotCS)
}

// ---------------------------------------------------------------------------
// Capture-ingest WS loopback gate + token auth
// ---------------------------------------------------------------------------

func TestCaptureIngestWSHandler_RejectsNonLoopback(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	handler := newCaptureIngestWSHandler(al, newCaptureRegistry())

	req := httptest.NewRequest("GET", "/api/v1/browser/capture-ingest", nil)
	req.RemoteAddr = "8.8.8.8:54321"
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, 403, rec.Code, "a non-loopback RemoteAddr must be rejected before any WS upgrade")
}

func TestCaptureIngestWSHandler_TokenMismatchClosesConnection(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	reg := newCaptureRegistry()
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.set("agent-a", cs)

	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })

	hello := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      "0000000000000000000000000000000000000000000000000000000000000000",
		ExtVersion: "1.0.0",
	}
	data, err := json.Marshal(hello)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	_, _, readErr := conn.ReadMessage()
	require.Error(t, readErr, "a token-mismatched hello must close the connection with no further frames")
}

// TestCaptureIngestWSHandler_RejectsNonWebSocketUpgrade covers the
// ServeHTTP branch BETWEEN the loopback gate and the WS upgrade — a
// loopback request that never sets an Upgrade:websocket header must be
// rejected with 426 (Upgrade Required), never silently 200/close. Gap-sweep
// addition (W2-C): this specific branch had no coverage — only the
// non-loopback (403, TestCaptureIngestWSHandler_RejectsNonLoopback) and
// token-mismatch paths were tested.
func TestCaptureIngestWSHandler_RejectsNonWebSocketUpgrade(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	handler := newCaptureIngestWSHandler(al, newCaptureRegistry())

	req := httptest.NewRequest("GET", "/api/v1/browser/capture-ingest", nil)
	req.RemoteAddr = "127.0.0.1:54321" // loopback -- must pass the FIRST gate
	// Deliberately NO Upgrade header.
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, 426, rec.Code, "a loopback, non-WS-upgrade request must be rejected with 426 Upgrade Required")
}

// TestCaptureIngestWSHandler_MalformedFirstFrame_NotHello_ClosesConnection
// covers serveConn's "not_hello" branch: a first frame that is either
// invalid JSON or valid JSON with the wrong `type` (i.e. not
// browser_capture_hello) must close the connection immediately, exactly
// like a token mismatch — before ANY session lookup is attempted. Gap-sweep
// addition (W2-C): only the valid-hello/wrong-token path
// (TestCaptureIngestWSHandler_TokenMismatchClosesConnection) was covered;
// this proves the type-discriminator gate itself is enforced.
func TestCaptureIngestWSHandler_MalformedFirstFrame_NotHello_ClosesConnection(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	reg := newCaptureRegistry()
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.set("agent-a", cs)

	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })

	// A syntactically valid frame, but the WRONG type (not
	// browser_capture_hello) -- must be treated identically to garbage JSON.
	wrongType := generated.BrowserCaptureControlFrame{
		Type:   string(generated.WsFrameTypeBrowserCaptureControl),
		Action: "ping",
	}
	data, err := json.Marshal(wrongType)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	_, _, readErr := conn.ReadMessage()
	require.Error(t, readErr, "a non-hello first frame must close the connection with no further frames")
}

// TestCaptureIngestWSHandler_ValidateInbound_RejectsSchemaInvalidHello
// proves gateway.validate_inbound is actually WIRED on the capture-ingest
// socket (not just that the BrowserCaptureHelloFrame.yaml schema itself is
// correct in isolation -- see TestWebRTCFrameSchemasRoundTrip in
// browser_webrtc_contract_test.go for that): a hello frame with a
// schema-invalid token (shorter than the schema's minLength:16) must be
// rejected via the "schema_invalid" branch of serveConn, closing the
// connection, before token lookup ever runs -- distinct from a
// well-formed-but-wrong token (TestCaptureIngestWSHandler_TokenMismatchClosesConnection,
// which is a "token_mismatch" rejection reachable only once schema
// validation has already passed or is disabled). Gap-sweep addition (W2-C).
func TestCaptureIngestWSHandler_ValidateInbound_RejectsSchemaInvalidHello(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.ValidateInbound = true
	})
	reg := newCaptureRegistry()
	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close() })

	// token is well under BrowserCaptureHelloFrame.yaml's minLength:16 --
	// schema-invalid regardless of whether any session's real token matches.
	tooShort := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      "short",
		ExtVersion: "1.0.0",
	}
	data, err := json.Marshal(tooShort)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	conn.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	_, _, readErr := conn.ReadMessage()
	require.Error(t, readErr, "a schema-invalid hello (token too short) must close the connection when validate_inbound is enabled")
}

func TestCaptureIngestWSHandler_HelloSupersedesPreviousConnection(t *testing.T) {
	_, al := newBrowserWSTestHandler(t, nil)
	reg := newCaptureRegistry()
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(nil, "agent-a", &fakeRelay{}, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	reg.set("agent-a", cs)

	handler := newCaptureIngestWSHandler(al, reg)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	wsURL := "ws" + srv.URL[len("http"):] + "/api/v1/browser/capture-ingest"
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	hello := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      cs.TokenHex(),
		ExtVersion: "1.0.0",
	}
	data, err := json.Marshal(hello)
	require.NoError(t, err)

	conn1, resp1, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp1 != nil {
		resp1.Body.Close()
	}
	t.Cleanup(func() { _ = conn1.Close() })
	require.NoError(t, conn1.WriteMessage(websocket.TextMessage, data))

	// Give the server a moment to bind conn1 as the current ingest connection.
	time.Sleep(100 * time.Millisecond)

	conn2, resp2, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	if resp2 != nil {
		resp2.Body.Close()
	}
	t.Cleanup(func() { _ = conn2.Close() })
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, data))

	// conn1 must be closed by the server once conn2's hello supersedes it.
	conn1.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	_, _, readErr := conn1.ReadMessage()
	require.Error(t, readErr, "the OLD ingest connection must be closed once a second hello with the same token arrives")
}
