// browser_webrtc_fixwavea_test.go — FIX WAVE A regression coverage for
// pkg/gateway/browser_webrtc.go / browser_ws.go: finding 1 (a slow
// browser_webrtc_offer must never block readLoop's ReadMessage loop, and any
// commit it eventually makes must respect a detach/close/newer-offer that
// arrived while it was negotiating) and finding 3 (h.captureFenceMu must not
// be held across another agent's synchronous, potentially-slow Stop()).
// Kept in a separate file per this project's own established convention
// (browser_webrtc_fixwave_test.go's header comment) rather than growing an
// existing file further.

package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// ---------------------------------------------------------------------------
// Finding 1: a slow browser_webrtc_offer must not block readLoop.
// ---------------------------------------------------------------------------

// TestBrowserWS_SlowWebRTCOffer_DoesNotBlockReadLoop is the FIX WAVE A
// finding 1 regression: before this fix, browser_ws.go's readLoop dispatched
// browser_webrtc_offer SYNCHRONOUSLY, inline, in the connection's single
// ReadMessage goroutine. Since gorilla/websocket only services the
// registered PongHandler (which refreshes the connection's read deadline)
// from INSIDE a ReadMessage call, a slow offer (real HandleViewerOffer can
// legitimately wait up to waitForTracksTimeout) starved every subsequent
// Pong from ever being processed — and, more immediately observable here,
// starved every OTHER frame the client sent on the SAME connection from ever
// being read at all, no matter how quickly the server could otherwise have
// answered it.
//
// This test proves readLoop keeps dispatching OTHER frames while a
// browser_webrtc_offer is deliberately held open on a controlled channel: it
// sends the offer, then IMMEDIATELY sends a second, unrelated
// browser_attach frame (targeting an agent with no registered manager — a
// fast, synchronous, purely in-memory rejection) on the SAME connection, and
// asserts the attach's browser_status(error) response arrives promptly.
// Without dispatchWebRTCOffer's async dispatch, this read blocks until the
// offer's own HandleViewerOffer call returns — which this test deliberately
// never releases until AFTER the short read deadline below would already
// have elapsed, so the old (buggy) synchronous dispatch would fail this
// assertion with a read timeout.
func TestBrowserWS_SlowWebRTCOffer_DoesNotBlockReadLoop(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever reports Capable=true on linux")
	}
	tmpDir := t.TempDir()
	bogusExec := filepath.Join(tmpDir, "no-such-chrome-binary")
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.CaptureSharedContext = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.ExecPath = bogusExec
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	require.True(t, mgr.CaptureVideoCapability().Capable,
		"capability gate must report Capable=true via the exec_path filename heuristic, so the ladder reaches "+
			"Start()/HandleViewerOffer instead of stopping earlier at not_capable")

	// Pre-seed a fake CaptureSession whose Start() completes instantly (a
	// fake, non-CDP EncoderStarter) but whose HandleViewerOffer blocks on
	// offerBlock until released — simulating the real relay's waitForTracks
	// wait without needing real ingest/CDP machinery.
	offerBlock := make(chan struct{})
	var releaseOnce sync.Once
	releaseOffer := func() { releaseOnce.Do(func() { close(offerBlock) }) }
	t.Cleanup(releaseOffer) // always release, even on failure — never leak the goroutine

	relay := &fakeRelay{viewerOfferBlock: offerBlock}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	_, err = mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	handler.captures.set(defaultAgent.ID, cs)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialBrowserTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	writeBrowserAuthFrame(t, conn, "dev-token")

	offerFrame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-slow-offer",
	}
	offerData, err := json.Marshal(offerFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, offerData))

	// Immediately (the offer is still blocked, and stays blocked until well
	// after the read deadline below) send an UNRELATED frame on the SAME
	// connection — it must be answered promptly if, and only if, readLoop is
	// not stuck inside the offer's synchronous handler.
	attachFrame := generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   "no-such-agent-fixwavea-probe",
		SessionId: "probe-session",
	}
	attachData, err := json.Marshal(attachFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, attachData))

	resp := readBrowserFrame(t, conn, 3*time.Second)
	require.Equal(t, "browser_status", resp.Type,
		"readLoop must still process the unrelated browser_attach frame promptly while the webrtc offer is "+
			"blocked — a read timeout here means the old synchronous offer-dispatch bug has regressed")
	require.Equal(t, "error", resp.State, "probe agent has no registered manager, so attach must fail cleanly")

	// Release the blocked offer and let it run to completion — proves the
	// offer eventually DOES get processed once its slow step completes (no
	// goroutine/session leak), and that the async path still delivers a
	// normal success response.
	releaseOffer()

	answerResp := readBrowserFrame(t, conn, 3*time.Second)
	require.Equal(t, string(generated.WsFrameTypeBrowserWebrtcAnswer), answerResp.Type,
		"once released, the offer must still complete normally and answer the viewer")

	stateResp := readBrowserWebRTCStateFrame(t, conn, 3*time.Second)
	require.True(t, stateResp.Available)
	require.NotNil(t, stateResp.Active)
	require.True(t, *stateResp.Active)
}

