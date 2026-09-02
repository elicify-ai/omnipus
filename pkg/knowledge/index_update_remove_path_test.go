// Tests for Index.UpdatePath and Index.RemovePath — the single-path update
// capability the text index lacked entirely before this change (the
// properties index already had UpsertNote/DeleteNote; see propindex).
//
// The operator's requirement is instant reindexing on a write Omnipus itself
// performs (a vault tool writing a note, a human adding a file through the
// UI), without waiting for the next full SyncWith. These tests hold
// UpdatePath/RemovePath to the SAME guarantees SyncWith already gives:
//
//   - a file becomes/stops being findable exactly as SyncWith would leave it
//     (FR-033/FR-034a/FR-039a all still apply — segmentation, and zero
//     content reads for an attachment);
//   - the manifest stays consistent, proven by asserting a FOLLOWING SyncWith
//     reports the file Unchanged rather than re-indexing it — the failure
//     mode index.go's own comments call out repeatedly ("a document indexed
//     without its manifest entry updated makes the next Sync skip it as
//     unchanged against a document that no longer matches");
//   - a shrinking edit leaves no orphan segments, the same F10/FR-034a
//     property SyncWith's own orphan-segment test pins, reached this time
//     through UpdatePath instead of SyncWith's walk.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/library"
)

// ---------------------------------------------------------------------------
// UpdatePath: a new file becomes findable, and the manifest is left
// consistent.
// ---------------------------------------------------------------------------

func TestUpdatePath_NewFileBecomesFindable(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	// PRECONDITION: nothing has been written yet, so the term cannot be
	// found. Without this the "findable after" assertion below could pass
	// on a pre-existing state (e.g. a stub Search that always returns a
	// hit) and the test would not have proven anything.
	pre, err := ix.Search("brandnewword", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre) != 0 {
		t.Fatalf("precondition failed: Search found %v before the file even existed", b2HitPaths(pre))
	}

	b2WriteFile(t, root, "new.md", "a note containing brandnewword right here\n")

	if err := ix.UpdatePath(context.Background(), "new.md"); err != nil {
		t.Fatalf("UpdatePath: %v", err)
	}

	hits, err := ix.Search("brandnewword", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(hits, "new.md") {
		t.Fatalf("Search after UpdatePath = %v, want new.md", b2HitPaths(hits))
	}

	// Manifest consistency, direct check: a record now exists for the path.
	m, err := LoadManifest(ix.ManifestPath(), ix.Root())
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := m.Get("new.md")
	if !ok {
		t.Fatal("manifest holds no record for new.md after UpdatePath")
	}
	if rec.Segments != 1 {
		t.Errorf("rec.Segments = %d, want 1", rec.Segments)
	}

	// THE decisive manifest-consistency assertion: a following SyncWith must
	// report the file Unchanged, not re-index it. If UpdatePath had indexed
	// the file without updating the manifest (or with stale stat facts),
	// this Sync would see StatUnchanged() fail and re-index it instead.
	stats := b2Sync(t, ix)
	if stats.Unchanged != 1 {
		t.Errorf("following Sync: Unchanged = %d, want 1", stats.Unchanged)
	}
	if stats.Indexed != 0 {
		t.Errorf("following Sync: Indexed = %d, want 0 — the manifest was not left consistent with the index", stats.Indexed)
	}
}

// ---------------------------------------------------------------------------
// UpdatePath: an edit replaces the old content with the new.
// ---------------------------------------------------------------------------

func TestUpdatePath_EditedFile_OldContentStopsMatchingNewContentMatches(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "alpha keyword_old body text\n")

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if stats.Indexed != 1 {
		t.Fatalf("initial Sync: Indexed = %d, want 1", stats.Indexed)
	}

	// PRECONDITIONS: the old term is findable, the new term is not — so the
	// assertions after the edit cannot pass on a pre-existing state.
	if hits, err := ix.Search("keyword_old", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "note.md") {
		t.Fatalf("precondition failed: keyword_old should be findable before the edit, got %v", b2HitPaths(hits))
	}
	if hits, err := ix.Search("keyword_new", 10); err != nil {
		t.Fatal(err)
	} else if len(hits) != 0 {
		t.Fatalf("precondition failed: keyword_new should NOT be findable before the edit, got %v", b2HitPaths(hits))
	}

	b2WriteFile(t, root, "note.md", "alpha keyword_new body text\n")
	if err := ix.UpdatePath(context.Background(), "note.md"); err != nil {
		t.Fatalf("UpdatePath: %v", err)
	}

	if hits, err := ix.Search("keyword_old", 10); err != nil {
		t.Fatal(err)
	} else if containsPath(hits, "note.md") {
		t.Errorf("keyword_old still matches after the edit: %v", b2HitPaths(hits))
	}
	if hits, err := ix.Search("keyword_new", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "note.md") {
		t.Errorf("keyword_new does not match after the edit: %v", b2HitPaths(hits))
	}

	// Manifest consistency: a following Sync reports Unchanged, not Indexed.
	stats2 := b2Sync(t, ix)
	if stats2.Unchanged != 1 || stats2.Indexed != 0 {
		t.Errorf("following Sync: Unchanged=%d Indexed=%d, want Unchanged=1 Indexed=0", stats2.Unchanged, stats2.Indexed)
	}
}

