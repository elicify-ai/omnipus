// Tests for detection and identity (FR-020, FR-021, FR-022, FR-023, FR-025,
// FR-026). Spec: docs/internal/specs/adr-067-knowledge-base-and-preview-spec.md
// §13.1 items 13, 14, 15, 16 and 71.
//
// Every expected value below is derived from the specification text, never from
// what the implementation happens to do.
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
	"runtime"
	"sort"
	"strings"
	"testing"
)

// detectRecordingFS is a DetectFS that delegates directory listing to the real
// filesystem (so entries carry real lstat-derived types) while RECORDING every
// call, and REFUSES every file-content read.
//
// Refusing rather than merely counting is deliberate: a counter alone can be
// satisfied by an implementation that reads a file and ignores the result. An
// implementation that depends on file contents in any way fails outright here.
type detectRecordingFS struct {
	dirsListed []string
	filesRead  []string
}

func (f *detectRecordingFS) ReadDir(name string) ([]os.DirEntry, error) {
	f.dirsListed = append(f.dirsListed, filepath.Clean(name))
	return os.ReadDir(name)
}

func (f *detectRecordingFS) ReadFile(name string) ([]byte, error) {
	f.filesRead = append(f.filesRead, filepath.Clean(name))
	return nil, fmt.Errorf("detectRecordingFS: refusing to read file contents: %s", name)
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// treeOf lists every path under root, relative and sorted, so a before/after
// comparison can prove that a refused operation created nothing.
func treeOf(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// --- FR-020 -----------------------------------------------------------------

// TestDetectKnowledgeBase_MarkerMatrix is spec test 13 (US-4 AS-1, AS-2, AS-3).
//
// Oracle: FR-020 — "a folder is a knowledge base if its ROOT CONTAINS
// .omnipus-vault/ or .obsidian/". Both markers are spelled with a trailing
// slash, i.e. they are directories; "its root" excludes subdirectories. FR-044
// ("skip and report symbolic links rather than following them") supplies the
// symlink rows.
func TestDetectKnowledgeBase_MarkerMatrix(t *testing.T) {
	cases := []struct {
		name         string
		build        func(t *testing.T, root string)
		wantKB       bool
		wantOmnipus  bool
		wantObsidian bool
	}{
		{
			name:         "obsidian marker directory alone is a knowledge base",
			build:        func(t *testing.T, root string) { mustMkdir(t, filepath.Join(root, ObsidianMarkerDirName)) },
			wantKB:       true,
			wantObsidian: true,
		},
		{
			name:        "omnipus marker directory alone is a knowledge base",
			build:       func(t *testing.T, root string) { mustMkdir(t, filepath.Join(root, MarkerDirName)) },
			wantKB:      true,
			wantOmnipus: true,
		},
		{
			name: "both markers is a knowledge base",
			build: func(t *testing.T, root string) {
				mustMkdir(t, filepath.Join(root, MarkerDirName))
				mustMkdir(t, filepath.Join(root, ObsidianMarkerDirName))
			},
			wantKB:       true,
			wantOmnipus:  true,
			wantObsidian: true,
		},
		{
			name: "a folder full of markdown with neither marker is an ordinary folder",
			build: func(t *testing.T, root string) {
				mustWrite(t, filepath.Join(root, "Alpha.md"), "# Alpha\n")
				mustWrite(t, filepath.Join(root, "notes", "Beta.md"), "# Beta\n")
			},
			wantKB: false,
		},
		{
			name:   "a regular FILE named .obsidian is not a marker",
			build:  func(t *testing.T, root string) { mustWrite(t, filepath.Join(root, ObsidianMarkerDirName), "{}") },
			wantKB: false,
		},
		{
			name:   "a regular FILE named .omnipus-vault is not a marker",
			build:  func(t *testing.T, root string) { mustWrite(t, filepath.Join(root, MarkerDirName), "{}") },
			wantKB: false,
		},
		{
			name: "a marker in a SUBFOLDER does not make the parent a knowledge base",
			build: func(t *testing.T, root string) {
				mustMkdir(t, filepath.Join(root, "inner", ObsidianMarkerDirName))
			},
			wantKB: false,
		},
		{
			name: "an unrelated dot-directory is not a marker",
			build: func(t *testing.T, root string) {
				mustMkdir(t, filepath.Join(root, ".git"))
				mustMkdir(t, filepath.Join(root, ".omnipus"))
			},
			wantKB: false,
		},
		{
			name: "a SYMLINK named .omnipus-vault is never followed and is not a marker",
			build: func(t *testing.T, root string) {
				if runtime.GOOS == "windows" {
					t.Skip("symlink creation needs privilege on Windows")
				}
				elsewhere := filepath.Join(t.TempDir(), "real-vault")
				mustMkdir(t, elsewhere)
				if err := os.Symlink(elsewhere, filepath.Join(root, MarkerDirName)); err != nil {
					t.Fatalf("symlink: %v", err)
				}
			},
			wantKB: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.build(t, root)

			got, err := Detect(root)
			if err != nil {
				t.Fatalf("Detect(%s): unexpected error: %v", root, err)
			}
			if got.IsKnowledgeBase() != tc.wantKB {
				t.Errorf("IsKnowledgeBase() = %v, want %v (FR-020)", got.IsKnowledgeBase(), tc.wantKB)
			}
			if got.HasOmnipusMarker != tc.wantOmnipus {
				t.Errorf("HasOmnipusMarker = %v, want %v", got.HasOmnipusMarker, tc.wantOmnipus)
			}
			if got.HasObsidianMarker != tc.wantObsidian {
				t.Errorf("HasObsidianMarker = %v, want %v", got.HasObsidianMarker, tc.wantObsidian)
			}

			// The convenience form must agree with the detailed one.
			isKB, err := IsKnowledgeBase(root)
			if err != nil {
				t.Fatalf("IsKnowledgeBase(%s): unexpected error: %v", root, err)
			}
			if isKB != tc.wantKB {
				t.Errorf("IsKnowledgeBase() convenience form = %v, want %v", isKB, tc.wantKB)
			}
		})
	}
}

// --- FR-021 -----------------------------------------------------------------

// TestDetectKnowledgeBase_NoContentReads is spec test 14 (US-4 AS-4).
//
// Oracle: FR-021 — "the system MUST NOT read file contents to decide
// detection", and the BDD scenario "Given a mounted folder with a marker and 500
// notes / When detection runs / Then no note file is opened for reading".
//
// The fake refuses every read, so an implementation that consults contents
// cannot reach the right verdict by accident; the recorded call list then proves
// zero reads were even attempted, including of the marker's own JSON.
func TestDetectKnowledgeBase_NoContentReads(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, MarkerDirName))
	// A marker document exists and is well-formed: the point is that detection
	// does not read it either.
	mustWrite(t, MarkerPath(root), `{"display_name":"Research"}`)
	for i := range 500 {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("Note-%03d.md", i)), "# Note\n\nbody\n")
	}

	fake := &detectRecordingFS{}
	got, err := DetectUsing(fake, root)
	if err != nil {
		t.Fatalf("DetectUsing: unexpected error: %v", err)
	}
	if !got.IsKnowledgeBase() {
		t.Fatalf("IsKnowledgeBase() = false, want true (FR-020: .omnipus-vault/ present)")
	}
	if len(fake.filesRead) != 0 {
		t.Errorf("detection read %d file(s), want 0 (FR-021): %v", len(fake.filesRead), fake.filesRead)
	}
	if len(fake.dirsListed) != 1 || fake.dirsListed[0] != filepath.Clean(root) {
		t.Errorf("detection listed %v, want exactly [%s] — the decision is one listing of the ROOT (FR-020, FR-021)",
			fake.dirsListed, filepath.Clean(root))
	}
}

