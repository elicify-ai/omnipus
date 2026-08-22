// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// contain.go — the containment core of the knowledge-base link graph
// (ADR-067 D6, FR-043, FR-044, NB-6, NB-7, NB-9).
//
// Everything in this file exists because of one fact: the indexer walks a real
// folder on the operator's disk with the operator's full filesystem
// permissions, and the links it follows are attacker-controllable text that
// lives inside files Omnipus did not write. A note is a document, but
// "[[../../../.ssh/id_rsa]]" inside it is an instruction, and the only safe
// posture is to treat every path that arrives from a file as hostile until it
// has been proven to resolve inside the collection root.
//
// Two rules, both unconditional on every platform (NB-16):
//
//   - FR-043 — every walked path AND every resolved link target MUST resolve
//     inside the collection root, checked against the REAL path after symlink
//     resolution, not the lexical one. A lexical check alone is defeated by a
//     single symlink: "notes/private" is lexically inside the root and may
//     point anywhere on the disk.
//
//   - FR-044 — symbolic links are SKIPPED and REPORTED, never followed. This
//     is stricter than "followed but contained", and deliberately so: it also
//     disposes of loop detection entirely (ADR-067 D6). A symlink that is
//     never traversed cannot form a cycle, so the walk of a real tree is
//     finite by construction rather than by a visited-set heuristic.
//
// Nothing here reads a file's CONTENTS. Containment is decided with lstat and
// symlink resolution only (AC-6.2: a read-recording filesystem fake must count
// zero content reads for a refused target).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Errors returned by the containment layer. They are sentinel values so a
// caller can distinguish "this link points nowhere" from "this link tried to
// leave the collection", which are very different events: the first is an
// ordinary broken link, the second is worth an operator's attention.
var (
	// ErrCollectionRootInvalid means the supplied root is not a usable
	// collection root: empty, relative, missing, or not a directory.
	ErrCollectionRootInvalid = errors.New("knowledge: invalid collection root")

	// ErrEmptyTarget means the link carried no target at all ("[[]]").
	ErrEmptyTarget = errors.New("knowledge: empty link target")

	// ErrAbsoluteTarget means the link named an absolute filesystem path.
	// Refused outright rather than reinterpreted as root-relative, so that
	// "[[/etc/passwd]]" can never become a read of anything (US-10 AS-2).
	ErrAbsoluteTarget = errors.New("knowledge: absolute link target refused")

	// NOTE: the sentinel for "this path leaves the collection" is
	// ErrOutsideCollection, declared once for the whole package in detect.go.
	// It is used unchanged here — a second sentinel with the same meaning
	// would let a caller's errors.Is check pass for one escape and fail for
	// another, which is the worst possible shape for a containment error.
)

// LinkFS is the narrow filesystem surface the link graph uses. It exists so a
// test can supply a recording implementation and prove, by count, that no
// content read of an out-of-root path ever happened — the assertion AC-6.2
// requires and the one a "no error was returned" test cannot make.
//
// Every name passed to a LinkFS is an absolute, cleaned OS path.
//
// Open is the ONLY method that reads a file's contents. Lstat, ReadDir and
// EvalSymlinks inspect metadata; a containment refusal must be reachable
// using those three alone.
type LinkFS interface {
	// Lstat reports metadata for name WITHOUT following a terminal symlink.
	Lstat(name string) (fs.FileInfo, error)
	// ReadDir lists a directory. Entries must report symlinks as symlinks
	// (fs.ModeSymlink), i.e. it must not follow them, matching os.ReadDir.
	ReadDir(name string) ([]fs.DirEntry, error)
	// EvalSymlinks returns the real path of name with every symbolic link
	// resolved, matching filepath.EvalSymlinks.
	EvalSymlinks(name string) (string, error)
	// Open opens a file for reading. This is a CONTENT read.
	Open(name string) (fs.File, error)
}

// osLinkFS is the real filesystem.
type osLinkFS struct{}

func (osLinkFS) Lstat(name string) (fs.FileInfo, error)     { return os.Lstat(name) }
func (osLinkFS) ReadDir(name string) ([]fs.DirEntry, error) { return os.ReadDir(name) }
func (osLinkFS) EvalSymlinks(name string) (string, error)   { return filepath.EvalSymlinks(name) }
func (osLinkFS) Open(name string) (fs.File, error)          { return os.Open(name) }

