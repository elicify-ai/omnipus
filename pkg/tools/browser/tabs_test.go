package browser

// tabs_test.go — ADR-041 D1/D2/D3 unit coverage for the tab-set model:
// add/switch/close/neighbour-activation, MaxTabs enforcement on both
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
	"sync"
	"sync/atomic"
	"testing"

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

func newTestManagerWithFakeTabs(t *testing.T, maxTabs int) *BrowserManager {
	t.Helper()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	cfg.MaxTabs = maxTabs
	m := &BrowserManager{
		cfg:      cfg,
		sessions: make(map[string]*sessionEntry),
		started:  true, // skip ensureStarted's real Chromium launch
	}
	fn, _ := fakeTabFactory()
	m.createTabFn = fn
	return m
}

// --- Session(default) creation + following the active tab (ADR-041 D1) ---

func TestSession_CreatesSingleTabBrowsingContext(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)

	ctx, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	require.NotNil(t, ctx)

	tabs, activeIdx, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)
}

func TestSession_FollowsActiveTabAfterSwitch(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)

	firstCtx, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)

	// OpenTab makes the new tab active — Session() must now return ITS
	// context, not the first tab's (ADR-041 D1: "Session always returns the
	// ACTIVE tab's context").
	secondCtx, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	require.NotEqual(t, firstCtx, secondCtx, "Session must follow the active tab, not stay pinned to the first tab created")

	// Switching back to tab 0 makes Session() return the first tab's ctx again.
	_, err = m.SwitchTab(DefaultSessionID, 0)
	require.NoError(t, err)
	thirdCtx, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	assert.Equal(t, firstCtx, thirdCtx, "Session must follow SwitchTab back to tab 0")
}

func TestSession_MaxTabsCap_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 1)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	// A second, DIFFERENT browsing context (session ID) must be refused once
	// the manager-wide tab cap (totalTabCountLocked) is reached.
	_, err = m.Session("another-session")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum concurrent tabs")
	assert.Contains(t, err.Error(), "1")
}

// --- Tab-set add/switch/close/neighbour-activation (ADR-041 D1/D3) ---

func TestOpenTab_AppendsAndActivates(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	tab, err := m.OpenTab(DefaultSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	tabs, activeIdx, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
	assert.False(t, tabs[0].Active)
	assert.True(t, tabs[1].Active)
}

func TestOpenTab_MaxTabsCap_Refused(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 2)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err, "second tab is within the 2-tab cap")

	_, err = m.OpenTab(DefaultSessionID)
	require.Error(t, err, "third tab must be refused — MaxTabs=2")
	assert.Contains(t, err.Error(), "maximum concurrent tabs")

	tabs, _, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "a refused OpenTab must not leave a partially-added tab")
}

func TestSwitchTab_ChangesActiveIndex(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)

	tab, err := m.SwitchTab(DefaultSessionID, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	tabs, activeIdx, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
	assert.False(t, tabs[0].Active)
	assert.False(t, tabs[2].Active)
}

func TestSwitchTab_OutOfRange_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	_, err = m.SwitchTab(DefaultSessionID, 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")

	_, err = m.SwitchTab(DefaultSessionID, -1)
	require.Error(t, err)
}

func TestSwitchTab_UnknownSession_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.SwitchTab("never-opened", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no active session")
}

func TestCloseTab_NonActiveTab_KeepsActiveIndexStable(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID) // tab 1
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID) // tab 2, active
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(DefaultSessionID, 0)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	// Active tab (was index 2) shifted down to index 1 after removing index 0.
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

func TestCloseTab_ActiveTab_ActivatesSlidInNeighbour(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID) // tab 1
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID) // tab 2
	require.NoError(t, err)

	_, err = m.SwitchTab(DefaultSessionID, 1)
	require.NoError(t, err)

	// Closing the active tab (index 1): the tab that slides into index 1
	// (formerly index 2) becomes active.
	tabs, activeIdx, err := m.CloseTab(DefaultSessionID, 1)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

func TestCloseTab_ActiveLastTab_FallsBackToNewLastTab(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID) // tab 1, active
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(DefaultSessionID, 1)
	require.NoError(t, err)
	require.Len(t, tabs, 1)
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)
}