// TestDetect_MissingFolderIsAnErrorNotAnOrdinaryFolder.
//
// Oracle: §11 "The operator's filesystem" — "Missing folder → broken mount.
// Permission denied → reported, never skipped silently" — and FR-112, "the
// system MUST report files it cannot address, rather than omitting them
// silently". A mount whose target vanished must not read as "an ordinary folder
// with no knowledge-base features".
func TestDetect_MissingFolderIsAnErrorNotAnOrdinaryFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-folder")

	if _, err := Detect(missing); err == nil {
		t.Fatalf("Detect(%s) = nil error, want an error naming the missing folder", missing)
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Detect error = %v, want one wrapping fs.ErrNotExist", err)
	}

	if _, err := IsKnowledgeBase(missing); err == nil {
		t.Errorf("IsKnowledgeBase(%s) = nil error, want an error", missing)
	}
	if _, err := Detect("  "); err == nil {
		t.Errorf("Detect(blank) = nil error, want an error")
	}
}

// --- FR-022, FR-023, FR-025 -------------------------------------------------

// TestCreateKnowledgeBase_WritesOwnMarkerOnly is spec test 15 (US-4 AS-5).
//
// Oracle: FR-022 (".omnipus-vault/ is written"), FR-023 (".obsidian/ is never
// created") and FR-025 (created "inside the workspace tree, not at arbitrary
// host paths"), plus the BDD scenario "Creating a knowledge base writes only the
// Omnipus marker".
func TestCreateKnowledgeBase_WritesOwnMarkerOnly(t *testing.T) {
	home := t.TempDir()
	const wsID = "ws-alpha"

	c, err := CreateInWorkspace(home, wsID, "research-notes", Marker{DisplayName: "Research"})
	if err != nil {
		t.Fatalf("CreateInWorkspace: %v", err)
	}

	// FR-025: it lives inside workspaces/<id>/work/, not anywhere else.
	wantParent, err := realPath(filepath.Join(home, "workspaces", wsID, "work"))
	if err != nil {
		t.Fatalf("resolve expected work dir: %v", err)
	}
	wantRoot := filepath.Join(wantParent, "research-notes")
	if c.Root() != wantRoot {
		t.Errorf("Root() = %q, want %q (FR-025: inside the workspace work tree)", c.Root(), wantRoot)
	}

	// FR-022: the Omnipus marker directory and its document exist.
	if fi, err := os.Stat(MarkerDir(c.Root())); err != nil || !fi.IsDir() {
		t.Errorf("%s/ missing or not a directory (FR-022): stat err=%v", MarkerDirName, err)
	}
	if _, err := os.Stat(MarkerPath(c.Root())); err != nil {
		t.Errorf("marker document missing (FR-022, FR-024): %v", err)
	}

	// FR-023: nothing named .obsidian anywhere in what we created.
	for _, rel := range treeOf(t, c.Root()) {
		if filepath.Base(rel) == ObsidianMarkerDirName {
			t.Errorf("created %q — Omnipus must never create %s (FR-023)", rel, ObsidianMarkerDirName)
		}
	}

	// The result is detected as a knowledge base by the same rule as any other.
	if isKB, err := IsKnowledgeBase(c.Root()); err != nil || !isKB {
		t.Errorf("IsKnowledgeBase(created) = %v, %v; want true, nil (FR-020)", isKB, err)
	}

	// Templates location exists (ADR-067 D2, D12).
	if fi, err := os.Stat(c.TemplatesDir()); err != nil || !fi.IsDir() {
		t.Errorf("templates directory %s missing: %v", c.TemplatesDir(), err)
	}

	// Creating over an existing knowledge base fails loudly rather than
	// silently re-initialising the operator's collection.
	if _, err := CreateInWorkspace(home, wsID, "research-notes", Marker{DisplayName: "Research"}); !errors.Is(err, ErrAlreadyKnowledgeBase) {
		t.Errorf("second CreateInWorkspace error = %v, want ErrAlreadyKnowledgeBase", err)
	}
}