// OSLinkFS returns a LinkFS backed by the real filesystem.
func OSLinkFS() LinkFS { return osLinkFS{} }

// CollectionRoot is a validated knowledge-base root: absolute, existing, a
// directory, and with every symbolic link on the way to it already resolved.
//
// The stored path is the REAL path. That matters more than it looks: on macOS
// a temporary directory arrives as /var/folders/... while /var is itself a
// symlink to /private/var, so a containment comparison made against the
// unresolved spelling rejects every file in the collection. Resolving once, at
// construction, is what makes every later comparison a plain string test.
type CollectionRoot struct {
	real string
}

// NewCollectionRoot validates path and returns the collection root.
//
// path MUST be absolute. A relative path is refused rather than joined against
// the process working directory, because "notes" silently meaning "whatever
// happens to be beside the gateway binary" is never what a caller meant.
func NewCollectionRoot(fsys LinkFS, path string) (CollectionRoot, error) {
	if strings.TrimSpace(path) == "" {
		return CollectionRoot{}, fmt.Errorf("%w: empty path", ErrCollectionRootInvalid)
	}
	if !filepath.IsAbs(path) {
		return CollectionRoot{}, fmt.Errorf("%w: %q is not absolute", ErrCollectionRootInvalid, path)
	}
	resolved, err := fsys.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return CollectionRoot{}, fmt.Errorf("%w: %q: %v", ErrCollectionRootInvalid, path, err)
	}
	resolved = filepath.Clean(resolved)
	fi, err := fsys.Lstat(resolved)
	if err != nil {
		return CollectionRoot{}, fmt.Errorf("%w: %q: %v", ErrCollectionRootInvalid, path, err)
	}
	if !fi.IsDir() {
		return CollectionRoot{}, fmt.Errorf("%w: %q is not a directory", ErrCollectionRootInvalid, path)
	}
	return CollectionRoot{real: resolved}, nil
}

// Path returns the collection root's real absolute path.
func (r CollectionRoot) Path() string { return r.real }

// Valid reports whether this root was produced by NewCollectionRoot. A zero
// CollectionRoot contains nothing at all — it must never behave like "/".
func (r CollectionRoot) Valid() bool { return r.real != "" }

// Contains reports whether an ALREADY-REAL absolute path is the root itself or
// lives underneath it.
//
// The caller is responsible for having resolved symlinks first; this is a
// string test and cannot tell a real path from a lexical one. Use
// ResolveContained when the path came from a file.
func (r CollectionRoot) Contains(realPath string) bool {
	if !r.Valid() {
		return false
	}
	return isWithinOrEqual(r.real, filepath.Clean(realPath))
}

// Rel converts a contained real path into the collection-relative,
// slash-separated form used everywhere in the graph.
func (r CollectionRoot) Rel(realPath string) (string, error) {
	clean := filepath.Clean(realPath)
	if !r.Contains(clean) {
		return "", fmt.Errorf("%w: %q", ErrOutsideCollection, realPath)
	}
	if clean == r.real {
		return ".", nil
	}
	return filepath.ToSlash(clean[len(r.real)+1:]), nil
}

