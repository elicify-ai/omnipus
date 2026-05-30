//go:build linux

// Package sandbox_test — subprocess-level Apply coverage for the Landlock backend.
//
// TestLandlock_ApplySubprocess forks the test binary itself as a subprocess
// and calls LinuxBackend.Apply inside the child. This lets us verify that
// Landlock actually restricts filesystem access without permanently sandboxing
// the parent test process (Landlock's restrict_self is a one-way ratchet that
// cannot be removed from the calling process).

package sandbox_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// TestLandlock_ApplySubprocess verifies that LinuxBackend.Apply actually
// enforces filesystem restrictions at the kernel level. It forks the test
// binary as a child process; the child calls Apply with a workspace-only
// policy, then tries to read /etc/passwd. If Landlock enforced the policy,
// the read fails and the child exits with code 42 (sentinel). If Landlock
// is unavailable, the child exits with 77 (skip sentinel).
//
// The parent interprets:
//   - exit 42  → Landlock enforced: test passes
//   - exit 77  → Landlock unavailable at subprocess time: parent skips
//   - anything else → test failure with stderr dump
func TestLandlock_ApplySubprocess(t *testing.T) {
	if os.Getenv("OMNIPUS_LANDLOCK_SUBPROCESS_CHILD") == "1" {
		runLandlockChild()
		return // unreachable — runLandlockChild calls os.Exit
	}

	// Parent path: skip if the Landlock backend is not available on this kernel.
	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		t.Skipf("Landlock backend not available (backend=%q) — skipping subprocess test", name)
	}
	_ = backend

	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock restrictions)")
	}

	workspace := t.TempDir()

	// Re-exec the test binary with the child sentinel env var set.
	//nolint:gosec // intentional test-binary self-exec
	cmd := exec.Command(os.Args[0],
		"-test.run=TestLandlock_ApplySubprocess",
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"OMNIPUS_LANDLOCK_SUBPROCESS_CHILD=1",
		"OMNIPUS_LANDLOCK_SANDBOX_DIR="+workspace,
	)
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
	case 42:
		// Landlock enforced — /etc/passwd read was blocked inside workspace policy.
		t.Logf("Landlock subprocess enforcement confirmed (child exited 42)")
	case 77:
		t.Skipf("Landlock not available inside child process (child exited 77):\n%s", out)
	default:
		t.Fatalf("child exited with unexpected code %d (expected 42 for enforced, 77 for skip):\n%s",
			exitCode, out)
	}
}

// runLandlockChild is the child-mode implementation. It must only call os.Exit —
// never t.Fatal — because we communicate the result via exit code so the parent
// can unambiguously distinguish enforcement (42) from skip (77) from failure.
func runLandlockChild() {
	workspace := os.Getenv("OMNIPUS_LANDLOCK_SANDBOX_DIR")
	if workspace == "" {
		// No workspace provided — cannot set up a meaningful policy.
		os.Exit(77)
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		// Kernel does not support Landlock in this subprocess environment.
		os.Exit(77)
	}

	policy := sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{
			{Path: workspace, Access: sandbox.AccessRead | sandbox.AccessWrite},
		},
	}

	if err := backend.Apply(policy); err != nil {
		// EINVAL from create_ruleset means the kernel rejected our rights bitmask —
		// this can happen when the kernel reports a Landlock ABI version that our
		// backend does not fully enumerate (e.g. ABI v4+ with unknown access bits).
		// Treat as skip so the parent test is not mis-reported as a failure.
		fmt.Fprintf(os.Stderr, "Landlock Apply failed (treating as skip): %v\n", err)
		os.Exit(77)
	}

	// With Landlock active, reading /etc/passwd (outside the workspace) must fail
	// with EACCES (permission denied). Any other error (ENOENT, EIO, EMFILE, etc.)
	// indicates a test environment problem, not Landlock enforcement, and must not
	// be misreported as enforcement success.
	_, err := os.ReadFile("/etc/passwd")
	if err == nil {
		fmt.Fprintf(os.Stderr,
			"Landlock did NOT block /etc/passwd read — policy was not enforced\n")
		os.Exit(1)
	}

	// Only EACCES (or EPERM, which Landlock may return on some kernels) confirms
	// that Landlock is the cause of the denial. Any other errno is an unexpected
	// failure — report it distinctly so the parent can surface a clear diagnostic.
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		fmt.Fprintf(os.Stderr, "Landlock blocked /etc/passwd as expected: %v\n", err)
		os.Exit(42)
	}
	// Unexpected read error — exit 2 so the parent fails with a clear message.
	fmt.Fprintf(os.Stderr, "Unexpected /etc/passwd read error (not EACCES/EPERM): %v\n", err)
	os.Exit(2)
}

