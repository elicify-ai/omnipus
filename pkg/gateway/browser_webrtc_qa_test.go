//go:build !cgo

// browser_webrtc_qa_test.go — QA wave additions closing coverage gaps NOT
// exercised by TestWebRTCEndToEndInProcess (browser_webrtc_e2e_test.go):
//
//   - input round-trip LATENCY end to end (that file only proves "a frame
//     eventually arrived", never how long that took);
//   - exact PAYLOAD INTEGRITY (that file's own doc comment says assertion
//     (c) is "observed at the deepest seam reachable... "; it counts
//     dcFramesObserved > 0 but never checks the payload sent is the payload
//     received).
//
// Reuses the SAME in-process, no-Chrome harness pattern
// TestWebRTCEndToEndInProcess established (real gateway browser-WS handler +
// real capture-ingest handler + real Pion relay Session + a
// fake-only-the-CDP-launch encoder + a real Pion viewer), and every
// package-level helper that file defines (e2eWait, e2eSafeLogf,
// e2eNonTrickleOffer, e2eWaitCond, startE2EFakeEncoder, e2eFakeEncoder,
// newE2EFakeViewer, e2eFakeViewer, wsTypeOnly, webrtcStateFrameDecoder) and
// browser_ws_test.go defines (newBrowserWSTestHandler, dialBrowserTestWS,
// writeBrowserAuthFrame) rather than re-deriving them. The ORIGINAL
// TestWebRTCEndToEndInProcess function is left completely untouched by this
// file — everything here is additive.
//
// Traces to: QA wave task "TDD WAVE — live-browser WebRTC video feature"
// item 1 ("Input round-trip latency + payload integrity"); ADR-047 FR-A2 /
// Q4 spike (docs/internal/architecture/ADR-047-live-browser-webrtc.md lines
// 41, 54).
//
// RESOURCE RULE: run ONLY narrowly, one test at a time —
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestWebRTCInputRoundTrip_LatencyAndPayloadIntegrity$' -p 1 ./pkg/gateway/
//
// never the full gateway suite / ./....

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/gorilla/websocket"
	pion "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// qaInputHarness bundles the handles a QA input test needs once the fake
// viewer's "input" data channel has opened and is ready to send on.
type qaInputHarness struct {
	viewerConn *websocket.Conn
	viewer     *e2eFakeViewer
	cs         *browser.CaptureSession
}

