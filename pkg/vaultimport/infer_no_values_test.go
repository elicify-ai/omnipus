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
	"strings"
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
// ZERO values: the name is the only evidence, and it is allowed to decide.
// ---------------------------------------------------------------------------

// TestInferSchema_DateNamedPropertyWithNoValuesIsADate is the defect this
// file was opened for. All twelve of the founder's `project` notes declare
// `deadline:` and every one of them leaves it blank; the old rule read that
// as `text`, and view_write.go must refuse `deadline != ""` on a text
// property because FR-007a keeps `""` a PRESENT value there. Projects.base's
// "Deadlines" view — whose ONLY row-set loss was that one filter — shipped
// DISABLED as a result.
func TestInferSchema_DateNamedPropertyWithNoValuesIsADate(t *testing.T) {
	got := inferOneType(t, "project",
		"deadline:\nstage: build\n",
		"deadline:\nstage: build\n",
		"deadline:\nstage: ship\n",
	)

	p := mustProp(t, got, "deadline")
	if p.Type != records.TypeDate {
		t.Errorf("deadline inferred as %q, want %q — with no value anywhere the name is the only evidence, and `text` is the one declaration that costs the `!= \"\"` filter",
			p.Type, records.TypeDate)
	}
	if p.Kind != ClassifyDateFromName {
		t.Errorf("Kind = %q, want %q — a decision made with no observation behind it must say so", p.Kind, ClassifyDateFromName)
	}
	if p.NameEvidenced == nil {
		t.Fatal("NameEvidenced is nil: the run typed this property from its NAME and recorded no evidence, which makes it a SILENT guess — the one thing this package refuses")
	}
	if p.NameEvidenced.RecordType != "project" || p.NameEvidenced.Property != "deadline" {
		t.Errorf("NameEvidenced names %s.%s, want project.deadline", p.NameEvidenced.RecordType, p.NameEvidenced.Property)
	}
	if p.NameEvidenced.Type != records.TypeDate {
		t.Errorf("NameEvidenced.Type = %q, want %q", p.NameEvidenced.Type, records.TypeDate)
	}
	if p.NameEvidenced.DeclaringNotes != 3 {
		t.Errorf("NameEvidenced.DeclaringNotes = %d, want 3 — the report's whole claim is that EVERY note declaring the key left it blank, and that claim needs the count",
			p.NameEvidenced.DeclaringNotes)
	}
	// A property no note ever filled in cannot be required, whatever its
	// type: FR-007 counts a blank as absence, so requiring it would fail
	// every note of the type against the schema this run just wrote.
	if p.Required {
		t.Error("deadline declared required=true — no note carries a value for it, so every note of the type would be invalid against this run's own schema")
	}
}

// TestInferSchema_NonDateNamedPropertyWithNoValuesStaysText holds the rule to
// dates. There is no `_count -> integer` and no `is_* -> checkbox`; a name
// this package cannot read stays text, because churn bought with a guess is
// still a guess.
func TestInferSchema_NonDateNamedPropertyWithNoValuesStaysText(t *testing.T) {
	got := inferOneType(t, "deal",
		"owner:\nseat_count:\nis_active:\nstage: new\n",
		"owner:\nseat_count:\nis_active:\nstage: new\n",
	)
	for _, name := range []string{"owner", "seat_count", "is_active"} {
		p := mustProp(t, got, name)
		if p.Type != records.TypeText {
			t.Errorf("%s inferred as %q, want text — only DATE is read from a name; extending the rule to other types would be a guess with nothing measured behind it", name, p.Type)
		}
		if p.Kind != ClassifyText {
			t.Errorf("%s Kind = %q, want %q", name, p.Kind, ClassifyText)
		}
		if p.NameEvidenced != nil {
			t.Errorf("%s carries NameEvidenced: nothing was inferred from its name, so claiming so in the report would be false", name)
		}
	}
}