// ResolveContained turns a collection-relative candidate into a real absolute
// path, refusing anything that does not resolve inside the root.
//
// Order of refusal, and why each stage exists:
//
//  1. Empty — "[[]]" is a link to nothing (DS-1 case 9); it must not become a
//     link to the collection root.
//  2. Absolute — "/etc/passwd", "C:\Windows\..." and UNC "\\host\share" are
//     refused outright. Obsidian would read a leading slash as vault-relative;
//     doing the same here would silently turn an escape attempt into a
//     lookup, and the spec requires the escape to be REPORTED (US-10 AS-2).
//  3. Lexical containment — "../../.." is caught before any syscall, so the
//     ordinary traversal case costs nothing and touches nothing.
//  4. Real containment — the path is resolved through every symlink and
//     re-checked. This is the stage a lexical-only implementation omits, and
//     the one a symlinked directory inside the collection defeats.
//
// The target's contents are never opened. Non-existent paths are allowed
// through stage 4 by resolving their deepest existing ancestor: the caller
// decides what a missing file means, and for link resolution it means
// unresolved.
func (r CollectionRoot) ResolveContained(fsys LinkFS, relPath string) (string, error) {
	if !r.Valid() {
		return "", fmt.Errorf("%w: root not initialised", ErrCollectionRootInvalid)
	}
	candidate := strings.TrimSpace(relPath)
	if candidate == "" {
		return "", fmt.Errorf("%w: %q", ErrEmptyTarget, relPath)
	}
	if IsAbsoluteTarget(candidate) {
		return "", fmt.Errorf("%w: %q", ErrAbsoluteTarget, relPath)
	}
	joined := filepath.Join(r.real, filepath.FromSlash(candidate))
	if !isWithinOrEqual(r.real, joined) {
		return "", fmt.Errorf("%w: %q", ErrOutsideCollection, relPath)
	}
	resolved, err := realPathAllowingMissing(fsys, joined)
	if err != nil {
		return "", err
	}
	if !isWithinOrEqual(r.real, resolved) {
		return "", fmt.Errorf("%w: %q resolves to %q", ErrOutsideCollection, relPath, resolved)
	}
	return resolved, nil
}

// IsAbsoluteTarget reports whether a link target names an absolute filesystem
// location on ANY platform, independent of the platform this binary runs on.
// A POSIX build must still refuse "C:\Users\x\.ssh\id_rsa", because the note
// containing it was authored somewhere and the refusal is what gets reported.
//
// The drive-letter test deliberately requires a separator (or end of string)
// after the colon, so a note legitimately named "Meeting: 2026-01-01.md"
// (DS-3 case 2, and measured in the reference collection) is NOT mistaken for
// a drive reference.
func IsAbsoluteTarget(target string) bool {
	if target == "" {
		return false
	}
	if target[0] == '/' || target[0] == '\\' {
		return true
	}
	if len(target) >= 2 && isASCIILetter(target[0]) && target[1] == ':' {
		if len(target) == 2 || target[2] == '/' || target[2] == '\\' {
			return true
		}
	}
	return filepath.IsAbs(target)
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isWithinOrEqual(root, candidate) is declared in detect.go and shared by the
// whole package. Its trailing-separator guard is what stops "/vault-backup"
// matching a root of "/vault"; a prefix test without it is a containment bug
// that looks correct in every hand-written example.

// realPathAllowingMissing resolves every symlink in p. When p does not exist,
// it resolves the deepest ancestor that does and re-appends the remainder, so
// containment can still be decided for a path that is about to be created or
// that a link merely names.
func realPathAllowingMissing(fsys LinkFS, p string) (string, error) {
	if resolved, err := fsys.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved), nil
	}
	remainder := ""
	dir := p
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding anything that
			// exists. Fall back to the lexical form; the caller's
			// containment check still applies to it.
			return filepath.Clean(p), nil
		}
		base := filepath.Base(dir)
		if remainder == "" {
			remainder = base
		} else {
			remainder = filepath.Join(base, remainder)
		}
		dir = parent
		if resolved, err := fsys.EvalSymlinks(dir); err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
	}
}

// SkipReason names why an entry was excluded from the walk. Every exclusion is
// reportable (NB-9): a file the indexer cannot address must never simply
// vanish from the collection.
type SkipReason string

const (
	// SkipSymlink — a symbolic link, skipped without being followed (FR-044).
	SkipSymlink SkipReason = "symlink"
	// SkipOutsideRoot — the entry did not resolve inside the root (FR-043).
	SkipOutsideRoot SkipReason = "outside_root"
	// SkipUnreadable — the entry could not be listed or stat'ed.
	SkipUnreadable SkipReason = "unreadable"
	// SkipIrregular — a socket, device or named pipe: not a note, not a
	// directory, and not something to open.
	SkipIrregular SkipReason = "irregular"
)

// SkippedEntry is one reported exclusion from the walk.
type SkippedEntry struct {
	// RelPath is the collection-relative, slash-separated path of the entry.
	RelPath string
	// Reason is why it was excluded.
	Reason SkipReason
	// Detail carries the underlying cause when there is one.
	Detail string
}

