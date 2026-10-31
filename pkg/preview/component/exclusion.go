// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// WriteRoot is one place an agent may write: a workspace work tree, a mount,
// an operator allowance or a grant.
type WriteRoot struct {
	// Path is the folder the authority covers (everything at or below it).
	// A regex allowance that names a text prefix rather than a folder is
	// reduced to the folder holding that prefix before it gets here
	// (pkg/preview/writeroots.go::allowanceRoots).
	Path string
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
// Both paths are compared in two spellings: as written, and with every
// symlink in the longest existing part resolved (so a writable alias that
// points into the component, or a component reached through a symlink, keeps
// its physical ancestry, W2-S02 R2). Each pair is decided by filesystem
// identity where the paths exist (pkg/fspolicy::CoversForDeny: device +
// inode along the ancestor chain, so a case or Unicode-normalization
// spelling of the same folder on macOS still overlaps), else by the
// case-folded comparison — the refusing direction. A path whose symlinks
// cannot be resolved (a permission error, a dangling link) overlaps: an
// undecidable authority keeps preview off.
func FirstOverlap(root string, roots []WriteRoot) *WriteRoot {
	rs, rok := spellings(root)
	for i := range roots {
		w := roots[i]
		if w.All {
			return &roots[i]
		}
		if w.Path == "" {
			continue
		}
		ws, wok := spellings(w.Path)
		if !rok || !wok || overlapsAny(rs, ws) {
			return &roots[i]
		}
	}
	return nil
}

func overlapsAny(as, bs []string) bool {
	for _, a := range as {
		for _, b := range bs {
			if fspolicy.CoversForDeny(a, b) || fspolicy.CoversForDeny(b, a) {
				return true
			}
		}
	}
	return false
}

// spellings returns p as written (absolute, cleaned) and with its existing
// part's symlinks resolved; ok is false when resolution hit anything other
// than a missing component.
func spellings(p string) ([]string, bool) {
	lexical := absClean(p)
	resolved, err := resolveExisting(lexical)
	if err != nil {
		return []string{lexical}, false
	}
	if resolved == lexical {
		return []string{lexical}, true
	}
	return []string{lexical, resolved}, true
}

// resolveExisting resolves every symlink in the longest existing ancestor of
// p and appends the missing rest unchanged. A dangling symlink is an error:
// its target is where a write through it would land, and that is unknown.
func resolveExisting(p string) (string, error) {
	rest := ""
	for cur := p; ; {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(r, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", err
		}
		if info, lerr := os.Lstat(cur); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("dangling symlink " + cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
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
