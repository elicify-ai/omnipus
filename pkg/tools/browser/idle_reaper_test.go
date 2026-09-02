package browser

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression coverage for the browsing-context lifecycle leak: closing the
// live panel is a pure UI dismiss (the SPA sends no shutdown frame) and
// browser.CloseSession had ZERO production callers, so a browsing context —
// and its resident Chrome — outlived the panel indefinitely. Reopening the
// panel days later showed the exact page the user had left.
//
// NOTE (2026-08-05): reaping became PER TAB — each tab is judged on its own
// last-touched time, and a context is closed only once every tab in it has
// gone. An attached live-panel viewer still exempts the whole context. The
// tests below remain valid as the session-level happy path (one tab per
// session), but read reaper_edge_test.go for the authoritative per-tab
// contract before adding anything here.

// newReapableManager builds a fake-tab manager with a controllable clock and a
// known idle TTL, so idleness can be aged deterministically without sleeping.
func newReapableManager(t *testing.T, ttl time.Duration) (*BrowserManager, *time.Time) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	m.cfg.IdleTTL = ttl
	clock := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	m.nowFn = func() time.Time { return clock }
	return m, &clock
}

func TestReapIdleSessions_ClosesContextIdleBeyondTTL(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID) // stamps lastActivity
	require.NoError(t, err)

	// Not yet idle enough — must survive.
	*clock = clock.Add(29 * time.Minute)
	assert.Empty(t, m.ReapIdleSessions(), "a context inside its TTL must not be reaped")
	assert.Contains(t, m.sessions, testSessionID)

	// Past the TTL with no viewer and no tool call — reap.
	*clock = clock.Add(2 * time.Minute)
	reaped := m.ReapIdleSessions()
	assert.Equal(t, []string{testSessionID}, reaped,
		"a context with no viewer and no agent activity past idle_ttl must be closed; "+
			"without this the browsing context outlives the panel forever")
	assert.NotContains(t, m.sessions, testSessionID, "the reaped session must be removed")
}

// TestReapIdleSessions_NeverReapsWatchedContext — somebody is literally
// watching this context. Elapsed time since the last tool call is irrelevant.
func TestReapIdleSessions_NeverReapsWatchedContext(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID)

	*clock = clock.Add(72 * time.Hour)
	assert.Empty(t, m.ReapIdleSessions(),
		"a context with an attached viewer must never be reaped, however long since the last tool call")
	assert.Contains(t, m.sessions, testSessionID)
}

// TestReapIdleSessions_ReapsAfterLastViewerLeaves — the idle clock starts when
// the last viewer departs, which is exactly the "user closed the panel" case
// this whole feature exists for.
func TestReapIdleSessions_ReapsAfterLastViewerLeaves(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID)
	m.ViewerAttached(testSessionID) // two panels open

	*clock = clock.Add(48 * time.Hour)
	m.ViewerDetached(testSessionID) // one closes — still watched
	assert.Empty(t, m.ReapIdleSessions(), "still one viewer attached")

	m.ViewerDetached(testSessionID) // last one closes; clock restarts here
	assert.Empty(t, m.ReapIdleSessions(), "detach itself is activity — TTL restarts from the departure")

	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions(),
		"once the last viewer has been gone longer than the TTL, the context must be reaped")
}

// TestReapIdleSessions_AgentActivityKeepsContextAlive — the half of the
// contract that protects an agent working in an unwatched tab.
func TestReapIdleSessions_AgentActivityKeepsContextAlive(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	// No viewer ever attaches, but the agent keeps working every 20 minutes.
	for i := 0; i < 5; i++ {
		*clock = clock.Add(20 * time.Minute)
		_, err := m.Session(testSessionID) // a tool call resolving the session
		require.NoError(t, err)
		require.Empty(t, m.ReapIdleSessions(),
			"an agent actively using the browser must never have it reaped out from under it")
	}

	// Work stops; now it ages out.
	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions())
}

// TestReapIdleSessions_ZeroTTLDisablesReaping — the operator escape hatch.
func TestReapIdleSessions_ZeroTTLDisablesReaping(t *testing.T) {
	m, clock := newReapableManager(t, 0)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	*clock = clock.Add(365 * 24 * time.Hour)
	assert.Empty(t, m.ReapIdleSessions(), "idle_ttl<=0 must disable reaping entirely")
	assert.Contains(t, m.sessions, testSessionID)
}

// TestReapIdleSessions_NeverReapsUnstampedSessionOnFirstSweep — a TAB created
// microseconds ago by a path that forgot to stamp its lastActivity must not
// read as infinitely idle. It is stamped and judged on the NEXT sweep.
func TestReapIdleSessions_NeverReapsUnstampedSessionOnFirstSweep(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	// Simulate a tab entry that never got stamped (zero value) — lastActivity
	// now lives on the tab, not the session (per-tab reaping).
	m.mu.Lock()
	m.sessions[testSessionID].tabs[0].lastActivity = time.Time{}
	m.mu.Unlock()

	assert.Empty(t, m.ReapIdleSessions(),
		"an unstamped session must be adopted by the sweep, not reaped as infinitely idle")
	assert.Contains(t, m.sessions, testSessionID)

	// It is now stamped, so ordinary aging applies from here.
	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions())
}