// newQAInputHarness stands up the SAME real-signaling / real-relay /
// fake-CDP-launch-only topology TestWebRTCEndToEndInProcess uses, wires
// dcRecorder ALONGSIDE (never instead of) the REAL production
// handler.webrtcInputSink(mgr, cfg) — exactly as TestWebRTCEndToEndInProcess
// wraps it for its own dcFramesObserved counter — and returns only once the
// viewer's "input" data channel has opened. dcRecorder may be nil.
func newQAInputHarness(t *testing.T, dcRecorder func(viewerID string, raw []byte)) *qaInputHarness {
	t.Helper()

	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// "capability gate must report Capable=true" here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}

	// Reserve a real TCP listener FIRST so cfg.Gateway.Port matches the
	// ACTUAL port this test's httptest server binds to — see
	// TestWebRTCEndToEndInProcess's identical comment for why.
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr, ok := lst.Addr().(*net.TCPAddr)
	require.Truef(t, ok, "listener address is %T, want *net.TCPAddr", lst.Addr())
	port := addr.Port

	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.Port = port
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)

	// Plant a fake, non-functional "chrome" binary so the capability gate in
	// handleWebRTCOffer reports Capable=true — it is NEVER executed: capture
	// start is fully faked via the EncoderStarter seam below.
	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "chrome"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.True(t, browser.ClassifyVideoCapability(installRoot).Capable, "capability gate must report Capable=true")

	ingestHandler := newCaptureIngestWSHandler(al, handler.captures)
	t.Cleanup(ingestHandler.Wait)

	mux := http.NewServeMux()
	mux.Handle("/api/v1/browser/ws", handler)
	mux.Handle("/api/v1/browser/capture-ingest", ingestHandler)

	srv := httptest.NewUnstartedServer(mux)
	require.NoError(t, srv.Listener.Close())
	srv.Listener = lst
	srv.Start()
	t.Cleanup(srv.Close)

	pumpStop := make(chan struct{})
	t.Cleanup(func() { close(pumpStop) })

	fakeStarter := browser.EncoderStarter(
		func(_ context.Context, _ *browser.BrowserManager, tokenHex, ingestURL, _ string) (context.Context, context.CancelFunc, error) {
			enc, startErr := startE2EFakeEncoder(ingestURL, tokenHex)
			if startErr != nil {
				return nil, nil, fmt.Errorf("qa fake encoder: %w", startErr)
			}
			enc.startPumping(pumpStop)
			tabCtx, cancel := context.WithCancel(context.Background())
			return tabCtx, cancel, nil
		},
	)

	// dcRecorder observes every frame ALONGSIDE the real production sink —
	// it never replaces it, so this test proves the frame genuinely
	// traveled through webrtcInputSink -> browserInputFrameToLiveInput ->
	// mgr.Live().Input, exactly like TestWebRTCEndToEndInProcess's
	// dcFramesObserved counter, just with the raw bytes retained.
	realSink := handler.webrtcInputSink(mgr, al.GetConfig())
	sink := webrtc.InputSink(func(viewerID string, raw []byte) {
		realSink(viewerID, raw)
		if dcRecorder != nil {
			dcRecorder(viewerID, raw)
		}
	})

	relay := webrtc.NewSession(webrtc.Config{StunServer: ""}, sink, e2eSafeLogf(t.Name()))

	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeStarter, e2eSafeLogf(t.Name()))
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	cs.SetOnStopped(func() { handler.captures.removeIfCurrent(defaultAgent.ID, cs) })

	_, err = mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	handler.captures.set(defaultAgent.ID, cs)

	viewerConn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = viewerConn.Close() })
	writeBrowserAuthFrame(t, viewerConn, "dev-token")

	viewer := newE2EFakeViewer(t)
	t.Cleanup(func() { _ = viewer.pc.Close() })

	offerV := e2eNonTrickleOffer(t, viewer.pc)
	offerFrame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       offerV,
		SessionId: "qa-input-session",
	}
	offerData, err := json.Marshal(offerFrame)
	require.NoError(t, err)
	require.NoError(t, viewerConn.WriteMessage(websocket.TextMessage, offerData))

	viewerConn.SetReadDeadline(time.Now().Add(e2eWait))
	_, firstData, err := viewerConn.ReadMessage()
	require.NoError(t, err, "viewer: read first response frame")
	var firstType wsTypeOnly
	require.NoError(t, json.Unmarshal(firstData, &firstType))
	if firstType.Type == string(generated.WsFrameTypeBrowserWebrtcState) {
		var early webrtcStateFrameDecoder
		require.NoError(t, json.Unmarshal(firstData, &early))
		t.Fatalf(
			"handleWebRTCOffer failed before answering (available=%v reason=%q) — fake encoder start error, see logs",
			early.Available,
			early.Reason,
		)
	}
	require.Equal(t, string(generated.WsFrameTypeBrowserWebrtcAnswer), firstType.Type)
	var answerFrame generated.BrowserWebRTCAnswerFrame
	require.NoError(t, json.Unmarshal(firstData, &answerFrame))
	require.NotEmpty(t, answerFrame.Sdp)

	viewerConn.SetReadDeadline(time.Now().Add(e2eWait))
	_, stateData, err := viewerConn.ReadMessage()
	require.NoError(t, err, "viewer: read browser_webrtc_state")
	var stateFrame webrtcStateFrameDecoder
	require.NoError(t, json.Unmarshal(stateData, &stateFrame))
	require.Equal(t, string(generated.WsFrameTypeBrowserWebrtcState), stateFrame.Type)
	require.True(t, stateFrame.Available, "browser_webrtc_state.available must be true on a successful offer")

	require.NoError(
		t,
		viewer.pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answerFrame.Sdp}),
	)

	select {
	case <-viewer.dcOpen:
	case <-time.After(e2eWait):
		t.Fatal("input data channel did not open")
	}

	return &qaInputHarness{viewerConn: viewerConn, viewer: viewer, cs: cs}
}

