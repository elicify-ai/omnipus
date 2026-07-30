// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"fmt"
	"os"
	"path"
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

	// Case-insensitive collision backstop (see caseInsensitiveMatch's
	// doc): the exact-case Stat miss above only proves no entry exists
	// with rel's EXACT casing. Mirror mkdir -p's own idempotency, extended
	// to case: a differently-cased sibling that is ITSELF a directory is
	// treated as the same idempotent success an exact-case Mkdir already
	// gets above (Windows/macOS would resolve them to the identical
	// directory anyway); a differently-cased sibling that is a FILE is
	// ErrAlreadyExists, consistent with the exact-case rule just above.
	parent := dirParent(rel)
	if match, found, ciErr := r.caseInsensitiveMatch(parent, path.Base(rel)); ciErr != nil {
		return nil, false, ciErr
	} else if found {
		matchedRel := match
		if parent != "" {
			matchedRel = parent + "/" + match
		}
		existing, statErr := r.root.Stat(matchedRel)
		if statErr != nil {
			return nil, false, translateErr(statErr)
		}
		if !existing.IsDir() {
			return nil, false, ErrAlreadyExists
		}
		return existing, false, nil
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
