// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package programs

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup starts the check in its own process group, so the timeout
// stops every process it started (FR-005).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// stopProcessGroup kills the check's whole process group.
func stopProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// The group id is the child's pid (Setpgid). ESRCH means it already
	// ended; the child itself is killed too in case the group kill failed.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

// awaitProcessGroupGone waits (bounded) until no process of the stopped
// check's group is left, so a stopped check is reported only once its whole
// group is gone. Call it after cmd.Wait has reaped the direct child.
func awaitProcessGroupGone(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-cmd.Process.Pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		time.Sleep(20 * time.Millisecond)
	}
}
