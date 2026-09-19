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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// stopGracePeriod is how long Stop waits for taskkill to take effect.
// On Windows we use /F (force) so the grace period is mainly a safety margin.
var stopGracePeriod = 3 * time.Second

// pollInterval is how often Stop polls for process death.
var pollInterval = 100 * time.Millisecond

// processImageName reports whether pid exists and, when readable, the name of
// its executable. It uses the Windows process API directly because wmic is
// deprecated and is no longer installed on current Windows runners and
// consumer installs.
func processImageName(pid int) (alive bool, exeName string, err error) {
	handle, openErr := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if openErr != nil {
		// Windows reports an invalid PID as an invalid parameter. Treat that
		// as a confirmed dead process; any other OpenProcess failure remains
		// unknown so callers continue to fail safe.
		if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("daemon: open process %d: %w", pid, openErr)
	}
	defer windows.CloseHandle(handle)

	var size uint32 = 32768
	buffer := make([]uint16, size)
	if queryErr := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); queryErr != nil {
		return true, "", fmt.Errorf("daemon: query process image %d: %w", pid, queryErr)
	}
	return true, filepath.Base(windows.UTF16ToString(buffer[:size])), nil
}

// checkProcess returns (alive, isOmnipus, identityErr).
//
// Windows can deny even limited access to a live process. In that case identity
// CANNOT be determined — identityErr is set to the underlying error, signalling
// to Status and Stop that they must fail safe (refuse to act, do not clear the
// PID file, return an error to the caller).
func checkProcess(pid int) (alive bool, isOmnipus bool, identityErr error) {
	alive, exeName, err := processImageName(pid)
	if err != nil {
		slog.Warn("daemon: cannot determine process identity — failing safe",
			"pid", pid, "error", err)
		// Return identityErr so callers can distinguish "unknown" from "not ours".
		return false, false, fmt.Errorf("daemon: process identity check failed: %w", err)
	}
	if !alive {
		return false, false, nil
	}
	if exeName == "" {
		return false, false, nil
	}
	return true, strings.Contains(strings.ToLower(exeName), "omnipus"), nil
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
