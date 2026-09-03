package browser

// capture_session_test.go — ADR-047 / wave-plan W2-A unit coverage for
// CaptureSession's lifecycle logic, using a fake RelaySession (this
// package's own narrow interface, per the wave-plan's "fake webrtc.Session
// behind a narrow interface you define at the consumption point") and a
// fake EncoderStarter, so no real chromedp/Pion machinery is touched.

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// newCaptureTestManager builds a real BrowserManager (no chromedp/CDP
// machinery touched by anything in this file — every CaptureSession test
// uses a fake RelaySession/EncoderStarter) for the Shutdown/
// invalidateConnection orphaned-capture-session tests below, which need a
// real manager to call m.Shutdown()/m.invalidateConnection() on.
func newCaptureTestManager(t *testing.T) *BrowserManager {
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

// fakeRelay is a test double for RelaySession.
type fakeRelay struct {
	mu             sync.Mutex
	ingestOfferSDP string
	ingestErr      error
	viewerOffers   map[string]string
	viewerErr      error
	closedViewers  []string
	recaptureCalls int
	closeCalls     int
	stats          webrtc.Stats
	// videoArrivesOnOffer, when true, flips stats.HasVideo to true as part
	// of a SUCCESSFUL HandleViewerOffer call (FIX WAVE A finding 2
	// coverage) — a faithful-enough model of the real relay's contract
	// (pkg/tools/browser/webrtc/viewer.go's waitForTracks blocks until the
	// ingest video track exists BEFORE HandleViewerOffer can ever return a
	// success answer), so a test can simulate "video started flowing during
	// this exact offer" without needing real ingest/CDP machinery.
	videoArrivesOnOffer bool
	// onViewerRemoved backs the optional viewerRemover capability
	// (SetOnViewerRemoved) -- set by newCaptureSessionWithDeps's type
	// assertion since fakeRelay implements it, letting a test simulate a
	// RELAY-SIDE-ONLY viewer eviction (an ICE failure, or an unrecovered
	// disconnect-grace timeout) via triggerViewerRemoved below, exactly the
	// contract the real webrtc.Session honors from its own removeViewer.
	onViewerRemoved func(viewerID string, handle any)
	onIngestLost    func()
}

// SetOnViewerRemoved implements the viewerRemover optional capability (see
// capture_session.go) so tests can exercise CaptureSession's wiring of it
// without a real webrtc.Session/Pion PeerConnection.
func (f *fakeRelay) SetOnViewerRemoved(fn func(viewerID string, handle any)) {
	f.mu.Lock()
	f.onViewerRemoved = fn
	f.mu.Unlock()
}

// triggerViewerRemoved simulates the relay itself evicting viewerID (e.g. an
// ICE failure, or an unrecovered disconnect-grace timeout) — exactly the
// SetOnViewerRemoved contract the real webrtc.Session honors from its own
// removeViewer (pkg/tools/browser/webrtc/viewer.go). Test-only; a no-op if
// nothing registered a callback (SetOnViewerRemoved never called, or called
// with a nil fn). Passes a nil handle: fakeRelay carries no viewerOfferHandler
// capability, so a viewerID registered via plain AddViewer (this fake's own
// tests never call HandleViewerOffer through a real offerHandler) has no
// relay identity recorded (viewerRegistration.relayHandle stays nil,
// recordViewerRelayHandle's own zero value) — nil correctly matches that,
// exercising the SAME removeViewerByRelayHandle path production uses rather
// than a parallel, weaker test-only mechanism.
func (f *fakeRelay) triggerViewerRemoved(viewerID string) {
	f.mu.Lock()
	fn := f.onViewerRemoved
	f.mu.Unlock()
	if fn != nil {
		fn(viewerID, nil)
	}
}

// SetOnIngestLost implements the ingestLossNotifier optional capability (see
// capture_session.go) so tests can exercise CaptureSession's wiring of it
// without a real Pion PeerConnection dying.
func (f *fakeRelay) SetOnIngestLost(fn func()) {
	f.mu.Lock()
	f.onIngestLost = fn
	f.mu.Unlock()
}

// triggerIngestLost simulates the relay's ingest peer connection dying —
// exactly the SetOnIngestLost contract the real webrtc.Session honors from its
// OnConnectionStateChange handler.
func (f *fakeRelay) triggerIngestLost() {
	f.mu.Lock()
	fn := f.onIngestLost
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (f *fakeRelay) HandleIngestOffer(sdp string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ingestErr != nil {
		return "", f.ingestErr
	}
	f.ingestOfferSDP = sdp
	return "ingest-answer-sdp", nil
}

func (f *fakeRelay) HandleViewerOffer(viewerID, sdp string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.viewerErr != nil {
		return "", f.viewerErr
	}
	if f.viewerOffers == nil {
		f.viewerOffers = make(map[string]string)
	}
	f.viewerOffers[viewerID] = sdp
	if f.videoArrivesOnOffer {
		f.stats.HasVideo = true
	}
	return "viewer-answer-" + viewerID, nil
}

func (f *fakeRelay) CloseViewer(viewerID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedViewers = append(f.closedViewers, viewerID)
}

func (f *fakeRelay) SignalRecapture() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recaptureCalls++
}

func (f *fakeRelay) SendToViewer(string, []byte) error { return nil }

func (f *fakeRelay) Stats() webrtc.Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stats
}

func (f *fakeRelay) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

func (f *fakeRelay) recaptureCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recaptureCalls
}

func (f *fakeRelay) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCalls
}

// fakeEncoderStarter returns an EncoderStarter that never touches real
// chromedp: it just counts invocations and returns either a canceled-on-cancel
// bare context or the configured error.
func fakeEncoderStarter(callCount *int32, startErr error) EncoderStarter {
	return func(_ context.Context, _ *BrowserManager, _, _, _, _ string) (context.Context, context.CancelFunc, error) {
		atomic.AddInt32(callCount, 1)
		if startErr != nil {
			return nil, nil, startErr
		}
		ctx, cancel := context.WithCancel(context.Background())
		return ctx, cancel, nil
	}
}

func newTestCaptureSession(t *testing.T, relay *fakeRelay, starter EncoderStarter) *CaptureSession {
	t.Helper()
	cs, err := NewCaptureSessionWithDeps(nil, "agent-1", relay, starter, nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}
	return cs
}

