// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

// SizeGuard bounds a single boundary commit's write-set (OBS-3, FR-153).
// The guard exists BECAUSE git-level compaction cannot be relied on to
// undo the damage once large/incompressible content has landed: the go-git
// embedding spike measured `.git` growing exactly 1:1 with re-encoded
// media bytes across a commit, and `RepackObjects` (go-git's `git gc`
// analogue) consolidated loose objects into packfiles WITHOUT shrinking
// total bytes. A guard that trips BEFORE the commit is the only reliable
// control point — retention/compaction after the fact does not help.
//
// Retention note (spike-derived recommendation, not enforced by this
// package): keep write-sets scoped to source/output paths and route large
// generated media (screenshots, recordings, rendered documents) through
// Omnipus's existing file-based artifact storage instead of the auto-commit
// path; if a plan genuinely needs many attempts against a media-heavy
// work/ tree, cap retained attempt history at the plan-engine layer rather
// than relying on this package to prune committed history (this package
// never rewrites or deletes history — see the package doc's caveat 3).
type SizeGuard struct {
	// MaxFiles is the maximum number of write-set files a single boundary
	// commit may touch (after directory expansion). <= 0 means unlimited.
	MaxFiles int
	// MaxBytes is the maximum total size, in bytes, of the write-set files
	// a single boundary commit may touch. <= 0 means unlimited.
	MaxBytes int64
}

// DefaultSizeGuard returns the guard applied when a Repo is opened without
// an explicit WithSizeGuard option: 2000 files / 200 MiB per boundary
// commit. These defaults are deliberately generous for ordinary
// source-and-output work/ trees and deliberately tight against the
// media-heavy case the spike measured (a single 15 MB recording + a couple
// of re-encodes would consume a meaningful fraction of the byte budget on
// its own) — operators with a genuine need for larger commits should pass
// WithSizeGuard explicitly rather than this package silently accepting an
// unbounded write-set.
func DefaultSizeGuard() SizeGuard {
	return SizeGuard{
		MaxFiles: 2000,
		MaxBytes: 200 * 1024 * 1024,
	}
}

// exceeds reports whether the guard trips for the given file count/byte
// total. A zero-or-negative bound means "no limit" for that dimension.
func (g SizeGuard) exceeds(files int, bytes int64) bool {
	if g.MaxFiles > 0 && files > g.MaxFiles {
		return true
	}
	if g.MaxBytes > 0 && bytes > g.MaxBytes {
		return true
	}
	return false
}