// ---------------------------------------------------------------------------
// UpdatePath: a shrinking edit (fewer segments than before) leaves no orphan
// segment documents. This is the mutation-sensitive guard for indexOneFile's
// delete-before-reindex step: bleve's Index() call is an upsert by document
// id, so a segment count that does not change would pass even if the delete
// step were removed entirely — only a SHRINK exposes it.
// ---------------------------------------------------------------------------

func TestUpdatePath_ShrinkingEdit_LeavesNoOrphanSegments(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()

	// Two segments: a full IndexSegmentSize of filler with no newlines (so
	// the segmenting loop's line-boundary search finds none and cuts at the
	// exact buffer boundary — deterministic segment count), then a short
	// tail carrying a marker that exists ONLY in segment ordinal 1.
	oldContent := strings.Repeat("x", IndexSegmentSize) + " oldtailmarkerxyz"
	b2WriteFile(t, root, "shrink.md", oldContent)

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if stats.Indexed != 1 {
		t.Fatalf("initial Sync: Indexed = %d, want 1", stats.Indexed)
	}
	if stats.Segments != 2 {
		t.Fatalf("test setup bug: initial Segments = %d, want 2 (the marker must land in a second segment)", stats.Segments)
	}

	m, err := LoadManifest(ix.ManifestPath(), ix.Root())
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := m.Get("shrink.md")
	if !ok || rec.Segments != 2 {
		t.Fatalf("test setup bug: manifest record = %+v, want Segments=2", rec)
	}

	// PRECONDITION: the marker is findable before the edit.
	if hits, err := ix.Search("oldtailmarkerxyz", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "shrink.md") {
		t.Fatalf("precondition failed: marker should be findable before the shrink, got %v", b2HitPaths(hits))
	}

	// Shrink to a single short line: one segment, no marker.
	b2WriteFile(t, root, "shrink.md", "a short replacement body\n")
	if err := ix.UpdatePath(context.Background(), "shrink.md"); err != nil {
		t.Fatalf("UpdatePath: %v", err)
	}

	if hits, err := ix.Search("oldtailmarkerxyz", 10); err != nil {
		t.Fatal(err)
	} else if len(hits) != 0 {
		t.Errorf("marker from the removed second segment is still findable after the shrink: %v", b2HitPaths(hits))
	}

	docs, err := ix.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if docs != 1 {
		t.Errorf("DocCount = %d after shrinking a 2-segment note to 1 segment, want 1 (an orphan segment document survives)", docs)
	}

	m2, err := LoadManifest(ix.ManifestPath(), ix.Root())
	if err != nil {
		t.Fatal(err)
	}
	rec2, ok := m2.Get("shrink.md")
	if !ok {
		t.Fatal("manifest holds no record for shrink.md after UpdatePath")
	}
	if rec2.Segments != 1 {
		t.Errorf("manifest Segments = %d after the shrink, want 1", rec2.Segments)
	}
}

// ---------------------------------------------------------------------------
// UpdatePath: attachments stay filename-only, and UpdatePath reads zero
// content bytes from them (FR-039a).
// ---------------------------------------------------------------------------

