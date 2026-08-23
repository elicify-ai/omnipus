// browser_webrtc_e2e_test.go — ADR-047 / wave-plan W2-C: the centerpiece
// integration test proving the FULL WebRTC signaling + media + input path
// end-to-end, IN-PROCESS, with NO Chrome involved:
//
//   - a REAL gateway browser-WS handler (BrowserWSHandler, /api/v1/browser/ws)
//   - a REAL capture-ingest WS handler (captureIngestWSHandler,
//     /api/v1/browser/capture-ingest), served on the SAME httptest server
//   - a REAL Pion-backed webrtc.Session (host-only ICE — Config.StunServer
//     is empty, per wave-plan decision 7)
//
// The only faked piece is the encoder-PAGE LAUNCH itself, via the
// browser.EncoderStarter seam CaptureSession exposes exactly for this
// purpose (see capture_session.go's doc comment on EncoderStarter and
// wave-plan W2-A's "fake webrtc.Session behind a narrow interface" note,
// which browser_webrtc_test.go already exploits for handler-level tests with
// a fakeRelay — this file goes one step further and fakes only the CHROME
// launch, keeping the relay 100% real). In place of driving real CDP, the
// starter spins up a goroutine that plays the encoder role with its OWN real
// Pion PeerConnection: it dials the real capture-ingest WS, sends
// browser_capture_hello, offers one video + one audio track, and applies the
// real answer — exactly what captureext/embedded/encoder.js does against a
// real Chrome tabCapture MediaStream. A second, fully independent Pion
// PeerConnection plays a real external-browser viewer, dialing the real
// browser WS and exercising the exact signaling browserWebRTC.ts performs
// (offer → answer, "input" data channel).
//
// Goroutine-safety note: EncoderStarter runs on the SERVER's own per-
// connection goroutine (BrowserWSHandler.readLoop, dispatching the viewer's
// browser_webrtc_offer) — NOT this test function's goroutine. Go's testing
// package requires t.FailNow (which require/assert call on failure) to run
// ONLY on the test's own goroutine, so every helper reachable from
// startE2EFakeEncoder returns plain errors instead of taking *testing.T; a
// startup failure there surfaces to THIS test's own goroutine as an ordinary
// browser_webrtc_state{available:false,reason:"error"} frame over the wire,
// which is asserted on normally. Similarly Session's own async ICE/DTLS
// teardown goroutines can outlive this test function, so logf never uses
// t.Logf (which panics if called post-test) — see e2eSafeLogf.
//
// Traces to: docs/internal/design/webrtc-build/wave-plan.md row W2-C;
// docs/internal/architecture/ADR-047-live-browser-webrtc.md D1/D3/D4/D6.
//
// RESOURCE RULE: run ONLY narrowly —
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestWebRTCEndToEndInProcess$' -p 1 ./pkg/gateway/
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	pion "github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// e2eWait bounds every blocking wait in this file (ICE gathering, WS reads,
// polling loops) — generous for a loopback-only exchange but bounded so a
// wedged connection fails the test instead of hanging the run.
const e2eWait = 20 * time.Second

// e2eSafeLogf mirrors pkg/tools/browser/webrtc's session_test.go safeLogf:
// Session's internal goroutines can fire log lines after this test function
// has already returned (t.Logf panics in that case), so this writes straight
// to stderr instead.
func e2eSafeLogf(name string) func(string, ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "["+name+"] "+format+"\n", args...)
	}
}

// ---------------------------------------------------------------------------
// Small Pion helpers. Two flavors of the same operations are needed: a
// *testing.T-based flavor (safe ONLY on this test function's own goroutine —
// used for the fake VIEWER, which the test drives directly) and a plain
// error-returning flavor (safe on ANY goroutine — used for the fake ENCODER,
// which runs inside the EncoderStarter callback on the server's own
// goroutine; see the file doc comment above).
// ---------------------------------------------------------------------------

func e2eNonTrickleOfferPlain(pc *pion.PeerConnection, timeout time.Duration) (string, error) {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("create offer: %w", err)
	}
	gatherComplete := pion.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("set local description: %w", err)
	}
	select {
	case <-gatherComplete:
	case <-time.After(timeout):
		return "", fmt.Errorf("ICE gathering did not complete within %s", timeout)
	}
	local := pc.LocalDescription()
	if local == nil {
		return "", fmt.Errorf("nil LocalDescription after gathering")
	}
	return local.SDP, nil
}

