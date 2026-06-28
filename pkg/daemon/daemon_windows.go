// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// stopGracePeriod is how long Stop waits for taskkill to take effect.
// On Windows we use /F (force) so the grace period is mainly a safety margin.
var stopGracePeriod = 3 * time.Second

// pollInterval is how often Stop polls for process death.
var pollInterval = 100 * time.Millisecond

// errWmicUnavailable is returned by wmicCheckProcess when wmic is not on PATH.
// Callers use it to distinguish "tool missing" from "process not found".
var errWmicUnavailable = errors.New("daemon: wmic not found on PATH")

// wmicCheckProcess invokes wmic to check whether pid is alive and belongs to
// an omnipus binary.
//
// Return values:
//   - (true, true, nil)   — pid is alive and is an omnipus process
//   - (true, false, nil)  — pid is alive but is NOT an omnipus process
//   - (false, false, nil) — pid does not exist (wmic reported no rows)
//   - (false, false, err) — identity could NOT be determined (wmic missing or
//     unexpected exec error); callers MUST treat this as "unknown" (fail-safe)
//     and MUST NOT act as if the process were dead.
func wmicCheckProcess(pid int) (alive bool, isOmnipus bool, err error) {
	// Detect wmic absence before invoking it. Win11 24H2+ removed wmic.
	if _, lookErr := exec.LookPath("wmic"); lookErr != nil {
		return false, false, errWmicUnavailable
	}

	pidStr := fmt.Sprintf("%d", pid)
	out, execErr := exec.Command(
		"wmic", "process", "where",
		fmt.Sprintf("ProcessId=%s", pidStr),
		"get", "Name",
	).Output()
	if execErr != nil {
		// wmic returns non-zero when the PID does not exist AND on some
		// error conditions (e.g. WMI service not running). Distinguish them
		// by inspecting the output: a clean "no instance" from wmic produces
		// an exit code but specific output; any other failure is unknown.
		outStr := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(outStr, "no instance") || strings.Contains(outStr, "not found") {
			// Clean "process does not exist" path.
			return false, false, nil
		}
		// Unexpected wmic failure — we cannot determine the process state.
		return false, false, fmt.Errorf("daemon: wmic query failed: %w", execErr)
	}

	// wmic output example when the process exists:
	//   Name
	//   omnipus.exe
	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // skip header row
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return true, strings.Contains(strings.ToLower(line), "omnipus"), nil
	}
	// No data rows after the header → process does not exist.
	return false, false, nil
}

// checkProcess returns (alive, isOmnipus, identityErr).
//
// On Windows we use wmic to check process existence and name. When wmic is
// absent (Win11 24H2+) or returns an unexpected error, identity CANNOT be
// determined — identityErr is set to the underlying error, signalling to
// Status and Stop that they must fail safe (refuse to act, do not clear the
// PID file, return an error to the caller).
func checkProcess(pid int) (alive bool, isOmnipus bool, identityErr error) {
	a, o, err := wmicCheckProcess(pid)
	if err != nil {
		slog.Warn("daemon: cannot determine process identity — failing safe",
			"pid", pid, "error", err)
		// Return identityErr so callers can distinguish "unknown" from "not ours".
		return false, false, fmt.Errorf("daemon: process identity check failed: %w", err)
	}
	return a, o, nil
}

// spawnProcess launches exe with the given args in a new process group using
// CREATE_NEW_PROCESS_GROUP so the child is not affected by Ctrl-C signals sent
// to the parent's console. The home parameter is accepted for API consistency
// with the Unix implementation but is not used on Windows.
func spawnProcess(exe string, args []string, _ string) (int, error) {
	childEnv := os.Environ()

	cmd := exec.Command(exe, args...)
	cmd.Env = childEnv
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("daemon: exec.Command.Start: %w", err)
	}

	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		slog.Debug("daemon: Process.Release after spawn", "pid", pid, "error", err)
	}

	return pid, nil
}

// killProcess terminates the process using taskkill /F.
// On Windows there is no graceful-shutdown signal equivalent to SIGTERM for
// arbitrary processes; we use /F (force) directly and then verify termination.
func killProcess(pid int) error {
	pidStr := fmt.Sprintf("%d", pid)
	out, err := exec.Command("taskkill", "/F", "/PID", pidStr).CombinedOutput()
	if err != nil {
		// If the process is already gone, taskkill returns an error but the
		// message contains "not found" or similar — treat that as success.
		if strings.Contains(strings.ToLower(string(out)), "not found") ||
			strings.Contains(strings.ToLower(string(out)), "no tasks") {
			slog.Debug("daemon: taskkill: process already gone", "pid", pid)
			return nil
		}
		return fmt.Errorf("daemon: taskkill pid %d: %w (output: %s)", pid, err, strings.TrimSpace(string(out)))
	}

	// Poll for process death (taskkill /F is synchronous on Windows, but we
	// verify anyway to be safe).
	deadline := time.Now().Add(stopGracePeriod)
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		alive, _, _ := checkProcess(pid)
		if !alive {
			slog.Debug("daemon: process terminated", "pid", pid)
			return nil
		}
	}

	return fmt.Errorf("daemon: process %d still alive after taskkill", pid)
}
