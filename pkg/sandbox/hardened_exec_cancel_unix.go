//go:build !windows

// Process-group cancellation for hardened_exec foreground children (Unix).
//
// THREAT / BUG (HIGH): exec.CommandContext's default cmd.Cancel only signals
// the DIRECT child PID (Process.Kill = SIGKILL to that one pid). The
// hardened-exec foreground path runs commands like `sh -c 'cmd1 && sleep N'`,
// where sh forks a grandchild (sleep) that INHERITS sh's stdout/stderr pipe
// fds. applyPlatformHardening sets Setpgid=true, so sh is a process-group
// leader and the grandchild joins that group — but nothing ever signaled the
// GROUP. On timeout the default cancel killed sh, yet the sleep grandchild
// kept the write end of the stdout pipe open. Because cmd.WaitDelay was unset
// (0), os/exec read the pipe until EOF, which never came until sleep exited on
// its own — so the command outlived its deadline by the full grandchild
// duration (reproduced: ~30s overrun on a 2s timeout). That is a time-limit
// bypass / resource-exhaustion vector on the production-default Linux path.
//
// FIX: override cmd.Cancel to signal the whole process GROUP (negative pid),
// SIGTERM-then-SIGKILL with a short grace so well-behaved children flush and
// exit cleanly while stragglers are force-killed. cmd.WaitDelay (set by the
// caller in runOnCurrentThread, cross-platform) is the backstop that stops a
// stuck pipe from ever blocking Wait() indefinitely even if a process escapes
// its group (e.g. a child that called setsid()).

package sandbox

import (
	"os/exec"
	"syscall"
	"time"
)

// cancelGraceDuration is how long the process group is given to react to
// SIGTERM before the group is force-killed with SIGKILL. Short, because this
// only fires on timeout/cancellation — the child has already overrun and we
// want Wait() to return promptly. Bounded well under the caller's WaitDelay
// (5s) so the group is killed before the pipe-drain backstop trips.
const cancelGraceDuration = 1 * time.Second

// installProcessGroupCancel sets cmd.Cancel so that context cancellation (the
// wall-clock timeout) kills the child's entire process GROUP, not just the
// direct child PID. Must be called AFTER applyPlatformHardening (which sets
// Setpgid=true) and BEFORE cmd.Start. The cmd.Process handle is read inside
// the closure, which only runs after Start, so capturing cmd is safe.
//
// Returns syscall.ESRCH-suppressed nil when the group is already gone so
// os/exec does not treat an already-exited child as a cancellation error.
func installProcessGroupCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		if pid <= 0 {
			return nil
		}
		// SIGTERM the whole group first (negative pid == process group).
		// Setpgid made the child its own group leader with pgid == pid.
		// ESRCH means the group is already gone — not an error.
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			// Fall back to signaling just the direct child if the group
			// signal failed for a reason other than "already gone".
			if ignoredErr := cmd.Process.Signal(syscall.SIGTERM); ignoredErr != nil {
				_ = ignoredErr
			}
		}

		// Grace window: give the group a moment to flush and exit on
		// SIGTERM, then SIGKILL any survivors. We escalate from a timer
		// rather than polling so a clean exit isn't delayed — but we MUST
		// still deliver SIGKILL to guarantee the pipe-holding grandchild
		// dies and Wait() unblocks, so the SIGKILL is unconditional.
		time.Sleep(cancelGraceDuration)
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			// Last resort: kill the direct child via the safe OS handle.
			return cmd.Process.Kill()
		}
		return nil
	}
}
