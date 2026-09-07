// Tests for the collection walk (ADR-067 FR-033's inventory, FR-039a's
// note/attachment split, and the containment property the walk contributes).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// b2WriteFile creates a file under root, making parents as needed.
func b2WriteFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return abs
}

// b2RelPaths returns the entry paths of a scan, for order-sensitive comparison.
func b2RelPaths(entries []ScanEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.RelPath)
	}
	return out
}

// b2CountingOpen replaces the package's single content-read seam with one that
// records every path opened, and returns a restore function.
//
// This is the read-recording filesystem the spec's tests 14, 19 and 70 all call
// for. It is the only way to assert an ABSENCE of reads: "no error occurred" is
// equally true of an implementation that reads everything.
func b2CountingOpen(t *testing.T) (opened *[]string, restore func()) {
	t.Helper()
	prev := openFileForRead
	var seen []string
	openFileForRead = func(path string) (*os.File, error) {
		seen = append(seen, path)
		return prev(path)
	}
	return &seen, func() { openFileForRead = prev }
}

// TestScan_ClassifiesNotesAndAttachments covers DS-2 rows 1 and 2 (0 notes, 1
// note) plus a mixed collection, and pins the note/attachment split FR-039a
// depends on. .canvas and .base are Obsidian's own formats and are attachments
// here on purpose: this package has no parser for them, and FR-039a forbids
// opening a file we cannot parse.
func TestScan_ClassifiesNotesAndAttachments(t *testing.T) {
	t.Run("empty collection", func(t *testing.T) {
		res, err := Scan(t.TempDir())
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if len(res.Entries) != 0 {
			t.Errorf("Entries = %v, want none (DS-2 row 1: 0 notes, 0 attachments)", b2RelPaths(res.Entries))
		}
	})

	t.Run("single note", func(t *testing.T) {
		root := t.TempDir()
		b2WriteFile(t, root, "Only.md", "hello")
		res, err := Scan(root)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if got := b2RelPaths(res.Entries); len(got) != 1 || got[0] != "Only.md" {
			t.Fatalf("Entries = %v, want [Only.md] (DS-2 row 2)", got)
		}
		if res.Entries[0].Kind != ScanKindNote {
			t.Errorf("Kind = %q, want %q", res.Entries[0].Kind, ScanKindNote)
		}
		if res.Entries[0].Size != int64(len("hello")) {
			t.Errorf("Size = %d, want %d", res.Entries[0].Size, len("hello"))
		}
	})

	t.Run("mixed collection", func(t *testing.T) {
		root := t.TempDir()
		want := map[string]ScanKind{
			"Ordinary Note.md":        ScanKindNote,
			"deep/folder/Nested.md":   ScanKindNote,
			"Ünïcödé — Näme.md":       ScanKindNote,
			".hidden.md":              ScanKindNote,
			"upper/CASE.MARKDOWN":     ScanKindNote,
			"img/diagram-v3.png":      ScanKindAttachment,
			"docs/spec.pdf":           ScanKindAttachment,
			"board.canvas":            ScanKindAttachment,
			"queries/people.base":     ScanKindAttachment,
			"no-extension-at-all":     ScanKindAttachment,
			"archive/notes.md.backup": ScanKindAttachment,
		}
		for rel := range want {
			b2WriteFile(t, root, rel, "x")
		}

		res, err := Scan(root)
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got := map[string]ScanKind{}
		for _, e := range res.Entries {
			got[e.RelPath] = e.Kind
		}
		for rel, kind := range want {
			if got[rel] != kind {
				t.Errorf("%q classified %q, want %q", rel, got[rel], kind)
			}
		}
		if len(got) != len(want) {
			t.Errorf("scanned %d entries, want %d: %v", len(got), len(want), b2RelPaths(res.Entries))
		}
		// DS-3 row 6: a dotfile note is INDEXED (merely hidden in the explorer).
		if got[".hidden.md"] != ScanKindNote {
			t.Error(".hidden.md must be scanned as a note (DS-3 row 6)")
		}
		if n := len(res.Notes()); n != 5 {
			t.Errorf("Notes() = %d, want 5", n)
		}
		if n := len(res.Attachments()); n != 6 {
			t.Errorf("Attachments() = %d, want 6", n)
		}
	})
}

// TestScan_OpensNoFile is the walk's half of FR-039a and of US-4 AS-4
// ("detection has read no file contents"). The walk classifies by extension and
// sizes by Lstat; it never opens anything, note or attachment.
func TestScan_OpensNoFile(t *testing.T) {
	root := t.TempDir()
	b2WriteFile(t, root, "note.md", "text")
	b2WriteFile(t, root, "img/diagram-v3.png", "PNGDATA")

	opened, restore := b2CountingOpen(t)
	defer restore()

	if _, err := Scan(root); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(*opened) != 0 {
		t.Errorf("Scan opened %v; the walk must read zero file contents", *opened)
	}
}