// TestCreateKnowledgeBase_RefusesOutsideTheWorkspaceTree.
//
// Oracle: FR-025 — knowledge bases are created "inside the workspace tree, not
// at arbitrary host paths" (ADR-067 D11: "It does NOT gain the ability to create
// directories at arbitrary host paths"). Each refusal is followed by a
// before/after tree comparison of the whole home directory, because "returns an
// error" and "creates nothing" are different claims and only the second one is
// the requirement.
func TestCreateKnowledgeBase_RefusesOutsideTheWorkspaceTree(t *testing.T) {
	home := t.TempDir()
	const wsID = "ws-alpha"

	// Establish the tree first so the comparison is against a populated home.
	if _, err := CreateInWorkspace(home, wsID, "kb-one", Marker{DisplayName: "One"}); err != nil {
		t.Fatalf("setup CreateInWorkspace: %v", err)
	}
	before := treeOf(t, home)

	refusals := []struct {
		name        string
		workspaceID string
		relPath     string
	}{
		{"parent traversal", wsID, "../escaped"},
		{"deep traversal", wsID, "../../../escaped"},
		{"absolute host path", wsID, "/tmp/escaped-kb"},
		{"absolute host path in home", wsID, "/etc/omnipus-kb"},
		{"empty relative path", wsID, ""},
		{"dot", wsID, "."},
		{"backslash separator", wsID, `..\escaped`},
		{"NUL in name", wsID, "kb\x00name"},
		{"traversing workspace id", "../../escape", "kb-two"},
		{"workspace id with separator", "a/b", "kb-two"},
		{"empty workspace id", "", "kb-two"},
	}
	for _, r := range refusals {
		t.Run(r.name, func(t *testing.T) {
			c, err := CreateInWorkspace(home, r.workspaceID, r.relPath, Marker{DisplayName: "Escaped"})
			if err == nil {
				t.Fatalf("CreateInWorkspace(%q, %q) succeeded with root %q, want refusal (FR-025)",
					r.workspaceID, r.relPath, c.Root())
			}
			after := treeOf(t, home)
			if strings.Join(before, "\n") != strings.Join(after, "\n") {
				t.Errorf("refused create still changed the tree (FR-025).\nbefore: %v\nafter:  %v", before, after)
			}
		})
	}
}

