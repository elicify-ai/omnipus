package browser

// live_document_discovery_emission_test.go — the discovery-arm half of the
// document-watch failure-emission contract (CHECK finding F2, review finding
// A1, 2026-10-02): live_document_frame.go::initialize's Page.getFrameTree
// error arm had no deterministic owner. The paint-recovery arm is owned by
// TestLiveDocumentStaleRecoveryIsBoundedAndFailureReportedOnce and the death
// arm by TestLiveDeathWatcherRetainsOriginalSource; the M3b mutant (a genuine
// erroring getFrameTree stub with the suite still green) proved the gap. This
// file owns the CURRENT-watch half: a discovery error on the live view's
// active epoch must reach the attached viewer's status sink exactly once,
// through the real attach → close-active-tab → rebindWatch production
// lifecycle. The obsolete/stale-epoch halves stay deliberately silent — that
// suppression is pinned by TestLiveDocumentInitializationRetainsItsOriginalWork
// ("failure after replacement") and must NOT be asserted to surface here.
//
// Determinism (why there is no sleep, no Eventually, no race winner): the
// only status emitter in this lifecycle is reportFailure's single error-arm
// execution for the fresh epoch's single discovery serve — proven by the
// served==2 attribution assertion — so the <statuses> receive is an
// existence proof, not a timing bet; that deadline bounds only the FAILURE
// mode. The pre-arming firstServe receive is deadline-bounded too (2 s,
// named failure — CHECK F2): a regression that stops the attach epoch from
// ever serving fails in ~2 s instead of hanging the package for go test's
// 10-minute timeout. firstServe orders arming strictly after the attach
// epoch's successful serve has executed. "Success is silent" is structural:
// a successful discovery has no emission path at all. "Reported once" is
// received + a bounded 50 ms settle (CHECK F1, the house idiom of
// live_fresh_attachment_review_test.go): the settle is bounded observation,
// NOT proof of producer completion — no seam exists after the sink send on
// the watch goroutine (initialize returns into processEvents and parks) —
// so a duplicate delayed more than 50 ms after the first could still evade
// it; that residual is stated here, not hidden. The duplicate shape this
// file must catch (M-2x: back-to-back synchronous reportFailure calls)
// sends within microseconds of the first. Harness note: this follows the
// real-time channel idiom of live_fresh_attachment_review_test.go (the
// goroutine-ful lifecycle tests cannot use testing/synctest — registry
// sweeper, death-watch and processEvents goroutines never exit, so a bubble
// cannot drain).

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// liveDocumentRefreshFailureMessage is the production contract text every
// document-refresh failure emits to status sinks (live_document_frame.go::
// reportFailure → emitFailure). Pinned exactly: it is the user-visible string
// the frozen-panel viewer must see when discovery fails. The load-bearing
// oracle below is that the message ARRIVES at all — a regression that swallows
// the emission fails on the Fatalf arm, not on wording.
const liveDocumentRefreshFailureMessage = "The browser could not confirm the new page picture. Reload the page or retry the browser connection."

// discoveryFrameTreeStub is the closed transport stub for the discovery
// lifecycle: it serves Page.getFrameTree successfully until failDiscovery
// arms a genuine error for every subsequent serve, and rejects every other
// protocol command loudly so the fake-tab path can never reach the real
// transport (whose first chromedp.Run on a bare fixture context launches a
// real browser — issue #1081). Serves are recorded BEFORE any rejection, so
// even an alien command is named by the closed-surface assertion. firstServe
// closes on the stub's first serve — under this file's ordering that is the
// attach epoch's discovery — giving the test a channel-synchronized point
// (no sleep, no race winner) to arm the error strictly after the successful
// serve has executed; the receive on it is deadline-bounded (2 s, named
// failure) because it closes on the SUCCESS path only — an attach-epoch
// regression that never serves, or one whose discovery fails before arming,
// must fail with a name, not hang the package (CHECK F2).
type discoveryFrameTreeStub struct {
	mu         sync.Mutex
	failing    bool
	served     []string
	firstServe chan struct{}
	once       sync.Once
}

