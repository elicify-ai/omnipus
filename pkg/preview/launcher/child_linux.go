// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package launcher

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// launcherStackBytes is the stack limit set with the data limit (spec §3.1
// D1.2): the data limit does not count the main thread's stack.
const launcherStackBytes = 8 << 20

// applyLimits sets the Linux limits before the go-byte (AD-2 order). Memory
// is the data limit (RLIMIT_DATA, spec §3.1 D1): it counts the private
// writable memory a program uses, not the address space the Go runtime only
// reserves, so the launcher and Go children start under it (O3 defects 1
// and 3). The stack limit goes with it, never above the inherited one.
func applyLimits(l Limits) error {
	if l.MemoryBytes > 0 {
		if err := setRlimit(unix.RLIMIT_DATA, l.MemoryBytes, "data"); err != nil {
			return err
		}
		if err := setStackLimit(); err != nil {
			return err
		}
	}
	return applyCommonLimits(l)
}

// setStackLimit sets soft = hard = min(8 MiB, inherited soft limit).
func setStackLimit() error {
	var cur unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_STACK, &cur); err != nil {
		return fmt.Errorf("read stack limit: %w", err)
	}
	v := uint64(launcherStackBytes)
	if cur.Cur < v { // RLIM_INFINITY is the largest value: never lowers v
		v = cur.Cur
	}
	return setRlimit(unix.RLIMIT_STACK, v, "stack")
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
