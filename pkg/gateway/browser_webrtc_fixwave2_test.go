// browser_webrtc_fixwave2_test.go — second fix-wave coverage for
// pkg/gateway/browser_webrtc.go, converged from the 14-reviewer review of
// the live-browser WebRTC epic (see the fix list in the fix-wave task
// prompt): fix 3 (ADR-048 condition-2 fence TOCTOU + mid-startup supersede
// snipe), fix 4 (watchdog RTP-progress staleness), fix 5 (capture-ingest
// write-deadline), and fix 7 (DC input schema-validation parity). Kept in a
// separate file per this project's own established convention
// (browser_webrtc_fixwave_test.go's header comment) rather than growing an
// existing file further. Mirrors browser_webrtc_test.go/
// browser_webrtc_fixwave_test.go's fixtures (newBrowserWSTestHandler,
// newFixWaveHandlerWithAudit, newTestBrowserWSConn/drainOneFrame/
// decodeWebRTCState, fakeRelay/fakeEncoderStarter).

package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// ---------------------------------------------------------------------------
// Fix 3a: captureFenceMu genuinely serializes the fence-check + ensure
// section (the TOCTOU fix).
// ---------------------------------------------------------------------------

// TestHandleWebRTCOffer_CaptureFenceMu_SerializesFenceCheckAndEnsure proves
// h.captureFenceMu (browser_ws.go's BrowserWSHandler field) actually guards
// handleWebRTCOffer's fence-check+ensureCaptureSession section: while the
// test holds the mutex itself (standing in for a concurrent offer from a
// DIFFERENT agent that got there first), a second handleWebRTCOffer call
// must block rather than racing ahead — the exact TOCTOU window that let two
// concurrent first-offers for different agents both observe an empty
// h.captures snapshot before fix 3a made the fence-check+registration
// atomic.
func TestHandleWebRTCOffer_CaptureFenceMu_SerializesFenceCheckAndEnsure(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// "Should be true" here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}
	// Force the exec resolver onto the managed install path (the fake binary
	// below), bypassing its $PATH lookup of a real google-chrome/chromium —
	// a runner with real Chrome installed (GH Actions ubuntu-latest images
	// do) would otherwise resolve to it INSTEAD of the fake binary this test
	// relies on for a deterministic launch failure past the fence. See
	// exec_resolver.go's execPathCaches.resolve doc comment.
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "1")
	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.CaptureSharedContext = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	// Capability gate must pass so the ladder actually reaches
	// h.captureFenceMu.Lock() instead of returning earlier at "not_capable".
	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "chrome"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.True(t, browser.ClassifyVideoCapability(installRoot).Capable)

	// Stand in for "another agent's offer already holds the fence" by
	// grabbing captureFenceMu directly (white-box, same package).
	handler.captureFenceMu.Lock()

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-fence-mutex",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		handler.handleWebRTCOffer(wc, &state, "viewer-fence-mutex", "user-1", data, al.GetConfig(), 0)
		close(done)
	}()

	select {
	case <-done:
		handler.captureFenceMu.Unlock()
		t.Fatal(
			"handleWebRTCOffer completed its fence-check+ensure section while captureFenceMu was held externally — the critical section is not actually serialized (TOCTOU fix 3a regressed)",
		)
	case <-time.After(300 * time.Millisecond):
		// Expected: still blocked on the mutex.
	}

	handler.captureFenceMu.Unlock()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("handleWebRTCOffer never completed after captureFenceMu was released")
	}
	// This chrome-less harness then fails at launch (captureStartTimeout) —
	// this test only cares that it PROCEEDED, not what it produced.
	drainOneFrame(t, wc)
}

// ---------------------------------------------------------------------------
// Fix 3b: a still-starting capture session is skipped, never superseded —
// the "mid-startup supersede snipe" fix.
// ---------------------------------------------------------------------------

