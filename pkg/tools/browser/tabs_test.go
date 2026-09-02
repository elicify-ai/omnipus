package browser

// tabs_test.go — ADR-041 D1/D2/D3 unit coverage for the tab-set model:
// add/switch/close/neighbor-activation, the FR-060 memory gate on both
// Session() and adoption, Session(default) following the active tab,
// ReconcileTabs adopting a newly-detected target, and the
// never-zero-tabs invariant. Every test here uses BrowserManager.createTabFn
// (a test seam mirroring evalCDP/listTargets' exact rationale — see its doc
// comment in manager.go) to fabricate tabs without a real Chromium/CDP
// connection, so these run in any environment, including this devpod which
// has no Chromium binary.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTabFactory returns a BrowserManager.createTabFn stand-in that
// fabricates a tabEntry with a real (but never-dialed) chromedp context —
// chromedp.NewContext allocates no browser and dials nothing; only Run does,
// and this fake path never calls Run — instead of an ordinary
// context.WithCancel context. installTargetListenerLocked's
// chromedp.ListenTarget call panics with ErrInvalidContext on anything that
// isn't FromContext-recognizable (see live_deadlock_test.go's identical
// tabCtx, tabCancel := chromedp.NewContext(...) precedent), so this fake
// factory must use the real constructor even though it never talks to CDP.
// Each call gets a fresh, incrementing fake target ID unless targetID is
// already non-empty (the ADR-041 D2 adoption path), in which case the
// requested ID is preserved — mirroring createTab's real behavior of
// attaching to the CALLER-supplied target.
func fakeTabFactory() (fn func(allocCtx context.Context, targetID target.ID) (*tabEntry, error), canceledCount *int32) {
	var n int64
	canceled := new(int32)
	fn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		ctx, cancel := chromedp.NewContext(context.Background())
		id := targetID
		if id == "" {
			id = target.ID(fmt.Sprintf("fake-target-%d", atomic.AddInt64(&n, 1)))
		}
		return &tabEntry{
			ctx: ctx,
			cancel: func() {
				atomic.AddInt32(canceled, 1)
				cancel()
			},
			targetID: id,
		}, nil
	}
	return fn, canceled
}

// newTestManagerWithFakeTabs builds a manager with no real Chromium behind it.
//
// It takes no tab cap: ADR-072 D1.5a deleted every counter, and the only limit
// is the FR-060 memory gate. A test that needs the gate to refuse installs
// refuseTabsAtOrAbove (or unmeasurableHost) on m.memoryPressureFn — the seam
// that replaced the cap argument this helper used to take.
func newTestManagerWithFakeTabs(t *testing.T) *BrowserManager {
	t.Helper()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{
		cfg:      cfg,
		key:      testKey,
		sessions: make(map[string]*sessionEntry),
		started:  true, // skip ensureStarted's real Chromium launch
	}
	// A tool call reaches mgr.Live() through controlledResult, so a manager
	// built as a struct literal needs its live-view registry wired the same way
	// NewBrowserManager wires one. Without it every tool-level test in this
	// package panics on a nil registry the moment the control-lock gate runs.
	m.live = newLiveViewRegistry(m)
	fn, _ := fakeTabFactory()
	m.createTabFn = fn
	// Fake tabs are chromedp contexts with no CDP connection behind them, so
	// SwitchTab's real Page.bringToFront would block until PageTimeout. Same
	// rationale as createTabFn — see tabFocusFn's doc comment.
	m.tabFocusFn = func(context.Context, ...chromedp.Action) error { return nil }
	return m
}

// --- Session(default) creation + following the active tab (ADR-041 D1) ---

func TestSession_CreatesSingleTabBrowsingContext(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	ctx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotNil(t, ctx)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)
}

