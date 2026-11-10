//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func inspectNativeProcess(pid int) (ProcessInspection, error) {
	p := ProcessInspection{PID: pid, State: "unknown"}
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return p, errors.New("invalid native PID")
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			p.State = "absent"
			return p, nil
		}
		return p, err
	}
	defer windows.CloseHandle(h)
	var birth, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &birth, &exit, &kernel, &user); err != nil {
		return p, err
	}
	if birth.HighDateTime == 0 && birth.LowDateTime == 0 {
		return p, errors.New("native process birth unavailable")
	}
	p.StartID = fmt.Sprintf("%08x:%08x", birth.HighDateTime, birth.LowDateTime)
	state, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return p, err
	}
	if state == windows.WAIT_OBJECT_0 {
		p.State = "exited"
		return p, nil
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return p, errors.New("unknown native process state")
	}
	var size uint32 = 32768
	buf := make([]uint16, size)
	if err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return p, err
	}
	exe, err := filepath.EvalSymlinks(windows.UTF16ToString(buf[:size]))
	if err != nil {
		return p, err
	}
	// The process handle retains birth identity if the numeric PID is reused.
	state, err = windows.WaitForSingleObject(h, 0)
	if err != nil {
		return p, err
	}
	if state == windows.WAIT_OBJECT_0 {
		p.State = "exited"
		return p, nil
	}
	if state != uint32(windows.WAIT_TIMEOUT) {
		return p, errors.New("unknown native process state")
	}
	p.State, p.Executable = "running", exe
	return p, nil
}
