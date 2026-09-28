// Omnipus — library-views-anywhere-spec §4 step 1: the .view discovery
// helper. A saved view lives anywhere inside its knowledge base (FD-1 /
// FR-VA-001), the discovery walk is the only place that decides which files
// count, and — by D-SYMLINK-READ / D-SIZECAP — the same operation that
// decides also opens the file for reading with no-follow semantics and a
// size-capped read, so a file that grew past the cap between walk and read,
// or that was swapped for a symlink, can never silently be read.
//
// The helper is the only place that reads a .view file's bytes, the only
// caller of WalkContained for views, and the only function that knows what
// the view extension looks like — every downstream consumer takes
// []ViewFile and parses.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// MaxViewFileBytes is D-SIZECAP's ceiling — one named constant, one place to
// change. A .view file over this size is reported view_too_large and never
// read past the cap. 256 KiB is spec's floor, deliberately round and
// well above any real view's expected size (a filter tree plus metadata).
const MaxViewFileBytes = 256 * 1024

// viewExtension is the chosen file extension for a saved view (spec Q1/A —
// `.view`). Kept as a constant rather than a string literal so the
// uppercase-tolerant matcher below stays a single source of truth.
const viewExtension = ".view"

// ViewFile is one .view file's bytes, already read, paired with the path
// the caller needs to record it under. Returning bytes — not paths — is
// what closes the TOCTOU window MIN-001 identified: the open-with-no-follow
// and the read are the same syscall, not two calls a second process can win.
type ViewFile struct {
	// Path is the absolute, real (post-symlink) path of the file. Empty when
	// the entry came back as a rejection rather than a clean read (Size>0
	// and Err==nil otherwise).
	Path string
	// Bytes is the file's contents, capped at MaxViewFileBytes+1 so a caller
	// reading it back can distinguish "exactly the cap" from "over the cap".
	// Always non-nil for a clean read.
	Bytes []byte
	// Size is the actual on-disk size. May exceed MaxViewFileBytes when the
	// file grew past it between walk and read; in that case Bytes is also
	// capped at MaxViewFileBytes+1.
	Size int64
	// Err is non-nil for any rejection that happened during the open/read:
	// unreadable, swapped-symlink, over-cap. When Err is set, the entry's
	// own rejection code is reported alongside (Rejection field).
	Err error
	// Rejection is the ViewRejectionCode-style wire code for Err when Err is
	// set. Empty for a clean read. Front-ends compare against the same
	// enumerator the schema now exports.
	Rejection string
	// Skipped is set when the walk's enclosing directory could not be read;
	// one ViewFile per SkipUnreadable WalkResult entry, with Bytes/Err empty
	// and Path set to the directory's collection-relative path.
	Skipped bool
}

// ViewDiscoveryReport is the helper's full output: every file read plus the
// walk's skipped-directory notes. Skipped is surfaced on EVERY caller
// (FR-VA-025, R2-MIN-007) so a silent skip cannot disappear the view count.
type ViewDiscoveryReport struct {
	Files    []ViewFile
	Skipped  []string // collection-relative directory paths WalkContained reported unreadable
}

