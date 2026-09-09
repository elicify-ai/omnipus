package browser

// reaper_edge_test.go — EDGE-CASE coverage for the tab-set reaper rewrite.
//
// idle_reaper_test.go already covers the session-level happy path (whole
// context idle -> reaped, viewer attached -> never reaped, agent activity
// keeps it alive, zero-TTL disables reaping, unstamped session isn't treated
// as infinitely idle, viewer-count underflow, unknown-session no-op). This
// file is deliberately narrower and meaner: it targets the seams the rewrite
// actually introduces — per-tab lastActivity, partial-tab reaping inside a
// surviving session, the 5-minute TTL boundary, activeIdx bookkeeping across
// a mid-set removal, and defensive/degenerate inputs.
//
// Contract under test (pinned directly for this suite — there is no
// wave-spec doc for this feature):
//   - ReapIdleSessions() []string returns ONLY session IDs whose whole
//     browsing context was closed (every tab gone). A tab closed
//     individually without emptying its session is NOT in the return value.
//   - DefaultIdleTTL is 5 minutes (down from 30).
//   - tabEntry gains its own lastActivity time.Time.
//   - viewers>0 protects the ENTIRE session, however idle its tabs are.
//   - Otherwise, each tab idle longer than the TTL is closed individually;
//     if that empties the session, browserCancel fires, the session is
//     deleted, and its ID is returned.
//   - A zero lastActivity (tab or session) is stamped now and judged on a
//     LATER sweep — never treated as infinitely idle.
//   - activeIdx must keep pointing at the same tab after tabs are removed
//     from the middle of the set.
//
// Time is driven exclusively through m.nowFn (via the *time.Time clock
// pointer returned by newEdgeManager) — no test in this file calls
// time.Now() or sleeps.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test scaffolding --------------------------------------------------------

// cancelTracker records how many times each fake tab's cancel func fired,
// keyed by target ID. Plain int counters (not just "something was canceled
// somewhere") so a test can assert precisely which tabs the reaper closed —
// and that the survivors were never touched at all.
type cancelTracker struct {
	mu     sync.Mutex
	counts map[target.ID]int
}

func newCancelTracker() *cancelTracker {
	return &cancelTracker{counts: make(map[target.ID]int)}
}

func (c *cancelTracker) count(id target.ID) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[id]
}

// trackedTabFactory mirrors tabs_test.go's fakeTabFactory (a real-but-
// undialed chromedp context — installTargetListenerLocked's
// chromedp.ListenTarget panics on anything that isn't FromContext-
// recognizable, so the fake can't just be a bare context.WithCancel), but
// records cancel invocations per target ID instead of one aggregate counter.
func trackedTabFactory(tracker *cancelTracker) func(context.Context, target.ID) (*tabEntry, error) {
	var n int64
	return func(_ context.Context, targetID target.ID) (*tabEntry, error) {
		ctx, cancel := chromedp.NewContext(context.Background())
		id := targetID
		if id == "" {
			n++
			id = target.ID(fmt.Sprintf("edge-target-%d", n))
		}
		return &tabEntry{
			ctx: ctx,
			cancel: func() {
				tracker.mu.Lock()
				tracker.counts[id]++
				tracker.mu.Unlock()
				cancel() // stdlib context.CancelFunc — safe to call more than once
			},
			targetID: id,
		}, nil
	}
}

// newEdgeManager builds a fake-tab manager with a controllable clock, a
// no tab cap at all (these tests open several tabs across several sessions),
// and per-target cancel tracking.
func newEdgeManager(t *testing.T, ttl time.Duration, tracker *cancelTracker) (*BrowserManager, *time.Time) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.createTabFn = trackedTabFactory(tracker)
	m.cfg.IdleTTL = ttl
	clock := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	m.nowFn = func() time.Time { return clock }
	return m, &clock
}