func TestSession_FollowsActiveTabAfterSwitch(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	firstCtx, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// OpenTab makes the new tab active — Session() must now return ITS
	// context, not the first tab's (ADR-041 D1: "Session always returns the
	// ACTIVE tab's context").
	//
	// PRE-EXISTING DATA RACE FIX (confirmed via -race on base 9d31e106):
	// firstCtx/secondCtx/thirdCtx are real chromedp contexts (fakeTabFactory
	// builds them via chromedp.NewContext(context.Background()), same as
	// production createTab) — chromedp.NewContext starts a background
	// goroutine (chromedp.go's "go func() { <-ctx.Done(); ... }") that reads
	// and CASes the context's internal done-channel pointer the moment its
	// parent is ever Done. require.NotEqual/assert.Equal fall through to
	// testify's ObjectsAreEqual -> reflect.DeepEqual for non-[]byte types,
	// which recursively walks the context's UNEXPORTED fields via
	// reflect.Value.IsNil() on that very same memory — a genuine data race
	// against that goroutine, reproduced here:
	//   WARNING: DATA RACE
	//   Write at ... by chromedp.NewContext.func1 (context.(*valueCtx).Done)
	//   Previous read at ... by reflect.Value.IsNil (via testify's
	//   ObjectsAreEqual -> reflect.DeepEqual, called from this test's
	//   require.NotEqual)
	// The fix: never deep-compare a context.Context's internals at all —
	// this test only ever wants OBJECT IDENTITY ("is this the SAME tab's
	// context"), which the language's own `==`/`!=` on the context.Context
	// interface already gives for free: chromedp.NewContext's returned
	// value is ultimately a *context.valueCtx wrapping a *context.cancelCtx
	// (both pointer types), so `==`/`!=` is a plain, race-free pointer
	// comparison — it never dereferences into the struct's fields the way
	// reflect.DeepEqual does, so the background goroutine's writes are
	// never observed by this comparison at all.
	secondCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	assert.True(
		t,
		firstCtx != secondCtx,
		"Session must follow the active tab, not stay pinned to the first tab created",
	)

	// Switching back to tab 0 makes Session() return the first tab's ctx again.
	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)
	thirdCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	assert.True(t, firstCtx == thirdCtx, "Session must follow SwitchTab back to tab 0")
}

// FR-060: the gate that used to be a tab CAP is now live memory, at the same
// site (createFirstTab). The refusal names memory and a remedy, and names no
// limit and no config key — there is none to raise (ADR-072 D1.5a).
func TestSession_MemoryPressure_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(1)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	// A second, DIFFERENT browsing context (session ID) must be refused while
	// the machine is under memory pressure.
	_, err = m.Session("another-session")
	require.Error(t, err)
	assert.ErrorIs(t, err, errMemoryPressureTabOpen)
	assert.Contains(t, err.Error(), "memory")
	assert.Contains(t, err.Error(), "browser_close_tab")
	assert.NotContains(t, strings.ToLower(err.Error()), deletedTabCapConfigKey,
		"the refusal must not name a setting this build no longer has")
	assert.NotContains(t, strings.ToLower(err.Error()), "limit reached")
}

// --- Tab-set add/switch/close/neighbor-activation (ADR-041 D1/D3) ---

func TestOpenTab_AppendsAndActivates(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tab, err := m.OpenTab(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
	assert.False(t, tabs[0].Active)
	assert.True(t, tabs[1].Active)
}

func TestOpenTab_MemoryPressure_Refused(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(2)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err, "the second tab opens while there is memory headroom")

	_, err = m.OpenTab(testSessionID)
	require.Error(t, err, "the third tab must be refused once the machine is under memory pressure")
	assert.ErrorIs(t, err, errMemoryPressureTabOpen)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "a refused OpenTab must not leave a partially-added tab")
}

// FR-082: on a host whose memory cannot be measured at all, the FIRST tab
// opens and the SECOND is refused. A floor of zero would remove browsing
// entirely from gVisor and GKE Sandbox, which this project supports; a floor
// of two is unpriced. Both halves are asserted, because a gate that refuses
// the first tab and a gate that admits without limit both pass a test that
// only checks one of them.
func TestOpenTab_UnmeasurableHost_FirstTabOpensSecondIsRefused(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = unmeasurableHost()

	_, err := m.Session(testSessionID)
	require.NoError(t, err, "the FIRST tab must open on an unmeasurable host")

	_, err = m.OpenTab(testSessionID)
	require.Error(t, err, "the SECOND tab must be refused on an unmeasurable host")
	assert.ErrorIs(t, err, errMemoryPressureTabOpen)
}

func TestSwitchTab_ChangesActiveIndex(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	tab, err := m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
	assert.False(t, tabs[0].Active)
	assert.False(t, tabs[2].Active)
}

func TestSwitchTab_OutOfRange_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, err = m.SwitchTab(testSessionID, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")

	_, err = m.SwitchTab(testSessionID, -1)
	require.Error(t, err)
}

func TestSwitchTab_UnknownSession_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.SwitchTab("never-opened", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no active session")
}

func TestCloseTab_NonActiveTab_KeepsActiveIndexStable(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 2, active
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(testSessionID, 0)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	// Active tab (was index 2) shifted down to index 1 after removing index 0.
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

func TestCloseTab_ActiveTab_ActivatesSlidInNeighbour(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 2
	require.NoError(t, err)

	_, err = m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	// Closing the active tab (index 1): the tab that slides into index 1
	// (formerly index 2) becomes active.
	tabs, activeIdx, err := m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

func TestCloseTab_ActiveLastTab_FallsBackToNewLastTab(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1, active
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)
}

func TestCloseTab_OutOfRange_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	_, _, err = m.CloseTab(testSessionID, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestCloseTab_LastRemainingTab_NeverLeavesZeroTabs(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing the last tab must succeed by opening a fresh replacement, not error")
	require.Len(t, tabs, 1, "the browsing context must never be left with zero tabs")
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)

	// The browsing context must still be usable afterward.
	ctx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotNil(t, ctx)
}

func TestCloseTab_CancelsTheClosedTabsContext(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}
	fn, canceled := fakeTabFactory()
	m.createTabFn = fn

	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	require.EqualValues(t, 0, atomic.LoadInt32(canceled))

	_, _, err = m.CloseTab(testSessionID, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(canceled), "closing a tab must cancel its chromedp context")
}

