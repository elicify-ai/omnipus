package browser

// capture_session_reconcile_test.go — ADR-047 fix-wave finding 3 unit
// coverage for CaptureSession.ReconcileScreencast's pause/resume DECISION
// logic and LiveView.pauseScreencast/resumeScreencast's mechanics. Every
// test here uses a REAL *BrowserManager (via NewBrowserManager, matching
// TestBrowserManager_Live's pattern) so mgr.Live() is properly wired, but
// deliberately hand-installs LiveView state exactly the way live_test.go's
// existing "fake an active screencast" tests do (see
// TestLiveView_Detach_RefCountsViewers) rather than going through a real
// browser_attach/mgr.Session() — no real chromedp/Chrome/Pion machinery is
// touched anywhere in this file, per this fix-wave's "not real Chrome"
// testing directive. A bogus (non-chromedp) context.Background() tabCtx
// makes any CDP call this file's code paths might attempt fail fast and
// harmlessly (established by TestLiveView_Detach_RefCountsViewers), so no
// scenario here ever blocks or reaches out to a real browser process.

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// newReconcileTestManager builds a real *BrowserManager (mgr.Live() wired,
// per NewBrowserManager's contract) without ever starting a real browser —
// construction is lazy (ADR-038), and none of this file's tests ever call
// mgr.Session()/any tool that would trigger ensureStarted.
func newReconcileTestManager(t *testing.T) *BrowserManager {
	t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	mgr, err := NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	if err != nil {
		t.Fatalf("NewBrowserManager: %v", err)
	}
	return mgr
}

// fakeActiveScreencast hand-installs an "active screencast, N viewers"
// LiveView state for sessionID, exactly mirroring live_test.go's
// TestLiveView_Detach_RefCountsViewers technique -- no chromedp.Run call,
// no real Chrome. Returns the LiveView for direct assertions.
func fakeActiveScreencast(mgr *BrowserManager, sessionID string, viewerIDs ...string) *LiveView {
	lv := mgr.Live().view(sessionID)
	listenCtx, cancel := context.WithCancel(context.Background())
	lv.mu.Lock()
	lv.tabCtx = context.Background()
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	for _, id := range viewerIDs {
		lv.viewers[id] = func(LiveFrame) {}
	}
	lv.mu.Unlock()
	return lv
}

func TestCaptureSession_ReconcileScreencast_PausesWhenWebRTCCoversEveryJPEGViewer(t *testing.T) {
	mgr := newReconcileTestManager(t)
	lv := fakeActiveScreencast(mgr, DefaultSessionID, "viewer-1")

	relay := &fakeRelay{stats: webrtc.Stats{HasVideo: true}}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	// The WebRTC viewer set is EXACTLY the JPEG viewer set ({"viewer-1"}) --
	// nobody depends on the JPEG stream for pixels once this attaches.
	cs.AddViewer("viewer-1")

	lv.mu.Lock()
	paused := lv.pausedForWebRTC
	active := lv.isActiveLocked()
	viewerCount := len(lv.viewers)
	lv.mu.Unlock()

	if !paused {
		t.Error(
			"ReconcileScreencast: screencast should be PAUSED once WebRTC covers the only JPEG viewer, but pausedForWebRTC=false",
		)
	}
	if active {
		t.Error(
			"ReconcileScreencast: the CDP screencast subscription should have been torn down when paused, but isActiveLocked()=true",
		)
	}
	if viewerCount != 1 {
		t.Errorf(
			"ReconcileScreencast: pausing must not touch viewer registrations, got %d viewers, want 1",
			viewerCount,
		)
	}
}

func TestCaptureSession_ReconcileScreencast_StaysResumedWithAJPEGOnlyViewer(t *testing.T) {
	mgr := newReconcileTestManager(t)
	lv := fakeActiveScreencast(mgr, DefaultSessionID, "jpeg-only-viewer", "webrtc-viewer")

	relay := &fakeRelay{stats: webrtc.Stats{HasVideo: true}}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	// Only "webrtc-viewer" gets a WebRTC attachment -- "jpeg-only-viewer" is
	// the mixed-viewer case (ADR-047 D3's per-viewer ICE fallback, or simply
	// a viewer that never sent a browser_webrtc_offer at all) that MUST keep
	// the screencast running for it.
	cs.AddViewer("webrtc-viewer")

	lv.mu.Lock()
	paused := lv.pausedForWebRTC
	active := lv.isActiveLocked()
	lv.mu.Unlock()

	if paused {
		t.Error(
			"ReconcileScreencast: must NOT pause while a JPEG-only viewer remains attached (mixed-viewer case), but pausedForWebRTC=true",
		)
	}
	if !active {
		t.Error(
			"ReconcileScreencast: the screencast must stay running for the JPEG-only viewer, but isActiveLocked()=false",
		)
	}
}