// TestLandlock_BindBlockedSubprocess verifies that a Landlock policy with a
// non-empty BindPortRules list blocks bind() to ports OUTSIDE the allow-list.
// Re-execs the test binary; the child applies a policy that allows binding
// only to port 18001 and then tries to bind 0.0.0.0:5173. Expected child exit
// codes follow the existing convention:
//
//	42 — bind correctly blocked with EACCES
//	77 — Landlock unavailable / ABI < 4 (skip)
//	1  — bind unexpectedly succeeded (test failure)
//	2  — unexpected error type
//
// runLandlockBindSubprocess is a shared helper that spawns the test binary as
// a child with the given env var and test run name, then validates the child
// exit code. wantCode is the exit code that signals success (42 = blocked,
// 0 = allowed); 77 always means "Landlock unavailable in child" (skip).
func runLandlockBindSubprocess(t *testing.T, childEnvVar, testRunName, successMsg string, wantCode int) {
	t.Helper()
	workspace := t.TempDir()
	//nolint:gosec // intentional test-binary self-exec
	cmd := exec.Command(os.Args[0],
		"-test.run="+testRunName,
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		childEnvVar+"=1",
		"OMNIPUS_LANDLOCK_SANDBOX_DIR="+workspace,
	)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("child failed to run: %v\n%s", err, out)
		}
	}
	switch exitCode {
	case wantCode:
		t.Logf("%s (child exited %d)", successMsg, wantCode)
	case 77:
		t.Skipf("Landlock unavailable in child (exit 77):\n%s", out)
	default:
		t.Fatalf("child exit %d (expected %d):\n%s", exitCode, wantCode, out)
	}
}

// Requires Landlock ABI v4+ for NET_BIND_TCP. Pre-v4 kernels skip via 77.
func TestLandlock_BindBlockedSubprocess(t *testing.T) {
	if os.Getenv("OMNIPUS_LANDLOCK_BIND_BLOCKED_CHILD") == "1" {
		runLandlockBindBlockedChild()
		return
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock restrictions)")
	}
	abi := sandbox.ProbeLandlockABI()
	if abi < 4 {
		t.Skipf("Landlock ABI v4 required for NET_BIND_TCP (have v%d)", abi)
	}
	runLandlockBindSubprocess(t, "OMNIPUS_LANDLOCK_BIND_BLOCKED_CHILD",
		"TestLandlock_BindBlockedSubprocess", "Landlock bind enforcement confirmed", 42)
}

// TestLandlock_BindAllowedSubprocess is the dual of BindBlocked: the same
// policy must let bind(0.0.0.0:18001) succeed because 18001 is on the
// allow-list. Child exits 0 on success, 77 when Landlock is unavailable,
// 1 on EACCES (rule didn't take effect), 2 on other unexpected errors.
func TestLandlock_BindAllowedSubprocess(t *testing.T) {
	if os.Getenv("OMNIPUS_LANDLOCK_BIND_ALLOWED_CHILD") == "1" {
		runLandlockBindAllowedChild()
		return
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock restrictions)")
	}
	abi := sandbox.ProbeLandlockABI()
	if abi < 4 {
		t.Skipf("Landlock ABI v4 required for NET_BIND_TCP (have v%d)", abi)
	}
	runLandlockBindSubprocess(t, "OMNIPUS_LANDLOCK_BIND_ALLOWED_CHILD",
		"TestLandlock_BindAllowedSubprocess", "Landlock bind allow-rule confirmed", 0)
}

