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
// The child is detached from the parent process group so it outlives the
// spawning process. On Unix this is done via SysProcAttr.Setpgid=true
// (puts the child in a new process group; this is NOT a new session — the
// child remains in the same session as the parent but is immune to SIGHUP
// because it is no longer a process group leader of the foreground group).
// On Windows a separate file in daemon_windows.go sets
// CREATE_NEW_PROCESS_GROUP.
//
// # Stop
//
// [Stop] reads the PID file, verifies the PID belongs to a live omnipus
// process (best-effort name check), sends SIGTERM (Unix) / taskkill (Windows),
// waits up to the grace period for clean exit, then sends SIGKILL / force-kills
// if needed. A stale PID file is silently cleared.
//
// The never-kill guarantee strength varies by platform:
//   - Linux: /proc/<pid>/exe identifies the binary precisely — very strong.
//   - macOS/non-Linux POSIX: no /proc; the name check falls back to /proc/<pid>/comm
//     (truncated, 15 chars) or is unavailable — conservatively assumes ours when the
//     name cannot be read, so Stop will proceed rather than orphan a running gateway.
//   - Windows: depends on wmic availability. When wmic is missing (Win11 24H2+) or
//     fails unexpectedly, Stop refuses to act (fail-safe) and returns an error.
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
)

// PIDPath returns the absolute path to the gateway PID file for the given
// Omnipus home directory (typically ~/.omnipus).
func PIDPath(home string) string {
	return filepath.Join(home, pidFile)
}

// readPID reads the PID file and returns the stored PID.
//
// Returns (0, nil) when:
//   - the file does not exist (no gateway running)
//   - the file exists but contains unparseable / empty content (stale/corrupt);
//     in this case the file is removed and a slog.Warn is emitted.
//
// Returns (0, err) only for genuine I/O errors (e.g. unreadable directory).
func readPID(home string) (int, error) {
	path := PIDPath(home)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("daemon: read PID file: %w", err)
	}
	pidStr := strings.TrimSpace(string(data))
	pid, parseErr := strconv.Atoi(pidStr)
	if parseErr != nil {
		// Corrupt or empty PID file — treat as stale rather than a hard error.
		// Remove it so subsequent calls see a clean state, and report
		// not-running (0, nil) so neither Status nor Stop permanently wedge.
		slog.Warn("daemon: PID file has unparseable content — treating as stale",
			"path", path, "content", pidStr)
		removePIDPath(path)
		pid = 0 // be explicit: return zero so callers use the "no PID" path
	}
	return pid, nil
}

// writePID writes pid atomically to the PID file (mode 0600).
func writePID(home string, pid int) error {
	return WritePID(home, pid)
}

// WritePID writes pid atomically to the PID file (mode 0600).
// It is exported so the gateway can self-register its own PID after the
// HTTP listener binds (MAJOR-2: hand-started gateways are tracked the same
// way as spawner-started ones, making [Status] and [Stop] authoritative
// regardless of launch path).
func WritePID(home string, pid int) error {
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
	removePIDPath(PIDPath(home))
}

// removePIDPath deletes the file at path. Errors are logged at Debug level
// and not propagated (removal is best-effort cleanup).
func removePIDPath(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Debug("daemon: remove PID file", "path", path, "error", err)
	}
}

// RemovePID deletes the gateway PID file. It is exported so the gateway can
// clean up its own PID file on graceful shutdown (MAJOR-2: symmetric with
// [WritePID]). Removal is best-effort; errors are logged at Debug level.
func RemovePID(home string) {
	removePID(home)
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

	alive, isOmnipus, identityErr := checkProcess(pid)
	if identityErr != nil {
		// Identity cannot be determined (e.g. wmic missing on Windows). Fail safe:
		// do not remove the PID file and report the error to the caller so they
		// know Status is inconclusive rather than silently wrong.
		return false, 0, fmt.Errorf("daemon: status inconclusive — process identity check failed: %w", identityErr)
	}
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
//
// When the child started successfully but writing the PID file failed, SpawnExe
// returns (pid, err) — the non-zero pid allows the caller to recover (e.g. kill
// the orphaned child). The child is NOT automatically killed on PID-write failure
// so that operators who catch the error can inspect or adopt the process; the
// error log line names the pid and instructs the operator on recovery.
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

	alive, isOmnipus, identityErr := checkProcess(pid)
	if identityErr != nil {
		// Identity cannot be determined (e.g. wmic missing on Windows). Fail safe:
		// do not kill, do not remove the PID file, return an error so the caller
		// knows Stop was unable to act (rather than silently orphaning the process).
		return false, fmt.Errorf("daemon: stop refused — process identity check failed (pid %d): %w", pid, identityErr)
	}
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