func newDiscoveryFrameTreeStub() *discoveryFrameTreeStub {
	return &discoveryFrameTreeStub{firstServe: make(chan struct{})}
}

// bind installs the stub on the view's runCDP seam. Must run BEFORE
// reg.Attach/rebindWatch so no watch epoch ever sees the real
// runCDPWithTimeout, and on the view the attach path actually resolves —
// pre-create it via the registry (create-or-reuse, live.go::view) exactly
// like the second-registry fixture-isolation fix in live_deadlock_test.go.
func (s *discoveryFrameTreeStub) bind(lv *LiveView) {
	lv.runCDP = func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			if err := action.Do(cdp.WithExecutor(ctx, liveInputExecutor(s.serve))); err != nil {
				return err
			}
		}
		return nil
	}
}

func (s *discoveryFrameTreeStub) serve(_ context.Context, method string, _, result any) error {
	s.mu.Lock()
	failing := s.failing
	s.served = append(s.served, method)
	s.mu.Unlock()
	if method != "Page.getFrameTree" {
		return fmt.Errorf("discoveryFrameTreeStub: unexpected protocol command %s on the fake-tab path", method)
	}
	if failing {
		return errors.New("discoveryFrameTreeStub: Page.getFrameTree discovery failed")
	}
	fixtureValue[*page.GetFrameTreeReturns](result).FrameTree = &page.FrameTree{
		Frame: &cdp.Frame{ID: "fixture-main", LoaderID: "fixture-loader"},
	}
	s.once.Do(func() { close(s.firstServe) })
	return nil
}

// failDiscovery arms the genuine error for every subsequent serve; the next
// discovery is by construction the fresh watch epoch's.
func (s *discoveryFrameTreeStub) failDiscovery() {
	s.mu.Lock()
	s.failing = true
	s.mu.Unlock()
}

func (s *discoveryFrameTreeStub) servedMethods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.served...)
}

