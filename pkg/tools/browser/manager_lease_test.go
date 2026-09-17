// manager_lease_test.go: tests for workspace lease and ownership of a browser instance - attach to the pool/coordinator, the browsing key, the write lease, and which tab set a turn addresses.

package browser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// --- moved from manager.go tests 2026-09-15 ---

// --- integration: LoadExtension actually wires ctx through ------------------

// TestCoordinator_LoadExtension_HonorsCallerContext is a real-Chrome
// integration test proving LoadExtension's ACTUAL production call site — not
// just boundedCallContext in isolation — now uses the caller's ctx. Before
// the fix, ctx was accepted but never referenced anywhere in LoadExtension's
// body: the CDP call always ran against the coordinator's own long-lived
// rootCtx, so an already-canceled caller ctx had zero effect and the call
// could only ever complete or hang on Chrome's own response.
//
// Passing an ALREADY-CANCELED context means boundedCallContext's "no
// deadline" branch wraps it via context.WithTimeout(ctx, def) — which
// immediately observes the already-canceled parent (context's own
// propagateCancel fires synchronously for an already-done parent) — so the
// resulting callCtx is Done() before the CDP round trip even starts. Against
// a REAL Chrome, the genuine CDP response requires a measurable IPC round
// trip that cannot complete before this test's own goroutine has finished
// canceling and dispatching, so this is not a flaky race in practice (a real
// io/ipc round trip is never zero-duration) — it deterministically proves
// the ctx is now load-bearing, which was impossible before this fix (the old
// code would have SUCCEEDED here, using rootCtx instead).
func TestCoordinator_LoadExtension_HonorsCallerContext(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	cfg.ExtensionDir = filepath.Join(t.TempDir(), "does-not-need-to-exist-for-this-assertion")
	// A directory need not actually contain a valid manifest for THIS test:
	// we assert on ctx-driven cancellation, which fires before Chrome ever
	// gets far enough to validate the manifest content. (A separate,
	// non-canceled-ctx call against a real extension dir is exercised by
	// TestCoordinator_LoadExtension_SucceedsWithGenerousContext below.)

	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	// Force Chrome live first via a normal, generous-context Register so the
	// LoadExtension call under test exercises ONLY the ctx-cancellation
	// behavior, not coordinator launch latency.
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("warm-agent"))
	if _, err := coord.Register(context.Background(), "warm-agent", mgr); err != nil {
		t.Fatalf("Register (warm-up): %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := coord.LoadExtension(canceledCtx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from LoadExtension given an already-canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the error chain to contain context.Canceled, got: %v", err)
	}
	if elapsed > 15*time.Second {
		t.Fatalf(
			"LoadExtension took %s to honor an already-canceled context — ctx is not actually load-bearing",
			elapsed,
		)
	}
}

// TestCoordinator_LoadExtension_SucceedsWithGenerousContext is the happy-path
// companion to the cancellation test above: a normal, generous context still
// succeeds end-to-end against a real unpacked extension directory, proving
// the bound-wiring fix didn't break the working case.
func TestCoordinator_LoadExtension_SucceedsWithGenerousContext(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	extDir := t.TempDir()
	writeMinimalUnpackedExtension(t, extDir)
	cfg.ExtensionDir = extDir

	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("warm-agent"))
	if _, err := coord.Register(context.Background(), "warm-agent", mgr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	id, err := coord.LoadExtension(context.Background())
	if err != nil {
		t.Fatalf("LoadExtension with a generous context should succeed: %v", err)
	}
	if id == "" {
		t.Fatal("expected a non-empty extension id")
	}
}