// --- ADR-041 fix F3: passive Target.targetCreated listener re-arm ---
//
// chromedp.ListenTarget's registration is scoped to the ctx it was given, so
// closing the tab whose ctx currently holds the ADR-041 D2 passive listener
// silently ends new-tab detection forever unless something re-installs it on
// whichever tab becomes the new tab 0. installTargetListenerLocked does this
// re-arm, tracked via sessionEntry.listenerTarget. These tests can't fire a
// real CDP Target.targetCreated event in this pod (no Chromium binary), so
// they verify the re-arm BOOKKEEPING directly: se.listenerTarget must track
// se.tabs[0].targetID after any operation that changes which tab occupies
// slot 0.

// TestCloseTab_NonLastBranch_RearmsListenerOnNewTab0 covers CloseTab's
// non-last branch: closing tab 0 out of >= 2 tabs slides another tab into
// slot 0, and the listener must be re-armed onto IT.
func TestCloseTab_NonLastBranch_RearmsListenerOnNewTab0(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // tab 1
	require.NoError(t, err)

	m.mu.Lock()
	se := m.sessions[testSessionID]
	oldListenerTarget := se.listenerTarget
	oldTab0TargetID := se.tabs[0].targetID
	newTab0TargetIDBeforeClose := se.tabs[1].targetID // what will slide into slot 0
	m.mu.Unlock()

	require.Equal(t, oldTab0TargetID, oldListenerTarget, "the listener must start out armed on tab 0")

	// Close tab 0 — the ONLY non-last branch that changes which tab occupies
	// slot 0 without going through registerFreshSessionLocked.
	_, _, err = m.CloseTab(testSessionID, 0)
	require.NoError(t, err)

	m.mu.Lock()
	se = m.sessions[testSessionID]
	newListenerTarget := se.listenerTarget
	newTab0TargetID := se.tabs[0].targetID
	m.mu.Unlock()

	assert.Equal(t, newTab0TargetIDBeforeClose, newTab0TargetID, "sanity: the surviving tab slid into slot 0")
	assert.Equal(t, newTab0TargetID, newListenerTarget,
		"the listener must be re-armed onto the NEW tab 0 after closing the old one")
	assert.NotEqual(t, oldListenerTarget, newListenerTarget,
		"the listener bookkeeping must actually change — re-arming onto the same (now-closed) target would "+
			"silently leave new-tab detection dead")
}

// TestCloseTab_LastRemainingTab_RearmsListenerOnReplacement covers the
// last-tab-replacement path (registerFreshSessionLocked, via createFirstTab):
// closing the LAST tab tears down the whole sessionEntry and builds a brand
// new one around a fresh blank tab — the listener must be armed on THAT tab,
// not left pointing at the torn-down one.
func TestCloseTab_LastRemainingTab_RearmsListenerOnReplacement(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	oldListenerTarget := m.sessions[testSessionID].listenerTarget
	oldTab0TargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()
	require.Equal(t, oldTab0TargetID, oldListenerTarget)

	tabs, _, err := m.CloseTab(testSessionID, 0)
	require.NoError(t, err, "closing the last tab must succeed via the fresh-replacement path")
	require.Len(t, tabs, 1)

	m.mu.Lock()
	se := m.sessions[testSessionID]
	newListenerTarget := se.listenerTarget
	newTab0TargetID := se.tabs[0].targetID
	m.mu.Unlock()

	assert.Equal(t, newTab0TargetID, newListenerTarget,
		"the listener must be armed on the replacement tab's target, not the torn-down one")
	assert.NotEqual(t, oldListenerTarget, newListenerTarget,
		"the replacement tab is a genuinely new CDP target — the listener bookkeeping must have moved on")
}

// --- ADR-041 fix F1: Session/OpenTab/CloseTab must not race to
// independently create (and leak) the first tab of a not-yet-existing
// browsing context ---

