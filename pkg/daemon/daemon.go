// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package daemon provides shared primitives for managing the Omnipus gateway
// background process. Both the desktop launcher (cmd/omnipus-launcher-tui) and
// the CLI (cmd/omnipus, `omnipus stop` / auto-start, FR-016) use this package
// so there is exactly one PID-file convention and no competing mechanisms.
//
// # PID file
//
// The PID file lives at <home>/gateway.pid (0600). It stores the decimal PID
// of a spawned gateway as a plain ASCII string with no trailing newline. A
// stale file (process dead or not an omnipus binary) is removed automatically
// whenever [Status] or [Stop] detects it.
//
// # Spawn
//
// [Spawn] launches the current executable (via [os.Executable]) with the
// sub-command "start" prepended and any caller-supplied extra args appended.
// The child is detached from the parent session/process group so it outlives
// the spawning process. On Unix this is done via SysProcAttr.Setpgid=true
// (avoids the Setsid ioctl, which is blocked by seccomp when the caller is
// already a session leader). On Windows a separate file in daemon_windows.go
// sets CREATE_NEW_PROCESS_GROUP.
//
// # Stop
//
// [Stop] reads the PID file, verifies the PID belongs to a live omnipus
// process (best-effort name check), sends SIGTERM (Unix) / taskkill (Windows),
// waits up to stopGracePeriod for clean exit, then sends SIGKILL / force-kills
// if needed. A stale PID file is silently cleared; [Stop] never kills an
// unrelated process.
package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

const (
	pidFile = "gateway.pid"

	// stopGracePeriod is how long [Stop] waits between SIGTERM and SIGKILL.
	// Exported via the stopGracePeriodDuration constant in each OS file so the
	// test can override it; here we keep the documented value for reference.
	// Actual value: 5 s on Unix, 3 s on Windows (the OS files set it).
)

// PIDPath returns the absolute path to the gateway PID file for the given
// Omnipus home directory (typically ~/.omnipus).
func PIDPath(home string) string {
	return filepath.Join(home, pidFile)
}

// readPID reads the PID file and returns the stored PID.
// It returns (0, nil) if the file does not exist, and a non-nil error
// for any other read/parse failure.
func readPID(home string) (int, error) {
	data, err := os.ReadFile(PIDPath(home))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("daemon: read PID file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("daemon: parse PID file: %w", err)
	}
	return pid, nil
}

// writePID writes pid atomically to the PID file (mode 0600).
func writePID(home string, pid int) error {
	data := []byte(strconv.Itoa(pid))
	if err := fileutil.WriteFileAtomic(PIDPath(home), data, 0o600); err != nil {
		return fmt.Errorf("daemon: write PID file: %w", err)
	}
	return nil
}

// removePID deletes the PID file. Errors from Remove are logged at Debug level
// (the file may have already been removed by a concurrent process) and not
// propagated: removal is best-effort cleanup.
func removePID(home string) {
	if err := os.Remove(PIDPath(home)); err != nil && !os.IsNotExist(err) {
		slog.Debug("daemon: remove PID file", "path", PIDPath(home), "error", err)
	}
}

// Status reports whether the gateway is running and returns its PID.
//
// It reads the PID file, checks that the process is alive, and performs a
// best-effort name check to guard against PID reuse.  If the PID file is
// stale (process dead or wrong binary), Status removes the file and returns
// (false, 0, nil).
//
// A missing PID file is not an error: Status returns (false, 0, nil).
func Status(home string) (running bool, pid int, err error) {
	pid, err = readPID(home)
	if err != nil {
		return false, 0, err
	}
	if pid <= 0 {
		// PID 0 is never a valid user-space process; treat as stale.
		removePID(home)
		return false, 0, nil
	}

	alive, isOmnipus := checkProcess(pid)
	if !alive || !isOmnipus {
		if !alive {
			slog.Debug("daemon: stale PID file — process is not alive", "pid", pid)
		} else {
			slog.Debug("daemon: PID file points at a non-omnipus process — ignoring", "pid", pid)
		}
		removePID(home)
		return false, 0, nil
	}

	return true, pid, nil
}

// Spawn launches the gateway detached from the current process. It runs
//
//	<current-executable> start [args...]
//
// in a new process group / job so the child outlives the parent. The PID
// file is written atomically after the child has been started. Spawn returns
// an error if the executable cannot be resolved, the fork fails, or the PID
// file cannot be written.
//
// Spawn does NOT verify that the gateway is ready to serve traffic — callers
// that need readiness should poll the health endpoint after Spawn returns.
//
// Callers must check [Status] before calling Spawn to avoid duplicate
// processes; Spawn itself does NOT check for a running instance.
func Spawn(home string, args []string) (pid int, err error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("daemon: resolve executable: %w", err)
	}
	return SpawnExe(home, exe, args)
}

// SpawnExe is like [Spawn] but uses the explicitly provided executable path
// instead of [os.Executable]. This is intended for callers (e.g. the desktop
// launcher) that launch a separate `omnipus` binary rather than re-invoking
// themselves.
//
// It runs:
//
//	<exe> start [args...]
//
// detached in a new process group, and writes the child PID to the PID file.
func SpawnExe(home string, exe string, args []string) (pid int, err error) {
	// Build argv: <exe> start [caller args...]
	spawnArgs := make([]string, 0, 1+len(args))
	spawnArgs = append(spawnArgs, "start")
	spawnArgs = append(spawnArgs, args...)

	pid, err = spawnProcess(exe, spawnArgs, home)
	if err != nil {
		return 0, fmt.Errorf("daemon: spawn gateway: %w", err)
	}

	if err := writePID(home, pid); err != nil {
		// The process is running but we couldn't record the PID.
		// Log it so the operator can recover manually.
		slog.Error("daemon: spawned gateway but failed to write PID file — "+
			"process will not be tracked",
			"pid", pid, "error", err)
		return pid, err
	}

	slog.Info("daemon: gateway spawned", "pid", pid, "home", home)
	return pid, nil
}

// Stop terminates the gateway process identified by the PID file.
//
// It reads the PID file, verifies the PID is a live omnipus process (to
// guard against killing an unrelated reused PID), and then terminates it.
// On Unix it sends SIGTERM first, waits up to the grace period, and then
// sends SIGKILL. On Windows it calls taskkill /F.
//
// Stop returns (true, nil) when the process was found and terminated.
// It returns (false, nil) when no gateway is running (missing or stale PID
// file) — this is not an error.  It returns (false, err) for I/O or kill
// failures.
func Stop(home string) (stopped bool, err error) {
	pid, err := readPID(home)
	if err != nil {
		return false, err
	}
	if pid == 0 {
		slog.Debug("daemon: stop called but no PID file found")
		return false, nil
	}

	alive, isOmnipus := checkProcess(pid)
	if !alive {
		slog.Debug("daemon: stop called but process is not alive — clearing stale PID file", "pid", pid)
		removePID(home)
		return false, nil
	}
	if !isOmnipus {
		// Safety guard: never kill a PID that is not ours.
		slog.Warn("daemon: PID file points at a non-omnipus process — refusing to kill",
			"pid", pid)
		removePID(home)
		return false, fmt.Errorf("daemon: PID %d is not an omnipus process; PID file removed", pid)
	}

	if err := killProcess(pid); err != nil {
		return false, fmt.Errorf("daemon: kill gateway (pid %d): %w", pid, err)
	}

	removePID(home)
	slog.Info("daemon: gateway stopped", "pid", pid)
	return true, nil
}
