//go:build linux

// Linux-specific tests for hardened_exec. RLIMIT_AS enforcement is
// platform-specific (kernel-side); we exercise it here with a guaranteed-
// heavy allocation path.

package sandbox

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
)

// TestRun_LinuxSetsPgid asserts the SysProcAttr we apply ends up putting
// the child in a new process group. We can't easily test Pdeathsig from
// userland without faking parent death; the Setpgid bit is observable
// via getpgid syscall on the child — we simulate by inspecting the cmd
// before Start so the assertion is deterministic.
func TestRun_LinuxSetsPgid(t *testing.T) {
	cmd := exec.Command("true")
	if err := applyPlatformHardening(cmd, Limits{}); err != nil {
		t.Fatalf("applyPlatformHardening: %v", err)
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil after applyPlatformHardening")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Errorf("Setpgid = false; want true")
	}
	if cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
		t.Errorf("Pdeathsig = %v; want SIGTERM", cmd.SysProcAttr.Pdeathsig)
	}
}

// TestRun_LinuxMemoryLimitEnforced spawns a child that tries to allocate
// more than the configured RLIMIT_AS. The allocation should fail; the
// process should exit with non-zero status. We use perl since it's
// commonly present on Linux CI; skip if not available.
func TestRun_LinuxMemoryLimitEnforced(t *testing.T) {
	if _, err := exec.LookPath("perl"); err != nil {
		t.Skip("perl not in PATH; skipping memory cap probe")
	}
	// Cap to 64 MiB — the smallest documented BuildStatic floor.
	const capBytes = 64 * 1024 * 1024
	// Allocate 256 MiB of strings — should be rejected by RLIMIT_AS.
	script := `my $s = "x" x (256*1024*1024); print length($s);`

	res, err := Run(
		context.Background(),
		[]string{"perl", "-e", script},
		nil,
		Limits{TimeoutSeconds: 30, MemoryLimitBytes: capBytes},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// On RLIMIT_AS hit, perl typically panics with "Out of memory!" and
	// exits non-zero. We tolerate any non-zero exit since perl's exact
	// behavior varies by version.
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit code under RLIMIT_AS; got 0; stdout=%s stderr=%s",
			res.Stdout, res.Stderr)
	}
}
