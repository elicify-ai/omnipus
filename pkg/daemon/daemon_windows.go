// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package daemon

import (
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

// checkProcess returns (alive, isOmnipus).
//
// On Windows we use wmic to check process existence and name.
// alive: the process exists.
// isOmnipus: the process image name contains "omnipus".
func checkProcess(pid int) (alive bool, isOmnipus bool) {
	pidStr := fmt.Sprintf("%d", pid)
	out, err := exec.Command(
		"wmic", "process", "where",
		fmt.Sprintf("ProcessId=%s", pidStr),
		"get", "Name",
	).Output()
	if err != nil {
		// wmic returns non-zero if the PID does not exist.
		return false, false
	}

	// wmic output example:
	//   Name
	//   omnipus.exe
	lines := strings.Split(string(out), "\n")
	for _, line := range lines[1:] { // skip header
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		alive = true
		isOmnipus = strings.Contains(strings.ToLower(line), "omnipus")
		return alive, isOmnipus
	}
	return false, false
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
		alive, _ := checkProcess(pid)
		if !alive {
			slog.Debug("daemon: process terminated", "pid", pid)
			return nil
		}
	}

	return fmt.Errorf("daemon: process %d still alive after taskkill", pid)
}
