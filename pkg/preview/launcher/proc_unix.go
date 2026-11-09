// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux || darwin

package launcher

import (
	"os/exec"
	"syscall"
)

// killTree kills the launcher's process group: the launcher becomes the
// target by execve and keeps the group, and anything the target starts
// (LibreOffice's soffice.bin) inherits it.
func killTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
