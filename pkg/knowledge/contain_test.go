// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// contain_test.go — FR-043 (containment), FR-044 (symlinks skipped and
// reported), US-10 (links cannot be used to read outside the collection).
//
// Every test here is written so that the OBVIOUS wrong implementation fails
// it. In particular:
//
//   - The traversal tests count CONTENT READS with a recording filesystem, so
//     an implementation that refuses the link only after opening the target
//     fails even though it returns the right answer. "Reported unresolved" and
//     "never read" are two separate obligations (US-10 AS-1) and only the
//     second one has a security consequence.
//   - The symlink-escape test uses a symlink whose LEXICAL path is perfectly
//     innocent. An implementation that checks containment on the joined string
//     accepts it; only one that re-checks the REAL path refuses it.
//   - The loop test counts ReadDir calls per directory rather than measuring
//     elapsed time. A stopwatch is not a proof that a walk terminates.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// b3RecordingFS wraps a real LinkFS and records every call, so a test can
// assert on COUNTS rather than on the absence of an error.
type b3RecordingFS struct {
	inner LinkFS

	mu       sync.Mutex
	opened   []string
	readDirs []string
	lstats   []string
	evals    []string

	// dirErrors forces ReadDir to fail for a path, so an unreadable directory
	// can be tested without depending on the test process's uid or on chmod
	// semantics that differ between platforms.
	dirErrors map[string]error
}

func b3Recording() *b3RecordingFS {
	return &b3RecordingFS{inner: OSLinkFS(), dirErrors: map[string]error{}}
}

func (f *b3RecordingFS) Lstat(name string) (fs.FileInfo, error) {
	f.mu.Lock()
	f.lstats = append(f.lstats, name)
	f.mu.Unlock()
	return f.inner.Lstat(name)
}

func (f *b3RecordingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	f.mu.Lock()
	f.readDirs = append(f.readDirs, name)
	err := f.dirErrors[name]
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.inner.ReadDir(name)
}

func (f *b3RecordingFS) EvalSymlinks(name string) (string, error) {
	f.mu.Lock()
	f.evals = append(f.evals, name)
	f.mu.Unlock()
	return f.inner.EvalSymlinks(name)
}

func (f *b3RecordingFS) Open(name string) (fs.File, error) {
	f.mu.Lock()
	f.opened = append(f.opened, name)
	f.mu.Unlock()
	return f.inner.Open(name)
}

// openedOutside returns every CONTENT read whose path is not inside root.
func (f *b3RecordingFS) openedOutside(root string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, p := range f.opened {
		if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
			out = append(out, p)
		}
	}
	return out
}

func (f *b3RecordingFS) readDirCount(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.readDirs {
		if p == path {
			n++
		}
	}
	return n
}

func b3WriteNote(t *testing.T, root, rel, body string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return abs
}

func b3Root(t *testing.T, fsys LinkFS, dir string) CollectionRoot {
	t.Helper()
	r, err := NewCollectionRoot(fsys, dir)
	if err != nil {
		t.Fatalf("NewCollectionRoot(%q): %v", dir, err)
	}
	return r
}