// TestHandleWebRTCOffer_OtherAgentStartingCapture_SkippedNotSuperseded is
// the deterministic proof for fix 3b: another agent's capture session that
// is still INSIDE its own Start() call (cs.IsStarting()==true) — the exact
// window where AddViewer has not run yet, so the session LOOKS viewerless to
// the pre-fix logic — must be skipped by the ADR-048 condition-2 fence, not
// denied against or superseded (Stop()'d). Deterministic via a blocked fake
// EncoderStarter (no timing race needed): otherCS's Start() is held open on
// a channel for the whole test.
func TestHandleWebRTCOffer_OtherAgentStartingCapture_SkippedNotSuperseded(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// "Should be true" here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}
	// See TestHandleWebRTCOffer_CaptureFenceMu_SerializesFenceCheckAndEnsure's
	// identical Setenv for why.
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "1")
	tmpDir := t.TempDir()
	handler, al, auditDir := newFixWaveHandlerWithAudit(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.CaptureSharedContext = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "chrome"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.True(t, browser.ClassifyVideoCapability(installRoot).Capable)

	startCalled := make(chan struct{})
	releaseStart := make(chan struct{})
	t.Cleanup(func() { close(releaseStart) })
	starter := browser.EncoderStarter(
		func(context.Context, *browser.BrowserManager, string, string, string) (context.Context, context.CancelFunc, error) {
			close(startCalled)
			<-releaseStart
			ctx, cancel := context.WithCancel(context.Background())
			return ctx, cancel, nil
		},
	)
	otherCS, err := browser.NewCaptureSessionWithDeps(nil, "other-agent-starting", &fakeRelay{}, starter, nil)
	require.NoError(t, err)
	handler.captures.set("other-agent-starting", otherCS)

	go func() {
		_, _ = otherCS.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	}()

	select {
	case <-startCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("otherCS.Start() never invoked its EncoderStarter")
	}
	require.True(t, otherCS.IsStarting(), "otherCS must be observed mid-Start() at this point")
	require.Zero(
		t,
		otherCS.ViewerCount(),
		"otherCS must look viewerless — AddViewer has not run yet, exactly the pre-fix false-supersede trigger",
	)

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-skip-starting",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	handler.handleWebRTCOffer(wc, &state, "viewer-skip-starting", "user-1", data, al.GetConfig(), 0)

	// otherCS must NOT have been superseded/stopped.
	select {
	case <-otherCS.Done():
		t.Fatal("a still-starting capture session must be SKIPPED, not superseded/stopped (fix 3b regressed)")
	default:
	}

	// The requesting agent's offer must not have been DENIED at the fence
	// either — a starting session is invisible to the fence decision
	// entirely, it just proceeds (and then fails at this chrome-less
	// harness's launch step, same terminal state as every sibling launch-
	// failure test). Distinguish "denied at the fence" from "proceeded past
	// it" via the audit reason field: both paths use
	// EventBrowserWebRTCStreamStartFailed, but only the fence-denial branch
	// sets reason=multi_agent_capture_denied.
	for _, r := range readBrowserAuditRecords(t, auditDir) {
		if r.Event == audit.EventBrowserWebRTCStreamStartFailed {
			assert.NotEqual(
				t,
				"multi_agent_capture_denied",
				r.Fields["reason"],
				"the requesting agent's offer must not be denied at the fence just because another agent's session is still starting",
			)
		}
	}

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(
		t,
		got.Available,
		"this chrome-less harness must fail at launch (proved it proceeded PAST the fence, not denied by it)",
	)
}

// ---------------------------------------------------------------------------
// Fix 4: encoder-liveness watchdog RTP-progress staleness (a dead capture
// with a live ping beacon).
// ---------------------------------------------------------------------------

// TestWatchEncoderLiveness_StopsSession_WhenVideoPacketsFrozenDespiteFreshPings
// proves fix 4: the watchdog now also stops a session whose Stats().
// VideoPackets never advances across consecutive ticks while a viewer is
// attached — even when the ping beacon stays perfectly fresh throughout
// (encoderLivenessStaleAfter is set to 24h so THAT signal alone could never
// fire in this test window), reproducing the "dead-capture encoder with a
// live ping beacon defeats the old watchdog" finding.
func TestWatchEncoderLiveness_StopsSession_WhenVideoPacketsFrozenDespiteFreshPings(t *testing.T) {
	origInterval, origStale := encoderLivenessCheckInterval, encoderLivenessStaleAfter
	encoderLivenessCheckInterval = 5 * time.Millisecond
	encoderLivenessStaleAfter = 24 * time.Hour // isolates the RTP-progress signal
	t.Cleanup(func() {
		encoderLivenessCheckInterval = origInterval
		encoderLivenessStaleAfter = origStale
	})

	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	relay := &fakeRelay{}
	relay.setStats(webrtc.Stats{HasVideo: true, VideoPackets: 100}) // frozen for the whole test

	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(
		nil,
		"watchdog-stall-agent",
		relay,
		fakeEncoderStarter(&calls, nil),
		nil,
	)
	require.NoError(t, err)
	_, err = cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	require.NoError(t, err)
	cs.AddViewer("viewer-stall") // ViewerCount() > 0 is required to engage the stall check

	var onStoppedCalls int32
	cs.SetOnStopped(func() { atomic.AddInt32(&onStoppedCalls, 1) })

	// Keep the ping beacon alive throughout, faster than the check interval
	// — proves the stop is driven by the RTP-stall signal, not staleAfter.
	pingStop := make(chan struct{})
	t.Cleanup(func() { close(pingStop) })
	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-pingStop:
				return
			case <-ticker.C:
				cs.RecordPing()
			}
		}
	}()

	go handler.watchEncoderLiveness(cs, "watchdog-stall-agent", encoderLivenessCheckInterval, encoderLivenessStaleAfter)

	require.Eventually(
		t,
		func() bool { return atomic.LoadInt32(&onStoppedCalls) == 1 },
		2*time.Second,
		5*time.Millisecond,
		"watchdog must stop a session whose VideoPackets never advances across consecutive ticks with an attached viewer, despite a fresh ping beacon",
	)
}

