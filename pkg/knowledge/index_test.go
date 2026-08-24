// Tests for the knowledge base index (ADR-067 FR-030 … FR-034a, FR-039,
// FR-039a, FR-046).
//
// Oracles come from the spec, never from the implementation:
//
//	FR-030  index stored OUTSIDE the collection, under $OMNIPUS_HOME
//	FR-031  keyed by the collection root's resolved real path, reference counted
//	FR-032  index directories 0700, index files 0600
//	FR-033  re-parse only files whose size, mtime or content hash changed
//	FR-034  bounded-memory batches, never one whole-collection batch
//	FR-034a no maximum note size; a note over 8 MB becomes consecutive segments
//	        carrying absolute byte offsets, and segment hits collapse to ONE result
//	FR-039  the index persists across restarts without rebuilding
//	FR-039a attachments indexed by filename and path only, never opened
//	FR-046  identical query answers after a rebuild from unchanged files
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
)

// b2Open opens an index and closes it when the test ends.
func b2Open(t *testing.T, home, root string) *Index {
	t.Helper()
	ix, err := OpenIndex(home, root)
	if err != nil {
		t.Fatalf("OpenIndex(%q, %q): %v", home, root, err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	return ix
}

// b2Sync runs a reconcile and fails the test on error.
func b2Sync(t *testing.T, ix *Index) SyncStats {
	t.Helper()
	stats, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return stats
}

// b2TreeSnapshot lists every path under root with its size — the before/after
// comparison test 22 calls for.
func b2TreeSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			out = append(out, "d "+filepath.ToSlash(rel))
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		out = append(out, fmt.Sprintf("f %s %d", filepath.ToSlash(rel), info.Size()))
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// b2HitPaths returns the result paths in rank order.
func b2HitPaths(hits []IndexHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out
}

// ---------------------------------------------------------------------------
// Engine guard — the same property pkg/memrooms/index pins, for the same reason
// (hard constraint #2: pure Go, no CGo).
// ---------------------------------------------------------------------------

func TestIndex_UsesPureGoScorchBackend(t *testing.T) {
	if scorch.Name != "scorch" {
		t.Fatalf("scorch.Name = %q, want \"scorch\"; openOrCreateBleve passes it to bleve.NewUsing", scorch.Name)
	}
	if bleve.Config.DefaultIndexType != scorch.Name {
		t.Errorf("bleve default index type = %q, want scorch (a CGo backend would break the pure-Go constraint)",
			bleve.Config.DefaultIndexType)
	}
	for _, cgoKV := range []string{"leveldb", "rocksdb", "goleveldb"} {
		if bleve.Config.DefaultKVStore == cgoKV {
			t.Errorf("bleve DefaultKVStore = %q, a CGo-backed store", cgoKV)
		}
	}

	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "the scorch backend is pure Go and needs no CGo")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, err := ix.Search("scorch", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "note.md" {
		t.Fatalf("Search = %v, want exactly [note.md]", b2HitPaths(hits))
	}
	if hits[0].Score <= 0 {
		t.Errorf("Score = %v, want a positive BM25 score", hits[0].Score)
	}
}

// ---------------------------------------------------------------------------
// FR-030 — the index lives outside the operator's folder
// ---------------------------------------------------------------------------

// TestIndexLocation_OutsideCollection is spec test 22. The oracle is a
// before/after tree diff of the COLLECTION: indexing must leave it byte-for-byte
// as it was. Asserting only that the index directory exists under $OMNIPUS_HOME
// would pass an implementation that writes in both places.
func TestIndexLocation_OutsideCollection(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "alpha content")
	b2WriteFile(t, root, "sub/b.md", "bravo content")
	b2WriteFile(t, root, "img/diagram-v3.png", "PNGDATA")

	before := b2TreeSnapshot(t, root)

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if stats.Indexed != 3 {
		t.Fatalf("Indexed = %d, want 3", stats.Indexed)
	}
	if _, err := ix.Search("alpha", 5); err != nil {
		t.Fatalf("Search: %v", err)
	}

	after := b2TreeSnapshot(t, root)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("indexing changed the collection (FR-030 — the operator's folder is theirs)\nbefore:\n%s\nafter:\n%s",
			strings.Join(before, "\n"), strings.Join(after, "\n"))
	}

	realRoot, err := ResolveCollectionRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if within(realRoot, ix.Dir()) {
		t.Errorf("index dir %q is inside the collection %q", ix.Dir(), realRoot)
	}
	wantPrefix := filepath.Join(home, indexHomeSubdir)
	if !within(wantPrefix, ix.Dir()) {
		t.Errorf("index dir %q is not under $OMNIPUS_HOME/%s", ix.Dir(), indexHomeSubdir)
	}
	if entries, readErr := os.ReadDir(ix.Dir()); readErr != nil || len(entries) == 0 {
		t.Errorf("index dir %q is empty (err=%v) — the index has to be SOMEWHERE", ix.Dir(), readErr)
	}
	if _, statErr := os.Stat(ix.ManifestPath()); statErr != nil {
		t.Errorf("manifest not written beside the index: %v", statErr)
	}
}

// within reports whether candidate is at or below parent.
func within(parent, candidate string) bool {
	rel, err := filepath.Rel(parent, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// ---------------------------------------------------------------------------
// FR-032 — permissions
// ---------------------------------------------------------------------------

// TestIndexPermissions_0700_0600 is spec test 23. The index holds the full text
// of every note, so the modes are asserted over EVERY file and directory, not
// just the ones we create ourselves: bleve writes index_meta.json 0666&umask,
// which is exactly the file a spot-check of "the directory we made" would miss.
func TestIndexPermissions_0700_0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 5; i++ {
		b2WriteFile(t, root, fmt.Sprintf("note%d.md", i), "confidential note body")
	}
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	checked := 0
	err := filepath.WalkDir(ix.Dir(), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		checked++
		want := fs.FileMode(0o600)
		what := "file"
		if d.IsDir() {
			want, what = 0o700, "dir"
		}
		if got := info.Mode().Perm(); got != want {
			rel, _ := filepath.Rel(ix.Dir(), path)
			t.Errorf("%s %q mode = %04o, want %04o (FR-032)", what, rel, got, want)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk index dir: %v", err)
	}
	if checked < 3 {
		t.Fatalf("only %d index paths inspected — an empty walk would pass vacuously", checked)
	}
}

// ---------------------------------------------------------------------------
// FR-031 — one corpus, one index, reference counted
// ---------------------------------------------------------------------------

// TestIndexIdentity_SharedByRealpath is spec test 24 and the lifecycle scenario
// "revoking one of two mounts does not destroy the shared index".
//
// Both halves are required. Sharing alone would be satisfied by never closing
// anything; reference counting alone would be satisfied by two separate indexes
// that each close cleanly — and two indexes over one corpus is the bug (double
// the memory, double the reconcile, and a scorch bolt lock that deadlocks).
func TestIndexIdentity_SharedByRealpath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	home := t.TempDir()
	base := t.TempDir()
	realDir := filepath.Join(base, "vault")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	b2WriteFile(t, realDir, "shared.md", "zarquon seven lives here")

	link := filepath.Join(base, "vault-via-symlink")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}

	realRoot, err := ResolveCollectionRoot(realDir)
	if err != nil {
		t.Fatal(err)
	}

	first, err := OpenIndex(home, realDir)
	if err != nil {
		t.Fatalf("OpenIndex(real): %v", err)
	}
	second, err := OpenIndex(home, link)
	if err != nil {
		t.Fatalf("OpenIndex(symlink): %v", err)
	}

	if first != second {
		t.Errorf("two mounts of one folder returned different handles (%p vs %p) — one corpus gets one index (FR-031)", first, second)
	}
	if first.Dir() != second.Dir() {
		t.Errorf("two mounts produced two index directories: %q vs %q", first.Dir(), second.Dir())
	}
	if refs := indexRegistryRefs(realRoot); refs != 2 {
		t.Errorf("refs = %d after two opens, want 2", refs)
	}

	b2Sync(t, first)

	// Workspace A revokes its mount. Workspace B must still be able to search.
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("first Close: %v", closeErr)
	}
	if refs := indexRegistryRefs(realRoot); refs != 1 {
		t.Errorf("refs = %d after one release, want 1", refs)
	}
	hits, err := second.Search("zarquon", 5)
	if err != nil {
		t.Fatalf("search after the other mount was revoked: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "shared.md" {
		t.Errorf("search after revoking one mount = %v, want [shared.md]", b2HitPaths(hits))
	}

	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("second Close: %v", closeErr)
	}
	if refs := indexRegistryRefs(realRoot); refs != 0 {
		t.Errorf("refs = %d after the last release, want 0 (the handle must actually close)", refs)
	}
	// A third open after everyone let go must work — proving the handle really
	// was closed rather than leaked (a leaked bolt lock would hang or error).
	third, err := OpenIndex(home, realDir)
	if err != nil {
		t.Fatalf("reopen after full release: %v", err)
	}
	defer func() { _ = third.Close() }()
	if hits, err = third.Search("zarquon", 5); err != nil || len(hits) != 1 {
		t.Errorf("reopened index search = %v (err=%v), want [shared.md] with no rebuild", b2HitPaths(hits), err)
	}
}

