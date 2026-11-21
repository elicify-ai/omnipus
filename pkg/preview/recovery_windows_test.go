//go:build windows

package preview

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// T-99 / §3.4 D4, D7 (FR-051, B-64): Windows recovery. The converter has no
// file boundary there (founder Q22 = A), so the record is not trusted against
// a compromised converter — but recovery keeps its stop (killPID; killGroup
// is a no-op on Windows), because the Windows launcher may outlive the
// gateway (§3.3). This row belongs to the Windows inventory (T-41) and runs
// in Windows CI; a Unix-tagged test does not count. killGroup must not be
// relied on: the stopped process is the verified launcher's PID, whose job
// closes the target with it.
func TestPreviewRecoveryWindows_StopsVerifiedLauncherJobAndReaps(t *testing.T) {
	// A live launcher-like process: cmd.exe holding a ping child, so the job
	// closure is observable through the child.
	launcherProc := exec.Command("cmd", "/c", "ping", "-n", "60", "127.0.0.1")
	require.NoError(t, launcherProc.Start())
	launcherPID := launcherProc.Process.Pid
	launcherStart := mustWindowsProcessStart(t, launcherPID)
	launcherDone := make(chan struct{})
	go func() { _ = launcherProc.Wait(); close(launcherDone) }()
	t.Cleanup(func() {
		killPID(launcherPID)
		select {
		case <-launcherDone:
		case <-time.After(5 * time.Second):
		}
	})

	home := t.TempDir()
	dir := filepath.Join(scratchRoot(home), "windows-verified")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	record, err := json.Marshal(ownerRecord{
		State:        stateRunning,
		GatewayPID:   os.Getpid(),
		GatewayStart: "not-current-start",
		Launcher:     &procIdentity{PID: launcherPID, Start: launcherStart, Executable: launcherProc.Path},
		Executables:  []string{launcherProc.Path},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ownerFile), record, 0o600))

	var report RecoveryReport
	recoverOne(dir, os.Getpid(), mustWindowsProcessStart(t, os.Getpid()), &report)

	require.Eventually(t, func() bool { return !processAlive(launcherPID) },
		10*time.Second, 20*time.Millisecond,
		"T-99: the verified launcher (its job, target included) is stopped by recovery")
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist, "T-99: the crashed call's folder is removed")
	require.Equal(t, 1, report.Killed, "T-99: exactly the verified launcher is stopped")
	require.Empty(t, report.Warnings)
}

// mustWindowsProcessStart reads the live process's start identity the way the
// recovery code does; a failure fails the test, never a fabricated value.
func mustWindowsProcessStart(t *testing.T, pid int) string {
	t.Helper()
	start, err := processStart(pid)
	require.NoError(t, err, "instrument: the process's start identity must be readable")
	return start
}
