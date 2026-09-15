// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package fileutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func flockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func flockUnlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

// flockOpenedHook, when non-nil, runs each time WithFlock has opened its lock
// file and before it tries to lock it. Test seam only (flock_unix_test.go): it
// lets a test know that a caller holds the CURRENT lock file open, so the "lock
// file removed while a caller waits for it" path is exercised deterministically
// instead of raced. Set it before starting any goroutine that calls WithFlock.
var flockOpenedHook func(path string)

// WithFlock acquires an OS-level advisory exclusive lock on the file at path
// (creating it if absent), calls fn, then releases the lock. It is the
// cross-process half of a store's locking; the in-process half is the store's
// own mutex.
//
// Lock a sidecar (SidecarLockPath), never the data file fn replaces: opening
// the data file here creates it empty on its first write, and a rename over it
// moves the data file to a new inode that this lock does not cover. See
// SidecarLockPath.
//
// The lock is only ever held on the file that is at path when fn starts. If the
// lock file was removed or replaced while this call waited for it — a holder
// deleting a record removes its sidecar (RemoveLocked) — the lock on the old
// file is dropped and the file now at path is opened and locked instead. That
// is what makes removing a held lock file safe. If path can no longer be opened
// (its directory was removed), the open error is returned and fn does not run.
//
// On Windows this function is provided by flock_windows.go and calls fn
// directly without opening the file, because an open handle on the destination
// file prevents WriteFileAtomic from renaming the temp file over it.
//
// Errors from fn, flockUnlock, and f.Close are all captured and joined so
// none are silently discarded.
func WithFlock(path string, fn func() error) (retErr error) {
	f, err := lockCurrentFile(path)
	if err != nil {
		return err
	}
	// Defer close so it always runs; capture its error and join with retErr.
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("fileutil: close flock %q: %w", path, closeErr))
		}
	}()
	// Defer unlock so it always runs after fn; capture its error and join with retErr.
	defer func() {
		if unlockErr := flockUnlock(f); unlockErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("fileutil: release flock %q: %w", path, unlockErr))
		}
	}()

	return fn()
}

// lockCurrentFile opens path, locks it exclusively, and returns the open file
// once the lock is held on the file that is at path now. A lock that turns out
// to be on a file since removed from, or replaced at, path is released and the
// open is retried. Each retry means another holder removed the file while it
// held the lock, so the loop cannot spin without other callers making progress.
func lockCurrentFile(path string) (*os.File, error) {
	for {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return nil, fmt.Errorf("fileutil: open for flock %q: %w", path, err)
		}
		if flockOpenedHook != nil {
			flockOpenedHook(path)
		}
		if err := flockExclusive(f); err != nil {
			lockErr := fmt.Errorf("fileutil: acquire flock %q: %w", path, err)
			if closeErr := f.Close(); closeErr != nil {
				lockErr = errors.Join(lockErr, fmt.Errorf("fileutil: close flock %q: %w", path, closeErr))
			}
			return nil, lockErr
		}

		current, statErr := heldFileIsAtPath(f, path)
		if statErr == nil && current {
			return f, nil
		}
		var errs []error
		if statErr != nil {
			errs = append(errs, statErr)
		}
		if unlockErr := flockUnlock(f); unlockErr != nil {
			errs = append(errs, fmt.Errorf("fileutil: release flock %q: %w", path, unlockErr))
		}
		if closeErr := f.Close(); closeErr != nil {
			errs = append(errs, fmt.Errorf("fileutil: close flock %q: %w", path, closeErr))
		}
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
	}
}

// heldFileIsAtPath reports whether the open file f is still the file at path.
// A path that no longer exists reports false with no error, so the caller
// retries and surfaces the open error itself if the directory is gone too.
func heldFileIsAtPath(f *os.File, path string) (bool, error) {
	held, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("fileutil: stat held flock %q: %w", path, err)
	}
	onDisk, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("fileutil: stat flock path %q: %w", path, err)
	}
	return os.SameFile(held, onDisk), nil
}
