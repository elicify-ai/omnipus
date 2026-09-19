// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package environmentsetup

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// installLock is the workspace prefix lifetime lock: an advisory flock held
// on <workspace>/.omnipus/.install.lock for the whole installation
// (ES-FR-03/BDD-05 same-target serialization with existing primitives — the
// same advisory-lock primitive the shared-store publication lock uses).
//
// The lock file sits in the reserved .omnipus metadata dir but OUTSIDE the
// installer-writable prefix (.omnipus/env): the installer legitimately owns
// its prefix, so a generic script that clears and recreates it must not be
// able to unlink the lock mid-run and open the workspace to a second
// installer.
//
// The lock is deliberately NON-BLOCKING at acquire time: a second installer
// into the same workspace gets a clear busy refusal instead of an invisible
// unbounded block before a session_id exists. The kernel releases the lock
// when the handle closes or the holder process dies, so a crashed installer
// cannot strand the workspace.
//
// On Unix, flock covers same-process overlap too: two BeginInstall calls in
// one process open separate file descriptions, and flock treats them as
// independent lock holders.
//
// On Windows, lock_windows.go provides in-process serialization only — the
// repo's documented graceful-degradation posture for advisory locking
// (fileutil/flock_windows.go, Hard Constraint 4).
//
// The lock is an accidental-overlap guard, not adversarial: a same-user
// workspace owner can always trash their own workspace. flock on a replaced
// lock file falls back to locking the new file — a deleted lock file cannot
// strand the lock.
// installLock holds the open lock-file handle; the OS drops the advisory
// lock when this handle closes.
type installLock struct {
	f *os.File
}

func acquireInstallLock(root *os.Root, rel string) (*installLock, error) {
	f, err := root.OpenFile(rel, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", rel, err)
	}
	if lerr := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); lerr != nil {
		_ = f.Close()
		if errors.Is(lerr, unix.EWOULDBLOCK) || errors.Is(lerr, unix.EAGAIN) {
			return nil, fmt.Errorf("%w: %s", errTargetBusy, rel)
		}
		return nil, fmt.Errorf("lock %s: %w", rel, lerr)
	}
	return &installLock{f: f}, nil
}

// release drops the lifetime lock. Nil-safe so shared-scope targets can call
// it unconditionally. The kernel releases an advisory flock when the handle
// closes — and when the holder process dies — so no stale-lock state exists.
func (l *installLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = l.f.Close()
	l.f = nil
}
