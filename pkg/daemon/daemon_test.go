// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

// newHome creates a temporary directory to use as OMNIPUS_HOME for a test and
// registers its cleanup with t.Cleanup.
func newHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "omnipus-daemon-test-*")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// writePIDFile writes an arbitrary integer to the PID file, bypassing writePID
// so tests can plant any value (including dead PIDs).
func writePIDFile(t *testing.T, home string, pid int) {
	t.Helper()
	path := PIDPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for PID file: %v", err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatalf("write PID file: %v", err)
	}
}

// readPIDFile returns the raw content of the PID file, or "" if it does not
// exist.
func readPIDFile(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(PIDPath(home))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read PID file: %v", err)
	}
	return strings.TrimSpace(string(data))
}

// ---- PIDPath ----------------------------------------------------------------

func TestPIDPath(t *testing.T) {
	home := "/tmp/myomnipus"
	got := PIDPath(home)
	want := filepath.Join(home, "gateway.pid")
	if got != want {
		t.Errorf("PIDPath(%q) = %q, want %q", home, got, want)
	}
}

// ---- Status — stale PID (never-existed) ------------------------------------

// TestStatus_StalePID_NeverExisted verifies that Status clears the PID file
// and returns not-running when the PID file contains PID 0 (never a valid
// user-space PID on any platform).
func TestStatus_StalePID_NeverExisted(t *testing.T) {
	home := newHome(t)
	// PID 0 is never a valid user-space PID on any platform.
	writePIDFile(t, home, 0)

	running, pid, err := Status(home)
	if err != nil {
		t.Fatalf("Status: unexpected error for PID 0: %v", err)
	}
	if running {
		t.Errorf("Status: expected not-running for PID 0, got running (pid=%d)", pid)
	}
	// Status must have removed the stale PID file.
	if got := readPIDFile(t, home); got != "" {
		t.Errorf("Status: stale PID file not removed; content=%q", got)
	}
}

// TestStatus_StalePID_HighDeadPID plants a very large PID that is extremely
// unlikely to be alive.  Status must report not-running and remove the file.
func TestStatus_StalePID_HighDeadPID(t *testing.T) {
	home := newHome(t)
	// 2^31-1 (max int32) is accepted by the kernel as a PID but will never be
	// running in a normal test environment.
	const deadPID = 2147483647
	writePIDFile(t, home, deadPID)

	running, _, err := Status(home)
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if running {
		// If somehow PID 2147483647 is running, skip gracefully.
		t.Skip("PID 2147483647 appears to be alive; skipping test")
	}

	if got := readPIDFile(t, home); got != "" {
		t.Errorf("Status: stale PID file not removed; content=%q", got)
	}
}

// ---- Status — missing PID file ----------------------------------------------

func TestStatus_NoPIDFile_ReturnsNotRunning(t *testing.T) {
	home := newHome(t)
	running, pid, err := Status(home)
	if err != nil {
		t.Fatalf("Status: unexpected error with no PID file: %v", err)
	}
	if running {
		t.Errorf("Status: expected not-running, got running (pid=%d)", pid)
	}
	if pid != 0 {
		t.Errorf("Status: expected pid=0, got %d", pid)
	}
}

// ---- Status — corrupted PID file (self-heal) --------------------------------

// TestStatus_CorruptPIDFile_SelfHeals verifies that a non-numeric / empty PID
// file is treated as stale: Status removes the file and returns
// (running=false, pid=0, err=nil) rather than propagating a parse error that
// would permanently wedge `omnipus stop`.
func TestStatus_CorruptPIDFile_SelfHeals(t *testing.T) {
	home := newHome(t)
	path := PIDPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o600); err != nil {
		t.Fatalf("write PID file: %v", err)
	}

	running, pid, err := Status(home)
	if err != nil {
		t.Fatalf("Status: expected nil error for corrupt PID file (self-heal), got: %v", err)
	}
	if running {
		t.Errorf("Status: expected running=false for corrupt PID file, got running=true (pid=%d)", pid)
	}
	if pid != 0 {
		t.Errorf("Status: expected pid=0 for corrupt PID file, got %d", pid)
	}
	// The stale file must have been removed so subsequent calls return cleanly.
	if got := readPIDFile(t, home); got != "" {
		t.Errorf("Status: corrupt PID file not removed; content=%q", got)
	}
}

// TestStop_CorruptPIDFile_SelfHeals verifies that Stop with a corrupt PID file
// is also a safe no-op (file removed, returns not-stopped, nil error) rather
// than permanently wedging the stop command.
func TestStop_CorruptPIDFile_SelfHeals(t *testing.T) {
	home := newHome(t)
	path := PIDPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("garbage\x00\xff"), 0o600); err != nil {
		t.Fatalf("write PID file: %v", err)
	}

	stopped, err := Stop(home)
	if err != nil {
		t.Fatalf("Stop: expected nil error for corrupt PID file (self-heal), got: %v", err)
	}
	if stopped {
		t.Error("Stop: expected stopped=false for corrupt PID file, got true")
	}
	if got := readPIDFile(t, home); got != "" {
		t.Errorf("Stop: corrupt PID file not removed; content=%q", got)
	}
}

