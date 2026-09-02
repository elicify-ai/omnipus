//go:build !windows

// coordinator_shutdown_process_test.go — proves the OS PROCESS is gone after
// Shutdown, not merely that the coordinator's own bookkeeping says so.
//
// Operator requirement (2026-08-13): "closing omnipus should close all tabs of
// that session." Verified live by SIGTERMing a real gateway and watching all
// eight Chrome processes disappear within 2s — but nothing in the suite
// asserted it, and the closest existing test (TestCoordinator_Shutdown_IsSoleKill)
// checks coord.PID()==0 and KillCount()==1: both are fields this package sets
// itself. A Shutdown that zeroed its own bookkeeping and leaked the process
// would pass that test and leave the operator with an orphaned browser holding
// every tab open — the same "mechanism reported success, property never
// checked" shape behind the JPEG-screencast and focus-emulation episodes.

package browser

import (
	"context"
	"syscall"
	"testing"
	"time"
)

// processAlive reports whether pid is a live process, via signal 0 — the
// POSIX existence probe, which delivers nothing and only reports whether the
// process can be signalled.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func TestCoordinator_Shutdown_KillsTheRealChromeProcess(t *testing.T) {
	skipIfNoBrowser(t)
	cfg, home := newCoordinatorTestConfig(t)
	coord := NewBrowserCoordinator(home, cfg)
	mgr := newTestManager(t, cfg)
	mgr.AttachSharedChrome(coord, browserTestKey("agent-a"))
	if _, _, err := coord.Register(context.Background(), "agent-a", mgr); err != nil {
		t.Fatalf("Register: %v", err)
	}

	pid := coord.PID()
	if pid == 0 {
		t.Fatal("expected a live Chrome pid before Shutdown")
	}
	if !processAlive(pid) {
		t.Fatalf("sanity: Chrome pid %d must be alive before Shutdown", pid)
	}

	coord.Shutdown()

	// Chrome exits asynchronously after the kill; poll rather than assume an
	// instant transition. The live gateway measurement showed the whole tree
	// gone within 2s, so 15s is generous without being a hang.
	deadline := time.Now().Add(15 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf(
				"Chrome process %d is STILL ALIVE 15s after coordinator.Shutdown() — shutting down Omnipus "+
					"left an orphaned browser holding every tab of this session open",
				pid,
			)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