// openTabs opens n tabs total for sessionID (tab 0 via Session(), the rest
// via OpenTab) and returns their tabEntry pointers in creation order. Every
// returned tab starts with a zero lastActivity — callers stamp the values
// they actually want to test with setTabActivity.
func openTabs(t *testing.T, m *BrowserManager, sessionID string, n int) []*tabEntry {
	t.Helper()
	_, err := m.Session(sessionID)
	require.NoError(t, err)
	for i := 1; i < n; i++ {
		_, err := m.OpenTab(sessionID)
		require.NoError(t, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	se := m.sessions[sessionID]
	require.NotNil(t, se)
	require.Len(t, se.tabs, n)
	tabs := make([]*tabEntry, len(se.tabs))
	copy(tabs, se.tabs)
	return tabs
}

// setTabActivity stamps a single tab's lastActivity directly, bypassing
// whatever production call sites do the stamping — these tests exercise the
// reaper's sweep logic itself, not who touches what.
func setTabActivity(m *BrowserManager, tab *tabEntry, when time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tab.lastActivity = when
}

// --- DefaultIdleTTL literal pin ----------------------------------------------

// TestDefaultIdleTTL_Is5Minutes pins the literal new value. idle_reaper_test.go's
// TestDefaultConfig_SetsIdleTTL only checks cfg.IdleTTL == DefaultIdleTTL,
// which is self-referential and would pass even if DefaultIdleTTL were left
// at its old 30-minute value — only a literal comparison catches that
// regression.
func TestDefaultIdleTTL_Is5Minutes(t *testing.T) {
	assert.Equal(t, 5*time.Minute, DefaultIdleTTL,
		"the reaper rewrite lowers the default idle TTL from 30m to 5m")
}

// --- partial-tab idleness -----------------------------------------------------

// TestReapIdleSessions_PartialTabIdle_OnlyIdleTabsClosed — a session with a
// mix of idle and fresh tabs must lose only the idle ones. The session
// itself survives, so it must NOT appear in ReapIdleSessions' return value —
// pinning the "returns only fully-closed sessions" half of the contract
// against the most likely implementation bug (returning session IDs for any
// session that had SOME tab closed).
func TestReapIdleSessions_PartialTabIdle_OnlyIdleTabsClosed(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	tabs := openTabs(t, m, testSessionID, 3)
	idleT0, freshT1, idleT2 := tabs[0], tabs[1], tabs[2]

	base := *clock
	setTabActivity(m, idleT0, base)
	setTabActivity(m, idleT2, base)

	m.mu.Lock()
	browserCtx := m.sessions[testSessionID].browserCtx
	m.mu.Unlock()

	*clock = clock.Add(6 * time.Minute) // past the 5-minute TTL for T0/T2
	setTabActivity(m, freshT1, *clock)  // touched right now — must survive

	reaped := m.ReapIdleSessions()

	assert.Empty(t, reaped, "the session survives (one tab remains) — it must not be in the reaped list")
	assert.Equal(t, 1, tracker.count(idleT0.targetID), "idle tab 0 must be closed exactly once")
	assert.Equal(t, 1, tracker.count(idleT2.targetID), "idle tab 2 must be closed exactly once")
	assert.Zero(t, tracker.count(freshT1.targetID), "the fresh tab must never be closed")

	m.mu.Lock()
	se, ok := m.sessions[testSessionID]
	m.mu.Unlock()
	require.True(t, ok, "session must still be present")
	require.Len(t, se.tabs, 1, "only the fresh tab must remain")
	assert.Equal(t, freshT1.targetID, se.tabs[0].targetID)
	assert.Equal(t, 0, se.activeIdx, "the sole survivor must be a valid activeIdx target")
	assert.NoError(t, browserCtx.Err(), "the browsing context must stay alive — the session was not fully emptied")
}

// --- TTL boundary --------------------------------------------------------------

// TestReapIdleSessions_TTLBoundary_ExactlyAtVsOneNanosecondPast pins which
// side of the TTL is inclusive. This mirrors the house convention already
// established at the session level (now.Sub(lastActivity) < ttl -> skip,
// i.e. idle-duration >= ttl reaps): idle-duration == ttl is INCLUSIVE
// (reaped); ttl-1ns is exclusive (survives).
func TestReapIdleSessions_TTLBoundary_ExactlyAtVsOneNanosecondPast(t *testing.T) {
	const ttl = 5 * time.Minute
	tests := []struct {
		name       string
		idleFor    time.Duration
		wantReaped bool
	}{
		{"one nanosecond before TTL survives", ttl - time.Nanosecond, false},
		{"exactly at TTL is reaped (inclusive)", ttl, true},
		{"one nanosecond past TTL is reaped", ttl + time.Nanosecond, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newCancelTracker()
			m, clock := newEdgeManager(t, ttl, tracker)

			tabs := openTabs(t, m, testSessionID, 1)
			setTabActivity(m, tabs[0], *clock)

			*clock = clock.Add(tc.idleFor)
			reaped := m.ReapIdleSessions()

			if tc.wantReaped {
				assert.Equal(t, []string{testSessionID}, reaped)
			} else {
				assert.Empty(t, reaped)
			}
		})
	}
}

// --- viewer protection ----------------------------------------------------------

// TestReapIdleSessions_ViewerProtectsEveryTabEvenAfterHours — rule 1 covers
// the WHOLE session, not just the currently-active tab: every tab in a
// viewed session must survive no matter how idle, individually.
func TestReapIdleSessions_ViewerProtectsEveryTabEvenAfterHours(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	tabs := openTabs(t, m, testSessionID, 3)
	for _, tb := range tabs {
		setTabActivity(m, tb, *clock)
	}
	m.ViewerAttached(testSessionID)

	*clock = clock.Add(6 * time.Hour)
	// FR-052: a viewer pins only while it keeps proving it is there. Six
	// hours of a real panel is six hours of pongs; see viewer_heartbeat_test.go.
	m.ViewerHeartbeat(testSessionID)
	reaped := m.ReapIdleSessions()

	assert.Empty(t, reaped)
	for _, tb := range tabs {
		assert.Zero(t, tracker.count(tb.targetID), "no tab may be closed while a viewer is attached, however idle")
	}
	m.mu.Lock()
	se, ok := m.sessions[testSessionID]
	m.mu.Unlock()
	require.True(t, ok, "the viewed session must still exist")
	assert.Len(t, se.tabs, 3)
}

// TestReapIdleSessions_ViewerDetach_RestartsIdleClockFromDetachMoment pins
// the coordinator-directed semantics: ViewerAttached/ViewerDetached call
// touchAllTabsLocked, so detaching the last viewer restarts EVERY tab's idle
// clock from the moment of departure. A tab that sat idle for hours while a
// viewer watched it gets a full, fresh TTL grace period once that viewer
// leaves — "N minutes for tabs not open in the UI" counts from when the
// panel closes, not retroactively from whenever an agent last touched it.
//
// *** VOLATILITY FLAG — see final report ***
// During this QA pass, manager.go's ViewerAttached/ViewerDetached/
// touchAllTabsLocked implementation was observed to change UNDER TEST at
// least three times while this exact behavior was being verified: (1)
// originally session-level touchSessionLocked with 30m TTL, pre-per-tab
// rewrite; (2) touch-on-detach via touchAllTabsLocked (matches this test);
// (3) briefly rewritten to explicitly "pure bookkeeping, does NOT touch any
// tab's lastActivity" (the opposite), citing an earlier version of THIS test
// by its old name as the thing it satisfied; (4) rewritten back to (2),
// touchAllTabsLocked restored, now citing this test's CURRENT name as the
// pinned contract. Each transition was independently confirmed by re-reading
// manager.go and re-running the suite, not assumed from a single read. This
// test locks to the coordinator's explicit instruction, which is also what
// the code implements as of the most recent read — but given the observed
// churn, re-verify this specific test after any further manager.go edit
// before trusting a green run here.
func TestReapIdleSessions_ViewerDetach_RestartsIdleClockFromDetachMoment(t *testing.T) {
	const ttl = 5 * time.Minute
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, ttl, tracker)

	tabs := openTabs(t, m, testSessionID, 1)
	setTabActivity(m, tabs[0], *clock)
	m.ViewerAttached(testSessionID)

	*clock = clock.Add(3 * time.Hour) // deep past TTL while "protected"
	m.ViewerHeartbeat(testSessionID)  // FR-052: still a live panel, not a phantom
	require.Empty(t, m.ReapIdleSessions(), "must stay protected while the viewer is attached")

	m.ViewerDetached(testSessionID)
	detachedAt := *clock

	// One nanosecond short of a full TTL since the detach: must still survive
	// — proves the clock restarted at detach rather than staying at its
	// pre-detach (hours-idle) value.
	*clock = detachedAt.Add(ttl - time.Nanosecond)
	assert.Empty(t, m.ReapIdleSessions(),
		"a tab must not be reaped before a full TTL has elapsed SINCE the last viewer's detach")

	// One nanosecond past a full TTL since the detach: now reapable.
	*clock = detachedAt.Add(ttl + time.Nanosecond)
	reaped := m.ReapIdleSessions()
	assert.Equal(t, []string{testSessionID}, reaped,
		"once a full TTL has elapsed since the last viewer left, the tab must be reaped")
}

