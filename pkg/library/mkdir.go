// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"fmt"
	"os"
)

// Mkdir creates the directory at rel (already-cleaned, non-root), creating
// any missing intermediate directories along the way — mkdir -p semantics —
// the sole directory-creation primitive the Library exposes (UAT Issue 4: a
// file explorer with no way to create a folder, and a non-malicious nested
// Move/Copy destination like "subfolder/test.txt" had no path to success
// because those operations deliberately do not auto-create missing parents;
// see requireParentDir's doc). Idempotent: if a directory already exists at
// rel, this returns its existing os.FileInfo with created=false rather than
// an error, mirroring mkdir -p / os.MkdirAll's own idempotency. Returns
// ErrAlreadyExists if a regular FILE already exists at rel (a file cannot be
// turned into a directory), ErrNotDir if an intermediate path component
// exists as a file (e.g. Mkdir("a/b") when "a" is already a file),
// ErrInvalidPath for the reserved work-tree root (""), and ErrOutsideRoot if
// rel resolves outside the workspace's work tree via a symlink.
func (r *Root) Mkdir(rel string) (fi os.FileInfo, created bool, err error) {
	if rel == "" {
		return nil, false, ErrInvalidPath
	}
	if existing, statErr := r.root.Stat(rel); statErr == nil {
		if !existing.IsDir() {
			return nil, false, ErrAlreadyExists
		}
		return existing, false, nil
	} else if !os.IsNotExist(statErr) {
		return nil, false, translateErr(statErr)
	}

	if mkErr := r.root.MkdirAll(rel, 0o700); mkErr != nil {
		return nil, false, translateErr(mkErr)
	}
	fi, err = r.root.Stat(rel)
	if err != nil {
		return nil, false, fmt.Errorf("library: stat created directory: %w", err)
	}
	return fi, true, nil
}
