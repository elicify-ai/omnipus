// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// A2(d) regression: a NON-ZERO `words` result over a text index that does not
// yet reflect the whole vault must NOT be reported complete:true. The prior
// wiring emitted the freshness/coverage signal ONLY on the zero-hit branch
// (checkTextIndexPopulated), so a partial index that returned SOME hits while
// more matching files sat unindexed on disk answered complete:true with no
// signal anywhere the caller reads — the exact false-completeness (R1) the
// tester reported ("words=composio returns 1 hit while 68 files contain it").
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//   -run '^TestFind_StalePartialTextIndex' ./pkg/records/knowledgefind/

package knowledgefind

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestFind_StalePartialTextIndex_NonZeroHits_IsNotClaimedComplete(t *testing.T) {
	f := gardenCorpus(t)

	// The text index returns ONE hit (PL-0002/Fern) but reports that it
	// currently reflects only 1 of the 4 plant files on disk — a partial /
	// stale index that under-reports. This is the non-zero shape the zero-hit
	// refusal branch never sees.
	f.text.only = []string{"garden/plants/PL-0002.md"}
	f.text.fresh = &TextIndexFreshness{
		Built: true, Fresh: false,
		ScannedFiles: 4, IndexedFiles: 1, PendingFiles: 3,
	}

	resp := mustFind(t, f.deps(), req(withType("plant"), withWords("Fern")))

	if len(resp.Rows) != 1 {
		t.Fatalf("expected the single indexed hit to be returned, got %d rows", len(resp.Rows))
	}
	if resp.Complete {
		t.Fatalf("a partial/stale text index must NOT be reported complete:true "+
			"(rows=%d, reason=%q) — this is the false-completeness A2(d) must remove",
			len(resp.Rows), str2(resp.CompleteReason))
	}

	var found bool
	for _, p := range resp.Problems {
		if p.Code == generated.IndexUnavailable &&
			strings.Contains(p.Reason, "1 of") &&
			strings.Contains(p.Reason, "4") &&
			strings.Contains(p.Reason, "under-report") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no freshness/coverage problem quoting the counts (1 of 4, may under-report): %+v", resp.Problems)
	}
}

func TestFind_FreshTextIndex_NonZeroHits_StaysComplete(t *testing.T) {
	// The mirror case: a FRESH index that fully reflects the vault must not be
	// dragged to complete:false by the new signal. This is the guard that the
	// A2(d) fix does not over-warn on a healthy index.
	f := gardenCorpus(t)
	f.text.only = []string{"garden/plants/PL-0002.md"}
	f.text.fresh = &TextIndexFreshness{
		Built: true, Fresh: true,
		ScannedFiles: 4, IndexedFiles: 4, PendingFiles: 0,
	}

	resp := mustFind(t, f.deps(), req(withType("plant"), withWords("Fern")))
	for _, p := range resp.Problems {
		if p.Code == generated.IndexUnavailable {
			t.Fatalf("a fresh index must not emit a freshness/coverage problem: %+v", p)
		}
	}
	if !resp.Complete {
		t.Fatalf("a fresh, fully-indexed vault must stay complete:true, got reason %q", str2(resp.CompleteReason))
	}
}

// str2 unwraps the optional CompleteReason for test messages.
func str2(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