func TestCaptureSession_StartIsSingleFlightAcrossConcurrentCallers(t *testing.T) {
	var calls int32
	relay := &fakeRelay{}
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	const n = 10
	var wg sync.WaitGroup
	justStartedCount := int32(0)
	errCount := int32(0)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
			if err != nil {
				atomic.AddInt32(&errCount, 1)
				return
			}
			if started {
				atomic.AddInt32(&justStartedCount, 1)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("encoderStarter called %d times, want exactly 1 (single-flight)", got)
	}
	if got := atomic.LoadInt32(&justStartedCount); got != 1 {
		t.Fatalf("justStarted=true reported %d times, want exactly 1", got)
	}
	if got := atomic.LoadInt32(&errCount); got != 0 {
		t.Fatalf("%d Start() callers got an error, want 0", got)
	}

	// A subsequent Start() after the first batch completed must also be a
	// no-op (already started): no new encoderStarter call, justStarted=false.
	started, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	if err != nil {
		t.Fatalf("Start after already-started: %v", err)
	}
	if started {
		t.Fatal("Start reported justStarted=true on an already-started session")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("encoderStarter called again on an already-started session: %d calls", got)
	}
}

func TestCaptureSession_StartPropagatesEncoderError(t *testing.T) {
	var calls int32
	wantErr := errors.New("encoder boom")
	relay := &fakeRelay{}
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, wantErr))

	// justStarted reports "this call ran the startup attempt" (true here —
	// exactly one caller always runs it, success or failure), NOT "the
	// startup succeeded" — callers (browser_webrtc.go's handleWebRTCOffer)
	// MUST check err first and only consult justStarted once err is nil
	// (which is exactly what it does: it returns on err != nil before ever
	// reading justStarted). What matters here is that the error propagates.
	started, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start error = %v, want wrapping %v", err, wantErr)
	}
	_ = started

	// A stopped-before-first-successful-start session must refuse reuse.
	cs.Stop()
	started, err = cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
	if started || err == nil {
		t.Fatalf("Start on a stopped session = (%v, %v), want (false, non-nil)", started, err)
	}
}

// TestDefaultEncoderStarter_NoRootContext_ReturnsSharedChromeNotLiveError
// calls the REAL production defaultEncoderStarter (never a fake) — every
// other Start()/EncoderStarter test in this file substitutes
// fakeEncoderStarter, so defaultEncoderStarter itself had zero direct unit
// coverage. Reaches its step-3 gate (capture_session.go: "rootCtx :=
// coord.rootContext(); if rootCtx == nil { return ... "shared Chrome is not
// live" }") by constructing a coordinator that is deliberately never
// launched (rootCtx stays nil by construction — NewBrowserCoordinator never
// touches it), while making steps 1-2 succeed WITHOUT a real Chrome:
//   - step 1 (mgr.Session(testSessionID)): a hand-planted sessionEntry
//     with a live (never-canceled) tab context, mirroring sessionEntry's own
//     doc comment ("nil only for hand-built sessionEntry literals in tests
//     that bypass Session()/createFirstTab entirely... every nil-checked at
//     its two call sites") and live_deadlock_test.go's identical technique —
//     plus mgr.started=true so ensureStarted() never attempts a real
//     coordinator.Register() launch.
//   - step 2 (coord.LoadedExtensionID() != captureext.ExtensionID): the
//     coordinator's loadedExtensionID is set directly to captureext.ExtensionID
//     so LoadExtension (which itself needs a live rootCtx) is never called —
//     calling it here would fail for a DIFFERENT reason and mask the one
//     this test targets.
func TestDefaultEncoderStarter_NoRootContext_ReturnsSharedChromeNotLiveError(t *testing.T) {
	coord := NewBrowserCoordinator(t.TempDir(), BrowserConfig{})
	// Never Register()'d/launched — coord.rootCtx stays nil, which is exactly
	// the precondition defaultEncoderStarter's step 3 gate checks for.

	fakeCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := &BrowserManager{
		cfg:      BrowserConfig{ProfileDir: t.TempDir() + "/profiles/default"},
		started:  true,
		sessions: map[string]*sessionEntry{},
	}
	mgr.AttachSharedChrome(coord, browserTestKey("agent-no-root-ctx"))
	// The capture path resolves the WORKSPACE-OWNED tab set (the live panel is
	// the operator, ADR-072 §0.2a), so the hand-planted entry must be keyed the
	// way production will look it up — AttachSharedChrome above is what gives
	// the manager the browsing key that key is built from.
	mgr.sessions[mgr.OperatorSessionID()] = &sessionEntry{
		tabs:      []*tabEntry{{ctx: fakeCtx, cancel: cancel}},
		activeIdx: 0,
	}

	coord.mu.Lock()
	coord.loadedExtensionID = captureext.ExtensionID
	coord.mu.Unlock()

	_, _, err := defaultEncoderStarter(
		context.Background(),
		mgr,
		"", // no panel context: falls back to the operator's set, as before (#671)
		"deadbeef",
		"ws://127.0.0.1:1/api/v1/browser/capture-ingest",
		"",
	)
	if err == nil {
		t.Fatal("expected an error when the coordinator has no live root context")
	}
	if !strings.Contains(err.Error(), "shared Chrome is not live") {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), "shared Chrome is not live")
	}
}