// testInputDispatchBudget bounds the elapsed time between a viewer sending a
// browser_input frame on the "input" data channel and that EXACT frame
// reaching the production webrtcInputSink (real DC transport ->
// inputQueueCapacity queue -> single worker -> JSON parse ->
// browserInputFrameToLiveInput -> mgr.Live().Input dispatch).
//
// Evidence for the value: ADR-047's Q4 spike measured REAL CDP round trips
// (browser -> gateway -> real Chrome DevTools Protocol -> back) under a 30s /
// ~2200-event stress run at p50 10.9ms / p95 21.0ms / max 88.4ms
// (docs/internal/architecture/ADR-047-live-browser-webrtc.md line 54). This
// test's path does NOT include a real CDP call at all — there is no
// attached live view / no real Chrome tab in this harness, so
// mgr.Live().Input returns a benign "not attached" error almost immediately
// (see browser.IsBenignLiveInputError) — meaning this test measures a
// STRICT SUBSET of the real production path (DC transport + queue/worker
// handoff + JSON decode + conversion, minus the CDP call itself), which
// should complete far faster than the real-CDP numbers above.
//
// 300ms is chosen as roughly 3.4x the real-CDP OBSERVED MAX (88.4ms) —
// generous enough to absorb scheduler jitter on this pod's shared,
// resource-constrained CPUs (2-4 vCPUs per CLAUDE.md) without flaking on a
// loaded box, while still tight enough to catch a genuine regression:
// anything pushing this in-process, no-CDP dispatch into the hundreds of
// milliseconds would indicate a real stall (lock contention, an
// accidentally-introduced blocking call, or a broken non-blocking guarantee
// in the queue/worker path) rather than ordinary scheduling noise.
const testInputDispatchBudget = 300 * time.Millisecond

