// Omnipus — KB-7a: AND-first querying with an OR-ranked, typo-tolerant
// fallback (docs/internal/defect-list-knowledge-base-ux-2026-09-08.md,
// KB-6/KB-7, ratified 2026-09-08).
//
// The reproduction case mirrors the founder's own measurement on his real
// 784-note vault, query "investment report": zero notes contained both
// terms, twelve contained "investment", two hundred twelve contained
// "report", and the pre-fix OR search returned ~224 results — 29% of the
// whole vault — with the genuinely relevant notes buried in a flat,
// unranked-looking list.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"fmt"
	"strings"
	"testing"
)

// kb7Corpus builds a vault mirroring the measured ratio: a handful of notes
// mentioning the RARE term, many more mentioning the COMMON term, and
// crucially ZERO notes mentioning both — the exact shape that made the old
// OR-only search return the entire common-term population for a
// two-word query.
func kb7Corpus(t *testing.T, root string, rareCount, commonCount int) {
	t.Helper()
	for i := 0; i < rareCount; i++ {
		b2WriteFile(t, root, fmt.Sprintf("rare/r%03d.md", i),
			fmt.Sprintf("Note %d discusses the investment thesis for this quarter in detail.", i))
	}
	for i := 0; i < commonCount; i++ {
		b2WriteFile(t, root, fmt.Sprintf("common/c%03d.md", i),
			fmt.Sprintf("Note %d: each one reports back with real evidence, unrelated topic %d.", i, i))
	}
}

// TestSearch_ANDFirst_ZeroOverlapFallsBackAndRanksRareTermAboveNoise is the
// task's own "single most valuable test": REPRODUCE first (asserting the
// AND tier alone finds nothing, exactly like the founder's "notes
// containing BOTH terms: 0" measurement), then prove the fix — the
// OR-fallback ranks every rare-term ("investment") note ABOVE every
// common-term-only ("report") note, and the response says it fell back.
func TestSearch_ANDFirst_ZeroOverlapFallsBackAndRanksRareTermAboveNoise(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	const rareCount, commonCount = 12, 40 // ratio mirrors the founder's 12-vs-212
	kb7Corpus(t, root, rareCount, commonCount)
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	// REPRODUCE: confirm the fixture actually has zero notes containing
	// both terms — otherwise this test would not be exercising KB-7a at
	// all. buildAndQuery is the AND tier itself; asking it directly (not
	// through the OR-fallback searchRaw wraps around it) proves the strict
	// tier alone finds nothing, matching the founder's own measurement.
	andOnly, andTotal, err := ix.runSearch(buildAndQuery([]string{"investment", "report"}), 50, "investment report")
	if err != nil {
		t.Fatalf("AND-tier query: %v", err)
	}
	if andTotal != 0 || len(andOnly) != 0 {
		t.Fatalf("fixture is meant to have ZERO notes containing both terms, got %d raw hits (total=%d): %v — "+
			"the AND-first behavior this test exists to prove is not being reproduced",
			len(andOnly), andTotal, pathsOf(andOnly))
	}

	// THE FIX: the full search (AND-first, OR-ranked fallback) must still
	// answer — never a dead end — and must rank every rare-term note above
	// every common-term-only note.
	hits, truncated, fellBack, err := ix.SearchFiltered("investment report", rareCount+commonCount, nil)
	if err != nil {
		t.Fatalf("SearchFiltered: %v", err)
	}
	if truncated {
		t.Fatalf("unexpected truncation for a %d-note corpus", rareCount+commonCount)
	}
	if !fellBack {
		t.Fatal("fellBack = false; a query whose AND tier found nothing must report that it " +
			"relaxed to the OR-ranked fallback (KB-7a disclosure)")
	}
	if len(hits) == 0 {
		t.Fatal("OR-fallback returned zero hits; a too-narrow AND query must degrade, not dead-end")
	}
	for _, h := range hits {
		if !h.FallbackMode {
			t.Errorf("hit %q does not carry FallbackMode=true even though the query fell back", h.Path)
		}
	}

	// The core relevance assertion: every "rare/" (investment) hit outranks
	// every "common/" (report-only) hit. BM25's IDF weighting already does
	// this (rank.go's own long-standing design note); KB-7a's job is to
	// make sure the fallback path a caller actually reaches preserves it
	// and that AND-first does not somehow invert it.
	lastRareRank, firstCommonRank := -1, -1
	for i, h := range hits {
		switch {
		case strings.HasPrefix(h.Path, "rare/"):
			lastRareRank = i
		case strings.HasPrefix(h.Path, "common/") && firstCommonRank == -1:
			firstCommonRank = i
		}
	}
	if firstCommonRank == -1 {
		t.Fatal("fixture check: no common-term note was returned at all; the ranking claim is untestable")
	}
	if lastRareRank == -1 {
		t.Fatal("fixture check: no rare-term note was returned at all; the ranking claim is untestable")
	}
	if lastRareRank > firstCommonRank {
		t.Errorf("a common-term-only note (rank %d) outranks the LAST rare-term note (rank %d); "+
			"the founder's own reported bad hit matched the common term alone — this is exactly "+
			"the failure KB-7 exists to prevent. Full order: %v", firstCommonRank, lastRareRank, pathsOf(hits))
	}
}