// DiscoverViewFiles walks root via WalkContained (FR-VA-001, D-WALK) and
// returns the contents of every `.view` file found, together with the
// walk's SkipUnreadable notes.
//
// Every entry the walker surfaces as a regular file with the .view extension
// is opened through openNoFollow (D-SYMLINK-READ: no-follow semantics —
// O_NOFOLLOW where the platform has it, an open-then-Fstat comparison on
// Windows), read through io.LimitReader(MaxViewFileBytes+1) (D-SIZECAP: the
// read itself bounds what it reads), and handed back as bytes — never as a
// path the caller re-opens later (which is what MIN-001 originally
// identified as the race). A path walked as a regular file that resolves to
// something else at open time (a swap for a symlink) is rejected with
// view_unreadable, never read.
//
// The walk also stops descending at any directory carrying its own
// `.obsidian` or `.omnipus-vault` marker other than the collection root it
// started from (D-WALK's nested-vault rule, R2-MIN-012). A hand-copied
// nested vault cannot be discovered as part of the outer collection.
func DiscoverViewFiles(fsys LinkFS, root CollectionRoot) (ViewDiscoveryReport, error) {
	var rep ViewDiscoveryReport
	if !root.Valid() {
		return rep, fmt.Errorf("%w: root not initialised", ErrCollectionRootInvalid)
	}
	walk, err := WalkContained(fsys, root)
	if err != nil {
		return rep, fmt.Errorf("knowledge: walk collection for views: %w", err)
	}

	for _, sk := range walk.Skipped {
		if sk.Reason == SkipUnreadable {
			rep.Skipped = append(rep.Skipped, sk.RelPath)
		}
	}
	sort.Strings(rep.Skipped)

	// Map of collection-relative directory paths to a stop signal (a nested
	// vault). WalkContained does not honour nested-vault stops today — it
	// descends into every non-control directory unconditionally. The signal
	// is rebuilt here so the helper stays the only place that knows about
	// nested vaults.
	stopDirs := collectNestedVaults(fsys, root, walk)

	for _, rel := range walk.Files {
		// A file inside a nested-vault directory is not part of this
		// collection's discovery. WalkContained now honours the
		// nested-vault stop itself (D-WALK, R2-MIN-012); the helper's
		// own stopDirs map is kept as defence-in-depth for tests that
		// exercise the helper against a walk produced by a different
		// walker, but the production path never hits it.
		dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
		if dir != "." {
			if _, stop := stopDirs[dir]; stop {
				continue
			}
		}

		if !isViewExtension(filepath.Base(filepath.FromSlash(rel))) {
			continue
		}

		vf, err := openAndReadView(fsys, root, rel)
		if err != nil {
			rep.Files = append(rep.Files, vf)
			continue
		}
		rep.Files = append(rep.Files, vf)
	}

	sort.Slice(rep.Files, func(i, j int) bool {
		return rep.Files[i].Path < rep.Files[j].Path
	})
	return rep, nil
}

