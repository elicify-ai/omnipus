package browser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression coverage for the browsing-context lifecycle leak: closing the
// live panel is a pure UI dismiss (the SPA sends no shutdown frame) and
// browser.CloseSession had ZERO production callers, so a browsing context —
// and its resident Chrome — outlived the panel indefinitely. Reopening the
// panel days later showed the exact page the user had left.
//
// The reaper's contract is deliberately conservative: a context is closed only
// when BOTH signals are quiet (no attached viewer AND no agent tool call).
// An agent can legitimately be mid-task in a tab nobody is watching, and
// reaping that out from under it would be strictly worse than the stale tab
// this fixes.

// newReapableManager builds a fake-tab manager with a controllable clock and a
// known idle TTL, so idleness can be aged deterministically without sleeping.
func newReapableManager(t *testing.T, ttl time.Duration) (*BrowserManager, *time.Time) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t, 5)
	m.cfg.IdleTTL = ttl
	clock := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	m.nowFn = func() time.Time { return clock }
	return m, &clock
}

func TestReapIdleSessions_ClosesContextIdleBeyondTTL(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(DefaultSessionID) // stamps lastActivity
	require.NoError(t, err)

	// Not yet idle enough — must survive.
	*clock = clock.Add(29 * time.Minute)
	assert.Empty(t, m.ReapIdleSessions(), "a context inside its TTL must not be reaped")
	assert.Contains(t, m.sessions, DefaultSessionID)

	// Past the TTL with no viewer and no tool call — reap.
	*clock = clock.Add(2 * time.Minute)
	reaped := m.ReapIdleSessions()
	assert.Equal(t, []string{DefaultSessionID}, reaped,
		"a context with no viewer and no agent activity past idle_ttl must be closed; "+
			"without this the browsing context outlives the panel forever")
	assert.NotContains(t, m.sessions, DefaultSessionID, "the reaped session must be removed")
}

// TestReapIdleSessions_NeverReapsWatchedContext — somebody is literally
// watching this context. Elapsed time since the last tool call is irrelevant.
func TestReapIdleSessions_NeverReapsWatchedContext(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	m.ViewerAttached(DefaultSessionID)

	*clock = clock.Add(72 * time.Hour)
	assert.Empty(t, m.ReapIdleSessions(),
		"a context with an attached viewer must never be reaped, however long since the last tool call")
	assert.Contains(t, m.sessions, DefaultSessionID)
}

// TestReapIdleSessions_ReapsAfterLastViewerLeaves — the idle clock starts when
// the last viewer departs, which is exactly the "user closed the panel" case
// this whole feature exists for.
func TestReapIdleSessions_ReapsAfterLastViewerLeaves(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	m.ViewerAttached(DefaultSessionID)
	m.ViewerAttached(DefaultSessionID) // two panels open

	*clock = clock.Add(48 * time.Hour)
	m.ViewerDetached(DefaultSessionID) // one closes — still watched
	assert.Empty(t, m.ReapIdleSessions(), "still one viewer attached")

	m.ViewerDetached(DefaultSessionID) // last one closes; clock restarts here
	assert.Empty(t, m.ReapIdleSessions(), "detach itself is activity — TTL restarts from the departure")

	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{DefaultSessionID}, m.ReapIdleSessions(),
		"once the last viewer has been gone longer than the TTL, the context must be reaped")
}

// TestReapIdleSessions_AgentActivityKeepsContextAlive — the half of the
// contract that protects an agent working in an unwatched tab.
func TestReapIdleSessions_AgentActivityKeepsContextAlive(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	// No viewer ever attaches, but the agent keeps working every 20 minutes.
	for i := 0; i < 5; i++ {
		*clock = clock.Add(20 * time.Minute)
		_, err := m.Session(DefaultSessionID) // a tool call resolving the session
		require.NoError(t, err)
		require.Empty(t, m.ReapIdleSessions(),
			"an agent actively using the browser must never have it reaped out from under it")
	}

	// Work stops; now it ages out.
	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{DefaultSessionID}, m.ReapIdleSessions())
}

