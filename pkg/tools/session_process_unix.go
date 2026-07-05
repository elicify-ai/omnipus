//go:build !windows

package tools

import (
	"errors"
	"syscall"
)

func killProcessGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err != nil {
		// ESRCH means the process group no longer exists — treat as success.
		if errors.Is(err, syscall.ESRCH) {
			// Attempt the individual PID as a fallback in case the process is not a
			// group leader (intentional: some shells don't create a new process group).
			_ = syscall.Kill(pid, syscall.SIGKILL)
			return nil
		}
		// Attempt individual PID fallback regardless of the group-kill error.
		// This covers EPERM on the group signal but success on the direct signal.
		_ = syscall.Kill(pid, syscall.SIGKILL)
		return err
	}
	// Group signal succeeded; also signal the individual PID as a fallback for
	// processes that are not group leaders (intentional belt-and-suspenders).
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return nil
}