// G1 / CRIT-002 (the headline): a manager reload (Release + new manager
// AttachSharedChrome + Register for the SAME key) must land on the SAME live
// Chrome — same PID, KillCount==0 — AND the cookie a tab set BEFORE the
// reload must still be readable AFTER it (login survives a Settings save).
//
// The context-id half of this assertion is GONE with ADR-075 FR-031, which
// deleted CDP browser contexts. What persists a login across a reload is now
// the workspace's own --user-data-dir profile directory on disk (FR-043),
// which is strictly stronger: the old CDP context was in-memory and did not
// survive a Chrome restart at all. The cookie assertion below is therefore
// the load-bearing one and is unchanged.
func TestCoordinator_Reload_ReAdoptsContext_CookieSurvives(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(func() { coord.Shutdown() })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "<html><body>cookie-test</body></html>")
	}))
	t.Cleanup(srv.Close)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, browserTestKey("agent-a"))

	// First registration + session: set a cookie in agent-a's browser context.
	_, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	tabCtx, err := mgrA.Session(testSessionID)
	if err != nil {
		t.Fatalf("Session 1: %v", err)
	}
	sctx, cancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer cancel()
	if navErr := chromedp.Run(sctx, chromedp.Navigate(srv.URL)); navErr != nil {
		t.Fatalf("navigate 1: %v", navErr)
	}
	if cookieErr := chromedp.Run(sctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if serr := network.SetCookie("reload_marker", "AdoptedSurvives").WithURL(srv.URL).Do(ctx); serr != nil {
			return fmt.Errorf("set cookie: %w", serr)
		}
		return nil
	})); cookieErr != nil {
		t.Fatalf("set cookie before reload: %v", cookieErr)
	}
	pid1 := coord.PID()
	if pid1 == 0 {
		t.Fatal("expected a live Chrome pid before reload")
	}

	// Reload: Release drops only the manager's connection (CRIT-002/C1). The
	// Chrome process and its profile directory survive for the next Register.
	coord.Release("agent-a")
	if coord.KillCount() != 0 {
		t.Fatalf("Release must not kill Chrome; KillCount=%d", coord.KillCount())
	}

	// A NEW manager for the same agent re-registers (mirrors loop.go's reload).
	mgrA2 := newTestManager(t, cfg)
	mgrA2.AttachSharedChrome(coord, browserTestKey("agent-a"))
	if _, rerr := coord.Register(context.Background(), "agent-a", mgrA2); rerr != nil {
		t.Fatalf("Register 2: %v", rerr)
	}

	// CRIT-002 assertions: same pid, no kill.
	if coord.PID() != pid1 {
		t.Fatalf("CRIT-002 VIOLATION: Chrome pid changed across reload: %d → %d", pid1, coord.PID())
	}
	if coord.KillCount() != 0 {
		t.Fatalf("KillCount must stay 0 across reload; got %d", coord.KillCount())
	}

	// The cookie set before the reload must still be present in the re-adopted
	// context — login/localStorage survives a Settings save.
	tabCtx2, err := mgrA2.Session(testSessionID)
	if err != nil {
		t.Fatalf("Session 2: %v", err)
	}
	sctx2, cancel2 := context.WithTimeout(tabCtx2, 30*time.Second)
	defer cancel2()
	if err := chromedp.Run(sctx2, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatalf("navigate 2: %v", err)
	}
	if err := chromedp.Run(sctx2, chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, gerr := network.GetCookies().Do(ctx)
		if gerr != nil {
			return fmt.Errorf("get cookies: %w", gerr)
		}
		for _, c := range cookies {
			if c.Name == "reload_marker" && c.Value == "AdoptedSurvives" {
				return nil
			}
		}
		return fmt.Errorf(
			"cookie reload_marker=AdoptedSurvives not found in re-adopted context (got %d cookies)",
			len(cookies),
		)
	})); err != nil {
		t.Fatalf("cookie did not survive reload (context not re-adopted): %v", err)
	}
}

// G3 / CRIT-001: when the shared Chrome process dies, the crash detector
// (watchForCrash → ensureLaunched relaunch) brings up a FRESH Chrome within the
// recovery bound, the per-agent contexts are cleared (in-memory, lost on process
// restart), and the next Register creates a NEW browser context id (not a stale
// dead one). Session works against the relaunched Chrome.
func TestCoordinator_CrashRecovery(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(func() { coord.Shutdown() })

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, browserTestKey("agent-a"))

	_, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	if _, sessErr := mgrA.Session(testSessionID); sessErr != nil {
		t.Fatalf("Session 1: %v", sessErr)
	}
	pid1 := coord.PID()
	if pid1 == 0 {
		t.Fatal("expected a live Chrome pid")
	}

	// Kill the shared Chrome process. chromedp's allocator goroutine owns
	// cmd.Wait; we only Kill (SIGKILL) — watchForCrash detects the transport
	// drop via LostConnection and relaunches.
	coord.mu.Lock()
	cmd := coord.cmd
	coord.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		t.Fatal("no capturable Chrome cmd to kill")
	}
	if killErr := cmd.Process.Kill(); killErr != nil {
		t.Fatalf("kill Chrome: %v", killErr)
	}

	// Wait for the crash detector to relaunch a FRESH Chrome (new pid, non-zero,
	// different from the dead one), bounded well past the launch timeout.
	deadline := time.Now().Add(30 * time.Second)
	var pid2 int
	for time.Now().Before(deadline) {
		pid2 = coord.PID()
		if pid2 != 0 && pid2 != pid1 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if pid2 == 0 {
		t.Fatal("crash recovery did not relaunch Chrome (PID still 0 after 30s)")
	}
	if pid2 == pid1 {
		t.Fatalf("crash recovery did not produce a fresh pid; still %d", pid1)
	}

	// Register again → the relaunched Chrome, reachable through a fresh root
	// context. There is no context id to compare any more (ADR-075 FR-031
	// deleted CDP browser contexts); what makes recovery observable is the
	// fresh pid asserted above plus the usable session asserted below.
	if _, err := coord.Register(context.Background(), "agent-a", mgrA); err != nil {
		t.Fatalf("Register after crash: %v", err)
	}

	// The relaunched Chrome is usable.
	if _, err := mgrA.Session(testSessionID); err != nil {
		t.Fatalf("Session after crash recovery: %v", err)
	}
}