// TestCaptureSession_HandleViewerOffer_ReconcilesOnVideoTransition is the FIX
// WAVE A finding 2 regression: for a BRAND NEW capture session, AddViewer
// runs BEFORE the ingest side has connected at all, so its own
// ReconcileScreencast call is guaranteed to observe HasVideo=false and
// correctly leave the JPEG screencast running (setup assertion below,
// matching TestCaptureSession_ReconcileScreencast_StaysResumedWhenVideoNotFlowingYet
// exactly) — before this fix, NOTHING ever reconciled again after that point,
// so the JPEG screencast kept running for the rest of the session even once
// WebRTC video started flowing moments later, inside that SAME
// HandleViewerOffer call. This test fails without CaptureSession.
// HandleViewerOffer's post-success ReconcileScreencast call: with only the
// AddViewer-time reconcile in place, pausedForWebRTC would stay false forever
// once HasVideo flips true mid-offer.
func TestCaptureSession_HandleViewerOffer_ReconcilesOnVideoTransition(t *testing.T) {
	mgr := newReconcileTestManager(t)
	lv := fakeActiveScreencast(mgr, DefaultSessionID, "viewer-1")

	// HasVideo starts false (zero value) -- the ingest side hasn't connected
	// yet, exactly the ordinary brand-new-session case.
	relay := &fakeRelay{videoArrivesOnOffer: true}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	// AddViewer's own reconcile necessarily sees HasVideo=false here -- this
	// mirrors TestCaptureSession_ReconcileScreencast_StaysResumedWhenVideoNotFlowingYet
	// and MUST still hold after this fix (that sibling test's own
	// requirement is unaffected by it).
	gen := cs.AddViewer("viewer-1")
	lv.mu.Lock()
	pausedAfterAdd := lv.pausedForWebRTC
	lv.mu.Unlock()
	if pausedAfterAdd {
		t.Fatal(
			"setup: AddViewer must NOT pause before video flows (matches the sibling StaysResumedWhenVideoNotFlowingYet test)",
		)
	}

	// Now the SAME viewer's offer actually negotiates -- the fake models the
	// real relay's waitForTracks contract: a successful return means the
	// ingest video track now exists (videoArrivesOnOffer flips
	// stats.HasVideo to true as part of this call, see fakeRelay's doc
	// comment in capture_session_test.go).
	answer, _, err := cs.HandleViewerOffer("viewer-1", "offer-sdp", gen)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	if answer == "" {
		t.Fatal("HandleViewerOffer returned an empty answer on success")
	}

	lv.mu.Lock()
	pausedAfterOffer := lv.pausedForWebRTC
	active := lv.isActiveLocked()
	lv.mu.Unlock()
	if !pausedAfterOffer {
		t.Error(
			"HandleViewerOffer succeeding (video now flowing, and this WebRTC viewer covers the only JPEG viewer) " +
				"must trigger a reconcile that pauses the JPEG screencast -- AddViewer's own earlier reconcile " +
				"necessarily saw HasVideo=false and cannot have caught this transition on its own",
		)
	}
	if active {
		t.Error(
			"ReconcileScreencast: the CDP screencast subscription should have been torn down once paused, " +
				"but isActiveLocked()=true",
		)
	}
}

func TestCaptureSession_ReconcileScreencast_StaysResumedWhenVideoNotFlowingYet(t *testing.T) {
	mgr := newReconcileTestManager(t)
	lv := fakeActiveScreencast(mgr, DefaultSessionID, "viewer-1")

	// HasVideo=false -- the WebRTC viewer attached but the shared video
	// track hasn't arrived from the ingest yet (see waitForTracks in
	// ingest.go). Pausing here would black out the ONLY viewer's live view
	// entirely for however long that takes.
	relay := &fakeRelay{stats: webrtc.Stats{HasVideo: false}}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	cs.AddViewer("viewer-1")

	lv.mu.Lock()
	paused := lv.pausedForWebRTC
	lv.mu.Unlock()

	if paused {
		t.Error("ReconcileScreencast: must NOT pause before the relay reports HasVideo=true, but pausedForWebRTC=true")
	}
}

