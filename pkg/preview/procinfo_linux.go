// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package preview

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processStart returns the process start time in clock ticks since boot
// (/proc/<pid>/stat field 22), stable across execve.
func processStart(pid int) (string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	s := string(data)
	// The command name (field 2) may contain spaces and parentheses; the
	// fields after the last ')' are fixed.
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", errors.New("malformed stat")
	}
	fields := strings.Fields(s[i+1:])
	// fields[0] is field 3 (state); starttime is field 22 → index 19.
	if len(fields) < 20 {
		return "", errors.New("short stat")
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", err
	}
	return "ticks:" + fields[19], nil
}

// processExecutable returns the executable of pid.
func processExecutable(pid int) (string, error) {
	return os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
}

func executableMatches(recorded, candidate string) bool { return recorded == candidate }

func killPID(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

func killGroup(pgid int) {
	if pgid > 1 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