// TestInferSchema_EmptyListWithADateNameKeepsItsArity checks the name rule
// composes with the arity rule instead of overriding it. `dates: []` is
// unambiguous ARITY evidence with no VALUE evidence, and both readings have
// to survive: many=true (the operator wrote a list) AND date (the name).
func TestInferSchema_EmptyListWithADateNameKeepsItsArity(t *testing.T) {
	got := inferOneType(t, "sprint",
		"review_date: []\nname: a\n",
		"review_date: []\nname: b\n",
	)
	p := mustProp(t, got, "review_date")
	if p.Type != records.TypeDate {
		t.Errorf("review_date inferred as %q, want date", p.Type)
	}
	if !p.Many {
		t.Error("review_date declared many=false — every note wrote a LIST, and records.Validate reaches the arity check on an empty sequence, so many=false would make each of them an arity error this run created")
	}
}

// ---------------------------------------------------------------------------
// ONE value and the name stops mattering. This is the guard rail.
// ---------------------------------------------------------------------------

// TestInferSchema_OneObservedValueOverridesTheName is the test that keeps the
// name rule from becoming the "naive if it looks like a date call it a date"
// rule. `renewal_date` is as date-shaped a name as exists; the moment a note
// writes a non-date into it, the name is worth nothing.
func TestInferSchema_OneObservedValueOverridesTheName(t *testing.T) {
	got := inferOneType(t, "subscription",
		"renewal_date:\nvendor: a\n",
		"renewal_date:\nvendor: b\n",
		"renewal_date: PLACEHOLDER — usage-based model, no fixed renewal\nvendor: c\n",
	)
	p := mustProp(t, got, "renewal_date")
	if p.Type == records.TypeDate {
		t.Fatal("renewal_date inferred as date despite a note holding `PLACEHOLDER — usage-based model, no fixed renewal`: the name overrode the data, and the note the importer just read is now invalid against the schema the same run wrote")
	}
	if p.Kind == ClassifyDateFromName {
		t.Errorf("Kind = %q: the name-only rule fired on a property that HAS a value, which is the one thing it must never do", p.Kind)
	}
	if p.NameEvidenced != nil {
		t.Error("NameEvidenced set on a property with an observed value — the report would claim a name decided something the data decided")
	}
}

// TestInferSchema_RealDatesStillClassifyByValueNotByName keeps the two paths
// distinguishable in the report. A property every note filled in with a real
// date is a MEASUREMENT (ClassifyDate); one nobody filled in is a GUESS
// (ClassifyDateFromName). Both declare `date`, and conflating them would
// hide which of the two the founder is reading.
func TestInferSchema_RealDatesStillClassifyByValueNotByName(t *testing.T) {
	got := inferOneType(t, "invoice",
		"issued_date: 2026-01-02\nref: a\n",
		"issued_date: 2026-03-04\nref: b\n",
	)
	p := mustProp(t, got, "issued_date")
	if p.Type != records.TypeDate {
		t.Fatalf("issued_date inferred as %q, want date", p.Type)
	}
	if p.Kind != ClassifyDate {
		t.Errorf("Kind = %q, want %q — every value parsed, so this is a measurement and must not be reported as a name-based guess", p.Kind, ClassifyDate)
	}
	if p.NameEvidenced != nil {
		t.Error("NameEvidenced set on a property whose every value parsed as a date")
	}
}

