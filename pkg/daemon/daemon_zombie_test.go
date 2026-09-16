// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build darwin || linux

package daemon

import (
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestCheckProcess_UnreapedZombieIsDead proves this platform's zombie
// detection end to end: a child we killed but deliberately did NOT reap
// (no Wait before the assertions) must be reported dead by checkProcess,
// and Status must clear a PID file that points at the corpse.
//
// This is the regression test for the macOS defect where a dead-but-unreaped
// gateway was reported "running": kill(pid, 0) succeeds for a zombie, and the
// isZombie guard was Linux-only, so on macOS every zombie looked alive.
//
// The child stays a zombie for the whole test because WE are its parent and
// we never wait(2) until after every assertion — the kernel cannot free the
// PID entry until then, so the state under test is deterministic.
func TestCheckProcess_UnreapedZombieIsDead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-process zombie test in short mode")
	}

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := cmd.Process.Pid

	// Kill WITHOUT reaping: no cmd.Wait() here — the child must remain a
	// zombie while the assertions below run.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child %d: %v", pid, err)
	}

	// Wait until the kernel has actually processed the exit — the signal is
	// posted, not yet acted on, when Kill returns. isZombie observing the
	// corpse here is the readiness gate; the oracle is checkProcess below.
	deadline := time.Now().Add(5 * time.Second)
	for !isZombie(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d never became a zombie after SIGKILL — platform zombie detection is broken on %s", pid, runtime.GOOS)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The core assertion: checkProcess must see through the zombie. A pass
	// here with the zombie confirmed above means isZombie worked; an "alive"
	// verdict means zombie detection is broken on this platform.
	alive, _, _ := checkProcess(pid)
	if alive {
		t.Errorf("checkProcess(%d) reported an unreaped zombie as alive — zombie detection is broken on %s", pid, runtime.GOOS)
	}

	// Production consequence: a PID file pointing at the corpse is stale and
	// Status must clear it, not report the gateway as running.
	home := newHome(t)
	writePIDFile(t, home, pid)
	running, _, err := Status(home)
	if err != nil {
		t.Fatalf("Status: unexpected error for zombie PID: %v", err)
	}
	if running {
		t.Errorf("Status reported a zombie (pid %d) as running; stale PID file was not recognized", pid)
	}
	if got := readPIDFile(t, home); got != "" {
		t.Errorf("Status did not remove the PID file pointing at a zombie; content=%q", got)
	}

	// Reap only after every assertion — hygiene, so the test binary does not
	// carry the corpse for the rest of the package run.
	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &ws, 0, nil); err != nil {
		t.Logf("reap child %d after assertions: %v", pid, err)
	}
}
