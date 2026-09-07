// Omnipus — FR-020, FR-021a, FR-021b, FR-021d, FR-020b: what the index stores
// and what comes back out.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestRoundTrip_DecodeMatchesTheParser is the anti-drift guard.
//
// The index has a projection (BuildNoteRows) and a decode (StoredProp.Typed).
// Two functions that translate between the same two shapes are two places to be
// wrong, and the way they go wrong is quiet: a value that survives storage in a
// slightly different form still compares, still renders, and answers a question
// nobody asked. So the assertion is not "the round trip is lossless" in the
// abstract — it is that decoding out of the index produces EXACTLY what
// records.ResolveProperty produces from the original file, for every one of the
// seven declared types.
func TestRoundTrip_DecodeMatchesTheParser(t *testing.T) {
	sc := plantSchema(t)
	src := `---
type: plant
id: PL-0001
species: Monstera deliciosa
condition: growing
planted: 2026-03-14T09:30:00+02:00
height_cm: 41.250
cuttings: -7
bed: "[[Bed 3#Shelf|the top shelf]]"
keeper: "[[Rosa]]"
labels: [indoor, humid, "trailing"]
---
`
	rows := note(t, "garden/plant-0001.md", sc, src)
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, rows)

	got := collect(t, store, Selector{RecordType: "plant"})
	if len(got) != 1 {
		t.Fatalf("expected exactly one candidate, got %d", len(got))
	}
	cand := got[0]

	rec := records.ParseRecord("garden/plant-0001.md", []byte(src))
	for _, name := range sc.PropertyOrder {
		prop, ok := sc.Property(name)
		if !ok {
			t.Fatalf("schema lost property %q", name)
		}
		want := records.ResolveProperty(rec, prop)

		sp, ok := cand.Prop(name)
		if !ok {
			t.Errorf("property %q is missing from the candidate entirely; "+
				"a record that loses a property it has on disk answers a question nobody asked", name)
			continue
		}
		have, err := sp.Typed(prop)
		if err != nil {
			t.Errorf("decoding %q: %v", name, err)
			continue
		}
		if have.State != want.State {
			t.Errorf("property %q: state %v round-tripped as %v (FR-021b)", name, want.State, have.State)
		}
		if len(have.Values) != len(want.Values) {
			t.Errorf("property %q: %d values in, %d out", name, len(want.Values), len(have.Values))
			continue
		}
		for i := range want.Values {
			if !reflect.DeepEqual(have.Values[i], want.Values[i]) {
				t.Errorf("property %q element %d:\n  parser: %#v\n  index:  %#v",
					name, i, want.Values[i], have.Values[i])
			}
			if want.SourcePosition(i) != have.SourcePosition(i) {
				t.Errorf("property %q element %d: source position %d became %d",
					name, i, want.SourcePosition(i), have.SourcePosition(i))
			}
		}
	}
}

// TestStorage_ThreeStatesSurviveStorage is FR-021b.
//
// "Never in the typed column" leaves NULL there, and NULL is the ABSENCE
// representation — so without a separate flag a corrupt value and a missing one
// become the same thing in storage. They are asserted here as three distinct
// outcomes, because the failure is that two of them collapse into one.
func TestStorage_ThreeStatesSurviveStorage(t *testing.T) {
	sc := plantSchema(t)
	// `condition` holds a value outside the declared set; `planted` is absent;
	// `species` is present and conforming.
	src := `---
type: plant
id: PL-0002
species: Ficus lyrata
condition: thriving
---
`
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/plant-0002.md", sc, src))

	got := collect(t, store, Selector{RecordType: "plant"})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}

	for _, tc := range []struct {
		prop string
		want records.PropertyState
		why  string
	}{
		{"species", records.StatePresent, "a conforming value"},
		{"condition", records.StateNonConforming,
			"R-4: a value outside the declared set compares false for every operator AND is reported — it is not absence"},
		{"planted", records.StateAbsent,
			"FR-007: an absent property is distinct from every value of that property, and must not be reported as a problem"},
	} {
		sp, ok := got[0].Prop(tc.prop)
		if !ok {
			t.Errorf("property %q has no row at all; the three states cannot be distinguished without one", tc.prop)
			continue
		}
		if sp.State != tc.want {
			t.Errorf("property %q: state is %v, want %v (%s)", tc.prop, sp.State, tc.want, tc.why)
		}
	}

	// The non-conforming value must not have reached a typed column.
	if sp, ok := got[0].Prop("condition"); ok && len(sp.Elems) != 0 {
		t.Errorf("FR-021a: a non-conforming value reached the typed columns as %#v; "+
			"it must be stored as non-conforming and flagged, never coerced", sp.Elems)
	}
}

