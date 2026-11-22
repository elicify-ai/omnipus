// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package programs

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroup starts the check in a new process group (FR-005).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// stopProcessGroup stops the check and every process it started: taskkill
// /T walks the child tree; the direct kill covers a missing taskkill.
func stopProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}

// awaitProcessGroupGone has nothing more to wait for on Windows: taskkill /T
// returns after the tree is stopped.
func awaitProcessGroupGone(_ *exec.Cmd) {}
