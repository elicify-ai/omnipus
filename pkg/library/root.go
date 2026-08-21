// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package library implements the Library file-explorer surface
// (docs/internal/specs/library-spec.md D-2): a workspace-relative path
// explorer rooted at workspaces/<id>/work/ — the SAME directory every agent
// file/exec tool is confined to (ADR-046) — not the UUID-keyed workspace
// media library (pkg/media/library).
//
// Path safety is this package's entire reason to exist (library-spec.md
// Constraints: "every Library path operation resolves inside the target
// workspace's work tree; no '..' escape, symlinks not followed out of the
// root"). Every operation goes through a Go 1.24+ os.Root opened at the
// workspace's work/ directory (pkg/tools/resolvepath.go documents the same
// discipline for agent-tool path resolution; this package follows it
// independently rather than importing pkg/tools, which is a heavier,
// turn/ctx-oriented package this leaf does not need). os.Root refuses any
// path whose resolution — including through a symlink — would leave the
// root, at the syscall/path-walk level, not merely via a prior lexical
// string check; that closes the CWE-357 TOCTOU class of bug a
// resolve-then-return-a-bare-path design would leave open.
//
// A structural violation the caller could have caught before ever touching
// the filesystem (an absolute path, a literal ".." segment, an embedded NUL
// or backslash) is rejected by CleanRelPath with ErrInvalidPath — a 400 at
// the REST layer. A path that is lexically fine but resolves outside the
// root ONLY because of a symlink is rejected by os.Root itself at I/O time
// and surfaces here as ErrOutsideRoot — a 403, per library-spec.md's own
// per-operation status table ("403 if path resolves outside the workspace's
// work tree (traversal or an out-of-root symlink)").
package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Sentinel errors. REST handlers (pkg/gateway/rest_library.go) map these to
// HTTP status codes via errors.Is — never by string-matching Error().
var (
	// ErrInvalidPath marks a structural path violation caught before any
	// filesystem access: absolute, contains a literal ".." segment, empty
	// where a concrete path is required, an embedded NUL byte, or a
	// backslash (Library paths are always forward-slash per contract).
	ErrInvalidPath = errors.New("library: invalid path")
	// ErrOutsideRoot marks a path that is lexically valid but whose
	// resolution — via a symlink — would leave the workspace's work tree.
	// Only os.Root itself can detect this (it re-walks and re-checks at
	// I/O time), so this is only ever produced by translateErr wrapping a
	// real os.Root error, never by CleanRelPath.
	ErrOutsideRoot = errors.New("library: path escapes the workspace work tree")
	// ErrNotFound marks a path with nothing on disk at the resolved
	// location (including "the workspace has no work tree yet and a
	// specific subdirectory was requested").
	ErrNotFound = errors.New("library: not found")
	// ErrNotDir marks an operation that requires a directory target
	// (listing, or an operation's destination/parent-directory check)
	// finding a regular file instead.
	ErrNotDir = errors.New("library: not a directory")
	// ErrIsDir marks a file-scoped operation (read content, download,
	// write content) finding a directory instead of a regular file.
	ErrIsDir = errors.New("library: is a directory")
	// ErrAlreadyExists marks a rename/move/copy destination that is
	// already occupied.
	ErrAlreadyExists = errors.New("library: destination already exists")
	// ErrIsMountRoot marks an operation aimed at a mounted folder's own entry
	// rather than at something inside it.
	//
	// This is a DATA-LOSS guard, not a tidiness rule. A mount resolves to its
	// own os.Root, so "delete work/myrepo" would reach that root as "." and
	// recursively remove the contents of the operator's real folder — their
	// actual repository, not a workspace file. Revoking a mount is a different
	// operation with a different verb (unmount), and it deletes nothing.
	//
	// Rename is refused for the milder reason that a mount's name lives in the
	// mount record; renaming the symlink alone would desynchronise the two.
	ErrIsMountRoot = errors.New("library: path is a mounted folder's own entry")
	// ErrCrossRootTransfer marks a move/rename whose source and destination
	// live in different roots (work tree ↔ a mount, or two different mounts).
	//
	// os.Root cannot rename across roots — that is not a limitation to work
	// around but the containment doing its job, since a cross-root rename is by
	// definition an operation that leaves the root it started in. Performing it
	// safely means copy-then-delete with its own partial-failure semantics,
	// which is deliberately NOT smuggled in behind a verb the caller believes
	// is atomic.
	ErrCrossRootTransfer = errors.New("library: cannot move directly between the work tree and a mounted folder")
)