// TestStorage_ANonConformingElementDoesNotDeleteItsSiblings.
//
// FR-021a is a rule about a VALUE. A `many` property with one bad element
// resolves to StateNonConforming while its good elements are still parsed, and
// an index that skipped the whole property on that state would hold LESS than
// the vault does — silently, and only for the records that already have a
// problem, which is the population least likely to be spot-checked.
//
// This case is here because the obvious implementation ("only store conforming
// properties") passes every other test in this file.
func TestStorage_ANonConformingElementDoesNotDeleteItsSiblings(t *testing.T) {
	sc := plantSchema(t)
	src := `---
type: plant
id: PL-0006
species: Begonia
labels:
  - indoor
  - { nested: mapping }
  - humid
---
`
	rec := records.ParseRecord("garden/plant-0006.md", []byte(src))
	prop, _ := sc.Property("labels")
	want := records.ResolveProperty(rec, prop)
	if want.State != records.StateNonConforming || len(want.Values) != 2 {
		t.Fatalf("fixture precondition: the parser must report a non-conforming property with two surviving values, got state=%v values=%d",
			want.State, len(want.Values))
	}

	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/plant-0006.md", sc, src))

	got := collect(t, store, Selector{RecordType: "plant"})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	sp, ok := got[0].Prop("labels")
	if !ok {
		t.Fatal("the property vanished entirely")
	}
	if sp.State != records.StateNonConforming {
		t.Errorf("state is %v, want non-conforming — R-4 needs the flag to report rather than compare", sp.State)
	}
	have, err := sp.Typed(prop)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(have.Values) != 2 || have.Values[0].Text != "indoor" || have.Values[1].Text != "humid" {
		t.Errorf("the conforming siblings of a bad element were lost: %#v", have.Values)
	}
	// The source positions must still name the file, not the filtered slice:
	// `humid` is the THIRD entry the operator wrote.
	if have.SourcePosition(1) != 2 {
		t.Errorf("source position of the surviving second value is %d, want 2 — a finding would send the operator to the wrong line",
			have.SourcePosition(1))
	}
}

// TestStorage_NoNumberBecomesAFloat is FR-020b, asserted at the storage layer.
//
// The rule is a promise about the WHOLE path, and a REAL column is the one place
// the path could break without anyone writing a float: SQLite would happily turn
// 9007199254740993 into 9007199254740992 and 0.1+0.2 into 0.30000000000000004,
// silently, in the store rather than in the code anyone reviews.
func TestStorage_NoNumberBecomesAFloat(t *testing.T) {
	sc := plantSchema(t)
	// Values chosen because a float64 cannot hold them: the integer is above
	// 2^53, and the decimal has more significant digits than a double carries.
	src := `---
type: plant
id: PL-0003
species: Aloe vera
cuttings: 9007199254740993
height_cm: 12345678901234567890.123456789
---
`
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/plant-0003.md", sc, src))

	got := collect(t, store, Selector{RecordType: "plant"})
	if len(got) != 1 {
		t.Fatalf("expected one candidate, got %d", len(got))
	}
	for prop, want := range map[string]string{
		"cuttings":  "9007199254740993",
		"height_cm": "12345678901234567890.123456789",
	} {
		sp, ok := got[0].Prop(prop)
		if !ok || len(sp.Elems) != 1 {
			t.Fatalf("property %q did not round-trip as a single value: %#v", prop, sp)
		}
		if sp.Elems[0].Num != want {
			t.Errorf("FR-020b: %q stored as %q, want the exact digits %q — a binary float lost precision somewhere in the path",
				prop, sp.Elems[0].Num, want)
		}
	}
}

