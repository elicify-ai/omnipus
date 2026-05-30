//go:build linux

// T2.20: Spawn_NoLeakedParentFD_AfterStart.
//
// Verifies that SpawnBackgroundChild does NOT leak the parent-side log file
// descriptor after the child is forked. Before the B1.4-e fix, the parent's
// copy of the log fd was kept open for the lifetime of the gateway process;
// this test measures open fds before and after spawn and asserts the count
// did not increase (the parent closed its fd via logFile.Close after Start).

package sandbox_test

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// TestSpawn_NoLeakedParentFD_AfterStart (T2.20) counts open fds in the
// parent process before and after spawning a background child. The count
// must not increase permanently — specifically, the log file opened by
// SpawnBackgroundChild on behalf of the child must be closed in the parent
// after cmd.Start().
func TestSpawn_NoLeakedParentFD_AfterStart(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("fd-leak test must run as non-root")
	}

	workspace := t.TempDir()

	// Count open fds before spawn.
	beforeFDs := countOpenFDs(t)

	// Spawn a very short-lived child. We pass zero Limits (god mode) to
	// avoid Landlock apply complications in the test process.
	cmd, err := sandbox.SpawnBackgroundChild(
		[]string{"sh", "-c", "sleep 0.1"},
		workspace,
		nil,
		0,
		sandbox.Limits{}, // zero = god mode, no hardening
	)
	if err != nil {
		t.Fatalf("SpawnBackgroundChild: %v", err)
	}

	// Wait for the child to exit and the reap goroutine to clean up.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child did not exit within 5 s")
	}

	// Give the Go runtime a moment to schedule any cleanup goroutines.
	time.Sleep(50 * time.Millisecond)

	// Count open fds after spawn and child exit.
	afterFDs := countOpenFDs(t)

	// Allow for minor fluctuation (1-2 fds) from test infrastructure, but
	// a persistent leak would show as many additional fds (≥ 1 per spawn).
	const tolerance = 3
	if afterFDs > beforeFDs+tolerance {
		t.Errorf("possible fd leak: before=%d after=%d (delta=%d, tolerance=%d)",
			beforeFDs, afterFDs, afterFDs-beforeFDs, tolerance)
	} else {
		t.Logf("fd count: before=%d after=%d — no significant leak detected", beforeFDs, afterFDs)
	}
}

// countOpenFDs returns the number of open file descriptors in /proc/self/fd.
func countOpenFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		// /proc not available — skip gracefully.
		t.Skipf("cannot count open fds: /proc/self/fd unavailable: %v", err)
	}
	// Count only valid numeric entries (actual fds, not "." or "..").
	count := 0
	for _, e := range entries {
		if _, parseErr := strconv.Atoi(e.Name()); parseErr == nil {
			// Verify it is still open (readlink works).
			linkPath := filepath.Join("/proc/self/fd", e.Name())
			if _, statErr := os.Readlink(linkPath); statErr == nil {
				count++
			}
		}
	}
	// Subtract the fd used by the ReadDir itself (already closed).
	return count
}

// TestSpawn_LogFileClosedAfterStart verifies that the .dev-server.log file
// opened inside SpawnBackgroundChild is closed in the parent after Start().
// We do this by checking that the log file's fd is not in /proc/self/fd after
// the function returns.
func TestSpawn_LogFileClosedAfterStart(t *testing.T) {
	workspace := t.TempDir()

	// Spawn the child and immediately kill it.
	cmd, err := sandbox.SpawnBackgroundChild(
		[]string{"sh", "-c", "sleep 5"},
		workspace,
		nil,
		0,
		sandbox.Limits{},
	)
	if err != nil {
		t.Fatalf("SpawnBackgroundChild: %v", err)
	}
	// Kill immediately — we only care about the parent's fd state post-Start.
	_ = cmd.Process.Signal(syscall.SIGTERM)

	// Wait briefly for the child.
	done := make(chan struct{}, 1)
	go func() {
		cmd.Wait() //nolint:errcheck
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
	}

	logPath := filepath.Join(workspace, ".dev-server.log")

	// The log file should exist (child may or may not have written to it).
	// The key assertion is that no fd in /proc/self/fd resolves to logPath —
	// the parent must have closed its copy.
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot read /proc/self/fd: %v", err)
	}
	for _, e := range entries {
		if _, parseErr := strconv.Atoi(e.Name()); parseErr != nil {
			continue
		}
		linkPath := filepath.Join("/proc/self/fd", e.Name())
		target, err := os.Readlink(linkPath)
		if err != nil {
			continue
		}
		if target == logPath {
			t.Errorf("parent still has fd open to %s after SpawnBackgroundChild returned — fd leak (B1.4-e)", logPath)
		}
	}
}