// DestinationParentNotFoundError marks a rename/move/copy/write destination
// whose parent directory does not exist yet (UAT Issue 4: a bare 404 gave
// the caller no way to tell "your source doesn't exist" from "your
// destination's parent folder doesn't exist", and no path to success either
// way since this package had no directory-creation primitive). Wraps
// ErrNotFound — so `errors.Is(err, ErrNotFound)` and the existing 404
// mapping keep working unchanged — but is also independently identifiable
// via errors.As so the REST layer can name the specific missing directory
// and point the caller at POST /library/{workspace_id}/mkdir instead of
// returning a bare "not found". Move/Copy/Rename deliberately do NOT
// auto-create missing parents (see Root.requireParentDir's doc for why);
// Mkdir is the caller's explicit, deliberate way to create the folder first.
type DestinationParentNotFoundError struct {
	// Parent is the missing parent directory's workspace-relative path.
	// Never "" — the work-tree root always exists (OpenRoot creates it),
	// so a missing parent is always a real, nameable subdirectory.
	Parent string
}

func (e *DestinationParentNotFoundError) Error() string {
	return fmt.Sprintf("library: destination parent directory %q does not exist", e.Parent)
}

// Unwrap makes errors.Is(err, ErrNotFound) true for an
// *DestinationParentNotFoundError, so any existing caller checking only for
// the generic sentinel keeps working unchanged.
func (e *DestinationParentNotFoundError) Unwrap() error { return ErrNotFound }

// Root is a path-safe handle onto one workspace's work/ directory, backed by
// an os.Root opened at that directory. Every method takes an
// ALREADY-CLEANED relative path (see CleanRelPath) — Root does not
// re-validate lexical structure, only enforces containment via os.Root
// itself.
type Root struct {
	dir  string // absolute workspace work/ directory (workspace.SafeWorkDir)
	root *os.Root

	// mounts maps a mount's NAME to its own independently-opened os.Root.
	//
	// ADR-063 D4 made a mount a real local folder reachable inside a workspace,
	// materialised as a symlink at work/<name>. os.Root refuses to follow a
	// symlink out of its root — correctly, that is what stops one workspace
	// reading another's files — so a single root can NEVER open a mount. Before
	// this map existed the Library listed mounted folders and then failed on
	// click with "path escapes the workspace work tree": visible and unopenable,
	// which reads as a broken product rather than as a boundary.
	//
	// One root per mount rather than one relaxed root: browsing inside a mount
	// is then contained to THAT folder exactly as work/ is contained to itself.
	// Nothing is loosened — you still cannot escape a mount, reach a sibling
	// workspace, or walk up to the rest of the disk — the containment simply
	// learns that a workspace legitimately has more than one root, which is what
	// the enforcement engine already models (fspolicy.FSPolicy.AllowedRoots).
	mounts map[string]*mountRoot
}

// mountRoot is one mounted folder's own containment plus the facts the API
// layer needs to describe it (the real path it points at, and whether the grant
// was flagged broad when it was created).
type mountRoot struct {
	root   *os.Root
	name   string
	target string // realpath-resolved absolute path on the host
	broad  bool
}

// OpenRoot opens workspaceID's work/ directory as a path-safe Root,
// creating the directory (mkdir 0700, not its contents) if it does not yet
// exist. Callers MUST have already confirmed the workspace itself exists
// (workspace.Exists) before calling this — OpenRoot only concerns itself
// with the work/ subdirectory, and will happily create an empty work/ tree
// for a workspace that is otherwise real but has never had an agent turn or
// upload touch its files yet (a fresh workspace has no work/ until
// something writes to it). This mkdir-on-open is deliberate: it makes
// "browse a brand-new workspace's Library" a plain 200-with-empty-list
// rather than a 404, without needing a separate no-mkdir read path — see
// CountVisibleRootEntries for the ONE place that intentionally does NOT
// want this side effect (the cheap virtual-root listing across every
// workspace, which must not mkdir dozens of work/ trees just from being
// displayed).
func OpenRoot(home, workspaceID string) (*Root, error) {
	dir, err := workspace.SafeWorkDir(home, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return nil, fmt.Errorf("library: create work directory: %w", mkErr)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("library: open work directory root: %w", err)
	}
	return &Root{dir: dir, root: r, mounts: openMountRoots(home, workspaceID)}, nil
}