// TestInferSchema_DateFromNameNeverFiresWhereAnyValueExists sweeps the value
// shapes a note can write and asserts the name rule stays out of all of
// them. It is the invariant the zero-self-invalidation bar rests on: the
// rule can only ever fire where there is nothing to invalidate.
func TestInferSchema_DateFromNameNeverFiresWhereAnyValueExists(t *testing.T) {
	cases := map[string]string{
		"a real date":     "end_date: 2026-01-02\n",
		"a placeholder":   "end_date: PLACEHOLDER — renewal date unknown\n",
		"free text":       "end_date: tbd\n",
		"a wikilink":      "end_date: \"[[Some Note]]\"\n",
		"a boolean":       "end_date: true\n",
		"an integer":      "end_date: 41\n",
		"a decimal":       "end_date: 12.50\n",
		"a quoted word":   "end_date: \"present\"\n",
		"a list of dates": "end_date:\n  - 2026-01-02\n  - 2026-02-03\n",
		"a block scalar":  "end_date: |\n  2026-01-02\n",
	}
	for name, second := range cases {
		t.Run(name, func(t *testing.T) {
			got := inferOneType(t, "thing",
				"end_date:\nname: a\n",
				second+"name: b\n",
			)
			p := mustProp(t, got, "end_date")
			if p.Kind == ClassifyDateFromName || p.NameEvidenced != nil {
				t.Fatalf("the name-only rule fired on end_date although a note holds %s — the rule's whole safety argument is that it never sees a value", name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The closed name list.
// ---------------------------------------------------------------------------

func TestNameEvidencedDate_IsAClosedList(t *testing.T) {
	accepted := []string{
		"date", "deadline", "DEADLINE", "due",
		"end_date", "close_date", "renewal_date", "due_date",
		"effective_date", "grant_date", "publish_date",
		"registration_renewal_date",
		"end-date", "date_signed", "date-signed",
	}
	for _, n := range accepted {
		if !nameEvidencedDate(n) {
			t.Errorf("%q not read as a date name — it cannot plausibly mean anything else", n)
		}
	}
	// Each rejection is a name that reads just as naturally as an amount, a
	// term, a checkbox or free text. Refusing them is the whole reason the
	// list can be trusted at all.
	// Every rejection below is a name that reads just as naturally as
	// something other than a date. `created_at` and `updated_at` are the
	// pointed ones: an `_at` suffix was SUGGESTED for the accept list and
	// was refused on measurement — the founder's vault writes the literal
	// string `timestamp` into all five of its `_at` properties, and
	// `timestamp` is not one of records' six accepted date layouts.
	rejected := []string{
		"expiry", "completed", "last_activity", "last_refreshed",
		"created_at", "updated_at", "started_at", "captured_at",
		"term", "period", "close",
		"updated", "created", "cost", "seats", "owner", "status",
		"candidate", "update", "mandate", "validate",
		"", "  ",
	}
	for _, n := range rejected {
		if nameEvidencedDate(n) {
			t.Errorf("%q read as a date name — the list must stay closed to names that could mean something else", n)
		}
	}
}

// TestNameEvidencedDate_SubstringsDoNotCount pins the rule to a whole word,
// not a substring. `mandate`, `update` and `candidate` all end in the four
// letters of `date`, and a rule that matched on that would type three
// perfectly ordinary text properties as calendar dates.
func TestNameEvidencedDate_SubstringsDoNotCount(t *testing.T) {
	for _, n := range []string{"mandate", "update", "candidate", "validate", "predate"} {
		if nameEvidencedDate(n) {
			t.Errorf("%q matched the date-name rule on a bare substring", n)
		}
	}
}

// ---------------------------------------------------------------------------
// The dirt: a genuine split is REFUSED, and reported.
// ---------------------------------------------------------------------------

// TestInferSchema_HalfDatesHalfPlaceholdersStaysTextAndIsReported is the
// founder's `subscription.renewal_date` reduced to its shape: half real ISO
// dates, half hand-written placeholders. Declaring `date` would invalidate
// every placeholder note. Declaring `text` SILENTLY leaves him a disabled
// view and no line explaining it. The only honest answer is both: text, and
// a report naming the counter-examples.
func TestInferSchema_HalfDatesHalfPlaceholdersStaysTextAndIsReported(t *testing.T) {
	got := inferOneType(t, "subscription", splitFixture(16, 16, "renewal_date")...)
	p := mustProp(t, got, "renewal_date")
	if p.Type != records.TypeText {
		t.Fatalf("renewal_date inferred as %q: two of the four notes hold a placeholder, and any non-text declaration makes them invalid against the schema this run wrote", p.Type)
	}
	if p.Ambiguity == nil {
		t.Fatal("a 2-of-4 date split was declared text SILENTLY — the founder loses `renewal_date != \"\"` and the report says nothing about why; an unexplained loss is the defect, not the refusal")
	}
	if p.Ambiguity.BestType != records.TypeDate {
		t.Errorf("Ambiguity.BestType = %q, want date", p.Ambiguity.BestType)
	}
	if p.Ambiguity.MatchedCount != 16 || p.Ambiguity.TotalValues != 32 {
		t.Errorf("Ambiguity counts = %d/%d, want 16/32", p.Ambiguity.MatchedCount, p.Ambiguity.TotalValues)
	}
	if len(p.Ambiguity.Examples) == 0 {
		t.Fatal("no counter-examples recorded — `2/4 values parse as date` without naming a note the founder can open is a number, not evidence")
	}
	for _, ex := range p.Ambiguity.Examples {
		if !strings.Contains(ex.Value, "PLACEHOLDER") {
			t.Errorf("counter-example %q is not one of the values that BLOCKED the inference", ex.Value)
		}
	}
	if p.Kind != ClassifyAmbiguous {
		t.Errorf("Kind = %q, want %q", p.Kind, ClassifyAmbiguous)
	}
}

// TestInferSchema_ExactHalfIsOnTheReportingSideOfTheFloor pins the boundary
// the real vault lands on. `subscription.renewal_date` is 31 of 62 — exactly
// one half — so whether it is reported at all is decided by which side of
// the comparison `>=` sits on. Left to a float division that is a coin toss;
// the rule is `matched*2 >= total`, in integers.
func TestInferSchema_ExactHalfIsOnTheReportingSideOfTheFloor(t *testing.T) {
	// 31 dates against 31 placeholders — the founder's own numbers, an odd
	// numerator over a total that is not a power of two, so nothing here is
	// exactly representable in binary either.
	got := inferOneType(t, "thing", splitFixture(31, 31, "when")...)
	p := mustProp(t, got, "when")
	if p.Ambiguity == nil {
		t.Fatalf("a dead-even 31/62 split went unreported: with the floor at one half, exactly half must fall on the REPORTED side (matched*%d >= %d*total)",
			ambiguousMatchFloorDen, ambiguousMatchFloorNum)
	}
	if p.Ambiguity.MatchedCount*ambiguousMatchFloorDen < ambiguousMatchFloorNum*p.Ambiguity.TotalValues {
		t.Fatalf("reported a split (%d/%d) that does not clear the floor", p.Ambiguity.MatchedCount, p.Ambiguity.TotalValues)
	}
}

// TestInferSchema_CoincidentalMatchStaysSilent keeps the floor doing the job
// it was put there for. A sixth of the values parsing as a date is what the
// floor's own comment calls coincidence, and reporting it would train the
// operator to skim the section that matters.
func TestInferSchema_CoincidentalMatchStaysSilent(t *testing.T) {
	// 4 accidental dates in 24 free-text labels — 16.7%, the shape the floor
	// was put there for.
	got := inferOneType(t, "thing", splitFixture(4, 20, "label")...)
	p := mustProp(t, got, "label")
	if p.Ambiguity != nil {
		t.Errorf("a 4-of-24 accidental date match was reported as an ambiguity (%d/%d) — below the floor a partial match is coincidence, and reporting it is noise",
			p.Ambiguity.MatchedCount, p.Ambiguity.TotalValues)
	}
}

// ---------------------------------------------------------------------------
// The report channel.
// ---------------------------------------------------------------------------

func TestCollectNameEvidencedInferences_IsCompleteAndSorted(t *testing.T) {
	inferred := map[string][]InferredProperty{
		"project": {
			{Name: "stage", Type: records.TypeText},
			{Name: "deadline", Type: records.TypeDate, Kind: ClassifyDateFromName,
				NameEvidenced: &NameEvidencedInference{RecordType: "project", Property: "deadline", Type: records.TypeDate, DeclaringNotes: 12}},
		},
		"contract": {
			{Name: "renewal_date", Type: records.TypeDate, Kind: ClassifyDateFromName,
				NameEvidenced: &NameEvidencedInference{RecordType: "contract", Property: "renewal_date", Type: records.TypeDate, DeclaringNotes: 2}},
			{Name: "end_date", Type: records.TypeDate, Kind: ClassifyDateFromName,
				NameEvidenced: &NameEvidencedInference{RecordType: "contract", Property: "end_date", Type: records.TypeDate, DeclaringNotes: 2}},
		},
	}
	got := CollectNameEvidencedInferences(inferred)
	want := []string{"contract.end_date", "contract.renewal_date", "project.deadline"}
	if len(got) != len(want) {
		t.Fatalf("collected %d guesses, want %d — every name-based decision must reach the report", len(got), len(want))
	}
	for i, w := range want {
		if g := got[i].RecordType + "." + got[i].Property; g != w {
			t.Errorf("position %d is %s, want %s — the order must not depend on Go's randomised map iteration, or two identical runs print different reports", i, g, w)
		}
	}
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
