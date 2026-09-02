package browser

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// lease_test.go — §14's write lease, the primitive.
//
// The lease exists because two TURNS — not two agents — can address one tab
// set: /loop, async system-notify (#505) and cron SessionModeMain each start a
// second turn on an already-live session id. Two turns interleaving CDP
// commands on one page is what ADR-072 §5 calls the most expensive failure
// class in this design, because the page ends up in a state neither turn asked
// for and neither can tell.

// fakeLeaseClock drives the bounded wait without sleeping. Now() advances only
// when After() is consumed, so a test controls exactly how much of the budget
// has elapsed.
type fakeLeaseClock struct {
	mu    sync.Mutex
	now   time.Time
	waits int
}

func newFakeLeaseClock() *fakeLeaseClock {
	return &fakeLeaseClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeLeaseClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeLeaseClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.waits++
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

func (c *fakeLeaseClock) waitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waits
}

// installFakeLeaseClock swaps the package clock for the duration of a test.
func installFakeLeaseClock(t *testing.T) *fakeLeaseClock {
	t.Helper()
	prev := leaseClockImpl
	fake := newFakeLeaseClock()
	leaseClockImpl = fake
	t.Cleanup(func() { leaseClockImpl = prev })
	return fake
}

// leaseTestManager builds a manager whose only exercised machinery is the lease
// table. No Chromium, no CDP.
func leaseTestManager(t *testing.T) *BrowserManager {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.cfg.LeaseWait = 50 * time.Millisecond
	return m
}

// TestWriteLease_OneWriterOnSharedTab: the lease is mutual exclusion. While one
// writer holds it, a second cannot also hold it.
func TestWriteLease_OneWriterOnSharedTab(t *testing.T) {
	installFakeLeaseClock(t)
	m := leaseTestManager(t)

	release, ok, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "jim")
	require.True(t, ok)
	require.NotNil(t, release)

	_, ok2, holder := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "mia")
	require.False(t, ok2, "a second writer must NOT hold the lease at the same time")
	require.Equal(t, "jim", holder, "the deferral must name who is holding it")

	release()
	_, ok3, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "mia")
	require.True(t, ok3, "releasing must hand the lease on")
}

// TestWriteLease_BothWritersEventuallyComplete is FR-020's real contract, and
// it is deliberately NOT "neither errored".
//
// The loser RETRIES inside the tool and BOTH writers complete. A test asserting
// only that neither returned an error would pass on a build where the second
// writer did nothing at all — which is the silent no-op this whole mechanism
// exists to prevent — so this asserts that both bodies of work RAN.
func TestWriteLease_BothWritersEventuallyComplete(t *testing.T) {
	m := leaseTestManager(t)
	m.cfg.LeaseWait = 2 * time.Second // real clock; the hold below is short

	var completed int64
	var order []string
	var orderMu sync.Mutex

	work := func(agent string, hold time.Duration) {
		release, ok, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), agent)
		if !ok {
			return
		}
		defer release()
		orderMu.Lock()
		order = append(order, agent)
		orderMu.Unlock()
		time.Sleep(hold)
		atomic.AddInt64(&completed, 1)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); work("jim", 30*time.Millisecond) }()
	go func() { defer wg.Done(); work("mia", 30*time.Millisecond) }()
	wg.Wait()

	require.Equal(t, int64(2), atomic.LoadInt64(&completed),
		"BOTH writers must complete their work under ordinary contention — a deferral is the "+
			"outcome PAST the bound, not the normal one")
	orderMu.Lock()
	defer orderMu.Unlock()
	require.Len(t, order, 2, "both writers must have entered the critical section, one after the other")
}

// TestWriteLease_LoserDefersPastBound: once the retry budget is exhausted the
// loser returns a NON-error deferral naming the holder — never a Go error, and
// never a silent success.
func TestWriteLease_LoserDefersPastBound(t *testing.T) {
	installFakeLeaseClock(t)
	m := leaseTestManager(t)

	release, ok, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "jim")
	require.True(t, ok)
	defer release()

	deferred, rel := leaseWrite(context.Background(), m, testKey, TabOwnerWorkspace(), "mia", "browser_click")
	require.NotNil(t, deferred, "past the bound the loser must defer")
	require.NotNil(t, rel, "release must be non-nil even on the deferred path — a caller defers before checking")
	require.NotPanics(t, rel, "the deferred path's release must be a safe no-op")
	require.False(t, deferred.IsError, "a deferral is coordination, not a tool failure")

	var body struct {
		Deferred bool   `json:"deferred"`
		Reason   string `json:"reason"`
	}
	payload := deferred.ForLLM[len("browser_click: "):]
	require.NoError(t, json.Unmarshal([]byte(payload), &body))
	require.True(t, body.Deferred, `the body must carry a machine-checkable "deferred": true`)
	require.Contains(t, body.Reason, "jim", "the reason must name the holder")
	require.Contains(t, body.Reason, "retry",
		"a writer-vs-writer deferral means RETRY; it must not read like the human-control one, which means stop")
	require.NotContains(t, body.Reason, "human is currently controlling",
		"the two deferral reasons mean opposite things and must be distinguishable")
}

// TestWriteLease_ReadOnlyToolsUngated is FR-021's surviving half. browser_list_tabs
// is read-only and must NEVER take the lease: leasing it makes the headline demo
// — "which tabs are open?" — return a deferral behind another turn's long
// navigate (round-2 CRIT-104).
func TestWriteLease_ReadOnlyToolsUngated(t *testing.T) {
	m := leaseTestManager(t)

	// Somebody else holds the lease for the whole test.
	release, ok, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "jim")
	require.True(t, ok)
	defer release()

	res := &fixedResolver{mgr: m, key: testKey, owner: TabOwnerWorkspace()}
	tool := &ListTabsTool{res: res}
	result := tool.Execute(context.Background(), map[string]any{})
	require.NotNil(t, result)
	require.False(t, result.IsError)
	require.NotContains(t, result.ForLLM, `"deferred"`,
		"browser_list_tabs must answer while another turn holds the write lease")
}