// openMountRoots opens one os.Root per mount declared on this workspace.
//
// A mount whose target cannot be opened is SKIPPED rather than failing the
// whole Root: spec FR-8.2 requires that a missing target never blocks the
// workspace, because the operator's folder is theirs to move, rename, or put on
// a volume that is not currently attached. The mount stays listed (the caller
// still sees it, and can revoke it) — it simply cannot be entered until the
// target comes back. Failing here instead would make one detached external disk
// take the entire Library offline.
func openMountRoots(home, workspaceID string) map[string]*mountRoot {
	mounts, ok := workspace.LoadMounts(home, workspaceID)
	if !ok || len(mounts) == 0 {
		return nil
	}
	out := make(map[string]*mountRoot, len(mounts))
	for _, m := range mounts {
		if m.Name == "" || m.HostPath == "" {
			continue
		}
		mr, err := os.OpenRoot(m.HostPath)
		if err != nil {
			// Target missing, renamed, or on a detached volume. RECORD it with a
			// nil root rather than skipping it.
			//
			// Skipping made the doc comment above false: with no entry in the
			// map, mountFor and annotateMount saw an ordinary directory, so the
			// row lost its badge, its host path and its Unmount action, the
			// header count and the mounts dialog omitted it (both filter on
			// entry.mount), and isMountRootEntry stopped refusing Delete and
			// Rename on it. A BROKEN mount — the one an operator most wants to
			// revoke — became the one they could not revoke, and could delete
			// through by accident.
			out[m.Name] = &mountRoot{
				root:   nil,
				name:   m.Name,
				target: m.HostPath,
				broad:  workspace.IsBroadMountTarget(m.HostPath),
			}
			continue
		}
		out[m.Name] = &mountRoot{
			root:   mr,
			name:   m.Name,
			target: m.HostPath,
			broad:  workspace.IsBroadMountTarget(m.HostPath),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolve maps a workspace-relative path to the os.Root that actually contains
// it, and to that path expressed relative to THAT root.
//
// This is the single seam through which every filesystem operation in this
// package passes. It exists so the multi-root rule is stated once: a path whose
// FIRST segment names a mount belongs to that mount's root, everything else
// belongs to the work root. Spreading the same test across the ~26 call sites
// that touch a root would be 26 chances to forget it, and the one that forgot
// would silently fall back to the work root — where the mount is a symlink, so
// the operation fails with the containment error this change exists to remove.
//
// rel MUST already have passed CleanRelPath: resolve does no lexical validation
// and relies on os.Root for containment, exactly as the single-root code did.
func (r *Root) resolve(rel string) (*os.Root, string) {
	if r.mounts == nil || rel == "" || rel == "." {
		return r.root, rel
	}
	name, rest, _ := strings.Cut(rel, "/")
	m, ok := r.mounts[name]
	if !ok || m.root == nil {
		// A mount whose target could not be opened has no root to resolve into.
		// Falling back to the work root is correct: there the entry is the
		// dangling symlink, and os.Root refuses to follow it — so the caller
		// gets the containment error that describes reality ("this does not
		// resolve") instead of a nil-pointer panic.
		return r.root, rel
	}
	if rest == "" {
		// The mount's own directory entry — the root itself, from its side.
		return m.root, "."
	}
	return m.root, rest
}

// mountFor reports the mount a workspace-relative path belongs to, if any.
// Used by the API layer to mark entries and to name the real destination
// before a write lands on the operator's actual disk.
func (r *Root) mountFor(rel string) *mountRoot {
	if r.mounts == nil || rel == "" || rel == "." {
		return nil
	}
	name, _, _ := strings.Cut(rel, "/")
	return r.mounts[name]
}

// MountAt returns the mount rooted at the first segment of rel, reporting its
// name, real host path, and whether it was flagged a broad grant. ok is false
// when rel is not inside a mount.
func (r *Root) MountAt(rel string) (name, target string, broad, ok bool) {
	m := r.mountFor(rel)
	if m == nil {
		return "", "", false, false
	}
	return m.name, m.target, m.broad, true
}

// HostPath returns the real absolute path a workspace-relative path resolves
// to, following a mount when one applies. It is a STRING computation for
// display and audit — it grants nothing and opens nothing.
func (r *Root) HostPath(rel string) string {
	if m := r.mountFor(rel); m != nil {
		_, rest, _ := strings.Cut(rel, "/")
		if rest == "" {
			return m.target
		}
		return filepath.Join(m.target, rest)
	}
	if rel == "" || rel == "." {
		return r.dir
	}
	return filepath.Join(r.dir, rel)
}

// Close releases the underlying os.Root. Safe to call on a nil *Root.
func (r *Root) Close() error {
	if r == nil || r.root == nil {
		return nil
	}
	// Every mount root is a separate open file descriptor; closing only the
	// work root would leak one per mount per request.
	for _, m := range r.mounts {
		if m != nil && m.root != nil {
			_ = m.root.Close()
		}
	}
	return r.root.Close()
}

// CleanRelPath validates and cleans a caller-supplied, workspace-relative
// Library path (library-spec.md Constraints: "Never absolute and never
// containing a '..' segment"). Returns ("", nil) for the work-tree root
// itself (raw == "", ".", or a path that cleans down to "."). Any other
// structural violation returns ErrInvalidPath. This is a PURE, filesystem-
// free lexical check — it does not know whether the resulting path actually
// exists, and it does not (cannot) detect a symlink escape; that is
// os.Root's job at actual I/O time, surfaced as ErrOutsideRoot by
// translateErr.
//
// fs.ValidPath is the load-bearing check here: it rejects any path element
// that is "." or ".." (including a non-leading one, e.g. "a/../b" splits to
// elements ["a","..","b"]) or empty, and rejects a leading/trailing slash —
// exactly the "unrooted, slash-separated, no dot-segments" shape the
// Library contract requires, with no need to hand-roll segment-walking.
func CleanRelPath(raw string) (string, error) {
	if raw == "" || raw == "." {
		return "", nil
	}
	if strings.IndexByte(raw, 0) != -1 {
		return "", ErrInvalidPath
	}
	if strings.ContainsRune(raw, '\\') {
		return "", ErrInvalidPath
	}
	if strings.HasPrefix(raw, "/") {
		return "", ErrInvalidPath
	}
	cleaned := path.Clean(raw)
	if cleaned == "." {
		return "", nil
	}
	if !fs.ValidPath(cleaned) {
		return "", ErrInvalidPath
	}
	// Reject any path element that STARTS WITH ".." but isn't exactly ".."
	// (fs.ValidPath already rejected the exact-match case above) — e.g.
	// "..%2fdana-pwned-encoded.txt" or "folder/..sneaky". UAT Issue 6: such
	// a name is lexically safe (no real traversal — os.Root still confines
	// it) but this package's "hidden" heuristic (entryFromParts:
	// strings.HasPrefix(name, ".")) also matches it, so it silently
	// vanishes from the default (non-hidden) listing the instant it's
	// created — confusing, not a security hole, but avoidable. Rather than
	// carve out a narrower "hidden" definition (D-8's contract is "name
	// begins with a dot", defined once for client and server; a
	// double-dot-prefixed name IS a dotfile by that same convention) this
	// rejects the name outright at creation time, as a sanity check —
	// simpler than teaching every caller a special case, and it matches how
	// most real file managers refuse to create/rename to a name starting
	// with "..". Applies to every path operation (read and write) so a
	// name like this can never enter or be addressed through the Library.
	for _, seg := range strings.Split(cleaned, "/") {
		if strings.HasPrefix(seg, "..") {
			return "", ErrInvalidPath
		}
		// Cross-platform filename safety (pkg/pathsafe), applied to EVERY
		// segment — not just the leaf name — so a reserved Windows device
		// name, an NTFS-illegal character, or a trailing dot/space can
		// never enter the Library via an intermediate directory either
		// (e.g. mkdir-ing "CON/report.txt" in one call). See pathsafe's
		// package doc for why these rules apply unconditionally rather
		// than only when actually running on Windows — a workspace must
		// behave identically whichever OS opens it.
		if err := pathsafe.ValidateComponent(seg); err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
		}
	}
	if err := pathsafe.ValidateRelPathLength(cleaned); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	return cleaned, nil
}

// CountVisibleRootEntries returns the number of direct, non-hidden entries
// at the root of workspaceID's work tree, WITHOUT creating the work/
// directory if it is absent (0 in that case, matching
// LibraryWorkspaceNode.entry_count's documented "0 when the work tree does
// not exist yet" contract). Used only by the cheap Library virtual-root
// listing (GET /library/workspaces) across every workspace at once, which
// must not have the side effect of mkdir'ing every workspace's work/ tree
// just from being displayed in the sidebar — unlike OpenRoot, which is
// deliberately eager for the single-workspace, user-is-actually-browsing-it
// case.
func CountVisibleRootEntries(home, workspaceID string) (int, error) {
	dir, err := workspace.SafeWorkDir(home, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("library: count root entries: %w", err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		count++
	}
	return count, nil
}

// translateErr normalizes an error returned by an os.Root method into one of
// this package's sentinels. os.Root reports a path whose resolution
// (including through a symlink) would leave the root with a wrapped
// errors.New("path escapes from parent") (Go's os/file.go) — checked here
// by substring, the same convention pkg/tools/resolvepath.go's wrapFSErr
// already uses for the identical message, since os.Root does not export a
// dedicated sentinel for it.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	if strings.Contains(err.Error(), "escapes from parent") {
		return ErrOutsideRoot
	}
	// os.Root.MkdirAll (and any other syscall that walks an intermediate
	// path component) reports a component that exists but is a regular
	// file — not a directory — as a *PathError wrapping syscall.ENOTDIR,
	// whose message is "not a directory" on every platform Omnipus targets.
	// No portable sentinel is exported for this (same reasoning translateErr
	// already applies to "escapes from parent" above), so match by
	// substring. Surfaces as ErrNotDir (400) — e.g. Root.Mkdir("a/b/c")
	// when "a" already exists as a file.
	if strings.Contains(err.Error(), "not a directory") {
		return ErrNotDir
	}
	return err
}

// StatDir stats rel (already-cleaned; "" means the work-tree root) and
// requires it to be a directory. Returns ErrNotFound if nothing exists
// there, ErrNotDir if it exists but is a regular file.
func (r *Root) StatDir(rel string) (os.FileInfo, error) {
	name := rel
	if name == "" {
		name = "."
	}
	rt, sub := r.resolve(name)
	fi, err := rt.Stat(sub)
	if err != nil {
		return nil, translateErr(err)
	}
	if !fi.IsDir() {
		return nil, ErrNotDir
	}
	return fi, nil
}

// StatFile stats rel and requires it to be a regular file. Returns
// ErrNotFound if nothing exists there, ErrIsDir if it exists but is a
// directory.
func (r *Root) StatFile(rel string) (os.FileInfo, error) {
	rt, sub := r.resolve(rel)
	fi, err := rt.Stat(sub)
	if err != nil {
		return nil, translateErr(err)
	}
	if fi.IsDir() {
		return nil, ErrIsDir
	}
	return fi, nil
}

// isMountRootEntry reports whether rel names a mounted folder's OWN entry
// (rather than something inside it). Used to refuse operations that would act
// on the operator's real folder itself — see ErrIsMountRoot.
func (r *Root) isMountRootEntry(rel string) bool {
	if r.mounts == nil || rel == "" || rel == "." {
		return false
	}
	name, rest, _ := strings.Cut(rel, "/")
	if rest != "" {
		return false
	}
	_, ok := r.mounts[name]
	return ok
}

// sameRoot reports whether two workspace-relative paths resolve to the same
// os.Root, i.e. whether an atomic rename between them is even expressible.
func (r *Root) sameRoot(aRel, bRel string) bool {
	ra, _ := r.resolve(aRel)
	rb, _ := r.resolve(bRel)
	return ra == rb
}
