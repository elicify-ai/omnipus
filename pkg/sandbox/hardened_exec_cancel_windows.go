//go:build windows

// Process-group cancellation for hardened_exec foreground children (Windows).
//
// Windows has no POSIX process-group signalling, and Go's os/exec on Windows
// does not expose Setpgid. The foreground child's lifecycle on Windows is
// governed by the Job Object installed in applyPostStartHardening (KILL_ON_
// JOB_CLOSE), and the cross-platform cmd.WaitDelay backstop set by the caller
// guarantees Wait() cannot block on a stuck pipe indefinitely.
//
// We therefore leave cmd.Cancel as the os/exec default (Process.Kill), which
// on Windows calls TerminateProcess on the child handle. There is no
// platform-correct group-kill to install here without a Job Object owned by
// the cancel path; the WaitDelay backstop is the cross-platform guarantee.

package sandbox

import "os/exec"

// installProcessGroupCancel is a no-op on Windows: the default cmd.Cancel
// (Process.Kill -> TerminateProcess) plus the caller's cmd.WaitDelay backstop
// already bound the foreground child's lifetime. See file header.
func installProcessGroupCancel(cmd *exec.Cmd) {
	// Explicitly unused: the Windows path installs no cancel hook (see the
	// function doc above). Named so the parameter is not mistaken for an
	// oversight.
	_ = cmd
}