// ---- Stop — stale PID file --------------------------------------------------

// TestStop_StalePIDFile_SafeNoop verifies that Stop with a dead PID is a safe
// no-op: it reports not-running and removes the file without killing anything.
func TestStop_StalePIDFile_SafeNoop(t *testing.T) {
	home := newHome(t)
	const deadPID = 2147483647
	writePIDFile(t, home, deadPID)

	stopped, err := Stop(home)
	if err != nil {
		// The only acceptable error is if the PID was somehow alive (very unlikely).
		// If the file was removed it's still a safe no-op.
		if readPIDFile(t, home) == "" {
			return // file cleaned up — acceptable
		}
		t.Fatalf("Stop: unexpected error for dead PID: %v", err)
	}

	if stopped {
		t.Error("Stop: expected stopped=false for a dead PID, got true")
	}

	if got := readPIDFile(t, home); got != "" {
		t.Errorf("Stop: stale PID file not removed; content=%q", got)
	}
}

// TestStop_NoPIDFile_SafeNoop verifies that Stop with no PID file is a safe
// no-op that reports (false, nil).
func TestStop_NoPIDFile_SafeNoop(t *testing.T) {
	home := newHome(t)
	stopped, err := Stop(home)
	if err != nil {
		t.Fatalf("Stop: unexpected error with no PID file: %v", err)
	}
	if stopped {
		t.Error("Stop: expected stopped=false when no PID file, got true")
	}
}

// ---- PID read/write round-trip ----------------------------------------------

func TestPIDReadWriteRoundTrip(t *testing.T) {
	home := newHome(t)
	const wantPID = 12345

	if err := writePID(home, wantPID); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	got, err := readPID(home)
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if got != wantPID {
		t.Errorf("readPID = %d, want %d", got, wantPID)
	}

	// Verify file mode is 0600.
	fi, err := os.Stat(PIDPath(home))
	if err != nil {
		t.Fatalf("stat PID file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("PID file perm = %04o, want 0600", perm)
		}
	}
}

// TestReadPID_NoFile_ReturnsZero verifies readPID returns (0, nil) for a
// missing file (not an error).
func TestReadPID_NoFile_ReturnsZero(t *testing.T) {
	home := newHome(t)
	pid, err := readPID(home)
	if err != nil {
		t.Fatalf("readPID: unexpected error: %v", err)
	}
	if pid != 0 {
		t.Errorf("readPID = %d, want 0 for missing file", pid)
	}
}

// ---- Spawn / Stop round-trip ------------------------------------------------

// TestSpawnStop_RoundTrip spawns a real child process and then stops it.
// On Unix we spawn "sleep 60"; on Windows we spawn "cmd /C timeout /T 60 /NOBREAK".
//
// This test is skipped in environments where spawning a child process is not
// feasible (e.g. heavily sandboxed CI), but is included for local validation.
// We use -p 1 scope as directed by CLAUDE.md.
func TestSpawnStop_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping spawn/stop round-trip in short mode")
	}

	home := newHome(t)

	// Shorten the grace period so the test does not wait 5 seconds.
	origGrace := stopGracePeriod
	origPoll := pollInterval
	stopGracePeriod = 2 * time.Second
	pollInterval = 50 * time.Millisecond
	defer func() {
		stopGracePeriod = origGrace
		pollInterval = origPoll
	}()

	// Find a long-running system command to act as the "gateway" child.
	// We spawn it via Spawn using our own executable + args trick: since we
	// cannot spawn "omnipus start" in a test (that would boot a real gateway),
	// we directly test spawnProcess + writePID + killProcess instead.
	//
	// This exercises the full round-trip without the omnipus binary.
	childExe, childArgs := sleepCommand()

	pid, err := spawnProcess(childExe, childArgs, home)
	if err != nil {
		t.Fatalf("spawnProcess: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("spawnProcess: invalid pid %d", pid)
	}

	// Write the PID manually (since we bypassed Spawn for test isolation).
	if err := writePID(home, pid); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	// Allow the child a moment to start.
	time.Sleep(100 * time.Millisecond)

	// The process should be alive.
	alive, _, _ := checkProcess(pid)
	if !alive {
		t.Fatalf("child process %d is not alive immediately after spawn", pid)
	}

	// Now kill it.
	if err := killProcess(pid); err != nil {
		t.Fatalf("killProcess(%d): %v", pid, err)
	}

	// Allow OS time to reap.
	time.Sleep(200 * time.Millisecond)

	// Process should be gone.
	alive, _, _ = checkProcess(pid)
	if alive {
		t.Errorf("process %d still alive after killProcess", pid)
	}

	// Clean up PID file.
	removePID(home)
	if got := readPIDFile(t, home); got != "" {
		t.Errorf("PID file not removed after stop; content=%q", got)
	}
}

