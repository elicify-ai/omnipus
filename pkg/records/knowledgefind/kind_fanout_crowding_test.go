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
// 1,200 notes rank ahead of 40 attachments for the same word, comfortably
// crowding every attachment out of textFanout(DefaultLimit) == 1,000's
// window. A kind=attachment query must still surface all 40 real attachment
// hits, and must never claim completeness over a Selected count that
// undercounts them.
//
// F5: this used to seed only 210 notes and ask withLimit(1) — a page size
// so small that Shown(<=1) was ALWAYS less than Evaluated(40), which forces
// finishVerdict to set Complete=false for the PAGING reason alone
// (assemble.go's "%d of %d shown" branch), regardless of anything the
// exhaustion signal below it did. Dimension 2 (the `if ... && resp.Complete`
// check) could therefore never independently fail: resp.Complete was
// already false before that line ever ran. 1,200 notes keeps the crowding
// scenario (want=1,001 stays under the note count, so the first window is
// still all notes) while the DEFAULT limit (50, omitted here) both
// preserves that crowding AND comfortably fits all 40 real attachments on
// one page (Shown == Evaluated == 40 whenever the count is right), so
// dimension 2 is now driven ONLY by the completeness signal it exists to
// check.
func TestKindCrowdedFanout_AttachmentSurvivesNoteDomination(t *testing.T) {
	const numNotes = 1200
	const numAttachments = 40

	f := newFixture(t)
	d := f.deps()
	d.Store = nil // the properties index is absent — the text-only fallback
	seedKindCrowdedCorpus(f, numNotes, numAttachments)

	resp, err := Find(context.Background(), d, req(
		withWords("report"), withKind(KindAttachment),
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
	if resp.Counts.Shown != numAttachments {
		t.Errorf("Counts.Shown = %d, want %d — this test deliberately sizes the page so no paging "+
			"reason can set Complete=false; a mismatch here means dimension 2 below is not testing "+
			"what it claims to", resp.Counts.Shown, numAttachments)
	}

	// Dimension 2 — F5's honesty backstop, made a REAL discriminator: the
	// original shape here was `if Selected < numAttachments && Complete`,
	// which the finding correctly calls dead — not merely because of
	// withLimit(1) above, but structurally: its own guard clause
	// (Selected < numAttachments) is a STRICT SUBSET of dimension 1's
	// `Selected != numAttachments` check immediately above, using the same
	// non-halting t.Errorf. Whenever that guard would be true, dimension 1
	// has ALREADY failed the test for the identical reason — so this
	// check could never be the thing that flips a passing test to
	// failing; at best it adds detail to an already-failed one. That is
	// the precise meaning of "dead" here, and pagination was a second,
	// independent way the same line could never fire, not the only one.
	//
	// The fix is the OPPOSITE polarity, which genuinely is independent of
	// dimension 1's own outcome: this stub (stubText) has no fetch ceiling
	// of its own — see stubText.SearchDeep's doc comment — so whenever the
	// count is proven correct, the search really IS exhaustive, and
	// Complete must say so. This fires on ITS OWN whenever a regression
	// makes the answer over-conservative (reports incomplete despite having
	// actually proven completeness) even though dimension 1 stays green —
	// the false-NEGATIVE mirror of the false-POSITIVE bug F3 guards via
	// TestKindNarrowedDeepReask_CannotProveExhaustionAtSearcherCeiling
	// (same package), whose fixture — a searcher WITH a fetch ceiling —
	// is what makes the false-positive direction of this same assertion
	// shape independently reachable.
	if resp.Counts.Selected == numAttachments && !resp.Complete {
		t.Errorf("Counts.Selected correctly reports all %d real attachment matches, but Complete=false — "+
			"this stub's corpus has no fetch ceiling of its own, so the search genuinely IS exhaustive "+
			"here and must say so\nfull response:\n%s", numAttachments, Render(resp))
	}
	assertResponseInvariants(t, resp)
}

// TestKindCrowdedFanout_NoteSurvivesAttachmentDomination is the mirror case
// named in the finding: attachments consuming the fanout must not cost a
// kind=note query its own notes either.
//
// F5: scaled up from 210/40 to 1,200/40 and withLimit(1) dropped, for the
// same reason as the mirror test above — a page size below the real match
// count forces Complete=false for a paging reason regardless of the
// exhaustion signal, which made dimension 2 unable to fail independently.
func TestKindCrowdedFanout_NoteSurvivesAttachmentDomination(t *testing.T) {
	const numAttachments = 1200
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
		withWords("report"), withKind(KindNote),
	))
	if err != nil {
		t.Fatalf("Find: unexpected refusal: %v", err)
	}

	if resp.Counts.Selected != numNotes {
		t.Errorf("Counts.Selected = %d, want %d (all matching notes) — the "+
			"attachment-dominated fanout crowded real note hits out before the "+
			"kind filter ever saw them", resp.Counts.Selected, numNotes)
	}
	if resp.Counts.Shown != numNotes {
		t.Errorf("Counts.Shown = %d, want %d — this test deliberately sizes the page so no paging "+
			"reason can set Complete=false; a mismatch here means dimension 2 below is not testing "+
			"what it claims to", resp.Counts.Shown, numNotes)
	}
	// Dimension 2 — see TestKindCrowdedFanout_AttachmentSurvivesNoteDomination's
	// own comment for why this is the independent, non-dead shape (the
	// mirror of `Selected < numNotes && Complete`, which the finding
	// correctly calls dead: its guard is a strict subset of the
	// `Selected != numNotes` check above, so it could never be the reason
	// a passing test starts failing).
	if resp.Counts.Selected == numNotes && !resp.Complete {
		t.Errorf("Counts.Selected correctly reports all %d real note matches, but Complete=false — "+
			"this stub's corpus has no fetch ceiling of its own, so the search genuinely IS exhaustive "+
			"here and must say so\nfull response:\n%s", numNotes, Render(resp))
	}
	assertResponseInvariants(t, resp)
}
