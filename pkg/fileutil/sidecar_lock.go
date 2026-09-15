// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fileutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// SidecarLockPath returns the lock file that serializes cross-process writers
// of the data file at path: path + ".lock". Lock this path — never the data
// file itself — around a WriteFileAtomic of path:
//
//	lock := fileutil.SidecarLockPath(p)
//	err := fileutil.WithFlock(lock, func() error {
//		return fileutil.WriteFileAtomic(p, data, 0o600)
//	})
//
// Why a separate file. WithFlock opens the path it locks with O_CREATE, and
// WriteFileAtomic replaces its target by renaming a temp file over it. Locking
// the target itself had two defects:
//
//   - The first write of a file created it EMPTY and left it that way through
//     the temp-file write and its fsync, until the rename replaced it. A reader
//     outside the writer's in-process mutex (another process, a second store
//     over the same directory) read zero bytes and saw a corrupt record, and a
//     crash inside that window left the empty file behind for good.
//   - The rename unlinks the inode the lock was taken on, so a writer that
//     opened the path before the rename and one that opened it after held locks
//     on two different files and ran at the same time.
//
// A sidecar is never renamed over, so neither can happen, and the data file is
// only ever created by the rename.
//
// Every writer and every other locker of the same data file must use the same
// sidecar, or they stop excluding each other. Listers of a store directory must
// select data files by their own suffix (".json", ".jsonl", ...) so a sidecar is
// never read as data. pkg/entity names its sidecars <id>.lock rather than
// <id>.json.lock; that scheme predates this helper and is deliberately left
// alone.
//
// On Windows WithFlock is a no-op that never opens its path, so no sidecar file
// is created there.
func SidecarLockPath(path string) string {
	return path + ".lock"
}

// RemoveLocked removes the data file at path and its sidecar lock file
// (SidecarLockPath), holding that sidecar's lock while it does. It is the
// delete counterpart of WithFlock(SidecarLockPath(path), WriteFileAtomic...):
// taking the same lock means a concurrent writer cannot rename a new copy into
// place after the remove and resurrect the record, and removing the sidecar
// means a deleted record leaves nothing behind.
//
// Removing a lock file while holding it is safe because WithFlock, once it has
// the lock, confirms it is holding the file that is at the path NOW and
// otherwise retries. A caller that opened the sidecar before this removal
// therefore moves on to the file a later caller created, instead of running
// alongside it on the removed one.
//
// When path does not exist RemoveLocked returns an error for which
// errors.Is(err, fs.ErrNotExist) is true, and creates no sidecar. The same
// holds when another remover deleted path while this call waited for the lock;
// the sidecar is removed in that case too.
func RemoveLocked(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("fileutil: remove %q: %w", path, err)
	}
	lockPath := SidecarLockPath(path)
	return WithFlock(lockPath, func() error {
		var errs []error
		if err := os.Remove(path); err != nil {
			errs = append(errs, fmt.Errorf("fileutil: remove %q: %w", path, err))
		}
		// This call created or holds the sidecar, so it goes whether or not the
		// data file was still there.
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("fileutil: remove lock file %q: %w", lockPath, err))
		}
		return errors.Join(errs...)
	})
}