// G5 / M2 (rewritten for CRIT-001's lockfile model — see file doc): a second
// coordinator must never silently drive Chrome while the single-launch
// lockfile is held by a LIVE prior omnipus gateway. Simulates that by holding
// the lock ourselves (standing in for "another process") and stamping the
// ownership marker with OUR OWN live pid. ensureLaunched must fail without
// launching anything.
func TestCoordinator_LaunchLock_LiveOwnerRejected(t *testing.T) {
	home := t.TempDir()
	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30 * time.Second,
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
	}
	coord := NewBrowserCoordinator(home, cfg)

	if err := os.MkdirAll(cfg.ProfileDir, 0o700); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	lockFile, ok, err := acquireLaunchLock(coord.lockPath())
	if err != nil {
		t.Fatalf("acquireLaunchLock (simulating a live prior gateway): %v", err)
	}
	if !ok {
		t.Fatal("expected to acquire the launch lock ourselves (nothing else should hold it in a fresh t.TempDir())")
	}
	t.Cleanup(func() { releaseLaunchLock(lockFile) })
	if err := coord.writeOwnershipMarker(os.Getpid(), "Chrome-for-Testing"); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	if err := coord.ensureLaunched(context.Background()); err == nil {
		t.Fatal("ensureLaunched should FAIL when the launch lock is held by a live omnipus process")
	} else if !strings.Contains(err.Error(), "prior omnipus gateway") {
		t.Fatalf("ensureLaunched error should mention a prior omnipus gateway; got %v", err)
	}
	if coord.PID() != 0 {
		t.Fatalf("no Chrome should have launched while the lock is held by a live owner; got pid %d", coord.PID())
	}
}

// M2, re-scoped by ADR-075: two agents on ONE browsing key Register against
// that key's coordinator and get exactly one Chrome (one PID) and the SAME
// underlying *chromedp.Browser.
//
// The original name was TestCoordinator_TwoAgents_OneChrome_TwoContexts and
// the original third assertion was "two DISTINCT browser context ids". That
// assertion is deleted, not weakened: FR-031 removed CDP browser contexts, so
// there is no id to compare, and per-agent isolation is no longer a thing the
// product offers or claims. Isolation is per WORKSPACE now, and it is proved
// by TestPool_TwoWorkspaces_TwoChromes against two Chrome PROCESSES with two
// --user-data-dir paths — a boundary this test never had.
func TestCoordinator_TwoAgentsOnOneKey_ShareOneChrome(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, browserTestKey("agent-a"))
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, browserTestKey("agent-b"))

	rootA, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register A: %v", err)
	}
	rootB, err := coord.Register(context.Background(), "agent-b", mgrB)
	if err != nil {
		t.Fatalf("Register B: %v", err)
	}
	pid := coord.PID()
	if pid == 0 {
		t.Fatal("expected a live Chrome pid after Register")
	}
	// CRIT-001: both agents drive chromedp CHILD contexts of the SAME shared
	// rootCtx (one CDP pipe, multiplexed). Assert that identity directly via
	// the underlying *chromedp.Browser pointer (rootA/rootB themselves are
	// distinct context.Context values — each Register call wraps a fresh
	// child — but both must resolve to the one shared Browser).
	brA := chromedp.FromContext(rootA)
	brB := chromedp.FromContext(rootB)
	if brA == nil || brB == nil || brA.Browser == nil || brB.Browser == nil {
		t.Fatal("expected both Register calls to return a context bound to a live *chromedp.Browser")
	}
	if brA.Browser != brB.Browser {
		t.Fatal("both agents on one key should share the SAME underlying Chrome (*chromedp.Browser)")
	}
	t.Cleanup(func() { coord.Shutdown() })
}

