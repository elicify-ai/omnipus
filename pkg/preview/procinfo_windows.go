// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package preview

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func openProcess(pid int) (windows.Handle, error) {
	return windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
}

// processStart returns the process creation time.
func processStart(pid int) (string, error) {
	h, err := openProcess(pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return fmt.Sprintf("ft:%d", created.Nanoseconds()), nil
}

func processExecutable(pid int) (string, error) {
	h, err := openProcess(pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n]), nil
}

func executableMatches(recorded, candidate string) bool { return recorded == candidate }

func killPID(pid int) {
	h, err := openProcess(pid)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.TerminateProcess(h, 1)
}

// killGroup: on Windows the launcher's job owns the tree; killing the
// launcher (killPID) ends it.
func killGroup(int) {}

func processAlive(pid int) bool {
	h, err := openProcess(pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}