// TestCaptureSession_StopWhileStarting_NoOrphanedEncoderTarget is the
// regression guard for the Start()/Stop() race capture_session.go's Start()
// explicitly handles (the "cs.stopped" branch inside startOnce.Do, comment:
// "A Stop() (e.g. browser death detected concurrently) raced this Start()
// and won — tear down what we just built rather than leaving an orphaned
// encoder target nobody will ever close"). Blocks a fake EncoderStarter
// mid-flight, calls Stop() while Start() is still inside it (so Stop()'s own
// tabCancel snapshot is nil — cs.tabCtx/tabCancel are only assigned AFTER
// startEncoder returns), then releases the block and proves: (a) Start()
// itself returns a non-nil "stopped while starting" error, (b) the
// encoder-context cancel func startEncoder returned WAS invoked (no orphaned
// target/leaked context) even though Stop() never saw it.
func TestCaptureSession_StopWhileStarting_NoOrphanedEncoderTarget(t *testing.T) {
	relay := &fakeRelay{}

	startCalled := make(chan struct{})
	releaseStart := make(chan struct{})
	cancelCalled := make(chan struct{})
	var cancelOnce sync.Once

	starter := EncoderStarter(
		func(context.Context, *BrowserManager, string, string, string, string) (context.Context, context.CancelFunc, error) {
			close(startCalled)
			<-releaseStart // block until the test releases it, simulating a slow chromedp.Run
			tabCtx, cancel := context.WithCancel(context.Background())
			wrappedCancel := func() {
				cancelOnce.Do(func() { close(cancelCalled) })
				cancel()
			}
			return tabCtx, wrappedCancel, nil
		},
	)

	cs := newTestCaptureSession(t, relay, starter)

	startErrCh := make(chan error, 1)
	go func() {
		_, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
		startErrCh <- err
	}()

	select {
	case <-startCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("startEncoder was never invoked")
	}

	// Stop() races in and wins while Start() is still blocked inside
	// startEncoder — cs.tabCtx/tabCancel are still unset at this point, so
	// Stop()'s own tabCancel snapshot is nil (asserted indirectly: Stop()
	// completing here without cancelCalled having fired yet is the proof).
	cs.Stop()
	select {
	case <-cancelCalled:
		t.Fatal(
			"tabCancel fired from Stop() itself before startEncoder even returned — precondition of this test is violated",
		)
	default:
	}

	close(releaseStart)

	select {
	case err := <-startErrCh:
		if err == nil {
			t.Fatal("Start() must return an error when Stop() raced it while starting")
		}
		if !strings.Contains(err.Error(), "stopped while starting") {
			t.Fatalf("Start() error = %q, want it to mention %q", err.Error(), "stopped while starting")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start() never returned after being unblocked")
	}

	select {
	case <-cancelCalled:
		// Good: the encoder target's own tabCancel was invoked by Start()'s
		// stopped-while-starting branch — no orphaned encoder target.
	case <-time.After(2 * time.Second):
		t.Fatal("tabCancel was never called on the encoder context Start() abandoned — orphaned encoder target")
	}
}

func TestCaptureSession_ValidateToken(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	tok := cs.TokenHex()
	if !cs.ValidateToken(tok) {
		t.Fatal("ValidateToken rejected the session's own minted token")
	}
	if cs.ValidateToken("") {
		t.Fatal("ValidateToken accepted an empty token")
	}
	if cs.ValidateToken("not-hex-zz") {
		t.Fatal("ValidateToken accepted a malformed (non-hex) token")
	}
	if cs.ValidateToken("aa") {
		t.Fatal("ValidateToken accepted a wrong-length token")
	}
	// A different session's token must not validate against this one.
	other := newTestCaptureSession(t, &fakeRelay{}, fakeEncoderStarter(&calls, nil))
	if cs.ValidateToken(other.TokenHex()) {
		t.Fatal("ValidateToken accepted a DIFFERENT session's token")
	}
}

func TestCaptureSession_BindIngestSupersedesPreviousConnection(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	firstClosed := false
	_, epoch1 := cs.BindIngest(
		func(string, *string, int, int, int) error { return nil },
		func() { firstClosed = true },
	)
	if epoch1 == 0 {
		t.Fatal("BindIngest returned epoch 0 on first bind")
	}

	prevClose, epoch2 := cs.BindIngest(
		func(string, *string, int, int, int) error { return nil },
		func() {},
	)
	if epoch2 == epoch1 {
		t.Fatal("second BindIngest returned the SAME epoch as the first — supersede tracking broken")
	}
	if prevClose == nil {
		t.Fatal("second BindIngest returned a nil previousClose — the first connection's close callback was lost")
	}
	prevClose()
	if !firstClosed {
		t.Fatal("invoking the returned previousClose did not close the first connection")
	}

	// UnbindIngest with the STALE (first) epoch must NOT clear the current
	// (second) binding.
	cs.UnbindIngest(epoch1)
	sent := false
	cs.mu.Lock()
	stillBound := cs.ingestSend != nil
	cs.mu.Unlock()
	if !stillBound {
		t.Fatal("UnbindIngest with a stale epoch cleared the CURRENT ingest binding")
	}

	// UnbindIngest with the CURRENT epoch clears it.
	cs.UnbindIngest(epoch2)
	cs.mu.Lock()
	stillBound = cs.ingestSend != nil
	cs.mu.Unlock()
	if stillBound {
		t.Fatal("UnbindIngest with the current epoch failed to clear the binding")
	}
	_ = sent
}

func TestCaptureSession_RecapturePropagatesToRelayAndIngest(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	var gotAction string
	var gotW, gotH int
	var mu sync.Mutex
	cs.BindIngest(func(action string, _ *string, expectedW, expectedH int, _ int) error {
		mu.Lock()
		gotAction = action
		gotW, gotH = expectedW, expectedH
		mu.Unlock()
		return nil
	}, func() {})

	cs.Recapture()

	if got := relay.recaptureCount(); got != 1 {
		t.Fatalf("relay.SignalRecapture called %d times, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAction != "recapture" {
		t.Fatalf("ingest send action = %q, want \"recapture\"", gotAction)
	}
	if gotW != 0 || gotH != 0 {
		t.Fatalf("Recapture() (no dims) sent expected %dx%d, want 0x0 (absent)", gotW, gotH)
	}
}

// TestCaptureSession_RecaptureAtPropagatesExpectedDims proves the
// dimension-carrying path this task adds: RecaptureAt must thread its
// expectedW/expectedH straight through to the ingest send callback, so the
// gateway's browser_ws.go handleViewport can hand the encoder the
// CDP-verified CSS viewport instead of leaving it to race the OS window
// reflow via its own chrome.tabs.get poll (see RecaptureAt's doc comment,
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md follow-up).
func TestCaptureSession_RecaptureAtPropagatesExpectedDims(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	var gotAction string
	var gotW, gotH int
	var mu sync.Mutex
	cs.BindIngest(func(action string, _ *string, expectedW, expectedH int, _ int) error {
		mu.Lock()
		gotAction = action
		gotW, gotH = expectedW, expectedH
		mu.Unlock()
		return nil
	}, func() {})

	cs.RecaptureAt(615, 744)

	if got := relay.recaptureCount(); got != 1 {
		t.Fatalf("relay.SignalRecapture called %d times, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAction != "recapture" {
		t.Fatalf("ingest send action = %q, want \"recapture\"", gotAction)
	}
	if gotW != 615 || gotH != 744 {
		t.Fatalf("RecaptureAt(615, 744) sent expected %dx%d, want 615x744", gotW, gotH)
	}
}

func TestCaptureSession_RecaptureWithNoIngestBoundIsNoop(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	// No BindIngest call — must not panic, and the relay side still fires.
	cs.Recapture()
	if got := relay.recaptureCount(); got != 1 {
		t.Fatalf("relay.SignalRecapture called %d times, want 1", got)
	}
}

func TestCaptureSession_StopSendsShutdownClosesViewersAndRelay(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	if _, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cs.AddViewer("viewer-a")
	cs.AddViewer("viewer-b")

	var gotAction string
	var reasonSet bool
	cs.BindIngest(func(action string, reason *string, _, _ int, _ int) error {
		gotAction = action
		reasonSet = reason != nil
		return nil
	}, func() {})

	stoppedCalled := false
	cs.SetOnStopped(func() { stoppedCalled = true })

	cs.Stop()

	if gotAction != "shutdown" {
		t.Fatalf("ingest send action on Stop = %q, want \"shutdown\"", gotAction)
	}
	if !reasonSet {
		t.Fatal("shutdown control frame carried no reason")
	}
	if got := relay.closeCount(); got != 1 {
		t.Fatalf("relay.Close called %d times, want 1", got)
	}
	relay.mu.Lock()
	closedViewers := append([]string(nil), relay.closedViewers...)
	relay.mu.Unlock()
	if len(closedViewers) != 2 {
		t.Fatalf("relay.CloseViewer called for %d viewers, want 2 (got %v)", len(closedViewers), closedViewers)
	}
	if !stoppedCalled {
		t.Fatal("SetOnStopped callback was not invoked")
	}

	// Stop must be idempotent — a second call must not panic or double-fire
	// onStopped/relay.Close.
	cs.Stop()
	if got := relay.closeCount(); got != 1 {
		t.Fatalf("relay.Close called %d times after a second Stop(), want still 1 (idempotent)", got)
	}
}

func TestCaptureSession_RemoveViewer_GraceStopFiresAfterTimer(t *testing.T) {
	orig := captureGracePeriod
	captureGracePeriod = 30 * time.Millisecond
	defer func() { captureGracePeriod = orig }()

	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))
	if _, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cs.AddViewer("only-viewer")
	if got := cs.ViewerCount(); got != 1 {
		t.Fatalf("ViewerCount = %d, want 1", got)
	}

	cs.RemoveViewer("only-viewer")
	if got := cs.ViewerCount(); got != 0 {
		t.Fatalf("ViewerCount after RemoveViewer = %d, want 0", got)
	}

	// Not stopped immediately — the grace timer hasn't fired yet.
	if relay.closeCount() != 0 {
		t.Fatal("relay.Close called before the grace period elapsed")
	}

	deadline := time.Now().Add(2 * time.Second)
	for relay.closeCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := relay.closeCount(); got != 1 {
		t.Fatalf("relay.Close called %d times after grace period, want 1 (Stop should have fired)", got)
	}
}