// --- FR-024 -----------------------------------------------------------------

// TestKnowledgeBaseIdentity_SurvivesRelocation is spec test 16 (US-4 AS-6).
//
// Oracle: FR-024 and the BDD scenario "Identity survives relocation" — a
// knowledge base named "Research" moved to another path "is still named
// Research" and "no migration step was required".
//
// The display name is deliberately DIFFERENT from the folder name. With
// folder == name, a broken implementation that ignores the marker entirely and
// returns filepath.Base(root) would pass this test at every path.
func TestKnowledgeBaseIdentity_SurvivesRelocation(t *testing.T) {
	home := t.TempDir()
	const displayName = "Research"

	c, err := CreateInWorkspace(home, "ws-alpha", "kb-folder-with-another-name", Marker{DisplayName: displayName})
	if err != nil {
		t.Fatalf("CreateInWorkspace: %v", err)
	}
	originalRoot := c.Root()
	if got := c.DisplayName(); got != displayName {
		t.Fatalf("DisplayName() at creation = %q, want %q", got, displayName)
	}

	// The marker must carry no absolute path — that is the MECHANISM by which
	// relocation needs no migration step. Assert it directly, before moving.
	raw, err := os.ReadFile(MarkerPath(originalRoot))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if strings.Contains(string(raw), originalRoot) || strings.Contains(string(raw), home) {
		t.Errorf("marker stores an absolute path, so relocation would need migration (FR-024): %s", raw)
	}

	// Move the folder somewhere entirely outside the original workspace tree.
	newParent := t.TempDir()
	newRoot := filepath.Join(newParent, "relocated-kb")
	if renameErr := os.Rename(originalRoot, newRoot); renameErr != nil {
		t.Fatalf("relocate: %v", renameErr)
	}

	moved, err := OpenCollection(newRoot)
	if err != nil {
		t.Fatalf("OpenCollection after relocation: %v", err)
	}
	if got := moved.DisplayName(); got != displayName {
		t.Errorf("DisplayName() after relocation = %q, want %q (FR-024)", got, displayName)
	}
	if got := moved.Marker().DisplayName; got != displayName {
		t.Errorf("Marker().DisplayName after relocation = %q, want %q", got, displayName)
	}
	if !strings.HasPrefix(moved.TemplatesDir(), moved.Root()) {
		t.Errorf("TemplatesDir() = %q, want a path under the NEW root %q", moved.TemplatesDir(), moved.Root())
	}
}

// --- FR-026 -----------------------------------------------------------------

