// live_tabchange_coordination_test.go — round-2 findings F1/F3/F5, the three
// coordination defects the round-1 fixes left behind. Every one of them is
// invisible on a fast machine and routine on the 2-CPU hosted box, which is
// exactly why none of them had a test:
//
//   - F1: a burst of tab changes DROPPED every re-apply after the first, and
//     the first one's geometry was then cached as if it described the tab the
//     user had ended up on;
//   - F3: the foreground re-assert was wired to the rare recovery path instead
//     of the one every tab click takes, and the ordinary switch rebuilt the
//     encoder twice;
//   - F5: a timed-out sharpness override told the user nothing at all.
//
// These assert on what the USER experiences — which tab gets resized, which
// geometry the clicks are mapped through, how many encoder rebuilds one click
// costs, and whether the panel says anything — never on internal call counts
// for their own sake.

package browser

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- shared scaffolding -----------------------------------------------------

// ingestLedger records the control frames an encoder would receive, with the
// expected geometry each carried. Counting at the ingest boundary (rather than
// at any one Recapture* method) means a recapture arriving by either entry
// point is still seen.
type ingestLedger struct {
	mu      sync.Mutex
	actions []string
	dims    [][2]int
}

func (l *ingestLedger) recaptures() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, a := range l.actions {
		if a == "recapture" {
			n++
		}
	}
	return n
}

func (l *ingestLedger) lastDims() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.dims) == 0 {
		return 0, 0
	}
	d := l.dims[len(l.dims)-1]
	return d[0], d[1]
}

// orderLog records foreground selection, measurement, and the qualified
// encoder command at their actual boundaries.
type orderLog struct {
	mu   sync.Mutex
	seen []string
}

func (o *orderLog) add(s string) {
	o.mu.Lock()
	o.seen = append(o.seen, s)
	o.mu.Unlock()
}

func (o *orderLog) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.seen...)
}

// --- F1: a burst of tab changes ---------------------------------------------

// A -> B -> C faster than one re-apply completes. On the hosted box the settle
// poll alone is ~350ms and CDP is already starved, so overlapping re-applies
// are the NORMAL case there; on macOS the whole re-apply is 100-200ms and this
// almost never happens. The in-flight guard used to DROP the later changes
// outright: C was left with neither the panel's viewport nor its own
// per-target deviceScaleFactor override (Chrome's override is per TARGET), and
// nothing logged a word about it — the blur-on-every-tab-open defect the
// re-apply exists to fix, reinstated silently on Linux only.
func TestOnTabsChanged_BurstOfTabChangesStillReachesTheLastTab(t *testing.T) {
	tabA, cancelA := context.WithCancel(context.Background())
	t.Cleanup(cancelA)
	tabB, cancelB := context.WithCancel(context.WithValue(context.Background(), viewportTargetTestKey{}, "B"))
	t.Cleanup(cancelB)
	tabC, cancelC := context.WithCancel(context.WithValue(context.Background(), viewportTargetTestKey{}, "C"))
	t.Cleanup(cancelC)

	entry := &tabEntry{ctx: tabB, cancel: cancelB, targetID: "burst-B"}
	mgr := &BrowserManager{
		started:  true,
		sessions: map[string]*sessionEntry{"s1": {tabs: []*tabEntry{entry}, activeIdx: 0}},
	}
	relay := &fakeRelay{}
	cs, err := NewCaptureSessionWithDeps(mgr, "agent-burst", relay, fakeEncoderStarter(new(int32), nil), nil)
	require.NoError(t, err)
	cs.mu.Lock()
	cs.foregroundAssertFn = func(context.Context) bool { return true }
	cs.mu.Unlock()
	mgr.captures = map[string]*CaptureSession{"s1": cs}

	var (
		mu       sync.Mutex
		resized  []context.Context
		holdB    = make(chan struct{})
		sawB     = make(chan struct{})
		sawBOnce sync.Once
	)
	var releaseOnce sync.Once
	releaseB := func() { releaseOnce.Do(func() { close(holdB) }) }
	t.Cleanup(releaseB)
	lv := &LiveView{
		mgr:                mgr,
		sessionID:          "s1",
		viewers:            make(map[string]struct{}),
		statusSinks:        make(map[string]StatusSink),
		controlSinks:       make(map[string]ControlSink),
		tabsSinks:          make(map[string]TabsSink),
		lastKnownActiveCtx: tabA,
		lastRequestedW:     633,
		lastRequestedH:     686,
		lastRequestedScale: 2,
	}
	lv.runCDP = func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case windowBoundsAction:
			mu.Lock()
			resized = append(resized, ctx)
			mu.Unlock()
			if ctx.Value(viewportTargetTestKey{}) == "B" {
				sawBOnce.Do(func() { close(sawB) })
				<-holdB // hold B's re-apply open so C's change lands mid-flight
			}
		case viewportFrameGeometryAction:
			if ctx.Value(viewportTargetTestKey{}) == "C" {
				*a.width, *a.height, *a.scale = 640, 480, 2
			} else {
				*a.width, *a.height, *a.scale = 800, 600, 2
			}
		case layoutMetricsAction:
			if ctx.Value(viewportTargetTestKey{}) == "C" {
				*a.w, *a.h = 640, 480 // C's real geometry
			} else {
				*a.w, *a.h = 800, 600 // B's — must never end up cached while C is active
			}
		}
		return nil
	}

	lv.onTabsChanged(nil, 0) // switch to B
	select {
	case <-sawB:
	case <-time.After(5 * time.Second):
		t.Fatal("the re-apply never reached tab B")
	}

	// C becomes active while B's re-apply is still in flight.
	mgr.mu.Lock()
	entry.ctx = tabC
	entry.targetID = "burst-C"
	mgr.mu.Unlock()
	lv.onTabsChanged(nil, 0)

	releaseB()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range resized {
			if c.Value(viewportTargetTestKey{}) == "C" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"the tab the user actually ended up on must be given the panel's viewport and its own "+
			"deviceScaleFactor override — dropping the second change leaves it soft and mis-sized, silently")

	require.Eventually(t, func() bool {
		lv.mu.Lock()
		defer lv.mu.Unlock()
		return !lv.viewportReapplyInFlight
	}, 5*time.Second, 10*time.Millisecond, "the re-apply worker never finished")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	assert.Equal(t, 640, lv.cssViewportW,
		"input must be mapped through the geometry of the tab that is actually active")
	assert.Equal(t, 480, lv.cssViewportH)
}

