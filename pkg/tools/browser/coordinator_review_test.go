package browser

// coordinator_review_test.go — tests added by the 7-reviewer pass on ADR-043
// (G1/G3/G4/G5). They guard the four correctness fixes the reviewers
// surfaced: CRIT-002/C1 reload re-adoption with cookie survival (G1), CRIT-001
// crash recovery into fresh contexts (G3), the I-1/W3/C1 tab-budget reservation
// (G4), and the M2 foreign-holder rejection (G5). G1/G3 launch a real Chrome
// via the coordinator and skip on short/no-Chrome runs; G4 is a pure unit test
// over the coordinator's in-memory budget.
//
// WebRTC build W1-A (CRIT-001) rewrote G5: the fixed CDP TCP debug port
// (9223) that the old preflight guarded is gone — CDP now flows over
// Chromium's --remote-debugging-pipe, so there is no shared port a foreign
// process could squat anymore. The single-launch guard moved to an
// O_EXCL/flock lockfile living INSIDE this coordinator's own profile dir
// (coordinator.go's takeLaunchLock), which by construction only another
// omnipus coordinator could ever hold — so the only failure mode worth
// guarding is "a prior LIVE omnipus gateway still owns this lock", not
// "some unrelated process is squatting a shared port". No Chrome launches in
// the rewritten G5 either.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// G1 / CRIT-002 (the headline): a manager reload (Release + new manager
// AttachSharedChrome + Register for the SAME agentID) must re-adopt the SAME
// browser context — same BrowserContextID, same Chrome PID, KillCount==0, AND
// the cookie a tab set BEFORE the reload is still readable AFTER it (login
// survives a Settings save). Also guards CRIT-003 (no manager path disposes the
// context).
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
	_, ctxID1, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	if ctxID1 == "" {
		t.Fatal("expected a non-empty browser context id")
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
			return serr
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
	// coordinator-owned browser context survives for the next Register.
	coord.Release("agent-a")
	if coord.KillCount() != 0 {
		t.Fatalf("Release must not kill Chrome; KillCount=%d", coord.KillCount())
	}

	// A NEW manager for the same agent re-registers (mirrors loop.go's reload).
	mgrA2 := newTestManager(t, cfg)
	mgrA2.AttachSharedChrome(coord, browserTestKey("agent-a"))
	_, ctxID2, err := coord.Register(context.Background(), "agent-a", mgrA2)
	if err != nil {
		t.Fatalf("Register 2: %v", err)
	}

	// CRIT-002 assertions: same context id, same pid, no kill.
	if ctxID1 != ctxID2 {
		t.Fatalf("CRIT-002 VIOLATION: browser context id changed across reload: %q → %q", ctxID1, ctxID2)
	}
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
			return gerr
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

	_, ctxID1, err := coord.Register(context.Background(), "agent-a", mgrA)
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

	// CRIT-001: all per-agent contexts are gone with the dead process.
	if coord.contextCount() != 0 {
		t.Fatalf("CRIT-001 VIOLATION: expected contextCount==0 after crash (fresh-empty); got %d", coord.contextCount())
	}

	// Register again → a NEW browser context id (not the stale dead one).
	_, ctxID2, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register after crash: %v", err)
	}
	if ctxID2 == "" {
		t.Fatal("expected a non-empty fresh browser context id after crash")
	}
	if ctxID1 == ctxID2 {
		t.Fatalf(
			"CRIT-001 VIOLATION: post-crash Register returned the STALE context id %q (expected a fresh one)",
			ctxID2,
		)
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
