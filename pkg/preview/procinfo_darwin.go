// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package preview

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// processStart returns the process start time (kern.proc.pid p_starttime),
// stable across execve.
func processStart(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if kp.Proc.P_pid != int32(pid) {
		return "", errors.New("no such process")
	}
	t := kp.Proc.P_starttime
	return fmt.Sprintf("tv:%d.%06d", t.Sec, t.Usec), nil
}

// processExecutable returns the process's command name (p_comm, at most 16
// bytes). macOS offers no pure-Go full-path query here, so identity on macOS
// is PID + start time + command name.
func processExecutable(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	b := kp.Proc.P_comm[:]
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return "comm:" + string(b[:n]), nil
}

// executableMatches compares a recorded full path with a live command name.
func executableMatches(recorded, candidate string) bool {
	base := filepath.Base(recorded)
	if len(base) > 16 {
		base = base[:16]
	}
	return candidate == "comm:"+base || candidate == recorded
}

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
