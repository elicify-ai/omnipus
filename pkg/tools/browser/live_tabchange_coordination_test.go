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
	"strings"
	"sync"
	"testing"
	"time"

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

func (l *ingestLedger) reset() {
	l.mu.Lock()
	l.actions, l.dims = nil, nil
	l.mu.Unlock()
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

// The other half of F1, isolated: even with the coalescing above, an apply
// that started against tab B finishes AFTER the user has moved to C, and its
// final act was an unconditional write of B's geometry into the cache. A
// stale-but-positive cache is worse than an empty one — it passes
// rescaleToCSSViewport's cache-hit guard, so every click from then on is
// silently mapped through the dimensions of a tab the user has left.
func TestApplyViewport_DiscardsAMeasurementTheActiveTabHasMovedPast(t *testing.T) {
	tabB, cancelB := context.WithCancel(context.Background())
	t.Cleanup(cancelB)
	tabC, cancelC := context.WithCancel(context.Background())
	t.Cleanup(cancelC)

	lv := &LiveView{
		sessionID:          "s1",
		viewers:            make(map[string]struct{}),
		lastKnownActiveCtx: tabB,
	}
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if a, ok := actions[0].(layoutMetricsAction); ok {
			// The user switches to C while this very read-back is happening.
			lv.mu.Lock()
			lv.lastKnownActiveCtx = tabC
			lv.mu.Unlock()
			*a.w, *a.h = 800, 600
		}
		return nil
	}

	applied, err := lv.applyViewport(tabB, 800, 600, 1)
	require.NoError(t, err)
	require.True(t, applied, "the resize itself did happen — only the cache write is in question")

	lv.mu.Lock()
	defer lv.mu.Unlock()
	assert.Zero(t, lv.cssViewportW,
		"B's geometry must not be cached while C is the active tab — the next input event re-fetches "+
			"from the live tab instead of trusting a number that describes a tab nobody is looking at")
	assert.Zero(t, lv.cssViewportH)
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
	_, err = m.live.AttachContext(context.Background(), testSessionID, "viewer-1", nil, nil, nil)
	require.NoError(t, err)
	lv, ok := m.live.lookup(testSessionID)
	require.True(t, ok)
	order := &orderLog{}
	m.tabFocusFn = func(_ context.Context, actions ...chromedp.Action) error {
		for _, action := range actions {
			if _, ok := action.(*page.BringToFrontParams); ok {
				order.add("focus")
			}
		}
		return nil
	}
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			switch a := action.(type) {
			case viewportFrameGeometryAction:
				*a.width, *a.height, *a.scale = 800, 600, 1
				order.add("measure")
			case layoutMetricsAction:
				*a.w, *a.h = 800, 600
			}
		}
		return nil
	}
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

// A normal tab switch focuses the selected target, measures it, then sends
// one qualified recapture. The encoder binds the explicit target ID.
func TestSwitchTab_OrdinarySwitchReassertsForegroundBeforeTheControlFrame(t *testing.T) {
	m, _, _, ledger, order := newAttachedLiveManager(t)

	_, err := m.SwitchTab(testSessionID, 0) // a real move
	require.NoError(t, err)

	require.Eventually(t, func() bool { return ledger.recaptures() == 1 }, 3*time.Second, 5*time.Millisecond,
		"an ordinary tab switch owes the encoder exactly one recapture")
	require.Eventually(t, func() bool { return len(order.snapshot()) == 3 }, 3*time.Second, 5*time.Millisecond,
		"the selected target must be focused and measured before recapture")

	assert.Equal(t, []string{"focus", "measure", "control:recapture"}, order.snapshot(),
		"the encoder must receive measured geometry after the selected target is focused")
}

// One user action, one encoder rebuild. With a viewport already applied, the
// ordinary switch used to recapture TWICE: onTabsChanged's immediate,
// geometry-less Recapture(), and then the post-re-apply RecaptureAt a few
// hundred ms later. Two full encoder rebuilds and two PLI bursts per tab
// click, worst exactly where it hurts most. The first of the two could never
// have been the right one anyway — it re-binds the stream before the new
// target has been given the panel's size and per-target sharpness.
func TestSwitchTab_WithAViewportAppliedRecapturesOnceWithTheVerifiedSize(t *testing.T) {
	m, lv, _, ledger, _ := newAttachedLiveManager(t)

	lv.mu.Lock()
	lv.lastRequestedW, lv.lastRequestedH, lv.lastRequestedScale = 633, 686, 2
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case layoutMetricsAction:
			*a.w, *a.h = 633, 686
		case viewportFrameGeometryAction:
			*a.width, *a.height, *a.scale = 633, 686, 2
		}
		return nil
	}
	lv.mu.Unlock()

	_, err := m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return ledger.recaptures() >= 1 }, 3*time.Second, 5*time.Millisecond,
		"the picture must follow the tab")
	time.Sleep(300 * time.Millisecond) // long enough for a second rebuild to show up

	assert.Equal(t, 1, ledger.recaptures(),
		"one tab click must cost exactly one encoder rebuild, not one before the re-apply and one after")
	w, h := ledger.lastDims()
	assert.Equal(t, 633, w, "and it must carry the CDP-verified size the new tab actually reached")
	assert.Equal(t, 686, h)
}