func TestCloseTab_OutOfRange_ReturnsError(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	_, _, err = m.CloseTab(DefaultSessionID, 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestCloseTab_LastRemainingTab_NeverLeavesZeroTabs(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	tabs, activeIdx, err := m.CloseTab(DefaultSessionID, 0)
	require.NoError(t, err, "closing the last tab must succeed by opening a fresh replacement, not error")
	require.Len(t, tabs, 1, "the browsing context must never be left with zero tabs")
	assert.Equal(t, 0, activeIdx)
	assert.True(t, tabs[0].Active)

	// The browsing context must still be usable afterward.
	ctx, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	require.NotNil(t, ctx)
}

func TestCloseTab_CancelsTheClosedTabsContext(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	cfg.MaxTabs = 5
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}
	fn, canceled := fakeTabFactory()
	m.createTabFn = fn

	_, err = m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)
	require.EqualValues(t, 0, atomic.LoadInt32(canceled))

	_, _, err = m.CloseTab(DefaultSessionID, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(canceled), "closing a tab must cancel its chromedp context")
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
	cfg.MaxTabs = 20
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
				_, errs[i] = m.Session(DefaultSessionID)
			} else {
				_, errs[i] = m.OpenTab(DefaultSessionID)
			}
		}(i)
	}
	wg.Wait()

	for i := range n {
		require.NoError(t, errs[i], "no concurrent Session/OpenTab call for a brand-new session should error")
	}

	m.mu.Lock()
	se, ok := m.sessions[DefaultSessionID]
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

	tabs, _, err := m.ListTabs(DefaultSessionID)
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
	cfg.MaxTabs = 10
	m := &BrowserManager{cfg: cfg, sessions: make(map[string]*sessionEntry), started: true}

	fn, canceled := fakeTabFactory()
	var created int32
	m.createTabFn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		atomic.AddInt32(&created, 1)
		return fn(allocCtx, targetID)
	}

	_, err = m.Session(DefaultSessionID)
	require.NoError(t, err)
	require.EqualValues(t, 1, atomic.LoadInt32(&created))

	var wg sync.WaitGroup
	var closeErr, openErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, closeErr = m.CloseTab(DefaultSessionID, 0) // triggers last-tab-replacement
	}()
	go func() {
		defer wg.Done()
		_, openErr = m.OpenTab(DefaultSessionID)
	}()
	wg.Wait()

	require.NoError(t, closeErr)
	require.NoError(t, openErr)

	m.mu.Lock()
	se, ok := m.sessions[DefaultSessionID]
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

	tabs, _, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, survivingTabs)
}

// --- ADR-041 D2: adoption + MaxTabs cap on adoption ---

func TestAdoptTarget_AppendsAndActivatesNewTab(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	result, err := m.adoptTarget(DefaultSessionID, target.ID("opened-by-window-open"))
	require.NoError(t, err)
	require.NotNil(t, result.Adopted)
	assert.False(t, result.Unadopted)
	assert.Equal(t, 1, result.Adopted.Index)
	assert.True(t, result.Adopted.Active)

	tabs, activeIdx, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
}

func TestAdoptTarget_AlreadyTracked_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	tid := target.ID("dup-target")
	result1, err := m.adoptTarget(DefaultSessionID, tid)
	require.NoError(t, err)
	require.NotNil(t, result1.Adopted)

	result2, err := m.adoptTarget(DefaultSessionID, tid)
	require.NoError(t, err)
	assert.Nil(t, result2.Adopted, "adopting an already-tracked target must be a silent no-op")
	assert.False(t, result2.Unadopted, "already-tracked is a true no-op, not a reportable Unadopted case")

	tabs, _, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "must not double-adopt the same target")
}

func TestAdoptTarget_MaxTabsCap_ReportsUnadopted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 1)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	result, err := m.adoptTarget(DefaultSessionID, target.ID("runaway-window-open"))
	require.NoError(t, err, "capped adoption is not an error — a runaway window.open loop must not error the caller")
	assert.Nil(t, result.Adopted)
	require.True(t, result.Unadopted, "ADR-041 fix F2: a detected-but-refused target must be reported, not silently dropped")
	assert.Equal(t, tabAdoptReasonMaxTabs, result.Reason)

	tabs, _, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 1, "adoption beyond MaxTabs must be dropped, not appended")
}

func TestAdoptTarget_NoBrowsingContext_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	// No Session() call yet — nothing to adopt into.
	result, err := m.adoptTarget(DefaultSessionID, target.ID("orphan"))
	require.NoError(t, err)
	assert.Nil(t, result.Adopted)
	assert.False(t, result.Unadopted)
}

func TestAdoptTarget_ConcurrentAdoptionOfSameTarget_OnlyOneWins(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 10)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	tid := target.ID("raced-target")
	const n = 8
	var wg sync.WaitGroup
	results := make([]tabAdoptResult, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = m.adoptTarget(DefaultSessionID, tid)
		}(i)
	}
	wg.Wait()

	adoptedCount := 0
	for i := range n {
		require.NoError(t, errs[i])
		if results[i].Adopted != nil {
			adoptedCount++
		}
	}
	assert.Equal(t, 1, adoptedCount, "exactly one concurrent adoptTarget call for the same target ID must win")

	tabs, _, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 2, "the target must be adopted exactly once, not duplicated")
}

// --- ADR-041 D2: ReconcileTabs ---

