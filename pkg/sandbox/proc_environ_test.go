//go:build linux

// T2.7: ChildCannotReadGatewayProcEnviron.
//
// Documents gap C6 from the pentest report: a hardened child process can read
// /proc/<parent-pid>/environ because Linux grants same-uid processes read access
// to /proc/<pid>/environ unless PR_SET_DUMPABLE is set to 0.
//
// CLOSED in v0.2 #155: sandbox.HardenGatewaySelf() applies PR_SET_DUMPABLE=0
// at gateway boot, which makes /proc/<gateway-pid>/{environ,mem,maps,...}
// owned by root and unreadable even by other same-uid processes. The test
// now calls HardenGatewaySelf in the parent before spawning the child, so
// the child gets EACCES on the environ read.

package sandbox_test

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// TestChildCannotReadGatewayProcEnviron (T2.7) spawns a subprocess that
// attempts to read /proc/<parent-pid>/environ (the test process acting as
// the "gateway" parent). The test asserts EACCES or EPERM from the kernel.
//
// Expected outcome today: FAIL (the child CAN read the parent's environ).
// Expected outcome after v0.2 C6 fix: PASS.
//
// The test is structured as a subprocess re-exec using the same env-sentinel
// pattern as TestLandlock_ApplySubprocess to avoid permanently restricting
// the parent test process.
func TestChildCannotReadGatewayProcEnviron(t *testing.T) {
	t.Logf("documents C6 from pentest report: same-uid /proc/<pid>/environ readable by children")

	if os.Getenv("OMNIPUS_PROC_ENVIRON_CHILD") == "1" {
		runProcEnvironChild()
		return // unreachable — runProcEnvironChild calls os.Exit
	}

	if os.Getuid() == 0 {
		t.Skip("proc-environ test must run as non-root (root can always read /proc)")
	}

	// Apply the production self-hardening in this (parent) test process
	// before forking the child. In production this is called by the
	// gateway boot path (pkg/gateway/sandbox_apply.go); here we mirror it
	// so the test exercises the same defense.
	if err := sandbox.HardenGatewaySelf(); err != nil {
		t.Fatalf("HardenGatewaySelf failed: %v", err)
	}

	parentPID := os.Getpid()

	//nolint:gosec // intentional test-binary self-exec
	cmd := exec.Command(os.Args[0],
		"-test.run=TestChildCannotReadGatewayProcEnviron",
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"OMNIPUS_PROC_ENVIRON_CHILD=1",
		fmt.Sprintf("OMNIPUS_PROC_ENVIRON_PARENT_PID=%d", parentPID),
	)
	// Use a process group so child cleanup is clean.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	out, err := cmd.CombinedOutput()
	var exitCode int
	if err == nil {
		exitCode = 0
	} else if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else {
		t.Fatalf("child process failed to run: %v\n%s", err, out)
	}

	switch exitCode {
	case 0:
		// Child reported EACCES/EPERM — gap is closed.
		t.Logf("C6 closed: child could not read parent /proc/%d/environ (PR_SET_DUMPABLE=0 enforced)",
			parentPID)
	case 1:
		// Child successfully read the parent's environ — REGRESSION.
		t.Errorf("C6 REGRESSION: child read parent /proc/%d/environ despite "+
			"PR_SET_DUMPABLE=0:\n%s", parentPID, out)
	case 77:
		t.Skipf("/proc not mounted or parent PID not passed (child exit 77):\n%s", out)
	default:
		t.Fatalf("child exit %d (expected 0=blocked, 1=regression, 77=skip):\n%s",
			exitCode, out)
	}
}

// runProcEnvironChild is the child-mode implementation. Communicates via exit codes:
//
//	0  — could NOT read parent environ (EACCES/EPERM) — gap is fixed
//	1  — successfully read parent environ — gap is still open
//	77 — /proc unavailable or parent PID missing
func runProcEnvironChild() {
	pidStr := os.Getenv("OMNIPUS_PROC_ENVIRON_PARENT_PID")
	if pidStr == "" {
		os.Exit(77)
	}
	parentPID, err := strconv.Atoi(pidStr)
	if err != nil || parentPID <= 0 {
		fmt.Fprintf(os.Stderr, "invalid OMNIPUS_PROC_ENVIRON_PARENT_PID=%q\n", pidStr)
		os.Exit(77)
	}

	path := fmt.Sprintf("/proc/%d/environ", parentPID)
	_, readErr := os.ReadFile(path)
	if readErr == nil {
		// Successfully read — gap is still open (documented failure).
		fmt.Fprintf(os.Stderr, "C6: child read %s successfully — gap still open\n", path)
		os.Exit(1)
	}

	// Check for EACCES or EPERM — the expected outcome after the fix.
	if isEACCESErr(readErr) {
		fmt.Fprintf(os.Stderr, "C6: child got permission denied on %s — gap fixed: %v\n", path, readErr)
		os.Exit(0)
	}

	// Some other error (ENOENT means the parent exited before child ran).
	if os.IsNotExist(readErr) {
		fmt.Fprintf(os.Stderr, "parent PID %d no longer exists; skip\n", parentPID)
		os.Exit(77)
	}
	fmt.Fprintf(os.Stderr, "unexpected error reading %s: %v\n", path, readErr)
	os.Exit(2)
}

func isEACCESErr(err error) bool {
	return err != nil && (os.IsPermission(err))
}