// TestCaptureSession_ReconcileScreencast_RemoveViewerResumesDecision proves
// RemoveViewer recomputes the pause decision (not just AddViewer) using the
// SAME zero-JPEG-viewers trick TestCaptureSession_ReconcileScreencast_
// ResumesOnStop uses to stay Chrome-free: with no JPEG viewers ever
// attached, resumeScreencast's zero-viewers early return fires (clears the
// flag, no CDP call) rather than its mgr.Session()-dependent happy path. The
// "a JPEG viewer stays attached and the screencast actually restarts for
// it" case exercises the exact same resumeScreencast code this file's
// TestLiveView_PauseScreencast_* tests already cover up to the CDP call —
// the real restart itself needs a live browser and is left to the existing
// Chrome-gated E2E suite (browser_e2e_test.go), consistent with this
// fix-wave's "not real Chrome" testing directive.
func TestCaptureSession_ReconcileScreencast_RemoveViewerResumesDecision(t *testing.T) {
	mgr := newReconcileTestManager(t)
	lv := fakeActiveScreencast(mgr, DefaultSessionID) // zero JPEG viewers

	relay := &fakeRelay{stats: webrtc.Stats{HasVideo: true}}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	cs.AddViewer("webrtc-only-viewer")
	lv.mu.Lock()
	pausedAfterAdd := lv.pausedForWebRTC
	lv.mu.Unlock()
	if !pausedAfterAdd {
		t.Fatal("setup: expected the screencast to pause (no JPEG viewers to keep it running for)")
	}

	cs.RemoveViewer("webrtc-only-viewer")

	lv.mu.Lock()
	pausedAfterRemove := lv.pausedForWebRTC
	lv.mu.Unlock()
	if pausedAfterRemove {
		t.Error(
			"ReconcileScreencast: RemoveViewer must resolve to \"resume\" (clear pausedForWebRTC) once no WebRTC viewers remain",
		)
	}
}

