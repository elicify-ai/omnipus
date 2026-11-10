// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package fileutil

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// renameReplacingDurable replaces path with tmpPath and asks Windows to
// return only once the move has reached disk. MOVEFILE_COPY_ALLOWED is not
// set: the temp file and the target are in the same directory, so a
// cross-volume copy must not be used as a fallback.
//
// This is not os.Rename. os.Rename on Windows is MoveFileEx with
// MOVEFILE_REPLACE_EXISTING only, and Go documents that Rename is not atomic
// on non-Unix platforms. WRITE_THROUGH is strictly stronger and its error is
// returned. It is still not a power-loss guarantee for a same-volume rename:
// the explicit flush sentence in the platform documentation covers
// copy-and-delete moves.
func renameReplacingDurable(tmpPath, path string) error {
	from, err := windows.UTF16PtrFromString(tmpPath)
	if err != nil {
		return fmt.Errorf("fileutil: write-through rename %q to %q: %w", tmpPath, path, err)
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("fileutil: write-through rename %q to %q: %w", tmpPath, path, err)
	}
	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING | windows.MOVEFILE_WRITE_THROUGH)
	if err := windows.MoveFileEx(from, to, flags); err != nil {
		return fmt.Errorf("fileutil: write-through rename %q to %q: %w", tmpPath, path, err)
	}
	return nil
}

// syncParentDir does not open a directory on Windows. An unprivileged
// process cannot obtain the writable directory handle FlushFileBuffers
// requires, so there is no directory flush to attempt. The write-through
// rename above is the durability step; its error is already returned.
func syncParentDir(string) error {
	return nil
}
