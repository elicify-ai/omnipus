// Omnipus — regression coverage for the 2026-09-13 UAT finding D-130: at the
// limit the SPA always asks for, the web search's Notes group contained
// records — every record past the records group's own limit fell through
// the kind=note query and was filed as a note.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"testing"
)

// TestUAT_D130_PlainNotesOnlyRejectsRecordsBeforeAnyLimit — the notes
// partition is decided per candidate at the store ("declares a type or
// not"), so no limit on a records partition can leak a record into it.
func TestUAT_D130_PlainNotesOnlyRejectsRecordsBeforeAnyLimit(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 5; i++ {
		f.plant(i, "growing", "40.0")
	}
	f.write("notes/plain.md", "# Plain\n\ngrowing things\n")
	f.text.only = []string{
		"garden/plant-0001.md", "garden/plant-0002.md", "garden/plant-0003.md",
		"garden/plant-0004.md", "garden/plant-0005.md", "notes/plain.md",
	}

	// The documented kind=note: every markdown note, records included — the
	// tool door's meaning is unchanged.
	d := f.depsWithText()
	all := mustFind(t, d, req(withWords("growing"), withKind(KindNote), withLimit(2)))
	if all.Counts.Evaluated != 6 {
		t.Fatalf("kind=note without the switch must still see all six notes, evaluated %d:\n%s",
			all.Counts.Evaluated, Render(all))
	}

	// The in-process partition: only the typeless note survives, whatever
	// the limit.
	d.PlainNotesOnly = true
	plain := mustFind(t, d, req(withWords("growing"), withKind(KindNote), withLimit(2)))
	if len(plain.Rows) != 1 || plain.Rows[0].Path != "notes/plain.md" {
		t.Fatalf("D-130: PlainNotesOnly must return only the typeless note, got %s", Render(plain))
	}
	if plain.Counts.Evaluated != 1 {
		t.Errorf("D-130: the evaluated count must describe the notes partition only (1), got %d", plain.Counts.Evaluated)
	}
	if !plain.Complete {
		t.Errorf("one typeless note under limit 2 is a complete answer: %v", plain.CompleteReason)
	}

	// kind=record is untouched by the switch.
	recs := mustFind(t, d, req(withWords("growing"), withKind(KindRecord), withLimit(10)))
	if len(recs.Rows) != 5 {
		t.Errorf("kind=record must be unaffected by PlainNotesOnly, got %d rows", len(recs.Rows))
	}
}