// TestSession_ConcurrentWithOpenTab_NoOrphanedTabs is the F1 regression
// guard: before the fix, OpenTab (and CloseTab's last-tab-replacement) did
// NOT register in m.pending the way Session() did, so a human's "+ new tab"
// (OpenTab) racing the agent's next Session() call for the SAME brand-new
// sessionID could each independently call createTab (unlocked) and then
// blindly overwrite m.sessions[sessionID] — whichever finished last won,
// silently discarding (leaking: never canceled, never counted by
// totalTabCountLocked, never reachable again) the other's freshly-created
// tab.
//
// The invariant this asserts — every physically-created tab is EITHER
// tracked in the surviving tab set OR explicitly canceled, with nothing
// in-between — holds regardless of goroutine scheduling: OpenTab calls that
// happen to observe the browsing context as not-yet-existing correctly
// converge on ONE shared first tab via createFirstTab's m.pending dedup
// (same as Session()); OpenTab calls that happen to observe it as
// already-existing correctly append their OWN additional tab (that's
// OpenTab's actual contract, not a bug) — so the raw tab COUNT is
// scheduling-dependent and not a safe thing to pin exactly, but "nothing
// vanishes without being tracked or canceled" always must hold.
func TestSession_ConcurrentWithOpenTab_NoOrphanedTabs(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}

	fn, canceled := fakeTabFactory()
	var created int32
	m.createTabFn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		atomic.AddInt32(&created, 1)
		return fn(allocCtx, targetID)
	}

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, errs[i] = m.Session(testSessionID)
			} else {
				_, errs[i] = m.OpenTab(testSessionID)
			}
		}(i)
	}
	wg.Wait()

	for i := range n {
		require.NoError(t, errs[i], "no concurrent Session/OpenTab call for a brand-new session should error")
	}

	m.mu.Lock()
	se, ok := m.sessions[testSessionID]
	require.True(t, ok, "the browsing context must exist after the race")
	survivingTabs := len(se.tabs)
	total := m.totalTabCountLocked()
	m.mu.Unlock()

	assert.Equal(t, survivingTabs, total,
		"totalTabCountLocked must exactly match the reachable tab count — no undercount from an "+
			"orphaned overwrite")

	createdN := atomic.LoadInt32(&created)
	canceledN := atomic.LoadInt32(canceled)
	assert.EqualValues(t, createdN, int32(survivingTabs)+canceledN,
		"ADR-041 fix F1: every physically-created tab must be EITHER tracked in the surviving "+
			"sessionEntry OR explicitly canceled — never orphaned (created, then silently discarded by "+
			"a blind m.sessions[id]=... overwrite, with its chromedp context and goroutine leaked forever)")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, survivingTabs)
	assert.GreaterOrEqual(t, survivingTabs, 1)
}

// TestCloseTab_LastTabReplacement_ConcurrentWithOpenTab_NoLeak is F1's
// second regression guard: CloseTab's last-tab-replacement branch used to
// build its own bare sessionEntry and blindly overwrite m.sessions[sessionID]
// too, exactly like OpenTab did — a concurrent OpenTab racing the replacement
// could suffer the identical leak. Drives that race directly and applies the
// same "created == tracked + canceled" no-orphan invariant as the test above.
func TestCloseTab_LastTabReplacement_ConcurrentWithOpenTab_NoLeak(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}

	fn, canceled := fakeTabFactory()
	var created int32
	m.createTabFn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		atomic.AddInt32(&created, 1)
		return fn(allocCtx, targetID)
	}

	_, err = m.Session(testSessionID)
	require.NoError(t, err)
	require.EqualValues(t, 1, atomic.LoadInt32(&created))

	var wg sync.WaitGroup
	var closeErr, openErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, closeErr = m.CloseTab(testSessionID, 0) // triggers last-tab-replacement
	}()
	go func() {
		defer wg.Done()
		_, openErr = m.OpenTab(testSessionID)
	}()
	wg.Wait()

	require.NoError(t, closeErr)
	require.NoError(t, openErr)

	m.mu.Lock()
	se, ok := m.sessions[testSessionID]
	require.True(t, ok)
	survivingTabs := len(se.tabs)
	total := m.totalTabCountLocked()
	m.mu.Unlock()

	assert.Equal(t, survivingTabs, total, "totalTabCountLocked must match the actually-reachable tab count")
	assert.GreaterOrEqual(t, survivingTabs, 1, "the browsing context must never end up with zero tabs")

	createdN := atomic.LoadInt32(&created)
	canceledN := atomic.LoadInt32(canceled)
	assert.EqualValues(t, createdN, int32(survivingTabs)+canceledN,
		"every physically-created tab (the original + whatever the race created) must be EITHER "+
			"tracked or canceled — never orphaned")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, survivingTabs)
}

// --- ADR-041 D2: adoption + the FR-060 memory gate on adoption ---