// --- multiple sessions ------------------------------------------------------

// TestReapIdleSessions_MultipleSessions_OnlyFullyIdleSessionsReturned drives
// four sessions through four different fates in one manager and checks the
// returned slice names exactly the one that fully closed — this is the test
// that would catch a reaper hardcoded to "close everything" or "close
// nothing" (both trivially pass a single-session test).
func TestReapIdleSessions_MultipleSessions_OnlyFullyIdleSessionsReturned(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	// A: single idle tab, no viewer -> fully closes.
	aTabs := openTabs(t, m, "session-a", 1)
	setTabActivity(m, aTabs[0], *clock)

	// B: single tab, will be touched again right before the sweep -> survives.
	bTabs := openTabs(t, m, "session-b", 1)
	setTabActivity(m, bTabs[0], *clock)

	// C: viewer attached, tab idle -> survives regardless (rule 1).
	cTabs := openTabs(t, m, "session-c", 1)
	setTabActivity(m, cTabs[0], *clock)
	m.ViewerAttached("session-c")

	// D: two tabs, one goes idle, one stays fresh -> partial reap, session survives.
	dTabs := openTabs(t, m, "session-d", 2)
	setTabActivity(m, dTabs[0], *clock)

	*clock = clock.Add(6 * time.Minute) // past the 5-minute TTL
	setTabActivity(m, bTabs[0], *clock) // B: freshly touched right at sweep time
	setTabActivity(m, dTabs[1], *clock) // D's second tab: freshly touched
	m.ViewerHeartbeat("session-c")      // FR-052: C's viewer is live, not a phantom

	reaped := m.ReapIdleSessions()

	assert.ElementsMatch(t, []string{"session-a"}, reaped,
		"exactly session-a fully emptied; B/C/D must not appear even though D lost a tab")

	m.mu.Lock()
	_, aOK := m.sessions["session-a"]
	_, bOK := m.sessions["session-b"]
	_, cOK := m.sessions["session-c"]
	dSE, dOK := m.sessions["session-d"]
	m.mu.Unlock()

	assert.False(t, aOK, "session-a must be gone")
	assert.True(t, bOK, "session-b must survive")
	assert.True(t, cOK, "session-c must survive (viewer-protected)")
	require.True(t, dOK, "session-d must survive (one tab remains)")
	require.Len(t, dSE.tabs, 1, "session-d must have lost exactly its idle tab")
	assert.Equal(t, dTabs[1].targetID, dSE.tabs[0].targetID)
}

