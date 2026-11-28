// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package credentials

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockSidecar takes the exclusive advisory lock without blocking. It
// reports false when another holder has it. The lock is compatible with
// fileutil.WithFlock (both use flock on the same sidecar).
func tryLockSidecar(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlockSidecar(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
