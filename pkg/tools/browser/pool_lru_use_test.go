package browser

// pool_lru_use_test.go — "least recently used" has to mean USED. Both pool
// lifetime controls (LRU eviction and idle close) read chromeInstance.lastUsed,
// and it was a LAUNCH time wearing a use time's name.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Defect 1: "least recently used" has to mean USED -----------------------

// busyManager returns a manager whose browsing context holds one tab last
// touched at `when`.
//
// That stamp is what a real browser_* tool call leaves behind: every one of
// them resolves its active tab through BrowserManager.Session(), which calls
// touchTabLocked. It is the only DURABLE evidence of use the pool can read
// after the fact.
func busyManager(when time.Time) *BrowserManager {
	return &BrowserManager{
		sessions: map[string]*sessionEntry{
			"s": {tabs: []*tabEntry{{lastActivity: when}}},
		},
	}
}

// TestPool_EvictionSparesTheBusyOldBrowserAndTakesTheIdleNewOne is defect 1.
//
// chromeInstance.lastUsed was stamped when a browser was STARTED and never
// again, because a manager reaches the pool only on its first tool call —
// BrowserManager.ensureStarted returns early once m.started is set, so every
// later navigate, click and screenshot goes straight to the coordinator and
// the pool never hears about it. "Least recently used" therefore meant
// "oldest STARTED", and eviction closed the workspace that had been working
// all morning in favour of one opened later and barely touched: every open
// tab, half-filled form and scroll position gone, with no error and no way for
// the agent to tell anything had happened.
//
// THE TRAP, named because the brief for this unit walked into it: do NOT build
// this test on the in-flight counter. An in-flight call already makes an
// instance pinned(), so it is skipped by evictableLocked on the BROKEN build
// too — a test written that way passes without the fix and guards nothing.
// The busy browser here is deliberately NOT in a call. It is a browser that
// was used a minute ago, which is the state a real one spends almost all of
// its time in.
//
// Mutation contract: make evictableLocked compare inst.lastUsed again instead
// of observedLastUse and this test goes red — it picks "busy" as the victim.
func TestPool_EvictionSparesTheBusyOldBrowserAndTakesTheIdleNewOne(t *testing.T) {
	f := newPoolFixture(t)

	// 09:00 — the workspace that will be working all morning starts up.
	busy := f.mustAcquire(t, "busy")

	// 11:00 — a second workspace is opened. It is the NEWER browser.
	f.advance(2 * time.Hour)
	idle := f.mustAcquire(t, "idle")
	idleMgr := busyManager(*f.now) // touched once, when it was opened
	busyMgr := busyManager(*f.now) // busy has been in use all along, too

	f.pool.mu.Lock()
	busy.mgrs[busyMgr] = struct{}{}
	idle.mgrs[idleMgr] = struct{}{}
	f.pool.mu.Unlock()

	// 11:59 — "busy" has kept working; "idle" has not been touched since it
	// was opened. Neither is in a call right now.
	f.advance(59 * time.Minute)
	busyMgr.mu.Lock()
	busyMgr.sessions["s"].tabs[0].lastActivity = *f.now
	busyMgr.mu.Unlock()

	require.Zero(t, busyMgr.InFlight(), "the busy browser must NOT be mid-call — an in-flight "+
		"call pins it on the broken build too, which would make this test hollow")
	require.Zero(t, idleMgr.InFlight())

	f.pool.mu.Lock()
	victim := f.pool.evictableLocked()
	f.pool.mu.Unlock()

	require.NotNil(t, victim, "something must be evictable — neither browser is watched or busy")
	assert.Equal(t, "ws:idle", victim.key.String(),
		"eviction picked the browser that STARTED first rather than the one used least "+
			"recently. 'busy' was used a minute ago and 'idle' an hour ago; closing 'busy' "+
			"destroys every open tab, half-filled form and scroll position in the workspace "+
			"somebody is actually working in, silently")
}

// TestPool_IdleCloseMeasuresFromLastUseNotFromLaunch is the same stale stamp
// seen through the other control it feeds.
//
// tools.browser.idle_close_ttl says a browser may sit with nothing to do for
// that long before it is closed. Measured from the LAUNCH it means something
// else entirely, and always something shorter: a browser used continuously for
// half an hour was eligible for closure the instant its last tab was reaped,
// because its clock had been running since it started.
//
// The sweep is the gateway's existing one-minute pass, so a browser in use is
// observed many times while its tabs are still open — which is where the
// evidence of use is read.
//
// Mutation contract: drop the observedLastUse stamp from CloseIdle and the
// first assertion goes red (the browser is closed 10 minutes after its last
// use, on a 15-minute TTL).
func TestPool_IdleCloseMeasuresFromLastUseNotFromLaunch(t *testing.T) {
	f := newPoolFixture(t)
	ttl := f.pool.idleCloseTTL
	require.Equal(t, 15*time.Minute, ttl, "this test's arithmetic is written against the default")

	inst := f.mustAcquire(t, "alpha")
	mgr := busyManager(*f.now)
	f.pool.mu.Lock()
	inst.mgrs[mgr] = struct{}{}
	f.pool.mu.Unlock()

	// The workspace is used for half an hour. The gateway sweeps every minute
	// throughout; nothing may be closed while a tab is open.
	for i := 0; i < 30; i++ {
		f.advance(time.Minute)
		mgr.mu.Lock()
		mgr.sessions["s"].tabs[0].lastActivity = *f.now
		mgr.mu.Unlock()
		require.Empty(t, f.pool.CloseIdle(*f.now),
			"a browser with an open tab has something to do and must never be closed")
	}
	lastUse := *f.now

	// The per-tab reaper takes the idle tab. The browser now has nothing to do
	// — but it was used 0 minutes ago, not 30.
	mgr.mu.Lock()
	mgr.sessions["s"].tabs = nil
	mgr.mu.Unlock()
	require.Zero(t, mgr.TotalOpenTabs())

	// 10 minutes after the last use, and 40 after launch. On a 15-minute TTL
	// this browser is NOT due.
	f.advance(10 * time.Minute)
	assert.Empty(t, f.pool.CloseIdle(*f.now),
		"this browser was used %v ago on a %v TTL, but idle close measured from its LAUNCH "+
			"(%v ago) and killed it early — the setting does not mean what its own description "+
			"says", f.now.Sub(lastUse), ttl, f.now.Sub(lastUse)+30*time.Minute)
	assert.Equal(t, []string{"ws:alpha"}, f.pool.LiveKeys())

	// 16 minutes after the last use, it IS due. Without this half, a build
	// that simply never closed anything would pass the assertion above.
	f.advance(6 * time.Minute)
	assert.Equal(t, []string{"ws:alpha"}, f.pool.CloseIdle(*f.now),
		"past the TTL with no tabs, no viewer and no call in flight, the browser must be closed "+
			"— nothing else reclaims its memory")
	assert.Empty(t, f.pool.LiveKeys())
}