// FR-008, re-scoped to THE KEY'S OWN CHROME: coordinator.Shutdown() is the
// SOLE kill path for the Chrome this key owns. After it that key's pid is gone
// and KillCount==1. It says nothing about any other key's Chrome — under
// ADR-075 there are N of them, and TestPool_CrashIsContained is what proves
// one going down leaves the others up.
func TestCoordinator_Shutdown_IsSoleKill(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("agent-a"))
	if _, err := coord.Register(context.Background(), "agent-a", mgr); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if coord.PID() == 0 {
		t.Fatal("expected a live Chrome pid before Shutdown")
	}

	coord.Shutdown()

	if coord.PID() != 0 {
		t.Fatalf("expected pid 0 after coordinator.Shutdown(); got %d", coord.PID())
	}
	if coord.KillCount() != 1 {
		t.Fatalf("expected KillCount==1 after Shutdown; got %d", coord.KillCount())
	}
}

// ---------------------------------------------------------------------------
// DELETED with ADR-075 FR-031, and deliberately not replaced:
//
//   - TestBrowserCoordinator_CaptureSharedContext_ConfigAndEnvOverride
//   - TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID
//
// Both exercised the retired shared-context config knob and its env
// override. The knob, the override and the whole CDP-browser-context
// mechanism they selected between
// are gone: every session now bootstraps into Chrome's DEFAULT context, which
// is the only one chrome.tabCapture can reach, and isolation moved down to
// one Chrome process and one profile directory per workspace.
//
// The surviving question — "is a CDP browser context ever created at all?" —
// is answered structurally by TestNoCDPBrowserContextIsEverCreated
// (no_residual_test.go), which cannot be satisfied by a passing runtime
// assertion against a build that reintroduced the branch.
// ---------------------------------------------------------------------------

// Ownership marker round-trip (M2 primitive): the coordinator can write+read
// its own marker; a stale/foreign pid is detected as not-alive. This never
// launches Chrome (writeOwnershipMarker/readOwnershipMarker/pidAlive are pure
// file/OS-signal logic), so it deliberately uses budgetTestConfig — NOT
// newCoordinatorTestConfig, whose ExecPath resolution can trigger a real
// Chrome-for-Testing download this test does not need (#615 finding: this
// call was previously ungated by testing.Short()/skipIfNoBrowser, so it ran —
// and could download — unconditionally in every CI gate).
func TestCoordinator_OwnershipMarker_RoundTrip(t *testing.T) {
	cfg, home := budgetTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	if err := coord.writeOwnershipMarker(999999, "Chrome-for-Testing"); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	pid, owner, err := coord.readOwnershipMarker()
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	// Owner is the fixed omnipus identity tag (ownershipMarkerOwner); the
	// product arg is stored separately. The marker's job is identity (ours vs
	// foreign) + pid, so assert the identity constant + the pid round-trip.
	if pid != 999999 || owner != ownershipMarkerOwner {
		t.Fatalf("marker round-trip mismatch: pid=%d owner=%q (want %q)", pid, owner, ownershipMarkerOwner)
	}
	if pidAlive(999999) {
		t.Fatal("pid 999999 should not be alive — foreign-marker detection relies on pidAlive")
	}
}

// TestCoordinator_Register_NewAgentWindow_MatchesAgentWindowSize is the
// live-Chrome end-to-end companion to the unit test above: it forces two
// separate agents to open their first real tab (mirroring exactly how
// every production browser tool call gets its context, via
// BrowserManager.Session) and asserts BOTH agents' windows are exactly
// 1280x720 via the real CDP Browser.getWindowForTarget call. On the Chrome
// build available in this environment this passes both before and after
// the fix (see file doc comment) — it is real evidence of correct
// end-to-end behavior, not the revert-proof (that is the unit test above).
func TestCoordinator_Register_NewAgentWindow_MatchesAgentWindowSize(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, browserTestKey("agent-window-a"))
	mgrB := newTestManager(t, cfg)
	mgrB.AttachSharedChrome(coord, browserTestKey("agent-window-b"))

	// Session (not a bare Register call) is what every real browser tool
	// call goes through: it drives ensureStarted -> coordinator.Register ->
	// createFirstTab, producing a fully chromedp-attached tab context in
	// the SAME per-agent window Register creates.
	tabA, err := mgrA.Session("default")
	if err != nil {
		t.Fatalf("agent-window-a Session: %v", err)
	}
	tabB, err := mgrB.Session("default")
	if err != nil {
		t.Fatalf("agent-window-b Session: %v", err)
	}

	boundsA := windowBoundsForSession(t, tabA)
	boundsB := windowBoundsForSession(t, tabB)

	for name, b := range map[string]*browser.Bounds{"agent-window-a": boundsA, "agent-window-b": boundsB} {
		if b.Width != agentWindowWidth || b.Height != agentWindowHeight {
			t.Errorf(
				"%s: window bounds = %dx%d, want %dx%d (D17 regression — new-window size not pinned to the screencast cap)",
				name,
				b.Width,
				b.Height,
				agentWindowWidth,
				agentWindowHeight,
			)
		}
	}
}

