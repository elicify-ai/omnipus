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
		// Group signal failed for a reason OTHER than "already gone" (e.g.
		// EPERM). Attempt the individual PID as a fallback regardless — and
		// if THAT succeeds (or the direct PID is also merely already gone),
		// report overall SUCCESS, not the group-kill error: the session was
		// still terminated, just not via the process-group signal, and a
		// caller that sees an error here would wrongly leave the session
		// labeled "running" (and, for KillAndRelabel callers, skip the
		// atomic relabel/count entirely) despite the process actually being
		// dead. Only report failure when BOTH signals failed for a reason
		// other than "already gone".
		fallbackErr := syscall.Kill(pid, syscall.SIGKILL)
		if fallbackErr == nil || errors.Is(fallbackErr, syscall.ESRCH) {
			return nil
		}
		// Both the group signal and the direct-PID fallback failed for a
		// real reason (not merely "already gone") — this is a genuine kill
		// failure, distinct from ErrSessionDone (the caller-side benign
		// "lost the race, already terminal" case): that is detected by the
		// CALLER via errors.Is(err, ErrSessionDone) before ever reaching
		// here, so any error returned by this function is by construction a
		// real syscall failure, never a masked double-cancel.
		return err
	}
	// Group signal succeeded; also signal the individual PID as a fallback for
	// processes that are not group leaders (intentional belt-and-suspenders).
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return nil
}
