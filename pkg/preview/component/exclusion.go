// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// WriteRoot is one place an agent may write: a workspace work tree, a mount,
// an operator allowance or a grant.
type WriteRoot struct {
	// Path is the folder (or, with Prefix, the path text) the authority
	// covers.
	Path string
	// Prefix marks a path-text prefix rather than a folder: the authority
	// covers every path whose text starts with Path ("^/srv/data" also
	// covers "/srv/data2/..."). Set for regex write allowances.
	Prefix bool
	// All marks an authority whose extent cannot be determined (a write
	// allowance Omnipus cannot reduce to a folder, or a write source that
	// could not be read). It overlaps every component: preview stays off
	// until it is removed (AD-4 fails closed).
	All bool
	// Grant names the grant for the person and the doctor line, e.g.
	// "mount docs of workspace ws-1".
	Grant string
}

// FirstOverlap returns the first write root that is inside, equal to, or
// contains root (AD-4 writer exclusion, R2-MAJ-012), or nil.
//
// Containment is decided by filesystem identity where the paths exist
// (pkg/fspolicy::CoversForDeny: device + inode along the ancestor chain), so
// a case or Unicode-normalization spelling of the same folder on macOS, or a
// symlinked spelling, still overlaps; where identity cannot answer, the
// case-folded comparison — the refusing direction — decides.
func FirstOverlap(root string, roots []WriteRoot) *WriteRoot {
	r := absClean(root)
	for i := range roots {
		w := roots[i]
		if w.All {
			return &roots[i]
		}
		if w.Path == "" {
			continue
		}
		if w.Prefix {
			prefix := absClean(w.Path)
			if strings.HasSuffix(w.Path, string(filepath.Separator)) && !strings.HasSuffix(prefix, string(filepath.Separator)) {
				prefix += string(filepath.Separator)
			}
			if prefixOverlaps(prefix, r) {
				return &roots[i]
			}
			continue
		}
		p := absClean(w.Path)
		if fspolicy.CoversForDeny(p, r) || fspolicy.CoversForDeny(r, p) {
			return &roots[i]
		}
	}
	return nil
}

// prefixOverlaps reports whether any path whose text starts with prefix can
// be root, lie under root, or contain root. The prefix splits into a folder
// (dir) and the start of one name inside it (stem): writable paths are dir's
// entries whose names start with stem, and everything below them.
func prefixOverlaps(prefix, root string) bool {
	dir, stem := prefix, ""
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		dir, stem = filepath.Dir(prefix), filepath.Base(prefix)
	}
	if !isASCII(stem) {
		// Case folding of non-ASCII names and Unicode normalization cannot
		// be decided from text: treat the whole folder as writable.
		stem = ""
	}
	if stem == "" {
		return fspolicy.CoversForDeny(dir, root) || fspolicy.CoversForDeny(root, dir)
	}
	// Writable paths below root: dir itself is inside root.
	if fspolicy.CoversForDeny(root, dir) {
		return true
	}
	// root (or one of its ancestors) is an entry of dir named stem...
	for a := root; ; {
		parent := filepath.Dir(a)
		if parent == a {
			return false
		}
		if strings.HasPrefix(strings.ToLower(filepath.Base(a)), strings.ToLower(stem)) && sameDir(parent, dir) {
			return true
		}
		a = parent
	}
}

// sameDir is identity equality with the refusing (case-folded) fallback.
func sameDir(a, b string) bool {
	if ai, err := os.Stat(a); err == nil {
		if bi, err := os.Stat(b); err == nil {
			return os.SameFile(ai, bi)
		}
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func absClean(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
