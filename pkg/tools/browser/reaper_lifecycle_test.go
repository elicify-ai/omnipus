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