func TestCaptureSession_AddViewerCancelsGraceTimer(t *testing.T) {
	orig := captureGracePeriod
	captureGracePeriod = 30 * time.Millisecond
	defer func() { captureGracePeriod = orig }()

	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))
	if _, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cs.AddViewer("v1")
	cs.RemoveViewer("v1") // arms the grace timer
	cs.AddViewer("v2")    // must cancel it

	time.Sleep(100 * time.Millisecond) // well past the (short) grace period
	if got := relay.closeCount(); got != 0 {
		t.Fatalf("relay.Close called %d times — grace timer fired despite a new viewer joining", got)
	}
}

// TestCaptureSession_RelayViewerEviction_RemovesViewerAndArmsGraceTimer
// proves CaptureSession's consumption of a RELAY-SIDE-ONLY viewer eviction
// (ICE failure / disconnect-grace timeout) — no gateway-driven RemoveViewer
// or browser_detach ever happens; the relay itself notifies via
// SetOnViewerRemoved, wired to cs.removeViewerByRelayHandle
// (newCaptureSessionWithDeps). Uses fakeRelay's SetOnViewerRemoved/
// triggerViewerRemoved to simulate the eviction without real Pion/ICE
// machinery — the real relay's OWN identity-checked notification wiring
// (removeViewer -> notifyViewerRemoved) has its own dedicated Go<->Go proof
// in pkg/tools/browser/webrtc/session_test.go; this test isolates
// CaptureSession's consumption of that notification: the evicted viewer must
// actually be removed from cs.viewers (ViewerCount drops to 0) and the
// grace-stop timer must arm exactly as it would for an explicit
// RemoveViewer, so an orphaned WebRTC session doesn't linger forever because
// nobody ever told it its one viewer is gone.
func TestCaptureSession_RelayViewerEviction_RemovesViewerAndArmsGraceTimer(t *testing.T) {
	orig := captureGracePeriod
	captureGracePeriod = 30 * time.Millisecond
	defer func() { captureGracePeriod = orig }()

	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))
	if _, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cs.AddViewer("webrtc-only-viewer")
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

	// The grace-stop timer must have armed exactly as it would for an
	// explicit RemoveViewer — otherwise a relay-side eviction leaves the
	// capture session (and its encoder tab) running forever with zero
	// viewers and nothing to ever stop it.
	deadline := time.Now().Add(2 * time.Second)
	for relay.closeCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := relay.closeCount(); got != 1 {
		t.Fatalf("relay.Close called %d times after grace period, want 1 (Stop should have fired)", got)
	}
}

func TestBrowserManager_CaptureSessionRegistryLifecycle(t *testing.T) {
	m := &BrowserManager{}

	if got := m.CaptureSession(); got != nil {
		t.Fatalf("CaptureSession on a fresh manager = %v, want nil", got)
	}

	var calls int32
	built := 0
	newFn := func() (*CaptureSession, error) {
		built++
		return newTestCaptureSession(t, &fakeRelay{}, fakeEncoderStarter(&calls, nil)), nil
	}

	cs1, err := m.EnsureCaptureSession(newFn)
	if err != nil {
		t.Fatalf("EnsureCaptureSession: %v", err)
	}
	cs2, err := m.EnsureCaptureSession(newFn)
	if err != nil {
		t.Fatalf("EnsureCaptureSession (second call): %v", err)
	}
	if cs1 != cs2 {
		t.Fatal(
			"EnsureCaptureSession constructed a second session — must reuse the existing one (one active stream per agent)",
		)
	}
	if built != 1 {
		t.Fatalf("newFn invoked %d times, want exactly 1", built)
	}
	if m.CaptureSession() != cs1 {
		t.Fatal("CaptureSession() does not return the session EnsureCaptureSession installed")
	}

	// ClearCaptureSession with a STALE reference must not clear the current one.
	stale := newTestCaptureSession(t, &fakeRelay{}, fakeEncoderStarter(&calls, nil))
	m.ClearCaptureSession(stale)
	if m.CaptureSession() != cs1 {
		t.Fatal("ClearCaptureSession cleared the registry using a stale (non-current) session reference")
	}

	m.ClearCaptureSession(cs1)
	if m.CaptureSession() != nil {
		t.Fatal("ClearCaptureSession(current) did not clear the registry")
	}

	// A fresh EnsureCaptureSession after clearing constructs a new session.
	cs3, err := m.EnsureCaptureSession(newFn)
	if err != nil {
		t.Fatalf("EnsureCaptureSession after clear: %v", err)
	}
	if cs3 == cs1 {
		t.Fatal("EnsureCaptureSession after ClearCaptureSession reused the torn-down session")
	}
	if built != 2 {
		t.Fatalf("newFn invoked %d times after clear+re-ensure, want 2", built)
	}
}

