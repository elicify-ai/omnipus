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

// TestReapIdleSessions_NeverReapsUnstampedSessionOnFirstSweep — a TAB created
// microseconds ago by a path that forgot to stamp its lastActivity must not
// read as infinitely idle. It is stamped and judged on the NEXT sweep.
func TestReapIdleSessions_NeverReapsUnstampedSessionOnFirstSweep(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	// Simulate a tab entry that never got stamped (zero value) — lastActivity
	// now lives on the tab, not the session (per-tab reaping).
	m.mu.Lock()
	m.sessions[DefaultSessionID].tabs[0].lastActivity = time.Time{}
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

// TestReapIdleSessions_ReleasesGlobalTabBudgetReservation — ADR-043 D7
// tab-budget accounting. A tab opened via browser_open_tab reserves a global
// slot (BrowserCoordinator.reservedTabs, via TryOpenTab) that is only ever
// released by browser_close_tab's tool wrapper (tabs.go's
// CloseTabTool.Execute calling releaseGlobalTab) or, per this fix, by the
// reaper closing the tab instead. Without this release, a tab that was
// opened via the tool and later closed by ReapIdleSessions — rather than an
// explicit browser_close_tab call — would leak its reservation forever: the
// coordinator's own doc comments (coordinator.go's reservedTabs field and
// ReleaseTab) are explicit that the matching release for a successful open
// is "on a successful open+later close", and there is no other release path
// anywhere in tabs.go's OpenTabTool.Execute (only the reserve-then-open-
// failed branch calls releaseGlobalTab on the tool side). Under any
// operator-configured tools.browser.max_total_tabs cap, that leak
// permanently and monotonically shrinks the effective budget every time the
// reaper — not the tool — is what actually closes a tab, which is now the
// COMMON case at a 5-minute TTL.
func TestReapIdleSessions_ReleasesGlobalTabBudgetReservation(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)
	coordinator := &BrowserCoordinator{}
	m.AttachSharedChrome(coordinator, "agent-under-test")

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	// Simulate this tab having gone through browser_open_tab: that tool
	// reserves a slot (incrementing reservedTabs) AND the resulting tab owns
	// it, which is what a later browser_close_tab — or this reap — hands back.
	// Both halves matter: the counter alone is not enough now that ownership
	// is explicit, because a tab that never reserved must not release.
	coordinator.mu.Lock()
	coordinator.reservedTabs = 1
	coordinator.mu.Unlock()
	m.mu.Lock()
	m.sessions[DefaultSessionID].tabs[0].holdsGlobalReservation = true
	m.mu.Unlock()

	*clock = clock.Add(31 * time.Minute)
	reaped := m.ReapIdleSessions()
	require.Equal(t, []string{DefaultSessionID}, reaped, "sanity: the tab must actually be reaped")

	coordinator.mu.Lock()
	got := coordinator.reservedTabs
	coordinator.mu.Unlock()
	assert.Zero(t, got,
		"reaping a tab must release any global-tab-budget reservation it held — otherwise reservedTabs "+
			"leaks upward forever under an operator-configured max_total_tabs cap, since only "+
			"browser_close_tab's tool wrapper releases it otherwise")
}

// TestReapIdleSessions_DoesNotReleaseBudgetForNeverReservedTab is the
// complement of the test above, and pins the precision that makes it safe.
//
// A session's FIRST tab is created by Session()/createFirstTab, which is
// deliberately NOT budget-gated and therefore never reserved a slot. Releasing
// on its behalf would decrement a counter that only tracks real reservations,
// handing back a slot belonging to somebody else's concurrent open — quietly
// making an operator-configured max_total_tabs more permissive than they asked
// for. Harmless while the budget is unlimited (the new default), wrong under
// any cap, and now driven by a once-a-minute sweep rather than a human-paced
// close.
func TestReapIdleSessions_DoesNotReleaseBudgetForNeverReservedTab(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)
	coordinator := &BrowserCoordinator{}
	m.AttachSharedChrome(coordinator, "agent-under-test")

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)

	// Someone else has an open in flight; this tab owns none of it.
	coordinator.mu.Lock()
	coordinator.reservedTabs = 1
	coordinator.mu.Unlock()

	*clock = clock.Add(31 * time.Minute)
	reaped := m.ReapIdleSessions()
	require.Equal(t, []string{DefaultSessionID}, reaped, "sanity: the tab must actually be reaped")

	coordinator.mu.Lock()
	got := coordinator.reservedTabs
	coordinator.mu.Unlock()
	assert.Equal(t, 1, got,
		"reaping a tab that never reserved a slot must NOT decrement the budget — that reservation "+
			"belongs to a concurrent opener, and stealing it loosens a configured max_total_tabs")
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

// --- new tabs land on the start page -----------------------------------------
//
// Regression coverage for a gap the ORIGINAL start-page tests missed entirely:
// they verified StartPageURL() returned the right string, but nothing verified
// a new tab actually NAVIGATED there. It did not — chromedp.NewContext opens a
// bare about:blank — so the feature was inert in production while its unit
// tests passed. Caught only by driving the live panel (UAT v39, 2026-08-03:
// a brand-new tab measured luma 255 / 1 color bucket, i.e. pure white).

func TestNewTab_NavigatesToStartPage(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
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
	m := newTestManagerWithFakeTabs(t, 5)
	m.cfg.StartPageURL = "http://localhost:5000/browser-start"

	var called int
	m.navigateFn = func(context.Context, string) error { called++; return nil }

	m.navigateNewTabToStartPage(context.Background(), target.ID("existing-target"))
	require.Zero(t, called, "adopting an existing target must never navigate it away from its own page")
}

func TestNewTab_NoStartPageConfigured_DoesNotNavigate(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)
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
	m := newTestManagerWithFakeTabs(t, 5)
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