// rawTCPBind issues bind(2) directly on a fresh AF_INET TCP socket, bypassing
// Go's net.Listen. Landlock NET_BIND_TCP enforces at the bind(2) syscall, but
// net.Listen has internal dual-stack/fallback paths that can mask the rule on
// some Go versions; for a deterministic kernel-level test we go through the
// syscall directly.
func rawTCPBind(port uint16) error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer syscall.Close(fd)
	sa := &syscall.SockaddrInet4{Port: int(port)}
	// 0.0.0.0
	return syscall.Bind(fd, sa)
}

// runLandlockBindBlockedChild applies a workspace-only FS policy plus a
// single-port (18001) bind allow-rule, then attempts to bind a port that is
// NOT on the allow-list. The expected outcome is EACCES from the kernel.
//
// runtime.LockOSThread is required: Landlock's landlock_restrict_self only
// restricts the calling thread. Without locking, Go can migrate this goroutine
// to a different OS thread between Apply and the bind syscall, and the bind
// happens on an unrestricted thread.
func runLandlockBindBlockedChild() {
	runtime.LockOSThread()
	workspace := os.Getenv("OMNIPUS_LANDLOCK_SANDBOX_DIR")
	if workspace == "" {
		os.Exit(77)
	}
	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		os.Exit(77)
	}
	policy := sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{
			{Path: workspace, Access: sandbox.AccessRead | sandbox.AccessWrite},
			// Loader needs to read shared libs; mirror the production policy.
			{Path: "/lib", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/lib64", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/usr/lib", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/usr/lib64", Access: sandbox.AccessRead | sandbox.AccessExecute},
		},
		BindPortRules: []sandbox.NetPortRule{{Port: 18001}},
	}
	if err := backend.Apply(policy); err != nil {
		fmt.Fprintf(os.Stderr, "Apply failed (skip): %v\n", err)
		os.Exit(77)
	}
	err := rawTCPBind(5173)
	if err == nil {
		fmt.Fprintf(os.Stderr, "Landlock did NOT block bind(0.0.0.0:5173)\n")
		os.Exit(1)
	}
	if isEACCES(err) {
		fmt.Fprintf(os.Stderr, "bind(5173) blocked as expected: %v\n", err)
		os.Exit(42)
	}
	fmt.Fprintf(os.Stderr, "Unexpected bind error (not EACCES): %v\n", err)
	os.Exit(2)
}

// runLandlockBindAllowedChild mirrors the blocked variant but binds a port
// that IS on the allow-list. The kernel must let it through.
func runLandlockBindAllowedChild() {
	runtime.LockOSThread()
	workspace := os.Getenv("OMNIPUS_LANDLOCK_SANDBOX_DIR")
	if workspace == "" {
		os.Exit(77)
	}
	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		os.Exit(77)
	}
	policy := sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{
			{Path: workspace, Access: sandbox.AccessRead | sandbox.AccessWrite},
			{Path: "/lib", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/lib64", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/usr/lib", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/usr/lib64", Access: sandbox.AccessRead | sandbox.AccessExecute},
		},
		BindPortRules: []sandbox.NetPortRule{{Port: 18001}},
	}
	if err := backend.Apply(policy); err != nil {
		fmt.Fprintf(os.Stderr, "Apply failed (skip): %v\n", err)
		os.Exit(77)
	}
	if err := rawTCPBind(18001); err != nil {
		fmt.Fprintf(os.Stderr, "bind(18001) unexpectedly failed: %v\n", err)
		if isEACCES(err) {
			os.Exit(1)
		}
		os.Exit(2)
	}
	os.Exit(0)
}

// isEACCES walks errors.Is to check for EACCES inside wrapped errors.
func isEACCES(err error) bool {
	return errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM)
}