// TestStorage_IdentifierDoesNotFold is spec §7 test 89 (R-8).
//
// Two records whose identifiers differ only in case must coexist, be returned
// separately, and compare unequal. A TEXT COLLATE NOCASE column would make them
// ONE key — loud, because a UNIQUE constraint would reject the second, but a
// data-loss refusal for a case nobody chose. The assertion is on the OUTCOME
// (two distinct records survive), not on the DDL.
func TestStorage_IdentifierDoesNotFold(t *testing.T) {
	sc := plantSchema(t)
	upper := `---
type: plant
id: PL-0142
species: Sedum
---
`
	lower := `---
type: plant
id: pl-0142
species: Sedum
---
`
	store, _ := openIndex(t, Options{})
	mustUpsert(t,
		store,
		note(t, "garden/upper.md", sc, upper),
		note(t, "garden/lower.md", sc, lower),
	)

	got := collect(t, store, Selector{RecordType: "plant"})
	if len(got) != 2 {
		t.Fatalf("R-8: expected two distinct records, got %d — the identifiers were folded into one key", len(got))
	}
	ids := map[string]bool{}
	for _, c := range got {
		ids[c.RecordID] = true
	}
	if !ids["PL-0142"] || !ids["pl-0142"] {
		t.Errorf("R-8: identifiers came back as %v; both spellings must survive byte-exactly", ids)
	}
}

// TestStorage_DuplicateIdentifiersAreRecorded is FR-039's precondition.
//
// A vault MAY contain a duplicate identifier — that is a hard validation error
// the operator must be told about. An index that refuses to store the second
// occurrence cannot report it, so the duplicate would vanish instead of being
// named. This asserts the index records both, which is what makes the report
// possible.
func TestStorage_DuplicateIdentifiersAreRecorded(t *testing.T) {
	sc := plantSchema(t)
	body := `---
type: plant
id: PL-0500
species: Hoya
---
`
	store, _ := openIndex(t, Options{})
	mustUpsert(t,
		store,
		note(t, "garden/a.md", sc, body),
		note(t, "garden/b.md", sc, body),
	)
	got := collect(t, store, Selector{RecordType: "plant"})
	if len(got) != 2 {
		t.Fatalf("expected both notes to be indexed so a duplicate can be REPORTED, got %d", len(got))
	}
}

// TestStorage_NoteWithNoSchemaIsStillACandidate is FR-005 and D6's flat case.
func TestStorage_NoteWithNoSchemaIsStillACandidate(t *testing.T) {
	src := "# Just a note\n\n- [ ] water the ferns\n"
	rows := note(t, "garden/ordinary.md", nil, src)
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, rows)

	got := collect(t, store, Selector{Kind: KindNote})
	if len(got) != 1 {
		t.Fatalf("FR-005: a note with no declared type must still be indexed as an ordinary note, got %d candidates", len(got))
	}
	if got[0].RecordType != "" {
		t.Errorf("an ordinary note acquired a record type %q", got[0].RecordType)
	}
	if len(got[0].Props) != 0 {
		t.Errorf("an ordinary note acquired properties: %#v", got[0].Props)
	}
}

