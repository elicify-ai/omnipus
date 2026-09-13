// Omnipus — regression coverage for the 2026-09-13 UAT findings D-01 and
// D-07 (the `words` half of knowledge_find).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// seedStoreFromTextHits gives the fixture's properties store one bare row per
// text hit (note or attachment by the hit's own kind), so a words query runs
// through the production shape — a SQLite build with the store open — rather
// than the nil-store fallback that D-01 retired on such builds.
func seedStoreFromTextHits(t *testing.T, f *fixture, hits map[string]TextHit) {
	t.Helper()
	for path, h := range hits {
		kind := h.Kind
		if kind == "" {
			kind = KindNote
		}
		if err := f.store.UpsertNote(context.Background(), propindex.NoteRows{
			Path: path, Kind: kind, SourceHash: h.SourceHash,
		}); err != nil {
			t.Fatalf("UpsertNote(%s): %v", path, err)
		}
	}
}

// asPropindexLessBuild flips the platform seam for one test: the shape of a
// build (records_no_sqlite, mipsle, netbsd, freebsd-arm) where the properties
// index cannot exist at all, and plain words over notes must keep working.
func asPropindexLessBuild(t *testing.T) {
	t.Helper()
	prev := propertyIndexAvailable
	propertyIndexAvailable = false
	t.Cleanup(func() { propertyIndexAvailable = prev })
}

// relaxedStubText is stubText plus the optional TextTermCounter, and it marks
// every hit as coming from the OR-ranked fallback tier.
type relaxedStubText struct {
	*stubText
	counts []generated.VaultTermCount
}

func (s *relaxedStubText) Search(ctx context.Context, words string, limit int) ([]TextHit, error) {
	hits, err := s.stubText.Search(ctx, words, limit)
	for i := range hits {
		hits[i].Relaxed = true
	}
	return hits, err
}

func (s *relaxedStubText) TermDocumentCounts(context.Context, string) ([]generated.VaultTermCount, error) {
	return s.counts, nil
}

// TestUAT_D07_RelaxedWordMatchIsDeclaredWithPerWordCounts — B-40b probe 5:
// `words: "Collision zzqqxx"` (one word present, one absent from the whole
// vault) answered "COMPLETE: yes — 1 of 1 shown" with nothing saying the two
// words were OR-ed. The row may still be returned, but the answer must say
// it is a near match and which word was not found.
func TestUAT_D07_RelaxedWordMatchIsDeclaredWithPerWordCounts(t *testing.T) {
	f := newFixture(t)
	f.write("Projects/Collision.md", "---\ntype: plant\nname: Collision probe body\n---\n")
	relaxed := &relaxedStubText{
		stubText: f.text,
		counts: []generated.VaultTermCount{
			{Term: "collision", Documents: 1},
			{Term: "zzqqxx", Documents: 0},
		},
	}
	relaxed.only = []string{"Projects/Collision.md"}
	d := f.deps()
	d.Text = relaxed

	resp := mustFind(t, d, req(withWords("Collision zzqqxx")))

	if len(resp.Rows) != 1 {
		t.Fatalf("the near match must still be returned (1 row), got %d rows", len(resp.Rows))
	}
	if resp.Complete {
		t.Fatalf("a relaxed answer must not claim COMPLETE: yes — that is exactly the D-07 defect")
	}
	var found *generated.RecordProblem
	for i := range resp.Problems {
		if resp.Problems[i].Code == generated.TextSearchRelaxed {
			found = &resp.Problems[i]
		}
	}
	if found == nil {
		t.Fatalf("no text_search_relaxed problem in %+v", resp.Problems)
	}
	for _, want := range []string{`every word of "Collision zzqqxx"`, "collision: 1", "zzqqxx: 0"} {
		if !strings.Contains(found.Reason, want) {
			t.Errorf("problem reason %q does not carry %q", found.Reason, want)
		}
	}
	rendered := Render(resp)
	if !strings.Contains(rendered, "COMPLETE: no") || !strings.Contains(rendered, "zzqqxx: 0") {
		t.Errorf("the rendered answer must say the match was relaxed and which word was missing:\n%s", rendered)
	}
}

