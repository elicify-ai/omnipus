// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package daemon

import "golang.org/x/sys/unix"

// procStatZombie is XNU's SZOMB process state ("intermediate state in process
// termination", <sys/proc.h> = 5): the process has exited but its parent has
// not yet waited for it, so the kernel keeps the PID entry alive. golang.org/x/sys
// does not export the SZOMB constant for darwin, so it is defined here.
const procStatZombie = 5

// isZombie reports whether the process is in zombie state by querying the
// kern.proc.pid sysctl, whose kinfo_proc carries the process state in
// Proc.P_stat. macOS has no /proc, so this is the only kernel-provided way to
// distinguish "running" from "exited but unreaped" — kill(pid, 0) succeeds for
// both. Returns false on any sysctl error (conservative: assume not zombie),
// matching the Linux /proc reader's convention. A sysctl error here means the
// process does not exist or is not visible to us, neither of which is zombie
// evidence.
func isZombie(pid int) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return false
	}
	return kp.Proc.P_stat == procStatZombie
}
