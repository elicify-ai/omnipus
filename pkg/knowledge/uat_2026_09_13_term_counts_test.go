// Omnipus — regression coverage for the 2026-09-13 UAT finding D-07:
// per-word document counts for declaring a relaxed text match.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import "testing"

func TestUAT_D07_TermDocumentCountsReportEachWordSeparately(t *testing.T) {
	ix := vocabIndex(t) // Pipeline.md holds "prospect"; nothing holds "zzqqxx"

	counts, err := ix.TermDocumentCounts("prospect zzqqxx gardening")
	if err != nil {
		t.Fatalf("TermDocumentCounts: %v", err)
	}
	got := map[string]int{}
	for _, c := range counts {
		got[c.Term] = c.Documents
	}
	if got["prospect"] != 1 {
		t.Errorf("prospect: got %d files, want 1 (Pipeline.md)", got["prospect"])
	}
	if got["gardening"] != 1 {
		t.Errorf("gardening: got %d files, want 1 (Other.md)", got["gardening"])
	}
	if n, ok := got["zzqqxx"]; !ok || n != 0 {
		t.Errorf("zzqqxx: got (%d, present=%v), want an explicit 0 — the absent word is the whole point", n, ok)
	}
	if len(counts) != 3 {
		t.Errorf("expected one entry per distinct word, got %+v", counts)
	}

	// The strict tier finds nothing for the pair, so SearchFiltered falls
	// back and marks the hits — that is the relaxation the counts disclose.
	hits, _, relaxed, err := ix.SearchFiltered("prospect zzqqxx", 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !relaxed || len(hits) == 0 || !hits[0].FallbackMode {
		t.Fatalf("precondition: the pair must be answered by the fallback tier (relaxed=%v, hits=%d)", relaxed, len(hits))
	}
}
