package browser

// coordinator_review_test.go — tests added by the 7-reviewer pass on ADR-043
// (G1/G3/G4). They guard the correctness fixes the reviewers surfaced: CRIT-002/
// C1 reload re-adoption with cookie survival (G1), CRIT-001 crash recovery into
// fresh contexts (G3), and the I-1/W3/C1 tab-budget reservation (G4). G1/G3
// launch a real Chrome via the coordinator and skip on short/no-Chrome runs; G4
// is a pure unit test over the coordinator's in-memory budget.
//
// The former G5 (M2 preflight foreign-PORT rejection) was RETIRED with CRIT-001:
// there is no CDP port anymore (CDP is over the pipe), so the "foreign Chrome
// squatting our port" case is gone. The single-launch guarantee is now enforced
// by the O_EXCL/flock lockfile and proven by TestCoordinator_LockfileSingleLaunch
// (coordinator_test.go).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	if testing.Short() {
		t.Skip("needs a real Chrome")
	}
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
	t.Cleanup(func() { coord.Shutdown() })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "<html><body>cookie-test</body></html>")
	}))
	t.Cleanup(srv.Close)

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, "agent-a")

	// First registration + session: set a cookie in agent-a's browser context.
	_, ctxID1, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	if ctxID1 == "" {
		t.Fatal("expected a non-empty browser context id")
	}
	tabCtx, err := mgrA.Session(defaultSessionID)
	if err != nil {
		t.Fatalf("Session 1: %v", err)
	}
	sctx, cancel := context.WithTimeout(tabCtx, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(sctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatalf("navigate 1: %v", err)
	}
	if err := chromedp.Run(sctx, chromedp.ActionFunc(func(ctx context.Context) error {
		if serr := network.SetCookie("reload_marker", "AdoptedSurvives").WithURL(srv.URL).Do(ctx); serr != nil {
			return serr
		}
		return nil
	})); err != nil {
		t.Fatalf("set cookie before reload: %v", err)
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
	mgrA2.AttachSharedChrome(coord, "agent-a")
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
	tabCtx2, err := mgrA2.Session(defaultSessionID)
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
		return fmt.Errorf("cookie reload_marker=AdoptedSurvives not found in re-adopted context (got %d cookies)", len(cookies))
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
	if testing.Short() {
		t.Skip("needs a real Chrome")
	}
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg, 30)
	t.Cleanup(func() { coord.Shutdown() })

	mgrA := newTestManager(t, cfg)
	mgrA.AttachSharedChrome(coord, "agent-a")

	_, ctxID1, err := coord.Register(context.Background(), "agent-a", mgrA)
	if err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	if _, err := mgrA.Session(defaultSessionID); err != nil {
		t.Fatalf("Session 1: %v", err)
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
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill Chrome: %v", err)
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
		t.Fatalf("CRIT-001 VIOLATION: post-crash Register returned the STALE context id %q (expected a fresh one)", ctxID2)
	}

	// The relaunched Chrome is usable.
	if _, err := mgrA.Session(defaultSessionID); err != nil {
		t.Fatalf("Session after crash recovery: %v", err)
	}
}

// G4 / I-1/W3/C1: TryOpenTab atomically checks + RESERVES a slot (live tabs +
// in-flight reservations) so concurrent openers at the boundary see exactly one
// winner. Pure unit test over the coordinator's in-memory budget — no Chrome.
func TestCoordinator_TabBudgetDenial(t *testing.T) {
	home := t.TempDir()
	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30 * time.Second,
		MaxTabs:     5,
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
	}
	coord := NewBrowserCoordinator(home, cfg, 1) // maxTotalTabs=1

	// First opener at the boundary: allowed + reserves the one slot.
	ok, reason := coord.TryOpenTab("agent-a")
	if !ok {
		t.Fatalf("first TryOpenTab should succeed; got reason=%q", reason)
	}
	if reason != "" {
		t.Fatalf("first TryOpenTab reason should be empty; got %q", reason)
	}

	// Second concurrent opener: denied — the reservation from the first counts.
	ok, reason = coord.TryOpenTab("agent-b")
	if ok {
		t.Fatal("second TryOpenTab should be denied (budget=1, 1 reservation in flight)")
	}
	if reason == "" || !strings.Contains(reason, "budget") {
		t.Fatalf("denied TryOpenTab reason should mention budget; got %q", reason)
	}

	// ReleaseTab returns the slot → the next opener succeeds. This proves
	// ReleaseTab is no longer a no-op (the I-1 fix) and that the reservation
	// counter is decremented.
	coord.ReleaseTab("agent-a")
	ok, reason = coord.TryOpenTab("agent-c")
	if !ok {
		t.Fatalf("TryOpenTab after ReleaseTab should succeed; got reason=%q", reason)
	}
}

// (Former G5 / M2 preflight foreign-PORT rejection retired — see file header.
// The single-launch guarantee is now the O_EXCL/flock lockfile, exercised by
// TestCoordinator_LockfileSingleLaunch in coordinator_test.go.)