// --- activeIdx bookkeeping ---------------------------------------------------

// TestReapIdleSessions_ActiveIdxFollowsSameTab_RemovalPosition removes a
// single non-active tab from the first, a middle, and the last position of a
// 3-tab set and checks activeIdx keeps pointing at the SAME tab identity
// (not merely a valid index) in every case.
func TestReapIdleSessions_ActiveIdxFollowsSameTab_RemovalPosition(t *testing.T) {
	tests := []struct {
		name           string
		idleCreation   int // creation-order index of the tab that goes idle
		activeCreation int // creation-order index of the tab left active
	}{
		{"removing the first tab", 0, 1},
		{"removing a middle tab", 1, 2},
		{"removing the last tab", 2, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newCancelTracker()
			m, clock := newEdgeManager(t, 5*time.Minute, tracker)

			tabs := openTabs(t, m, testSessionID, 3)
			base := *clock
			for _, tb := range tabs {
				setTabActivity(m, tb, base) // everyone starts equally "idle-eligible"
			}

			_, err := m.SwitchTab(testSessionID, tc.activeCreation)
			require.NoError(t, err)
			wantTargetID := tabs[tc.activeCreation].targetID

			*clock = clock.Add(6 * time.Minute) // past TTL for whoever isn't re-touched
			for i, tb := range tabs {
				if i != tc.idleCreation {
					setTabActivity(m, tb, *clock) // keep every non-idle tab fresh at sweep time
				}
			}

			reaped := m.ReapIdleSessions()
			assert.Empty(t, reaped, "the active tab and at least one sibling survive — session must not close")
			assert.Equal(t, 1, tracker.count(tabs[tc.idleCreation].targetID))

			m.mu.Lock()
			se, ok := m.sessions[testSessionID]
			m.mu.Unlock()
			require.True(t, ok)
			require.True(t, se.activeIdx >= 0 && se.activeIdx < len(se.tabs),
				"activeIdx must stay in bounds after a sibling is reaped")
			assert.Equal(t, wantTargetID, se.tabs[se.activeIdx].targetID,
				"activeIdx must keep pointing at the SAME tab identity after %s", tc.name)
		})
	}
}