// TestLiveDocumentDiscoveryErrorOnCurrentWatchReachesStatusSink pins the
// visible-current-error contract for the document watch's discovery arm: a
// Page.getFrameTree failure on the CURRENT active watch epoch reaches the
// attached viewer's status sink exactly once with the production message,
// while a successful discovery and every obsolete epoch stay silent. The
// error takes initialize's no-capture emission arm (work == nil &&
// CaptureSessionForPanel == nil) — the browserless-panel viewer of finding
// F2 — driven through the REAL CloseTab → onTabsChanged → rebindWatch path,
// never a hand-installed watch or a parallel emitter.
func TestLiveDocumentDiscoveryErrorOnCurrentWatchReachesStatusSink(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	// This fixture opens fake tabs; host memory is outside its routing contract.
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	reg := newLiveViewRegistry(m)

	// reg is this test's second registry (newLiveViewRegistry overwrote the
	// fixture's tabs-changed callback), so the stub must go on the view the
	// attach path actually resolves: pre-create it here, bind the seam, then
	// Attach reuses the same view (live.go::view is create-or-reuse).
	stub := newDiscoveryFrameTreeStub()
	lv := reg.view(testSessionID)
	stub.bind(lv)

	_, err := m.Session(testSessionID) // tab 0
	require.NoError(t, err)
	tab1, err := m.OpenTab(testSessionID) // tab 1, becomes active
	require.NoError(t, err)
	require.True(t, tab1.Active)
	tab1Ctx, err := m.Session(testSessionID)
	require.NoError(t, err)

	statuses := make(chan string, 4)
	controlledByOther, err := reg.Attach(testSessionID, "viewer1", func(message string) { statuses <- message }, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)

	// Sanity: attach bound the view to the active tab — the stubbed instance
	// is the one the attach-time document watch uses.
	lv.mu.Lock()
	require.Equal(t, tab1Ctx, lv.tabCtx, "sanity: the live view must be bound to the active tab before the close")
	lv.mu.Unlock()

	// The attach epoch's discovery runs asynchronously; wait for its exact
	// serve on the stub's channel — this serve has executed when the receive
	// returns, so arming below can never retroactively fail it. The deadline
	// bounds the SETUP phase (CHECK F2): firstServe closes on the success
	// path only, so a regression that stops the attach epoch from ever
	// serving (watch not installed, serve dropped) — or one whose attach-time
	// discovery errors before this test arms — fails here with a name in ~2 s
	// instead of hanging the package for go test's 10-minute timeout.
	select {
	case <-stub.firstServe:
	case <-time.After(2 * time.Second):
		t.Fatalf("attach-time discovery never served; served=%v", stub.servedMethods())
	}

	// A successful frame discovery has NO emission path at all (its status
	// sources are the discovery error that did not happen and a death
	// broadcast that cannot fire before any context is canceled), so this
	// no-event assertion is structural, not a timed window.
	require.Empty(t, statuses, "a successful frame discovery on a current watch must not emit a status")

	// Arm the genuine discovery failure. The attach epoch serves its
	// discovery exactly once and has already served, so the next discovery is
	// by construction the fresh epoch's.
	stub.failDiscovery()

	// Close the ACTIVE tab — the real production path (BrowserManager.CloseTab
	// → notifyTabsChanged → onTabsChanged → rebindWatch(survivor,
	// initializePicture=false)) installs a new CURRENT watch epoch whose
	// discovery now fails. No capture session exists on this manager, so the
	// error takes initialize's no-capture emission arm instead of the
	// documented silent return.
	_, _, err = m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	survivorCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotEqual(t, tab1Ctx, survivorCtx, "sanity: the survivor must be a different tab context than the closed one")

	// The visible-current-error contract: the discovery failure reaches the
	// attached viewer's status sink with the production message. Exactly one
	// message can ever be sent (single error arm, single discovery serve per
	// epoch, no other emitter in this lifecycle), so this receive is an
	// existence proof; the deadline bounds only the failure mode.
	select {
	case got := <-statuses:
		require.Equal(t, liveDocumentRefreshFailureMessage, got,
			"the current watch's discovery failure must surface the production document-refresh message")
	case <-time.After(2 * time.Second):
		t.Fatalf("discovery error on a current watch never reached the status sink; served=%v", stub.servedMethods())
	}

	// Exactly once — bounded settle (CHECK F1, house idiom of
	// live_fresh_attachment_review_test.go). The receive above releases the
	// watch goroutine; in the duplicate shape this file must catch (M-2x:
	// back-to-back synchronous reportFailure calls on that goroutine) the
	// second send lands within microseconds of the first, well inside the
	// 50 ms window, so the drain fails the test. This window is bounded
	// observation, NOT proof of producer completion — no seam exists after
	// the sink send on the watch goroutine to synchronize on (initialize
	// returns into processEvents, which parks forever) — so a hypothetical
	// duplicate delayed more than 50 ms after the first could still evade it;
	// that residual is stated, not hidden. On the passing path the window
	// costs a constant 50 ms and asserts nothing about timing; a later
	// emission is structurally impossible in current production shape (the
	// error arm executes once per epoch and no other emitter exists here).
	select {
	case extra := <-statuses:
		t.Fatalf("the discovery failure must be reported exactly once; extra=%q", extra)
	case <-time.After(50 * time.Millisecond):
	}

	// Attribution + closed surface: the whole lifecycle served exactly two
	// protocol commands through the stub — the attach epoch's successful
	// discovery and the fresh epoch's failing one — so the message above
	// traces to the production discovery path executing the genuine error,
	// not to any other command or a parallel emitter.
	served := stub.servedMethods()
	require.Len(t, served, 2, "the fake-tab lifecycle must serve exactly attach+rebind frame discovery: %v", served)
	for _, method := range served {
		require.Equal(t, "Page.getFrameTree", method,
			"the fake-tab lifecycle must not reach any protocol command beyond frame discovery: %v", served)
	}

	// View ownership: the production rebind moved the view onto the surviving
	// tab, so the erroring epoch is the current one and the closed tab's
	// obsolete epoch stays silent — suppression semantics intact, never
	// asserted to surface.
	lv.mu.Lock()
	require.Equal(t, survivorCtx, lv.tabCtx, "the view must follow the surviving tab — the erroring epoch is the current one")
	lv.mu.Unlock()
}

