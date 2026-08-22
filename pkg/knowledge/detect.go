// Detection and identity: is this folder a knowledge base, and which folder,
// exactly, is "this collection"? (FR-020, FR-021, FR-025, FR-026.)
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
	"strings"

	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

var (
	// ErrOutsideCollection is returned for any path that would leave the
	// collection's single root — a traversing wikilink, an absolute-path link,
	// or a path belonging to a different collection entirely. It is the one
	// gate every link, backlink and search hit must pass (FR-026, FR-043).
	ErrOutsideCollection = errors.New("knowledge: path is outside the collection")

	// ErrMultipleRoots is the sentinel behind MultipleRootsError, so callers
	// that only need the class can use errors.Is (FR-026).
	ErrMultipleRoots = errors.New("knowledge: a knowledge base is exactly one mounted folder")
)

// MultipleRootsError reports an attempt to give one knowledge base a second
// root directory, and NAMES BOTH — the one already bound and the one refused.
//
// Both are named because the operator's next question is always "which two?".
// A bare "already has a root" tells them nothing actionable: the same host
// folder mounted twice is legitimate and silently accepted (see
// Collection.AttachRoot), so the only case that reaches this error is two
// genuinely different folders, and the fix is to choose one or to open the
// second as its own collection.
//
// This is not a tidiness rule. Merging two roots into one collection would let a
// wikilink in one folder resolve into the other, and would make "what links
// here?" answer with files from a folder the reader never opened (FR-026).
type MultipleRootsError struct {
	// Existing is the root the collection is already bound to (real path).
	Existing string
	// Attempted is the root that was refused (real path where it could be
	// resolved, otherwise as given).
	Attempted string
}

func (e *MultipleRootsError) Error() string {
	return fmt.Sprintf(
		"knowledge: a knowledge base is exactly one mounted folder: %q is already this collection's root, refusing to also use %q",
		e.Existing, e.Attempted)
}

// Unwrap lets errors.Is(err, ErrMultipleRoots) succeed while errors.As still
// recovers both paths.
func (e *MultipleRootsError) Unwrap() error { return ErrMultipleRoots }

// DetectFS is the filesystem seam detection and scoped lookup go through.
//
// It exists so a test can PROVE the two properties that cannot be proved by
// reading the code: that detection reads no file contents (FR-021), and that
// deciding a path belongs to another collection never touches that other
// collection (FR-026). A fake implementing this interface counts ReadFile calls
// and records every directory it listed.
//
// ReadFile is on the interface even though nothing in this file calls it. That
// is the point: an implementation that starts reading note contents to decide
// detection has to go through this method, where the fake is watching.
type DetectFS interface {
	// ReadDir lists a directory's entries WITHOUT following symlinks in the
	// entries themselves (os.ReadDir semantics: entry.IsDir reflects lstat).
	ReadDir(name string) ([]os.DirEntry, error)
	// ReadFile reads a whole file's contents.
	ReadFile(name string) ([]byte, error)
}

// OSFS is the real filesystem implementation of DetectFS.
type OSFS struct{}

// ReadDir implements DetectFS.
func (OSFS) ReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }

// ReadFile implements DetectFS.
func (OSFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// Detection is what detection decided about one folder. It is a verdict plus the
// evidence behind it, so a caller can tell an Obsidian vault Omnipus has never
// written to from one it created itself.
type Detection struct {
	// Root is the folder that was examined, as given.
	Root string
	// HasOmnipusMarker reports a .omnipus-vault/ DIRECTORY at the root.
	HasOmnipusMarker bool
	// HasObsidianMarker reports a .obsidian/ DIRECTORY at the root.
	HasObsidianMarker bool
}

// IsKnowledgeBase is the verdict: either marker alone suffices (FR-020).
func (d Detection) IsKnowledgeBase() bool { return d.HasOmnipusMarker || d.HasObsidianMarker }

// Detect reports whether root is a knowledge base, using the real filesystem.
func Detect(root string) (Detection, error) { return DetectUsing(OSFS{}, root) }

// IsKnowledgeBase is the one-line form of Detect for callers that need only the
// verdict. A folder that cannot be listed is an ERROR, never a quiet "no": a
// mount whose target has gone missing must surface as a broken mount, not as an
// ordinary folder that happens to have no features (FR-112, ADR-067 D13).
func IsKnowledgeBase(root string) (bool, error) {
	d, err := Detect(root)
	if err != nil {
		return false, err
	}
	return d.IsKnowledgeBase(), nil
}

// DetectUsing is Detect against an injected filesystem.
//
// It reads DIRECTORY ENTRIES ONLY (FR-021). It never opens a note, never reads
// the marker document, and never consults file contents of any kind — including
// the marker's own JSON, which is read separately by ReadMarker AFTER the
// verdict is already decided. The reason is not performance: a folder of
// 100,000 notes must be classified in one directory listing, and a detection
// rule that depended on contents would make "is this a knowledge base?" a
// question whose answer could change without any directory changing.
//
// A marker must be a DIRECTORY. FR-020 spells both markers with a trailing
// slash, and both are directories in every real collection. A regular file named
// ".obsidian" is therefore not a marker, and neither is a SYMLINK named
// ".obsidian" pointing at one somewhere else — symlinks are never followed
// anywhere in this package (FR-044), and honouring a symlinked marker would let
// a folder claim knowledge-base status by pointing at another folder's config.
func DetectUsing(fsys DetectFS, root string) (Detection, error) {
	d := Detection{Root: root}
	if strings.TrimSpace(root) == "" {
		return d, fmt.Errorf("knowledge: detect: empty root path")
	}
	entries, err := fsys.ReadDir(root)
	if err != nil {
		return d, fmt.Errorf("knowledge: detect %s: %w", root, err)
	}
	for _, e := range entries {
		switch e.Name() {
		case MarkerDirName:
			d.HasOmnipusMarker = e.IsDir()
		case ObsidianMarkerDirName:
			d.HasObsidianMarker = e.IsDir()
		}
	}
	return d, nil
}

// Collection is exactly one mounted folder (FR-026, AW-5).
//
// Its root is stored as a REAL PATH — absolute and symlink-resolved — because
// that is the identity the whole feature is keyed on: the same host folder
// mounted into two workspaces, or twice into one, is ONE collection with ONE
// index (ADR-067 D3). Two mounts that resolve to the same real path are
// therefore the same collection and are accepted; two that do not are refused.
type Collection struct {
	root   string
	marker Marker
	hasMar bool
}

// OpenCollection binds a Collection to root, which must already be a knowledge
// base (FR-020). It reads the Omnipus marker if one is present; an Obsidian
// vault Omnipus has never written to is opened successfully with no marker, and
// falls back to the folder's own name for display.
func OpenCollection(root string) (*Collection, error) {
	resolved, err := realPath(root)
	if err != nil {
		return nil, err
	}
	d, err := Detect(resolved)
	if err != nil {
		return nil, err
	}
	if !d.IsKnowledgeBase() {
		return nil, fmt.Errorf("%w: %s", ErrNotKnowledgeBase, resolved)
	}
	c := &Collection{root: resolved}
	m, err := ReadMarker(resolved)
	switch {
	case err == nil:
		c.marker, c.hasMar = m, true
	case errors.Is(err, ErrNoMarker):
		// Legitimate: a folder detected via .obsidian/ alone.
	default:
		return nil, err
	}
	return c, nil
}

// Root is the collection's single root directory, as a real path.
func (c *Collection) Root() string { return c.root }

// HasMarker reports whether an Omnipus marker was read at open time.
func (c *Collection) HasMarker() bool { return c.hasMar }

// Marker returns the Omnipus marker, zero-valued when there is none.
func (c *Collection) Marker() Marker { return c.marker }

// DisplayName is the operator-facing name. It comes from the marker when there
// is one (FR-024) and from the folder's own name otherwise — never from an
// absolute path, so it does not change when the folder moves.
func (c *Collection) DisplayName() string {
	if c.hasMar {
		if name := strings.TrimSpace(c.marker.DisplayName); name != "" {
			return name
		}
	}
	return filepath.Base(c.root)
}

// TemplatesDir is the absolute path of this collection's templates directory.
func (c *Collection) TemplatesDir() string { return TemplatesPath(c.root, c.marker) }

// AttachRoot binds another root to this collection, which succeeds ONLY when it
// is the same folder reached by a different name (FR-026).
//
// The same host folder mounted twice — into two workspaces, or twice into one —
// is legitimate and common: CreateMount checks mount NAME collisions only, never
// host path (ADR-067 D3). Those mounts resolve to the same real path and are
// accepted here, which is what makes one corpus get one index. A genuinely
// different folder is refused with a *MultipleRootsError naming both.
func (c *Collection) AttachRoot(root string) error {
	resolved, err := realPath(root)
	if err != nil {
		// Name the unresolvable candidate as given: the operator still needs to
		// know which path was refused.
		return &MultipleRootsError{Existing: c.root, Attempted: strings.TrimSpace(root)}
	}
	if resolved == c.root {
		return nil
	}
	return &MultipleRootsError{Existing: c.root, Attempted: resolved}
}

// ResolveInside turns a collection-relative path into an absolute path inside
// this collection, or refuses with ErrOutsideCollection.
//
// EVERY link, backlink and search hit must be resolved through this method. It
// is the mechanical reason a wikilink cannot reach a second collection, a
// sibling folder, or the operator's home directory:
//
//   - "../../../.ssh/id_rsa" and "/etc/passwd" are refused by
//     library.CleanRelPath, the same validator every Library path goes through.
//   - a path naming a file in a DIFFERENT collection is refused for exactly the
//     same reason — from this collection's root it can only be spelled with a
//     traversal or as an absolute path.
//   - the containment check is repeated after joining, so a future change to
//     CleanRelPath cannot silently widen this.
//
// It performs NO filesystem access at all: refusing an out-of-collection path
// must never require reading the thing being refused.
func (c *Collection) ResolveInside(rel string) (string, error) {
	cleaned, err := library.CleanRelPath(rel)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrOutsideCollection, rel, err)
	}
	if cleaned == "" {
		return "", fmt.Errorf("%w: %q names the collection root, not a file within it", ErrOutsideCollection, rel)
	}
	abs := filepath.Join(c.root, filepath.FromSlash(cleaned))
	if !isWithinOrEqual(c.root, abs) {
		return "", fmt.Errorf("%w: %q resolves to %q, outside %q", ErrOutsideCollection, rel, abs, c.root)
	}
	return abs, nil
}