// TestReapIdleSessions_ActiveTabItselfReaped_ActiveIdxStaysCoherent covers
// the one case with no single correct answer: the active tab is itself the
// one reaped. PINNED: only that activeIdx lands in-bounds (0 <= activeIdx <
// len(tabs)), non-negative, and does not alias the tab that was just
// removed — NOT which specific surviving sibling becomes active. If a
// future spec fixes a "which neighbor becomes active" rule, tighten this.
func TestReapIdleSessions_ActiveTabItselfReaped_ActiveIdxStaysCoherent(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	tabs := openTabs(t, m, testSessionID, 3)
	base := *clock
	setTabActivity(m, tabs[0], base)
	setTabActivity(m, tabs[1], base) // this one goes idle AND is active
	setTabActivity(m, tabs[2], base)

	_, err := m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	*clock = clock.Add(6 * time.Minute)
	setTabActivity(m, tabs[0], *clock) // keep both siblings fresh
	setTabActivity(m, tabs[2], *clock)

	reaped := m.ReapIdleSessions()
	assert.Empty(t, reaped, "two fresh siblings remain — the session survives")
	assert.Equal(t, 1, tracker.count(tabs[1].targetID), "the idle active tab must still be closed")

	m.mu.Lock()
	se, ok := m.sessions[testSessionID]
	m.mu.Unlock()
	require.True(t, ok)
	require.Len(t, se.tabs, 2)
	require.True(t, se.activeIdx >= 0 && se.activeIdx < len(se.tabs),
		"activeIdx must never go negative or past the end when the active tab itself is the one reaped")
	assert.NotEqual(t, tabs[1].targetID, se.tabs[se.activeIdx].targetID,
		"activeIdx must not resolve to the tab that was just reaped")
}

// --- empty / degenerate inputs -----------------------------------------------

// TestReapIdleSessions_EmptyManager_ReturnsEmptyNoPanic — sweeping a manager
// with zero sessions must be a safe, empty no-op.
func TestReapIdleSessions_EmptyManager_ReturnsEmptyNoPanic(t *testing.T) {
	tracker := newCancelTracker()
	m, _ := newEdgeManager(t, 5*time.Minute, tracker)

	var reaped []string
	assert.NotPanics(t, func() { reaped = m.ReapIdleSessions() })
	assert.Empty(t, reaped)
}

