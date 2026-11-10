// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomicSyncDir writes data to path and returns an error when the
// durability step fails. WriteFileAtomic is a different function and is not
// changed: it still warns and continues when a directory sync fails. Callers
// that need a failed durability step to fail the write use this function.
//
// Shared steps match WriteFileAtomic. Create the parent directory, write a
// temp file in that directory, flush the temp file, set permissions, close
// it, then rename it over path. The temp file flush is the data flush
// (FlushFileBuffers on Windows). After a successful rename, a durability
// error leaves the new bytes in place and is still returned: the directory
// entry may not be durable yet.
//
// Unix (darwin and Linux): the rename is os.Rename. The durability step
// opens the parent directory and syncs it. An open error and a sync error
// are both returned, and each names the directory and the step. This
// function has no warn-only branch.
//
// Windows: no directory handle is opened. os.Open on a directory is
// read-only, and FlushFileBuffers requires GENERIC_WRITE, which an
// unprivileged process cannot hold on a directory. The durability step is
// the rename itself: MoveFileEx with MOVEFILE_REPLACE_EXISTING and
// MOVEFILE_WRITE_THROUGH. A failure of that call is returned. This is the
// platform operation, not a skipped check and not the warn-only posture.
//
// What this function does not guarantee:
//   - MOVEFILE_WRITE_THROUGH's explicit flush wording covers copy-and-delete
//     moves. A same-volume rename only has "does not return until the file
//     is actually moved on the disk." That same-volume case is not
//     observable from user space, so a power loss can still drop the
//     directory entry.
//   - On non-Unix platforms Go documents that os.Rename is not atomic. This
//     function does not use os.Rename on Windows and does not claim that a
//     same-volume MoveFileEx is atomic.
//   - A rename does not order this file against files in other directories.
//   - On Windows a rename fails while any handle is open on the destination.
//     That error is returned.
func WriteFileAtomicSyncDir(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("fileutil: create directory %q: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("fileutil: create temp file in %q: %w", dir, err)
	}
	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return fmt.Errorf("fileutil: chmod temp file: %w", err)
	}

	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("fileutil: write temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("fileutil: sync temp file: %w", err)
	}
	if err := tmpFile.Chmod(perm); err != nil {
		return fmt.Errorf("fileutil: set permissions: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("fileutil: close temp file: %w", err)
	}

	if err := renameReplacingDurable(tmpPath, path); err != nil {
		return err
	}
	// The temp name is gone. A durability failure must not remove the target.
	cleanup = false
	return syncParentDir(dir)
}
