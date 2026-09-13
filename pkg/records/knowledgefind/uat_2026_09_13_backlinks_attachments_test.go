// Omnipus — regression coverage for the 2026-09-13 UAT findings D-30
// (`file.backlinks` always empty: only body links were inverted, never the
// relation values in other notes' frontmatter) and D-29 (every attachment
// reported "freshness unknown; one index holds no content hash" — neither
// index may open an attachment, so presence is the only comparison there is).
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

func withSelect(cols ...string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) { c := append([]string(nil), cols...); r.Select = &c }
}

// TestUAT_D30_BacklinksIncludeRelationValues — Q-19: PRJ-0001 had two
// inbound references through relation properties and file.backlinks was
// empty.
func TestUAT_D30_BacklinksIncludeRelationValues(t *testing.T) {
	f := newFixture(t)
	f.set = plantAndBedSet(t)
	f.write("garden/Bed 1.md", "---\ntype: bed\nid: BED-0001\nlocation: south wall\nsunlight: full\n---\n")
	f.plant(1, "growing", "40.0") // bed: [[Bed 1]] in the frontmatter, no body link
	f.plant(2, "growing", "41.0") // bed: [[Bed 2]] — a control that must NOT appear
	f.write("notes/Diary.md", "---\ntype: \n---\nVisited [[Bed 1]] today.\n")

	resp := mustFind(t, f.deps(), req(withType("bed"), withSelect(records.FileBacklinksProp)))
	if len(resp.Rows) != 1 {
		t.Fatalf("expected the one bed, got %s", Render(resp))
	}
	var cell string
	for _, c := range resp.Rows[0].Cells {
		if c.Property == records.FileBacklinksProp {
			cell = c.Value
		}
	}
	for _, want := range []string{"plant-0001", "Diary"} {
		if !strings.Contains(cell, want) {
			t.Errorf("D-30: file.backlinks must include %q (relation values and body links alike), got %q\n%s",
				want, cell, Render(resp))
		}
	}
	if strings.Contains(cell, "plant-0002") {
		t.Errorf("plant-0002 links to Bed 2, not Bed 1, and must not be a backlink here: %q", cell)
	}
}

// TestUAT_D29_AttachmentsAgreeByPresence — Q-13: 0 of 18 attachments agreed.
func TestUAT_D29_AttachmentsAgreeByPresence(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"img/one.png", "img/two.png"} {
		if err := f.store.UpsertNote(context.Background(), propindex.NoteRows{Path: p, Kind: propindex.KindAttachment}); err != nil {
			t.Fatal(err)
		}
		f.text.hits[p] = TextHit{Path: p, Kind: KindAttachment, Score: 1} // no hash: never opened
	}
	f.text.only = []string{"img/one.png", "img/two.png"}

	resp := mustFind(t, f.deps(), req(withWords("png"), withKind(KindAttachment)))
	if len(resp.Rows) != 2 {
		t.Fatalf("expected two attachments, got %s", Render(resp))
	}
	if resp.Index == nil || resp.Index.Agreeing != 2 || resp.Index.Returned != 2 {
		t.Fatalf("D-29: both attachments are held by both indexes and must agree, got %+v\n%s", resp.Index, Render(resp))
	}
	for _, p := range resp.Problems {
		if strings.Contains(p.Reason, "holds no content hash") {
			t.Errorf("D-29: an attachment must not be reported as hash-unknown: %s", p.Reason)
		}
	}
	if !resp.Complete {
		t.Errorf("two agreeing attachments are a complete answer: %v", resp.CompleteReason)
	}

	// An attachment the text index does NOT hold is still flagged, and the
	// reason says what was compared.
	if err := f.store.UpsertNote(context.Background(), propindex.NoteRows{Path: "img/three.png", Kind: propindex.KindAttachment}); err != nil {
		t.Fatal(err)
	}
	typed := mustFind(t, f.deps(), req(withKind(KindAttachment)))
	var flagged bool
	for _, p := range typed.Problems {
		if strings.Contains(p.Reason, "img/three.png") && strings.Contains(p.Reason, "text index holds no entry for this attachment") {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("an attachment missing from the text index must be named, got %+v", typed.Problems)
	}
}