// TestReapIdleSessions_ZeroTabSession_DoesNotPanic exercises a defensive,
// off-the-happy-path shape: a sessionEntry that already has zero tabs before
// the sweep even starts (never produced by the normal Session/OpenTab/
// CloseTab paths, which maintain a never-zero-tabs invariant — see
// sessionEntry.active's doc comment — but reachable via a hand-built entry,
// exactly like live_deadlock_test.go and inspect_test.go already do
// elsewhere in this package). The contract does not pin what happens to a
// session that is ALREADY empty going in — only that the sweep must not
// panic or corrupt bookkeeping for other, healthy sessions.
func TestReapIdleSessions_ZeroTabSession_DoesNotPanic(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	healthyTabs := openTabs(t, m, "healthy", 1)
	setTabActivity(m, healthyTabs[0], *clock) // fresh — must survive regardless

	zeroBrowserCtx, zeroBrowserCancel := context.WithCancel(context.Background())
	t.Cleanup(zeroBrowserCancel)
	m.mu.Lock()
	m.sessions["zero-tabs"] = &sessionEntry{
		tabs:          nil,
		activeIdx:     0,
		browserCtx:    zeroBrowserCtx,
		browserCancel: zeroBrowserCancel,
	}
	m.mu.Unlock()

	require.NotPanics(t, func() {
		m.ReapIdleSessions()
	})

	m.mu.Lock()
	_, healthyStillThere := m.sessions["healthy"]
	m.mu.Unlock()
	assert.True(t, healthyStillThere, "a degenerate zero-tab session must not corrupt reaping for other sessions")
}

// --- idempotence ---------------------------------------------------------------

// TestReapIdleSessions_Idempotent_SecondSweepIsNoOp — two sweeps back to
// back with no elapsed time between them: the second must find nothing left
// to do and must not double-cancel the already-closed tab.
func TestReapIdleSessions_Idempotent_SecondSweepIsNoOp(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	tabs := openTabs(t, m, testSessionID, 1)
	setTabActivity(m, tabs[0], *clock)
	*clock = clock.Add(6 * time.Minute)

	first := m.ReapIdleSessions()
	require.Equal(t, []string{testSessionID}, first)

	second := m.ReapIdleSessions()
	assert.Empty(t, second, "a second sweep with nothing left to judge must be a pure no-op")
	assert.Equal(t, 1, tracker.count(tabs[0].targetID),
		"the tab's cancel must fire exactly once across both sweeps, not once per sweep")
}

// TestReapIdleSessions_Idempotent_SurvivingTabsUnaffectedBySecondSweep —
// same idea, but for the partial-survivor path: a fresh tab that survived
// sweep one cannot have aged into the TTL by sweep two if the clock never
// moved in between.
func TestReapIdleSessions_Idempotent_SurvivingTabsUnaffectedBySecondSweep(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	tabs := openTabs(t, m, testSessionID, 2)
	setTabActivity(m, tabs[0], *clock)
	*clock = clock.Add(6 * time.Minute)
	setTabActivity(m, tabs[1], *clock) // fresh exactly at sweep time

	first := m.ReapIdleSessions()
	assert.Empty(t, first, "session survives on its fresh tab")

	second := m.ReapIdleSessions()
	assert.Empty(t, second, "with no elapsed time the surviving tab cannot have aged into TTL")
	assert.Equal(t, 1, tracker.count(tabs[0].targetID))
	assert.Zero(t, tracker.count(tabs[1].targetID))
}

// --- externally-dead tab context ---------------------------------------------