func TestUpdatePath_Attachment_FindableByNameBodyNotSearchable_ZeroReads(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	// PRECONDITIONS: neither the name nor the (never-to-be-read) body term
	// is findable before the file exists.
	if hits, err := ix.Search("diagramv9", 10); err != nil {
		t.Fatal(err)
	} else if len(hits) != 0 {
		t.Fatalf("precondition failed: name term found before the file existed: %v", b2HitPaths(hits))
	}

	b2WriteFile(t, root, "diagramv9.pdf", "toppdfsecret1234 — bytes that must never be indexed")

	opened, restore := b2CountingOpen(t)
	defer restore()

	if err := ix.UpdatePath(context.Background(), "diagramv9.pdf"); err != nil {
		t.Fatalf("UpdatePath: %v", err)
	}

	if len(*opened) != 0 {
		t.Errorf("UpdatePath opened %v while indexing an attachment; FR-039a requires zero content reads", *opened)
	}

	hits, err := ix.Search("diagramv9", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPath(hits, "diagramv9.pdf") {
		t.Fatalf("attachment not findable by name after UpdatePath: %v", b2HitPaths(hits))
	}
	for _, h := range hits {
		if h.Path == "diagramv9.pdf" && h.Kind != ScanKindAttachment {
			t.Errorf("Kind = %q, want %q", h.Kind, ScanKindAttachment)
		}
	}

	bodyHits, err := ix.Search("toppdfsecret1234", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodyHits) != 0 {
		t.Errorf("attachment body is searchable: %v — its bytes were never supposed to be read", b2HitPaths(bodyHits))
	}

	m, err := LoadManifest(ix.ManifestPath(), ix.Root())
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := m.Get("diagramv9.pdf")
	if !ok {
		t.Fatal("manifest holds no record for the attachment after UpdatePath")
	}
	if rec.Hash != "" {
		t.Errorf("manifest Hash = %q for an attachment, want empty — hashing means reading, forbidden by FR-039a", rec.Hash)
	}

	stats := b2Sync(t, ix)
	if stats.Unchanged != 1 || stats.Indexed != 0 {
		t.Errorf("following Sync: Unchanged=%d Indexed=%d, want Unchanged=1 Indexed=0", stats.Unchanged, stats.Indexed)
	}
}

// ---------------------------------------------------------------------------
// UpdatePath: error paths.
// ---------------------------------------------------------------------------

func TestUpdatePath_MissingFile_Errors(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	err := ix.UpdatePath(context.Background(), "does-not-exist.md")
	if err == nil {
		t.Fatal("UpdatePath on a missing file returned nil, want an error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want it to wrap fs.ErrNotExist", err)
	}
}

func TestUpdatePath_PathTraversal_Refused(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	err := ix.UpdatePath(context.Background(), "../outside.md")
	if err == nil {
		t.Fatal("UpdatePath with a traversal path returned nil, want an error")
	}
	if !errors.Is(err, library.ErrInvalidPath) {
		t.Errorf("error = %v, want it to wrap library.ErrInvalidPath", err)
	}
}

func TestUpdatePath_Symlink_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevated privileges on Windows CI")
	}
	home, root := t.TempDir(), t.TempDir()
	target := b2WriteFile(t, root, "real.md", "content")
	link := filepath.Join(root, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	ix := b2Open(t, home, root)
	err := ix.UpdatePath(context.Background(), "link.md")
	if err == nil {
		t.Fatal("UpdatePath on a symlink returned nil, want an error")
	}
}

// ---------------------------------------------------------------------------
// RemovePath.
// ---------------------------------------------------------------------------