// TestResolveLink_ContainmentTraversal is spec test 19 (US-10 AS-1 and AS-2,
// DS-1 rows 7 and 8, FR-042, FR-043).
//
// Four escape spellings, each of which names a file that REALLY EXISTS just
// outside the collection, so "no read happened" is a meaningful claim rather
// than a vacuous one. The positive control matters as much: the in-root note
// must be opened, or a build that opens nothing at all would pass.
func TestResolveLink_ContainmentTraversal(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	// A real secret, immediately outside the collection.
	secret := filepath.Join(parent, "secret.md")
	if err := os.WriteFile(secret, []byte("# PRIVATE KEY\nzarquon-seven\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	b3WriteNote(t, root, "notes/Escapes.md", strings.Join([]string{
		"# Escapes",
		"1. [[../../../.ssh/id_rsa]]",
		"2. [[/etc/passwd]]",
		"3. [plain](../../secret.md)",
		"4. [encoded](..%2F..%2Fsecret.md)",
		"5. [[../secret]]",
		"",
	}, "\n"))
	b3WriteNote(t, root, "notes/Inside.md", "# Inside\nnothing to see\n")

	fake := b3Recording()
	cr := b3Root(t, fake, root)
	g, err := BuildLinkGraph(fake, cr)
	if err != nil {
		t.Fatalf("BuildLinkGraph: %v", err)
	}

	// Every one of the five escapes is reported unresolved (FR-042) with a
	// reason that says it LEFT the collection — not merely "no match", which
	// would read as an ordinary broken link.
	got := map[string]UnresolvedReason{}
	for _, u := range g.Unresolved() {
		got[u.Target] = u.Reason
	}
	// Both the plain and the percent-encoded markdown links carry the same
	// decoded target, which is the point: decoding happens before the
	// containment decision, so an encoded traversal is judged as the path it
	// actually denotes.
	want := map[string]UnresolvedReason{
		"../../../.ssh/id_rsa": ReasonOutsideRoot,
		"/etc/passwd":          ReasonAbsoluteTarget,
		"../../secret.md":      ReasonOutsideRoot,
		"../secret":            ReasonOutsideRoot,
	}
	for target, wantReason := range want {
		if reason, ok := got[target]; !ok {
			t.Errorf("link %q is not reported unresolved at all; unresolved set = %v", target, got)
		} else if reason != wantReason {
			t.Errorf("link %q reported reason %q, want %q", target, reason, wantReason)
		}
	}
	if len(g.Unresolved()) != 5 {
		t.Errorf("expected exactly 5 unresolved links, got %d: %+v", len(g.Unresolved()), g.Unresolved())
	}

	// The security obligation: nothing outside the root was read.
	if outside := fake.openedOutside(cr.Path()); len(outside) != 0 {
		t.Errorf("content was read outside the collection root %q: %v", cr.Path(), outside)
	}
	// Positive control — a test that opens nothing proves nothing.
	openedInside := 0
	fake.mu.Lock()
	for _, p := range fake.opened {
		if strings.HasPrefix(p, cr.Path()+string(filepath.Separator)) {
			openedInside++
		}
	}
	fake.mu.Unlock()
	if openedInside != 2 {
		t.Fatalf("positive control: expected the 2 in-root notes to be opened, got %d opens inside the root", openedInside)
	}
}

// TestResolveContained_SymlinkEscapeIsRealNotLexical is the containment test a
// lexical implementation cannot pass (FR-043: "checked against the REAL path
// after symlink resolution").
//
// "escape/secret.md" is a perfectly ordinary-looking collection-relative path.
// filepath.Join produces something inside the root. Only resolving the symlink
// reveals that it is not.
func TestResolveContained_SymlinkEscapeIsRealNotLexical(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows; the POSIX guarantee is what is being asserted")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("mkdir vault: %v", err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("zarquon-seven"), 0o600); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	b3WriteNote(t, root, "Real.md", "# Real")

	fake := b3Recording()
	cr := b3Root(t, fake, root)

	if _, err := cr.ResolveContained(fake, "escape/secret.md"); !errors.Is(err, ErrOutsideCollection) {
		t.Fatalf("a path through a symlinked directory must be refused as outside the collection; got err=%v", err)
	}
	// Positive control: an ordinary nested path IS resolved, so "refuses
	// everything" cannot pass.
	resolved, err := cr.ResolveContained(fake, "Real.md")
	if err != nil {
		t.Fatalf("positive control: an ordinary in-root path must resolve; got %v", err)
	}
	if want := filepath.Join(cr.Path(), "Real.md"); resolved != want {
		t.Errorf("ResolveContained(Real.md) = %q, want %q", resolved, want)
	}
	if outsideReads := fake.openedOutside(cr.Path()); len(outsideReads) != 0 {
		t.Errorf("refusing a symlink escape must not read anything outside the root: %v", outsideReads)
	}
}

// TestWalk_SymlinkSkippedAndReported is spec test 20 (US-10 AS-3, FR-044).
//
// Three symlinks — one to a file outside, one to a directory outside, one to a
// file INSIDE. All three are skipped and reported. The inside one is the case
// that separates "skip symlinks" from "skip escapes": FR-044 skips the link
// itself, not merely the ones that escape, which is what makes loop detection
// unnecessary.
func TestWalk_SymlinkSkippedAndReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("zarquon-seven"), 0o600); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}
	b3WriteNote(t, root, "Real.md", "# Real")

	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escapedir")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "Real.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatalf("symlink inside: %v", err)
	}

	fake := b3Recording()
	cr := b3Root(t, fake, root)
	res, err := WalkContained(fake, cr)
	if err != nil {
		t.Fatalf("WalkContained: %v", err)
	}

	if len(res.Files) != 1 || res.Files[0] != "Real.md" {
		t.Errorf("walk returned %v, want exactly [Real.md] — a symlink is never a file of the collection", res.Files)
	}
	reported := map[string]SkipReason{}
	for _, s := range res.Skipped {
		reported[s.RelPath] = s.Reason
	}
	for _, name := range []string{"escape.md", "escapedir", "alias.md"} {
		if got, ok := reported[name]; !ok {
			t.Errorf("symlink %q was skipped silently; FR-044 requires it to be REPORTED. skipped=%v", name, res.Skipped)
		} else if got != SkipSymlink {
			t.Errorf("symlink %q reported as %q, want %q", name, got, SkipSymlink)
		}
	}
	if len(res.Skipped) != 3 {
		t.Errorf("expected exactly 3 reported skips, got %d: %+v", len(res.Skipped), res.Skipped)
	}
	// Nothing outside was listed or read.
	fake.mu.Lock()
	dirs := append([]string(nil), fake.readDirs...)
	fake.mu.Unlock()
	for _, d := range dirs {
		if d != cr.Path() && !strings.HasPrefix(d, cr.Path()+string(filepath.Separator)) {
			t.Errorf("walk listed a directory outside the collection: %q", d)
		}
	}
	if outsideReads := fake.openedOutside(cr.Path()); len(outsideReads) != 0 {
		t.Errorf("walk read content outside the collection: %v", outsideReads)
	}
}