// TestCaptureSession_RelayViewerEviction_ResumesScreencast is the regression
// test for the "nothing resumes the JPEG screencast once WebRTC dies
// mid-session" bug (traced by the lead in webrtc/viewer.go's
// OnConnectionStateChange -> removeViewer): a viewer attaches via WebRTC,
// video is flowing, the screencast correctly pauses (matches
// TestCaptureSession_ReconcileScreencast_PausesWhenWebRTCCoversEveryJPEGViewer).
// The relay then evicts that SAME viewer entirely on its own -- an ICE
// failure or an unrecovered Disconnected timeout -- WITHOUT the gateway ever
// calling CaptureSession.RemoveViewer: no browser_detach frame arrives,
// because the browser_ws signaling connection and the JPEG live-view
// attachment both stay alive (BrowserLiveView.tsx's onFallback sends no
// frame back to the server on a WebRTC failure -- ADR-047's "JPEG never
// stopped running underneath" contract). Before this fix, CaptureSession had
// no way to learn of this relay-side-only eviction (ReconcileScreencast's
// other call sites -- AddViewer/RemoveViewer/Stop/HandleViewerOffer -- all
// require someone to notice and call in), so cs.viewers kept listing the
// dead viewer as WebRTC-covered forever and the JPEG screencast never
// resumed: the user was left staring at a frozen frame exactly when the
// fallback was supposed to save them.
//
// Uses fakeRelay's SetOnViewerRemoved/triggerViewerRemoved to simulate the
// relay-side eviction without real Pion/ICE machinery -- the real relay's
// OWN identity-checked notification wiring (removeViewer ->
// notifyViewerRemoved) has its own dedicated Go<->Go proof in
// pkg/tools/browser/webrtc/session_test.go; this test isolates
// CaptureSession's consumption of that notification (newCaptureSessionWithDeps
// wiring SetOnViewerRemoved to cs.RemoveViewer).
//
// Deliberately ZERO JPEG viewers (like TestCaptureSession_
// ReconcileScreencast_RemoveViewerResumesDecision/ResumesOnStop above) so the
// post-eviction resumeScreencast() call lands on its zero-viewers early
// return (clears the flag, no CDP call) rather than its
// mgr.Session()-dependent happy path -- keeps this a pure, Chrome-free unit
// test of the reconcile WIRING itself, per this file's "not real Chrome"
// testing directive. The "pause" decision itself doesn't need a live JPEG
// viewer either: with zero JPEG viewers attached, ReconcileScreencast's own
// "every attached JPEG viewer is WebRTC-covered" loop is vacuously true, so
// it still pauses (see the sibling tests' own setup for the same reasoning).
func TestCaptureSession_RelayViewerEviction_ResumesScreencast(t *testing.T) {
	mgr := newReconcileTestManager(t)
	lv := fakeActiveScreencast(mgr, DefaultSessionID) // zero JPEG viewers

	relay := &fakeRelay{stats: webrtc.Stats{HasVideo: true}}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	cs.AddViewer("webrtc-only-viewer")

	lv.mu.Lock()
	pausedAfterAdd := lv.pausedForWebRTC
	lv.mu.Unlock()
	if !pausedAfterAdd {
		t.Fatal("setup: expected the screencast to pause (no JPEG viewers to keep it running for)")
	}
	if got := cs.ViewerCount(); got != 1 {
		t.Fatalf("setup: ViewerCount = %d, want 1", got)
	}

	// Simulate the relay itself evicting "webrtc-only-viewer" (ICE failure /
	// disconnect timeout) -- no gateway-driven RemoveViewer/browser_detach
	// ever happens.
	relay.triggerViewerRemoved("webrtc-only-viewer")

	if got := cs.ViewerCount(); got != 0 {
		t.Fatalf(
			"ViewerCount after relay-side eviction = %d, want 0 (CaptureSession never learned the viewer left)",
			got,
		)
	}

	lv.mu.Lock()
	pausedAfterEviction := lv.pausedForWebRTC
	lv.mu.Unlock()
	if pausedAfterEviction {
		t.Error(
			"a relay-side viewer eviction (ICE failure) must resume the JPEG screencast, but pausedForWebRTC is still true -- the user would be stuck on a frozen frame",
		)
	}
}

func TestCaptureSession_ReconcileScreencast_ResumesOnStop(t *testing.T) {
	mgr := newReconcileTestManager(t)
	// Deliberately ZERO JPEG viewers here (unlike the other scenarios in
	// this file) so resumeScreencast's post-Stop() call lands on its
	// zero-viewers early return (clears the flag, no CDP call) rather than
	// its mgr.Session()-dependent happy path -- keeps this a pure,
	// Chrome-free unit test of the "resume on stop" wiring itself.
	lv := fakeActiveScreencast(mgr, DefaultSessionID)

	relay := &fakeRelay{stats: webrtc.Stats{HasVideo: true}}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	cs.AddViewer("webrtc-only-viewer")
	lv.mu.Lock()
	pausedBeforeStop := lv.pausedForWebRTC
	lv.mu.Unlock()
	if !pausedBeforeStop {
		t.Fatal("setup: expected the screencast to pause (no JPEG viewers to keep it running for)")
	}

	cs.Stop()

	lv.mu.Lock()
	pausedAfterStop := lv.pausedForWebRTC
	lv.mu.Unlock()
	if pausedAfterStop {
		t.Error(
			"ReconcileScreencast: Stop() must resume (clear pausedForWebRTC) now that WebRTC capture has stopped entirely",
		)
	}
}

// --- LiveView-level mechanics: pauseScreencast/resumeScreencast/attach ---
// (mirrors live_test.go's hand-built-LiveView convention exactly; kept in
// this file since it's the fix-wave finding 3 coverage, not a pre-existing
// live.go concern.)