// Coalescing is only honest if the surviving pass uses the LATEST caller's
// geometry. A pass that replayed the geometry captured when the worker
// started would hand the encoder the size of a tab the user has already left
// — the same class of stale-measurement bug as F1's cache write, one layer
// down.
func TestRecaptureForTabChangeAt_CoalescedPassUsesTheLatestGeometry(t *testing.T) {
	relay := &fakeRelay{}
	cs := newTestCaptureSession(t, relay, fakeEncoderStarter(new(int32), nil))

	release := make(chan struct{})
	var once sync.Once
	cs.mu.Lock()
	cs.foregroundAssertFn = func(context.Context) bool {
		once.Do(func() { <-release }) // hold the worker inside pass 1
		return true
	}
	cs.mu.Unlock()
	ledger := &ingestLedger{}
	cs.BindIngest(func(action string, _ *string, w, h int, _ int) error {
		ledger.mu.Lock()
		ledger.actions = append(ledger.actions, action)
		ledger.dims = append(ledger.dims, [2]int{w, h})
		ledger.mu.Unlock()
		return nil
	}, func() {})

	cs.RecaptureForTabChangeAt(633, 686) // starts the worker; it blocks
	cs.RecaptureForTabChangeAt(640, 480) // coalesces — this is where the user ended up
	close(release)

	require.Eventually(t, func() bool { return ledger.recaptures() == 2 }, 3*time.Second, 5*time.Millisecond,
		"the in-flight pass plus one coalesced re-run")
	w, h := ledger.lastDims()
	assert.Equal(t, 640, w, "the surviving pass must carry the geometry of the tab the user actually ended on")
	assert.Equal(t, 480, h)
}

// --- F5: a degradation the user is told about -------------------------------

// The deviceScaleFactor override is renderer-bound: it only ever times out on
// a loaded box, which in practice means only on hosted Linux. The user was
// left with a persistently soft picture, no message, no control and no stated
// recovery, while the same build on macOS never showed the branch at all —
// a parity break and an ADR-061 break at once (this project's gateway log is
// WARN-only in production, so the existing WarnCF reaches nobody watching).
func TestApplyViewport_TellsTheViewerWhenTheSharpnessSettingTimedOut(t *testing.T) {
	msgs := make(chan string, 8)
	scaleFails := true
	lv := &LiveView{
		sessionID:   "s1",
		viewers:     map[string]struct{}{"v1": {}},
		statusSinks: map[string]StatusSink{"v1": func(m string) { msgs <- m }},
	}
	lv.runCDP = func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case layoutMetricsAction:
			*a.w, *a.h = 633, 686
			return nil
		case windowBoundsAction:
			return nil
		}
		if isScaleAction(actions[0]) && scaleFails {
			return context.DeadlineExceeded
		}
		return nil
	}

	applied, err := lv.applyViewport(context.Background(), 633, 686, 2)
	require.NoError(t, err, "the window did resize — the sharpness setting is cosmetic and must never fail it")
	require.True(t, applied)

	var got string
	select {
	case got = <-msgs:
	case <-time.After(3 * time.Second):
		t.Fatal("the viewer was told nothing at all — a soft picture with no message, no control " +
			"and no stated recovery is exactly the silent degradation ADR-061 forbids")
	}
	assert.Contains(t, strings.ToLower(got), "soft",
		"the message must name what the user is seeing")
	assert.Contains(t, strings.ToLower(got), "resize",
		"and how it recovers")

	// Throttled: the SPA re-sends a viewport frame throughout a panel drag, so
	// a renderer wedged for a few seconds must produce one banner, not one per
	// drag frame.
	for i := 0; i < 5; i++ {
		_, err = lv.applyViewport(context.Background(), 633, 686, 2)
		require.NoError(t, err)
	}
	select {
	case extra := <-msgs:
		t.Fatalf("a continuing degradation must not re-notify on every apply: %q", extra)
	case <-time.After(200 * time.Millisecond):
	}

	// ...but a recovery re-arms it: the next degradation is a NEW event and
	// must be reported rather than swallowed by the previous one's window.
	scaleFails = false
	_, err = lv.applyViewport(context.Background(), 633, 686, 2)
	require.NoError(t, err)
	scaleFails = true
	_, err = lv.applyViewport(context.Background(), 633, 686, 2)
	require.NoError(t, err)

	select {
	case <-msgs:
	case <-time.After(3 * time.Second):
		t.Fatal("after the picture re-sharpened, a fresh degradation must be reported again")
	}
}
