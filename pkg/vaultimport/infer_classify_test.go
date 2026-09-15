// infer_classify_test.go: tests for classify collected signals into record kinds and properties (observed-value classification, relation targets, name evidence, domain adoption)

package vaultimport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// --- moved from infer.go tests 2026-09-15 ---

// TestAdoptObservedDomains_UnobservedYieldsToObserved is the rule working.
func TestAdoptObservedDomains_UnobservedYieldsToObserved(t *testing.T) {
	inferred, notes := adoptionFixture(t, []string{"draft", "review", "final"})

	// The precondition, asserted rather than assumed: without it an adoption
	// that never happened and an adoption that was unnecessary look identical.
	if p, _ := findInferredProperty(inferred["alpha"], "stage"); p.Type != records.TypeEnum {
		t.Fatalf("fixture not exercising the rule: alpha.stage is %q, want enum", p.Type)
	}
	if p, _ := findInferredProperty(inferred["beta"], "stage"); p.Type != records.TypeText {
		t.Fatalf("fixture not exercising the rule: beta.stage is %q, want the text fallback", p.Type)
	}

	adopted, declined := AdoptObservedDomains(inferred, notes)

	got, _ := findInferredProperty(inferred["beta"], "stage")
	if got.Type != records.TypeEnum {
		t.Fatalf("beta.stage stayed %q — a fallback nothing was observed for outranked three real observations, which is the split that costs the founder's untyped views their columns", got.Type)
	}
	if len(adopted) != 1 || adopted[0].RecordType != "beta" || adopted[0].Property != "stage" {
		t.Errorf("the adoption was not accounted for: adopted=%+v", adopted)
	}
	if len(declined) != 0 {
		t.Errorf("nothing should have been declined here: %+v", declined)
	}
	// The vocabulary has to come across too. An enum whose value set did not
	// follow is a domain match on paper that rejects every real value.
	if len(got.EnumValues) != 3 {
		t.Errorf("the adopted enum carries %d values, want the 3 observed: %v", len(got.EnumValues), got.EnumValues)
	}
}