// e2eNonTrickleOffer is the *testing.T convenience wrapper — call ONLY from
// this test function's own goroutine (see file doc comment).
func e2eNonTrickleOffer(t *testing.T, pc *pion.PeerConnection) string {
	t.Helper()
	sdp, err := e2eNonTrickleOfferPlain(pc, e2eWait)
	require.NoError(t, err)
	return sdp
}

func e2eWaitCond(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// e2eDummyPayload stands in for an encoded VP8/Opus frame — packet FLOW is
// what this test verifies, not real decodability (mirrors session_test.go's
// identical rationale).
var e2eDummyPayload = []byte("w2c-e2e-fake-encoded-media-frame-payload-0123456789")

// e2ePumpSamples writes e2eDummyPayload to track at ~30fps until stop closes.
// Plain (no *testing.T) — safe to call from any goroutine.
func e2ePumpSamples(track *pion.TrackLocalStaticSample, stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(33 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = track.WriteSample(media.Sample{Data: e2eDummyPayload, Duration: 33 * time.Millisecond})
			}
		}
	}()
}

// ---------------------------------------------------------------------------
// Fake encoder (plays the headless-Chrome tabCapture encoder page's role,
// over a REAL /api/v1/browser/capture-ingest WS connection). Every function
// on this path is plain-error-returning — see the file doc comment.
// ---------------------------------------------------------------------------

type e2eFakeEncoder struct {
	conn  *websocket.Conn
	pc    *pion.PeerConnection
	video *pion.TrackLocalStaticSample
	audio *pion.TrackLocalStaticSample
}

// startE2EFakeEncoder dials ingestWSURL, performs
// hello → offer → answer exactly as encoder.js does, and returns a ready
// e2eFakeEncoder with its PeerConnection's remote description already set.
// Call ONLY from a non-test goroutine (see file doc comment) — every failure
// is a returned error, never a t.Fatal/require call.
func startE2EFakeEncoder(ingestWSURL, tokenHex string) (enc *e2eFakeEncoder, err error) {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, dialErr := dialer.Dial(ingestWSURL, nil)
	if resp != nil {
		resp.Body.Close()
	}
	if dialErr != nil {
		return nil, fmt.Errorf("dial capture-ingest WS %q: %w", ingestWSURL, dialErr)
	}
	// From here on, any early return must close conn/pc — a small local
	// cleanup list keeps that from being repeated at every return site.
	var cleanups []func()
	defer func() {
		if err != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}()
	cleanups = append(cleanups, func() { conn.Close() })

	hello := generated.BrowserCaptureHelloFrame{
		Type:       string(generated.WsFrameTypeBrowserCaptureHello),
		Token:      tokenHex,
		ExtVersion: "1.0.0-e2e-test",
	}
	helloData, jsonErr := json.Marshal(hello)
	if jsonErr != nil {
		return nil, fmt.Errorf("marshal hello: %w", jsonErr)
	}
	if writeErr := conn.WriteMessage(websocket.TextMessage, helloData); writeErr != nil {
		return nil, fmt.Errorf("write hello: %w", writeErr)
	}

	pc, pcErr := pion.NewPeerConnection(pion.Configuration{})
	if pcErr != nil {
		return nil, fmt.Errorf("new peer connection: %w", pcErr)
	}
	cleanups = append(cleanups, func() { pc.Close() })

	video, videoErr := pion.NewTrackLocalStaticSample(
		pion.RTPCodecCapability{MimeType: pion.MimeTypeVP8},
		"video",
		"w2c-e2e-encoder",
	)
	if videoErr != nil {
		return nil, fmt.Errorf("new video track: %w", videoErr)
	}
	if _, addErr := pc.AddTrack(video); addErr != nil {
		return nil, fmt.Errorf("add video track: %w", addErr)
	}

	audio, audioErr := pion.NewTrackLocalStaticSample(
		pion.RTPCodecCapability{MimeType: pion.MimeTypeOpus},
		"audio",
		"w2c-e2e-encoder",
	)
	if audioErr != nil {
		return nil, fmt.Errorf("new audio track: %w", audioErr)
	}
	if _, addErr := pc.AddTrack(audio); addErr != nil {
		return nil, fmt.Errorf("add audio track: %w", addErr)
	}

	offerSDP, offerErr := e2eNonTrickleOfferPlain(pc, e2eWait)
	if offerErr != nil {
		return nil, fmt.Errorf("encoder-side offer: %w", offerErr)
	}

	offerFrame := generated.BrowserCaptureOfferFrame{
		Type: string(generated.WsFrameTypeBrowserCaptureOffer),
		Sdp:  offerSDP,
	}
	offerData, jsonErr := json.Marshal(offerFrame)
	if jsonErr != nil {
		return nil, fmt.Errorf("marshal browser_capture_offer: %w", jsonErr)
	}
	if writeErr := conn.WriteMessage(websocket.TextMessage, offerData); writeErr != nil {
		return nil, fmt.Errorf("write browser_capture_offer: %w", writeErr)
	}

	conn.SetReadDeadline(time.Now().Add(e2eWait))
	_, answerData, readErr := conn.ReadMessage()
	if readErr != nil {
		return nil, fmt.Errorf("read browser_capture_answer: %w", readErr)
	}
	var answerFrame generated.BrowserCaptureAnswerFrame
	if jsonErr := json.Unmarshal(answerData, &answerFrame); jsonErr != nil {
		return nil, fmt.Errorf("unmarshal browser_capture_answer: %w", jsonErr)
	}
	if answerFrame.Type != string(generated.WsFrameTypeBrowserCaptureAnswer) || answerFrame.Sdp == "" {
		return nil, fmt.Errorf("unexpected capture-ingest reply: %+v", answerFrame)
	}

	if setErr := pc.SetRemoteDescription(
		pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answerFrame.Sdp},
	); setErr != nil {
		return nil, fmt.Errorf("set remote description (capture answer): %w", setErr)
	}

	enc = &e2eFakeEncoder{conn: conn, pc: pc, video: video, audio: audio}

	// Drain any further server->client frames (e.g. a browser_capture_control
	// {shutdown} pushed on teardown) so the connection never blocks a write;
	// this test never needs the fake encoder to ACT on a control frame.
	go func() {
		for {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			if _, _, readErr := conn.ReadMessage(); readErr != nil {
				return
			}
		}
	}()

	return enc, nil
}