// TestHandleWebRTCOffer_SupersededByDetachDuringNegotiation_TearsDownCleanly
// proves the other half of finding 1's fix: the epoch/commit machinery that
// makes async dispatch SAFE. A browser_detach (or the connection closing)
// arriving while a background offer is still negotiating must invalidate
// that offer's generation, so when HandleViewerOffer eventually returns
// successfully, handleWebRTCOffer tears down the viewer/session state it
// just built instead of committing a WebRTC attachment nobody wants anymore
// (which would otherwise be a resource leak: a viewer registered forever on
// a capture session with no code path left that will ever call
// detachWebRTCViewer for it again).
func TestHandleWebRTCOffer_SupersededByDetachDuringNegotiation_TearsDownCleanly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever reports Capable=true on linux")
	}
	tmpDir := t.TempDir()
	bogusExec := filepath.Join(tmpDir, "no-such-chrome-binary")
	handler, al := newBrowserWSTestHandler(t, func(cfg *config.Config) {
		cfg.Tools.Browser.WebRTCEnabled = true
		cfg.Tools.Browser.CaptureSharedContext = true
		cfg.Tools.Browser.ProfileDir = filepath.Join(tmpDir, "browser-profile")
		cfg.Tools.Browser.ExecPath = bogusExec
	})
	t.Cleanup(handler.Wait)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	mgr, outcome := al.BrowserManagerForAgent(context.Background(), defaultAgent.ID, "")
	require.Equal(t, agent.BrowserResolveOK, outcome)
	require.True(t, mgr.CaptureVideoCapability().Capable)

	offerBlock := make(chan struct{})
	var releaseOnce sync.Once
	releaseOffer := func() { releaseOnce.Do(func() { close(offerBlock) }) }
	t.Cleanup(releaseOffer)

	relay := &fakeRelay{viewerOfferBlock: offerBlock}
	var calls int32
	cs, err := browser.NewCaptureSessionWithDeps(mgr, defaultAgent.ID, relay, fakeEncoderStarter(&calls, nil), nil)
	require.NoError(t, err)
	_, err = mgr.EnsureCaptureSession(func() (*browser.CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	handler.captures.set(defaultAgent.ID, cs)

	wc := newTestBrowserWSConn()
	var state browserConnState
	viewerID := "viewer-superseded-by-detach"

	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-superseded",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	// Mirrors dispatchWebRTCOffer's own sequence: bump the epoch synchronously,
	// THEN hand the negotiation off to a goroutine.
	epoch := state.beginWebRTCOffer()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.handleWebRTCOffer(wc, &state, viewerID, "user-1", data, al.GetConfig(), epoch)
	}()

	// Wait until AddViewer has definitely run (we're now blocked inside
	// HandleViewerOffer) before superseding.
	require.Eventually(t, func() bool { return cs.ViewerCount() == 1 }, 2*time.Second, 5*time.Millisecond,
		"handleWebRTCOffer must have called AddViewer before HandleViewerOffer blocks")

	// A browser_detach (or the connection closing) arrives while the offer
	// is still negotiating — this is exactly what readLoop's detach case and
	// its connection-close cleanup defer now both do unconditionally.
	handler.detachWebRTCViewer(&state, viewerID)

	// Now let the blocked negotiation succeed.
	releaseOffer()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleWebRTCOffer never returned after being unblocked")
	}

	// The offer must NOT have committed a WebRTC attachment — the detach
	// invalidated its epoch before it ever reached the commit point.
	require.Nil(t, state.webrtc, "a superseded offer must never commit an attachment after the fact")

	// The viewer/session state the superseded offer built must have been torn
	// down: AddViewer's registration undone (RemoveViewer) and the relay-side
	// viewer PeerConnection closed (CloseViewer) — otherwise this is a silent
	// resource leak with no remaining code path that will ever clean it up.
	require.Equal(t, 0, cs.ViewerCount(),
		"a superseded offer must call RemoveViewer to undo its own AddViewer, or the viewer leaks forever")
	relay.mu.Lock()
	closed := append([]string(nil), relay.closedViewers...)
	relay.mu.Unlock()
	require.Contains(t, closed, viewerID,
		"a superseded offer must close the relay-side viewer PeerConnection it just registered")

	// No success frame (answer/state) should have been sent for a superseded
	// commit — the connection already gave up on this offer.
	select {
	case frame := <-wc.sendCh:
		t.Fatalf("a superseded offer must not send a success frame after the fact, got: %s", frame)
	default:
	}
}