// TestWalk_SymlinkLoopTerminates is spec test 21 (US-10 AS-4).
//
// The oracle is a COUNT, not a clock: each real directory must be listed
// exactly once. An implementation that followed the loop would list the root
// again (and again), so the count fails long before any timeout would.
func TestWalk_SymlinkLoopTerminates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}
	root := t.TempDir()
	b3WriteNote(t, root, "Real.md", "# Real")
	if err := os.MkdirAll(filepath.Join(root, "loopdir"), 0o700); err != nil {
		t.Fatalf("mkdir loopdir: %v", err)
	}
	// The classic cycle: a directory inside the collection linking back to it.
	if err := os.Symlink(root, filepath.Join(root, "loopdir", "back")); err != nil {
		t.Fatalf("symlink loop: %v", err)
	}

	fake := b3Recording()
	cr := b3Root(t, fake, root)
	res, err := WalkContained(fake, cr)
	if err != nil {
		t.Fatalf("WalkContained: %v", err)
	}

	if got := fake.readDirCount(cr.Path()); got != 1 {
		t.Errorf("the collection root was listed %d times; a terminating walk lists each real directory exactly once", got)
	}
	if got := fake.readDirCount(filepath.Join(cr.Path(), "loopdir")); got != 1 {
		t.Errorf("loopdir was listed %d times, want 1", got)
	}
	if len(res.Files) != 1 || res.Files[0] != "Real.md" {
		t.Errorf("files = %v, want [Real.md]", res.Files)
	}
	var loop *SkippedEntry
	for i := range res.Skipped {
		if res.Skipped[i].RelPath == "loopdir/back" {
			loop = &res.Skipped[i]
		}
	}
	if loop == nil {
		t.Fatalf("the loop was not reported; skipped=%+v", res.Skipped)
	}
	if loop.Reason != SkipSymlink {
		t.Errorf("loop reported as %q, want %q", loop.Reason, SkipSymlink)
	}
}