// TestWatchEncoderLiveness_DoesNotStop_WhenVideoPacketsAdvancing is the
// negative control for fix 4: a relay whose VideoPackets keeps climbing must
// never trip the new stall check.
func TestWatchEncoderLiveness_DoesNotStop_WhenVideoPacketsAdvancing(t *testing.T) {
	origInterval, origStale := encoderLivenessCheckInterval, encoderLivenessStaleAfter
	encoderLivenessCheckInterval = 5 * time.Millisecond
	encoderLivenessStaleAfter = 24 * time.Hour
	t.Cleanup(func() {
		encoderLivenessCheckInterval = origInterval
		encoderLivenessStaleAfter = origStale
	})

	handler, _ := newBrowserWSTestHandler(t, nil)
	t.Cleanup(handler.Wait)

	relay := &fakeRelay{}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(
		nil,
		"watchdog-progress-agent",
		relay,
		fakeEncoderStarter(&calls, nil),
		nil,
	)
	require.NoError(t, err)
	_, err = cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	require.NoError(t, err)
	cs.AddViewer("viewer-progress")

	var onStoppedCalls int32
	cs.SetOnStopped(func() { atomic.AddInt32(&onStoppedCalls, 1) })

	progressStop := make(chan struct{})
	t.Cleanup(func() { close(progressStop) })
	go func() {
		var pkts int64
		ticker := time.NewTicker(1 * time.Millisecond) // faster than the 5ms check interval
		defer ticker.Stop()
		for {
			select {
			case <-progressStop:
				return
			case <-ticker.C:
				pkts++
				relay.setStats(webrtc.Stats{HasVideo: true, VideoPackets: pkts})
				cs.RecordPing()
			}
		}
	}()

	watchdogDone := make(chan struct{})
	checkInterval, staleAfter := encoderLivenessCheckInterval, encoderLivenessStaleAfter
	go func() {
		handler.watchEncoderLiveness(cs, "watchdog-progress-agent", checkInterval, staleAfter)
		close(watchdogDone)
	}()
	t.Cleanup(cs.Stop)

	time.Sleep(300 * time.Millisecond) // ~60 check ticks with continuous packet progress
	require.Zero(
		t,
		atomic.LoadInt32(&onStoppedCalls),
		"watchdog must not stop a session whose VideoPackets keeps advancing",
	)

	select {
	case <-watchdogDone:
		t.Fatal("watchdog exited on its own without ever being stopped")
	default:
	}
}

// ---------------------------------------------------------------------------
// Fix 5: capture-ingest write deadline bounds a wedged/backpressured socket.
// ---------------------------------------------------------------------------