// TestKnowledgeBase_SecondMountIsRefusedNotMerged is spec test 71.
//
// Oracle: FR-026 — "a knowledge base MUST be exactly one mounted folder. The
// system MUST refuse a second root with a TYPED error NAMING BOTH, and MUST NOT
// resolve a link, backlink or search hit across two collections." The spec's own
// note on this test: "Second root refused; a wikilink naming a note only in the
// second stays unresolved, proven by a read-recording fake."
func TestKnowledgeBase_SecondMountIsRefusedNotMerged(t *testing.T) {
	home := t.TempDir()
	first, err := CreateInWorkspace(home, "ws-alpha", "collection-a", Marker{DisplayName: "Alpha"})
	if err != nil {
		t.Fatalf("create first collection: %v", err)
	}
	second, err := CreateInWorkspace(home, "ws-alpha", "collection-b", Marker{DisplayName: "Beta"})
	if err != nil {
		t.Fatalf("create second collection: %v", err)
	}

	// A note that exists ONLY in the second collection.
	const onlyInSecond = "Only-In-Beta.md"
	mustWrite(t, filepath.Join(second.Root(), onlyInSecond), "# Only in Beta\n")

	// (1) The second root is refused with a typed error naming BOTH roots.
	err = first.AttachRoot(second.Root())
	if err == nil {
		t.Fatalf("AttachRoot(second root) = nil, want refusal (FR-026)")
	}
	var typed *MultipleRootsError
	if !errors.As(err, &typed) {
		t.Fatalf("AttachRoot error is %T, want *MultipleRootsError (FR-026: a TYPED error)", err)
	}
	if typed.Existing != first.Root() {
		t.Errorf("MultipleRootsError.Existing = %q, want %q", typed.Existing, first.Root())
	}
	if typed.Attempted != second.Root() {
		t.Errorf("MultipleRootsError.Attempted = %q, want %q", typed.Attempted, second.Root())
	}
	msg := typed.Error()
	if !strings.Contains(msg, first.Root()) || !strings.Contains(msg, second.Root()) {
		t.Errorf("error message %q must name BOTH roots %q and %q (FR-026)", msg, first.Root(), second.Root())
	}
	if !errors.Is(err, ErrMultipleRoots) {
		t.Errorf("errors.Is(err, ErrMultipleRoots) = false, want true")
	}

	// (2) The SAME folder reached by another name is one collection, not two —
	// ADR-067 D3 keys identity on the real path and reference-counts mounts.
	if runtime.GOOS != "windows" {
		alias := filepath.Join(t.TempDir(), "alias-to-a")
		if err := os.Symlink(first.Root(), alias); err != nil {
			t.Fatalf("symlink alias: %v", err)
		}
		if err := first.AttachRoot(alias); err != nil {
			t.Errorf("AttachRoot(symlink to the same folder) = %v, want nil — one host folder mounted twice is ONE collection (ADR-067 D3)", err)
		}
	}
	if err := first.AttachRoot(first.Root()); err != nil {
		t.Errorf("AttachRoot(own root) = %v, want nil", err)
	}

	// (3) No link, backlink or search hit resolves across the two collections.
	// Every path form a link could use to name the second collection's note is
	// refused, and the refusal is reached WITHOUT touching the second
	// collection: the fake records every listing and refuses every read.
	fake := &detectRecordingFS{}
	escapes := []string{
		"../collection-b/" + onlyInSecond,
		"../../collection-b/" + onlyInSecond,
		filepath.Join(second.Root(), onlyInSecond),
		"../../../.ssh/id_rsa",
		"/etc/passwd",
	}
	for _, rel := range escapes {
		if _, err := first.ResolveInside(rel); !errors.Is(err, ErrOutsideCollection) {
			t.Errorf("ResolveInside(%q) error = %v, want ErrOutsideCollection (FR-026, FR-043)", rel, err)
		}
		if _, ok, err := first.LookupInside(fake, rel); ok || !errors.Is(err, ErrOutsideCollection) {
			t.Errorf("LookupInside(%q) = ok:%v err:%v, want ok:false and ErrOutsideCollection", rel, ok, err)
		}
	}

	// A bare basename — the commonest wikilink form — finds nothing, because the
	// note lives in the other collection.
	if _, ok, err := first.LookupInside(fake, onlyInSecond); err != nil || ok {
		t.Errorf("LookupInside(%q) = ok:%v err:%v, want ok:false err:nil — the note is in a DIFFERENT collection (FR-026)", onlyInSecond, ok, err)
	}
	// Positive control: a note that IS in the first collection is found, so the
	// negatives above are not "everything returns false".
	mustWrite(t, filepath.Join(first.Root(), "In-Alpha.md"), "# In Alpha\n")
	if got, ok, err := first.LookupInside(fake, "In-Alpha.md"); err != nil || !ok {
		t.Errorf("LookupInside(In-Alpha.md) = %q, ok:%v, err:%v; want found", got, ok, err)
	}

	// Nothing under the second collection was read or even listed.
	if len(fake.filesRead) != 0 {
		t.Errorf("resolution read %d file(s), want 0: %v", len(fake.filesRead), fake.filesRead)
	}
	for _, dir := range fake.dirsListed {
		if isWithinOrEqual(second.Root(), dir) {
			t.Errorf("resolution listed %q, inside the OTHER collection %q (FR-026)", dir, second.Root())
		}
	}
}