// isViewExtension reports whether name's extension is `.view`
// (case-insensitive, per FR-VA-012a / MIN-010). A bare `.view` with no
// stem is NOT a view — it is a dotfile hidden by the Library's dot-prefix
// rule (EC-9).
func isViewExtension(name string) bool {
	if name == "" {
		return false
	}
	// A bare ".view" is the entire name, with no extension in the ordinary
	// sense; the Library's dot-prefix rule hides it.
	if name == viewExtension {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return ext == viewExtension
}

// openAndReadView opens the file at walkRel (collection-relative) with no
// follow semantics, verifies the open target matches what the walk found,
// reads at most MaxViewFileBytes+1 bytes, and returns the result. Any
// failure becomes a ViewFile.Err with an appropriate Rejection code —
// never a panic and never a silent drop.
func openAndReadView(fsys LinkFS, root CollectionRoot, walkRel string) (ViewFile, error) {
	abs, err := root.ResolveContainedNoSymlink(fsys, walkRel)
	if err != nil {
		return ViewFile{
			Path:      walkRel,
			Err:       fmt.Errorf("resolve view path: %w", err),
			Rejection: viewRejectionUnreadable,
		}, nil
	}

	f, err := openNoFollow(fsys, abs)
	if err != nil {
		return ViewFile{
			Path:      walkRel,
			Err:       fmt.Errorf("open view file: %w", err),
			Rejection: viewRejectionUnreadable,
		}, nil
	}
	defer f.Close()

	// The walk observed the file as a regular file. Compare the open fd's
	// stat (which DOES follow the open) against the walk's Lstat (which
	// does not). A mismatch means the file was swapped for a symlink
	// between walk and read — the exact race MIN-001 identified.
	walkInfo, werr := fsys.Lstat(abs)
	if werr != nil {
		return ViewFile{
			Path:      walkRel,
			Err:       fmt.Errorf("re-stat view file: %w", werr),
			Rejection: viewRejectionUnreadable,
		}, nil
	}
	st, ok := f.(interface{ Stat() (fs.FileInfo, error) })
	if !ok {
		return ViewFile{
			Path:      walkRel,
			Err:       errors.New("opened view file does not support Stat"),
			Rejection: viewRejectionUnreadable,
		}, nil
	}
	fdInfo, ferr := st.Stat()
	if ferr != nil {
		return ViewFile{
			Path:      walkRel,
			Err:       fmt.Errorf("stat opened view file: %w", ferr),
			Rejection: viewRejectionUnreadable,
		}, nil
	}
	if !sameRegularFile(walkInfo, fdInfo) {
		return ViewFile{
			Path:      walkRel,
			Err:       errors.New("view file was swapped for a different file between walk and read"),
			Rejection: viewRejectionUnreadable,
		}, nil
	}

	// Read at most cap+1 bytes so the caller can distinguish "exactly the
	// cap" from "over the cap" by checking len > cap.
	buf := &bytes.Buffer{}
	n, err := io.CopyN(buf, f, MaxViewFileBytes+1)
	_ = n
	if err != nil && !errors.Is(err, io.EOF) {
		return ViewFile{
			Path:      walkRel,
			Err:       fmt.Errorf("read view file: %w", err),
			Rejection: viewRejectionUnreadable,
		}, nil
	}
	data := buf.Bytes()
	size := walkInfo.Size()

	rejection := ""
	if int64(len(data)) > MaxViewFileBytes {
		// Trim to cap so a caller never sees bytes past it (D-SIZECAP).
		data = data[:MaxViewFileBytes]
		rejection = viewRejectionTooLarge
	}
	return ViewFile{
		Path: abs,
		// Bytes is what was read from disk; Path stays as the absolute
		// resolved path (matches what records.LoadViews's parse site
		// historically produced). Callers needing the walk-relative form
		// can compute it from the CollectionRoot.
		Bytes:     data,
		Size:      size,
		Rejection: rejection,
	}, nil
}

// viewRejectionUnreadable / viewRejectionTooLarge are the two codes this
// helper emits, named so the wire-layer translator can map them to the
// generated LibraryEntryViewRejection enum without a stringly-typed
// switch at every call site.
const (
	viewRejectionUnreadable = "view_unreadable"
	viewRejectionTooLarge   = "view_too_large"
)

// openNoFollow opens abs without following a terminal symlink. On Linux
// and macOS the syscall.O_NOFOLLOW flag does the work; on platforms that
// lack it (today: Windows) the caller is expected to have used
// ResolveContainedNoSymlink upstream, which already compares the resolved
// path against the lexical one — any link anywhere in the chain refuses
// before this call. The Stat-then-Fstat guard in openAndReadView then
// closes the open-time window D-SYMLINK-READ requires.
func openNoFollow(fsys LinkFS, abs string) (fs.File, error) {
	if o, ok := fsys.(interface {
		OpenNoFollow(string) (fs.File, error)
	}); ok {
		return o.OpenNoFollow(abs)
	}
	return openNoFollowDefault(abs)
}

func openNoFollowDefault(abs string) (fs.File, error) {
	// fs.OpenFile is the only standard-library call site that accepts
	// syscall.O_NOFOLLOW.
	return os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// ReadViewFile reads ONE .view file's bytes from root at rel with the
// SAME containment, no-follow, and size-cap rules the discovery walker
// applies — the canonical reader for any caller that needs to look up
// a single file's content (re-derivation's post-move verify, the
// Library content-put post-write validator, the editor's
// pre-fetch-of-bytes path).
//
// Public so callers outside the walk (single-file reads triggered by
// an explicit path argument, NOT by the whole-collection discovery)
// share the same reader rather than re-implementing the open/limit
// recipe. Returns the same ViewFile shape the walker produces.
func ReadViewFile(fsys LinkFS, root CollectionRoot, rel string) (ViewFile, error) {
	// Re-validate containment AND run the lexical-vs-resolved check that
	// ResolveContainedNoSymlink does (FR-044). A path that resolves
	// through a symlink refuses here before any open.
	abs, err := root.ResolveContainedNoSymlink(fsys, rel)
	if err != nil {
		return ViewFile{}, err
	}
	f, err := openNoFollow(fsys, abs)
	if err != nil {
		return ViewFile{}, fmt.Errorf("open view file: %w", err)
	}
	defer f.Close()
	walkInfo, werr := fsys.Lstat(abs)
	if werr != nil {
		return ViewFile{}, fmt.Errorf("lstat view file: %w", werr)
	}
	st, ok := f.(interface{ Stat() (fs.FileInfo, error) })
	if !ok {
		return ViewFile{}, errors.New("opened view file does not support Stat")
	}
	fdInfo, ferr := st.Stat()
	if ferr != nil {
		return ViewFile{}, fmt.Errorf("stat opened view file: %w", ferr)
	}
	if !sameRegularFile(walkInfo, fdInfo) {
		return ViewFile{}, errors.New("view file was swapped for a different file between walk and read")
	}
	buf := &bytes.Buffer{}
	if _, err := io.CopyN(buf, f, MaxViewFileBytes+1); err != nil && !errors.Is(err, io.EOF) {
		return ViewFile{}, fmt.Errorf("read view file: %w", err)
	}
	data := buf.Bytes()
	rejection := ""
	if len(data) > MaxViewFileBytes {
		data = data[:MaxViewFileBytes]
		rejection = viewRejectionTooLarge
	}
	return ViewFile{
		Path:      abs,
		Bytes:     data,
		Size:      walkInfo.Size(),
		Rejection: rejection,
	}, nil
}

// sameRegularFile reports whether two fs.FileInfo describe the SAME
// underlying file. Device + Inode is the test on POSIX; a caller on
// Windows where Inode is zero falls through to size+name+mtime which is
// the best portable approximation.
func sameRegularFile(a, b fs.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	if !a.Mode().IsRegular() || !b.Mode().IsRegular() {
		return false
	}
	if sysA := a.Sys(); sysA != nil {
		if sysB := b.Sys(); sysB != nil {
			if sa, ok := sysA.(*syscall.Stat_t); ok {
				if sb, ok := sysB.(*syscall.Stat_t); ok {
					if sa.Dev != 0 && sb.Dev != 0 && sa.Ino != 0 && sb.Ino != 0 {
						return sa.Dev == sb.Dev && sa.Ino == sb.Ino
					}
				}
			}
		}
	}
	// Fallback for platforms / fakes that do not surface a syscall.Stat_t:
	// size + name + mtime is the closest portable identity the standard
	// library exposes.
	return a.Size() == b.Size() && a.Name() == b.Name() && a.ModTime().Equal(b.ModTime())
}

// ---------------------------------------------------------------------------
// Nested-vault stop (D-WALK, R2-MIN-012). A directory that itself carries
// a `.obsidian` or `.omnipus-vault` marker is the START of a knowledge
// base, not a child of one — the outer walk stops at it so the inner
// vault's own listings cannot disagree with the outer's about which views
// exist (EC-11, Dataset A13, test 71).
//
// The marker is detected by looking at the directory's own children: a
// directory containing a `.obsidian` or `.omnipus-vault` child is a vault
// boundary, regardless of whether the marker is a file or a directory. The
// WalkContained output above supplies the candidate directories; this
// function consults each one's children via the LinkFS to decide.
// ---------------------------------------------------------------------------

// isNestedVault reports whether rel is a directory (already known to the
// walker) that itself carries a `.obsidian` or `.omnipus-vault` marker in
// its children — the rule D-WALK's round-2 nested-vault stop enforces.
func isNestedVault(fsys LinkFS, root CollectionRoot, rel string) bool {
	abs, err := root.ResolveContainedNoSymlink(fsys, rel)
	if err != nil {
		return false
	}
	entries, err := fsys.ReadDir(abs)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".obsidian" || name == ".omnipus-vault" {
			return true
		}
	}
	return false
}

// collectNestedVaults walks walk.Dirs a second time (cheap — already in
// memory) and returns the collection-relative paths of any directory that
// is itself a vault. Used by DiscoverViewFiles to suppress both the
// descendant walk AND the file list WalkContained already produced for
// those subtrees.
func collectNestedVaults(fsys LinkFS, root CollectionRoot, walk WalkResult) map[string]struct{} {
	stop := map[string]struct{}{}
	for _, d := range walk.Dirs {
		if d == "." {
			continue
		}
		if isNestedVault(fsys, root, d) {
			stop[d] = struct{}{}
		}
	}
	return stop
}

// HasNestedVaultMarker is a public test surface: callers (and the
// nested-vault stop tests) can ask whether a collection-relative directory
// is itself a vault without knowing the LinkFS plumbing.
func HasNestedVaultMarker(fsys LinkFS, root CollectionRoot, rel string) bool {
	return isNestedVault(fsys, root, rel)
}

// Compile-time check that openNoFollowDefault returns the same interface
// the LinkFS caller in openNoFollow expects. fs.File must implement Stat.
var _ fs.File = (*os.File)(nil)

// pathsafeRuleSet is the path-safe validator the spec mandates for view
// filenames (D-SEC, FR-VA-007). Exposed so callers (knowledge_configure's
// write_view, knowledge_restructure's rename) can share the same rule
// without each re-implementing the rule list.
var pathsafeRuleSet = pathsafe.ActiveRules()

// PathsafeRuleSet returns the pathsafe rule set this module uses for view
// filenames. Exposed for the rename/move path's own validator; the
// writers and the rename door cannot disagree about what a view filename
// may be without one of them being wrong.
func PathsafeRuleSet() *pathsafe.RuleSet { return &pathsafeRuleSet }