// TestBrowsingKey_HasNoLiteralConstructor is the structural half of D1.11: a
// BrowsingKey is minted only by resolution. The zero value must be unusable and
// must not render as anything a map could key on by accident.
func TestBrowsingKey_HasNoLiteralConstructor(t *testing.T) {
	var zero BrowsingKey
	require.True(t, zero.IsZero())
	require.Equal(t, "", zero.String())
	require.Equal(t, "", zero.WorkspaceID())
	require.Equal(t, "", zero.ProfileSegment(),
		"a zero key must not render a profile directory — that directory would be shared by everything")

	resolved := newTestBrowsingKey(t, "01J8ZQ4T7N9K3M2P5R6S7T8V9W")
	require.False(t, resolved.IsZero())
	require.Equal(t, "ws:01J8ZQ4T7N9K3M2P5R6S7T8V9W", resolved.String())
	require.Equal(t, "01J8ZQ4T7N9K3M2P5R6S7T8V9W", resolved.WorkspaceID())
}

// TestFocusedTabSet_SessionWithNoTabsAddressesTheOperatorsSet is the UAT-15
// regression: the operator browses to a page, asks the agent to act on it, and
// the agent must land on THAT page.
//
// Before this, a session with no tabs of its own defaulted to its own (empty)
// set, so browser_type went to a blank tab while the panel showed the
// operator's page unchanged — and the agent reported success. Reproduced 4/4
// by a UAT tester driving the real UI.
func TestFocusedTabSet_SessionWithNoTabsAddressesTheOperatorsSet(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	session, err := TabOwnerSession("chat-with-no-tabs-of-its-own")
	require.NoError(t, err)

	// The operator has browsed: Session() on the workspace-owned key creates
	// that set with one tab, which is what the live panel is looking at.
	_, err = m.Session(sessionKey(m.key, TabOwnerWorkspace()))
	require.NoError(t, err)

	// hasTabsLocked needs m.mu; focusedTabSet takes it itself, so the
	// preconditions are read in their own critical section and released
	// before the call under test.
	m.mu.Lock()
	operatorHasTabs := m.hasTabsLocked(TabOwnerWorkspace())
	sessionHasTabs := m.hasTabsLocked(session)
	m.mu.Unlock()

	require.True(t, operatorHasTabs,
		"precondition: the fixture must give the operator's set a tab, "+
			"or this test proves nothing about preferring it")
	require.False(t, sessionHasTabs,
		"precondition: the asking session must have no tabs of its own")

	require.Equal(t, TabOwnerWorkspace(), m.focusedTabSet(session),
		"a session with no tabs of its own must address the OPERATOR's set — "+
			"otherwise the agent acts on a blank tab of its own while the panel "+
			"shows the operator's page unchanged, and reports success")
}

// TestFocusedTabSet_SessionWithItsOwnTabsIsNotDiverted is the other half, and
// it is what keeps the change narrow: an agent that has been browsing must
// never be silently moved onto the operator's page mid-task.
func TestFocusedTabSet_SessionWithItsOwnTabsIsNotDiverted(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	session, err := TabOwnerSession("chat-that-has-been-browsing")
	require.NoError(t, err)

	// Both sets have a tab: the agent has been browsing, and so has the operator.
	_, err = m.Session(sessionKey(m.key, TabOwnerWorkspace()))
	require.NoError(t, err)
	_, err = m.Session(sessionKey(m.key, session))
	require.NoError(t, err)

	m.mu.Lock()
	sessionHasTabs := m.hasTabsLocked(session)
	m.mu.Unlock()
	require.True(t, sessionHasTabs, "precondition: the session owns a tab")

	require.Equal(t, session, m.focusedTabSet(session),
		"a session with its own tabs must keep addressing them")
}
