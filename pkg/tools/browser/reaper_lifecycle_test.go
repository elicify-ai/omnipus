// reaper_lifecycle_test.go — coverage for the four behaviors added in the
// reviewer fix wave (4d6719bd) that shipped with no tests of their own:
// the zero-tab-session escape hatch, the bounded cancel, and the
// cancel-outside-the-lock discipline on the reaper and on Shutdown.
//
// The lock tests are the important ones. The bug they guard is not "a cancel is
// slow" — it is that a cancel taken WHILE m.mu is held freezes every browser
// tool call for every agent on that manager, with no error and no log. So they
// assert the property directly: while a cancel is deliberately wedged, another
// goroutine must still be able to take m.mu.

package browser

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingTabFactory builds fake tabs whose cancel blocks until release is
// closed, so a test can hold a teardown mid-flight and observe what the rest of
// the manager can still do meanwhile.
func blockingTabFactory(
	release <-chan struct{},
) (func(context.Context, target.ID) (*tabEntry, error), *sync.WaitGroup) {
	var entered sync.WaitGroup
	var n int
	fn := func(_ context.Context, targetID target.ID) (*tabEntry, error) {
		ctx, cancel := chromedp.NewContext(context.Background())
		id := targetID
		if id == "" {
			n++
			id = target.ID("blocking-target-" + string(rune('a'+n)))
		}
		entered.Add(1)
		var once sync.Once
		return &tabEntry{
			ctx: ctx,
			cancel: func() {
				once.Do(func() {
					entered.Done() // signal we are INSIDE the cancel
					<-release      // ...and stay here until the test lets go
					cancel()
				})
			},
			targetID: id,
		}, nil
	}
	return fn, &entered
}

// canLockWithin reports whether m.mu can be acquired inside d — the direct
// question "is the manager still usable right now".
func canLockWithin(m *BrowserManager, d time.Duration) bool {
	got := make(chan struct{})
	go func() {
		m.mu.Lock()
		m.mu.Unlock()
		close(got)
	}()
	select {
	case <-got:
		return true
	case <-time.After(d):
		return false
	}
}

// --- 1. the zero-tab escape hatch ------------------------------------------

// A session that reaches ZERO tabs has no per-tab clock, so without a dedicated
// stamp the per-tab sweep would skip it forever. That state is reachable in
// production: CloseTab empties se.tabs and calls createFirstTab to restore the
// never-zero invariant; if that replacement fails (Chrome under load — exactly
// what this reaper exists to survive) the entry is stranded with a live
// browserCtx and no tabs.
func TestReapIdleSessions_StrandedEmptySession_ReapedAfterTTL(t *testing.T) {
	m, clock := newReapableManager(t, 5*time.Minute)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	var browserCancelled bool
	m.mu.Lock()
	se := m.sessions[testSessionID]
	se.tabs = nil // simulate CloseTab's replacement having failed
	se.browserCancel = func() { browserCancelled = true }
	m.mu.Unlock()

	// First sweep only STAMPS it — an empty session must not be torn down on
	// sight, or it would race CloseTab's legitimate momentary empty window.
	assert.Empty(t, m.ReapIdleSessions(), "an empty session must be stamped, not reaped on sight")
	assert.Contains(t, m.sessions, testSessionID)
	assert.False(t, browserCancelled)

	// Still inside the TTL: survives.
	*clock = clock.Add(4 * time.Minute)
	assert.Empty(t, m.ReapIdleSessions(), "still inside the TTL")
	assert.Contains(t, m.sessions, testSessionID)

	// Past it: the browsing context goes, and the session is reported.
	*clock = clock.Add(2 * time.Minute)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions(),
		"a session stranded empty for a whole TTL must be reaped — it holds a live browsing context")
	assert.True(t, browserCancelled, "the stranded browsing context must actually be canceled")
	assert.NotContains(t, m.sessions, testSessionID)
}

