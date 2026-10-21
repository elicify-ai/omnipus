//go:build darwin

package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func inspectNativeProcess(pid int) (ProcessInspection, error) {
	p := ProcessInspection{PID: pid, State: "unknown"}
	if pid <= 0 {
		return p, errors.New("invalid native PID")
	}
	if err := unix.Kill(pid, 0); err != nil {
		if errors.Is(err, unix.ESRCH) {
			p.State = "absent"
			return p, nil
		}
		return p, err
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return p, err
	}
	birth := kp.Proc.P_starttime
	if birth.Sec <= 0 {
		return p, errors.New("native process birth unavailable")
	}
	p.StartID = fmt.Sprintf("%d:%d", birth.Sec, birth.Usec)
	if kp.Proc.P_stat == procStatZombie {
		p.State = "exited"
		return p, nil
	}
	// KERN_PROCARGS2 starts with argc (native int32), followed by the
	// kernel-reported executable path, NUL terminated. Never read argv/env.
	args, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return p, err
	}
	if len(args) < 5 {
		return p, errors.New("native executable unavailable")
	}
	end := bytes.IndexByte(args[4:], 0)
	if end <= 0 {
		return p, errors.New("invalid native executable")
	}
	exe, err := filepath.EvalSymlinks(string(args[4 : 4+end]))
	if err != nil || !filepath.IsAbs(exe) {
		return p, errors.New("native executable identity unavailable")
	}
	after, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return p, err
	}
	if after.Proc.P_starttime != birth {
		return p, errors.New("native process identity changed")
	}
	if after.Proc.P_stat == procStatZombie {
		p.State = "exited"
		return p, nil
	}
	p.State, p.Executable = "running", exe
	return p, nil
}
