// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package fileutil

import (
	"errors"
	"fmt"
	"os"
)

func renameReplacingDurable(tmpPath, path string) error {
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("fileutil: rename temp file %q to %q: %w", tmpPath, path, err)
	}
	return nil
}

// syncParentDir persists the rename's directory entry. Both the open and the
// sync are returned; neither is warned and ignored.
func syncParentDir(dir string) error {
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("fileutil: open dir for sync %q: %w", dir, err)
	}
	syncErr := dirFile.Sync()
	closeErr := dirFile.Close()
	if syncErr != nil || closeErr != nil {
		return fmt.Errorf("fileutil: sync dir after rename %q: %w", dir, errors.Join(syncErr, closeErr))
	}
	return nil
}