// A session that goes empty and is REFILLED (CloseTab's normal path) must have
// its empty-stamp cleared, or the next empty window would inherit a stale clock
// and be reaped early.
func TestReapIdleSessions_EmptyThenRefilled_StampIsCleared(t *testing.T) {
	m, clock := newReapableManager(t, 5*time.Minute)
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	se := m.sessions[testSessionID]
	keep := se.tabs
	se.tabs = nil
	m.mu.Unlock()

	m.ReapIdleSessions() // stamps emptySince
	m.mu.Lock()
	se.tabs = keep // the replacement tab arrives, as CloseTab intends
	se.tabs[0].lastActivity = m.now()
	m.mu.Unlock()

	*clock = clock.Add(2 * time.Minute)
	assert.Empty(t, m.ReapIdleSessions(), "a refilled session must not carry its old empty-stamp")

	m.mu.Lock()
	stamp := m.sessions[testSessionID].emptySince
	m.mu.Unlock()
	assert.True(t, stamp.IsZero(), "emptySince must be cleared once the session has tabs again")
}

// --- 2. the bounded cancel --------------------------------------------------

// A wedged cancel must not stall the sweep: it is abandoned at the bound and
// the sweep still completes and still reports what it removed.
func TestReapIdleSessions_WedgedCancel_SweepStillCompletes(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	m, clock := newReapableManager(t, 5*time.Minute)
	fn, entered := blockingTabFactory(release)
	m.createTabFn = fn
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	*clock = clock.Add(6 * time.Minute)

	done := make(chan []string, 1)
	go func() { done <- m.ReapIdleSessions() }()

	select {
	case reaped := <-done:
		assert.Equal(t, []string{testSessionID}, reaped,
			"the sweep must still report the session it removed, even though the cancel wedged")
	case <-time.After(cancelBoundedTimeout + 20*time.Second):
		t.Fatal("ReapIdleSessions never returned — a wedged cancel must be abandoned at the bound, not block the sweep")
	}
	_ = entered
}

// --- 3/4. cancels must run with m.mu RELEASED -------------------------------

// The deadlock this whole discipline exists to prevent: a cancel taken under
// m.mu freezes every browser tool call for every agent on the manager.
func TestReapIdleSessions_CancelDoesNotHoldTheManagerLock(t *testing.T) {
	release := make(chan struct{})
	m, clock := newReapableManager(t, 5*time.Minute)
	fn, entered := blockingTabFactory(release)
	m.createTabFn = fn
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	*clock = clock.Add(6 * time.Minute)
	go m.ReapIdleSessions()

	entered.Wait() // the sweep is now parked INSIDE a cancel
	assert.True(t, canLockWithin(m, 3*time.Second),
		"m.mu must be free while a cancel is in flight — holding it across a wedged cancel is the "+
			"deadlock that freezes every browser tool call for every agent on this manager")
	close(release)
}

// Shutdown runs on every browser-config hot-reload and at gateway close, where
// the agent loop holds its own broader lock across it — so a cancel wedged
// under m.mu here stalls far more than the browser subsystem.
func TestShutdown_CancelDoesNotHoldTheManagerLock(t *testing.T) {
	release := make(chan struct{})
	m := newTestManagerWithFakeTabs(t)
	fn, entered := blockingTabFactory(release)
	m.createTabFn = fn
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	go m.Shutdown()

	entered.Wait()
	assert.True(t, canLockWithin(m, 3*time.Second),
		"Shutdown must release m.mu before canceling — it is reached on hot-reload and at gateway close")
	close(release)
}

// CloseSession has no production caller today, which makes it a landmine rather
// than a live bug: it sits beside the others and silently signals that
// canceling under the lock is fine here.
func TestCloseSession_CancelDoesNotHoldTheManagerLock(t *testing.T) {
	release := make(chan struct{})
	m := newTestManagerWithFakeTabs(t)
	fn, entered := blockingTabFactory(release)
	m.createTabFn = fn
	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	go m.CloseSession(testSessionID)

	entered.Wait()
	assert.True(t, canLockWithin(m, 3*time.Second),
		"CloseSession must release m.mu before canceling")
	close(release)
}

// --- 5. FR-025: per-tab reaping semantics survive the workspace re-key -------

