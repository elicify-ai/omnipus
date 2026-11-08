// Omnipus — a ControlFS anchored in one os.Root (#1017).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// LoadControlCheckedAt opens an os.Root at the knowledge base root vaultRoot
// and runs LoadControlChecked through it, so no schema or view read can leave
// the knowledge base. vaultRoot also names files in the reports, exactly as
// LoadSchemas/LoadViews name them.
func LoadControlCheckedAt(ctx context.Context, vaultRoot string, limits LoadLimits) (CheckedControl, error) {
	root, err := os.OpenRoot(vaultRoot)
	if err != nil {
		return CheckedControl{}, fmt.Errorf("opening knowledge base root: %w", err)
	}
	defer root.Close()
	return LoadControlChecked(ctx, RootControlFS{Root: root}, vaultRoot, limits)
}

// RootControlFS is a ControlFS whose every access goes through root, which
// is opened at the knowledge base root: os.Root refuses any name, link or
// parent step that would leave it, at the moment of each call.
type RootControlFS struct {
	Root *os.Root
}

// Lstat describes name without following a final link.
func (c RootControlFS) Lstat(name string) (fs.FileInfo, error) {
	return c.Root.Lstat(filepath.FromSlash(name))
}

// ReadDir lists the directory name.
func (c RootControlFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(c.Root.FS(), name)
}

// errOpenedNotRegular is returned when the opened object is not a regular
// file (a FIFO, a device, a folder swapped in after the listing).
var errOpenedNotRegular = errors.New("the opened file is not a regular file")

// OpenRegular opens name non-blocking (so a FIFO can never stall the
// caller), then checks the kind on the opened file itself.
func (c RootControlFS) OpenRegular(name string) (fs.File, error) {
	f, err := c.Root.OpenFile(filepath.FromSlash(name), nonBlockingReadFlags(), 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errOpenedNotRegular
	}
	return f, nil
}