// TestLiveDocumentDiscoveryErrorOnAttachEpochReachesStatusSink owns the
// attach-epoch half of the same visible-current-error contract (CHECK F2
// failure class (b), turned into an owned positive case): at attach time the
// attach epoch IS the current watch — live.go::Attach installs it
// synchronously (installDocumentWatchLocked, initializePicture=true) — so its
// discovery failure must reach the just-registered status sink exactly once
// with the production message. The stub is armed BEFORE Attach, so the very
// first discovery serve is the failing one. firstServe never closes on an
// error serve (success-only), which is exactly why this test must NOT wait on
// it — waiting would hang on the very regression class F2 names. No
// CloseTab, no rebind: served==[Page.getFrameTree] attributes the emission to
// the attach epoch's discovery executing the genuine error (a failure in an
// earlier arm would serve nothing, and any alien command would be named by
// the closed surface).
func TestLiveDocumentDiscoveryErrorOnAttachEpochReachesStatusSink(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	// Same fixture contract as the rebind-epoch test: fake tabs, no host memory claim.
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	reg := newLiveViewRegistry(m)

	// Second-registry trap (audited pattern, live_deadlock_test.go): pre-create
	// the view, bind the stub on ITS seam, then Attach reuses the same view.
	stub := newDiscoveryFrameTreeStub()
	lv := reg.view(testSessionID)
	stub.bind(lv)
	stub.failDiscovery() // armed BEFORE Attach: the first discovery is the failing one.

	_, err := m.Session(testSessionID) // tab 0, active
	require.NoError(t, err)

	statuses := make(chan string, 4)
	controlledByOther, err := reg.Attach(testSessionID, "viewer1", func(message string) { statuses <- message }, nil, nil)
	require.NoError(t, err)
	require.False(t, controlledByOther)

	// The visible-current-error contract for the attach epoch: same existence
	// proof as the rebind-epoch test — exactly one message can ever be sent
	// (single error arm, single discovery serve per epoch, no other emitter
	// in this lifecycle) — with the deadline bounding only the failure mode.
	select {
	case got := <-statuses:
		require.Equal(t, liveDocumentRefreshFailureMessage, got,
			"the attach epoch's discovery failure must surface the production document-refresh message")
	case <-time.After(2 * time.Second):
		t.Fatalf("discovery error on the attach epoch never reached the status sink; served=%v", stub.servedMethods())
	}

	// Exactly once — same bounded settle and same stated >50 ms residual as
	// the rebind-epoch test above (bounded observation, not producer-
	// completion proof; no post-emission seam exists on the watch goroutine).
	select {
	case extra := <-statuses:
		t.Fatalf("the attach-epoch discovery failure must be reported exactly once; extra=%q", extra)
	case <-time.After(50 * time.Millisecond):
	}

	// Attribution + closed surface: the whole lifecycle served exactly one
	// protocol command — the attach epoch's failing discovery — so the
	// message traces to the real attach path (Attach →
	// installDocumentWatchLocked → initialize), never a parallel emitter or
	// an earlier arm.
	served := stub.servedMethods()
	require.Len(t, served, 1, "the attach epoch must serve exactly one frame discovery: %v", served)
	require.Equal(t, "Page.getFrameTree", served[0],
		"the attach epoch must not reach any protocol command beyond frame discovery: %v", served)

	// Watch ownership sanity: the attach must have installed the document
	// watch synchronously — the failing epoch is the live view's own current
	// watch, not a detached one.
	lv.mu.Lock()
	require.NotNil(t, lv.documentWatch, "sanity: attach must install the document watch synchronously")
	lv.mu.Unlock()
}
