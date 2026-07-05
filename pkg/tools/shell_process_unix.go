//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func prepareCommandForTermination(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