// ---------------------------------------------------------------------------
// Fix-wave (ADR-048): Done()/LastPingAt()/ViewerIDs() — the encoder-liveness
// watchdog's primitives (pkg/gateway/browser_webrtc.go's watchEncoderLiveness
// reads all three).
// ---------------------------------------------------------------------------

func TestCaptureSession_LastPingAt_ZeroUntilRecorded(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	if got := cs.LastPingAt(); !got.IsZero() {
		t.Fatalf("LastPingAt() before any ping/bind = %v, want zero time.Time", got)
	}

	before := time.Now()
	cs.RecordPing()
	after := time.Now()

	got := cs.LastPingAt()
	if got.Before(before) || got.After(after) {
		t.Fatalf("LastPingAt() = %v, want between %v and %v", got, before, after)
	}
}

func TestCaptureSession_Done_ClosesExactlyOnceOnStop(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	select {
	case <-cs.Done():
		t.Fatal("Done() channel is already closed before Stop() was ever called")
	default:
	}

	cs.Stop()
	select {
	case <-cs.Done():
	default:
		t.Fatal("Done() channel must be closed immediately after Stop() completes")
	}

	// A second Stop() must not panic (double-close would panic the channel
	// close, not just be a logic bug) — Stop()'s own cs.stopped guard must
	// prevent a second close(cs.done).
	cs.Stop()
}

func TestCaptureSession_ViewerIDs_SnapshotAndSurvivesStop(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	if got := cs.ViewerIDs(); len(got) != 0 {
		t.Fatalf("ViewerIDs() on a fresh session = %v, want empty", got)
	}

	cs.AddViewer("viewer-a")
	cs.AddViewer("viewer-b")

	got := cs.ViewerIDs()
	sort.Strings(got)
	want := []string{"viewer-a", "viewer-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ViewerIDs() = %v, want %v", got, want)
	}

	// Stop() does not clear cs.viewers (only relay.CloseViewer is called per
	// id) — ViewerIDs() must remain accurate to call from INSIDE an
	// onStopped callback, which is exactly how the gateway's
	// notifyViewersStreamStopped (fix 3) uses it: to know who to push a
	// final browser_webrtc_state to.
	if _, err := cs.Start(context.Background(), "ws://127.0.0.1:1/api/v1/browser/capture-ingest"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cs.Stop()

	got = cs.ViewerIDs()
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ViewerIDs() after Stop() = %v, want still %v (Stop must not clear cs.viewers)", got, want)
	}
}

// ---------------------------------------------------------------------------
// Fix-wave CRIT (reviewer 1, conf 88): Shutdown()/invalidateConnection() must
// stop an orphaned CaptureSession rather than leaving it (and its encoder
// tab, living in the coordinator's own root context — see
// defaultEncoderStarter — outside anything these teardown paths otherwise
// cancel) running forever, unstoppable once the manager itself is gone.
// ---------------------------------------------------------------------------

