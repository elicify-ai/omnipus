// Omnipus — regression coverage for F3 and F5's second bullet: fetchWordHits'
// re-ask at propindex.BoundSurvivors used to treat its OWN searcher's fetch
// ceiling as proof the corpus was exhausted.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// stubTextCeilinged is a TextSearcher whose Search is backed by an internal
// fetch ceiling BELOW propindex.BoundSurvivors — the exact shape
// knowledge.Index's real production adapter has (F3's root cause:
// indexSearchMaxFetch caps at 2048 raw segment hits, far under
// BoundSurvivors' 10,000). It deliberately does NOT implement
// TextDeepSearcher: unlike stubText (fixture_test.go), which can always
// answer exhaustion honestly because it holds its whole corpus in memory
// with no ceiling of its own, this type models a searcher that CANNOT prove
// exhaustion once the corpus exceeds what it is willing to fetch in one
// call — the state fetchWordHits must stay honest about rather than
// inferring away.
type stubTextCeilinged struct {
	hits    map[string]TextHit
	only    []string // the full corpus, in RANK order
	ceiling int      // the simulated internal fetch ceiling
}

var _ TextSearcher = (*stubTextCeilinged)(nil)

func (s *stubTextCeilinged) Search(_ context.Context, _ string, limit int) ([]TextHit, error) {
	n := limit
	if s.ceiling > 0 && n > s.ceiling {
		// The searcher's OWN ceiling silently caps the ask — exactly what
		// knowledge.Index.SearchFiltered does at indexSearchMaxFetch, and
		// exactly what knowledge.Index.Search then hides by discarding the
		// truncated flag SearchFiltered reports for that case.
		n = s.ceiling
	}
	var out []TextHit
	for _, p := range s.only {
		if len(out) >= n {
			break
		}
		if h, ok := s.hits[p]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func (s *stubTextCeilinged) NearestTerms(context.Context, string, int) ([]generated.VaultTermCount, error) {
	return nil, nil
}

func (s *stubTextCeilinged) SourceHash(_ context.Context, path string) (string, bool, error) {
	h, ok := s.hits[path]
	if !ok {
		return "", false, nil
	}
	return h.SourceHash, true, nil
}

func (s *stubTextCeilinged) Populated(context.Context) (bool, error) { return true, nil }

// seedCeilingedCrowdedCorpus builds a corpus with notes both BEFORE and
// AFTER the target attachments in rank order, and a total size larger than
// the simulated ceiling — so the attachments all fall inside the visible
// window (every real attachment is genuinely captured), while additional
// notes ranked below the ceiling are never seen. This is what makes the
// exhaustion claim UNPROVABLE from inside fetchWordHits: nothing in the
// response it gets back distinguishes "everything past the ceiling is more
// of the crowding kind" (true here) from "there could be more of the target
// kind past the ceiling" (indistinguishable from here without proof).
func seedCeilingedCrowdedCorpus(notesBefore, attachments, notesAfter int) *stubTextCeilinged {
	s := &stubTextCeilinged{hits: map[string]TextHit{}}
	for i := 0; i < notesBefore; i++ {
		p := fmt.Sprintf("garden/note-before-%05d.md", i)
		s.hits[p] = TextHit{Path: p, SourceHash: "h", Score: 1, Kind: KindNote}
		s.only = append(s.only, p)
	}
	for i := 0; i < attachments; i++ {
		p := fmt.Sprintf("garden/attach-%05d.pdf", i)
		s.hits[p] = TextHit{Path: p, SourceHash: "h", Score: 1, Kind: KindAttachment}
		s.only = append(s.only, p)
	}
	for i := 0; i < notesAfter; i++ {
		p := fmt.Sprintf("garden/note-after-%05d.md", i)
		s.hits[p] = TextHit{Path: p, SourceHash: "h", Score: 1, Kind: KindNote}
		s.only = append(s.only, p)
	}
	return s
}

// TestKindNarrowedDeepReask_CannotProveExhaustionAtSearcherCeiling is F3's
// own regression, and also what makes find.go's `default:` truncation-
// honesty branch (F5's second bullet) reachable: before this fix it had NO
// coverage, and the mutation the review supplied for it —
// `return filtered, false, nil` in place of `return filtered, true, nil` —
// stayed green, because no fixture ever reached the branch at all.
//
// The corpus here: 1,200 notes, then 50 attachments, then 500 MORE notes —
// 1,750 documents total, comfortably larger than the simulated 2,000-item
// searcher ceiling is NOT exceeded by the notes+attachments alone (1,250 <
// 2,000) but IS exceeded once the trailing notes are counted (1,750 <
// 2,000 too — see the ceiling comment below for why it is set where it is).
// All 50 real attachments genuinely fall inside the ceiling's visible
// window, so fetchWordHits' own kind-narrowing recovery correctly finds
// every one of them (Selected == 50, matching kind_fanout_crowding_test.go's
// own dimension 1) — the defect is purely in the COMPLETENESS claim made
// about that count, which is what dimension 2 here checks.
func TestKindNarrowedDeepReask_CannotProveExhaustionAtSearcherCeiling(t *testing.T) {
	const notesBefore = 1200
	const numAttachments = 50
	const notesAfter = 500 // ranked BELOW the ceiling — never seen, and never counted

	// The ceiling sits exactly at notesBefore+numAttachments: every real
	// attachment is inside it, but the corpus (notesBefore+numAttachments+
	// notesAfter) is larger than it, so a re-ask at BoundSurvivors is
	// silently capped by the searcher's own ceiling rather than by the
	// corpus running out — the one case Search's plain contract cannot
	// distinguish from genuine exhaustion.
	const ceiling = notesBefore + numAttachments

	text := seedCeilingedCrowdedCorpus(notesBefore, numAttachments, notesAfter)
	text.ceiling = ceiling

	f := newFixture(t)
	d := f.deps()
	d.Store = nil // the properties index is absent — the text-only fallback
	d.Text = text

	// No withLimit: DefaultLimit (50) keeps textFanout(50) == 1,000, well
	// under notesBefore (1,200), so the first fanout+1 window is entirely
	// notes — the same crowding shape kind_fanout_crowding_test.go's own
	// tests reproduce — AND it is >= numAttachments (50), so every real
	// attachment fits on the one page: Shown == Evaluated whenever the
	// count itself is correct, exactly what decouples dimension 2 from
	// pagination-forced incompleteness (F5's first bullet, fixed the same
	// way in kind_fanout_crowding_test.go).
	resp, err := Find(context.Background(), d, req(
		withWords("report"), withKind(KindAttachment),
	))
	if err != nil {
		t.Fatalf("Find: unexpected refusal: %v", err)
	}

	// Dimension 1: every real attachment hit is counted — the kind-fanout
	// recovery this test's corpus shape exercises works correctly even
	// under a ceilinged searcher, because all 50 real matches fall inside
	// the visible window.
	if resp.Counts.Selected != numAttachments {
		t.Fatalf("Counts.Selected = %d, want %d (all matching attachments) — this test's corpus is "+
			"built so every real attachment is inside the searcher's ceiling; a mismatch here is a "+
			"different defect than the one this test guards", resp.Counts.Selected, numAttachments)
	}

	// Dimension 2 — F3 itself: the response must NOT claim completeness it
	// cannot back up. 1,750 real documents exist; the searcher's ceiling
	// only ever revealed 1,250 of them. Nothing fetchWordHits received back
	// proves the other 500 are all notes (that is a fact of THIS fixture,
	// not something the code under test could ever observe) — so the
	// honest answer is Complete:false with a truncation problem, never a
	// confident yes.
	if resp.Complete {
		t.Errorf("Complete = true, but the searcher's own fetch ceiling (%d) was reached before the "+
			"corpus (%d documents) was exhausted — nothing proves no further attachment exists past "+
			"where the searcher stopped looking. This is F3: exhaustion was inferred from "+
			"len(hits) < BoundSurvivors, which a ceilinged searcher can make true by coincidence "+
			"regardless of the real corpus size.\nfull response:\n%s", ceiling, notesBefore+numAttachments+notesAfter, Render(resp))
	}
	foundTruncated := false
	for _, p := range resp.Problems {
		if p.Code == generated.TextSearchTruncated {
			foundTruncated = true
		}
	}
	if !foundTruncated {
		t.Errorf("no text_search_truncated problem was reported, even though the searcher's ceiling "+
			"was reached without proving exhaustion\nfull response:\n%s", Render(resp))
	}
	assertResponseInvariants(t, resp)
}