// ---------------------------------------------------------------------------
// Finding 3: captureFenceMu must not be held across another agent's slow,
// synchronous Stop().
// ---------------------------------------------------------------------------

// TestHandleWebRTCOffer_SupersedeDoesNotBlockFenceOnSlowStop is the FIX WAVE
// A finding 3 regression: another agent's viewerless capture session is
// bound to an ingest-control send that blocks indefinitely (standing in for
// CaptureSession.Stop()'s real, captureIngestWriteTimeout-bounded-but-still-
// up-to-5s loopback write) — proving handleWebRTCOffer's fence section
// returns promptly regardless, because Stop() now runs off h.captureFenceMu
// (`go otherCS.Stop()`) instead of inline. Before this fix, the SAME
// blocked-forever otherCS would have made this call hang for as long as the
// block lasted (the test would have to time out waiting for
// handleWebRTCOffer to return at all), since Stop() ran synchronously INSIDE
// the captureFenceMu-held critical section.
func TestHandleWebRTCOffer_SupersedeDoesNotBlockFenceOnSlowStop(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// chrome-for-testing publishes no linux/arm64 build (installer.go's
		// cftPlatform errors before findInstalledBuild ever inspects the
		// fake binary this test plants below), so ClassifyVideoCapability
		// can never report Capable=true on linux/arm64 either — confirmed
		// by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64) failing
		// here.
		t.Skip("ClassifyVideoCapability only ever reports Capable=true on linux/amd64")
	}
	// See TestHandleWebRTCOffer_CaptureFenceMu_SerializesFenceCheckAndEnsure's
	// identical Setenv for why: forces the exec resolver onto the fake
	// managed binary below instead of a real Chrome the runner might have on
	// $PATH.
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

	installRoot := mgr.InstallRoot()
	fakeBinDir := filepath.Join(installRoot, "fake-version", "chrome-linux64")
	require.NoError(t, os.MkdirAll(fakeBinDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBinDir, "chrome"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	require.True(t, browser.ClassifyVideoCapability(installRoot).Capable)

	// Another agent's capture session, viewerless (grace-period leftover),
	// bound to an ingest connection whose "shutdown" control write blocks
	// forever until the test releases it — standing in for Stop()'s real,
	// bounded-but-still-slow loopback write. otherRelay's Close() (which
	// Stop() only calls AFTER the blocking requestControl write returns —
	// capture_session.go's Stop body does the control write, THEN
	// relay.CloseViewer per viewer, THEN relay.Close()) is the signal this
	// test uses to tell "Stop() is still blocked on the write" apart from
	// "Stop() has fully completed" — cs.Done() is NOT usable for that: it
	// closes right at the TOP of Stop(), before the blocking write even
	// starts, so it would already be closed by the time this test could ever
	// observe it either way.
	var calls int32
	otherRelay := &fakeRelay{}
	otherCS, err := browser.NewCaptureSessionWithDeps(
		nil,
		"other-agent-slow-stop",
		otherRelay,
		fakeEncoderStarter(&calls, nil),
		nil,
	)
	require.NoError(t, err)
	blockStop := make(chan struct{})
	var releaseOnce sync.Once
	releaseStop := func() { releaseOnce.Do(func() { close(blockStop) }) }
	t.Cleanup(releaseStop)
	sendStarted := make(chan struct{})
	var sendStartedOnce sync.Once
	otherCS.BindIngest(func(action string, reason *string, _, _ int, _ int) error {
		sendStartedOnce.Do(func() { close(sendStarted) })
		<-blockStop
		return nil
	}, func() {})
	handler.captures.set("other-agent-slow-stop", otherCS)
	t.Cleanup(otherCS.Stop) // idempotent; harmless if Stop() already ran

	wc := newTestBrowserWSConn()
	var state browserConnState
	frame := generated.BrowserWebRTCOfferFrame{
		Type:      string(generated.WsFrameTypeBrowserWebrtcOffer),
		AgentId:   defaultAgent.ID,
		Sdp:       "v=0\r\n",
		SessionId: "sess-slow-stop",
	}
	data, err := json.Marshal(frame)
	require.NoError(t, err)

	// #606: the budget bounds more than the fence — `done` closes only when
	// handleWebRTCOffer fully returns, which includes the post-fence cs.Start
	// (a real Chrome-launch attempt against the planted always-failing fake
	// binary) whose failure detection is bounded by 20s internal ceilings
	// (cdppipe defaultDialTimeout, captureStartTimeout). Nominal is ~10ms, but
	// a saturated CI runner can stretch it far past 2s with no product bug. A
	// genuine regression of the fix under test (Stop() inline under the fence)
	// blocks until releaseStop() — i.e. fails deterministically at ANY finite
	// budget — so widening does not weaken what this test discriminates.
	const fenceBudget = 10 * time.Second
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.handleWebRTCOffer(wc, &state, "viewer-slow-stop", "user-1", data, al.GetConfig(), 0)
	}()

	select {
	case <-done:
		// Good: handleWebRTCOffer returned WITHOUT waiting for otherCS.Stop()
		// (still blocked on blockStop at this point) to finish.
	case <-time.After(fenceBudget):
		t.Fatal(
			"handleWebRTCOffer did not return within the fence budget — captureFenceMu is still held across " +
				"another agent's slow Stop() (finding 3 regressed)",
		)
	}

	// Confirm this test genuinely exercised the blocking write (not a
	// trivial no-op that happened to return fast for some OTHER reason) —
	// the ingest-send closure must have been invoked by now.
	require.Eventually(t, func() bool {
		select {
		case <-sendStarted:
			return true
		default:
			return false
		}
	}, 2*time.Second, 5*time.Millisecond, "otherCS.Stop()'s ingest-control write was never attempted")

	// And it must NOT have completed yet: relay.Close() is only reached
	// AFTER the blocking control write returns, so closeCount() advancing
	// here would mean the write already returned — i.e. this test would no
	// longer be exercising the slow-write scenario finding 3 fixes.
	require.Equal(t, 0, otherRelay.closeCount(),
		"otherCS.Stop() must still be blocked on its ingest write at this point")

	// Now release the block and confirm Stop() DOES still run to completion
	// (asynchronously) — the supersede itself must still be correct, just
	// off the fence-held path.
	releaseStop()
	require.Eventually(t, func() bool { return otherRelay.closeCount() == 1 }, 2*time.Second, 5*time.Millisecond,
		"otherCS.Stop() must still complete (relay.Close called) once its blocked write is released")

	got := decodeWebRTCState(t, drainOneFrame(t, wc))
	require.False(t, got.Available, "this chrome-less harness must then fail at launch (the fence itself passed)")
	require.Equal(t, "error", got.Reason)
}
