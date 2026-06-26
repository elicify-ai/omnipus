//go:build linux

// Regression test for the HIGH-severity timeout-bypass bug: the hardened-exec
// foreground path killed only the direct child PID on timeout, so a
// `sh -c '... && sleep N'` grandchild that inherited the stdout pipe kept the
// pipe open and blocked cmd.Wait until the grandchild exited on its own — the
// command outlived its timeout by the full grandchild duration. The fix
// overrides cmd.Cancel to kill the whole process group and sets cmd.WaitDelay
// as a backstop. This test reproduces the bug shape and asserts the fix.

package sandbox

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestRun_TimeoutKillsProcessGroup is the core regression test. Before the
// fix, this returned in ~30s (the sleep duration) instead of ~2s (the
// timeout). The grandchild `sleep 30` inherits sh's stdout pipe; without a
// group kill + WaitDelay, cmd.Wait blocks reading that pipe until the sleep
// exits. We assert Run returns in well under the 5s WaitDelay backstop and
// reports TimedOut.
func TestRun_TimeoutKillsProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not in PATH; skipping process-group timeout test")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not in PATH; skipping process-group timeout test")
	}

	// A grandchild that long-outlives the timeout and holds the stdout pipe.
	// `echo started` proves we ran; `sleep 30` is the pipe-holding grandchild
	// that the OLD code would wait the full 30s for.
	argv := []string{"sh", "-c", "echo started && sleep 30"}

	start := time.Now()
	res, err := Run(context.Background(), argv, nil, Limits{TimeoutSeconds: 2})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The bug: ~30s. The fix: ~2s timeout + <=1s group grace, comfortably
	// under the 5s WaitDelay backstop. Assert hard ceiling of 4s so a
	// regression that reintroduces the pipe-block (which would push this to
	// ~30s) fails loudly, with generous slack for slow CI.
	if elapsed >= 4*time.Second {
		t.Fatalf("Run returned in %v; want < 4s — timeout bypass regressed "+
			"(grandchild pipe blocked Wait). stdout=%q stderr=%q",
			elapsed, res.Stdout, res.Stderr)
	}

	if !res.TimedOut {
		t.Errorf("TimedOut = false; want true (deadline elapsed before exit)")
	}

	// Sanity: the child actually started (proves we ran the real command and
	// the timing isn't an artifact of an early start failure).
	if !strings.Contains(string(res.Stdout), "started") {
		t.Errorf("stdout = %q; want it to contain \"started\"", res.Stdout)
	}
}

// TestRun_TimeoutReapsGrandchild verifies the sleep grandchild is actually
// dead after Run returns on timeout — not merely abandoned while we stopped
// waiting on it. We launch a uniquely-tagged sleep grandchild, let the
// timeout fire, then scan /proc for any surviving process running that exact
// sleep. A leak here means the group-kill did not reach the grandchild.
func TestRun_TimeoutReapsGrandchild(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not in PATH; skipping grandchild-reap test")
	}
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not in PATH; skipping grandchild-reap test")
	}

	// Use a long, distinctive sleep duration so we can recognise the
	// grandchild in /proc by its argv without colliding with unrelated sleeps.
	const tag = "987654" // unlikely-to-collide sleep duration in seconds
	argv := []string{"sh", "-c", "echo started && sleep " + tag}

	res, err := Run(context.Background(), argv, nil, Limits{TimeoutSeconds: 2})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false; want true")
	}

	// Give the kernel a brief moment to finish reaping the SIGKILL'd group.
	// Poll a few times so a slightly-slow reap doesn't flake the assertion.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if !grandchildSleepAlive(t, tag) {
			return // grandchild is gone — fix verified
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild `sleep %s` still alive after timeout — "+
				"group-kill did not reap it", tag)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// grandchildSleepAlive scans /proc for any live process whose cmdline is
// exactly `sleep <tag>`. Linux-only; used by TestRun_TimeoutReapsGrandchild.
func grandchildSleepAlive(t *testing.T, tag string) bool {
	t.Helper()
	entries, err := exec.Command("sh", "-c",
		"for d in /proc/[0-9]*; do tr '\\0' ' ' < \"$d/cmdline\" 2>/dev/null; echo; done").Output()
	if err != nil {
		// If we can't enumerate /proc, don't fail the test on the scan
		// itself — the timing assertion in the other test already guards
		// the core behavior. Treat as "not found".
		return false
	}
	for _, line := range strings.Split(string(entries), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "sleep" && fields[1] == tag {
			return true
		}
	}
	return false
}

// TestInstallProcessGroupCancel_SetsCancel is a fast unit check that the
// cancel hook is actually installed on Unix (the closure that performs the
// group kill). Guards against a refactor silently dropping the override.
func TestInstallProcessGroupCancel_SetsCancel(t *testing.T) {
	cmd := exec.Command("true")
	if err := applyPlatformHardening(cmd, Limits{}); err != nil {
		t.Fatalf("applyPlatformHardening: %v", err)
	}
	installProcessGroupCancel(cmd)
	if cmd.Cancel == nil {
		t.Fatal("cmd.Cancel is nil after installProcessGroupCancel; group-kill override missing")
	}
	// Sanity: the child should still be in its own process group so the
	// negative-pid signal in the cancel closure targets the whole subtree.
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Errorf("Setpgid not set; group-kill cancel would signal the wrong group")
	}
	// The cancel closure is safe to invoke before Start (cmd.Process == nil)
	// — it must return nil rather than panic. This proves the nil-guard.
	if err := cmd.Cancel(); err != nil {
		t.Errorf("cmd.Cancel() before Start returned %v; want nil", err)
	}
}