// LookupInside reports the absolute path of rel within this collection and
// whether a regular file is there.
//
// It exists so that "does this collection contain that note?" can be answered
// WITHOUT reading the note — the lookup lists one directory and compares names.
// That matters for FR-026: when a link names a note that only exists in a
// different collection, the answer must be "unresolved" reached without ever
// touching the other collection's files.
//
// A symlink is not a match: entry types come from lstat, and this package never
// follows symlinks (FR-044).
func (c *Collection) LookupInside(fsys DetectFS, rel string) (string, bool, error) {
	abs, err := c.ResolveInside(rel)
	if err != nil {
		return "", false, err
	}
	entries, err := fsys.ReadDir(filepath.Dir(abs))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("knowledge: lookup %q: %w", rel, err)
	}
	base := filepath.Base(abs)
	for _, e := range entries {
		if e.Name() == base && e.Type().IsRegular() {
			return abs, true, nil
		}
	}
	return "", false, nil
}

// CreateInWorkspace creates or initialises a knowledge base inside a workspace's
// work tree, and returns it (FR-022, FR-023, FR-025).
//
// relPath is slash-separated and relative to workspaces/<id>/work/. There is
// deliberately NO exported way to create a knowledge base at an arbitrary host
// path: Omnipus does not gain the ability to create directories wherever it
// likes (ADR-067 D11). Relocating a collection afterwards is the existing
// workspace transfer path's job, and the marker travels with the folder, so the
// name survives the move with no migration (FR-024).
//
// Containment is enforced at the SYSCALL boundary by os.Root, not by inspecting
// the string: a lexically clean relPath that traverses through a symlink still
// cannot escape the work tree. library.CleanRelPath runs first so the common
// refusals are typed and legible.
//
// It writes MarkerDirName. It never writes ObsidianMarkerDirName — no code path
// in this package does (FR-023, and doc.go for why).
func CreateInWorkspace(home, workspaceID, relPath string, m Marker) (*Collection, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	workDir, err := workspace.SafeWorkDir(home, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("knowledge: create in workspace: %w", err)
	}
	cleaned, err := library.CleanRelPath(relPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %q: %v", ErrOutsideCollection, relPath, err)
	}
	if cleaned == "" {
		return nil, fmt.Errorf("%w: %q does not name a folder inside the workspace work tree", ErrOutsideCollection, relPath)
	}
	if mkErr := os.MkdirAll(workDir, 0o700); mkErr != nil {
		return nil, fmt.Errorf("knowledge: prepare workspace work tree: %w", mkErr)
	}
	r, err := os.OpenRoot(workDir)
	if err != nil {
		return nil, fmt.Errorf("knowledge: open workspace work tree: %w", err)
	}
	defer func() { _ = r.Close() }()

	if _, statErr := r.Stat(filepath.ToSlash(cleaned) + "/" + MarkerDirName); statErr == nil {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyKnowledgeBase, filepath.Join(workDir, cleaned))
	}
	if err := r.MkdirAll(cleaned, 0o755); err != nil {
		return nil, fmt.Errorf("knowledge: create knowledge base folder: %w", err)
	}
	if err := writeMarkerInto(r, cleaned, m); err != nil {
		return nil, err
	}
	return OpenCollection(filepath.Join(workDir, filepath.FromSlash(cleaned)))
}

// realPath resolves p to an absolute, symlink-free, cleaned path. It mirrors
// pkg/workspace's resolveExistingDir: a collection's identity is its real path,
// so two spellings of one folder are one collection (ADR-067 D3).
func realPath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("knowledge: empty root path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("knowledge: resolve absolute path %q: %w", p, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("knowledge: resolve real path of %q: %w", abs, err)
	}
	return filepath.Clean(resolved), nil
}

// isWithinOrEqual reports whether candidate is root itself or lives strictly
// under it, guarded by a trailing separator so "/a/bc" does not match "/a/b".
// Mirrors pkg/workspace's helper of the same shape (unexported there).
func isWithinOrEqual(root, candidate string) bool {
	if candidate == root {
		return true
	}
	return strings.HasPrefix(candidate, root+string(filepath.Separator))
}