func TestBrowserManager_Shutdown_StopsOrphanedCaptureSession(t *testing.T) {
	m := newCaptureTestManager(t)

	relay := &fakeRelay{}
	var calls int32
	cs, err := NewCaptureSessionWithDeps(m, "agent-shutdown", relay, fakeEncoderStarter(&calls, nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}
	if _, err := m.EnsureCaptureSession(func() (*CaptureSession, error) { return cs, nil }); err != nil {
		t.Fatalf("EnsureCaptureSession: %v", err)
	}
	if m.CaptureSession() != cs {
		t.Fatal("manager did not register the capture session via EnsureCaptureSession")
	}

	var stoppedCalled int32
	cs.SetOnStopped(func() { atomic.AddInt32(&stoppedCalled, 1) })

	m.Shutdown()

	if got := atomic.LoadInt32(&stoppedCalled); got != 1 {
		t.Fatalf(
			"SetOnStopped callback fired %d times after Shutdown(), want exactly 1 — CaptureSession was not stopped",
			got,
		)
	}
	if m.CaptureSession() != nil {
		t.Fatal("CaptureSession() is still non-nil after Shutdown() stopped it — ClearCaptureSession did not run")
	}
	if got := relay.closeCount(); got != 1 {
		t.Fatalf("relay.Close called %d times, want 1 (Stop() must have torn down the relay)", got)
	}
}

// TestBrowserManager_Shutdown_NoCaptureSessionIsNoop proves the new guard is
// safe on a manager that never had a WebRTC capture session at all (the
// common case) — m.CaptureSession() returning nil must not panic or alter
// Shutdown's existing behavior.
func TestBrowserManager_Shutdown_NoCaptureSessionIsNoop(t *testing.T) {
	m := newCaptureTestManager(t)
	m.Shutdown() // must not panic
	if m.CaptureSession() != nil {
		t.Fatal("CaptureSession() on a manager that never had one should stay nil after Shutdown")
	}
}

func TestBrowserManager_InvalidateConnection_StopsOrphanedCaptureSession(t *testing.T) {
	m := newCaptureTestManager(t)

	relay := &fakeRelay{}
	var calls int32
	cs, err := NewCaptureSessionWithDeps(m, "agent-invalidate", relay, fakeEncoderStarter(&calls, nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}
	if _, err := m.EnsureCaptureSession(func() (*CaptureSession, error) { return cs, nil }); err != nil {
		t.Fatalf("EnsureCaptureSession: %v", err)
	}

	var stoppedCalled int32
	cs.SetOnStopped(func() { atomic.AddInt32(&stoppedCalled, 1) })

	m.invalidateConnection()

	if got := atomic.LoadInt32(&stoppedCalled); got != 1 {
		t.Fatalf("SetOnStopped callback fired %d times after invalidateConnection(), want exactly 1", got)
	}
	if m.CaptureSession() != nil {
		t.Fatal("CaptureSession() is still non-nil after invalidateConnection() stopped it")
	}
}

func TestCaptureSession_HandleOffersDelegateToRelay(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	answer, err := cs.HandleIngestOffer("ingest-offer-sdp")
	if err != nil || answer != "ingest-answer-sdp" {
		t.Fatalf("HandleIngestOffer = (%q, %v), want (\"ingest-answer-sdp\", nil)", answer, err)
	}

	gen := cs.AddViewer("viewer-x")
	answer, handle, err := cs.HandleViewerOffer("viewer-x", "viewer-offer-sdp", gen)
	if err != nil || answer != "viewer-answer-viewer-x" {
		t.Fatalf("HandleViewerOffer = (%q, %v), want (\"viewer-answer-viewer-x\", nil)", answer, err)
	}
	if handle == nil {
		t.Fatal("HandleViewerOffer returned a nil handle on success, want non-nil")
	}
}

// ---------------------------------------------------------------------------
// Supersede-safety fix (reviewer finding, traced mechanism): a superseded
// WebRTC offer's cleanup could tear down a NEWER, already-committed offer's
// live viewer connection, because CaptureSession.viewers was keyed ONLY by
// viewerID with no notion of "which attempt." AddViewer now mints a
// generation token per attempt; RemoveViewerIfCurrent/CleanupViewerOffer
// only act if that generation is still the one currently registered. The
// relay-level half of this fix (webrtc.Session.CloseViewerIfCurrent, using a
// REAL *ViewerHandle identity check against real Pion PeerConnections) has
// its own dedicated Go<->Go proof:
// pkg/tools/browser/webrtc/session_test.go's
// TestSessionCloseViewerIfCurrent_SupersededHandleIsNoop_NewerConnectionSurvives.
// The two tests below isolate the CaptureSession-level half.
// ---------------------------------------------------------------------------

// TestCaptureSession_CleanupViewerOffer_OlderAttemptCannotClobberNewerAttempt
// proves the fix directly: two AddViewer calls for the SAME viewerID (an
// older, ultimately-failed/superseded offer attempt racing a newer, winning
// one -- exactly pkg/gateway/browser_webrtc.go's handleWebRTCOffer scenario)
// must not let the OLDER attempt's cleanup erase the newer attempt's
// registration. Uses the plain fakeRelay (no viewerOfferHandler support), so
// this isolates the CaptureSession-level generation check specifically —
// cs.offerHandler stays nil, exercising CleanupViewerOffer's fallback
// relay-close branch alongside the (always-active) generation-checked
// RemoveViewerIfCurrent call.
func TestCaptureSession_CleanupViewerOffer_OlderAttemptCannotClobberNewerAttempt(t *testing.T) {
	relay := &fakeRelay{}
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(new(int32), nil))

	const viewerID = "viewer-race"

	genA := cs.AddViewer(viewerID) // older (losing) attempt
	genB := cs.AddViewer(viewerID) // newer (winning) attempt supersedes A
	if genA == genB {
		t.Fatal("setup: AddViewer must mint a distinct generation per call, even for the same viewerID")
	}
	if got := cs.ViewerCount(); got != 1 {
		t.Fatalf("ViewerCount after two AddViewer calls for the same viewerID = %d, want 1", got)
	}

	// A's own (losing) cleanup runs AFTER B has already superseded it --
	// simulating a slow/failed offer A whose teardown races in late.
	handleA := &ViewerAttachHandle{viewerID: viewerID, gen: genA}
	cs.CleanupViewerOffer(handleA)

	// B's registration must be completely untouched: without the fix, the
	// old unconditional RemoveViewer(viewerID) this replaces would have
	// deleted B's entry here (see the sibling test below for direct proof of
	// that old behavior).
	if got := cs.ViewerCount(); got != 1 {
		t.Fatalf("ViewerCount after A's superseded cleanup = %d, want still 1 (B must survive)", got)
	}

	// B's OWN cleanup DOES remove it -- proving this isn't an unconditional
	// no-op, just a correctly-scoped one.
	handleB := &ViewerAttachHandle{viewerID: viewerID, gen: genB}
	cs.CleanupViewerOffer(handleB)
	if got := cs.ViewerCount(); got != 0 {
		t.Fatalf("ViewerCount after B's OWN cleanup = %d, want 0", got)
	}

	// A nil handle (a caller that never reached a HandleViewerOffer attempt
	// at all) must be a safe no-op.
	cs.CleanupViewerOffer(nil)
}

// TestCaptureSession_RemoveViewer_UnconditionalVariant_WouldClobberNewerAttempt
// documents WHY CleanupViewerOffer/RemoveViewerIfCurrent exist: the plain,
// pre-existing RemoveViewer(viewerID) is UNCONDITIONAL and would incorrectly
// clobber a newer attempt's registration if it were used for offer cleanup —
// exactly the bug this fix-wave closes for the gateway's two cleanup call
// sites (offer failure, and offer superseded before commit).
// RemoveViewer(viewerID) itself is UNCHANGED and stays correct for its own
// two legitimate unconditional callers (an explicit browser_detach, and a
// relay-confirmed eviction notification — see its doc comment) — this test
// is not about those callers, only about contrasting the old mechanism with
// the new one.
func TestCaptureSession_RemoveViewer_UnconditionalVariant_WouldClobberNewerAttempt(t *testing.T) {
	relay := &fakeRelay{}
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(new(int32), nil))

	const viewerID = "viewer-race"
	cs.AddViewer(viewerID)
	cs.AddViewer(viewerID) // a newer attempt supersedes the first

	cs.RemoveViewer(viewerID) // the OLD, viewerID-only cleanup mechanism

	if got := cs.ViewerCount(); got != 0 {
		t.Fatalf(
			"ViewerCount = %d, want 0 -- RemoveViewer(viewerID) is unconditional by design and clobbers ANY generation",
			got,
		)
	}
}