// TestUAT_D01_SingleWordNearSpellingIsDeclared — B-40's false positive:
// `words: "bashwrittenmarker3"` returned the note holding
// "bashwrittenmarker2" (edit distance 1) as an exact hit.
func TestUAT_D01_SingleWordNearSpellingIsDeclared(t *testing.T) {
	f := newFixture(t)
	f.write("Projects/Bash Written.md", "---\ntype: plant\nname: bashwrittenmarker2\n---\n")
	relaxed := &relaxedStubText{
		stubText: f.text,
		counts:   []generated.VaultTermCount{{Term: "bashwrittenmarker3", Documents: 0}},
	}
	relaxed.only = []string{"Projects/Bash Written.md"}
	d := f.deps()
	d.Text = relaxed

	resp := mustFind(t, d, req(withWords("bashwrittenmarker3")))
	if resp.Complete {
		t.Fatalf("a near-spelling match must not claim COMPLETE: yes")
	}
	rendered := Render(resp)
	if !strings.Contains(rendered, `no indexed file contains "bashwrittenmarker3"`) {
		t.Errorf("the answer must say the word itself was not found:\n%s", rendered)
	}
}

// TestUAT_D07_ExactMatchIsNotDeclaredRelaxed — the disclosure must not fire
// on an exact (AND-tier) answer, or every search would read as loosened.
func TestUAT_D07_ExactMatchIsNotDeclaredRelaxed(t *testing.T) {
	f := newFixture(t)
	f.write("Projects/Collision.md", "---\ntype: plant\nname: Collision probe body\n---\n")
	f.text.only = []string{"Projects/Collision.md"}
	resp := mustFind(t, f.deps(), req(withWords("Collision probe body")))
	for _, p := range resp.Problems {
		if p.Code == generated.TextSearchRelaxed {
			t.Fatalf("an exact match was declared relaxed: %+v", p)
		}
	}
	if !resp.Complete {
		t.Fatalf("an exact, fully-evaluated answer must stay complete: %v", resp.CompleteReason)
	}
}

// TestUAT_D01_WordsOnlyFailsClosedWhenPropertiesIndexUnavailable — the S1
// half of D-01: while the properties index is not open, a `words` query
// answered from the text index alone and asserted COMPLETE: yes (the only
// tell being the silently absent INDEX line), where the identical query with
// a `type` was honestly refused. On a build that has a properties index the
// two must agree: refuse, and say why.
func TestUAT_D01_WordsOnlyFailsClosedWhenPropertiesIndexUnavailable(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("this build has no properties index; the platform carve-out applies instead")
	}
	f := newFixture(t)
	f.write("Projects/Bash Written.md", "---\ntype: plant\nname: bashwrittenmarker\n---\n")
	f.text.only = []string{"Projects/Bash Written.md"}
	d := f.deps()
	d.Store = nil
	d.StoreUnavailableReason = "rebuilding it failed: disk full"

	resp := mustRefuse(t, d, req(withWords("bashwrittenmarker")))
	if len(resp.Problems) == 0 || resp.Problems[0].Code != generated.IndexUnavailable {
		t.Fatalf("expected an index_unavailable refusal, got %+v", resp.Problems)
	}
	if !strings.Contains(resp.Problems[0].Reason, "disk full") {
		t.Errorf("the refusal must name the actual cause, got %q", resp.Problems[0].Reason)
	}
	if strings.Contains(resp.Problems[0].Reason, "not open, so no record") &&
		!strings.Contains(resp.Problems[0].Reason, "rebuilding it failed") {
		t.Errorf("the refusal must carry the store's own reason, got %q", resp.Problems[0].Reason)
	}
}
