// Omnipus — rooted file operations for pipeline-owned views.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// managedViewParent pins the verified parent directory. A parent symlink
// swapped between containment checking and opening cannot redirect a later
// write or unlink outside the collection. The caller closes the returned root.
func (m *ViewMembership) managedViewParent(rel string) (*os.Root, string, error) {
	root, err := NewCollectionRoot(OSLinkFS(), m.Root)
	if err != nil {
		return nil, "", err
	}
	abs, err := root.ResolveContainedNoSymlink(OSLinkFS(), rel)
	if err != nil {
		return nil, "", err
	}
	before, err := os.Lstat(filepath.Dir(abs))
	if err != nil {
		return nil, "", err
	}
	if !before.IsDir() {
		return nil, "", fmt.Errorf("knowledge: managed view parent is not a directory: %q", rel)
	}
	collection, err := os.OpenRoot(root.Path())
	if err != nil {
		return nil, "", err
	}
	parent, openErr := collection.OpenRoot(filepath.Dir(filepath.FromSlash(rel)))
	closeErr := collection.Close()
	if openErr != nil {
		return nil, "", errors.Join(openErr, closeErr)
	}
	if closeErr != nil {
		return nil, "", errors.Join(closeErr, parent.Close())
	}
	current, statErr := parent.Stat(".")
	if statErr != nil || !os.SameFile(before, current) {
		return nil, "", errors.Join(fmt.Errorf("knowledge: managed view parent changed while opening %q", rel), statErr, parent.Close())
	}
	return parent, filepath.Base(abs), nil
}

// writeManagedViewBytes replaces only an existing, verified regular view. The
// temp file and rename use the SAME pinned parent, not two pathname lookups
// separated by a symlink-swap window.
func (m *ViewMembership) writeManagedViewBytes(rel string, data []byte) (returnErr error) {
	parent, leaf, err := m.managedViewParent(rel)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Lstat(leaf)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("knowledge: managed view is no longer a regular file: %q", rel)
	}
	name := ".tmp-" + rand.Text()
	file, err := parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("knowledge: create managed view temp file: %w", err)
	}
	created, statErr := file.Stat()
	if statErr != nil {
		return errors.Join(statErr, file.Close())
	}
	cleanup := true
	fileClosed := false
	defer func() {
		if cleanup {
			if !fileClosed {
				returnErr = errors.Join(returnErr, file.Close())
			}
			returnErr = errors.Join(returnErr, removeCreatedView(parent, name, created))
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	closeErr := file.Close()
	fileClosed = true
	if closeErr != nil {
		return closeErr
	}
	if err := parent.Rename(name, leaf); err != nil {
		return fmt.Errorf("knowledge: atomically replace managed view %q: %w", rel, err)
	}
	cleanup = false
	if dir, err := parent.Open("."); err == nil {
		if syncErr := dir.Sync(); syncErr != nil {
			slog.Warn("knowledge: directory sync after managed view write failed", "path", rel, "error", syncErr)
		}
		_ = dir.Close()
	}
	return nil
}

// removeCreatedView never unlinks a replacement solely because it occupies a
// pathname formerly used by this operation. Its caller already returns the
// triggering failure to the user; a mismatch is additionally reported there.
func removeCreatedView(parent *os.Root, leaf string, created fs.FileInfo) error {
	current, err := parent.Lstat(leaf)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(current, created) {
		return fmt.Errorf("knowledge: cleanup refused because created file %q was replaced", leaf)
	}
	return parent.Remove(leaf)
}