// TestWebRTCInputRoundTrip_LatencyAndPayloadIntegrity closes the two gaps
// TestWebRTCEndToEndInProcess's assertion (c) deliberately left open — see
// that test's own doc comment: "observed at the deepest seam reachable...";
// it only asserts dcFramesObserved > 0 (SOME frame arrived), never that the
// frame RECEIVED is the frame SENT, and never measures how long the round
// trip took. This test asserts both, over the SAME real
// DC -> queue -> worker -> webrtcInputSink production path, and sends a
// SECOND, differently-valued frame to prove the sink is not echoing a
// hardcoded/constant payload (a differentiation check — two different
// inputs must produce two differently-shaped observations).
func TestWebRTCInputRoundTrip_LatencyAndPayloadIntegrity(t *testing.T) {
	type observedFrame struct {
		viewerID string
		raw      []byte
		at       time.Time
	}
	recvCh := make(chan observedFrame, 4)
	h := newQAInputHarness(t, func(viewerID string, raw []byte) {
		recvCh <- observedFrame{viewerID: viewerID, raw: append([]byte(nil), raw...), at: time.Now()}
	})

	recvNext := func(timeout time.Duration) observedFrame {
		t.Helper()
		select {
		case f := <-recvCh:
			return f
		case <-time.After(timeout):
			t.Fatal("input frame never reached webrtcInputSink")
			return observedFrame{}
		}
	}

	// --- Frame 1: a distinctive payload unlikely to collide with any
	// zero-value default — non-integer coordinates and a modifiers bit
	// combination (Alt=1 | Shift=8 = 9, per browser.LiveInput's documented
	// bit field: Alt=1, Ctrl=2, Meta=4, Shift=8). ---
	x1, y1 := 123.456, -987.654
	modifiers1 := 9
	frame1 := generated.BrowserInputFrame{
		Type:      "browser_input",
		Kind:      "mouse_move",
		X:         &x1,
		Y:         &y1,
		Modifiers: &modifiers1,
	}
	data1, err := json.Marshal(frame1)
	require.NoError(t, err)

	sendAt1 := time.Now()
	require.NoError(t, h.viewer.inputDC.SendText(string(data1)))
	got1 := recvNext(e2eWait)
	elapsed1 := got1.at.Sub(sendAt1)

	// (a) Payload integrity: byte-for-byte AND field-for-field.
	require.Equal(t, string(data1), string(got1.raw),
		"sink must receive the EXACT bytes sent, byte for byte — not a re-encoded or hardcoded substitute")
	var decoded1 generated.BrowserInputFrame
	require.NoError(t, json.Unmarshal(got1.raw, &decoded1))
	require.Equal(t, "mouse_move", decoded1.Kind)
	require.NotNil(t, decoded1.X)
	require.NotNil(t, decoded1.Y)
	require.Equal(t, x1, *decoded1.X)
	require.Equal(t, y1, *decoded1.Y)
	require.NotNil(t, decoded1.Modifiers)
	require.Equal(t, modifiers1, *decoded1.Modifiers)
	require.NotEmpty(t, got1.viewerID)

	// (b) Latency budget.
	require.Lessf(
		t,
		elapsed1,
		testInputDispatchBudget,
		"input dispatch took %s, want < %s (see testInputDispatchBudget's doc comment for the evidence behind this budget)",
		elapsed1,
		testInputDispatchBudget,
	)
	t.Logf(
		"OBSERVED input round-trip dispatch latency (frame 1, mouse_move): %s (budget %s)",
		elapsed1,
		testInputDispatchBudget,
	)

	// --- Frame 2: a DIFFERENT kind, DIFFERENT field set, no coordinates at
	// all — a hardcoded/stubbed sink that just echoed frame 1's shape back
	// (or ignored its input entirely) would fail one of the assertions
	// below. This is the anti-shortcut "differentiation" check: two
	// different inputs must produce two differently-shaped observations. ---
	key2 := "a"
	code2 := "KeyA"
	keyCode2 := 65
	frame2 := generated.BrowserInputFrame{
		Type:    "browser_input",
		Kind:    "key_down",
		Key:     &key2,
		Code:    &code2,
		KeyCode: &keyCode2,
	}
	data2, err := json.Marshal(frame2)
	require.NoError(t, err)

	sendAt2 := time.Now()
	require.NoError(t, h.viewer.inputDC.SendText(string(data2)))
	got2 := recvNext(e2eWait)
	elapsed2 := got2.at.Sub(sendAt2)

	require.Equal(t, string(data2), string(got2.raw))
	var decoded2 generated.BrowserInputFrame
	require.NoError(t, json.Unmarshal(got2.raw, &decoded2))
	require.Equal(t, "key_down", decoded2.Kind)
	require.NotEqual(
		t,
		decoded1.Kind,
		decoded2.Kind,
		"the two frames must be observed as genuinely different, not a hardcoded echo",
	)
	require.Nil(
		t,
		decoded2.X,
		"key_down frame must carry no X — a hardcoded responder echoing frame 1's shape would fail this",
	)
	require.NotNil(t, decoded2.Key)
	require.Equal(t, key2, *decoded2.Key)
	require.NotNil(t, decoded2.Code)
	require.Equal(t, code2, *decoded2.Code)
	require.NotNil(t, decoded2.KeyCode)
	require.Equal(t, keyCode2, *decoded2.KeyCode)

	require.Lessf(t, elapsed2, testInputDispatchBudget,
		"second frame's input dispatch took %s, want < %s", elapsed2, testInputDispatchBudget)
	t.Logf(
		"OBSERVED input round-trip dispatch latency (frame 2, key_down): %s (budget %s)",
		elapsed2,
		testInputDispatchBudget,
	)
}
