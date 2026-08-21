//go:build linux

// Package sandbox_test — subprocess-level proof that the per-turn
// workspace-rooted filesystem (agent-CoreTeam-membership-driven — see
// workspace.FindForAgent) does NOT require widening the kernel sandbox.
//
// STEP-0 invariant under test: the boot Landlock policy grants RWX on the whole
// $OMNIPUS_HOME (DefaultPolicy), and re-routing an exec child's cwd to
// $OMNIPUS_HOME/workspaces/<id>/ stays inside that grant. So a child under
// sandbox=enforce must be able to write into the re-rooted workspace dir, while
// still being denied write OUTSIDE $OMNIPUS_HOME. This subprocess test forks the
// test binary, applies DefaultPolicy($OMNIPUS_HOME) in the child, and verifies
// both halves of that claim.

package sandbox_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// Child exit-code convention (shared with the other Landlock subprocess tests):
//
//	42 — both halves passed: write INSIDE workspaces/<id>/ succeeded AND write
//	     OUTSIDE $OMNIPUS_HOME was blocked with EACCES/EPERM. (enforcement OK)
//	77 — Landlock unavailable / ABI rejected: parent skips.
//	 1 — write inside workspace was blocked (would be a false-positive denial,
//	     i.e. the re-root broke even though it should be inside the grant).
//	 2 — write outside home unexpectedly SUCCEEDED (sandbox escape — failure).
//	 3 — unexpected error setting up the test (env problem).
func TestLandlock_WorkspaceReroot_StaysInsideBootGrant(t *testing.T) {
	if os.Getenv("OMNIPUS_LANDLOCK_REROOT_CHILD") == "1" {
		runWorkspaceRerootChild()
		return // unreachable — child calls os.Exit
	}

	_, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		t.Skipf("Landlock backend not available (backend=%q) — skipping", name)
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock)")
	}

	home := t.TempDir()
	// Pre-create the workspace subtree the child will write into, mirroring the
	// loop's os.MkdirAll(workspaces/<id>) before the turn runs.
	wsDir := filepath.Join(home, "workspaces", "ws-1")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir wsDir: %v", err)
	}
	// An "outside" dir that must be OUTSIDE *every* DefaultPolicy grant so the
	// kernel denies a write there. It CANNOT come from t.TempDir(): that lands
	// under /tmp, which DefaultPolicy grants RWX as scratch space (see
	// sandbox.go's "/tmp" rule) — a write there would (correctly) succeed and
	// wrongly read as a sandbox escape. The user's home dir is not granted by
	// DefaultPolicy, so a write there is genuinely denied by Landlock.
	// If we cannot establish a dir that is both writable AND outside every grant
	// (HOME unset, or HOME itself under the granted /tmp), SKIP rather than fail:
	// the invariant simply cannot be probed there, and a hard failure would be a
	// false negative about enforcement.
	outsideBase, homeErr := os.UserHomeDir()
	if homeErr != nil || outsideBase == "" {
		t.Skip("no home dir to place an out-of-sandbox probe dir — cannot probe enforcement")
	}
	if base := filepath.Clean(outsideBase); base == "/tmp" || strings.HasPrefix(base+"/", "/tmp/") {
		t.Skipf("home dir %q is under the granted /tmp scratch space — cannot probe an out-of-grant write", outsideBase)
	}
	outside, mkErr := os.MkdirTemp(outsideBase, "omnipus-landlock-outside-")
	if mkErr != nil {
		t.Skipf("cannot create an out-of-sandbox probe dir under %q: %v", outsideBase, mkErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outside) })

	//nolint:gosec // intentional test-binary self-exec
	cmd := exec.Command(os.Args[0],
		"-test.run=^TestLandlock_WorkspaceReroot_StaysInsideBootGrant$",
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"OMNIPUS_LANDLOCK_REROOT_CHILD=1",
		"OMNIPUS_REROOT_HOME="+home,
		"OMNIPUS_REROOT_WS="+wsDir,
		"OMNIPUS_REROOT_OUTSIDE="+outside,
	)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("child failed to run: %v\n%s", err, out)
		}
	}

	switch exitCode {
	case 42:
		t.Logf("re-root stays inside boot grant: write inside workspace allowed, write outside home denied\n%s", out)
	case 77:
		t.Skipf("Landlock unavailable in child (exit 77):\n%s", out)
	case 1:
		t.Fatalf("write INSIDE re-rooted workspace was wrongly DENIED — re-root broke a legitimate write:\n%s", out)
	case 2:
		t.Fatalf("write OUTSIDE $OMNIPUS_HOME unexpectedly SUCCEEDED — kernel sandbox escape:\n%s", out)
	default:
		t.Fatalf("child exit %d (expected 42):\n%s", exitCode, out)
	}
}

func runWorkspaceRerootChild() {
	// runtime.LockOSThread is required: Landlock's landlock_restrict_self only
	// restricts the calling thread. Without locking, Go can migrate this
	// goroutine to a different OS thread between Apply and the two writes
	// below, and a write would then happen on an unrestricted thread — most
	// dangerously the "outside" write, which would wrongly appear to succeed
	// even with Landlock applied. See the identical rationale on
	// runLandlockBindBlockedChild in backend_linux_subprocess_test.go.
	runtime.LockOSThread()

	home := os.Getenv("OMNIPUS_REROOT_HOME")
	wsDir := os.Getenv("OMNIPUS_REROOT_WS")
	outside := os.Getenv("OMNIPUS_REROOT_OUTSIDE")
	if home == "" || wsDir == "" || outside == "" {
		fmt.Fprintln(os.Stderr, "missing env for reroot child")
		os.Exit(3)
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		os.Exit(77)
	}

	// Apply the SAME policy the gateway applies at boot: RWX on all of
	// $OMNIPUS_HOME (plus the read-only system paths). workspaces/<id>/ is a
	// child of home, so it inherits the RWX grant; nothing widens the sandbox
	// to the re-rooted dir specifically.
	policy := sandbox.DefaultPolicy(home, nil, nil, nil, nil)
	if err := backend.Apply(policy); err != nil {
		// ABI mismatch / kernel rejection → skip, not failure.
		fmt.Fprintf(os.Stderr, "Apply failed (treating as skip): %v\n", err)
		os.Exit(77)
	}

	// Half 1: writing into workspaces/<id>/ (the re-rooted cwd) must succeed.
	insideFile := filepath.Join(wsDir, "exec-output.txt")
	if err := os.WriteFile(insideFile, []byte("ok"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write INSIDE workspace failed (should be allowed): %v\n", err)
		os.Exit(1)
	}

	// Half 2: writing OUTSIDE $OMNIPUS_HOME must be denied by the kernel.
	outsideFile := filepath.Join(outside, "escape.txt")
	err := os.WriteFile(outsideFile, []byte("escape"), 0o644)
	if err == nil {
		fmt.Fprintf(os.Stderr, "write OUTSIDE home unexpectedly succeeded: %s\n", outsideFile)
		os.Exit(2)
	}
	if !errors.Is(err, syscall.EACCES) && !errors.Is(err, syscall.EPERM) {
		// A non-permission error (e.g. ENOENT) would not prove Landlock is the
		// cause. Treat as skip so we never mis-report enforcement.
		fmt.Fprintf(os.Stderr, "write outside home failed with non-EACCES error (skip): %v\n", err)
		os.Exit(77)
	}

	fmt.Fprintf(os.Stderr, "inside-allowed + outside-denied confirmed (outside err: %v)\n", err)
	os.Exit(42)
}