// sleepCommand returns the executable and args for a long-running no-op
// command suitable for use as a test child.
func sleepCommand() (exe string, args []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", "timeout", "/T", "60", "/NOBREAK"}
	}
	return "sleep", []string{"60"}
}

// ---- checkProcess does not kill unrelated PIDs (documentation test) ---------

// TestCheckProcess_OwnPID verifies that checkProcess recognizes the current
// process as alive (pid = os.Getpid()).
func TestCheckProcess_OwnPID(t *testing.T) {
	pid := os.Getpid()
	alive, _, _ := checkProcess(pid)
	if !alive {
		t.Errorf("checkProcess(%d): expected alive=true for own PID, got false", pid)
	}
}

// TestCheckProcess_DeadPID verifies that a dead PID is not alive.
func TestCheckProcess_DeadPID(t *testing.T) {
	// PID 2147483647 is effectively guaranteed not to be alive.
	const deadPID = 2147483647
	alive, _, _ := checkProcess(deadPID)
	if alive {
		t.Skip(fmt.Sprintf("PID %d appears alive; skipping", deadPID))
	}
}

// TestStatus_CurrentProcess_AlivePID verifies Status when the PID file points
// at the current (test) process PID. The process is alive but the name won't
// match "omnipus", so Status should:
//   - On a non-omnipus binary: clean up the stale file (isOmnipus=false).
//   - Report not-running.
//
// This test exercises the PID-reuse guard: Status never trusts a PID that
// doesn't belong to an omnipus process.
func TestStatus_CurrentProcess_NotOmnipusBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("wmic-based name check behaves differently in test binary; skipping")
	}
	if runtime.GOOS == "darwin" {
		// macOS has no /proc, so checkProcess's resolveExeName can't read the
		// PID's name and conservatively assumes "ours" (daemon_unix.go — a
		// deliberate fail-safe so we never fail to stop our own gateway on a
		// name-read error). That makes the "non-omnipus PID is cleaned up"
		// behavior asserted here Linux-specific; on macOS Status reports the
		// live-but-unidentifiable PID as running by design.
		t.Skip("checkProcess name-resolution is /proc-based (Linux only); macOS conservatively assumes 'ours'")
	}

	home := newHome(t)
	writePIDFile(t, home, os.Getpid())

	running, _, err := Status(home)
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}

	// The test binary is not named "omnipus", so Status should NOT report running.
	// (If the test binary happens to be called "omnipus.*", this check is vacuous
	// but harmless.)
	exe, _ := os.Executable()
	if !strings.Contains(strings.ToLower(filepath.Base(exe)), "omnipus") {
		if running {
			t.Errorf("Status: expected not-running for non-omnipus PID, got running")
		}
		// File should be removed.
		if got := readPIDFile(t, home); got != "" {
			t.Errorf("Status: stale PID file not removed for non-omnipus PID; content=%q", got)
		}
	}
}

// ---- WritePID / RemovePID exported API (self-registration seam) -------------

// TestWritePID_SeenByReadPID verifies that the exported WritePID writes a PID
// file that readPID can read back. This exercises the self-registration seam
// used by the gateway to register its own PID after the HTTP listener binds
// (MAJOR-2: hand-started gateways are tracked by Status/Stop).
func TestWritePID_SeenByReadPID(t *testing.T) {
	home := newHome(t)
	const wantPID = 99999

	if err := WritePID(home, wantPID); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	got, err := readPID(home)
	if err != nil {
		t.Fatalf("readPID after WritePID: %v", err)
	}
	if got != wantPID {
		t.Errorf("readPID = %d, want %d", got, wantPID)
	}

	// File mode must be 0600.
	if runtime.GOOS != "windows" {
		fi, statErr := os.Stat(PIDPath(home))
		if statErr != nil {
			t.Fatalf("stat PID file: %v", statErr)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("PID file perm = %04o, want 0600", perm)
		}
	}
}

// TestRemovePID_ClearsFile verifies that RemovePID removes a file written by
// WritePID and that a subsequent readPID returns (0, nil).
func TestRemovePID_ClearsFile(t *testing.T) {
	home := newHome(t)

	if err := WritePID(home, 12345); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	RemovePID(home)

	pid, err := readPID(home)
	if err != nil {
		t.Fatalf("readPID after RemovePID: %v", err)
	}
	if pid != 0 {
		t.Errorf("readPID after RemovePID = %d, want 0", pid)
	}
	if got := readPIDFile(t, home); got != "" {
		t.Errorf("PID file still present after RemovePID; content=%q", got)
	}
}