// TestOpenCollection_RefusesAnOrdinaryFolder.
//
// Oracle: FR-020 — a folder with neither marker is not a knowledge base, so
// there is no collection to open.
func TestOpenCollection_RefusesAnOrdinaryFolder(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "Note.md"), "# Note\n")

	if c, err := OpenCollection(root); !errors.Is(err, ErrNotKnowledgeBase) {
		got := "<nil>"
		if c != nil {
			got = c.Root()
		}
		t.Fatalf("OpenCollection(ordinary folder) = %s, %v; want ErrNotKnowledgeBase", got, err)
	}
}

// TestOpenCollection_ObsidianVaultWithNoOmnipusMarker.
//
// Oracle: FR-020 (".obsidian/ alone suffices") together with FR-023 (Omnipus
// never creates .obsidian/ — and, by the same reasoning, must not require its
// own marker to be present before it will read someone's existing vault). The
// display name falls back to the folder's own name, which is not an absolute
// path, so it still survives relocation.
func TestOpenCollection_ObsidianVaultWithNoOmnipusMarker(t *testing.T) {
	root := filepath.Join(t.TempDir(), "My Vault")
	mustMkdir(t, filepath.Join(root, ObsidianMarkerDirName))
	mustWrite(t, filepath.Join(root, "Note.md"), "# Note\n")

	c, err := OpenCollection(root)
	if err != nil {
		t.Fatalf("OpenCollection(obsidian vault): %v", err)
	}
	if c.HasMarker() {
		t.Errorf("HasMarker() = true, want false — Omnipus wrote nothing into this vault")
	}
	if got, want := c.DisplayName(), "My Vault"; got != want {
		t.Errorf("DisplayName() = %q, want %q", got, want)
	}
	// FR-023, restated as a property of opening: reading someone's Obsidian
	// vault must not write anything into it.
	if _, err := os.Stat(MarkerDir(c.Root())); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("opening an Obsidian vault created %s/ (FR-022 is about CREATE, not open)", MarkerDirName)
	}
}

// TestIsWithinOrEqual_SiblingPrefixIsNotContainment.
//
// The containment predicate is tested directly because it is otherwise
// UNREACHABLE from ResolveInside: library.CleanRelPath already refuses every
// traversing or absolute path, so the post-join check there is a backstop that
// no input can currently trigger. A guard nothing can exercise is a guard that
// cannot fail — the pattern docs/internal/false-green-patterns.md warns about —
// so the predicate gets its own falsifiable test.
//
// The case that matters is the sibling prefix: "/a/bc" starts with "/a/b" as a
// string but is not inside it. Writing containment as a bare strings.HasPrefix
// is the classic way to let a link out of the collection (FR-043).
func TestIsWithinOrEqual_SiblingPrefixIsNotContainment(t *testing.T) {
	sep := string(filepath.Separator)
	root := filepath.Join(sep+"a", "b")
	cases := []struct {
		candidate string
		want      bool
	}{
		{root, true},
		{filepath.Join(root, "note.md"), true},
		{filepath.Join(root, "sub", "note.md"), true},
		{filepath.Join(sep+"a", "bc"), false},
		{filepath.Join(sep+"a", "bc", "note.md"), false},
		{filepath.Join(sep + "a"), false},
		{filepath.Join(sep+"a", "b2"), false},
		{sep + "elsewhere", false},
	}
	for _, tc := range cases {
		if got := isWithinOrEqual(root, tc.candidate); got != tc.want {
			t.Errorf("isWithinOrEqual(%q, %q) = %v, want %v", root, tc.candidate, got, tc.want)
		}
	}
}
