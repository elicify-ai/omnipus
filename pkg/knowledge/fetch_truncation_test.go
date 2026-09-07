// Omnipus — code review A, F7: Index.SearchFiltered's escalating fetch loop
// used to exit silently at indexSearchMaxFetch (2048 raw segment hits), with
// no way for a caller to tell "stopped because it found everything" apart
// from "stopped because it hit the safety ceiling with more, unexamined,
// matches still on the table". SearchFiltered's doc comment even said the
// opposite: "limit is honoured as given — this layer does not silently
// clamp." A narrow `keep` filter (a folder scope, in production) whose
// surviving matches all rank below the ceiling made this reachable with an
// ordinary, fully-built index: 40,000 notes filtered to one folder returned
// one row out of thirty real matches with Complete: true.
//
// indexSearchMaxFetch is a `var`, not a `const` (see its doc comment in
// index.go), solely so these tests can lower it and reproduce the boundary
// against a fixture of a few dozen documents instead of the 2000+ real ones
// it would otherwise take to cross 2048 raw hits — this package already
// treats a ~1100-document fixture as "slow; skipped under -short" (see
// TestOpenIndexAndSync's "larger collection with attachments" subtest), and
// this test's default -short run must stay fast.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"strings"
	"testing"
)

// f7WithLoweredFetchCap lowers indexSearchMaxFetch for the duration of one
// test and restores it via t.Cleanup, so the boundary can be crossed by a
// small fixture. It is not run under t.Parallel() by any caller (checked at
// the call sites below), so this package-level var is never read
// concurrently with the mutation.
func f7WithLoweredFetchCap(t *testing.T, n int) {
	t.Helper()
	orig := indexSearchMaxFetch
	indexSearchMaxFetch = n
	t.Cleanup(func() { indexSearchMaxFetch = orig })
}

// f7Corpus builds a collection where a keyword ("review") matches both a
// large "noise" folder and a small "Meetings" folder — mirroring the review's
// own scenario. Every noise note repeats the term densely, so it scores far
// above a Meetings note that mentions it once; with indexSearchMaxFetch
// lowered below the noise count, the top-ranked raw hits examined are ALL
// noise, and every Meetings note ranks past the ceiling.
func f7Corpus(t *testing.T, root string, noise, meetings int) {
	t.Helper()
	for i := 0; i < noise; i++ {
		// Five occurrences of the term in a short document scores well above
		// a single occurrence — BM25 rewards term frequency in a short field,
		// and there is no other shared vocabulary to compete on.
		b2WriteFile(t, root, fmt.Sprintf("Other/noise-%03d.md", i),
			fmt.Sprintf("review review review review review filler%d", i))
	}
	for i := 0; i < meetings; i++ {
		b2WriteFile(t, root, fmt.Sprintf("Meetings/m-%03d.md", i),
			fmt.Sprintf("Meeting %d notes: a brief review took place.", i))
	}
}

// TestSearchFilteredReportsTruncationAtTheFetchCap is BDD for F7 at the
// Index.SearchFiltered layer directly: hitting the fetch ceiling with more
// raw hits unexamined and fewer than `limit` surviving files must report
// truncated=true, not the same "done" signal as a search that genuinely saw
// everything.
func TestSearchFilteredReportsTruncationAtTheFetchCap(t *testing.T) {
	f7WithLoweredFetchCap(t, 20)

	home, root := t.TempDir(), t.TempDir()
	f7Corpus(t, root, 25, 3) // 28 total matches; cap(20) < noise count(25)
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	keepMeetings := func(relPath string) bool {
		return strings.HasPrefix(relPath, "Meetings/")
	}

	hits, truncated, err := ix.SearchFiltered("review", 10, keepMeetings)
	if err != nil {
		t.Fatalf("SearchFiltered: %v", err)
	}
	if !truncated {
		t.Fatal("truncated = false after the escalating fetch loop hit its " +
			"cap with 25 higher-scoring noise notes ranked ahead of every " +
			"Meetings note and more raw hits left unexamined — the silent " +
			"truncation F7 exists to end")
	}
	if len(hits) >= 3 {
		t.Errorf("got %d Meetings hits out of 3 without escalating past the fetch cap; "+
			"the fixture does not exercise the bound (widen the noise/meetings ratio)", len(hits))
	}

	// The SANE case must NOT be flagged: the unfiltered search over the same
	// corpus finds enough within the (still-lowered) cap and must report
	// truncated=false — this test would be worthless if truncated were always
	// true regardless of whether the loop actually needed the ceiling.
	allHits, allTruncated, err := ix.SearchFiltered("review", 5, nil)
	if err != nil {
		t.Fatalf("SearchFiltered (unfiltered): %v", err)
	}
	if allTruncated {
		t.Error("truncated = true for an unfiltered search that found 5 matches " +
			"well within the fetch cap — a false positive defeats the signal's purpose")
	}
	if len(allHits) != 5 {
		t.Errorf("got %d hits, want exactly 5 (unfiltered, limit=5, cap=20, 28 available)", len(allHits))
	}
}

// TestSearcherSearchReportsFetchTruncationHonestly is BDD for F7 at the
// Searcher.Search / SearchReport layer — the shape a real caller (the
// knowledge_search tool, via SearchOptions.Folder) actually reads. Before the
// fix, buildSearchReport derived Complete from the index-BUILD phase alone,
// so an idle, fully-built index reported Complete: true over a folder-scoped
// answer that silently examined only the top-ranked, mostly-irrelevant slice
// of the corpus.
func TestSearcherSearchReportsFetchTruncationHonestly(t *testing.T) {
	f7WithLoweredFetchCap(t, 20)

	home, root := t.TempDir(), t.TempDir()
	f7Corpus(t, root, 25, 3)
	s, tracker := b4Searcher(t, home, root)
	// The index build itself is finished — Complete must still go false, and
	// for the RIGHT reason (FetchTruncated), never confused with "still
	// indexing".
	if tracker.Progress().InFlight() {
		t.Fatal("test setup: index build unexpectedly still in flight")
	}

	resp, err := s.Search("review", SearchOptions{TopN: 10, Folder: "Meetings"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	hits, report := resp.Results()

	if report.Complete {
		t.Error("report.Complete = true over a folder-scoped search that silently " +
			"stopped at the fetch cap before finding the folder's real matches")
	}
	if !report.FetchTruncated {
		t.Error("report.FetchTruncated = false — the fetch-cap truncation did not reach the report")
	}
	if report.Indeterminate {
		t.Error("report.Indeterminate = true — this is a FINISHED index; the incompleteness " +
			"is the search's own fetch truncation, not an unmeasured index build")
	}
	if len(hits) >= 3 {
		t.Errorf("got %d of 3 real Meetings matches; fixture does not exercise the bound", len(hits))
	}
	if !strings.Contains(report.Statement, "stopped after examining") {
		t.Errorf("report.Statement = %q, want it to name the fetch truncation", report.Statement)
	}
	if strings.Contains(report.Statement, " of ") {
		t.Errorf("report.Statement = %q — invented a ratio for a truncation that never counted a total", report.Statement)
	}
}