// TestReapIdleSessions_ZeroTTLDisablesReaping — the operator escape hatch.
func TestReapIdleSessions_ZeroTTLDisablesReaping(t *testing.T) {
	m, clock := newReapableManager(t, 0)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	*clock = clock.Add(365 * 24 * time.Hour)
	assert.Empty(t, m.ReapIdleSessions(), "idle_ttl<=0 must disable reaping entirely")
	assert.Contains(t, m.sessions, DefaultSessionID)
}

// TestReapIdleSessions_NeverReapsUnstampedSessionOnFirstSweep — a context
// created microseconds ago by a path that forgot to stamp lastActivity must
// not read as infinitely idle. It is stamped and judged on the NEXT sweep.
func TestReapIdleSessions_NeverReapsUnstampedSessionOnFirstSweep(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	// Simulate an entry that never got stamped (zero value).
	m.mu.Lock()
	m.sessions[DefaultSessionID].lastActivity = time.Time{}
	m.mu.Unlock()

	assert.Empty(t, m.ReapIdleSessions(),
		"an unstamped session must be adopted by the sweep, not reaped as infinitely idle")
	assert.Contains(t, m.sessions, DefaultSessionID)

	// It is now stamped, so ordinary aging applies from here.
	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{DefaultSessionID}, m.ReapIdleSessions())
}

// TestViewerDetached_NeverUnderflows — a detach without a matching attach (a
// viewer outliving a session recreation) must not drive the count negative,
// which would make the session permanently unreapable.
func TestViewerDetached_NeverUnderflows(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	m.ViewerDetached(DefaultSessionID)
	m.ViewerDetached(DefaultSessionID)
	m.ViewerDetached(DefaultSessionID)

	m.mu.Lock()
	got := m.sessions[DefaultSessionID].viewers
	m.mu.Unlock()
	assert.Equal(t, 0, got, "viewer count must clamp at zero, never go negative")

	// Still reapable — an underflowed count would have made this impossible.
	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{DefaultSessionID}, m.ReapIdleSessions(),
		"an underflowed viewer count would leave the session permanently unreapable")
}

// TestReapIdleSessions_UnknownSessionIsNoOp — sweeping an empty manager, and
// viewer bookkeeping for a session that does not exist, must both be inert.
func TestReapIdleSessions_UnknownSessionIsNoOp(t *testing.T) {
	m, _ := newReapableManager(t, 30*time.Minute)

	assert.Empty(t, m.ReapIdleSessions())
	assert.NotPanics(t, func() {
		m.ViewerAttached("never-opened")
		m.ViewerDetached("never-opened")
	})
}

// --- start page --------------------------------------------------------------

// TestStartPageURL_DefaultsToBlank pins the fallback: an unconfigured start
// page must behave exactly as before, never leaving a tab with nowhere to go.
func TestStartPageURL_DefaultsToBlank(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	m.cfg.StartPageURL = ""
	assert.Equal(t, BlankPageURL, m.StartPageURL())
}

func TestStartPageURL_UsesConfiguredValue(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	m.cfg.StartPageURL = "http://127.0.0.1:5000/browser-start"
	assert.Equal(t, "http://127.0.0.1:5000/browser-start", m.StartPageURL())
}

// TestStartPageURL_BlankOrWhitespaceFallsBack — a whitespace-only value is a
// misconfiguration, not a URL; it must not become the literal target.
func TestStartPageURL_BlankOrWhitespaceFallsBack(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
	m.cfg.StartPageURL = "   \t\n "
	assert.Equal(t, BlankPageURL, m.StartPageURL(),
		"a whitespace-only start page must fall back to about:blank, not be used verbatim")
}

// TestDefaultConfig_SetsIdleTTL — a fresh install reaps by default; shipping
// with reaping off would preserve the very leak this fixes.
func TestDefaultConfig_SetsIdleTTL(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	assert.Equal(t, DefaultIdleTTL, cfg.IdleTTL,
		"a fresh install must reap idle browsing contexts by default")
	assert.Positive(t, cfg.IdleTTL)
}
