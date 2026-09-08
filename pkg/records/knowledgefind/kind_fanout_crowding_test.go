// Omnipus — regression coverage for the 2026-09-07 code review finding on top
// of ae65b55be ("honour document kind in the text-only find fallback"): the
// kind filter in textOnlyResponse runs AFTER the text search's fan-out, so a
// kind whose real matches are outranked by the OTHER kind gets silently
// crowded out of the fanout window before the kind filter ever sees them.
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

// withKind sets the request's `kind` narrowing. It lives here rather than in
// builders_test.go because no other SQLite-tagged test in this package needs
// it yet, and an unused helper under the untagged file is the lint failure
// notNode's own comment warns about.
func withKind(k string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) {
		kk := generated.VaultFindRequestKind(k)
		r.Kind = &kk
	}
}

// seedKindCrowdedCorpus populates a stubText whose ranking puts every NOTE
// ahead of every ATTACHMENT that matches the same word — the shape that
// crowds a sparse kind out of a rank-ordered fanout window. It writes
// directly to f.text (no store.write): the properties index is nil for every
// test in this file, so nothing here needs a real candidate row, only a text
// hit carrying the kind the fix threads through TextHit.
func seedKindCrowdedCorpus(f *fixture, notes, attachments int) {
	only := make([]string, 0, notes+attachments)
	for i := 0; i < notes; i++ {
		p := fmt.Sprintf("garden/note-%04d.md", i)
		f.text.hits[p] = TextHit{Path: p, SourceHash: "h", Score: 1, Kind: KindNote}
		only = append(only, p)
	}
	for i := 0; i < attachments; i++ {
		p := fmt.Sprintf("garden/attach-%04d.pdf", i)
		f.text.hits[p] = TextHit{Path: p, SourceHash: "h", Score: 1, Kind: KindAttachment}
		only = append(only, p)
	}
	f.text.only = only
}

// TestKindCrowdedFanout_AttachmentSurvivesNoteDomination is the reproduction:
// 210 notes rank ahead of 40 attachments for the same word, comfortably
// crowding every attachment out of textFanout(1) == 200's window. A
// kind=attachment query must still surface all 40 real attachment hits, and
// must never claim completeness over a Selected count that undercounts them.
func TestKindCrowdedFanout_AttachmentSurvivesNoteDomination(t *testing.T) {
	const numNotes = 210
	const numAttachments = 40

	f := newFixture(t)
	d := f.deps()
	d.Store = nil // the properties index is absent — the text-only fallback
	seedKindCrowdedCorpus(f, numNotes, numAttachments)

	resp, err := Find(context.Background(), d, req(
		withWords("report"), withKind(KindAttachment), withLimit(1),
	))
	if err != nil {
		t.Fatalf("Find: unexpected refusal: %v", err)
	}

	// Dimension 1: every real attachment hit is counted, not just whatever
	// survived an unkinded top-N cut. Selected/Evaluated is the total
	// population the response claims to have found — it must equal the true
	// number of matching attachments, not the handful that happened to rank
	// inside a fanout window built without knowing kind mattered.
	if resp.Counts.Selected != numAttachments {
		t.Errorf("Counts.Selected = %d, want %d (all matching attachments) — "+
			"the note-dominated fanout crowded real attachment hits out before "+
			"the kind filter ever saw them", resp.Counts.Selected, numAttachments)
	}
	if resp.Counts.Evaluated != numAttachments {
		t.Errorf("Counts.Evaluated = %d, want %d", resp.Counts.Evaluated, numAttachments)
	}

	// Dimension 2: the response must never assert completeness over a
	// Selected count it knows undercounts the real population. This is the
	// honesty backstop the finding demands even where dimension 1 cannot be
	// fully repaired (Search's own cap).
	if resp.Counts.Selected < numAttachments && resp.Complete {
		t.Errorf("Complete=true but Counts.Selected=%d undercounts the %d real "+
			"attachment matches — this is a false claim of completeness over "+
			"results the kind filter discarded", resp.Counts.Selected, numAttachments)
	}
	assertResponseInvariants(t, resp)
}

// TestKindCrowdedFanout_NoteSurvivesAttachmentDomination is the mirror case
// named in the finding: attachments consuming the fanout must not cost a
// kind=note query its own notes either.
func TestKindCrowdedFanout_NoteSurvivesAttachmentDomination(t *testing.T) {
	const numAttachments = 210
	const numNotes = 40

	f := newFixture(t)
	d := f.deps()
	d.Store = nil
	// seedKindCrowdedCorpus always orders notes ahead of attachments, so this
	// direction (attachments dominating) builds the `only` list by hand
	// instead, with attachments occupying the front of the ranking.
	only := make([]string, 0, numAttachments+numNotes)
	for i := 0; i < numAttachments; i++ {
		p := fmt.Sprintf("garden/attach-%04d.pdf", i)
		f.text.hits[p] = TextHit{Path: p, SourceHash: "h", Score: 1, Kind: KindAttachment}
		only = append(only, p)
	}
	for i := 0; i < numNotes; i++ {
		p := fmt.Sprintf("garden/note-%04d.md", i)
		f.text.hits[p] = TextHit{Path: p, SourceHash: "h", Score: 1, Kind: KindNote}
		only = append(only, p)
	}
	f.text.only = only

	resp, err := Find(context.Background(), d, req(
		withWords("report"), withKind(KindNote), withLimit(1),
	))
	if err != nil {
		t.Fatalf("Find: unexpected refusal: %v", err)
	}

	if resp.Counts.Selected != numNotes {
		t.Errorf("Counts.Selected = %d, want %d (all matching notes) — the "+
			"attachment-dominated fanout crowded real note hits out before the "+
			"kind filter ever saw them", resp.Counts.Selected, numNotes)
	}
	if resp.Counts.Selected < numNotes && resp.Complete {
		t.Errorf("Complete=true but Counts.Selected=%d undercounts the %d real "+
			"note matches", resp.Counts.Selected, numNotes)
	}
	assertResponseInvariants(t, resp)
}
