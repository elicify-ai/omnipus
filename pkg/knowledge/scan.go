// Omnipus — knowledge base collection scanner (ADR-067 stage 2, unit B2).
//
// Scan walks a mounted collection root and produces the deterministic, sorted
// inventory that the manifest (FR-033) and the index (FR-034/FR-039a) work
// from. It answers three questions and nothing else:
//
//   - which files are in the collection, relative to its root;
//   - which of them are notes (parsed and full-text indexed) and which are
//     attachments (indexed by filename and path ONLY — FR-039a);
//   - the cheap freshness facts (size, mtime) the manifest compares against.
//
// What Scan deliberately does NOT do:
//
//   - It NEVER opens a file. Not a note, and emphatically not an attachment
//     (FR-039a / MV-19). Every content read in this package goes through the
//     openFileForRead seam in index.go so that "zero content reads" is a
//     countable property, not a claim.
//   - It NEVER follows a symbolic link. filepath.WalkDir uses ReadDir/Lstat
//     semantics, so a symlink — to a file, to a directory, inside or outside
//     the collection — is reported as a symlink entry and never descended
//     into. That is FR-044's "skip and report", and it is also why a symlink
//     loop cannot hang the walk: there is no edge to traverse.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// ScanKind distinguishes the two things a collection holds. The distinction is
// load-bearing for FR-039a: a note's bytes are read and indexed, an
// attachment's bytes are never touched at all.
type ScanKind string

const (
	// ScanKindNote is a markdown note: parsed, full-text indexed.
	ScanKindNote ScanKind = "note"
	// ScanKindAttachment is every other file: indexed by filename and path
	// only, its contents never opened (FR-039a).
	ScanKindAttachment ScanKind = "attachment"
)

// scanNoteExtensions is the closed set of extensions treated as notes. Anything
// else — including Obsidian's own .canvas and .base files — is an attachment,
// because this package has no parser for it and FR-039a forbids opening a file
// we cannot parse.
var scanNoteExtensions = map[string]struct{}{
	".md":       {},
	".markdown": {},
}

// scanSkippedDirNames are directory names never descended into. `.obsidian`
// and `.omnipus-vault` are the collection markers (FR-020) — configuration,
// not content. `.git` and `.trash` are tool state that would otherwise show up
// as thousands of phantom attachments.
//
// Note the omission: ordinary dotfiles are NOT skipped. DS-3 row 6 requires
// `.hidden.md` to be indexed (it is merely hidden in the explorer).
var scanSkippedDirNames = map[string]struct{}{
	".obsidian":      {},
	".omnipus-vault": {},
	".git":           {},
	".trash":         {},
}

// ScanEntry is one file in the collection.
type ScanEntry struct {
	// RelPath is the collection-relative path, always slash-separated so a
	// manifest written on one platform matches a scan on another.
	RelPath string
	// Kind is note or attachment.
	Kind ScanKind
	// Size is the file size in bytes, from Lstat.
	Size int64
	// ModTimeNanos is the modification time in Unix nanoseconds, from Lstat.
	ModTimeNanos int64
	// CtimeNanos is the file's BIRTH time in Unix nanoseconds, and HasCtime
	// says whether this platform recorded one at all (spec FR-133).
	//
	// It is here rather than left to the indexer because the indexer has no
	// os.FileInfo — this walk does, and the birth time is read from the SAME
	// one Size and ModTimeNanos come from. On macOS, the BSDs and Windows that
	// costs nothing: the value is already in the structure Lstat filled in. On
	// Linux it costs ONE statx(2) per file, because the birth time is reachable
	// no other way.
	//
	// HasCtime FALSE IS A REAL ANSWER, not a failure: a Linux kernel older than
	// 4.11, a filesystem that records no creation time, and any platform whose
	// stat structure has no birth field all land here, and `file.ctime` is then
	// ABSENT. It is never the POSIX inode-change time that shares the name —
	// that one moves on a chmod and is routinely LATER than the modification
	// time, which is exactly the kind of plausible wrong answer FR-133 exists
	// to refuse.
	//
	// Nothing here opens a file. statx(2) is a path lookup, so this package's
	// "never opens anything" property (FR-039a, and the header above) is
	// untouched.
	CtimeNanos int64
	HasCtime   bool
}

// ScanProblem records something the walk refused to do, or could not do. Both
// halves are reported rather than dropped: FR-044 requires a skipped symlink to
// be *reported*, and the collection's own integration boundary requires a
// permission error to be surfaced, "never skipped silently".
type ScanProblem struct {
	// RelPath is the collection-relative path the problem concerns.
	RelPath string
	// Reason is a stable, machine-readable cause.
	Reason ScanProblemReason
	// Detail carries the underlying error text, when there was one.
	Detail string
}

// ScanProblemReason is the closed set of reasons a path did not become an entry.
type ScanProblemReason string

const (
	// ScanProblemSymlink — the path is a symbolic link. Skipped, never
	// followed, on FR-044's terms.
	ScanProblemSymlink ScanProblemReason = "symlink_skipped"
	// ScanProblemUnreadable — the path could not be stat'd or its directory
	// could not be listed.
	ScanProblemUnreadable ScanProblemReason = "unreadable"
)

