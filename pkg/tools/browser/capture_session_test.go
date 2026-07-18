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

	"github.com/elicify-ai/omnipus/pkg/tools/browser/captureext"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

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
	return func(_ context.Context, _ *BrowserManager, _, _ string) (context.Context, context.CancelFunc, error) {
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
//   - step 1 (mgr.Session(DefaultSessionID)): a hand-planted sessionEntry
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
	coord := NewBrowserCoordinator(t.TempDir(), BrowserConfig{}, 30)
	// Never Register()'d/launched — coord.rootCtx stays nil, which is exactly
	// the precondition defaultEncoderStarter's step 3 gate checks for.

	fakeCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := &BrowserManager{
		cfg:     BrowserConfig{ProfileDir: t.TempDir() + "/profiles/default"},
		started: true,
		sessions: map[string]*sessionEntry{
			DefaultSessionID: {
				tabs:      []*tabEntry{{ctx: fakeCtx, cancel: cancel}},
				activeIdx: 0,
			},
		},
	}
	mgr.AttachSharedChrome(coord, "agent-no-root-ctx")

	coord.mu.Lock()
	coord.loadedExtensionID = captureext.ExtensionID
	coord.mu.Unlock()

	_, _, err := defaultEncoderStarter(context.Background(), mgr, "deadbeef", "ws://127.0.0.1:1/api/v1/browser/capture-ingest")
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

	starter := EncoderStarter(func(context.Context, *BrowserManager, string, string) (context.Context, context.CancelFunc, error) {
		close(startCalled)
		<-releaseStart // block until the test releases it, simulating a slow chromedp.Run
		tabCtx, cancel := context.WithCancel(context.Background())
		wrappedCancel := func() {
			cancelOnce.Do(func() { close(cancelCalled) })
			cancel()
		}
		return tabCtx, wrappedCancel, nil
	})

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
		t.Fatal("tabCancel fired from Stop() itself before startEncoder even returned — precondition of this test is violated")
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
		func(string, *string) error { return nil },
		func() { firstClosed = true },
	)
	if epoch1 == 0 {
		t.Fatal("BindIngest returned epoch 0 on first bind")
	}

	prevClose, epoch2 := cs.BindIngest(
		func(string, *string) error { return nil },
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
	var mu sync.Mutex
	cs.BindIngest(func(action string, _ *string) error {
		mu.Lock()
		gotAction = action
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
	cs.BindIngest(func(action string, reason *string) error {
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
		t.Fatal("EnsureCaptureSession constructed a second session — must reuse the existing one (one active stream per agent)")
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

func TestCaptureSession_HandleOffersDelegateToRelay(t *testing.T) {
	relay := &fakeRelay{}
	var calls int32
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(&calls, nil))

	answer, err := cs.HandleIngestOffer("ingest-offer-sdp")
	if err != nil || answer != "ingest-answer-sdp" {
		t.Fatalf("HandleIngestOffer = (%q, %v), want (\"ingest-answer-sdp\", nil)", answer, err)
	}

	answer, err = cs.HandleViewerOffer("viewer-x", "viewer-offer-sdp")
	if err != nil || answer != "viewer-answer-viewer-x" {
		t.Fatalf("HandleViewerOffer = (%q, %v), want (\"viewer-answer-viewer-x\", nil)", answer, err)
	}
}