func TestRemovePath_RemovedFileStopsMatching(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	abs := b2WriteFile(t, root, "gone.md", "a note with uniqueremoveword inside\n")

	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if stats.Indexed != 1 {
		t.Fatalf("initial Sync: Indexed = %d, want 1", stats.Indexed)
	}

	// PRECONDITION: the file is findable before it is removed.
	if hits, err := ix.Search("uniqueremoveword", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "gone.md") {
		t.Fatalf("precondition failed: gone.md should be findable before removal, got %v", b2HitPaths(hits))
	}

	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	if err := ix.RemovePath(context.Background(), "gone.md"); err != nil {
		t.Fatalf("RemovePath: %v", err)
	}

	hits, err := ix.Search("uniqueremoveword", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("gone.md is still findable after RemovePath: %v", b2HitPaths(hits))
	}

	m, err := LoadManifest(ix.ManifestPath(), ix.Root())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("gone.md"); ok {
		t.Error("manifest still holds a record for gone.md after RemovePath")
	}

	docs, err := ix.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Errorf("DocCount = %d after RemovePath removed the collection's only file, want 0", docs)
	}

	// Manifest consistency: a following Sync sees an already-consistent
	// state — the file is gone from disk AND gone from the manifest, so
	// there is nothing left for the deletion loop to (redundantly) remove.
	stats2, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stats2.Removed != 0 {
		t.Errorf("following Sync: Removed = %d, want 0 — RemovePath should have already reconciled the manifest", stats2.Removed)
	}
}

func TestRemovePath_NoRecord_IsNoOp(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	ix := b2Open(t, home, root)

	if exists, err := ManifestExists(ix.ManifestPath()); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Fatal("test setup bug: manifest already exists before RemovePath was called")
	}

	if err := ix.RemovePath(context.Background(), "never-indexed.md"); err != nil {
		t.Fatalf("RemovePath on a path the manifest never held = %v, want nil (a safe no-op)", err)
	}

	// It must not have manufactured a manifest write for a no-op.
	if exists, err := ManifestExists(ix.ManifestPath()); err != nil {
		t.Fatal(err)
	} else if exists {
		t.Error("RemovePath wrote a manifest for a no-op removal")
	}

	docs, err := ix.DocCount()
	if err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Errorf("DocCount = %d after a no-op RemovePath, want 0", docs)
	}
}

// ---------------------------------------------------------------------------
// Concurrency: UpdatePath/RemovePath share Index.mu with SyncWith.
// ---------------------------------------------------------------------------

// TestUpdatePath_SerializesWithSyncMutex proves the concurrency claim in
// UpdatePath's doc comment directly, white-box, rather than asserting it in
// prose: holding ix.mu (the exact lock SyncWith takes for the duration of a
// whole reconcile) must block a concurrent UpdatePath until it is released.
func TestUpdatePath_SerializesWithSyncMutex(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "alpha")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)
	b2WriteFile(t, root, "note.md", "beta")

	ix.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- ix.UpdatePath(context.Background(), "note.md")
	}()

	// holdWindow is deliberately generous: a real UpdatePath commits a batch
	// to scorch and fsyncs the manifest, which measured well over 100ms on
	// this sandbox's disk even with NO lock contention at all. The window
	// must comfortably exceed that natural latency, or a slow-but-unlocked
	// UpdatePath would pass this assertion for the wrong reason (finishing
	// on its own, coincidentally, before the window closes) rather than
	// because it is genuinely blocked on ix.mu for as long as it is held.
	const holdWindow = 2 * time.Second
	select {
	case err := <-done:
		ix.mu.Unlock()
		t.Fatalf("UpdatePath returned (err=%v) after only %v while ix.mu was held by another goroutine — "+
			"it does not share Index.mu with SyncWith as the doc comment claims", err, holdWindow)
	case <-time.After(holdWindow):
		// Expected: still blocked on ix.mu.
	}

	ix.mu.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("UpdatePath returned an error after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UpdatePath did not complete after ix.mu was released")
	}
}

// TestRemovePath_SerializesWithSyncMutex is TestUpdatePath_SerializesWithSyncMutex's
// counterpart for RemovePath — see that test's comments for why the hold
// window must exceed natural unlocked latency rather than a short guess.
func TestRemovePath_SerializesWithSyncMutex(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "alpha")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	ix.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- ix.RemovePath(context.Background(), "note.md")
	}()

	const holdWindow = 2 * time.Second
	select {
	case err := <-done:
		ix.mu.Unlock()
		t.Fatalf("RemovePath returned (err=%v) after only %v while ix.mu was held by another goroutine — "+
			"it does not share Index.mu with SyncWith as the doc comment claims", err, holdWindow)
	case <-time.After(holdWindow):
		// Expected: still blocked on ix.mu.
	}

	ix.mu.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RemovePath returned an error after the lock was released: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RemovePath did not complete after ix.mu was released")
	}
}