// TestIndexDirFor_DistinctCollectionsDistinctDirs: two different folders must
// never share an index directory, or one collection's search would answer with
// another's notes — the isolation failure US-9 is about, arriving through the
// back door.
func TestIndexDirFor_DistinctCollectionsDistinctDirs(t *testing.T) {
	home := t.TempDir()
	a, b := t.TempDir(), t.TempDir()

	dirA, err := IndexDirFor(home, a)
	if err != nil {
		t.Fatal(err)
	}
	dirB, err := IndexDirFor(home, b)
	if err != nil {
		t.Fatal(err)
	}
	if dirA == dirB {
		t.Fatalf("two collections share index dir %q", dirA)
	}
	again, err := IndexDirFor(home, a)
	if err != nil {
		t.Fatal(err)
	}
	if again != dirA {
		t.Errorf("IndexDirFor is not stable: %q then %q", dirA, again)
	}
	if _, err := IndexDirFor("", a); err == nil {
		t.Error("IndexDirFor with an empty home returned nil error; there is nowhere to put the index")
	}
}

// ---------------------------------------------------------------------------
// FR-033 / FR-039 — incremental, and persistent across restarts
// ---------------------------------------------------------------------------

// TestManifest_ReparsesOnlyChangedFiles is spec test 25, extended with FR-039's
// restart. Every assertion is a COUNT: "no error" would pass an implementation
// that re-parses the entire collection on every open, which is the startup
// penalty ADR-067 §1.2 exists to avoid.
func TestManifest_ReparsesOnlyChangedFiles(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	for i := 0; i < 12; i++ {
		b2WriteFile(t, root, fmt.Sprintf("n%02d.md", i), fmt.Sprintf("note number %d about alpha", i))
	}
	b2WriteFile(t, root, "img/diagram-v3.png", "PNGDATA")

	ix := b2Open(t, home, root)

	first := b2Sync(t, ix)
	if first.Indexed != 13 || first.Unchanged != 0 {
		t.Fatalf("first sync: Indexed=%d Unchanged=%d, want 13 and 0", first.Indexed, first.Unchanged)
	}

	second := b2Sync(t, ix)
	if second.Indexed != 0 || second.Unchanged != 13 {
		t.Errorf("unchanged reconcile: Indexed=%d Unchanged=%d, want 0 and 13 (FR-033)", second.Indexed, second.Unchanged)
	}

	// Edit exactly one note externally.
	b2WriteFile(t, root, "n05.md", "note number 5 now mentions zarquon instead")
	third := b2Sync(t, ix)
	if third.Indexed != 1 || third.Unchanged != 12 {
		t.Errorf("after editing one file: Indexed=%d Unchanged=%d, want 1 and 12 (AC-4.3: exactly one file re-parsed)",
			third.Indexed, third.Unchanged)
	}
	hits, err := ix.Search("zarquon", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "n05.md" {
		t.Errorf("edited note search = %v, want [n05.md] — an incremental index that misses the edit is worse than a slow one", b2HitPaths(hits))
	}

	// Delete a note: its documents must go with it.
	if rmErr := os.Remove(filepath.Join(root, "n07.md")); rmErr != nil {
		t.Fatal(rmErr)
	}
	fourth := b2Sync(t, ix)
	if fourth.Removed != 1 || fourth.Indexed != 0 {
		t.Errorf("after deleting one file: Removed=%d Indexed=%d, want 1 and 0", fourth.Removed, fourth.Indexed)
	}
	if hits, err = ix.Search("number 7", 10); err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Path == "n07.md" {
			t.Error("a deleted note is still in the index")
		}
	}

	// FR-039: close the index (a restart) and reopen. Nothing may be rebuilt.
	docsBefore, err := ix.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := ix.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	reopened := b2Open(t, home, root)
	docsAfter, err := reopened.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if docsAfter != docsBefore {
		t.Errorf("doc count after restart = %d, want %d — the index must persist, not rebuild (FR-039)", docsAfter, docsBefore)
	}
	restart := b2Sync(t, reopened)
	if restart.Indexed != 0 || restart.Unchanged != 12 {
		t.Errorf("reconcile after restart: Indexed=%d Unchanged=%d, want 0 and 12 (FR-039: no full rebuild)",
			restart.Indexed, restart.Unchanged)
	}
	if hits, err = reopened.Search("zarquon", 5); err != nil || len(hits) != 1 {
		t.Errorf("search after restart = %v (err=%v), want [n05.md]", b2HitPaths(hits), err)
	}
}

