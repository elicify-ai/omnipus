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
// existence proof, not a timing bet; the deadline bounds only the FAILURE
// mode. firstServe orders arming strictly after the attach epoch's
// successful serve has executed. "Success is silent" and "reported once"
// are structural: a successful discovery has no emission path at all, and
// the error arm runs exactly once per epoch. Harness note: this follows the
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
// serve has executed.
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
	// returns, so arming below can never retroactively fail it.
	<-stub.firstServe

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

	// Exactly once, non-blocking drain: any additional emission delivered by
	// now fails the test; a later one is structurally impossible (the error
	// arm executes once per epoch and no other emitter exists here).
	select {
	case extra := <-statuses:
		t.Fatalf("the discovery failure must be reported exactly once; extra=%q", extra)
	default:
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