func TestAdoptTarget_AppendsAndActivatesNewTab(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	result, err := m.adoptTarget(testSessionID, target.ID("opened-by-window-open"))
	require.NoError(t, err)
	require.NotNil(t, result.Adopted)
	assert.False(t, result.Unadopted)
	assert.Equal(t, 1, result.Adopted.Index)
	assert.True(t, result.Adopted.Active)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
}

func TestAdoptTarget_AlreadyTracked_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tid := target.ID("dup-target")
	result1, err := m.adoptTarget(testSessionID, tid)
	require.NoError(t, err)
	require.NotNil(t, result1.Adopted)

	result2, err := m.adoptTarget(testSessionID, tid)
	require.NoError(t, err)
	assert.Nil(t, result2.Adopted, "adopting an already-tracked target must be a silent no-op")
	assert.False(t, result2.Unadopted, "already-tracked is a true no-op, not a reportable Unadopted case")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "must not double-adopt the same target")
}

func TestAdoptTarget_MemoryPressure_ReportsUnadopted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(1)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	result, err := m.adoptTarget(testSessionID, target.ID("runaway-window-open"))
	require.NoError(t, err, "capped adoption is not an error — a runaway window.open loop must not error the caller")
	assert.Nil(t, result.Adopted)
	require.True(
		t,
		result.Unadopted,
		"ADR-041 fix F2: a detected-but-refused target must be reported, not silently dropped",
	)
	assert.Equal(t, tabAdoptReasonMemoryPressure, result.Reason)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 1, "an adoption the memory gate refuses must be dropped, not appended")
}

func TestAdoptTarget_NoBrowsingContext_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	// No Session() call yet — nothing to adopt into.
	result, err := m.adoptTarget(testSessionID, target.ID("orphan"))
	require.NoError(t, err)
	assert.Nil(t, result.Adopted)
	assert.False(t, result.Unadopted)
}

// TestAdoptTarget_ConcurrentAdoptionOfSameTarget_AllCallersSeeTheSameOutcome
// is the concurrency-safety guard for adoptTarget's pendingAdoptEntry design
// (see its doc comment in manager.go). It supersedes an earlier version of
// this test (renamed from *_OnlyOneWins) whose "exactly one caller sees
// Adopted" assertion encoded the OLD, since-fixed contract: a "losing"
// concurrent caller used to get told nothing happened (a blind no-op) even
// though the winner went on to succeed. That is exactly the gap that made
// browser_click silently report plain success on a target="_blank" click —
// its own ReconcileTabs call routinely LOST this same race to the async
// passive listener (a real CDP target-created event can be dispatched before
// the click's own CDP round trip even returns), so it saw "already pending"
// and reported nothing. The fix: a losing caller now WAITS for the winner's
// actual result and returns THAT, so every caller asking about the same
// target — winner and waiters alike — ends up with the SAME, correct answer.
// The one invariant that must still hold unconditionally: exactly ONE
// physical tab gets created for the target, never a duplicate, no matter how
// many concurrent callers ask about it.
//
// The n-1 "losing" callers are deliberately synchronized to land WHILE the
// winner's adoption is in flight — not left to an unsynchronized `go`
// burst against createTabFn's near-instant fake, which flaked under CI's
// contended (-p 4, shared 8-core) full-suite runs: heavy external
// scheduling pressure could let some of the n-1 goroutines receive no CPU
// time at all until AFTER the fake createTab (a few instructions, no real
// I/O) had already returned and the winner had fully finished, appending
// the tab to se.tabs and unlocking. Those late arrivals then hit
// adoptTarget's OWN top-of-function "already ours" fast path — a true,
// deliberate no-op (see TestAdoptTarget_AlreadyTracked_IsNoop) for a
// caller that finds a target already tracked, indistinguishable from a
// legitimate sequential re-adoption of a target this same test cannot
// (and must not) also report as freshly Adopted. That is a test
// synchronization gap, not a pendingAdoptEntry bug: the fix here is to
// block the winner inside createTab (via createTabFn) until every losing
// caller is confirmed in flight, so all n-1 deterministically observe the
// registered pendingAdoptEntry and take the WAIT branch this test exists
// to exercise, regardless of scheduler fairness.
func TestAdoptTarget_ConcurrentAdoptionOfSameTarget_AllCallersSeeTheSameOutcome(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	tid := target.ID("raced-target")
	const n = 8

	baseFn := m.createTabFn
	registered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	// adoptTarget's pendingAdopt gate (manager.go) guarantees only ONE
	// caller ever reaches createTabFn for a given targetID — every other
	// concurrent caller either becomes a waiter on the registered entry or
	// (the case under test) must be given the chance to become one. So
	// gating on targetID == tid here only ever fires for the winner.
	m.createTabFn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		if targetID == tid {
			once.Do(func() { close(registered) })
			<-release
		}
		return baseFn(allocCtx, targetID)
	}

	var wg sync.WaitGroup
	results := make([]tabAdoptResult, n)
	errs := make([]error, n)

	// Launch the winner alone first so it deterministically registers the
	// pendingAdoptEntry (manager.go) and then blocks inside createTabFn —
	// removing any reliance on scheduler luck to land the other n-1 calls
	// while the (otherwise near-instant) fake adoption is still in flight.
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], errs[0] = m.adoptTarget(testSessionID, tid)
	}()
	select {
	case <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("winner never reached createTabFn / registered the pendingAdoptEntry")
	}

	// The winner is now confirmedly blocked mid-adoption with its entry
	// still registered in m.pendingAdopt. Launch the other n-1 callers —
	// no matter how long the scheduler takes to actually run each one, the
	// entry stays put (the winner cannot proceed until release is closed
	// below), so every one of them is guaranteed to find it once it does
	// run and take the WAIT branch.
	for i := 1; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = m.adoptTarget(testSessionID, tid)
		}(i)
	}
	// A generous, fixed safety margin (orders of magnitude larger than a
	// handful of uncontended mutex acquisitions ever need, even under heavy
	// external CPU pressure) for the n-1 goroutines above to actually run
	// far enough to observe the still-registered entry before the winner is
	// released. There is no signal to wait on instead: reaching the
	// internal "already pending, wait on entry.done" select is exactly the
	// unexported implementation detail this black-box test must not reach
	// into to observe directly.
	time.Sleep(50 * time.Millisecond)
	close(release)

	wg.Wait()

	adoptedCount := 0
	for i := range n {
		require.NoError(t, errs[i])
		if results[i].Adopted != nil {
			adoptedCount++
			assert.Equal(t, 1, results[i].Adopted.Index,
				"every caller that sees Adopted must see the SAME adopted tab, not a duplicate")
		}
	}
	assert.Equal(t, n, adoptedCount,
		"ALL concurrent callers asking about the same target must see it was adopted — a losing racer waits "+
			"for and returns the winner's actual outcome instead of a blind no-op (pendingAdoptEntry)")

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "the target must be adopted EXACTLY ONCE — no duplicate tab despite 8 concurrent callers")
}