// TestWalkContained_UnreadableDirectoryIsReportedNotDropped covers NB-9: an
// exclusion the indexer cannot help must still be visible. A forced ReadDir
// error is used rather than chmod, which behaves differently for root and on
// different filesystems.
func TestWalkContained_UnreadableDirectoryIsReportedNotDropped(t *testing.T) {
	root := t.TempDir()
	b3WriteNote(t, root, "Readable.md", "# Readable")
	b3WriteNote(t, root, "locked/Hidden.md", "# Hidden")

	fake := b3Recording()
	cr := b3Root(t, fake, root)
	fake.dirErrors[filepath.Join(cr.Path(), "locked")] = fmt.Errorf("permission denied")

	res, err := WalkContained(fake, cr)
	if err != nil {
		t.Fatalf("an unreadable SUBdirectory must not fail the whole walk: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0] != "Readable.md" {
		t.Errorf("files = %v, want [Readable.md]", res.Files)
	}
	found := false
	for _, s := range res.Skipped {
		if s.RelPath == "locked" && s.Reason == SkipUnreadable {
			found = true
			if !strings.Contains(s.Detail, "permission denied") {
				t.Errorf("skip detail %q does not carry the underlying cause", s.Detail)
			}
		}
	}
	if !found {
		t.Errorf("unreadable directory was dropped silently; skipped=%+v", res.Skipped)
	}
}

// TestIsAbsoluteTarget_DriveLetterDoesNotSwallowAColonInAFilename guards the
// exact collision between two requirements: absolute-path links are refused
// (US-10 AS-2) while a note legitimately named "Meeting: 2026-01-01.md"
// remains addressable (DS-3 row 2, Stage 0).
func TestIsAbsoluteTarget_DriveLetterDoesNotSwallowAColonInAFilename(t *testing.T) {
	cases := []struct {
		target string
		want   bool
		why    string
	}{
		{"/etc/passwd", true, "POSIX absolute"},
		{"/", true, "the filesystem root itself"},
		{`\\server\share\x`, true, "UNC path"},
		{`C:\Users\me\.ssh\id_rsa`, true, "Windows drive with backslash"},
		{"C:/Users/me", true, "Windows drive with forward slash"},
		{"C:", true, "bare drive"},
		{"Meeting: 2026-01-01", false, "a colon in an operator's own filename (DS-3 row 2)"},
		{"Notes: draft/Target", false, "a colon before a slash is still a filename"},
		// The sharp cases: a one-letter word followed by a colon has EXACTLY
		// the shape of a drive reference, and only the separator requirement
		// tells them apart. Without it these notes become unaddressable —
		// silently, and only for operators who name notes this way.
		{"Q: what do we do now", false, "one-letter word + colon is not a drive"},
		{"C: notes from the call", false, "a real drive letter, but no separator after the colon"},
		{"C:notes", false, "no separator at all after the colon"},
		{"folder/Target", false, "an ordinary collection-relative path"},
		{"Ünïcödé — Näme", false, "unicode filename (DS-3 row 5)"},
		{"../escape", false, "traversal is refused by containment, not by this predicate"},
		{"", false, "empty is not absolute"},
	}
	for _, tc := range cases {
		if got := IsAbsoluteTarget(tc.target); got != tc.want {
			t.Errorf("IsAbsoluteTarget(%q) = %v, want %v (%s)", tc.target, got, tc.want, tc.why)
		}
	}
}

// TestCollectionRoot_ContainsRejectsSiblingPrefix guards the classic
// containment bug: a prefix test with no separator lets "/vault-backup" pass
// as though it were inside "/vault".
func TestCollectionRoot_ContainsRejectsSiblingPrefix(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	sibling := filepath.Join(parent, "vault-backup")
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	fake := b3Recording()
	cr := b3Root(t, fake, root)

	if cr.Contains(filepath.Join(cr.Path(), "note.md")) != true {
		t.Errorf("a file inside the root must be contained")
	}
	if cr.Contains(cr.Path()) != true {
		t.Errorf("the root itself must be contained")
	}
	realSibling, err := filepath.EvalSymlinks(sibling)
	if err != nil {
		t.Fatalf("EvalSymlinks(sibling): %v", err)
	}
	if cr.Contains(filepath.Join(realSibling, "note.md")) {
		t.Errorf("%q must NOT be contained by %q — sharing a name prefix is not containment", realSibling, cr.Path())
	}
	var zero CollectionRoot
	if zero.Contains("/anything") {
		t.Errorf("a zero CollectionRoot must contain nothing at all, never behave like /")
	}
}

// TestNewCollectionRoot_RefusesWhatIsNotACollectionRoot covers the
// construction-time guarantees the rest of the file depends on.
func TestNewCollectionRoot_RefusesWhatIsNotACollectionRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file.md")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	fake := b3Recording()
	for _, tc := range []struct{ name, path string }{
		{"empty", ""},
		{"relative", "notes"},
		{"missing", filepath.Join(dir, "no-such-dir")},
		{"a file, not a directory", file},
	} {
		if _, err := NewCollectionRoot(fake, tc.path); !errors.Is(err, ErrCollectionRootInvalid) {
			t.Errorf("NewCollectionRoot(%s=%q) err = %v, want ErrCollectionRootInvalid", tc.name, tc.path, err)
		}
	}
}

// TestWalkContained_IsDeterministic covers US-11: the same tree must produce
// the same ordering every run, on any machine. Filesystem listing order is not
// guaranteed, so this is a property of the walker, not of the disk.
func TestWalkContained_IsDeterministic(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		b3WriteNote(t, root, fmt.Sprintf("folder-%02d/Note-%02d.md", i%5, i), "# n")
	}
	fake := b3Recording()
	cr := b3Root(t, fake, root)

	first, err := WalkContained(fake, cr)
	if err != nil {
		t.Fatalf("walk 1: %v", err)
	}
	second, err := WalkContained(fake, cr)
	if err != nil {
		t.Fatalf("walk 2: %v", err)
	}
	if len(first.Files) != 40 {
		t.Fatalf("expected 40 files, got %d", len(first.Files))
	}
	if strings.Join(first.Files, "\n") != strings.Join(second.Files, "\n") {
		t.Errorf("two walks of the same tree disagreed on order")
	}
	if !sort.StringsAreSorted(first.Files) {
		t.Errorf("walk results are not sorted: %v", first.Files)
	}
}