// TestAdoptObservedDomains_DeclinesAVocabularyTooLargeToBeClosed is the half
// that keeps this rule honest, and it is the half the founder's own vault
// exercises: `status` there holds twenty-five distinct values across eighteen
// record types, which is not one closed vocabulary and must not be adopted as
// one.
func TestAdoptObservedDomains_DeclinesAVocabularyTooLargeToBeClosed(t *testing.T) {
	// THE UNION IS BUILT FROM SEVERAL SMALL VOCABULARIES, not one large one,
	// and that is the whole construction rather than a detail. A single type
	// with seventeen distinct values is not an enum at all — it fails the
	// enum rule on its own and the observed domain becomes `text`, so nothing
	// is ever offered for adoption and the decline branch is never reached.
	// An earlier draft of this test did exactly that and reported the decline
	// as missing when the rule had simply never been asked.
	//
	// This is also the founder's real shape: eighteen record types each with a
	// short, sensible `status` vocabulary of its own, whose UNION is
	// twenty-five values and is not a vocabulary at all.
	dir := t.TempDir()
	var notes []NoteRecord
	notes = append(notes,
		noteOnDisk(t, dir, "b1.md", "---\ntype: beta\nname: B1\n---\n\nbody\n"),
		noteOnDisk(t, dir, "b2.md", "---\ntype: beta\nname: B2\n---\n\nbody\n"),
	)
	// Four observing types, five values each, no overlap: each is comfortably
	// an enum on its own; together they are twenty, past the bound.
	// ENOUGH TYPES TO PASS adoptedEnumMaxDistinct, computed from the constant
	// rather than written as a number, so raising or lowering that ceiling
	// moves this fixture with it instead of silently making the test vacuous.
	// Each type stays at five values, comfortably an enum on its own — the
	// union is what has to be too large, never any single contributor.
	perType := 5
	var types []string
	for i := 0; len(types)*perType <= adoptedEnumMaxDistinct; i++ {
		types = append(types, fmt.Sprintf("rt%02d", i))
	}
	for ti, rt := range types {
		for i := 0; i < perType; i++ {
			notes = append(notes, noteOnDisk(t, dir,
				fmt.Sprintf("%s%d.md", rt, i),
				fmt.Sprintf("---\ntype: %s\nstage: %s_v%d\n---\n\nbody\n", rt, rt, i)))
			_ = ti
		}
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range groups {
		inferred[typeName] = InferSchema(g, names)
	}
	inferred["beta"] = append(inferred["beta"], InferredProperty{Name: "stage", Type: records.TypeText})

	// The preconditions, asserted: every observing type must genuinely be an
	// enum, and their union must genuinely exceed the bound. Either one false
	// and this test is measuring something else.
	union := map[string]bool{}
	for _, rt := range types {
		p, ok := findInferredProperty(inferred[rt], "stage")
		if !ok || p.Type != records.TypeEnum {
			t.Fatalf("fixture not exercising the rule: %s.stage is %q, want enum", rt, p.Type)
		}
		for _, v := range p.EnumValues {
			union[records.FoldKey(v)] = true
		}
	}
	if len(union) <= adoptedEnumMaxDistinct {
		t.Fatalf("fixture not exercising the rule: the union is %d values, within the %d bound, so nothing would decline", len(union), adoptedEnumMaxDistinct)
	}

	adopted, declined := AdoptObservedDomains(inferred, notes)

	got, _ := findInferredProperty(inferred["beta"], "stage")
	if got.Type != records.TypeText {
		t.Fatalf("beta.stage was adopted as %q although the observed union is %d values, past the %d bound past which this package stops calling a set closed",
			got.Type, len(union), adoptedEnumMaxDistinct)
	}
	for _, a := range adopted {
		if a.RecordType == "beta" && a.Property == "stage" {
			t.Fatalf("beta.stage is listed as adopted: %+v", a)
		}
	}
	// A refusal nobody is told about is indistinguishable from the rule never
	// having looked, which is why this package accounts for its declines as
	// well as its decisions.
	var found bool
	for _, d := range declined {
		if d.RecordType == "beta" && d.Property == "stage" {
			found = true
			if d.UnionSize <= d.Bound {
				t.Errorf("the decline is recorded with UnionSize=%d and Bound=%d, which would not have declined anything", d.UnionSize, d.Bound)
			}
		}
	}
	if !found {
		t.Errorf("the decline was never accounted for: declined=%+v", declined)
	}
}

// TestAdoptObservedDomains_NeverOverridesTheTypesOwnData is the containment,
// and it is the same sentence the two base-file rules turn on: data beats a
// base file, as data beats another type's data.
func TestAdoptObservedDomains_NeverOverridesTheTypesOwnData(t *testing.T) {
	dir := t.TempDir()
	// SEVEN DISTINCT PROSE VALUES, and the count is load-bearing exactly as it
	// is in the placeholder test next door. Two prose values are still a small
	// enough set to be read as an ENUM, and an earlier draft of this test used
	// two: adoption then replaced beta's vocabulary with alpha's while leaving
	// the declared TYPE at `enum` either way, so an assertion comparing types
	// passed with the containment clause deleted. Seven lands on text, where
	// an adoption is visible as a type change, and the values are compared as
	// well so a vocabulary swap cannot hide inside a matching type.
	//
	// Declared ahead of notes so notes can be preallocated for its own 2
	// fixed entries plus one per prose value appended in the loop below.
	prose := []string{
		"waiting on the bank to confirm the wire",
		"blocked until the lease is countersigned",
		"paused while the auditor is on leave",
		"with the founder for a final read",
		"held pending the currency correction",
		"queued behind the Q3 close",
		"open — no owner assigned yet",
	}
	notes := make([]NoteRecord, 0, 2+len(prose))
	notes = append(notes,
		noteOnDisk(t, dir, "a1.md", "---\ntype: alpha\nstage: draft\n---\n\nbody\n"),
		noteOnDisk(t, dir, "a2.md", "---\ntype: alpha\nstage: review\n---\n\nbody\n"),
	)
	for i, v := range prose {
		notes = append(notes, noteOnDisk(t, dir, fmt.Sprintf("b%d.md", i),
			fmt.Sprintf("---\ntype: beta\nstage: %s\n---\n\nbody\n", v)))
	}
	groups := CollectTypeGroups(notes)
	names := BuildNameIndex(notes)
	inferred := map[string][]InferredProperty{}
	for typeName, g := range groups {
		inferred[typeName] = InferSchema(g, names)
	}

	before, _ := findInferredProperty(inferred["beta"], "stage")
	if before.Type != records.TypeText {
		t.Fatalf("fixture not exercising the rule: beta.stage is %q, not text, so an adoption would not be visible as a type change", before.Type)
	}
	if p, _ := findInferredProperty(inferred["alpha"], "stage"); p.Type != records.TypeEnum {
		t.Fatalf("fixture not exercising the rule: alpha.stage is %q, so there is no domain on offer to refuse", p.Type)
	}

	AdoptObservedDomains(inferred, notes)

	after, _ := findInferredProperty(inferred["beta"], "stage")
	if after.Type != before.Type {
		t.Fatalf("beta.stage changed from %q to %q although beta's own notes carry values for it — those notes are now invalid under a schema derived from the vault that contains them",
			before.Type, after.Type)
	}
	if len(after.EnumValues) != len(before.EnumValues) {
		t.Fatalf("beta.stage kept its type but its vocabulary was replaced (%v -> %v); beta's own notes are the evidence for this property and they are the ones that must stand",
			before.EnumValues, after.EnumValues)
	}
}

// TestAdoptObservedDomains_TheAccountReachesTheFileAnOperatorWouldOpen is the
// honesty half. Every guess this package makes is acceptable because it is
// reported and correctable in one edit, and the report for this one is the
// schema file the property is declared in — not a run summary scrolled past
// once.
func TestAdoptObservedDomains_TheAccountReachesTheFileAnOperatorWouldOpen(t *testing.T) {
	inferred, notes := adoptionFixture(t, []string{"draft", "review", "final"})
	AdoptObservedDomains(inferred, notes)

	raw, err := RenderSchemaYAML("beta", inferred["beta"])
	if err != nil {
		t.Fatalf("RenderSchemaYAML: %v", err)
	}
	yaml := string(raw)
	if !strings.Contains(yaml, "alpha") {
		t.Errorf("beta.yaml does not name the record type whose data decided this, so an operator who thinks it wrong cannot find what to correct:\n%s", yaml)
	}
	if !strings.Contains(yaml, "stage") {
		t.Errorf("beta.yaml does not mention the adopted property at all:\n%s", yaml)
	}
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

// TestInferRelationTarget_DanglingLinksCountAgainstTheMajority is the exact
// case the founder's ruling names.
func TestInferRelationTarget_DanglingLinksCountAgainstTheMajority(t *testing.T) {
	cases := []struct {
		name string
		// byType is resolved links per target type; dangling is links that
		// resolve to no note at all.
		byType   map[string]int
		order    []string
		dangling int
		// wantTo is the `to:` FR-104a's PURPOSE requires. "" means the
		// property must be declared text with the fix named.
		wantTo string
		// strictWouldSay is what the narrow "resolved targets only" reading
		// would have produced — recorded so a future reader can see that
		// these rows discriminate between the two, which is the only reason
		// they are here.
		strictWouldSay string
		why            string
	}{
		{
			name:           "the founder's own case: 2 task, 1 person, 2 dangling",
			byType:         map[string]int{"task": 2, "person": 1},
			order:          []string{"task", "person"},
			dangling:       2,
			wantTo:         "",
			strictWouldSay: "task",
			why:            "task holds 2 of the property's 5 links. FR-104a exists to stop exactly this being declared `to: task`.",
		},
		{
			name:           "3 task, 0 other, 2 dangling — still short of 2/3",
			byType:         map[string]int{"task": 3},
			order:          []string{"task"},
			dangling:       2,
			wantTo:         "",
			strictWouldSay: "task",
			why:            "3 of 5 is 0.6. Every link that resolved agreed, but two thirds of the property's links did not point at a task, and a schema saying `to: task` would make the vault's own validator report the other two.",
		},
		{
			name:           "4 task, 0 other, 2 dangling — clears 2/3 of six",
			byType:         map[string]int{"task": 4},
			order:          []string{"task"},
			dangling:       2,
			wantTo:         "task",
			strictWouldSay: "task",
			why:            "4 of 6 is exactly 2/3, which the rule admits.",
		},
		{
			name:           "8 task, 1 person, 1 dangling — comfortably over",
			byType:         map[string]int{"task": 8, "person": 1},
			order:          []string{"task", "person"},
			dangling:       1,
			wantTo:         "task",
			strictWouldSay: "task",
			why:            "8 of 10.",
		},
		{
			name:           "1 task, 9 dangling — one resolved link is not evidence",
			byType:         map[string]int{"task": 1},
			order:          []string{"task"},
			dangling:       9,
			wantTo:         "",
			strictWouldSay: "task",
			why:            "the narrow reading calls this UNANIMOUS (1 of 1) and writes `to: task` off a single link out of ten. That is the failure mode with the largest gap between the two readings.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := danglingIndex(tc.byType)
			po := linksWithDangling("related", tc.byType, tc.order, tc.dangling)

			gotTo, rep := inferRelationTarget("contact", po, idx)

			if gotTo != tc.wantTo {
				t.Errorf("to = %q, want %q.\n  %s", gotTo, tc.wantTo, tc.why)
			}
			if tc.wantTo == "" && tc.strictWouldSay != "" && gotTo == tc.strictWouldSay {
				t.Errorf("the narrow \"resolved targets only\" reading of FR-104a is in force: it declared to=%q, and this case exists because the requirement's stated purpose says it must not", gotTo)
			}

			if rep == nil {
				t.Fatal("a non-unanimous or partly-dangling property must always carry a report")
			}
			resolved := 0
			for _, n := range tc.byType {
				resolved += n
			}
			if rep.ResolvedTotal != resolved {
				t.Errorf("ResolvedTotal = %d, want %d", rep.ResolvedTotal, resolved)
			}
			if want := resolved + tc.dangling; rep.LinkTotal != want {
				t.Errorf("LinkTotal = %d, want %d — the denominator must count every link, not only the ones that resolved", rep.LinkTotal, want)
			}
			if rep.Unresolved != tc.dangling {
				t.Errorf("Unresolved = %d, want %d", rep.Unresolved, tc.dangling)
			}

			if tc.wantTo == "" {
				if rep.Declared != "text" {
					t.Errorf("Declared = %q, want text — FR-034 rejects a relation with an empty `to:` outright", rep.Declared)
				}
				if rep.Remedy == "" {
					t.Error("a property FR-104a refuses to type must name the one-line knowledge_configure fix")
				}
			} else {
				if rep.Declared != "relation" {
					t.Errorf("Declared = %q, want relation", rep.Declared)
				}
				if rep.MajorityType != tc.wantTo {
					t.Errorf("MajorityType = %q, want %q", rep.MajorityType, tc.wantTo)
				}
			}
		})
	}
}

// TestInferRelationTarget_BothRatiosAreCarried checks that the narrower
// reading stays VISIBLE. The two readings disagree on real properties in the
// founder's vault, so an operator reading the report must be able to see what
// the other reading would have said without taking a code comment's word for
// it.
func TestInferRelationTarget_BothRatiosAreCarried(t *testing.T) {
	byType := map[string]int{"task": 2, "person": 1}
	idx := danglingIndex(byType)
	po := linksWithDangling("related", byType, []string{"task", "person"}, 2)

	to, rep := inferRelationTarget("contact", po, idx)
	if to != "" {
		t.Fatalf("expected the founder's case to be refused, got to=%q", to)
	}
	if rep.StrictNumerator != 2 || rep.StrictDenominator != 3 {
		t.Errorf("the narrow reading's ratio is reported as %d of %d, want 2 of 3 — without it the report cannot show which reading was applied",
			rep.StrictNumerator, rep.StrictDenominator)
	}
	if rep.LinkTotal != 5 {
		t.Errorf("LinkTotal = %d, want 5", rep.LinkTotal)
	}
}

// TestInferRelationTarget_UnanimityRequiresEveryLinkToResolve pins the one
// branch that returns a nil report. A property where every RESOLVED link
// agreed but some links dangled is NOT unanimous — it has a shortfall an
// operator should see — so it must still carry a report.
func TestInferRelationTarget_UnanimityRequiresEveryLinkToResolve(t *testing.T) {
	byType := map[string]int{"task": 4}

	t.Run("no dangling links: unanimous, nothing to report", func(t *testing.T) {
		to, rep := inferRelationTarget("contact", linksWithDangling("related", byType, []string{"task"}, 0), danglingIndex(byType))
		if to != "task" {
			t.Errorf("to = %q, want task", to)
		}
		if rep != nil {
			t.Errorf("a genuinely unanimous property should carry no split report, got %+v", rep)
		}
	})

	t.Run("two dangling links: reported, not silent", func(t *testing.T) {
		to, rep := inferRelationTarget("contact", linksWithDangling("related", byType, []string{"task"}, 2), danglingIndex(byType))
		if to != "task" {
			t.Errorf("to = %q, want task (4 of 6 clears 2/3)", to)
		}
		if rep == nil {
			t.Fatal("4 of 6 links resolved to task and 2 dangled — that shortfall must be reported, not swallowed by the unanimity branch")
		}
		if rep.Rule != RelationSupermajority {
			t.Errorf("Rule = %q, want %q", rep.Rule, RelationSupermajority)
		}
		if rep.Unresolved != 2 {
			t.Errorf("Unresolved = %d, want 2", rep.Unresolved)
		}
	})
}

// ---------------------------------------------------------------------------
// The threshold, at its exact boundary.
// ---------------------------------------------------------------------------

// TestInferRelationTarget_SupermajorityThreshold is FR-104a's rule as a
// boundary table. The test is `count/total >= 2/3` in exact integer
// arithmetic, so 2 of 3 and 6 of 9 PASS while 4 of 7 and 5 of 9 FAIL, with
// no rounding to argue about.
//
// The 4-of-7 row is the one that matters: under the PLURALITY rule this
// replaced, `contact` would win outright and be written into the schema as
// `to: contact` with a majority of the evidence against it.
func TestInferRelationTarget_SupermajorityThreshold(t *testing.T) {
	cases := []struct {
		contacts, tasks int
		wantTo          string
		wantRule        RelationRule
		wantDeclared    string
	}{
		{3, 0, "contact", RelationUnanimous, ""},             // unanimous: nil report
		{2, 1, "contact", RelationSupermajority, "relation"}, // exactly 2/3 — passes
		{6, 3, "contact", RelationSupermajority, "relation"}, // exactly 2/3 — passes
		{7, 3, "contact", RelationSupermajority, "relation"}, // 7/10 — passes
		{4, 3, "", RelationNoMajority, "text"},               // 4/7 — just under
		{5, 4, "", RelationNoMajority, "text"},               // 5/9 — just under
		{1, 1, "", RelationNoMajority, "text"},               // a dead tie is never a winner
	}
	for _, tc := range cases {
		name := fmt.Sprintf("%d_contact_%d_task", tc.contacts, tc.tasks)
		t.Run(name, func(t *testing.T) {
			idx, targets := mixedIndex(tc.contacts, tc.tasks)
			po := linksTo("related", targets...)

			to, rep := inferRelationTarget("project", po, idx)

			if to != tc.wantTo {
				t.Errorf("to = %q, want %q (%d of %d resolved links)",
					to, tc.wantTo, tc.contacts, tc.contacts+tc.tasks)
			}
			if tc.wantRule == RelationUnanimous {
				if rep != nil {
					t.Errorf("a unanimous target produced a split report: %+v", rep)
				}
				return
			}
			if rep == nil {
				t.Fatalf("no split report for a non-unanimous inference (rule %s)", tc.wantRule)
			}
			if rep.Rule != tc.wantRule {
				t.Errorf("rule = %q, want %q", rep.Rule, tc.wantRule)
			}
			if rep.Declared != tc.wantDeclared {
				t.Errorf("declared = %q, want %q", rep.Declared, tc.wantDeclared)
			}
			if rep.ResolvedTotal != tc.contacts+tc.tasks {
				t.Errorf("resolved total = %d, want %d", rep.ResolvedTotal, tc.contacts+tc.tasks)
			}
		})
	}
}

// TestInferRelationTarget_SupermajorityNamesTheMinority: FR-104a requires
// the minority to be reported BY NAME. Those links are real type mismatches
// and D5/FR-034's relation_type_mismatch finding is where they surface —
// the operator has to be told which ones, not merely that some exist.
func TestInferRelationTarget_SupermajorityNamesTheMinority(t *testing.T) {
	idx, targets := mixedIndex(2, 1)
	to, rep := inferRelationTarget("project", linksTo("related", targets...), idx)

	if to != "contact" {
		t.Fatalf("to = %q, want contact", to)
	}
	if rep == nil {
		t.Fatal("no split report")
	}
	if rep.MajorityType != "contact" || rep.MajorityCount != 2 {
		t.Errorf("majority = %s x%d, want contact x2", rep.MajorityType, rep.MajorityCount)
	}
	if len(rep.Minority) != 1 || !strings.Contains(rep.Minority[0], "task") {
		t.Errorf("minority = %v, want the one task link named", rep.Minority)
	}
	if !strings.Contains(rep.Minority[0], "1") {
		t.Errorf("minority %q does not carry its count", rep.Minority[0])
	}
}

// TestInferRelationTarget_NoMajorityNamesTheFix: the report must hand the
// operator the one-line edit, not just the problem. A finding with no
// remedy is a complaint.
func TestInferRelationTarget_NoMajorityNamesTheFix(t *testing.T) {
	idx, targets := mixedIndex(4, 3)
	to, rep := inferRelationTarget("project", linksTo("related", targets...), idx)

	if to != "" {
		t.Fatalf("to = %q, want empty — 4 of 7 is below the threshold", to)
	}
	if rep.Remedy == "" {
		t.Fatal("no remedy named for a property the importer refused to type")
	}
	if !strings.Contains(rep.Remedy, "knowledge_configure") {
		t.Errorf("remedy does not name the command that fixes it: %q", rep.Remedy)
	}
	for _, want := range []string{"project", "related", "contact", "task"} {
		if !strings.Contains(rep.Remedy, want) {
			t.Errorf("remedy omits %q, so it cannot be run as written: %q", want, rep.Remedy)
		}
	}
	// With no majority, the report must present the WHOLE evidence set —
	// that is what the operator chooses from.
	if len(rep.Minority) != 2 {
		t.Errorf("evidence set = %v, want both candidate types", rep.Minority)
	}
	if rep.MajorityType != "" || rep.MajorityCount != 0 {
		t.Errorf("a no-majority report still names a majority: %s x%d — that is the plurality rule leaking back in",
			rep.MajorityType, rep.MajorityCount)
	}
}

// TestInferRelationTarget_NothingResolved covers the two ways a link can
// fail to be evidence: no note has that title, and the note it names is not
// a record. Neither is evidence for any target type.
func TestInferRelationTarget_NothingResolved(t *testing.T) {
	t.Run("dangling links", func(t *testing.T) {
		idx := nameIndexOf(map[string]string{"somewhere": "contact"})
		to, rep := inferRelationTarget("project", linksTo("related", "Ghost", "Phantom"), idx)
		if to != "" {
			t.Errorf("to = %q, want empty", to)
		}
		if rep.Rule != RelationUnresolved || rep.Declared != "text" {
			t.Errorf("rule = %q declared = %q, want unresolved/text", rep.Rule, rep.Declared)
		}
		if rep.Unresolved != 2 {
			t.Errorf("unresolved = %d, want 2", rep.Unresolved)
		}
		if rep.Remedy == "" {
			t.Error("no remedy named")
		}
	})

	t.Run("links resolve to notes that are not records", func(t *testing.T) {
		// An empty type is a real note that carries no `type:`. It resolves,
		// so it is not "unresolved", but it is not evidence for a type
		// either — and it must never be counted as one.
		idx := nameIndexOf(map[string]string{"readme": "", "scratch": ""})
		to, rep := inferRelationTarget("project", linksTo("related", "README", "Scratch"), idx)
		if to != "" {
			t.Fatalf("to = %q, want empty — a non-record note is not evidence for any type", to)
		}
		if rep.ResolvedTotal != 0 {
			t.Errorf("resolved total = %d, want 0", rep.ResolvedTotal)
		}
		if rep.Rule != RelationUnresolved {
			t.Errorf("rule = %q, want unresolved", rep.Rule)
		}
	})
}

// ---------------------------------------------------------------------------
// The FR-034 consequence: a relation with no `to:` is rejected at load time,
// so "no majority" must declare TEXT, never a relation with a blank target.
// ---------------------------------------------------------------------------

func TestClassifyProperty_NoMajorityDeclaresTextNotABrokenRelation(t *testing.T) {
	idx, targets := mixedIndex(4, 3)
	po := linksTo("related", targets...)

	ip := classifyProperty("project", po, len(po.Values), idx)

	if ip.Type == records.TypeRelation {
		t.Fatalf("declared a relation with to=%q — schema load REJECTS a relation with no target, taking the whole record type down", ip.To)
	}
	if ip.Type != records.TypeText {
		t.Errorf("type = %q, want text", ip.Type)
	}
	if ip.To != "" {
		t.Errorf("to = %q on a text property", ip.To)
	}
	if ip.RelationSplit == nil {
		t.Error("the refusal was not reported at all — it would be invisible to the operator")
	}
}

func TestClassifyProperty_UnanimousLinksDeclareARelation(t *testing.T) {
	idx, targets := mixedIndex(3, 0)
	po := linksTo("owner", targets...)

	ip := classifyProperty("project", po, len(po.Values), idx)

	if ip.Type != records.TypeRelation {
		t.Fatalf("type = %q, want relation", ip.Type)
	}
	if ip.To != "contact" {
		t.Errorf("to = %q, want contact", ip.To)
	}
	if ip.Kind != ClassifyRelation {
		t.Errorf("kind = %q, want %q", ip.Kind, ClassifyRelation)
	}
	if ip.RelationSplit != nil {
		t.Error("a unanimous relation produced a split report — there is nothing split about it")
	}
}

// ---------------------------------------------------------------------------
// Determinism.
// ---------------------------------------------------------------------------

// TestHighestCount_TieBreaksByNameNotMapOrder: Go randomises map iteration,
// so a tie decided by "whichever key came first" gives a different schema on
// different runs of the same vault. The tie-break is by name.
func TestHighestCount_TieBreaksByNameNotMapOrder(t *testing.T) {
	byType := map[string]int{"zebra": 4, "alpha": 4, "middle": 4}
	first, firstCount := highestCount(byType)
	if firstCount != 4 {
		t.Fatalf("count = %d, want 4", firstCount)
	}
	if first != "alpha" {
		t.Errorf("tie broke to %q, want the lexically first name \"alpha\"", first)
	}
	for i := 0; i < 200; i++ {
		got, _ := highestCount(byType)
		if got != first {
			t.Fatalf("highestCount is not deterministic: got %q then %q on iteration %d", first, got, i)
		}
	}
}

// TestInferRelationTarget_IsDeterministicAcrossRuns runs the whole inference
// repeatedly over a tied vault. The answer, and the reported evidence, must
// be byte-identical every time or two imports of one vault disagree.
func TestInferRelationTarget_IsDeterministicAcrossRuns(t *testing.T) {
	idx, targets := mixedIndex(3, 3)
	var wantTo string
	var wantMinority string
	for i := 0; i < 100; i++ {
		to, rep := inferRelationTarget("project", linksTo("related", targets...), idx)
		minority := strings.Join(rep.Minority, "|")
		if i == 0 {
			wantTo, wantMinority = to, minority
			continue
		}
		if to != wantTo || minority != wantMinority {
			t.Fatalf("run %d disagreed: to=%q minority=%q, first run said to=%q minority=%q",
				i, to, minority, wantTo, wantMinority)
		}
	}
}

// ---------------------------------------------------------------------------
// End-to-end through the real grouping path.
// ---------------------------------------------------------------------------

// TestInferSchema_RelationTargetFromRealNotes drives FR-104a through the
// functions the importer actually calls — CollectTypeGroups, BuildNameIndex,
// InferSchema — rather than the internal one, so the wiring between them is
// covered too.
func TestInferSchema_RelationTargetFromRealNotes(t *testing.T) {
	dir := t.TempDir()
	notes := []NoteRecord{
		noteOnDisk(t, dir, "Alice.md", "---\ntype: contact\nname: Alice\n---\n"),
		noteOnDisk(t, dir, "Bob.md", "---\ntype: contact\nname: Bob\n---\n"),
		noteOnDisk(t, dir, "Carol.md", "---\ntype: contact\nname: Carol\n---\n"),
		noteOnDisk(t, dir, "Apollo.md", "---\ntype: project\nowner: \"[[Alice]]\"\n---\n"),
		noteOnDisk(t, dir, "Gemini.md", "---\ntype: project\nowner: \"[[Bob]]\"\n---\n"),
		noteOnDisk(t, dir, "Mercury.md", "---\ntype: project\nowner: \"[[Carol]]\"\n---\n"),
	}

	groups := CollectTypeGroups(notes)
	idx := BuildNameIndex(notes)
	got := InferSchema(groups["project"], idx)

	var owner *InferredProperty
	for i := range got {
		if got[i].Name == "owner" {
			owner = &got[i]
		}
	}
	if owner == nil {
		t.Fatalf("no `owner` property inferred for type project; got %+v", got)
	}
	if owner.Type != records.TypeRelation {
		t.Fatalf("owner type = %q, want relation", owner.Type)
	}
	if owner.To != "contact" {
		t.Errorf("owner to = %q, want contact — every link resolves to a contact note", owner.To)
	}
}
