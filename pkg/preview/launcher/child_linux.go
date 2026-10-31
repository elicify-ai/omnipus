// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package launcher

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func applyLimits(l Limits) error {
	if l.MemoryBytes > 0 {
		resource, name := unix.RLIMIT_AS, "address-space"
		if l.MemoryKind == "data" {
			resource, name = unix.RLIMIT_DATA, "data"
		}
		if err := setRlimit(resource, l.MemoryBytes, name); err != nil {
			return err
		}
	}
	return applyCommonLimits(l)
}

// armParentDeath sets the parent-death SIGKILL (it survives execve) and then
// checks the parent is still the one that started us: a parent that died
// before prctl would otherwise leave the target unowned.
func armParentDeath() error {
	parent := os.Getppid()
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGKILL), 0, 0, 0); err != nil {
		return err
	}
	if os.Getppid() != parent || parent == 1 {
		return errors.New("parent exited before limits were in force")
	}
	return nil
}