// WalkResult is the outcome of a contained walk.
type WalkResult struct {
	// Files holds every ordinary file found, collection-relative and
	// slash-separated, sorted lexicographically.
	Files []string
	// Dirs holds every directory descended into, in the same form, sorted.
	// The root itself is present as ".".
	Dirs []string
	// Skipped holds every reported exclusion, sorted by path then reason.
	Skipped []SkippedEntry
}

// WalkContained walks the collection and returns every contained ordinary
// file, never following a symbolic link.
//
// It descends into exactly what Scan descends into: the tool-state directories
// named by scanSkippedDirNames (`.obsidian`, `.omnipus-vault`, `.git`,
// `.trash`) are never entered, at any depth. That is a correctness requirement,
// not a tidiness one — see the comment at the skip itself.
//
// Termination is structural rather than defensive. Because symlinks are never
// traversed (FR-044), what is walked is a real directory tree, and a real
// directory tree has no cycles — so a symlink loop inside the collection
// cannot make this function revisit anything. There is deliberately no
// visited-set "loop breaker": a loop breaker would make the walk terminate
// while still having FOLLOWED the link once, which is precisely what NB-7
// forbids.
func WalkContained(fsys LinkFS, root CollectionRoot) (WalkResult, error) {
	var out WalkResult
	if !root.Valid() {
		return out, fmt.Errorf("%w: root not initialised", ErrCollectionRootInvalid)
	}

	type queued struct {
		real string
		rel  string
	}
	stack := []queued{{real: root.Path(), rel: "."}}

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out.Dirs = append(out.Dirs, cur.rel)

		entries, err := fsys.ReadDir(cur.real)
		if err != nil {
			if cur.rel == "." {
				return out, fmt.Errorf("knowledge: read collection root %q: %w", cur.real, err)
			}
			out.Skipped = append(out.Skipped, SkippedEntry{
				RelPath: cur.rel,
				Reason:  SkipUnreadable,
				Detail:  err.Error(),
			})
			continue
		}

		for _, e := range entries {
			name := e.Name()
			childReal := filepath.Join(cur.real, name)
			childRel := name
			if cur.rel != "." {
				childRel = cur.rel + "/" + name
			}

			// FR-043: every walked path is checked, including one produced
			// by a directory entry whose name is something unexpected.
			if !root.Contains(childReal) {
				out.Skipped = append(out.Skipped, SkippedEntry{
					RelPath: childRel,
					Reason:  SkipOutsideRoot,
					Detail:  childReal,
				})
				continue
			}

			mode := e.Type()
			switch {
			case mode&fs.ModeSymlink != 0:
				// FR-044. Reported, never followed, and never lstat'ed
				// through: whether it points inside or outside the
				// collection makes no difference to the decision.
				out.Skipped = append(out.Skipped, SkippedEntry{
					RelPath: childRel,
					Reason:  SkipSymlink,
				})
			case mode.IsDir():
				// FR-046: this walker and Scan's must agree about what is IN
				// a collection, because BuildLinkGraph uses this one and the
				// index uses that one, and two walkers with two answers means
				// knowledge_graph and knowledge_search describe different
				// collections. The skip set is scan.go's, shared verbatim —
				// see scanSkippedDirNames for why each name is on it. The
				// consequence of omitting it here was not cosmetic: notes in
				// `.trash` were opened and resurfaced as live backlinks on
				// real notes, and every `.obsidian/plugins/**` file became an
				// addressable wikilink target.
				//
				// Not reported in Skipped, and deliberately: Skipped means
				// "content this walk could not address" (NB-9). Tool state is
				// not content, and Scan does not report it either.
				if _, skip := scanSkippedDirNames[name]; skip {
					continue
				}
				stack = append(stack, queued{real: childReal, rel: childRel})
			case mode.IsRegular():
				out.Files = append(out.Files, childRel)
			default:
				out.Skipped = append(out.Skipped, SkippedEntry{
					RelPath: childRel,
					Reason:  SkipIrregular,
					Detail:  mode.String(),
				})
			}
		}
	}

	sort.Strings(out.Files)
	sort.Strings(out.Dirs)
	sort.Slice(out.Skipped, func(i, j int) bool {
		if out.Skipped[i].RelPath != out.Skipped[j].RelPath {
			return out.Skipped[i].RelPath < out.Skipped[j].RelPath
		}
		return out.Skipped[i].Reason < out.Skipped[j].Reason
	})
	return out, nil
}