// TestWriteLease_HumanControlTakesPrecedence is §14.2 rule 1's ordering, and
// the ordering is load-bearing rather than cosmetic: the control lock is the
// ONLY thing standing between "an agent drove the tab I had finished with" and
// "an agent drove the tab I am using right now". If leaseWrite ran first, the
// lease would serialise two agents perfectly while a human sat locked out of
// their own tab — and every lease test would stay green.
func TestWriteLease_HumanControlTakesPrecedence(t *testing.T) {
	m := leaseTestManager(t)

	// A human holds the wheel AND another turn holds the write lease. The
	// answer must be the human-control one.
	owner := TabOwnerWorkspace()
	require.True(t, m.Live().TakeControl(sessionKey(testKey, owner), "human-viewer"))
	release, ok, _ := m.acquireWrite(context.Background(), testKey, owner, "jim")
	require.True(t, ok)
	defer release()

	res := &fixedResolver{mgr: m, key: testKey, owner: owner}
	tool := &SwitchTabTool{res: res}
	result := tool.Execute(context.Background(), map[string]any{"index": float64(0)})
	require.NotNil(t, result)
	require.Contains(t, result.ForLLM, "human is currently controlling",
		"controlledResult is evaluated BEFORE the lease; a lease-contention message here means the "+
			"gates were reordered and the human-control mitigation has quietly moved behind the lease")
}

// TestWriteLease_ReleasedOnPanicAndCancel is FR-024. A panic, a CDP timeout or a
// cancelled context must not wedge the browser: release runs via defer, and it
// is idempotent, so a double release cannot hand the tab to a writer that never
// acquired.
func TestWriteLease_ReleasedOnPanicAndCancel(t *testing.T) {
	m := leaseTestManager(t)
	owner := TabOwnerWorkspace()
	lk := sessionKey(testKey, owner)

	t.Run("a panic under the lease still releases", func(t *testing.T) {
		func() {
			defer func() { _ = recover() }()
			release, ok, _ := m.acquireWrite(context.Background(), testKey, owner, "jim")
			require.True(t, ok)
			defer release()
			panic("a CDP driver blew up mid-call")
		}()
		require.False(t, m.writeLeases().isHeld(lk), "the lease must be released when the holder panics")
	})

	t.Run("release is idempotent", func(t *testing.T) {
		release, ok, _ := m.acquireWrite(context.Background(), testKey, owner, "jim")
		require.True(t, ok)
		release()
		require.NotPanics(t, release)

		// A second writer now holds it; the first writer's stale release must
		// NOT take it away from them.
		release2, ok, _ := m.acquireWrite(context.Background(), testKey, owner, "mia")
		require.True(t, ok)
		release()
		require.True(t, m.writeLeases().isHeld(lk),
			"a stale double-release must not hand the tab to a writer that never acquired")
		release2()
	})

	t.Run("a cancelled turn parks no goroutine", func(t *testing.T) {
		release, ok, _ := m.acquireWrite(context.Background(), testKey, owner, "jim")
		require.True(t, ok)
		defer release()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan struct{})
		go func() {
			_, ok, _ := m.acquireWrite(ctx, testKey, owner, "mia")
			require.False(t, ok)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("a cancelled context must abandon the wait immediately, not park a goroutine")
		}
	})
}

// TestWriteLease_BoundedWait is FR-023: the wait is BOUNDED and the bound is the
// configured, clamped lease_wait — not a hardcoded constant and not forever.
func TestWriteLease_BoundedWait(t *testing.T) {
	clock := installFakeLeaseClock(t)
	m := leaseTestManager(t)
	m.cfg.LeaseWait = 500 * time.Millisecond

	release, ok, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "jim")
	require.True(t, ok)
	defer release()

	start := leaseClockImpl.Now()
	_, ok2, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "mia")
	require.False(t, ok2)

	elapsed := leaseClockImpl.Now().Sub(start)
	require.GreaterOrEqual(t, elapsed, m.cfg.LeaseWait,
		"the loser must actually RETRY for the whole budget before deferring — a give-up-immediately "+
			"lease turns ordinary contention into a deferral the model has to handle")
	require.Less(t, elapsed, 2*m.cfg.LeaseWait,
		"the wait must be bounded by lease_wait, not run past it")
	require.Greater(t, clock.waitCount(), 1, "the wait must be a retry loop, not a single sleep")

	// The bound follows the CONFIG, not a constant: halve it and the wait
	// halves too.
	m.cfg.LeaseWait = 100 * time.Millisecond
	start = leaseClockImpl.Now()
	_, ok3, _ := m.acquireWrite(context.Background(), testKey, TabOwnerWorkspace(), "ava")
	require.False(t, ok3)
	shorter := leaseClockImpl.Now().Sub(start)
	require.Less(t, shorter, elapsed,
		"lease_wait must be the live bound; a hardcoded one would give the same elapsed time")
}

// TestWriteLease_DefaultWhenUnconfigured: an unset lease_wait leaves the
// package default in force rather than collapsing to zero, which would make
// every contended call defer instantly.
func TestWriteLease_DefaultWhenUnconfigured(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.cfg.LeaseWait = 0
	require.Equal(t, leaseWaitTimeout, m.leaseWait())
	require.Equal(t, 2*time.Second, leaseWaitTimeout, "FR-023's default is 2s")
}
