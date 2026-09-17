//go:build unix

package cdppipe

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// startInOwnProcessGroup starts Chrome as the leader of a new process group
// (pgid == Chrome's pid), so every helper it spawns — renderers, the GPU and
// utility processes — can be killed together by killProcessGroup, including a
// helper that is still running after the browser process itself has exited.
//
// Leaving the terminal's foreground process group does not let Chrome outlive
// a gateway that dies without tearing it down: Chrome exits on EOF of its CDP
// command pipe (fd 3), and the kernel closes that pipe when the gateway exits.
//
// A caller that asked for a new session (Setsid, via PipeOptions.ModifyCmd)
// already gets a new process group from it, and setpgid on a session leader
// fails, so Setpgid is left alone in that case.
func startInOwnProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if cmd.SysProcAttr.Setsid {
		return
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs every process still in the process group led by
// pid. An empty group (ESRCH) is the normal case after Chrome shut down its
// own helpers, and is not an error.
func killProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("killProcessGroup: %w", err)
	}
	return nil
}
