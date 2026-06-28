// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// stopGracePeriod is how long Stop waits for SIGTERM to take effect before
// escalating to SIGKILL. Tests can override via the package-level var; the
// var is unexported to discourage production mutation.
var stopGracePeriod = 5 * time.Second

// pollInterval is how often Stop polls for process death.
var pollInterval = 100 * time.Millisecond

// checkProcess returns (alive, isOmnipus, identityErr).
//
// alive: the process exists and is in a running state (not zombie).
// isOmnipus: best-effort check that the process name looks like an omnipus
// binary. On Linux we read /proc/<pid>/exe; on macOS and other POSIX systems
// we fall back to /proc/<pid>/comm or kill(pid, 0).
// identityErr: non-nil when the identity CANNOT be determined at all (i.e.
// we know the process is alive but cannot confirm whether it is ours). On
// Unix this is set when resolveExeName fails; the conservative convention here
// is to treat the error as "assume ours" (alive=true, isOmnipus=true, nil err)
// because Unix has kill(pid,0) as a reliable liveness signal — if it's alive
// and we can't read its name, it is most likely our own process.
//
// Zombie processes (State: Z in /proc/<pid>/status) are treated as dead:
// they cannot serve traffic and the gateway PID file should be cleared.
func checkProcess(pid int) (alive bool, isOmnipus bool, identityErr error) {
	// PID 0 is never a valid user-space process PID.
	if pid <= 0 {
		return false, false, nil
	}

	// kill(pid, 0) is the canonical cross-platform POSIX existence check:
	// it sends no signal, succeeds (errno=0) if the process exists and we
	// have permission, or returns ESRCH if the process does not exist.
	err := syscall.Kill(pid, 0)
	if err == syscall.ESRCH {
		return false, false, nil
	}
	// Any error other than ESRCH (e.g. EPERM — process exists but we can't
	// signal it) still means the process is alive.

	// On Linux, check for zombie state. A zombie holds the PID but cannot
	// serve traffic; we treat it as dead so the PID file is cleared.
	if isZombie(pid) {
		slog.Debug("daemon: process is a zombie — treating as dead", "pid", pid)
		return false, false, nil
	}

	// Best-effort name check via /proc/<pid>/exe (Linux) or /proc/<pid>/comm.
	// If neither is readable, we assume it is ours (conservative: avoids
	// failing to stop our own gateway due to a /proc read error). On Unix,
	// kill(pid,0) success is already reliable proof that the process exists;
	// a name-read failure is typically a transient permission issue on the
	// /proc symlink, not evidence of a different process, so assuming "ours"
	// is the safer choice (identityErr=nil signals this conservative assumption).
	exeName, ok := resolveExeName(pid)
	if !ok {
		// Cannot determine the name; conservatively treat as omnipus so Stop
		// proceeds rather than orphaning a running gateway.
		return true, true, nil
	}
	base := strings.ToLower(filepath.Base(exeName))
	isOmnipus = strings.Contains(base, "omnipus")
	return true, isOmnipus, nil
}

// isZombie reports whether the process is in zombie state by reading
// /proc/<pid>/status. Returns false on any read error (conservative: assume
// not zombie).
func isZombie(pid int) bool {
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.Contains(line, "Z")
		}
	}
	return false
}

// resolveExeName attempts to find the executable name for the given PID.
// Returns ("", false) if it cannot be determined.
func resolveExeName(pid int) (string, bool) {
	// Linux: /proc/<pid>/exe is a symlink to the real executable.
	exeLink := fmt.Sprintf("/proc/%d/exe", pid)
	if target, err := os.Readlink(exeLink); err == nil {
		return target, true
	}

	// Fallback: /proc/<pid>/comm contains just the process name (up to 15 chars).
	commPath := fmt.Sprintf("/proc/%d/comm", pid)
	if data, err := os.ReadFile(commPath); err == nil {
		return strings.TrimSpace(string(data)), true
	}

	// Non-Linux POSIX: cannot determine name without shelling out.
	return "", false
}

// spawnProcess launches exe with the given args as a detached child in a new
// process group (Setpgid=true). It returns the PID of the child.
//
// The home parameter is the Omnipus home directory; it is passed through to
// the child via the inherited environment (OMNIPUS_HOME if set) and is accepted
// here so the function signature is consistent across platforms and test helpers.
//
// We use Setpgid rather than Setsid so that:
//  1. The child is detached from the parent's process group (SIGHUP on parent
//     terminal close won't propagate to the child).
//  2. We avoid the Setsid ioctl, which is blocked by seccomp when the calling
//     process is already a session leader.
//
// Stdin is connected to /dev/null; stdout and stderr are inherited so that
// early startup errors are visible. In production, operators redirect them
// via a supervisor or shell redirection.
func spawnProcess(exe string, args []string, _ string) (int, error) {
	// Filter the parent env to pass only non-sensitive, runtime-relevant vars.
	// We explicitly pass OMNIPUS_HOME so the child finds its data directory.
	// We omit OMNIPUS_MASTER_KEY and OMNIPUS_KEY_FILE from the parent env on
	// purpose: the spawned gateway must use its own unlock mode.  Callers that
	// need a headless-unlock child should set those env vars in args or via the
	// gateway config.
	//
	// Currently we pass through the full parent env so that PATH, OMNIPUS_HOME,
	// OMNIPUS_MASTER_KEY (for non-interactive unlock), OMNIPUS_KEY_FILE, and
	// OMNIPUS_BEARER_TOKEN all flow to the child without the caller having to
	// re-enumerate them.  The env whitelist (FR-015 security hardening) is left
	// for the v0.2 security wave (issue #155).
	childEnv := os.Environ()

	cmd := exec.Command(exe, args...)
	cmd.Env = childEnv
	cmd.Stdin = nil // Go runtime connects this to /dev/null unconditionally for background children
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // detach from parent process group
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: exec.Command.Start: %w", err)
	}

	// Detach: we do NOT call cmd.Wait() — the child is intentionally an orphan.
	// Release our reference to the child so the Go runtime does not hold it.
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		// Non-fatal: the process is running; we just can't wait on it.
		slog.Debug("daemon: Process.Release after spawn", "pid", pid, "error", err)
	}

	return pid, nil
}

// killProcess terminates the process with the given PID using a graceful
// SIGTERM → wait → SIGKILL escalation.
func killProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("daemon: FindProcess(%d): %w", pid, err)
	}

	// Send SIGTERM for graceful shutdown.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			// Process already gone — that's fine.
			slog.Debug("daemon: SIGTERM: process already gone", "pid", pid)
			return nil
		}
		return fmt.Errorf("daemon: SIGTERM pid %d: %w", pid, err)
	}

	// Poll for process death during the grace period.
	deadline := time.Now().Add(stopGracePeriod)
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			// Process exited cleanly within the grace period.
			slog.Debug("daemon: process exited after SIGTERM", "pid", pid)
			return nil
		}
	}

	// Grace period expired — escalate to SIGKILL.
	slog.Warn("daemon: grace period expired; sending SIGKILL", "pid", pid)
	if err := proc.Signal(syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return fmt.Errorf("daemon: SIGKILL pid %d: %w", pid, err)
	}

	return nil
}
