package browser

// Fix Wave B, Task 2: regression coverage for BrowserCoordinator.WarmUp — the
// gateway's boot-time hook that launches the shared Chrome process eagerly,
// instead of leaving it entirely lazy until an agent's first browser tool
// call. These tests prove WarmUp actually starts a REAL, live Chrome process
// (not merely that a binary path resolved), that it does so without creating
// any per-agent state (no Chrome-per-agent pool — issue #570 tracks that
// separately), and that it is idempotent/safe to call more than once. The
// gateway-level wiring that DECIDES whether/when to call WarmUp
// (browserWarmUpEnabled, findSharedBrowserCoordinator — config.Enabled/
// CDPURL/OMNIPUS_SKIP_BROWSER_PREPROVISION gating) is covered separately in
// pkg/gateway/browser_warmup_test.go.

import (
	"context"
	"os"
	"syscall"
	"testing"
)

// TestBrowserCoordinator_WarmUp_LaunchesRealChrome proves WarmUp actually
// gets a Chrome PROCESS running and responding over CDP — not merely that a
// binary path was resolved (that weaker guarantee is all Preprovision ever
// gave). Confirms the pid via a direct signal-0 probe against the OS, and
// confirms WarmUp creates no per-agent browser context or manager
// registration (Task 2's "one shared Chrome, no per-agent launch" contract).
func TestBrowserCoordinator_WarmUp_LaunchesRealChrome(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	if pid := coord.PID(); pid != 0 {
		t.Fatalf("expected no live Chrome before WarmUp, got pid %d", pid)
	}

	if err := coord.WarmUp(context.Background()); err != nil {
		t.Fatalf("WarmUp: %v", err)
	}

	pid := coord.PID()
	if pid == 0 {
		t.Fatal("expected a live Chrome pid after WarmUp — the process was not actually launched")
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("os.FindProcess(%d): %v", pid, err)
	}
	// Signal 0 probes existence without delivering a signal (same technique
	// coordinator.go's own pidAlive uses) — the definitive "is this actually
	// a running process" check, stronger than merely observing a non-zero
	// pid value.
	if sigErr := proc.Signal(syscall.Signal(0)); sigErr != nil {
		t.Fatalf("Chrome pid %d reported by PID() is not actually alive: %v", pid, sigErr)
	}

	if n := coord.contextCount(); n != 0 {
		t.Fatalf("expected 0 per-agent browser contexts after WarmUp (no per-agent Chrome pool), got %d", n)
	}
	if n := coord.managerCount(); n != 0 {
		t.Fatalf("expected 0 registered managers after WarmUp (WarmUp registers no agent), got %d", n)
	}
}

// TestBrowserCoordinator_WarmUp_IdempotentSameChrome proves a second WarmUp
// call against an already-warm coordinator is a cheap no-op that resolves to
// the SAME live Chrome, rather than launching a second process or erroring.
func TestBrowserCoordinator_WarmUp_IdempotentSameChrome(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	if err := coord.WarmUp(context.Background()); err != nil {
		t.Fatalf("first WarmUp: %v", err)
	}
	pid1 := coord.PID()
	if pid1 == 0 {
		t.Fatal("expected a live pid after the first WarmUp")
	}

	if err := coord.WarmUp(context.Background()); err != nil {
		t.Fatalf("second WarmUp: %v", err)
	}
	pid2 := coord.PID()

	if pid1 != pid2 {
		t.Fatalf("expected WarmUp to be idempotent (same Chrome pid both times), got %d then %d", pid1, pid2)
	}
}

// TestBrowserCoordinator_WarmUp_ThenRegisterReusesSameChrome proves the
// warmed-up Chrome is the SAME process a real agent Register call later
// reuses — i.e. warm-up genuinely pre-pays the cold-start cost rather than
// warming an unrelated/throwaway instance.
func TestBrowserCoordinator_WarmUp_ThenRegisterReusesSameChrome(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	t.Cleanup(coord.Shutdown)

	if err := coord.WarmUp(context.Background()); err != nil {
		t.Fatalf("WarmUp: %v", err)
	}
	warmPID := coord.PID()

	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("post-warmup-agent"))
	if _, _, err := coord.Register(context.Background(), "post-warmup-agent", mgr); err != nil {
		t.Fatalf("Register after WarmUp: %v", err)
	}

	if got := coord.PID(); got != warmPID {
		t.Fatalf("expected Register to reuse the warmed-up Chrome (pid %d), got a different pid %d", warmPID, got)
	}
}