// --- ADR-041 D2: ReconcileTabs ---

func TestReconcileTabs_AdoptsNewlyDetectedTarget(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	rootCtx, err := m.Session(testSessionID)
	require.NoError(t, err)
	require.NotNil(t, rootCtx)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{
				TargetID: target.ID("new-blank-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://cal.com/booking",
			},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	require.True(t, outcome.Adopted)
	require.NotNil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)
	assert.Equal(t, 1, outcome.NewActive.Index)

	tabs, activeIdx, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
}

func TestReconcileTabs_IgnoresUnrelatedAndAlreadyTrackedTargets(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			// No OpenerID at all — a top-level target, not opened by us.
			{TargetID: target.ID("unrelated-top-level"), Type: "page", OpenerID: ""},
			// Opened by something outside this browsing context entirely.
			{TargetID: target.ID("unrelated-child"), Type: "page", OpenerID: target.ID("some-other-tab")},
			// A non-page target (e.g. a service worker) must be ignored.
			{TargetID: target.ID("a-worker"), Type: "service_worker", OpenerID: rootTargetID},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 1, "nothing here was opened by our own tab set — nothing should be adopted")
}

func TestReconcileTabs_NoBrowsingContext_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)
}

func TestReconcileTabs_ListTargetsError_IsPropagated(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	wantErr := fmt.Errorf("simulated CDP transport failure")
	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return nil, wantErr
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
}

// TestReconcileTabs_MaxTabsCap_ReportsUnadopted drives ReconcileTabs through
// a tab set the memory gate refuses to grow, and asserts the outcome reports the
// stranded tab — ADR-041 fix F2's regression guard, the exact failure class
// the ADR was written to prevent: a click opens a new tab that can't be
// adopted, and the pre-fix code silently reported nothing at all instead of
// telling the agent a tab was stranded. applyReconcileOutcome (tools.go) is
// what turns this into browser_click's
// tab_opened_but_not_adopted/reason/note result fields; this test proves the
// manager-level signal it consumes is actually populated.
func TestReconcileTabs_MemoryPressure_ReportsUnadopted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = refuseTabsAtOrAbove(1) // the root tab already exhausts the headroom
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{
				TargetID: target.ID("stranded-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://cal.com/booking",
			},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	require.True(t, outcome.Unadopted, "a genuinely new tab was detected but could not be adopted — must be reported")
	assert.Equal(t, tabAdoptReasonMemoryPressure, outcome.Reason)

	// The result map browser_click actually returns to the agent.
	result := map[string]any{"success": true}
	applyReconcileOutcome(result, outcome)
	assert.Equal(t, true, result["tab_opened_but_not_adopted"])
	assert.Equal(t, string(tabAdoptReasonMemoryPressure), result["reason"])
	// FR-063: the reason the model branches on must be the memory code, and
	// the note must name a remedy without naming a limit or a config key.
	assert.Equal(t, "memory_pressure", result["reason"])
	assert.Contains(t, result["note"], "browser_close_tab")
	reconcileNote, isString := result["note"].(string)
	require.True(t, isString, "the note must be a string the model can read")
	assert.NotContains(t, strings.ToLower(reconcileNote), deletedTabCapConfigKey)
	assert.NotEmpty(t, result["note"], "the agent needs a human-readable explanation, not just a machine reason code")
	assert.Nil(t, result["opened_new_tab"], "an unadopted tab must not ALSO be reported as opened_new_tab")
}

