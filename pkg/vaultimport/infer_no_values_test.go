// Omnipus — the suite for a property NOBODY EVER FILLED IN, and for the
// guard rail that keeps its rule away from every property somebody did.
//
// Two rules are under test here and they pull in opposite directions, which
// is the point:
//
//   - With ZERO observed values a date-NAMED property is declared `date`,
//     because text is not a neutral default (FR-007a gives it absence
//     semantics no other type has) and the name is the only evidence left.
//   - With ONE observed value the name stops mattering entirely. The
//     founder's `subscription.renewal_date` is 31 real dates against 31
//     hand-written `PLACEHOLDER — ...` strings; typing it `date` would make
//     31 of his own notes invalid against the schema the same run wrote, and
//     this package admits no exception to that.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// Helpers. Notes go through records.ParseRecord and CollectTypeGroups, never
// a hand-built PropertyObservation: an inference that only holds against a
// struct somebody typed by hand is not an inference about a vault.
// ---------------------------------------------------------------------------

// inferOneType writes each `body` as a note of record type `typeName` and
// returns the inferred declarations, keyed by property name.
func inferOneType(t *testing.T, typeName string, bodies ...string) map[string]InferredProperty {
	t.Helper()
	dir := t.TempDir()
	notes := make([]NoteRecord, 0, len(bodies))
	for i, body := range bodies {
		rel := fmt.Sprintf("n%d.md", i)
		notes = append(notes, noteOnDisk(t, dir, rel,
			"---\ntype: "+typeName+"\n"+body+"---\n\nbody\n"))
	}
	groups := CollectTypeGroups(notes)
	g, ok := groups[typeName]
	if !ok {
		t.Fatalf("no type group for %q — the fixture notes did not declare the type this test is about", typeName)
	}
	out := map[string]InferredProperty{}
	for _, p := range InferSchema(g, BuildNameIndex(notes)) {
		out[p.Name] = p
	}
	return out
}

// splitFixture builds `dates` notes holding a distinct real ISO date and
// `placeholders` notes holding a distinct hand-written placeholder, for one
// property.
//
// THE DISTINCT COUNTS ARE THE POINT, and a smaller fixture does not
// reproduce the defect. classifyProperty tries `enum` BEFORE it reports an
// ambiguity, so a split of four values across four distinct spellings is
// declared a 4-value enum and never reaches the ambiguity branch at all —
// which is a perfectly good outcome (an enum is non-text, so `!= ""` still
// translates and every observed value is declared, so no note is
// invalidated). The founder's `subscription.renewal_date` carries 62 values
// in ~48 distinct spellings, far past enumMaxDistinct, which is exactly why
// it falls through to text and needs the ambiguity report. Fixtures here
// stay above that threshold so they test the path the vault actually takes.
func splitFixture(dates, placeholders int, prop string) []string {
	out := make([]string, 0, dates+placeholders)
	for i := 0; i < dates; i++ {
		out = append(out, fmt.Sprintf("%s: 2026-%02d-%02d\nn: d%d\n", prop, 1+i%12, 1+i%28, i))
	}
	for i := 0; i < placeholders; i++ {
		out = append(out, fmt.Sprintf("%s: PLACEHOLDER — reason number %d is unknown\nn: p%d\n", prop, i, i))
	}
	return out
}

// mustProp fails loudly rather than returning a zero InferredProperty, whose
// Type is "" and would silently satisfy nothing.
func mustProp(t *testing.T, got map[string]InferredProperty, name string) InferredProperty {
	t.Helper()
	p, ok := got[name]
	if !ok {
		have := make([]string, 0, len(got))
		for k := range got {
			have = append(have, k)
		}
		t.Fatalf("property %q was not inferred at all; inferred: %v", name, have)
	}
	return p
}

// ---------------------------------------------------------------------------
// The real vault. SKIPS without OMNIPUS_KB_FIXTURE.
// ---------------------------------------------------------------------------