// ---------------------------------------------------------------------------
// CRITICAL fix (fix-wave 2-reviewer finding, lead-verified): CleanupViewerOffer's
// fallback branch was NOT identity-safe for one of the two ways handle.relay
// can be nil.
//
// webrtc.Session.HandleViewerOfferHandle returns a true nil handle on SEVEN
// early-return paths (empty viewerID, empty SDP, no-ingest-video-track
// timeout, session closed, buildPeerConnection failure, video AddTrack
// failure, audio AddTrack failure) -- all BEFORE it registers a viewerConn in
// its own registry (see viewer.go's doc comment for the exact registration
// point). CleanupViewerOffer's old code:
//
//	if cs.offerHandler != nil && handle.relay != nil {
//	    cs.offerHandler.CloseViewerIfCurrent(handle.relay)   // identity-safe
//	} else {
//	    cs.Relay().CloseViewer(handle.viewerID)              // UNCONDITIONAL
//	}
//
// treated "cs.offerHandler != nil && handle.relay == nil" the SAME as
// "cs.offerHandler == nil" (no supersede-safe capability at all) -- but
// those are different situations. The doc comment's justification ("no
// worse than before, since no identity information was ever available") is
// only true for the latter. For the former -- a REAL *webrtc.Session (which
// DOES support identity-safe closing) whose OWN attempt merely failed before
// registering anything -- falling through to the unconditional,
// viewerID-only CloseViewer can close a completely unrelated, live,
// already-registered sibling connection for the same viewerID: viewerID is
// per-CONNECTION (minted once in browser_ws.go), not per-offer, so two
// concurrent offers for the same viewerID/agent (e.g. offer A stalls in
// waitForTracks for 15s and ultimately times out; offer B, dispatched later
// on its own goroutine, registers, commits, and is actively viewed before A's
// failure is even discovered) are a real, reachable race
// (dispatchWebRTCOffer spawns one unserialized goroutine per offer frame).
//
// fakeOfferRelay below is a RelaySession double that ALSO implements the
// viewerOfferHandler optional capability (HandleViewerOfferHandle /
// CloseViewerIfCurrent) with fake, in-memory identity tracking -- no real
// Pion/ICE machinery needed to reproduce or prove this bug, since the defect
// is entirely in CleanupViewerOffer's own branching logic, not in the real
// relay's identity check (which already has its own dedicated Go<->Go proof:
// pkg/tools/browser/webrtc/session_test.go's
// TestSessionCloseViewerIfCurrent_SupersededHandleIsNoop_NewerConnectionSurvives).
// ---------------------------------------------------------------------------

// fakeOfferHandle is fakeOfferRelay's fake analog of *webrtc.ViewerHandle --
// an opaque per-attempt identity token, returned as `any` from
// HandleViewerOfferHandle exactly as the real type is.
type fakeOfferHandle struct {
	viewerID string
}

// fakeOfferRelay is a test double for RelaySession that additionally
// implements viewerOfferHandler, modeling the real webrtc.Session's
// identity-checked viewer registry (one currently-registered handle per
// viewerID; CloseViewerIfCurrent only removes it if the handle passed in is
// STILL the one currently registered) without any real WebRTC/ICE. Embeds
// fakeRelay for the plain RelaySession methods this test doesn't otherwise
// exercise; CloseViewer is overridden (not inherited) so it ALSO clears the
// fake identity registry -- mirroring the real webrtc.Session.CloseViewer's
// unconditional, viewerID-only semantics -- which is exactly what lets this
// test detect the CRITICAL bug's unconditional-fallback clobber.
type fakeOfferRelay struct {
	fakeRelay

	mu      sync.Mutex
	current map[string]*fakeOfferHandle
}

// registerHandleOK simulates a HandleViewerOfferHandle call that reaches
// registration successfully, returning a fresh handle now current for
// viewerID (replacing whatever was previously registered there, exactly like
// the real Session's same-viewerID replace branch).
func (f *fakeOfferRelay) registerHandleOK(viewerID string) *fakeOfferHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current == nil {
		f.current = make(map[string]*fakeOfferHandle)
	}
	h := &fakeOfferHandle{viewerID: viewerID}
	f.current[viewerID] = h
	return h
}

// HandleViewerOfferHandle implements viewerOfferHandler. An sdpOffer of
// exactly "FAIL_BEFORE_REGISTER" simulates one of the real Session's seven
// early-return failure paths -- a true nil handle, nothing registered.
// Anything else succeeds and registers, mirroring a real successful offer.
func (f *fakeOfferRelay) HandleViewerOfferHandle(viewerID, sdpOffer string) (string, any, error) {
	if sdpOffer == "FAIL_BEFORE_REGISTER" {
		return "", nil, errors.New("fakeOfferRelay: simulated failure before registration")
	}
	h := f.registerHandleOK(viewerID)
	return "answer-" + viewerID, h, nil
}

// CloseViewerIfCurrent implements viewerOfferHandler's identity-safe close:
// a no-op unless handle is STILL the currently-registered one for its
// viewerID.
func (f *fakeOfferRelay) CloseViewerIfCurrent(handle any) {
	h, ok := handle.(*fakeOfferHandle)
	if !ok || h == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.current[h.viewerID] == h {
		delete(f.current, h.viewerID)
	}
}

// CloseViewer overrides fakeRelay's plain bookkeeping-only implementation:
// the real webrtc.Session.CloseViewer unconditionally deletes and closes
// WHATEVER is currently registered for viewerID, regardless of identity --
// this override reproduces that exact unconditional semantics against the
// fake registry so the CRITICAL-bug scenario is faithfully reproducible.
func (f *fakeOfferRelay) CloseViewer(viewerID string) {
	f.fakeRelay.CloseViewer(viewerID)
	f.mu.Lock()
	delete(f.current, viewerID)
	f.mu.Unlock()
}

// isCurrentlyRegistered reports whether h is still the registered handle for
// viewerID in the fake registry.
func (f *fakeOfferRelay) isCurrentlyRegistered(viewerID string, h *fakeOfferHandle) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current[viewerID] == h
}