// TestReconcileTabs_OneClickTwoNewTargets_OneAdoptedOneStranded is the
// second-fix-wave regression guard for the exact bug UAT caught: a single
// click that spawns TWO new CDP targets in one go, where the first is
// adopted (filling the memory headroom) and the second is then refused. Before
// this fix, ReconcileOutcome aggregated both signals correctly at the
// manager level, but applyReconcileOutcome's if/else-if (tools.go) reported
// only the FIRST-matched signal (Adopted) to the agent and silently dropped
// the second target's Unadopted/Reason — the agent was told a tab opened but
// never told a second one was stranded. Asserts BOTH the ReconcileOutcome
// itself AND the applyReconcileOutcome result map carry both signals.
func TestReconcileTabs_OneClickTwoNewTargets_OneAdoptedOneStranded(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	// Root tab (1) + exactly one more (2) fits under the headroom; a third does
	// not. This is the same oracle the deleted two-tab cap expressed, now
	// expressed against live memory (FR-060).
	m.memoryPressureFn = refuseTabsAtOrAbove(2)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[testSessionID].tabs[0].targetID
	m.mu.Unlock()

	// One click handler that opened two target="_blank" links: the first
	// fills the remaining headroom, the second is then refused.
	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{
				TargetID: target.ID("adoptable-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://example.com/a",
			},
			{
				TargetID: target.ID("stranded-target"),
				Type:     "page",
				OpenerID: rootTargetID,
				URL:      "https://example.com/b",
			},
		}, nil
	}

	outcome, err := m.ReconcileTabs(testSessionID)
	require.NoError(t, err)

	// Both signals must survive the aggregation — neither clears the other.
	// (The adopted tab's URL isn't asserted here: fakeTabFactory, the test
	// seam used by newTestManagerWithFakeTabs, never populates title/url —
	// only the real createTab does, via refreshTabMeta's chromedp calls —
	// mirroring TestReconcileTabs_AdoptsNewlyDetectedTarget's identical
	// omission above.)
	require.True(t, outcome.Adopted, "the first new target must be reported as adopted")
	require.NotNil(t, outcome.NewActive)
	assert.Equal(t, 1, outcome.NewActive.Index)
	require.True(t, outcome.Unadopted, "the second new target must be reported as stranded, not silently dropped")
	assert.Equal(t, tabAdoptReasonMemoryPressure, outcome.Reason)
	assert.Equal(t, 1, outcome.UnadoptedCount)

	tabs, _, err := m.ListTabs(testSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "exactly one of the two new targets is adopted — the memory gate stops the second")

	// The result map browser_click actually returns to the agent must carry
	// BOTH keys — this is the exact bug: an if/else-if here used to report
	// only one of the two.
	result := map[string]any{"success": true}
	applyReconcileOutcome(result, outcome)
	assert.Equal(t, true, result["opened_new_tab"], "the adopted tab must still be reported")
	assert.Equal(t, true, result["tab_opened_but_not_adopted"], "the stranded tab must ALSO be reported, not dropped")
	assert.Equal(t, string(tabAdoptReasonMemoryPressure), result["reason"])
	assert.NotEmpty(t, result["note"], "the agent needs a human-readable explanation of both outcomes")
}