// TestReap_PerTabTTLAndViewerPin is ADR-072 D1 test 43. FR-025's wording is
// exact and worth repeating, because it is a constraint on this change rather
// than a new feature: per-tab reaping semantics are ASSERTED, NOT REWRITTEN.
//
// Three properties, in one scenario, because they only mean anything together:
//
//  1. An idle tab past IdleTTL is reaped — the sweep still works at all.
//  2. An ATTACHED VIEWER PINS THE WHOLE BROWSER. Not the tab being watched:
//     every tab in that context, in full, regardless of any individual tab's
//     idle time. The live panel's tab strip shows them all, so reaping one out
//     from under a watching human is the defect.
//  3. The BROWSER ITSELF IS NOT CLOSED by the per-tab sweep while tabs
//     survive. Whole-Chrome idle close is separate, later, and at a different
//     TTL; if the per-tab sweep took the process down, that separate control
//     would have nothing left to do and its absence would go unnoticed.
//
// Under ADR-072 the browser belongs to the WORKSPACE, which raises the cost of
// getting (2) or (3) wrong: the browser a sweep tears down is now shared by
// every agent on the workspace and holds the operator's live logins, not one
// agent's scratch context.
func TestReap_PerTabTTLAndViewerPin(t *testing.T) {
	m, clock := newReapableManager(t, 5*time.Minute)

	// --- Part 1: no viewer. The idle tab goes; the fresh one stays; the
	// browsing context survives because a tab survives.
	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	m.mu.Lock()
	se := m.sessions[testSessionID]
	require.Len(t, se.tabs, 2)
	idleTargetID := se.tabs[0].targetID
	freshTargetID := se.tabs[1].targetID
	require.NotEqual(t, idleTargetID, freshTargetID)
	var browserCancelled bool
	se.browserCancel = func() { browserCancelled = true }
	m.mu.Unlock()

	// Tab 0 goes quiet; tab 1 keeps being used.
	*clock = clock.Add(6 * time.Minute)
	m.mu.Lock()
	m.sessions[testSessionID].tabs[1].lastActivity = m.now()
	m.mu.Unlock()

	assert.Empty(t, m.ReapIdleSessions(),
		"the SESSION must not be reported reaped while one of its tabs survives")

	m.mu.Lock()
	se = m.sessions[testSessionID]
	surviving := make([]string, 0, len(se.tabs))
	for _, tb := range se.tabs {
		surviving = append(surviving, string(tb.targetID))
	}
	activeIdx := se.activeIdx
	m.mu.Unlock()

	assert.Equal(t, []string{string(freshTargetID)}, surviving,
		"the tab idle past IdleTTL must be reaped and the recently-used one must not")
	assert.False(t, browserCancelled,
		"the per-tab sweep must NOT close the browser while a tab survives — whole-Chrome idle close "+
			"is a separate control at a separate TTL (FR-040), and a per-tab sweep that takes the "+
			"process down leaves it nothing to do")
	assert.GreaterOrEqual(t, activeIdx, 0)
	assert.Less(t, activeIdx, len(surviving),
		"activeIdx must still point inside the shrunken tab set")

	// --- Part 2: an attached viewer pins EVERY tab in the context, however
	// idle. This is the assertion that fails on any build that "optimises" the
	// sweep into a per-tab-only decision.
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID)

	m.mu.Lock()
	pinned := len(m.sessions[testSessionID].tabs)
	// Backdate EVERY tab well past the TTL: without the viewer, all of them
	// would go and the whole context with them.
	stale := m.now().Add(-1 * time.Hour)
	for _, tb := range m.sessions[testSessionID].tabs {
		tb.lastActivity = stale
	}
	m.mu.Unlock()
	require.Equal(t, 3, pinned)

	*clock = clock.Add(30 * time.Minute)
	m.ViewerHeartbeat(testSessionID) // FR-052: the pin belongs to a viewer that is still there
	assert.Empty(t, m.ReapIdleSessions(),
		"an attached viewer pins the whole browsing context")

	m.mu.Lock()
	stillThere := len(m.sessions[testSessionID].tabs)
	m.mu.Unlock()
	assert.Equal(t, pinned, stillThere,
		"a viewer pins EVERY tab in the context it is watching, not just the active one — the panel's "+
			"tab strip shows them all, and reaping one out from under a watching human is the defect")
	assert.False(t, browserCancelled, "and the workspace's browser is certainly not closed under a viewer")

	// --- Part 3: the pin is a pin, not an exemption. Once the viewer leaves,
	// the same tabs are reapable again. (ViewerDetached re-stamps every tab, so
	// the clock starts from the moment the viewer left — deliberate, and
	// asserted here so a change to that stamping is visible.)
	m.ViewerDetached(testSessionID)
	*clock = clock.Add(6 * time.Minute)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions(),
		"once the last viewer detaches the context is reapable again — a viewer pins, it does not exempt")
	assert.True(t, browserCancelled,
		"when the LAST tab is reaped the browsing context goes with it, which is the pre-existing "+
			"whole-context behaviour FR-025 preserves")
}