// TestViewerDetached_NeverUnderflows — a detach without a matching attach (a
// viewer outliving a session recreation) must not drive the count negative,
// which would make the session permanently unreapable.
func TestViewerDetached_NeverUnderflows(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	m.ViewerDetached(testSessionID)
	m.ViewerDetached(testSessionID)
	m.ViewerDetached(testSessionID)

	m.mu.Lock()
	got := m.sessions[testSessionID].viewers
	m.mu.Unlock()
	assert.Equal(t, 0, got, "viewer count must clamp at zero, never go negative")

	// Still reapable — an underflowed count would have made this impossible.
	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions(),
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
	m := newTestManagerWithFakeTabs(t)
	m.cfg.StartPageURL = ""
	assert.Equal(t, BlankPageURL, m.StartPageURL())
}

func TestStartPageURL_UsesConfiguredValue(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.cfg.StartPageURL = "http://127.0.0.1:5000/browser-start"
	assert.Equal(t, "http://127.0.0.1:5000/browser-start", m.StartPageURL())
}

// TestStartPageURL_BlankOrWhitespaceFallsBack — a whitespace-only value is a
// misconfiguration, not a URL; it must not become the literal target.
func TestStartPageURL_BlankOrWhitespaceFallsBack(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
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

// --- new tabs land on the start page -----------------------------------------
//
// Regression coverage for a gap the ORIGINAL start-page tests missed entirely:
// they verified StartPageURL() returned the right string, but nothing verified
// a new tab actually NAVIGATED there. It did not — chromedp.NewContext opens a
// bare about:blank — so the feature was inert in production while its unit
// tests passed. Caught only by driving the live panel (UAT v39, 2026-08-03:
// a brand-new tab measured luma 255 / 1 color bucket, i.e. pure white).

func TestNewTab_NavigatesToStartPage(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.cfg.StartPageURL = "http://localhost:5000/browser-start"

	var got []string
	m.navigateFn = func(_ context.Context, url string) error {
		got = append(got, url)
		return nil
	}

	m.navigateNewTabToStartPage(context.Background(), "")
	require.Equal(t, []string{"http://localhost:5000/browser-start"}, got,
		"a brand-new tab must navigate to the start page — about:blank reads as a broken panel")
}

// TestNewTab_AdoptedTargetIsNotRedirected — a non-empty targetID means we are
// adopting an existing target (a popup, a window.open). Navigating it away
// would destroy the very page the user or agent just opened.
func TestNewTab_AdoptedTargetIsNotRedirected(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.cfg.StartPageURL = "http://localhost:5000/browser-start"

	var called int
	m.navigateFn = func(context.Context, string) error { called++; return nil }

	m.navigateNewTabToStartPage(context.Background(), target.ID("existing-target"))
	require.Zero(t, called, "adopting an existing target must never navigate it away from its own page")
}

func TestNewTab_NoStartPageConfigured_DoesNotNavigate(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.cfg.StartPageURL = "" // falls back to about:blank

	var called int
	m.navigateFn = func(context.Context, string) error { called++; return nil }

	m.navigateNewTabToStartPage(context.Background(), "")
	require.Zero(t, called, "with no start page configured the tab must be left on about:blank, not navigated")
}

// TestNewTab_StartPageNavigationFailureIsNonFatal — a tab that could not reach
// the start page is still a perfectly usable tab. Failing tab creation over a
// cosmetic landing page would be a far worse trade.
func TestNewTab_StartPageNavigationFailureIsNonFatal(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.cfg.StartPageURL = "http://localhost:5000/browser-start"
	m.navigateFn = func(context.Context, string) error { return errors.New("navigate exploded") }

	require.NotPanics(t, func() {
		m.navigateNewTabToStartPage(context.Background(), "")
	}, "a failed start-page navigation must never break tab creation")
}

// TestCreateTab_CallsStartPageNavigation closes the gap the tests above cannot:
// they exercise navigateNewTabToStartPage DIRECTLY, so they still pass if the
// call site inside createTab is deleted — which is precisely how the start page
// shipped inert the first time (unit tests green, brand-new tab still
// about:blank on live UAT v39).
//
// createTab's real body performs a CDP attach that cannot run without Chrome,
// so rather than add a second production seam purely for this test, this
// asserts on the SOURCE: createTab must contain the call. A source assertion is
// weak by nature, but it fails loudly on the one edit that caused the outage —
// removing the wiring — which no behavioral test in this file can see.
func TestCreateTab_CallsStartPageNavigation(t *testing.T) {
	src, err := os.ReadFile("manager.go")
	require.NoError(t, err)

	body := string(src)
	start := strings.Index(body, "func (m *BrowserManager) createTab(")
	require.Positive(t, start, "createTab must exist")
	end := strings.Index(body[start:], "\nfunc ")
	require.Positive(t, end, "createTab must be followed by another function")

	require.Contains(t, body[start:start+end], "navigateNewTabToStartPage(",
		"createTab must call navigateNewTabToStartPage — without it every new tab opens "+
			"about:blank and the start page is inert in production (UAT v39 regression)")
}