// TestStorage_UpsertReplacesRatherThanAccumulates.
//
// Re-indexing a note whose list shrank must not leave the removed elements
// behind. A stale element in a derived index is a value the vault does not
// contain being returned as though it did.
func TestStorage_UpsertReplacesRatherThanAccumulates(t *testing.T) {
	sc := plantSchema(t)
	before := `---
type: plant
id: PL-0004
species: Calathea
labels: [indoor, humid, shade]
---

- [ ] mist daily
- [ ] rotate weekly
`
	after := `---
type: plant
id: PL-0004
species: Calathea
labels: [indoor]
---

- [x] mist daily
`
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/plant-0004.md", sc, before))
	mustUpsert(t, store, note(t, "garden/plant-0004.md", sc, after))

	got := collect(t, store, Selector{RecordType: "plant"})
	if len(got) != 1 {
		t.Fatalf("re-indexing one note produced %d candidates", len(got))
	}
	sp, _ := got[0].Prop("labels")
	if len(sp.Elems) != 1 || sp.Elems[0].Text != "indoor" {
		t.Errorf("stale list elements survived the re-index: %#v", sp.Elems)
	}

	var tasks []TaskHit
	if err := store.Tasks(t.Context(), Selector{}, func(h TaskHit) error {
		tasks = append(tasks, h)
		return nil
	}); err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Task.Status != TaskDone {
		t.Errorf("stale checkbox rows survived the re-index: %#v", tasks)
	}
}

// TestStorage_DeleteRemovesEveryChildRow.
func TestStorage_DeleteRemovesEveryChildRow(t *testing.T) {
	store, _ := openIndex(t, Options{})
	rows := plantNote(t, 7, "growing")
	mustUpsert(t, store, rows)

	if err := store.DeleteNote(t.Context(), rows.Path); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if got := collect(t, store, Selector{}); len(got) != 0 {
		t.Errorf("the note survived its own deletion: %#v", got)
	}
	if err := store.Tasks(t.Context(), Selector{}, func(h TaskHit) error {
		t.Errorf("an orphaned task row survived: %#v", h)
		return nil
	}); err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if err := store.Relations(t.Context(), Selector{}, func(h RelationHit) error {
		t.Errorf("an orphaned relation row survived: %#v", h)
		return nil
	}); err != nil {
		t.Fatalf("Relations: %v", err)
	}
	// Deleting a note the index never held is not an error: the vault is the
	// source of truth and the index is allowed to be behind it.
	if err := store.DeleteNote(t.Context(), "garden/never-existed.md"); err != nil {
		t.Errorf("deleting an unknown path should be a no-op, got %v", err)
	}
}

// TestRelations_EdgesAreStoredOnceAndTheInverseIsNotStored is D5 / FR-032.
func TestRelations_EdgesAreStoredOnceAndTheInverseIsNotStored(t *testing.T) {
	sc := plantSchema(t)
	src := `---
type: plant
id: PL-0009
species: Pilea
bed: "[[Bed 3#Shelf|the top shelf]]"
keeper: "[[Rosa]]"
---
`
	store, _ := openIndex(t, Options{})
	mustUpsert(t, store, note(t, "garden/plant-0009.md", sc, src))

	var edges []RelationHit
	if err := store.Relations(t.Context(), Selector{RecordType: "plant"}, func(h RelationHit) error {
		edges = append(edges, h)
		return nil
	}); err != nil {
		t.Fatalf("Relations: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected exactly two edges — one per written link, no inverse — got %d: %#v", len(edges), edges)
	}
	byProp := map[string]RelationRow{}
	for _, e := range edges {
		byProp[e.Relation.Prop] = e.Relation
		if e.RecordID != "PL-0009" {
			t.Errorf("edge %q lost its owning record: %#v", e.Relation.Prop, e)
		}
	}
	bed := byProp["bed"]
	if bed.Target != "Bed 3" || bed.Heading != "Shelf" || bed.Display != "the top shelf" {
		t.Errorf("R-8: a relation is identified by its TARGET, and the parts must survive separately: %#v", bed)
	}
}
