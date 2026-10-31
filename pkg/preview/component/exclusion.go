// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// WriteRoot is one place an agent may write, from configuration: a
// workspace work tree, a mount, an operator allowance or a grant.
type WriteRoot struct {
	Path string
	// Grant names the grant for the person and the doctor line, e.g.
	// "mount docs of workspace ws-1".
	Grant string
}

// FirstOverlap returns the first write root that is inside, equal to, or
// contains root (AD-4 writer exclusion, R2-MAJ-012), or nil. Both sides are
// compared by real path where they exist.
func FirstOverlap(root string, roots []WriteRoot) *WriteRoot {
	r := canonical(root)
	for i := range roots {
		w := canonical(roots[i].Path)
		if w == "" {
			continue
		}
		if contains(w, r) || contains(r, w) {
			return &roots[i]
		}
	}
	return nil
}

func canonical(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// contains reports whether child is parent or below it. Case-insensitive on
// the platforms whose default filesystems fold case is deliberately NOT
// assumed: real paths are compared as the kernel returned them.
func contains(parent, child string) bool {
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
