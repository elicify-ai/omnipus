//go:build darwin || linux

package preview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func mustProcessStart(t *testing.T, pid int) string {
	t.Helper()
	start, err := processStart(pid)
	require.NoError(t, err, "instrument: the process start time is readable")
	return start
}

// spawnGroupedSleep starts a real, harmless sleeper as a group leader in its
// own process group. The returned closer kills it and waits for the exit.
func spawnGroupedSleep(t *testing.T, seconds string) (pid int, start string, cleanup func()) {
	t.Helper()
	cmd := exec.Command("/bin/sleep", seconds)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	pid = cmd.Process.Pid
	start = mustProcessStart(t, pid)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	return pid, start, func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

// T-94 / §3.4 D4 (FR-051): recovery derives the stopped group from the
// VERIFIED launcher's PID, never from a stored number. A live launcher-like
// process holds a grandchild in its group; an unrelated harmless process lives
// in another group; the record carries a legacy pgid naming the unrelated
// group. After recovery the launcher and the grandchild are gone and the
// unrelated process lives. The grandchild is required so that deleting group
// termination altogether cannot pass. Unix only — Windows is T-99.
func TestPreviewRecovery_StopsOnlyTheVerifiedProcessGroup(t *testing.T) {
	launcherPID, launcherStart, closeLauncher := spawnGroupedSleep(t, "60")
	defer closeLauncher()
	// Grandchild: a child of the test started DIRECTLY into the launcher-like
	// group (Pgid at spawn — no setpgid-after-exec race).
	grandchild := exec.Command("/bin/sleep", "60")
	grandchild.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: launcherPID}
	require.NoError(t, grandchild.Start())
	grandchildDone := make(chan struct{})
	go func() { _ = grandchild.Wait(); close(grandchildDone) }()
	defer func() {
		_ = syscall.Kill(grandchild.Process.Pid, syscall.SIGKILL)
		select {
		case <-grandchildDone:
		case <-time.After(5 * time.Second):
		}
	}()

	unrelatedPID, _, closeUnrelated := spawnGroupedSleep(t, "60")
	defer closeUnrelated()

	// A record the gateway could have written — plus the legacy pgid key that
	// names the UNRELATED group. Every other fact matches the live launcher.
	dir := filepath.Join(scratchRoot(t.TempDir()), "legacy-pgid")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	record, err := json.Marshal(ownerRecord{
		State:        stateRunning,
		GatewayPID:   os.Getpid(),
		GatewayStart: "not-current-start",
		Launcher:     &procIdentity{PID: launcherPID, Start: launcherStart, Executable: "/bin/sleep"},
		PGID:         unrelatedPID,
		Executables:  []string{"/bin/sleep"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ownerFile), record, 0o600))

	var report RecoveryReport
	recoverOne(dir, os.Getpid(), mustProcessStart(t, os.Getpid()), &report)

	require.Eventually(t, func() bool { return !processAlive(launcherPID) },
		5*time.Second, 20*time.Millisecond, "T-94: the verified launcher is stopped")
	require.Eventually(t, func() bool { return !processAlive(grandchild.Process.Pid) },
		5*time.Second, 20*time.Millisecond,
		"T-94: the launcher's own group — grandchild included — is stopped; deleting group termination cannot pass this test")
	require.True(t, processAlive(unrelatedPID),
		"T-94: the legacy pgid names the unrelated group — recovery must not act on a stored number, the unrelated process lives")
	require.Equal(t, 1, report.Killed, "exactly the verified launcher's group is killed")
	require.Empty(t, report.Warnings)
}

// T-96 (a) / B-39: a live process whose executable matches but whose start
// time differs is NOT stopped — PID reuse reads as a different process. The
// folder is kept with a warning.
func TestPreviewRecovery_StartTimeMismatchKeepsFolderAndProcess(t *testing.T) {
	pid, _, closeSleeper := spawnGroupedSleep(t, "60")
	defer closeSleeper()
	home := t.TempDir()
	dir := filepath.Join(scratchRoot(home), "pid-reused")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	record, err := json.Marshal(ownerRecord{
		State:        stateRunning,
		GatewayPID:   os.Getpid(),
		GatewayStart: "not-current-start",
		Launcher:     &procIdentity{PID: pid, Start: "a-start-time-this-process-never-had", Executable: "/bin/sleep"},
		Executables:  []string{"/bin/sleep"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ownerFile), record, 0o600))

	var report RecoveryReport
	recoverOne(dir, os.Getpid(), mustProcessStart(t, os.Getpid()), &report)

	require.True(t, processAlive(pid),
		"T-96 (a): a live process whose start time differs from the record is never stopped")
	_, err = os.Stat(dir)
	require.NoError(t, err, "T-96 (a): the folder is kept for diagnosis")
	require.Zero(t, report.Removed)
	require.Zero(t, report.Killed)
	require.Len(t, report.Warnings, 1)
	require.Contains(t, report.Warnings[0], "does not match its record")
}

// T-96 (b) / §3.4 D1: the old tmp/preview layout is never read, never acted
// on, never cleaned (greenfield, no migration). A folder there whose record
// names a LIVE harmless process must cost that process nothing — and the
// sibling valid call in the same Recover sweep must still be processed, so
// the row cannot pass because Recover returned early.
func TestPreviewRecovery_OldLayoutIsInert(t *testing.T) {
	pid, _, closeSleeper := spawnGroupedSleep(t, "60")
	defer closeSleeper()
	home := t.TempDir()

	// The old layout root, with a record naming the live sleeper as a running
	// launcher whose identity fully matches. Under the new root name this
	// folder is an inert leftover; the pre-amendment recovery read this root
	// and would stop the sleeper and reap the folder.
	oldDir := filepath.Join(home, "tmp", "preview", "old-run")
	require.NoError(t, os.MkdirAll(oldDir, 0o700))
	record, err := json.Marshal(ownerRecord{
		State:        stateRunning,
		GatewayPID:   os.Getpid(),
		GatewayStart: "not-current-start",
		Launcher:     &procIdentity{PID: pid, Start: mustProcessStart(t, pid), Executable: "/bin/sleep"},
		PGID:         pid,
		Executables:  []string{"/bin/sleep"},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, ownerFile), record, 0o600))

	// The sibling: a valid call folder that the SAME sweep must process.
	sibling, err := newCall(home, os.Getpid(), "not-current-start")
	require.NoError(t, err)

	report := Recover(home, os.Getpid(), mustProcessStart(t, os.Getpid()))

	require.True(t, processAlive(pid),
		"T-96 (b): the old layout's live launcher must not be stopped — the old root is never read")
	_, err = os.Stat(oldDir)
	require.NoError(t, err, "T-96 (b): the old layout folder is never cleaned")
	require.Zero(t, report.Killed, "nothing in the old layout is stopped")
	_, err = os.Stat(sibling.dir)
	require.ErrorIs(t, err, os.ErrNotExist,
		"instrument: the same Recover sweep processed the valid call — the row did not pass by returning early")
	require.Equal(t, 1, report.Removed, "the sibling call is the one removed folder")
}