func TestLiveView_PauseScreencast_StopsSubscriptionKeepsViewers(t *testing.T) {
	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}
	lv := &LiveView{mgr: mgr, sessionID: "s1", viewers: make(map[string]FrameSink), runCDP: runCDPWithTimeout}

	listenCtx, cancel := context.WithCancel(context.Background())
	lv.tabCtx = context.Background()
	lv.listenCtx = listenCtx
	lv.stopListen = cancel
	lv.viewers["v1"] = func(LiveFrame) {}
	lastFrame := &LiveFrame{Seq: 7, Data: "cached"}
	lv.lastFrame = lastFrame

	if !lv.pauseScreencast() {
		t.Fatal("pauseScreencast() = false on an active screencast, want true")
	}

	lv.mu.Lock()
	defer lv.mu.Unlock()
	if !lv.pausedForWebRTC {
		t.Error("pausedForWebRTC = false after pauseScreencast(), want true")
	}
	if lv.isActiveLocked() {
		t.Error("isActiveLocked() = true after pauseScreencast(), want false (subscription torn down)")
	}
	if _, ok := lv.viewers["v1"]; !ok {
		t.Error("pauseScreencast() removed a viewer registration, want it untouched")
	}
	if lv.lastFrame != lastFrame {
		t.Error("pauseScreencast() cleared the cached lastFrame, want it preserved for late attachers")
	}
	if listenCtx.Err() == nil {
		t.Error("pauseScreencast() did not cancel the listen subscription")
	}
}

func TestLiveView_PauseScreencast_NoopWhenNotActive(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	if lv.pauseScreencast() {
		t.Error("pauseScreencast() = true with no active screencast, want false (no-op)")
	}
}

func TestLiveView_PauseScreencast_NoopWhenAlreadyPaused(t *testing.T) {
	mgr := &BrowserManager{cfg: BrowserConfig{PageTimeout: 5 * time.Second}}
	lv := &LiveView{mgr: mgr, sessionID: "s1", viewers: make(map[string]FrameSink), runCDP: runCDPWithTimeout}
	listenCtx, cancel := context.WithCancel(context.Background())
	lv.tabCtx = context.Background()
	lv.listenCtx = listenCtx
	lv.stopListen = cancel

	if !lv.pauseScreencast() {
		t.Fatal("first pauseScreencast() = false, want true")
	}
	if lv.pauseScreencast() {
		t.Error("second pauseScreencast() = true, want false (already paused, idempotent no-op)")
	}
}

func TestLiveView_Attach_WhilePausedDeliversCachedFrameWithoutRestartingScreencast(t *testing.T) {
	lv := &LiveView{
		sessionID:    "s1",
		viewers:      make(map[string]FrameSink),
		statusSinks:  make(map[string]StatusSink),
		controlSinks: make(map[string]ControlSink),
		tabsSinks:    make(map[string]TabsSink),
	}
	lv.pausedForWebRTC = true
	cached := LiveFrame{Seq: 3, Data: "paused-frame"}
	lv.lastFrame = &cached

	var got *LiveFrame
	controlledByOther, err := lv.attach(
		context.Background(),
		"late-viewer",
		func(f LiveFrame) { got = &f },
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("attach() while paused: %v", err)
	}
	if controlledByOther {
		t.Error("attach() while paused reported controlled_by_other=true with no controller set")
	}
	if got == nil || got.Seq != 3 || got.Data != "paused-frame" {
		t.Fatalf("attach() while paused did not deliver the cached lastFrame, got %+v", got)
	}

	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.listenCtx != nil {
		t.Error(
			"attach() while paused started a fresh CDP screencast subscription (listenCtx != nil), want it left alone",
		)
	}
	if !lv.pausedForWebRTC {
		t.Error("attach() while paused cleared pausedForWebRTC, want it untouched -- resuming is the caller's job")
	}
	if _, ok := lv.viewers["late-viewer"]; !ok {
		t.Error("attach() while paused did not register the new viewer")
	}
}

func TestLiveView_ResumeScreencast_NoopWhenNotPaused(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	if lv.resumeScreencast() {
		t.Error("resumeScreencast() = true on a never-paused LiveView, want false (no-op)")
	}
}

func TestLiveView_ResumeScreencast_ClearsFlagWhenNoViewersRemain(t *testing.T) {
	lv := &LiveView{sessionID: "s1", viewers: make(map[string]FrameSink)}
	lv.pausedForWebRTC = true

	if lv.resumeScreencast() {
		t.Error("resumeScreencast() = true with zero viewers, want false (nothing to resume for)")
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.pausedForWebRTC {
		t.Error(
			"resumeScreencast() left pausedForWebRTC=true with zero viewers, want it cleared so a future Attach starts fresh",
		)
	}
}
