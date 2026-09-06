// Reproduction/regression harness for A2(a) — vault-tools-test-report F1 and
// harness-test-issues Issue 5: a body term present in N notes must be returned
// for all N notes.
//
// The field report ("words=\"composio\" returns 1 hit while 68 files contain
// it") turned out NOT to be a search-coverage defect: this test passes as
// written, i.e. a FULLY SYNCED index returns every note that contains the term.
// The single-hit symptom in the field was a STALE index — the collection had
// grown past what the index had reconciled — which is exactly why A2(d) adds an
// index-freshness signal so a caller can tell a stale index from absent
// content. This test guards the coverage property so a future segmentation or
// collapse change cannot silently regress it.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"testing"
)

// TestSearch_BodyTermReturnsEveryNoteContainingIt is the direct reproduction of
// the field report. The oracle is the number of notes the test itself wrote —
// never what the index happens to return.
func TestSearch_BodyTermReturnsEveryNoteContainingIt(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	const n = 68
	for i := 0; i < n; i++ {
		// The term sits in ordinary prose, in a body that is otherwise unique
		// per note, so nothing collapses two notes into one on any field but
		// the shared term.
		b2WriteFile(t, root, fmt.Sprintf("notes/note-%04d.md", i),
			fmt.Sprintf("Note %d discusses the composio integration in passing.", i))
	}
	ix := b2Open(t, home, root)
	stats := b2Sync(t, ix)
	if stats.Indexed != n {
		t.Fatalf("Indexed = %d, want %d", stats.Indexed, n)
	}

	hits, err := ix.Search("composio", 1000)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != n {
		t.Fatalf("Search(\"composio\") returned %d hits, want %d (every note that contains the term)", len(hits), n)
	}
}