// TestCaptureSession_CleanupViewerOffer_EarlyFailureMustNotClobberLiveSibling
// reproduces the CRITICAL incident directly: attempt A (older, dispatched
// first) ultimately fails BEFORE ever registering anything at the relay --
// producing a *ViewerAttachHandle with a true nil `relay` field, exactly as
// CaptureSession.HandleViewerOffer packages a webrtc.Session
// early-return failure. Attempt B (a totally separate, later-dispatched
// offer for the SAME viewerID -- viewerID is per-CONNECTION, not per-offer)
// meanwhile succeeds, registers, and is live. A's cleanup (CleanupViewerOffer)
// running AFTER B is already registered must NOT touch B's live relay
// registration -- it has nothing of its OWN to close there.
//
// FAILS WITHOUT THE FIX: the old code's fallback branch fires whenever
// `cs.offerHandler != nil && handle.relay != nil` is false -- true both when
// offerHandler is nil (no capability at all, the historically-safe case) AND
// when offerHandler is non-nil but this attempt's OWN handle.relay happens to
// be nil (the unsafe case this test isolates) -- so it calls
// cs.Relay().CloseViewer(viewerID) unconditionally, which (via this fake's
// faithful CloseViewer override) deletes B's live registration.
func TestCaptureSession_CleanupViewerOffer_EarlyFailureMustNotClobberLiveSibling(t *testing.T) {
	relay := &fakeOfferRelay{}
	cs, err := NewCaptureSessionWithDeps(nil, "agent-1", relay, fakeEncoderStarter(new(int32), nil), nil)
	if err != nil {
		t.Fatalf("NewCaptureSessionWithDeps: %v", err)
	}

	const viewerID = "viewer-shared-conn"

	// Attempt A dispatched first (older) -- AddViewer mints its generation
	// before B's.
	genA := cs.AddViewer(viewerID)
	// Attempt B dispatched second (newer) -- supersedes A's generation at the
	// CaptureSession level, exactly as two browser_webrtc_offer frames on the
	// same connection would.
	genB := cs.AddViewer(viewerID)
	if genA == genB {
		t.Fatal("setup: AddViewer must mint distinct generations")
	}

	// B's offer reaches the relay and registers successfully -- it is now
	// live.
	answerB, handleB, err := cs.HandleViewerOffer(viewerID, "real-offer-b", genB)
	if err != nil {
		t.Fatalf("HandleViewerOffer (B) = %v, want nil", err)
	}
	if answerB == "" {
		t.Fatal("setup: B's HandleViewerOffer returned an empty answer")
	}
	if handleB == nil || handleB.relay == nil {
		t.Fatal("setup: B's handle must have a non-nil relay identity (it registered)")
	}
	fakeHandleB, ok := handleB.relay.(*fakeOfferHandle)
	if !ok {
		t.Fatalf("setup: B's relay handle has unexpected dynamic type %T", handleB.relay)
	}
	if !relay.isCurrentlyRegistered(viewerID, fakeHandleB) {
		t.Fatal("setup: B must be registered at the relay immediately after its successful offer")
	}

	// A's offer FINALLY comes back (e.g. after a 15s waitForTracks timeout in
	// production) with an early failure -- it never reached registration, so
	// its handle carries a true nil relay identity.
	_, handleA, err := cs.HandleViewerOffer(viewerID, "FAIL_BEFORE_REGISTER", genA)
	if err == nil {
		t.Fatal("setup: expected A's simulated early failure")
	}
	if handleA == nil {
		t.Fatal("setup: HandleViewerOffer must still return a non-nil *ViewerAttachHandle even on early failure")
	}
	if handleA.relay != nil {
		t.Fatalf("setup: expected handleA.relay nil (A never registered), got %#v", handleA.relay)
	}

	// The bug: A's cleanup must not touch B's unrelated, live registration.
	cs.CleanupViewerOffer(handleA)

	if !relay.isCurrentlyRegistered(viewerID, fakeHandleB) {
		t.Fatal("CRITICAL: A's cleanup (which never registered anything of its own at the relay) " +
			"closed B's live, unrelated relay connection via the unconditional CloseViewer fallback")
	}

	// Confirm this isn't a blanket no-op: B's OWN CloseViewerIfCurrent call
	// (via its own cleanup) DOES still work, proving the registry is live and
	// the assertion above is meaningful, not vacuous.
	cs.CleanupViewerOffer(handleB)
	if relay.isCurrentlyRegistered(viewerID, fakeHandleB) {
		t.Fatal("setup sanity: B's own cleanup should remove its own registration")
	}
}

// --- ingest death recovery ----------------------------------------------------
//
// Regression coverage for the operator-reported "nothing loads" failure
// (0803 (2)(1).mov, UAT v41): the tab title and URL bar advanced through
// several real sites while the rendered frame stayed frozen on the start page
// for the whole session.
//
// Cause: webrtc/ingest.go's OnConnectionStateChange only LOGGED the state and
// never cleared s.ingestPC, so after the encoder's peer connection died the
// session kept a CLOSED connection installed. Every later recapture's keyframe
// request failed with "io: read/write on closed pipe" (observed repeating every
// ~3s in the gateway log), no keyframe ever arrived, and the stream froze on
// its last received frame. Only a successful NEW offer resets ingestPC — which
// never came, because nothing noticed the death.

func TestCaptureSession_IngestLoss_TriggersRecapture(t *testing.T) {
	relay := &fakeRelay{}
	_ = newTestCaptureSession(t, relay, nil)

	before := relay.recaptureCount()
	relay.triggerIngestLost()

	if got := relay.recaptureCount(); got != before+1 {
		t.Fatalf("a lost ingest connection must trigger a fresh capture: recaptures = %d, want %d; "+
			"without this the session keeps a dead connection and the stream stays frozen forever", got, before+1)
	}
}

// TestCaptureSession_IngestLoss_AfterStopIsInert — a death notification racing
// teardown must not resurrect work on a stopped session.
func TestCaptureSession_IngestLoss_AfterStopIsInert(t *testing.T) {
	relay := &fakeRelay{}
	cs := newTestCaptureSession(t, relay, nil)
	cs.Stop()

	before := relay.recaptureCount()
	relay.triggerIngestLost()

	if got := relay.recaptureCount(); got != before {
		t.Fatalf("a stopped session must not request a recapture: recaptures = %d, want %d", got, before)
	}
}

// TestCaptureSession_WiresIngestLossHook pins the WIRING itself. The two tests
// above drive the callback through the fake, so they would still pass if the
// SetOnIngestLost call in newCaptureSessionWithDeps were deleted — which is
// exactly the class of "feature present but never connected" bug that shipped
// the start page inert earlier in this same effort.
func TestCaptureSession_WiresIngestLossHook(t *testing.T) {
	relay := &fakeRelay{}
	_ = newTestCaptureSession(t, relay, nil)

	relay.mu.Lock()
	registered := relay.onIngestLost != nil
	relay.mu.Unlock()

	if !registered {
		t.Fatal("CaptureSession must register an ingest-loss callback on a relay that supports it — " +
			"without the wiring a dead ingest is never noticed and the stream freezes")
	}
}
