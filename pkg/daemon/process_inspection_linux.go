//go:build linux

package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
	stat := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	before, err := os.ReadFile(stat)
	if err != nil {
		return p, err
	}
	state, birth, err := linuxProcessBirth(before)
	if err != nil {
		return p, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return p, errors.New("native boot identity unavailable")
	}
	p.StartID = strings.TrimSpace(string(boot)) + ":" + birth
	if state == "Z" || state == "X" {
		p.State = "exited"
		return p, nil
	}
	exe, err := filepath.EvalSymlinks(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return p, err
	}
	after, err := os.ReadFile(stat)
	if err != nil {
		return p, err
	}
	newState, newBirth, err := linuxProcessBirth(after)
	if err != nil || birth != newBirth {
		return p, errors.New("native process identity changed")
	}
	if newState == "Z" || newState == "X" {
		p.State = "exited"
		return p, nil
	}
	p.State, p.Executable = "running", exe
	return p, nil
}

func linuxProcessBirth(stat []byte) (string, string, error) {
	// comm is parenthesized and may contain whitespace or ')'. Field 22 is
	// starttime; fields after the final ')' begin with state (field 3).
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return "", "", errors.New("invalid native process stat")
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 {
		return "", "", errors.New("incomplete native process stat")
	}
	birth, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || birth == 0 {
		return "", "", fmt.Errorf("invalid native process birth")
	}
	return fields[0], strconv.FormatUint(birth, 10), nil
}