// TestFixtureVault_NameEvidencedDatesNeverContradictARealValue carries the
// safety argument onto 757 notes nobody wrote for a test. The claim being
// checked is the one the whole rule rests on: a property typed from its name
// has NO observed value, so it cannot make any note invalid.
func TestFixtureVault_NameEvidencedDatesNeverContradictARealValue(t *testing.T) {
	root := fixtureVaultCopy(t)

	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("scanning the vault: %v", err)
	}
	notes, _, err := LoadNotes(inv)
	if err != nil {
		t.Fatalf("loading notes: %v", err)
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)

	guessed := 0
	for typeName, g := range groups {
		for _, p := range InferSchema(g, names) {
			if p.Kind != ClassifyDateFromName {
				continue
			}
			guessed++
			po := g.Props[p.Name]
			if len(po.Values) != 0 {
				t.Errorf("%s.%s was typed from its NAME although the vault holds %d value(s) for it (first: %q) — the rule fired where data exists, and those notes are now invalid against the schema this run writes",
					typeName, p.Name, len(po.Values), po.Values[0].Text)
			}
			if p.NameEvidenced == nil {
				t.Errorf("%s.%s: typed from its name with no evidence recorded — a silent guess", typeName, p.Name)
			}
			if p.Required {
				t.Errorf("%s.%s: declared required although no note carries a value — every note of the type would fail this run's own schema", typeName, p.Name)
			}
		}
	}
	if guessed == 0 {
		t.Fatal("no property in the founder's vault was typed from its name, so this measurement is vacuous — the vault or the rule has changed")
	}
	t.Logf("REAL VAULT: %d propert(ies) typed date from their NAME; every one of them has zero observed values, so none can invalidate a note", guessed)
}

// TestFixtureVault_TheImporterNeverInvalidatesANoteOverANameGuess is the
// acceptance bar itself, stated the way the founder reads it: after a full
// import, validate every note in the vault against the schemas the SAME run
// wrote, and count the findings that name a property this run typed from its
// name. That count must be zero.
//
// It is deliberately broader than the FR-104b bar next door
// (TestFixtureVault_TypedNotesAreNeverSelfInvalidated), which only looks at
// the ~11 notes the importer wrote a `type:` into. A schema change reaches
// every note of the type, typed by hand or not, and that is the population
// this rule can hurt.
func TestFixtureVault_TheImporterNeverInvalidatesANoteOverANameGuess(t *testing.T) {
	root := fixtureVaultCopy(t)

	// The guess set is computed from the PRISTINE vault, in the same three
	// steps and the same order Run's first act performs them — scan, group,
	// infer. It cannot be recomputed after the import: Run writes `type:`
	// into the untyped notes it can decide, which changes the type groups,
	// so a second inference would answer a different question than the one
	// the written schemas came from.
	preInv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("scanning the vault: %v", err)
	}
	preNotes, _, err := LoadNotes(preInv)
	if err != nil {
		t.Fatalf("loading notes: %v", err)
	}
	preNames := BuildNameIndex(preNotes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range CollectTypeGroups(preNotes) {
		inferred[typeName] = InferSchema(g, preNames)
	}
	guesses := CollectNameEvidencedInferences(inferred)
	if len(guesses) == 0 {
		t.Fatal("the run made no name-based type decision, so this measurement is vacuous")
	}
	guessed := map[string]bool{}
	for _, g := range guesses {
		guessed[g.RecordType+"."+g.Property] = true
	}

	if _, err = Run(root, true); err != nil {
		t.Fatalf("import failed: %v", err)
	}

	schemaSet, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}

	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the vault: %v", err)
	}
	notes, _, err := LoadNotes(inv)
	if err != nil {
		t.Fatalf("re-loading notes: %v", err)
	}

	blamed, checked := 0, 0
	for _, n := range notes {
		typeName := n.Rec.TypeName()
		if typeName == "" {
			continue
		}
		rr := records.ValidateRecord(schemaSet, n.Rec, records.ValidateOptions{})
		if !rr.Recognised {
			continue
		}
		checked++
		for _, f := range rr.Findings {
			if f.Property == "" {
				continue
			}
			if guessed[typeName+"."+f.Property] {
				blamed++
				t.Errorf("%s: %s.%s was typed `date` from its NAME and the SAME run now reports the note invalid: %v",
					n.RelPath, typeName, f.Property, f)
			}
		}
	}
	t.Logf("REAL VAULT acceptance bar: %d name-based type guesses over %d validated records, %d notes invalidated by one — the bar is 0",
		len(guesses), checked, blamed)
	if blamed != 0 {
		t.Errorf("ACCEPTANCE BAR FAILED: %d note(s) are invalid against a property this import typed from its name alone", blamed)
	}
}