func (e *e2eFakeEncoder) startPumping(stop <-chan struct{}) {
	e2ePumpSamples(e.video, stop)
	e2ePumpSamples(e.audio, stop)
}

func (e *e2eFakeEncoder) close() {
	_ = e.pc.Close()
	_ = e.conn.Close()
}

// ---------------------------------------------------------------------------
// Fake viewer (plays an external browser's role, over a REAL
// /api/v1/browser/ws connection). This side runs entirely on the test's own
// goroutine, so its helpers may use *testing.T directly.
// ---------------------------------------------------------------------------

type e2eFakeViewer struct {
	pc        *pion.PeerConnection
	videoPkts atomic.Int64
	audioPkts atomic.Int64
	inputDC   *pion.DataChannel
	dcOpen    chan struct{}
}

func newE2EFakeViewer(t *testing.T) *e2eFakeViewer {
	t.Helper()
	pc, err := pion.NewPeerConnection(pion.Configuration{})
	require.NoError(t, err)

	fv := &e2eFakeViewer{pc: pc, dcOpen: make(chan struct{})}

	recvonly := pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}
	_, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeVideo, recvonly)
	require.NoError(t, err)
	_, err = pc.AddTransceiverFromKind(pion.RTPCodecTypeAudio, recvonly)
	require.NoError(t, err)

	pc.OnTrack(func(track *pion.TrackRemote, receiver *pion.RTPReceiver) {
		go func() {
			buf := make([]byte, 1500)
			for {
				if _, _, rtcpErr := receiver.Read(buf); rtcpErr != nil {
					return
				}
			}
		}()
		go func() {
			buf := make([]byte, 1500)
			for {
				_, _, readErr := track.Read(buf)
				if readErr != nil {
					return
				}
				switch track.Kind() {
				case pion.RTPCodecTypeVideo:
					fv.videoPkts.Add(1)
				case pion.RTPCodecTypeAudio:
					fv.audioPkts.Add(1)
				case pion.RTPCodecTypeUnknown:
					// Not video or audio; this fake viewer only counts the
					// two track kinds it exercises.
				}
			}
		}()
	})

	dc, err := pc.CreateDataChannel("input", nil)
	require.NoError(t, err)
	fv.inputDC = dc
	dc.OnOpen(func() { close(fv.dcOpen) })

	return fv
}