// TestReapIdleSessions_ExternallyDeadContext_StillSweptWithoutPanic models a
// tab whose underlying chromedp CONTEXT died externally — a browser crash, a
// target destroyed out from under us — while tabEntry.cancel itself has
// NEVER been invoked. That is the real-world "dead tab" shape production
// code already anticipates (Session()'s own crash-recovery check reads
// tab.ctx.Err()).
//
// This is deliberately NOT "call tabEntry.cancel() twice": that state is
// impossible in production (a tab is only ever removed from se.tabs in the
// SAME m.mu critical section as its own single cancel call, so no code path
// invokes one tabEntry's cancel more than once) and, worse, isn't even safe
// to model directly — chromedp.NewContext's returned CancelFunc is NOT a
// plain, idempotent stdlib context.CancelFunc despite superficially looking
// like one: on a "first" context (chromedp.go:122-220) its second
// invocation blocks forever on `<-c.allocated` (chromedp.go:217), because
// the buffered allocation token was already drained by the first call and
// nothing ever refills it outside a real chromedp.Run(). A first draft of
// this test called the wrapper twice and hung for the full test timeout —
// confirmed by reproducing it and reading the chromedp source, not assumed.
//
// The fix: give this tab's context an externally-cancelable PARENT and cancel
// THAT — which kills tab.ctx via ordinary context-tree propagation without
// ever touching tabEntry.cancel — then let the reaper invoke tabEntry.cancel
// for the first and only time.
func TestReapIdleSessions_ExternallyDeadContext_StillSweptWithoutPanic(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	deadParentCtx, killUnderlyingContext := context.WithCancel(context.Background())
	t.Cleanup(killUnderlyingContext)
	m.createTabFn = func(_ context.Context, _ target.ID) (*tabEntry, error) {
		ctx, cancel := chromedp.NewContext(deadParentCtx)
		id := target.ID("dead-target")
		return &tabEntry{
			ctx: ctx,
			cancel: func() {
				tracker.mu.Lock()
				tracker.counts[id]++
				tracker.mu.Unlock()
				cancel()
			},
			targetID: id,
		}, nil
	}

	tabs := openTabs(t, m, testSessionID, 1)
	require.NoError(t, tabs[0].ctx.Err(), "sanity: the context starts alive")

	killUnderlyingContext() // simulate an external death — never touches tabEntry.cancel
	require.Error(t, tabs[0].ctx.Err(), "sanity: the underlying context is now dead")
	assert.Zero(
		t,
		tracker.count(tabs[0].targetID),
		"the external death must not itself invoke the tab's cancel wrapper",
	)

	setTabActivity(m, tabs[0], *clock)
	*clock = clock.Add(6 * time.Minute)

	var reaped []string
	require.NotPanics(t, func() { reaped = m.ReapIdleSessions() })
	assert.Equal(t, []string{testSessionID}, reaped,
		"an externally-dead but still-idle tab must still be swept and its session closed")
	assert.Equal(t, 1, tracker.count(tabs[0].targetID),
		"the reaper must still call the tab's own cancel exactly once to release its resources")
}

// --- rule 3: browserCancel on full closure -----------------------------------

// TestReapIdleSessions_FullClosure_CallsBrowserCancel proves the actual
// browserCtx is canceled (not merely the tab's own ctx) when a session's
// last tab is reaped — a live browserCtx after full closure means the
// browsing context (and its resident Chrome) leaked, exactly the bug this
// reaper exists to fix.
func TestReapIdleSessions_FullClosure_CallsBrowserCancel(t *testing.T) {
	tracker := newCancelTracker()
	m, clock := newEdgeManager(t, 5*time.Minute, tracker)

	tabs := openTabs(t, m, testSessionID, 1)
	setTabActivity(m, tabs[0], *clock)

	m.mu.Lock()
	browserCtx := m.sessions[testSessionID].browserCtx
	m.mu.Unlock()
	require.NoError(t, browserCtx.Err(), "sanity: browsing context must start out alive")

	*clock = clock.Add(6 * time.Minute)
	reaped := m.ReapIdleSessions()
	require.Equal(t, []string{testSessionID}, reaped)

	assert.Error(t, browserCtx.Err(),
		"rule 3: emptying a session's last tab must call browserCancel")
}
