// Tests for F10 (code review A): a read failure partway through a
// multi-segment note must not leave its already-committed segments behind
// with no manifest record to ever name them again.
//
// # The bug, restated as an oracle
//
// batchState.add can commit a segment to the LIVE index immediately —
// indexBatchMaxBytes equals IndexSegmentSize, so a full-size segment always
// does. Before this fix, a read error on a LATER segment of the SAME note
// simply returned an error out of indexNote; SyncWith's caller then called
// manifest.Remove(entry.RelPath), which is a no-op for a path the manifest
// never held a record of in the first place. The earlier, already-committed
// segments stayed in the index with nothing naming them: the next Sync sees
// hadRec == false for that path and issues no deletes (there is nothing to
// delete BY, only the manifest's per-path Segments count tells the ordinary
// deletion loop how many segment ids to remove), and if the file is later
// deleted from disk, the removal loop — which walks manifest.Entries — never
// sees a path it never held either. The segments are orphaned permanently.
//
// The reviewer's flagged detail: the trailing ClassifyContentFailure in
// indexNote does NOT catch this. It fires only when the read returned ZERO
// bytes for a file stat says has content (a TOTAL-eviction detector), and
// once even one segment has been added, totalRead is provably nonzero — so a
// partial, nonzero read failure sails past it in both places it is called.
//
// # Why the reproduction needs a dedicated seam
//
// indexNote seeks the SAME open *os.File back to 0 between its hashing pass
// and its segmenting pass (so the hash and the indexed content agree), which
// means the file handle must be an actual seekable regular file — a pipe or
// socket standing in for openFileForRead's return value cannot satisfy that
// Seek call at all, so the two hashing/segmenting reads cannot be pointed at
// different sources. Nor can any real POSIX regular-file trick reliably fail
// a read() at exactly the second call and not the first, portably, without
// root, on both Linux and macOS. readNoteChunk is the narrow seam this file
// uses instead — index.go's own doc comment on it explains why it exists
// beside openFileForRead rather than reusing it.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// TestIndexNote_ReadErrorAfterFirstSegmentLeavesNoOrphanDocuments is F10's
// exact reproduction and regression guard: a multi-segment note whose second
// read fails must leave ZERO documents behind for that path — not the one
// segment that had already committed before the failure.
func TestIndexNote_ReadErrorAfterFirstSegmentLeavesNoOrphanDocuments(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()

	// No '\n' anywhere, so the segmenting loop's line-boundary search always
	// finds none and cuts exactly at the buffer boundary — the read call
	// count this test depends on is then deterministic: read #1 fills exactly
	// one IndexSegmentSize-sized segment (and, since indexBatchMaxBytes ==
	// IndexSegmentSize, batch.add commits it immediately), read #2 is where
	// the injected failure lands.
	const noteSize = IndexSegmentSize*2 + 4096
	notePath := b2WriteFile(t, root, "big.md", strings.Repeat("x", noteSize))

	calls := 0
	origRead := readNoteChunk
	readNoteChunk = func(f *os.File, buf []byte) (int, error) {
		calls++
		if calls == 2 {
			return 0, errors.New("simulated cloud-mount read failure")
		}
		return io.ReadFull(f, buf)
	}
	t.Cleanup(func() { readNoteChunk = origRead })

	ix := b2Open(t, home, root)
	stats, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		t.Fatalf("SyncWith returned a top-level error rather than reporting a per-file problem: %v", err)
	}
	if calls < 2 {
		t.Fatalf("test setup bug: readNoteChunk was called %d time(s), want at least 2 — "+
			"the note fixture is not large enough to force a second segmenting read", calls)
	}
	if stats.Indexed != 0 {
		t.Errorf("Indexed = %d, want 0 — the note failed to index", stats.Indexed)
	}
	found := false
	for _, p := range stats.Problems {
		if p.RelPath == "big.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Problems = %+v, want a report for big.md", stats.Problems)
	}

	// THE DECISIVE ASSERTION. Before the fix this is exactly where the
	// orphan shows up: segment 0 committed to the live index before the
	// injected failure on segment 1, and nothing ever rolled it back.
	docs, docErr := ix.DocCount()
	if docErr != nil {
		t.Fatal(docErr)
	}
	if docs != 0 {
		t.Errorf("DocCount = %d after a note failed partway through indexing, want 0 — "+
			"segment 0 was committed before the injected failure and orphaned", docs)
	}

	// The manifest correctly holds no record either — which is exactly the
	// state that makes the orphan permanent if any documents survived: no
	// future Sync's ordinary per-path deletion loop can ever reach them.
	realRoot, resolveErr := ResolveCollectionRoot(root)
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	m, mErr := LoadManifest(ix.ManifestPath(), realRoot)
	if mErr != nil {
		t.Fatal(mErr)
	}
	if _, ok := m.Get("big.md"); ok {
		t.Errorf("manifest unexpectedly holds a record for big.md after it failed to index")
	}

	// Positive control, run with normal reads restored: deleting the file
	// from disk and reconciling again must leave the index at zero documents
	// — proving there is genuinely nothing left over from the failed attempt
	// for THIS run to have gotten lucky about.
	readNoteChunk = origRead
	if err := os.Remove(notePath); err != nil {
		t.Fatal(err)
	}
	stats2, err2 := ix.SyncWith(context.Background(), SyncOptions{})
	if err2 != nil {
		t.Fatalf("second SyncWith: %v", err2)
	}
	if stats2.Removed != 0 {
		t.Errorf("second Sync reported Removed = %d, want 0 — there was never a manifest record to remove by", stats2.Removed)
	}
	docs2, docErr2 := ix.DocCount()
	if docErr2 != nil {
		t.Fatal(docErr2)
	}
	if docs2 != 0 {
		t.Errorf("DocCount = %d after the file was deleted from disk following the failed index, want 0", docs2)
	}
}

// NOTE: rollbackPartialSegments also guards the batch.add error branch (a
// genuine bleve indexing failure on a later segment, rather than a read
// failure) by the identical mechanism, for the identical reason — ordinal
// already counts every segment that committed in an earlier loop iteration.
// It is not given its own reproduction here: forcing bleve's own Batch call
// to fail deterministically, from inside a Sync that already holds ix.mu,
// without either depending on an internal bleve validation rule (liable to
// change under us) or reaching for the live index handle in a way that would
// self-deadlock against that same mutex, was not worth the fragility for a
// second call site protected by the exact same three-line helper the test
// above already exercises end to end.