// TestApplyReconcileOutcome_AdoptedVsUnadoptedVsNoop pins the three
// result-map shapes applyReconcileOutcome (tools.go) produces from a
// BrowserManager.ReconcileOutcome — the mapping ClickTool.Execute relies on
// to surface ADR-041 fix F2's unadopted signal to the agent.
func TestApplyReconcileOutcome_AdoptedVsUnadoptedVsNoop(t *testing.T) {
	t.Run("adopted", func(t *testing.T) {
		tab := Tab{Index: 2, URL: "https://cal.com/booking", Active: true}
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{Adopted: true, NewActive: &tab})
		assert.Equal(t, true, result["opened_new_tab"])
		assert.Equal(t, 2, result["new_tab_index"])
		assert.Equal(t, "https://cal.com/booking", result["new_tab_url"])
		assert.Nil(t, result["tab_opened_but_not_adopted"])
	})

	t.Run("unadopted_memory_pressure", func(t *testing.T) {
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{Unadopted: true, Reason: tabAdoptReasonMemoryPressure})
		assert.Equal(t, true, result["tab_opened_but_not_adopted"])
		assert.Equal(t, "memory_pressure", result["reason"])
		assert.Contains(t, result["note"], "browser_close_tab")
		// FR-063: name a remedy that exists; name no limit and no config key.
		note, isString := result["note"].(string)
		require.True(t, isString, "the note must be a string the model can read")
		assert.Contains(t, strings.ToLower(note), "memory")
		assert.NotContains(t, strings.ToLower(note), deletedTabCapConfigKey)
		assert.NotContains(t, strings.ToLower(note), "tools.browser")
		assert.Nil(t, result["opened_new_tab"])
	})

	// FR-063's own guard: a reason code with no arm of its own must NOT fall
	// through to the default "it could not be adopted" text, because that text
	// suggests nothing and the model retries the same open in a loop.
	t.Run("every_reason_code_has_its_own_arm", func(t *testing.T) {
		for _, reason := range []tabAdoptReason{tabAdoptReasonMemoryPressure, tabAdoptReasonAttachFailed} {
			result := map[string]any{"success": true}
			applyReconcileOutcome(result, ReconcileOutcome{Unadopted: true, Reason: reason})
			note, _ := result["note"].(string)
			assert.NotEmpty(t, note, "reason %q reached no arm of its own", reason)
		}
	})

	t.Run("unadopted_attach_failed", func(t *testing.T) {
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{Unadopted: true, Reason: tabAdoptReasonAttachFailed})
		assert.Equal(t, true, result["tab_opened_but_not_adopted"])
		assert.Equal(t, "attach_failed", result["reason"])
		assert.Nil(t, result["opened_new_tab"])
	})

	t.Run("noop", func(t *testing.T) {
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{})
		assert.Nil(t, result["opened_new_tab"])
		assert.Nil(t, result["tab_opened_but_not_adopted"])
		assert.Nil(t, result["note"])
	})

	// Second-fix-wave regression: a single click can spawn two new targets in
	// one go — one adopted, one stranded. Both keys must appear together;
	// before the fix an if/else-if here reported only the Adopted case.
	t.Run("adopted_and_unadopted_together", func(t *testing.T) {
		tab := Tab{Index: 1, URL: "https://example.com/a", Active: true}
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{
			Adopted: true, NewActive: &tab,
			Unadopted: true, Reason: tabAdoptReasonMemoryPressure, UnadoptedCount: 1,
		})
		assert.Equal(t, true, result["opened_new_tab"], "adoption must still be reported")
		assert.Equal(t, 1, result["new_tab_index"])
		assert.Equal(t, true, result["tab_opened_but_not_adopted"], "the stranded tab must ALSO be reported")
		assert.Equal(t, "memory_pressure", result["reason"])
		assert.NotEmpty(t, result["note"])
	})
}

// --- ADR-041 D4: tabs-changed callback wiring ---

func TestSetTabsChangedFunc_FiresOnOpenSwitchClose(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	var mu sync.Mutex
	var calls []int // recorded activeIdx per call
	m.SetTabsChangedFunc(func(sessionID string, tabs []Tab, activeIdx int) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, activeIdx)
	})

	_, err := m.Session(testSessionID) // fires once (new tab)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID) // fires once (activeIdx=1)
	require.NoError(t, err)
	_, err = m.SwitchTab(testSessionID, 0) // fires once (activeIdx=0)
	require.NoError(t, err)
	_, _, err = m.CloseTab(testSessionID, 1) // fires once
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, 4)
	assert.Equal(t, []int{0, 1, 0, 0}, calls)
}

func TestSetTabsChangedFunc_NeverInvokedWithLockHeld(t *testing.T) {
	// Regression guard for the ADR-038 "no callback under the manager lock"
	// rule: the callback must be able to call BACK into the manager (e.g.
	// ListTabs) without deadlocking.
	m := newTestManagerWithFakeTabs(t)
	done := make(chan struct{})
	m.SetTabsChangedFunc(func(sessionID string, tabs []Tab, activeIdx int) {
		// If notifyTabsChanged were called with m.mu held, this would
		// deadlock and the test would time out (require.Eventually below).
		_, _, err := m.ListTabs(sessionID)
		assert.NoError(t, err)
		close(done)
	})

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	select {
	case <-done:
	default:
		t.Fatal("tabs-changed callback never completed — possible deadlock calling back into the manager")
	}
}