// --- F3: the re-assert belongs on the path users take every time ------------

// newAttachedLiveManager drives real tab callbacks and the qualified ingest
// boundary; only browser protocol execution and the relay are substituted.
func newAttachedLiveManager(t *testing.T) (*BrowserManager, *LiveView, *CaptureSession, *ingestLedger, *orderLog) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	t.Cleanup(m.Shutdown)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = m.OpenTab(testSessionID)
		require.NoError(t, err)
	}
	lv := m.live.view(testSessionID)
	order := &orderLog{}
	m.tabFocusFn = func(_ context.Context, actions ...chromedp.Action) error {
		for _, action := range actions {
			if _, ok := action.(*page.BringToFrontParams); ok {
				order.add("focus")
			}
		}
		return nil
	}
	executor := liveInputExecutor(func(_ context.Context, method string, _, result any) error {
		switch method {
		case "Page.getFrameTree":
			fixtureValue[*page.GetFrameTreeReturns](result).FrameTree = &page.FrameTree{Frame: &cdp.Frame{ID: "main", LoaderID: "loaded"}}
		case "Page.createIsolatedWorld":
			fixtureValue[*page.CreateIsolatedWorldReturns](result).ExecutionContextID = 71
		case "Runtime.evaluate":
		default:
			return fmt.Errorf("unexpected tab fixture protocol command %s", method)
		}
		return nil
	})
	lv.runCDP = func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			switch a := action.(type) {
			case viewportFrameGeometryAction:
				*a.width, *a.height, *a.scale = 800, 600, 1
				order.add("measure")
			case layoutMetricsAction:
				*a.w, *a.h = 800, 600
			case documentPaintAction, chromedp.ActionFunc:
				if runErr := action.Do(cdp.WithExecutor(ctx, executor)); runErr != nil {
					return runErr
				}
			}
		}
		return nil
	}
	_, err = m.live.AttachContext(context.Background(), testSessionID, "viewer-1", nil, nil, nil)
	require.NoError(t, err)
	// Discovery belongs to the already-loaded fake page, before capture starts.
	// Do not leave an asynchronous initial query holding an empty response.
	require.Eventually(t, func() bool {
		lv.mu.Lock()
		watch := lv.documentWatch
		lv.mu.Unlock()
		if watch == nil {
			return false
		}
		watch.mu.Lock()
		defer watch.mu.Unlock()
		return watch.frameID == "main" && watch.processed.Load() >= 1
	}, time.Second, time.Millisecond)
	cs, err := NewCaptureSessionWithDeps(m, "agent-e2e", &adapterRelay{nextToken: 40}, fakeEncoderStarter(new(int32), nil), nil)
	require.NoError(t, err)
	cs.panelSessionID = testSessionID
	_, err = m.EnsureCaptureSessionForPanel(testSessionID, func() (*CaptureSession, error) { return cs, nil })
	require.NoError(t, err)
	_, targetID, err := m.activeTargetSnapshot(testSessionID)
	require.NoError(t, err)
	_, err = cs.BeginFrameTransition(string(targetID), 800, 600, 1)
	require.NoError(t, err)
	// Same-index recovery still requests best-effort focus before its retained frame.
	cs.foregroundAssertFn = func(context.Context) bool { return true }
	ledger := &ingestLedger{}
	_, _, err = cs.BindIngestRecaptureContext(context.Background(), func(string, *string, int, int, int) error { return nil }, func(ctx context.Context, frame CaptureFrameState, current func() bool) error {
		if ctx.Err() != nil || !current() {
			return context.Canceled
		}
		order.add("control:recapture")
		ledger.mu.Lock()
		ledger.actions = append(ledger.actions, "recapture")
		ledger.dims = append(ledger.dims, [2]int{frame.Width, frame.Height})
		ledger.mu.Unlock()
		return nil
	}, func() {})
	require.NoError(t, err)
	return m, lv, cs, ledger, order
}