// TestSearch_ANDFirst_ExactMatchNeverFallsBack proves the negative: when
// the corpus DOES have a note containing every query term, the AND tier
// answers directly and the response must NOT claim a relaxed match.
func TestSearch_ANDFirst_ExactMatchNeverFallsBack(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "both.md", "The investment committee approved the quarterly report today.")
	b2WriteFile(t, root, "report-only.md", "Each one reports back with real evidence.")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, _, fellBack, err := ix.SearchFiltered("investment report", 10, nil)
	if err != nil {
		t.Fatalf("SearchFiltered: %v", err)
	}
	if fellBack {
		t.Error("fellBack = true for a query whose AND tier found a real match; " +
			"an exact answer must never be reported as relaxed")
	}
	if len(hits) == 0 || hits[0].Path != "both.md" {
		t.Errorf("expected both.md ranked first, got %v", pathsOf(hits))
	}
	for _, h := range hits {
		if h.FallbackMode {
			t.Errorf("hit %q carries FallbackMode=true on an exact AND-tier answer", h.Path)
		}
	}
}

// TestSearchReport_DisclosesRelaxedMatch pins the honesty-layer half of
// KB-7a: Searcher.Search's SearchReport must say, in its Statement, that
// the answer was relaxed — a reader must not mistake an OR-fallback answer
// for an exact one.
func TestSearchReport_DisclosesRelaxedMatch(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	kb7Corpus(t, root, 3, 5)
	s, tracker := b4Searcher(t, home, root)
	tracker.Finish(false)

	resp, err := s.Search("investment report", SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	_, report := resp.Results()
	if !report.RelaxedToOrRanking {
		t.Fatal("report.RelaxedToOrRanking = false for a query whose AND tier found nothing")
	}
	if report.Statement == "" {
		t.Fatal("report.Statement is empty; KB-7a requires disclosing a relaxed match even when " +
			"the search is otherwise complete and unclamped (US-6 AS-4 only suppresses the " +
			"INCOMPLETENESS notice, not this one)")
	}
	if !strings.Contains(strings.ToLower(report.Statement), "closest match") &&
		!strings.Contains(strings.ToLower(report.Statement), "some may contain only some") {
		t.Errorf("report.Statement = %q does not disclose the relaxed match in readable terms", report.Statement)
	}
}

// TestSearchReport_ExactMatchStaysSilentAboutRelaxation is the negative
// case for the disclosure sentence: a fully exact, complete, unclamped
// search must add nothing (US-6 AS-4's "no notice" rule extends to this
// new caveat too, not just index completeness).
func TestSearchReport_ExactMatchStaysSilentAboutRelaxation(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "both.md", "The investment committee approved the quarterly report today.")
	s, tracker := b4Searcher(t, home, root)
	tracker.Finish(false)

	resp, err := s.Search("investment report", SearchOptions{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	_, report := resp.Results()
	if report.RelaxedToOrRanking {
		t.Error("report.RelaxedToOrRanking = true for an exact AND-tier match")
	}
	if report.Statement != "" {
		t.Errorf("report.Statement = %q; an exact, complete, unclamped search must stay silent", report.Statement)
	}
}

// TestSearch_FuzzyMatchRanksBelowExactMatch pins KB-7's typo-tolerance
// requirement together with its own stated safety rail: "fuzzy matches
// MUST rank below exact ones, or precision collapses invisibly." A note
// containing the literal (mistyped-but-close) term must never outrank a
// note the caller can only reach via SetFuzziness(1)'s edit-distance pass.
func TestSearch_FuzzyMatchRanksBelowExactMatch(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	// "invesmtent" is one transposition away from "investment" — within
	// SetFuzziness(1)'s edit-distance-1 budget, and close enough for
	// bleve's Levenshtein automaton to bridge for a word this short (see
	// TestSearch_SingleTermTypoStillFindsAFuzzyMatch, which pins that pair
	// in isolation).
	//
	// A second term ("pipeline") that NEITHER note contains is added to
	// the query on purpose: with a single-term query, the AND tier alone
	// would already succeed on exact.md and searchRaw would never try the
	// OR-fallback tier at all — meaning typo-only.md, reachable ONLY
	// through the fuzzy clause THAT TIER carries, would never even be
	// looked for. Forcing the AND tier to fail globally (no note has
	// "pipeline") is what actually exercises the comparison this test
	// exists to make: an exact match and a fuzzy-only match landing in the
	// SAME fallback result set, ordered correctly.
	b2WriteFile(t, root, "typo-only.md", "Our invesmtent thesis is unchanged this year.")
	b2WriteFile(t, root, "exact.md", "Our investment thesis is unchanged this year.")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, _, fellBack, err := ix.SearchFiltered("investment pipeline", 10, nil)
	if err != nil {
		t.Fatalf("SearchFiltered: %v", err)
	}
	if !fellBack {
		t.Fatal("fellBack = false; neither note contains \"pipeline\" so the AND tier must have " +
			"found nothing and relaxed to the OR-fallback")
	}
	if len(hits) < 2 {
		t.Fatalf("expected both notes to be found (one exact, one only reachable via fuzzy "+
			"fallback), got %v", pathsOf(hits))
	}
	if hits[0].Path != "exact.md" {
		t.Errorf("the EXACT match must rank first, got order %v — fuzzy matches must rank "+
			"below exact ones (KB-7's own stated rail) or precision collapses invisibly",
			pathsOf(hits))
	}
}

// TestSearch_SingleTermTypoStillFindsAFuzzyMatch is the positive fuzzy case
// in isolation: a lone mistyped word, with nothing exact to match, must
// still surface the close note rather than a silent zero (KB-7: "a typo
// returns a silent zero-result" was the defect).
func TestSearch_SingleTermTypoStillFindsAFuzzyMatch(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "only.md", "Our investment thesis is unchanged this quarter.")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, _, fellBack, err := ix.SearchFiltered("invesmtent", 10, nil)
	if err != nil {
		t.Fatalf("SearchFiltered: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "only.md" {
		t.Fatalf("a one-edit-away typo must still find the note via fuzzy fallback, got %v", pathsOf(hits))
	}
	if !fellBack {
		t.Error("fellBack = false for a typo that only matched via the fuzzy OR-fallback tier")
	}
}
