// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package credentials

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockSidecar takes an exclusive LockFileEx lock on the sidecar without
// blocking (FR-049: the credential store has a real cross-process lock on
// Windows too). The handle is on the sidecar, never on credentials.json, so
// the atomic rename over the store file is not hindered.
func tryLockSidecar(f *os.File) (bool, error) {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}

func unlockSidecar(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol)
}