// TestCaptureIngestConn_SendJSON_WriteDeadlineBoundsWedgedWrite proves fix 5:
// captureIngestConn.sendJSON sets a write deadline before every write, so a
// peer that stops reading entirely (a wedged/crashed encoder — exactly what
// this simulates by dialing a real WS connection and then never calling
// ReadMessage again) makes sendJSON FAIL within captureIngestWriteTimeout
// rather than blocking forever. Before this fix, CaptureSession.Stop()'s
// synchronous shutdown-control write (capture_session.go's requestControl)
// could wedge the ENTIRE stop path on exactly this scenario.
func TestCaptureIngestConn_SendJSON_WriteDeadlineBoundsWedgedWrite(t *testing.T) {
	origTimeout := captureIngestWriteTimeout
	captureIngestWriteTimeout = 150 * time.Millisecond
	t.Cleanup(func() { captureIngestWriteTimeout = origTimeout })

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srvConnCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		srvConnCh <- conn
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, resp, dialErr := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, dialErr)
	if resp != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { clientConn.Close() })
	// Shrink the client's receive buffer so a modest amount of unread data
	// exhausts the connection's effective capacity quickly — realistic
	// capture-ingest control frames are small, and this test must stay fast
	// and deterministic rather than writing megabytes to fill a default
	// loopback buffer.
	if tcpConn, ok := clientConn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcpConn.SetReadBuffer(1)
	}

	var srvConn *websocket.Conn
	select {
	case srvConn = <-srvConnCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server never completed the WS upgrade")
	}
	t.Cleanup(func() { srvConn.Close() })
	if tcpConn, ok := srvConn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcpConn.SetWriteBuffer(1)
	}

	ic := &captureIngestConn{conn: srvConn}

	// The client deliberately never reads again from here on — exactly a
	// wedged/backpressured encoder socket. Hammer sendJSON with a moderate
	// payload until backpressure kicks in (an unread WS connection alone
	// doesn't reliably block a single small write on typical loopback
	// buffer sizes).
	type fillerPayload struct {
		Type   string `json:"type"`
		Action string `json:"action"`
		Filler string `json:"filler"`
	}
	payload := fillerPayload{Type: "browser_capture_control", Action: "ping", Filler: strings.Repeat("x", 4096)}

	errCh := make(chan error, 1)
	go func() {
		var lastErr error
		for i := 0; i < 500; i++ {
			lastErr = ic.sendJSON(payload)
			if lastErr != nil {
				errCh <- lastErr
				return
			}
		}
		errCh <- nil
	}()

	select {
	case sendErr := <-errCh:
		require.Error(
			t,
			sendErr,
			"sendJSON must eventually fail once the wedged peer stops draining the connection (never got backpressure within 500 writes — increase payload/iterations if this flakes)",
		)
		require.Contains(
			t,
			sendErr.Error(),
			"capture-ingest: write frame",
			"the failure must surface as the write-frame error path",
		)
	case <-time.After(15 * time.Second):
		t.Fatal(
			"sendJSON never returned — SetWriteDeadline is not bounding the write (this is exactly the fix-wave HIGH bug: a wedged encoder socket blocking forever, which would wedge CaptureSession.Stop() permanently)",
		)
	}
}

// ---------------------------------------------------------------------------
// Fix 7: DC input schema-validation parity with the WS input path.
// ---------------------------------------------------------------------------

// TestWebrtcInputSink_ValidateInbound_RejectsOversizedTextField proves fix 7:
// with cfg.Gateway.ValidateInbound enabled, webrtcInputSink now
// schema-validates every inbound data-channel frame BEFORE dispatch — an
// oversized "text" field (contracts/components/schemas/BrowserInputFrame.yaml
// caps it at 8192) is dropped silently (no browser_status frame at all,
// proven by the absence of ANY frame on the viewer's connection), never
// reaching mgr.Live().Input(). The control case (a valid-size frame through
// the SAME sink) DOES reach dispatch — proven by the "no active live view"
// non-benign error surfacing as a browser_status frame, exactly like
// TestWebrtcInputSink_NonBenignError_SurfacedToViewer — showing validation
// isn't just blackholing everything.
func TestWebrtcInputSink_ValidateInbound_RejectsOversizedTextField(t *testing.T) {
	browserCfg, err := browser.DefaultConfig()
	require.NoError(t, err)
	browserCfg.ProfileDir = t.TempDir()
	mgr, err := browser.NewBrowserManager(browserCfg, security.NewSSRFChecker(nil))
	require.NoError(t, err)

	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.ValidateInbound = true
	})
	t.Cleanup(handler.Wait)

	wc := newTestBrowserWSConn()
	handler.registerWebRTCViewerConn("viewer-oversized-text", wc, "sess-oversized-text")
	t.Cleanup(func() { handler.unregisterWebRTCViewerConn("viewer-oversized-text") })

	sink := handler.webrtcInputSink(mgr, al.GetConfig())

	oversized := generated.BrowserInputFrame{
		Type: "browser_input",
		Kind: "text",
		Text: strPtr(strings.Repeat("a", 8193)), // schema maxLength is 8192
	}
	rawOversized, err := json.Marshal(oversized)
	require.NoError(t, err)

	sink("viewer-oversized-text", rawOversized)

	select {
	case frame := <-wc.sendCh:
		t.Fatalf("an oversized text field must be dropped at schema validation, not dispatched (got frame: %s)", frame)
	case <-time.After(200 * time.Millisecond):
	}

	// Control: a valid-size frame through the SAME sink must still reach
	// dispatch.
	valid := generated.BrowserInputFrame{Type: "browser_input", Kind: "text", Text: strPtr("ok")}
	rawValid, err := json.Marshal(valid)
	require.NoError(t, err)
	sink("viewer-oversized-text", rawValid)

	frame := drainOneFrame(t, wc)
	var f struct {
		Type    string `json:"type"`
		State   string `json:"state"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(frame, &f))
	require.Equal(t, "browser_status", f.Type)
	require.Equal(t, "error", f.State)
	require.Contains(t, f.Message, "browser input failed")
}