func TestReconcileTabs_AdoptsNewlyDetectedTarget(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	rootCtx, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	require.NotNil(t, rootCtx)

	m.mu.Lock()
	rootTargetID := m.sessions[DefaultSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{TargetID: target.ID("new-blank-target"), Type: "page", OpenerID: rootTargetID, URL: "https://cal.com/booking"},
		}, nil
	}

	outcome, err := m.ReconcileTabs(DefaultSessionID)
	require.NoError(t, err)
	require.True(t, outcome.Adopted)
	require.NotNil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)
	assert.Equal(t, 1, outcome.NewActive.Index)

	tabs, activeIdx, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	require.Len(t, tabs, 2)
	assert.Equal(t, 1, activeIdx)
}

func TestReconcileTabs_IgnoresUnrelatedAndAlreadyTrackedTargets(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[DefaultSessionID].tabs[0].targetID
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

	outcome, err := m.ReconcileTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)

	tabs, _, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Len(t, tabs, 1, "nothing here was opened by our own tab set — nothing should be adopted")
}

func TestReconcileTabs_NoBrowsingContext_IsNoop(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	outcome, err := m.ReconcileTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	assert.False(t, outcome.Unadopted)
}

func TestReconcileTabs_ListTargetsError_IsPropagated(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	wantErr := fmt.Errorf("simulated CDP transport failure")
	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return nil, wantErr
	}

	outcome, err := m.ReconcileTabs(DefaultSessionID)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
}

// TestReconcileTabs_MaxTabsCap_ReportsUnadopted drives ReconcileTabs through
// a full tab set (MaxTabs reached) and asserts the outcome reports the
// stranded tab — ADR-041 fix F2's regression guard, the exact failure class
// the ADR was written to prevent: a click opens a new tab that can't be
// adopted, and the pre-fix code silently reported nothing at all instead of
// telling the agent a tab was stranded. applyReconcileOutcome (tools.go) is
// what turns this into browser_click's
// tab_opened_but_not_adopted/reason/note result fields; this test proves the
// manager-level signal it consumes is actually populated.
func TestReconcileTabs_MaxTabsCap_ReportsUnadopted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 1) // cap of 1 — the root tab already fills it
	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	rootTargetID := m.sessions[DefaultSessionID].tabs[0].targetID
	m.mu.Unlock()

	m.listTargets = func(ctx context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: rootTargetID, Type: "page", OpenerID: ""},
			{TargetID: target.ID("stranded-target"), Type: "page", OpenerID: rootTargetID, URL: "https://cal.com/booking"},
		}, nil
	}

	outcome, err := m.ReconcileTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.False(t, outcome.Adopted)
	assert.Nil(t, outcome.NewActive)
	require.True(t, outcome.Unadopted, "a genuinely new tab was detected but could not be adopted — must be reported")
	assert.Equal(t, tabAdoptReasonMaxTabs, outcome.Reason)

	// The result map browser_click actually returns to the agent.
	result := map[string]any{"success": true}
	applyReconcileOutcome(result, outcome)
	assert.Equal(t, true, result["tab_opened_but_not_adopted"])
	assert.Equal(t, string(tabAdoptReasonMaxTabs), result["reason"])
	assert.NotEmpty(t, result["note"], "the agent needs a human-readable explanation, not just a machine reason code")
	assert.Nil(t, result["opened_new_tab"], "an unadopted tab must not ALSO be reported as opened_new_tab")
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

	t.Run("unadopted_max_tabs", func(t *testing.T) {
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{Unadopted: true, Reason: tabAdoptReasonMaxTabs})
		assert.Equal(t, true, result["tab_opened_but_not_adopted"])
		assert.Equal(t, "max_tabs_reached", result["reason"])
		assert.Contains(t, result["note"], "browser_close_tab")
		assert.Nil(t, result["opened_new_tab"])
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
}

// --- ADR-041 D4: tabs-changed callback wiring ---

func TestSetTabsChangedFunc_FiresOnOpenSwitchClose(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)

	var mu sync.Mutex
	var calls []int // recorded activeIdx per call
	m.SetTabsChangedFunc(func(sessionID string, tabs []Tab, activeIdx int) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, activeIdx)
	})

	_, err := m.Session(DefaultSessionID) // fires once (new tab)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID) // fires once (activeIdx=1)
	require.NoError(t, err)
	_, err = m.SwitchTab(DefaultSessionID, 0) // fires once (activeIdx=0)
	require.NoError(t, err)
	_, _, err = m.CloseTab(DefaultSessionID, 1) // fires once
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
	m := newTestManagerWithFakeTabs(t, 5)
	done := make(chan struct{})
	m.SetTabsChangedFunc(func(sessionID string, tabs []Tab, activeIdx int) {
		// If notifyTabsChanged were called with m.mu held, this would
		// deadlock and the test would time out (require.Eventually below).
		_, _, err := m.ListTabs(sessionID)
		assert.NoError(t, err)
		close(done)
	})

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	select {
	case <-done:
	default:
		t.Fatal("tabs-changed callback never completed — possible deadlock calling back into the manager")
	}
}