// ---------------------------------------------------------------------------
// The centerpiece test.
// ---------------------------------------------------------------------------

// TestWebRTCEndToEndInProcess proves the full signaling + media + input path
// end-to-end with no Chrome: a real gateway browser-WS handler and a real
// capture-ingest handler on one httptest server, a real Pion relay Session,
// a faked-only-at-the-CDP-launch-boundary encoder, and a real Pion viewer.
//
// Assertions (wave-plan W2-C):
//
//	(a) RTP packets arrive on BOTH viewer tracks, with count growth over ~2s.
//	(b) A browser_webrtc_state frame with available:true (and active:true)
//	    arrives.
//	(c) Sending a BrowserInputFrame JSON on the viewer's "input" data channel
//	    reaches the Live layer — observed at the deepest seam reachable
//	    without a real attached Chrome tab (see the sink wrapper below); the
//	    frame→LiveInput conversion itself is already covered by
//	    TestBrowserInputFrameToLiveInput_TableParity (browser_webrtc_test.go).
//	(d) Viewer detach decrements the capture session's viewer refcount
//	    (asserted via the exported ViewerCount() state — the grace-STOP
//	    TIMER firing itself is already covered, with a shrunk grace period,
//	    by TestCaptureSession_RemoveViewer_GraceStopFiresAfterTimer in
//	    pkg/tools/browser/capture_session_test.go; captureGracePeriod is
//	    unexported and this test lives in a different package).
func TestWebRTCEndToEndInProcess(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// "capability gate must report Capable=true" here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}

	// Reserve a real TCP listener FIRST so cfg.Gateway.Port (which
	// handleWebRTCOffer uses verbatim to build the loopback ingest URL,
	// ws://127.0.0.1:<port>/api/v1/browser/capture-ingest) matches the
	// ACTUAL port this test's httptest server ends up bound to — the fake
	// encoder dials the URL the PRODUCTION code computes, unmodified.
	lst, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr, tcpAddrOk := lst.Addr().(*net.TCPAddr)
	require.True(t, tcpAddrOk, "listener address must be a *net.TCPAddr")
	port := tcpAddr.Port

	tmpDir := t.TempDir()
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Gateway.Port = port
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		// WebRTCStunServer left "" — host-only ICE (ADR-047 D3 / wave-plan
		// decision 7), matching the task's explicit requirement.
		// ADR-048 condition 3 (CaptureVideoCapability): the config-literal
		// test harness bypasses config.DefaultConfig()'s CaptureSharedContext
		// default (true), so it must be set explicitly here or the gate
		// ladder now reports not_capable before ever reaching this test's
		// faked capture-session machinery.
		cfg.Tools.Browser.CaptureSharedContext = true
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, ok := al.BrowserManagerForAgent(defaultAgent.ID)
	require.True(t, ok)

	// Plant a fake, non-functional "chrome" binary at the exact path
	// ClassifyVideoCapability/findInstalledBuild look for, so the capability
	// gate in handleWebRTCOffer reports Capable=true — same technique as
	// TestHandleWebRTCOffer_CapableButLaunchFails. It is NEVER executed:
	// capture start is fully faked via the EncoderStarter seam below, which
	// never shells out to it.
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

	// --- Real relay Session (Pion, host-only ICE) + FAKE encoder launch via
	// the EncoderStarter seam ---
	var encMu sync.Mutex
	var encState struct {
		enc          *e2eFakeEncoder
		mintedToken  string
		ingestURL    string
		stunServer   string
		starterCalls int
	}
	pumpStop := make(chan struct{})
	t.Cleanup(func() { close(pumpStop) })

	fakeStarter := browser.EncoderStarter(
		func(_ context.Context, _ *browser.BrowserManager, tokenHex, ingestURL, stunServer string) (context.Context, context.CancelFunc, error) {
			encMu.Lock()
			encState.starterCalls++
			encState.mintedToken = tokenHex
			encState.ingestURL = ingestURL
			encState.stunServer = stunServer
			encMu.Unlock()

			enc, startErr := startE2EFakeEncoder(ingestURL, tokenHex)
			if startErr != nil {
				return nil, nil, fmt.Errorf("e2e fake encoder: %w", startErr)
			}
			enc.startPumping(pumpStop)

			encMu.Lock()
			encState.enc = enc
			encMu.Unlock()

			tabCtx, cancel := context.WithCancel(context.Background())
			return tabCtx, cancel, nil
		},
	)

	// dcFramesObserved counts InputSink invocations that made it all the way
	// through the REAL production webrtcInputSink(mgr) — i.e. proves
	// assertion (c): the DC message was unmarshaled as a BrowserInputFrame,
	// converted via browserInputFrameToLiveInput, and dispatched into
	// mgr.Live().Input(). This wraps (never replaces) the exact function
	// ensureCaptureSession wires up in production — no production code was
	// changed to make this observable.
	var dcFramesObserved atomic.Int32
	realSink := handler.webrtcInputSink(mgr, al.GetConfig())
	sink := webrtc.InputSink(func(viewerID string, raw []byte) {
		realSink(viewerID, raw)
		dcFramesObserved.Add(1)
	})

	relay := webrtc.NewSession(webrtc.Config{StunServer: ""}, sink, e2eSafeLogf(t.Name()))

	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeStarter, e2eSafeLogf(t.Name()))
	require.NoError(t, err)
	t.Cleanup(cs.Stop) // idempotent safety net; the test also calls Stop explicitly below

	var onStoppedCalls atomic.Int32
	cs.SetOnStopped(func() {
		handler.captures.removeIfCurrent(defaultAgent.ID, cs)
		onStoppedCalls.Add(1)
	})

	// Register cs exactly where ensureCaptureSession (browser_webrtc.go)
	// would: mgr.capture (so handleWebRTCOffer's own EnsureCaptureSession
	// call — unmodified production code — reuses THIS session instead of
	// constructing a production one with defaultEncoderStarter) and the
	// handler's token->session registry (so the capture-ingest handler's
	// hello can resolve this session by token, exactly as in production).
	_, err = mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	handler.captures.set(defaultAgent.ID, cs)

	// --- FAKE VIEWER: real WS connection + real recvonly Pion PC + "input" DC ---
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
		SessionId: "e2e-session",
	}
	offerData, err := json.Marshal(offerFrame)
	require.NoError(t, err)
	require.NoError(t, viewerConn.WriteMessage(websocket.TextMessage, offerData))

	// First frame back MUST be browser_webrtc_answer (handleWebRTCOffer sends
	// the answer, then the state frame, over the same per-connection sendCh
	// — FIFO, single writer goroutine). If the fake encoder failed to start,
	// handleWebRTCOffer instead sends ONLY a browser_webrtc_state{available:
	// false, reason:"error"} — detect and fail loudly with that reason
	// rather than a confusing unmarshal error.
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

	// (b) browser_webrtc_state{available:true, active:true} follows.
	viewerConn.SetReadDeadline(time.Now().Add(e2eWait))
	_, stateData, err := viewerConn.ReadMessage()
	require.NoError(t, err, "viewer: read browser_webrtc_state")
	var stateFrame webrtcStateFrameDecoder
	require.NoError(t, json.Unmarshal(stateData, &stateFrame))
	require.Equal(t, string(generated.WsFrameTypeBrowserWebrtcState), stateFrame.Type)
	require.True(t, stateFrame.Available, "browser_webrtc_state.available must be true on a successful offer")
	require.NotNil(t, stateFrame.Active)
	require.True(t, *stateFrame.Active, "browser_webrtc_state.active must be true once the viewer PC is up")

	encMu.Lock()
	starterCalls := encState.starterCalls
	mintedToken := encState.mintedToken
	ingestURL := encState.ingestURL
	stunServer := encState.stunServer
	encMu.Unlock()
	require.Equal(t, 1, starterCalls, "EncoderStarter must be invoked exactly once (one active stream per agent)")
	require.Equal(
		t,
		cs.TokenHex(),
		mintedToken,
		"EncoderStarter must receive the SAME token ValidateToken checks against",
	)
	require.Contains(t, ingestURL, fmt.Sprintf(":%d/api/v1/browser/capture-ingest", port),
		"EncoderStarter must receive the production-computed ingest URL, pointed at THIS test's own listener")
	require.Empty(
		t,
		stunServer,
		"EncoderStarter must receive the configured stunServer verbatim (this test leaves WebRTCStunServer empty — host-only ICE)",
	)

	e2eSetAnswer := func(pc *pion.PeerConnection, sdp string) {
		require.NoError(t, pc.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: sdp}))
	}
	e2eSetAnswer(viewer.pc, answerFrame.Sdp)

	// (a) RTP packets arrive on BOTH viewer tracks, with count growth over ~2s.
	e2eWaitCond(t, e2eWait, "viewer to receive initial video RTP", func() bool { return viewer.videoPkts.Load() > 0 })
	e2eWaitCond(t, e2eWait, "viewer to receive initial audio RTP", func() bool { return viewer.audioPkts.Load() > 0 })

	videoAtT0 := viewer.videoPkts.Load()
	audioAtT0 := viewer.audioPkts.Load()
	time.Sleep(2 * time.Second)
	videoAtT2 := viewer.videoPkts.Load()
	audioAtT2 := viewer.audioPkts.Load()

	require.Greaterf(
		t,
		videoAtT2,
		videoAtT0,
		"video packet count must grow over ~2s (got %d -> %d)",
		videoAtT0,
		videoAtT2,
	)
	require.Greaterf(
		t,
		audioAtT2,
		audioAtT0,
		"audio packet count must grow over ~2s (got %d -> %d)",
		audioAtT0,
		audioAtT2,
	)
	t.Logf("OBSERVED packet growth over 2s: video %d -> %d, audio %d -> %d", videoAtT0, videoAtT2, audioAtT0, audioAtT2)

	stats := cs.Stats()
	require.True(t, stats.HasAudio, "relay Stats() must report HasAudio=true once the audio track has attached")
	t.Logf("OBSERVED relay Stats(): %+v", stats)

	// (c) DC -> sink -> dispatch plumbing fires.
	select {
	case <-viewer.dcOpen:
	case <-time.After(e2eWait):
		t.Fatal("input data channel did not open")
	}

	x, y := 42.0, 84.0
	inputFrame := generated.BrowserInputFrame{
		Type: "browser_input",
		Kind: "mouse_move",
		X:    &x,
		Y:    &y,
	}
	inputData, err := json.Marshal(inputFrame)
	require.NoError(t, err)
	require.NoError(t, viewer.inputDC.SendText(string(inputData)))

	e2eWaitCond(t, e2eWait, "DC input frame to reach the production InputSink (webrtcInputSink)", func() bool {
		return dcFramesObserved.Load() > 0
	})
	t.Logf(
		"OBSERVED %d data-channel input frame(s) reach webrtcInputSink -> browserInputFrameToLiveInput -> mgr.Live().Input",
		dcFramesObserved.Load(),
	)

	// (d) viewer detach decrements the capture session's refcount.
	require.Equal(t, 1, cs.ViewerCount(), "exactly one WebRTC viewer must be attached before detach")
	require.NoError(t, viewerConn.Close())

	e2eWaitCond(t, e2eWait, "capture session viewer count to drop to 0 after viewer detach", func() bool {
		return cs.ViewerCount() == 0
	})
	t.Logf("OBSERVED ViewerCount() dropped to 0 after viewer WS close (detachWebRTCViewer -> RemoveViewer)")

	// Explicit, deterministic teardown (rather than relying on the ~30s grace
	// timer RemoveViewer armed above) — also proves SetOnStopped/registry
	// cleanup fire in this full integration context (existing unit coverage
	// for that wiring uses a fakeRelay, not a real Session+real connections).
	cs.Stop()
	e2eWaitCond(t, e2eWait, "SetOnStopped callback to fire exactly once", func() bool {
		return onStoppedCalls.Load() == 1
	})
	gotID, gotCS := handler.captures.findByToken(mintedToken)
	require.Empty(t, gotID, "captureRegistry must no longer resolve the stopped session's token")
	require.Nil(t, gotCS)

	encMu.Lock()
	enc := encState.enc
	encMu.Unlock()
	if enc != nil {
		enc.close()
	}
}