// ScanResult is the deterministic inventory of a collection.
type ScanResult struct {
	// Root is the resolved real path that was walked.
	Root string
	// Entries is every file found, sorted by RelPath. The sort is what makes
	// a rebuild reproducible (FR-046) rather than dependent on directory
	// order.
	Entries []ScanEntry
	// Problems is every path the walk skipped or could not read.
	Problems []ScanProblem
}

// Notes returns the note entries, in RelPath order.
func (r *ScanResult) Notes() []ScanEntry { return r.entriesOfKind(ScanKindNote) }

// Attachments returns the attachment entries, in RelPath order.
func (r *ScanResult) Attachments() []ScanEntry { return r.entriesOfKind(ScanKindAttachment) }

func (r *ScanResult) entriesOfKind(k ScanKind) []ScanEntry {
	out := make([]ScanEntry, 0, len(r.Entries))
	for _, e := range r.Entries {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

// ResolveCollectionRoot returns the collection root's resolved real path — the
// value FR-031 makes the index's identity. Two mounts naming the same folder by
// different routes (a symlink, a relative path, a trailing slash) resolve to the
// same string and therefore share one index.
func ResolveCollectionRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("knowledge: collection root is empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("knowledge: resolve collection root %q: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("knowledge: resolve collection root %q: %w", root, err)
	}
	return filepath.Clean(resolved), nil
}

// ScanKindFor classifies a path by extension. Exported because the index and
// the manifest must agree with the walk on what a note is.
func ScanKindFor(relPath string) ScanKind {
	ext := strings.ToLower(filepath.Ext(relPath))
	if _, ok := scanNoteExtensions[ext]; ok {
		return ScanKindNote
	}
	return ScanKindAttachment
}

// Scan walks the collection at root and returns its inventory.
//
// root is resolved to its real path first; every entry is therefore recorded
// relative to a canonical root, which is what lets the manifest survive the
// collection being mounted by a different route.
//
// Symbolic links are skipped and reported (FR-044). Because they are never
// followed, a link that points outside the collection cannot be used to read
// outside it, and a link that forms a loop cannot make the walk diverge.
func Scan(root string) (*ScanResult, error) {
	realRoot, err := ResolveCollectionRoot(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(realRoot)
	if err != nil {
		return nil, fmt.Errorf("knowledge: scan %q: %w", realRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("knowledge: scan %q: not a directory", realRoot)
	}

	res := &ScanResult{Root: realRoot}

	walkErr := filepath.WalkDir(realRoot, func(path string, d fs.DirEntry, err error) error {
		rel := scanRelPath(realRoot, path)
		if err != nil {
			// A directory we could not list, or an entry we could not stat.
			// Reported, never silently dropped; the walk continues.
			res.Problems = append(res.Problems, ScanProblem{
				RelPath: rel, Reason: ScanProblemUnreadable, Detail: err.Error(),
			})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == realRoot {
			return nil
		}

		// A symlink — of any kind, to anywhere — is skipped and reported.
		// WalkDir never follows it, so no loop can develop and nothing
		// outside the collection is reachable through it.
		if d.Type()&fs.ModeSymlink != 0 {
			res.Problems = append(res.Problems, ScanProblem{
				RelPath: rel, Reason: ScanProblemSymlink,
			})
			return nil
		}

		if d.IsDir() {
			if _, skip := scanSkippedDirNames[d.Name()]; skip {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			// Sockets, devices, fifos: not content, never opened.
			res.Problems = append(res.Problems, ScanProblem{
				RelPath: rel, Reason: ScanProblemUnreadable,
				Detail: "not a regular file",
			})
			return nil
		}

		fi, statErr := d.Info()
		if statErr != nil {
			// Deliberately NOT propagated: one unstattable entry (a file removed
			// mid-walk, a permission hole) must not abort the whole collection.
			// It is recorded as a problem so it is visible, and the walk goes on.
			res.Problems = append(res.Problems, ScanProblem{
				RelPath: rel, Reason: ScanProblemUnreadable, Detail: statErr.Error(),
			})
			return nil //nolint:nilerr // reported as a ScanProblem; the walk must continue
		}
		// The birth time comes from the os.FileInfo already in hand, so on every
		// platform but Linux this is a struct-field read rather than a syscall.
		// It lives in pkg/fileutil rather than in the package that STORES it
		// because pkg/records/propindex's own tests import this package, so
		// depending on propindex here is an import cycle in the test binary.
		entry := ScanEntry{
			RelPath:      rel,
			Kind:         ScanKindFor(rel),
			Size:         fi.Size(),
			ModTimeNanos: fi.ModTime().UnixNano(),
		}
		if bt, ok := fileutil.BirthTime(path, fi); ok {
			entry.CtimeNanos, entry.HasCtime = bt.UnixNano(), true
		}
		res.Entries = append(res.Entries, entry)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("knowledge: scan %q: %w", realRoot, walkErr)
	}

	sort.Slice(res.Entries, func(i, j int) bool { return res.Entries[i].RelPath < res.Entries[j].RelPath })
	sort.Slice(res.Problems, func(i, j int) bool {
		if res.Problems[i].RelPath != res.Problems[j].RelPath {
			return res.Problems[i].RelPath < res.Problems[j].RelPath
		}
		return res.Problems[i].Reason < res.Problems[j].Reason
	})
	return res, nil
}

// scanRelPath converts an absolute walk path into the canonical
// slash-separated, collection-relative form used everywhere in this package.
func scanRelPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