// TestScan_SkipsAndReportsSymlinks covers the three symlink shapes the spec
// names: one escaping the collection, one pointing inside it, and one forming a
// loop. All three are skipped and REPORTED — reported matters, because a
// silently ignored symlink is indistinguishable from a missing file — and none
// is followed, so nothing outside the collection is reachable and the loop
// cannot make the walk diverge.
func TestScan_SkipsAndReportsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	b2WriteFile(t, root, "real.md", "a real note")
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "loopdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "loopdir", "back")); err != nil {
		t.Fatal(err)
	}

	opened, restore := b2CountingOpen(t)
	defer restore()

	done := make(chan *ScanResult, 1)
	errc := make(chan error, 1)
	go func() {
		res, err := Scan(root)
		if err != nil {
			errc <- err
			return
		}
		done <- res
	}()

	var res *ScanResult
	select {
	case res = <-done:
	case err := <-errc:
		t.Fatalf("Scan: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("Scan did not terminate — a followed symlink loop is the only way this happens")
	}

	if got := b2RelPaths(res.Entries); len(got) != 1 || got[0] != "real.md" {
		t.Errorf("Entries = %v, want exactly [real.md]; symlinks must not become entries", got)
	}
	for _, e := range res.Entries {
		if strings.HasPrefix(e.RelPath, "escape/") || strings.HasPrefix(e.RelPath, "loopdir/back/") {
			t.Errorf("entry %q came from a followed symlink", e.RelPath)
		}
	}

	reported := map[string]ScanProblemReason{}
	for _, p := range res.Problems {
		reported[p.RelPath] = p.Reason
	}
	for _, want := range []string{"escape", "alias.md", "loopdir/back"} {
		if reported[want] != ScanProblemSymlink {
			t.Errorf("symlink %q reported as %q, want %q — skipping without reporting hides it",
				want, reported[want], ScanProblemSymlink)
		}
	}
	if len(*opened) != 0 {
		t.Errorf("Scan opened %v; nothing outside the collection may be read", *opened)
	}
}

// TestScan_SkipsMarkerAndToolDirectories: the collection markers are
// configuration, not content, and .git/.trash are tool state. Indexing them
// would fill search with thousands of phantom attachments.
func TestScan_SkipsMarkerAndToolDirectories(t *testing.T) {
	root := t.TempDir()
	b2WriteFile(t, root, "keep.md", "keep me")
	b2WriteFile(t, root, ".obsidian/app.json", "{}")
	b2WriteFile(t, root, ".omnipus-vault/vault.json", "{}")
	b2WriteFile(t, root, ".git/config", "[core]")
	b2WriteFile(t, root, ".trash/deleted.md", "gone")

	res, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got := b2RelPaths(res.Entries); len(got) != 1 || got[0] != "keep.md" {
		t.Errorf("Entries = %v, want exactly [keep.md]", got)
	}
}

// TestScan_OrderIsDeterministic is FR-046's foundation. Two scans of the same
// unchanged collection must produce the same inventory in the same order;
// directory order is not guaranteed by any filesystem, so the sort is what makes
// a rebuild reproducible rather than merely usually-identical.
func TestScan_OrderIsDeterministic(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 60; i++ {
		b2WriteFile(t, root, fmt.Sprintf("dir%02d/note%02d.md", i%7, i), "body")
	}

	first, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	second, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	a, b := b2RelPaths(first.Entries), b2RelPaths(second.Entries)
	if len(a) != 60 {
		t.Fatalf("scanned %d entries, want 60", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("scan order differs at %d: %q vs %q", i, a[i], b[i])
		}
	}
	if !sort.StringsAreSorted(a) {
		t.Errorf("entries are not in path order: %v", a[:10])
	}
}

// TestScan_RootMustBeARealDirectory: a broken mount (US-16 AS-4) must surface as
// an error the caller can show, never as an empty collection — "your notes are
// gone" and "the folder moved" are different messages.
func TestScan_RootMustBeARealDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	if _, err := Scan(missing); err == nil {
		t.Error("Scan of a missing folder returned nil error; a broken mount must be reported")
	}

	file := b2WriteFile(t, t.TempDir(), "a.md", "x")
	if _, err := Scan(file); err == nil {
		t.Error("Scan of a regular file returned nil error")
	}

	if _, err := Scan(""); err == nil {
		t.Error("Scan of an empty root returned nil error")
	}
}

// TestResolveCollectionRoot_CanonicalisesSoTwoMountsAgree is FR-031's precondition:
// the index is keyed by the RESOLVED REAL path, so two mounts that name the same
// folder by different routes must resolve to the same string.
func TestResolveCollectionRoot_CanonicalisesSoTwoMountsAgree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	base := t.TempDir()
	realDir := filepath.Join(base, "vault")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "vault-link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	viaReal, err := ResolveCollectionRoot(realDir)
	if err != nil {
		t.Fatal(err)
	}
	viaLink, err := ResolveCollectionRoot(link)
	if err != nil {
		t.Fatal(err)
	}
	viaSlash, err := ResolveCollectionRoot(realDir + string(filepath.Separator))
	if err != nil {
		t.Fatal(err)
	}

	if viaReal != viaLink {
		t.Errorf("symlinked mount resolves to %q, real mount to %q — they are one corpus and must share one index (FR-031)", viaLink, viaReal)
	}
	if viaReal != viaSlash {
		t.Errorf("trailing separator changed the resolved root: %q vs %q", viaSlash, viaReal)
	}
}