// TestIndex_DeepReconcileUsesTheContentHash pins FR-033's THIRD criterion, the
// one size and mtime cannot see: a file rewritten with different bytes and its
// modification time restored. A stat-only check calls it unchanged forever.
//
// Both directions are asserted. Without the identical-bytes half, an
// implementation that simply re-parses everything in deep mode would pass.
func TestIndex_DeepReconcileUsesTheContentHash(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	same := b2WriteFile(t, root, "same.md", "alpha bravo charlie")
	changed := b2WriteFile(t, root, "changed.md", "delta echo foxtrot")
	// NOTE: the replacement below is the SAME LENGTH on purpose — the point of
	// this test is a change that size and mtime cannot see.

	ix := b2Open(t, home, root)
	if s := b2Sync(t, ix); s.Indexed != 2 {
		t.Fatalf("first sync Indexed=%d, want 2", s.Indexed)
	}

	sameStat, err := os.Stat(same)
	if err != nil {
		t.Fatal(err)
	}
	changedStat, err := os.Stat(changed)
	if err != nil {
		t.Fatal(err)
	}

	// Rewrite both, keeping size and mtime identical: one with the same bytes,
	// one with different bytes.
	if wErr := os.WriteFile(same, []byte("alpha bravo charlie"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(changed, []byte("delta echo zarquon"), 0o600); wErr != nil { // same length
		t.Fatal(wErr)
	}
	if tErr := os.Chtimes(same, sameStat.ModTime(), sameStat.ModTime()); tErr != nil {
		t.Fatal(tErr)
	}
	if tErr := os.Chtimes(changed, changedStat.ModTime(), changedStat.ModTime()); tErr != nil {
		t.Fatal(tErr)
	}

	// The cheap check cannot see either of them.
	if s := b2Sync(t, ix); s.Indexed != 0 || s.Unchanged != 2 {
		t.Fatalf("stat-only reconcile: Indexed=%d Unchanged=%d, want 0 and 2", s.Indexed, s.Unchanged)
	}

	deep, err := ix.SyncWith(context.Background(), SyncOptions{Deep: true})
	if err != nil {
		t.Fatalf("deep Sync: %v", err)
	}
	if deep.Indexed != 1 {
		t.Errorf("deep reconcile Indexed=%d, want exactly 1 — only the file whose CONTENT changed", deep.Indexed)
	}
	if deep.Unchanged != 1 {
		t.Errorf("deep reconcile Unchanged=%d, want 1 — identical bytes must not be re-parsed", deep.Unchanged)
	}
	hits, err := ix.Search("zarquon", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "changed.md" {
		t.Errorf("search after deep reconcile = %v, want [changed.md]", b2HitPaths(hits))
	}
}

// TestIndex_DeepReconcileNeverOpensAnAttachment: FR-039a has no verification
// exception. A deep drift check reads notes; it must still never open an
// attachment.
func TestIndex_DeepReconcileNeverOpensAnAttachment(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "a note")
	b2WriteFile(t, root, "img/diagram-v3.png", "PNGDATA")

	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	opened, restore := b2CountingOpen(t)
	defer restore()

	if _, err := ix.SyncWith(context.Background(), SyncOptions{Deep: true}); err != nil {
		t.Fatalf("deep Sync: %v", err)
	}
	for _, p := range *opened {
		if strings.HasSuffix(p, "diagram-v3.png") {
			t.Errorf("deep reconcile opened the attachment %q (FR-039a)", p)
		}
	}
}

// ---------------------------------------------------------------------------
// FR-039a — attachments by name and path only
// ---------------------------------------------------------------------------

// TestAttachments_IndexedByNameNeverRead is spec test 70. BOTH halves are
// required and the spec says so: an implementation that skips attachments
// entirely passes the "zero reads" half, and one that indexes their contents
// passes the "findable" half. Only together do they describe FR-039a.
func TestAttachments_IndexedByNameNeverRead(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "notes/architecture.md", "the architecture note mentions nothing unusual")
	// The attachment's CONTENTS carry a word that appears nowhere else. If the
	// indexer ever reads it, that word becomes searchable — a second, independent
	// witness to a read that the counter alone might miss.
	b2WriteFile(t, root, "img/diagram-v3.png", "\x89PNG\r\n\x1a\n zarquonsecret")
	b2WriteFile(t, root, "docs/quarterly report.pdf", "%PDF-1.7 zarquonsecret")

	ix := b2Open(t, home, root)

	opened, restore := b2CountingOpen(t)
	defer restore()

	stats := b2Sync(t, ix)
	if stats.Indexed != 3 {
		t.Fatalf("Indexed = %d, want 3 (attachments are indexed, not skipped)", stats.Indexed)
	}

	for _, p := range *opened {
		if strings.Contains(p, "diagram-v3.png") || strings.Contains(p, "quarterly report.pdf") {
			t.Errorf("the indexer opened attachment %q — FR-039a/MV-19 allow zero content reads", p)
		}
	}
	if len(*opened) != 1 {
		t.Errorf("opened %v; exactly one file (the note) may be read", *opened)
	}

	// Findable by name.
	hits, err := ix.Search("diagram-v3", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(hits, "img/diagram-v3.png") {
		t.Errorf("searching \"diagram-v3\" = %v, want it to find img/diagram-v3.png", b2HitPaths(hits))
	}
	if hits[0].Path == "img/diagram-v3.png" && hits[0].Kind != ScanKindAttachment {
		t.Errorf("attachment hit Kind = %q, want %q", hits[0].Kind, ScanKindAttachment)
	}

	// Findable by a word from its folder path.
	if hits, err = ix.Search("quarterly", 10); err != nil {
		t.Fatal(err)
	}
	if !containsPath(hits, "docs/quarterly report.pdf") {
		t.Errorf("searching \"quarterly\" = %v, want docs/quarterly report.pdf", b2HitPaths(hits))
	}

	// NOT findable by its contents — because its contents were never read.
	if hits, err = ix.Search("zarquonsecret", 10); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("searching a word that exists only INSIDE an attachment returned %v; its bytes must never be indexed",
			b2HitPaths(hits))
	}
}

func containsPath(hits []IndexHit, want string) bool {
	for _, h := range hits {
		if h.Path == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// FR-034 — bounded batches
// ---------------------------------------------------------------------------

// TestIndex_BoundedBatchesNeverOneWholeCollectionBatch pins FR-034. The oracle
// is the number of committed batches: pkg/memrooms/index's rebuildLocked — the
// precedent being copied — accumulates EVERY document into one batch and commits
// once, which is the shape this requirement forbids.
func TestIndex_BoundedBatchesNeverOneWholeCollectionBatch(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	const notes = indexBatchMaxDocs*5 + 7
	for i := 0; i < notes; i++ {
		b2WriteFile(t, root, fmt.Sprintf("d%02d/n%04d.md", i%13, i), fmt.Sprintf("note %d about alpha bravo charlie", i))
	}

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)

	if stats.Indexed != notes {
		t.Fatalf("Indexed = %d, want %d", stats.Indexed, notes)
	}
	wantMin := notes / indexBatchMaxDocs
	if stats.BatchCommits < wantMin {
		t.Errorf("BatchCommits = %d for %d documents at a bound of %d per batch, want at least %d — one commit means one whole-collection batch (FR-034)",
			stats.BatchCommits, notes, indexBatchMaxDocs, wantMin)
	}
	if stats.BatchCommits <= 1 {
		t.Errorf("BatchCommits = %d; a single batch over the whole collection is exactly what FR-034 forbids", stats.BatchCommits)
	}

	// Bounded batching must not lose documents at the boundaries.
	for _, i := range []int{0, indexBatchMaxDocs - 1, indexBatchMaxDocs, notes - 1} {
		q := fmt.Sprintf("note %d", i)
		hits, err := ix.Search(q, 5)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(hits) == 0 {
			t.Errorf("note %d is missing from the index — a batch boundary dropped it", i)
		}
	}
}

// ---------------------------------------------------------------------------
// FR-034a — segmentation, and one result per note
// ---------------------------------------------------------------------------

// b2SharedSegmentTerm is written alongside EVERY marker, so that one term is
// present in several different segments of the same note. Spec test 101 is about
// exactly that case: without a term that spans segments, a search returns one
// segment hit and an implementation that never collapses anything passes.
const b2SharedSegmentTerm = "zarquonspansegments"

// b2WriteSegmentedNote writes a note of at least minBytes, placing each marker
// (plus b2SharedSegmentTerm) at roughly its fraction of the file, and returns the
// byte offsets where the marker lines start.
func b2WriteSegmentedNote(t *testing.T, path string, minBytes int, markers map[float64]string) map[string]int64 {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	fractions := make([]float64, 0, len(markers))
	for frac := range markers {
		fractions = append(fractions, frac)
	}
	sort.Float64s(fractions)

	w := bufio.NewWriterSize(f, 1<<20)
	filler := "alpha bravo charlie delta echo foxtrot golf hotel india juliet\n"
	offsets := make(map[string]int64, len(markers))
	var written int64
	next := 0
	for written < int64(minBytes) || next < len(fractions) {
		if next < len(fractions) && written >= int64(float64(minBytes)*fractions[next]) {
			marker := markers[fractions[next]]
			offsets[marker] = written
			// The FIRST marker's segment carries the spanning term several
			// times, so its BM25 score is strictly higher than the others'.
			// Without that, every segment scores identically and "the collapsed
			// hit carries the BEST segment's score" is unfalsifiable — an
			// implementation that keeps the last segment seen passes.
			reps := 1
			if next == 0 {
				reps = 9
			}
			n, wErr := w.WriteString(marker + strings.Repeat(" "+b2SharedSegmentTerm, reps) + "\n")
			if wErr != nil {
				t.Fatal(wErr)
			}
			written += int64(n)
			next++
			continue
		}
		n, wErr := w.WriteString(filler)
		if wErr != nil {
			t.Fatal(wErr)
		}
		written += int64(n)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return offsets
}

// TestIndex_SegmentedNoteCollapsesToOneHit is spec test 101 and the single most
// important assertion in this unit.
//
// A note larger than IndexSegmentSize becomes several index DOCUMENTS. That is
// how bounded memory is achieved (FR-034a) — and it is invisible to the caller
// only if search collapses those documents back into one result. The naive
// implementation returns one row per segment: three rows for one note, ranked as
// three notes, with the note's true relevance split three ways.
//
// The oracle is therefore "exactly one", never "at least one".
func TestIndex_SegmentedNoteCollapsesToOneHit(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()

	// Over FR-034a's stated 8 MB threshold, so the requirement's own words
	// ("a note over 8 MB is indexed as consecutive segments") apply literally.
	const size = IndexSegmentThreshold + (1 << 20)
	offsets := b2WriteSegmentedNote(t, filepath.Join(root, "huge.md"), size, map[float64]string{
		0.02: "zarquonopening",
		0.50: "zarquonmiddle",
		0.98: "zarquonclosing",
	})
	// A second, ordinary note carrying the same spanning term, so "exactly one
	// result for huge.md" is a real collapse rather than an artefact of there
	// being only one file in the collection.
	b2WriteFile(t, root, "small.md", "a short note that also says "+b2SharedSegmentTerm+" once")

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)

	if stats.Indexed != 2 {
		t.Fatalf("Indexed = %d, want 2", stats.Indexed)
	}
	// FR-034a's literal claim: a note OVER 8 MB is more than one document.
	wantHugeSegments := int(math.Ceil(float64(size) / float64(IndexSegmentSize)))
	if wantHugeSegments < 2 {
		t.Fatalf("fixture is not over the %d-byte threshold; the test cannot prove segmentation", IndexSegmentThreshold)
	}
	if stats.Segments < wantHugeSegments+1 {
		t.Fatalf("Segments = %d for a %d-byte note plus a small one, want at least %d — the note was not segmented (FR-034a)",
			stats.Segments, size, wantHugeSegments+1)
	}
	docs, err := ix.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if docs < uint64(wantHugeSegments+1) {
		t.Fatalf("DocCount = %d, want at least %d index documents", docs, wantHugeSegments+1)
	}

	// The spanning term sits in three DIFFERENT segments of huge.md. Prove that
	// first — otherwise "exactly one result" is trivially true and proves nothing
	// about collapsing.
	rawSegments := map[int]bool{}
	raw, _, err := ix.searchRaw(b2SharedSegmentTerm, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range raw {
		if h.Path == "huge.md" {
			rawSegments[h.Segment] = true
		}
	}
	if len(rawSegments) < 3 {
		t.Fatalf("the spanning term is in %d segments of huge.md, want at least 3 — the fixture cannot exercise collapsing", len(rawSegments))
	}

	// Those three segment hits must reach the caller as exactly ONE result.
	hits, err := ix.Search(b2SharedSegmentTerm, 20)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	var huge IndexHit
	for _, h := range hits {
		if h.Path == "huge.md" {
			count++
			huge = h
		}
	}
	if count != 1 {
		t.Fatalf("huge.md appears %d times in %v, want exactly 1 (FR-034a: segment hits collapse into one result, scored by the best segment)",
			count, b2HitPaths(hits))
	}
	if !containsPath(hits, "small.md") {
		t.Errorf("results = %v, want small.md alongside huge.md — collapsing must not drop other files", b2HitPaths(hits))
	}

	// The collapsed score must be the BEST segment's score — not the last one
	// seen, and not a sum.
	var bestRaw, worstRaw float64
	worstRaw = math.MaxFloat64
	var sumRaw float64
	for _, h := range raw {
		if h.Path != "huge.md" {
			continue
		}
		sumRaw += h.Score
		if h.Score > bestRaw {
			bestRaw = h.Score
		}
		if h.Score < worstRaw {
			worstRaw = h.Score
		}
	}
	if !(bestRaw > worstRaw) {
		t.Fatalf("every segment of huge.md scored %v — the fixture cannot distinguish 'best segment' from 'any segment'", bestRaw)
	}
	if math.Abs(huge.Score-bestRaw) > 1e-9 {
		t.Errorf("collapsed score = %v, want the BEST segment's score %v (worst was %v, sum %v)",
			huge.Score, bestRaw, worstRaw, sumRaw)
	}

	// Terms at the very start and the very end must both be findable: a note is
	// never truncated (FR-034a).
	for marker := range offsets {
		markerHits, searchErr := ix.Search(marker, 20)
		if searchErr != nil {
			t.Fatalf("Search(%q): %v", marker, searchErr)
		}
		found := 0
		var got IndexHit
		for _, h := range markerHits {
			if h.Path == "huge.md" {
				found++
				got = h
			}
		}
		if found != 1 {
			t.Errorf("Search(%q) matched huge.md %d times, want exactly 1", marker, found)
		}
		// The hit carries the ABSOLUTE byte offset of the best segment's start,
		// which is what lets FR-050a's query-time excerpt re-read land correctly.
		// It must be at or before the marker, and within one segment of it.
		want := offsets[marker]
		if got.Offset > want {
			t.Errorf("Search(%q): hit offset %d is past the term at %d — a query-time re-read would miss it",
				marker, got.Offset, want)
		}
		if want-got.Offset >= IndexSegmentSize {
			t.Errorf("Search(%q): hit offset %d is more than one segment before the term at %d — the offset is not absolute",
				marker, got.Offset, want)
		}
		if got.Offset%1 != 0 || got.Offset < 0 {
			t.Errorf("Search(%q): nonsensical offset %d", marker, got.Offset)
		}
	}

	// The huge note's own score must be the BEST of its segments, not a sum and
	// not the last one seen.
	if huge.Score <= 0 {
		t.Errorf("collapsed hit score = %v, want the best segment's positive score", huge.Score)
	}
}

// b2LargeNoteChildEnv marks the re-executed child of the large-note test.
const b2LargeNoteChildEnv = "OMNIPUS_KB_B2_LARGE_NOTE_CHILD"

// b2LargeNoteBytes and b2LargeNoteMemoryBudget are spec test 62's own numbers:
// a 200 MB note, fully indexed, with peak memory staying under 128 MB above
// baseline. The budget being well BELOW the file size is the point — it means a
// whole-file read fails the test rather than merely being slower.
const b2LargeNoteBytes = 200 << 20 // 200 MiB

const b2LargeNoteMemoryBudget = 128 << 20 // 128 MiB above baseline

// TestIndex_LargeNoteSegmentedNotSkipped is spec test 62.
//
// It runs in a RE-EXECUTED CHILD PROCESS, for the reason the spec's own note
// implies: peak-memory readings are high-water marks, so a parent that has
// already run the rest of this suite starts from a baseline high enough to
// absorb a whole-file read and report green. A fresh process starts near zero.
//
// The oracle, and its honest limits. The measurement is runtime.MemStats.HeapSys
// — heap address space obtained from the OS, which only ever grows — not
// resident set size. It is portable (the spec notes that getrusage's unit
// differs between Linux and macOS) and it is exactly sensitive to the failure
// being guarded: reading a 48 MiB note into one buffer, or building one 48 MiB
// index document, forces the heap to obtain 48 MiB and it never gives it back.
// It does NOT see bleve's mmap'd segment files, so it under-reports true RSS —
// which makes it a conservative floor, not a ceiling.
//
// Two oracles the spec rejects are rejected here too: "no error occurred" is
// equally true of an implementation that silently skips the note, and a
// snapshot of HeapAlloc measures whatever happened to be live at that instant.
func TestIndex_LargeNoteSegmentedNotSkipped(t *testing.T) {
	if os.Getenv(b2LargeNoteChildEnv) == "" {
		if testing.Short() {
			t.Skip("large-note indexing is slow; skipped under -short")
		}
		cmd := exec.Command(os.Args[0],
			"-test.run=^TestIndex_LargeNoteSegmentedNotSkipped$",
			"-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), b2LargeNoteChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child process failed: %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "PASS") {
			t.Fatalf("child process did not report PASS — an empty run must fail, not pass:\n%s", out)
		}
		t.Logf("child output:\n%s", out)
		return
	}

	home, root := t.TempDir(), t.TempDir()
	offsets := b2WriteSegmentedNote(t, filepath.Join(root, "enormous.md"), b2LargeNoteBytes, map[float64]string{
		0.01: "zarquonfirstword",
		0.99: "zarquonlastword",
	})

	fi, err := os.Stat(filepath.Join(root, "enormous.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() < b2LargeNoteBytes {
		t.Fatalf("fixture note is %d bytes, want at least %d", fi.Size(), b2LargeNoteBytes)
	}
	if fi.Size() <= b2LargeNoteMemoryBudget {
		t.Fatalf("fixture (%d bytes) is not larger than the memory budget (%d) — the test could not detect a whole-file read",
			fi.Size(), b2LargeNoteMemoryBudget)
	}

	ix := b2Open(t, home, root)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	stats := b2Sync(t, ix)

	runtime.ReadMemStats(&after)
	growth := int64(after.HeapSys) - int64(before.HeapSys)

	if stats.Indexed != 1 {
		t.Fatalf("Indexed = %d, want 1 — the note must be indexed, never refused or skipped (FR-034a)", stats.Indexed)
	}
	wantSegments := int(math.Ceil(float64(fi.Size()) / float64(IndexSegmentSize)))
	if stats.Segments < wantSegments {
		t.Errorf("Segments = %d for a %d-byte note, want at least %d (ceil(size/%d))",
			stats.Segments, fi.Size(), wantSegments, IndexSegmentSize)
	}
	if stats.Segments < 2 {
		t.Fatalf("Segments = %d — the note was indexed as ONE document, which is the precedent's shape and the thing FR-034a forbids", stats.Segments)
	}

	// Not truncated: the last words of the file are indexed.
	for marker := range offsets {
		hits, searchErr := ix.Search(marker, 5)
		if searchErr != nil {
			t.Fatalf("Search(%q): %v", marker, searchErr)
		}
		if len(hits) != 1 || hits[0].Path != "enormous.md" {
			t.Errorf("Search(%q) = %v, want exactly [enormous.md] — a truncated note loses its tail silently",
				marker, b2HitPaths(hits))
		}
	}

	t.Logf("indexed %d bytes as %d segments; heap obtained from the OS grew by %d bytes (budget %d)",
		fi.Size(), stats.Segments, growth, int64(b2LargeNoteMemoryBudget))
	if growth > b2LargeNoteMemoryBudget {
		t.Errorf("heap grew by %d bytes indexing a %d-byte note, budget %d — peak memory is tracking the FILE size, which means the note is being held whole (FR-034a)",
			growth, fi.Size(), int64(b2LargeNoteMemoryBudget))
	}
}

// ---------------------------------------------------------------------------
// FR-046 — reproducibility
// ---------------------------------------------------------------------------

// TestRebuild_IdenticalQueryAndGraphAnswers is spec test 32 / AC-6.1: delete the
// index, rebuild it from unchanged files, and get the identical ranked result
// set for a fixed query corpus.
//
// It compares ANSWERS, never bytes. A scorch index is not byte-reproducible —
// segment names, ids and merge scheduling vary per run — so a byte comparison
// would either flake or be weakened into an assertion about nothing.
func TestRebuild_IdenticalQueryAndGraphAnswers(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()

	// A fixture with overlapping vocabulary, so ranking (not just membership) is
	// exercised: several notes match each query with different term densities.
	fixture := map[string]string{
		"projects/alpha.md":         "alpha alpha alpha bravo charlie retrieval index",
		"projects/bravo.md":         "bravo bravo alpha delta retrieval",
		"projects/charlie.md":       "charlie retrieval retrieval retrieval index alpha",
		"notes/daily/monday.md":     "monday standup alpha retrieval notes",
		"notes/daily/tuesday.md":    "tuesday retrospective bravo index",
		"archive/old note.md":       "archived alpha bravo charlie delta echo",
		"img/diagram-v3.png":        "",
		"attachments/report.pdf":    "",
		"reference/glossary.md":     "index retrieval ranking bm25 scorch segment",
		"reference/.hidden note.md": "hidden but indexed alpha",
	}
	for rel, body := range fixture {
		b2WriteFile(t, root, rel, body)
	}

	queries := []string{"alpha", "retrieval", "index", "bravo charlie", "diagram-v3", "monday", ""}

	collect := func(ix *Index) map[string][]IndexHit {
		out := make(map[string][]IndexHit, len(queries))
		for _, q := range queries {
			hits, err := ix.Search(q, 20)
			if err != nil {
				t.Fatalf("Search(%q): %v", q, err)
			}
			out[q] = hits
		}
		return out
	}

	ix := b2Open(t, home, root)
	if s := b2Sync(t, ix); s.Indexed != len(fixture) {
		t.Fatalf("first index: Indexed=%d, want %d", s.Indexed, len(fixture))
	}
	first := collect(ix)
	indexDir := ix.Dir()
	if err := ix.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Delete the index AND its manifest — a genuine rebuild from the files.
	if err := os.RemoveAll(indexDir); err != nil {
		t.Fatal(err)
	}

	rebuilt := b2Open(t, home, root)
	rebuiltStats := b2Sync(t, rebuilt)
	if rebuiltStats.Indexed != len(fixture) {
		t.Fatalf("rebuild: Indexed=%d, want %d (a rebuild that re-parses nothing is not a rebuild)",
			rebuiltStats.Indexed, len(fixture))
	}
	second := collect(rebuilt)

	nonEmpty := 0
	for _, q := range queries {
		a, b := first[q], second[q]
		if len(a) != len(b) {
			t.Errorf("Search(%q): %d results before rebuild, %d after (%v vs %v)",
				q, len(a), len(b), b2HitPaths(a), b2HitPaths(b))
			continue
		}
		if len(a) > 0 {
			nonEmpty++
		}
		for i := range a {
			if a[i].Path != b[i].Path {
				t.Errorf("Search(%q) rank %d: %q before rebuild, %q after (FR-046)", q, i, a[i].Path, b[i].Path)
			}
			if math.Abs(a[i].Score-b[i].Score) > 1e-9 {
				t.Errorf("Search(%q) rank %d (%s): score %v before rebuild, %v after (FR-046)",
					q, i, a[i].Path, a[i].Score, b[i].Score)
			}
			if a[i].Offset != b[i].Offset {
				t.Errorf("Search(%q) rank %d (%s): offset %d before rebuild, %d after",
					q, i, a[i].Path, a[i].Offset, b[i].Offset)
			}
		}
	}
	if nonEmpty < len(queries)-1 {
		t.Fatalf("only %d of %d queries returned any results — comparing empty sets proves nothing",
			nonEmpty, len(queries))
	}
}

// ---------------------------------------------------------------------------
// DS-2 — collection scale
// ---------------------------------------------------------------------------

// TestIndex_CollectionScale covers DS-2 rows 1, 2 and a scaled row 3. The empty
// case is the one most likely to be broken by accident: an index that errors on
// zero notes turns a brand-new knowledge base into a broken one.
func TestIndex_CollectionScale(t *testing.T) {
	t.Run("no notes", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		ix := b2Open(t, home, root)
		stats := b2Sync(t, ix)
		if stats.Scanned != 0 || stats.Indexed != 0 {
			t.Errorf("empty collection: Scanned=%d Indexed=%d, want 0 and 0", stats.Scanned, stats.Indexed)
		}
		hits, err := ix.Search("anything", 10)
		if err != nil {
			t.Fatalf("search on an empty collection must succeed, got %v", err)
		}
		if len(hits) != 0 {
			t.Errorf("empty collection search = %v, want no results", b2HitPaths(hits))
		}
	})

	t.Run("one note", func(t *testing.T) {
		home, root := t.TempDir(), t.TempDir()
		b2WriteFile(t, root, "Only Note.md", "the only note in the collection mentions zarquon")
		ix := b2Open(t, home, root)
		if s := b2Sync(t, ix); s.Indexed != 1 {
			t.Fatalf("Indexed = %d, want 1", s.Indexed)
		}
		hits, err := ix.Search("zarquon", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].Path != "Only Note.md" {
			t.Errorf("Search = %v, want [Only Note.md]", b2HitPaths(hits))
		}
	})

	t.Run("larger collection with attachments", func(t *testing.T) {
		if testing.Short() {
			t.Skip("scaled collection fixture is slow; skipped under -short")
		}
		home, root := t.TempDir(), t.TempDir()
		const notes, attachments = 748, 400
		for i := 0; i < notes; i++ {
			b2WriteFile(t, root, fmt.Sprintf("notes/%02d/note-%04d.md", i%20, i),
				fmt.Sprintf("note %d discussing alpha bravo charlie delta echo topic%d", i, i%37))
		}
		for i := 0; i < attachments; i++ {
			b2WriteFile(t, root, fmt.Sprintf("files/%02d/asset-%04d.png", i%10, i), "PNGDATA")
		}

		ix := b2Open(t, home, root)
		stats := b2Sync(t, ix)
		if stats.Indexed != notes+attachments {
			t.Fatalf("Indexed = %d, want %d", stats.Indexed, notes+attachments)
		}
		if stats.BatchCommits <= 1 {
			t.Errorf("BatchCommits = %d over %d documents — one batch for the whole collection (FR-034)",
				stats.BatchCommits, stats.Indexed)
		}

		// A requested count is honoured exactly when there are enough matches:
		// this layer does not silently clamp (FR-037's cap, and the duty to
		// REPORT it, belong to the tool layer above).
		hits, err := ix.Search("alpha", 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 25 {
			t.Errorf("Search(\"alpha\", 25) returned %d results, want 25 — every one of the %d notes contains it",
				len(hits), notes)
		}

		// And a query with FEWER matches than the limit returns exactly that
		// many. The expected count comes from the fixture's own definition —
		// topic%d is stamped with i%%37 — not from what the index reports.
		wantTopic7 := 0
		for i := 0; i < notes; i++ {
			if i%37 == 7 {
				wantTopic7++
			}
		}
		topicHits, err := ix.Search("topic7", 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(topicHits) != wantTopic7 {
			t.Errorf("Search(\"topic7\", 50) returned %d results, want %d", len(topicHits), wantTopic7)
		}
		seen := map[string]bool{}
		for _, h := range hits {
			if seen[h.Path] {
				t.Errorf("duplicate result path %q — results are one per file", h.Path)
			}
			seen[h.Path] = true
		}
		if hits, err = ix.Search("asset-0123", 5); err != nil {
			t.Fatal(err)
		}
		if !containsPath(hits, "files/03/asset-0123.png") {
			t.Errorf("attachment search = %v, want files/03/asset-0123.png", b2HitPaths(hits))
		}

		// A second reconcile re-parses nothing.
		second := b2Sync(t, ix)
		if second.Indexed != 0 || second.Unchanged != notes+attachments {
			t.Errorf("second reconcile: Indexed=%d Unchanged=%d, want 0 and %d",
				second.Indexed, second.Unchanged, notes+attachments)
		}
	})
}

// TestIndex_ShrinkingSegmentedNoteLeavesNoOrphans: a note that was five segments
// and is now two must not leave three stale documents behind. Orphaned segments
// are permanently findable text that exists nowhere on disk — a confidently
// wrong answer, and one nothing else would ever notice.
func TestIndex_ShrinkingSegmentedNoteLeavesNoOrphans(t *testing.T) {
	if testing.Short() {
		t.Skip("segmented fixture is slow; skipped under -short")
	}
	home, root := t.TempDir(), t.TempDir()
	notePath := filepath.Join(root, "shrinking.md")
	b2WriteSegmentedNote(t, notePath, IndexSegmentThreshold+(1<<20), map[float64]string{
		0.90: "zarquontail",
	})

	ix := b2Open(t, home, root)
	first := b2Sync(t, ix)
	wantSegments := int(math.Ceil(float64(IndexSegmentThreshold+(1<<20)) / float64(IndexSegmentSize)))
	if first.Segments < wantSegments {
		t.Fatalf("Segments = %d, want at least %d", first.Segments, wantSegments)
	}
	if hits, err := ix.Search("zarquontail", 5); err != nil || len(hits) != 1 {
		t.Fatalf("tail term not found before shrinking: %v (err=%v)", b2HitPaths(hits), err)
	}

	if err := os.WriteFile(notePath, []byte("now a very short note\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := b2Sync(t, ix)
	if second.Indexed != 1 {
		t.Fatalf("Indexed = %d after shrinking, want 1", second.Indexed)
	}
	if second.Segments != 1 {
		t.Errorf("Segments = %d after shrinking, want 1", second.Segments)
	}
	hits, err := ix.Search("zarquontail", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("Search(\"zarquontail\") = %v after the text was deleted from disk; stale segments were left behind",
			b2HitPaths(hits))
	}
	if docs, docErr := ix.DocCount(); docErr != nil || docs != 1 {
		t.Errorf("DocCount = %d (err=%v), want 1 — the old segments are still there", docs, docErr)
	}
}

// TestIndex_UnreadableNoteIsReportedNotIndexedAsEmpty: an evicted or unreadable
// file must produce a loud problem and be ABSENT from the index. Indexing it as
// empty would answer "this note contains nothing", which is a confidently wrong
// answer about a file that may contain anything.
func TestIndex_UnreadableNoteIsReportedNotIndexedAsEmpty(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "good.md", "a readable note about alpha")
	b2WriteFile(t, root, "evicted.md", "content that cannot be read")

	prev := openFileForRead
	openFileForRead = func(path string) (*os.File, error) {
		if strings.HasSuffix(path, "evicted.md") {
			return nil, fmt.Errorf("simulated cloud eviction")
		}
		return prev(path)
	}
	defer func() { openFileForRead = prev }()

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)

	if stats.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1 — only the readable note", stats.Indexed)
	}
	found := false
	for _, p := range stats.Problems {
		if p.RelPath == "evicted.md" && p.Reason == ScanProblemUnreadable {
			found = true
		}
	}
	if !found {
		t.Errorf("Problems = %+v, want an unreadable report for evicted.md", stats.Problems)
	}
	hits, err := ix.Search("evicted", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Path == "evicted.md" {
			t.Error("an unreadable note was indexed anyway — it must be absent, not empty")
		}
	}
}

// TestIndex_QuietlyEvictedNoteIsReportedNotIndexedAsEmpty is FR-111's other
// half, and the half that was unguarded.
//
// # Two ways a cloud placeholder reads as nothing, and only one was covered
//
// TestIndex_UnreadableNoteIsReportedNotIndexedAsEmpty simulates eviction as an
// OPEN ERROR. That is the LOUD variant, and the indexer already handled it: the
// error propagates out of indexNote, the sync loop records a ScanProblem and
// removes the file from the manifest.
//
// The QUIET variant is a clean EOF at zero bytes for a file stat says has
// content — no error anywhere. lifecycle.go's ClassifyContentFailure calls it
// out by name as "the quiet one, and the reason this function exists", and the
// indexer never called it: `filled == 0` broke the loop, `wroteAny` stayed
// false, and the "an empty note is still a note" branch wrote ONE EMPTY INDEX
// DOCUMENT. The index then answers "this note contains nothing" about a file
// that may contain anything, which is exactly what FR-111 forbids.
//
// The fixture reproduces the quiet variant honestly: stat sees the real,
// non-empty file (the walk stats the real path), while the read returns an
// empty handle and io.EOF. Nothing errors; the disagreement between the two is
// the whole signal.
func TestIndex_QuietlyEvictedNoteIsReportedNotIndexedAsEmpty(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "good.md", "a readable note about alpha")
	b2WriteFile(t, root, "evicted.md", "content that is not on local disk, only a placeholder")

	// An empty stand-in the evicted note's reads are served from.
	placeholder := filepath.Join(t.TempDir(), "placeholder")
	if err := os.WriteFile(placeholder, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	prev := openFileForRead
	openFileForRead = func(path string) (*os.File, error) {
		if strings.HasSuffix(path, "evicted.md") {
			return os.Open(placeholder) //nolint:gosec // test fixture
		}
		return prev(path)
	}
	defer func() { openFileForRead = prev }()

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)

	if stats.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1 — only the readable note", stats.Indexed)
	}
	var problem *ScanProblem
	for i := range stats.Problems {
		if stats.Problems[i].RelPath == "evicted.md" {
			problem = &stats.Problems[i]
		}
	}
	if problem == nil {
		t.Fatalf("Problems = %+v, want a report for evicted.md — a silently empty index entry is what FR-111 forbids", stats.Problems)
	}
	if !strings.Contains(problem.Detail, "not on local disk") {
		t.Errorf("problem detail = %q, want the eviction classification from ClassifyContentFailure", problem.Detail)
	}

	// The decisive assertion: the note is ABSENT from the index, not present
	// and empty. An empty document is findable by path and answers every
	// content question with "nothing".
	docs, err := ix.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if docs != 1 {
		t.Errorf("DocCount = %d, want 1 — the evicted note was indexed as an empty document", docs)
	}
	hits, err := ix.Search("placeholder", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Path == "evicted.md" {
			t.Error("the evicted note is in the index")
		}
	}

	// Positive control: the readable note IS indexed, so "absent" above is the
	// guard working rather than the whole sync failing.
	good, err := ix.Search("alpha", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(good) != 1 || good[0].Path != "good.md" {
		t.Fatalf("the readable note is missing from the index: %v", b2HitPaths(good))
	}
}

// ---------------------------------------------------------------------------
// Indexing progress — the number has to MOVE, and it has to move a bounded
// number of times.
//
// Oracles come from the SyncOptions.OnProgress / SyncWith contract, not from
// the loop: indexed counts files reconciled so far (indexed + unchanged)
// against the walk's total, never decreases, never exceeds the total, is
// coalesced to at most one call per interval AND per stride, and the LAST call
// of a run states the run's true final count.
// ---------------------------------------------------------------------------

// b2ProgressCalls records every OnProgress call in order.
type b2ProgressCalls struct {
	indexed []int
	total   []int
}

func (r *b2ProgressCalls) hook() func(indexed, total int) {
	return func(indexed, total int) {
		r.indexed = append(r.indexed, indexed)
		r.total = append(r.total, total)
	}
}

// b2Fixture writes n notes and returns the root holding them.
func b2Fixture(t *testing.T, n int) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < n; i++ {
		b2WriteFile(t, root, fmt.Sprintf("note%03d.md", i), fmt.Sprintf("# Note %d\nalpha bravo charlie %d", i, i))
	}
	return root
}

// TestSyncProgress_CountRisesAndEndsAtTheTotal is the founder-visible property:
// the number moves while the index runs, and it finishes on the true total
// rather than wherever the last throttled update happened to land.
//
// ProgressInterval is set to the smallest possible value so the TIME bound
// stops suppressing updates; the count bound (one update per stride, and a
// stride of 1 for a collection this size) is left doing the work. Without that
// the test would be asserting a property of the clock, not of the reporting.
func TestSyncProgress_CountRisesAndEndsAtTheTotal(t *testing.T) {
	const wantTotal = 60 // the fixture's own size — counted here, not read off the code
	home, root := t.TempDir(), b2Fixture(t, wantTotal)

	ix := b2Open(t, home, root)
	rec := &b2ProgressCalls{}
	stats, err := ix.SyncWith(context.Background(), SyncOptions{
		OnProgress:       rec.hook(),
		ProgressInterval: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("SyncWith: %v", err)
	}
	if stats.Scanned != wantTotal || stats.Indexed != wantTotal {
		t.Fatalf("fixture mis-built: Scanned=%d Indexed=%d, want %d each", stats.Scanned, stats.Indexed, wantTotal)
	}

	if len(rec.indexed) < 5 {
		t.Fatalf("OnProgress fired %d times for %d files: a bar that moves twice is the frozen bar this exists to fix (calls: %v)",
			len(rec.indexed), wantTotal, rec.indexed)
	}

	prev := -1
	for i, n := range rec.indexed {
		if n <= prev {
			t.Errorf("call %d reported %d after %d: the count must STRICTLY increase (calls: %v)", i, n, prev, rec.indexed)
		}
		if n > wantTotal {
			t.Errorf("call %d reported %d of %d: a count above the total is a ratio over 1", i, n, wantTotal)
		}
		if rec.total[i] != wantTotal {
			t.Errorf("call %d reported total %d, want the walk's %d", i, rec.total[i], wantTotal)
		}
		prev = n
	}

	last := rec.indexed[len(rec.indexed)-1]
	if last != wantTotal {
		t.Errorf("last OnProgress reported %d of %d; the final call must state the run's true count, not stop short of it",
			last, wantTotal)
	}
	if last != stats.Indexed+stats.Unchanged {
		t.Errorf("last OnProgress reported %d but the run returned Indexed+Unchanged = %d: the two must be the same arithmetic",
			last, stats.Indexed+stats.Unchanged)
	}
}

// TestSyncProgress_IsCoalescedNotOncePerFile pins the other half of the
// contract. A per-file hook on a 100,000-note vault is 100,000 WebSocket
// frames, which is worse for the reader than the three frames it replaced.
//
// The oracle is the documented bound itself — at most one call per interval,
// plus the final flush — checked against the run's MEASURED duration, so the
// assertion cannot go flaky when the machine is slow: a slower run simply
// permits proportionally more calls.
func TestSyncProgress_IsCoalescedNotOncePerFile(t *testing.T) {
	const wantTotal = 60
	home, root := t.TempDir(), b2Fixture(t, wantTotal)

	ix := b2Open(t, home, root)
	rec := &b2ProgressCalls{}
	started := time.Now()
	// No ProgressInterval: the production default (200ms) applies.
	if _, err := ix.SyncWith(context.Background(), SyncOptions{OnProgress: rec.hook()}); err != nil {
		t.Fatalf("SyncWith: %v", err)
	}
	elapsed := time.Since(started)

	permitted := 1 + int(elapsed/DefaultProgressInterval) + 1 // intervals that elapsed, plus the final flush
	if len(rec.indexed) > permitted {
		t.Errorf("OnProgress fired %d times in %v: the 200ms window permits at most %d (calls: %v)",
			len(rec.indexed), elapsed, permitted, rec.indexed)
	}
	if len(rec.indexed) >= wantTotal {
		t.Errorf("OnProgress fired %d times for %d files — that is the unthrottled per-file hook",
			len(rec.indexed), wantTotal)
	}
	if len(rec.indexed) == 0 {
		t.Fatal("OnProgress never fired: a run always reports its final count")
	}
	if last := rec.indexed[len(rec.indexed)-1]; last != wantTotal {
		t.Errorf("last OnProgress reported %d of %d: throttling may drop intermediate updates, never the final one",
			last, wantTotal)
	}
}

// TestProgressStride_BoundsUpdatesForALargeCollection checks the count bound
// that makes the worst case a property of the RUN rather than of its duration.
//
// Both directions matter. Too small a stride is the 100,000-frame flood; too
// large a stride is a bar that jumps from nothing to done, which is the frozen
// bar again wearing a different hat. The budget must be mostly USED.
func TestProgressStride_BoundsUpdatesForALargeCollection(t *testing.T) {
	for _, total := range []int{0, 1, 7, 999, 1000, 1001, 1500, 100000, 1000000} {
		stride := progressStride(total)
		if stride < 1 {
			t.Fatalf("progressStride(%d) = %d: a stride below 1 cannot bound anything", total, stride)
		}
		updates := 0
		if stride > 0 {
			updates = (total + stride - 1) / stride
		}
		if updates > maxProgressUpdates {
			t.Errorf("total %d with stride %d permits %d updates, above the %d budget",
				total, stride, updates, maxProgressUpdates)
		}
		if total >= maxProgressUpdates && updates < maxProgressUpdates/2 {
			t.Errorf("total %d with stride %d permits only %d updates: the budget is %d and a coarser bar than that stops informing anyone",
				total, stride, updates, maxProgressUpdates)
		}
	}
}

// TestProgressCoalescer_BothBoundsApplyAndTheFinalCallAlwaysGoesOut drives the
// coalescer on a fake clock, so the time bound is asserted by COUNTING time
// rather than by sleeping through it.
func TestProgressCoalescer_BothBoundsApplyAndTheFinalCallAlwaysGoesOut(t *testing.T) {
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	newC := func(total int, interval time.Duration, clock *time.Time) (*progressCoalescer, *b2ProgressCalls) {
		rec := &b2ProgressCalls{}
		return newProgressCoalescer(rec.hook(), total, interval, func() time.Time { return *clock }), rec
	}

	t.Run("an update inside the interval is suppressed", func(t *testing.T) {
		clock := base
		c, rec := newC(10, time.Second, &clock)
		clock = base.Add(999 * time.Millisecond)
		c.update(5)
		if len(rec.indexed) != 0 {
			t.Errorf("reported %v inside the interval; the window is the whole point", rec.indexed)
		}
		clock = base.Add(time.Second)
		c.update(5)
		if len(rec.indexed) != 1 || rec.indexed[0] != 5 {
			t.Errorf("calls = %v, want exactly [5] once the interval has passed", rec.indexed)
		}
	})

	t.Run("an update below the stride is suppressed however long it waits", func(t *testing.T) {
		clock := base
		// 4000 files: stride 4, so a single-file step must not report.
		c, rec := newC(4000, time.Millisecond, &clock)
		clock = base.Add(time.Hour)
		c.update(3)
		if len(rec.indexed) != 0 {
			t.Errorf("reported %v for a 3-file step against a stride of %d", rec.indexed, progressStride(4000))
		}
		c.update(4)
		if len(rec.indexed) != 1 || rec.indexed[0] != 4 {
			t.Errorf("calls = %v, want [4] once the stride is met", rec.indexed)
		}
	})

	t.Run("the final call ignores both bounds", func(t *testing.T) {
		clock := base
		c, rec := newC(10, time.Hour, &clock)
		c.update(9) // suppressed: no time has passed
		c.flush(10)
		if len(rec.indexed) != 1 || rec.indexed[0] != 10 {
			t.Errorf("calls = %v, want [10]: a run that finishes inside one window still reports its total", rec.indexed)
		}
	})

	t.Run("the final call is silent when it would repeat itself", func(t *testing.T) {
		clock := base
		c, rec := newC(10, time.Nanosecond, &clock)
		clock = base.Add(time.Second)
		c.update(10)
		c.flush(10)
		if len(rec.indexed) != 1 {
			t.Errorf("calls = %v, want one: the count already landed on the total", rec.indexed)
		}
	})

	t.Run("a count above the total is clamped, never reported as a ratio over 1", func(t *testing.T) {
		clock := base
		c, rec := newC(10, time.Nanosecond, &clock)
		clock = base.Add(time.Second)
		c.update(99)
		if len(rec.indexed) != 1 || rec.indexed[0] != 10 || rec.total[0] != 10 {
			t.Errorf("calls = %v of %v, want 10 of 10", rec.indexed, rec.total)
		}
	})

	t.Run("a nil hook is inert", func(t *testing.T) {
		clock := base
		c := newProgressCoalescer(nil, 10, time.Nanosecond, func() time.Time { return clock })
		clock = base.Add(time.Hour)
		c.update(5)
		c.flush(10) // must not panic
	})
}
