// Omnipus — §8 R-5 ordering, as a CONSUMER of this package can reach it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// This file is in package records_test, not records, and that is the whole
// point of it. The defect it pins could not be seen from inside the package.
//
// WHAT WENT WRONG
//
// Two authorities answered "what is this enum value's ordinal?", and they
// agreed only by accident:
//
//	Property.EnumPosition   — the index into the property's declared Values,
//	                          scanned out of the slice when the O(1) cache was
//	                          never built. Documented as supporting a Property
//	                          assembled outside the package with a plain struct
//	                          literal.
//	EnumValue.Position      — a struct FIELD, stamped only by the schema loader
//	                          and NewProperty. On a struct-literal Property
//	                          nobody stamps it, so it is zero on every value.
//
// compare_oracle.go's R-5 branch read the FIELD. So for the construction
// EnumPosition explicitly blesses, `todo < done` came back FALSE and
// `done <= todo` came back TRUE — with NO ComparisonProblem reported — while
// SortByEnumOrder, which goes through EnumPosition, sorted the very same three
// values [todo doing done] correctly. One property, two contradictory answers,
// no complaint: the silent-wrong-answer class ADR-068 exists to remove.
//
// WHY THE SUITE MISSED IT, AND WHY THIS FILE IS EXTERNAL
//
// Both existing enum fixtures hand-fill Position to match the slice index —
// compare_truthtable_test.go's stageValues did, and
// external_property_test.go's struct-literal case does. A fixture that fills in
// the field cannot detect code that depends on the field being filled in. The
// test below OMITS Position, which is what a consumer writing the obvious
// struct literal actually produces.

package records_test

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// enumOrderingProperty is the construction under test: a plain struct literal,
// Position OMITTED on every value. `Property.valuePos` is unexported, so this is
// the only enum property a consumer can build without calling NewProperty — and
// EnumPosition's doc comment promises it "behaves exactly like a parsed one".
//
// The declared order deliberately contradicts the lexical order: lexically
// "doing" < "done" < "todo"; by declared position todo(0) < doing(1) < done(2).
// A comparator that fell back to spelling would fail this fixture too.
func enumOrderingProperty() *records.Property {
	return &records.Property{
		Name:       "status",
		Type:       records.TypeEnum,
		RecordType: "widget",
		Values: []records.EnumValue{
			{Name: "todo"},
			{Name: "doing"},
			{Name: "done"},
		},
	}
}

// resolveStatus parses a note carrying one declared value and hands back the
// operand the oracle takes. It goes through the real parse path rather than
// hand-building a TypedValue, because hand-building one is how a test comes to
// fill in the very field the production path leaves empty.
func resolveStatus(t *testing.T, prop *records.Property, value string) records.PropertyValue {
	t.Helper()
	rec := records.ParseRecord("notes/a.md",
		[]byte("---\ntype: widget\nname: A\nstatus: "+value+"\n---\nbody\n"))
	pv := records.ResolveProperty(rec, prop)
	if pv.State != records.StatePresent {
		t.Fatalf("%q is declared by the property, so it must resolve present; got %s", value, pv.State)
	}
	return pv
}

// TestExternal_EnumOrderingFollowsDeclaredPositionOnAStructLiteralProperty is
// the required proof. Every expected value below comes from FR-010/R-5 —
// "ordering is by declared position" — applied to the fixture's declared order,
// never from running the comparator and recording what it said.
func TestExternal_EnumOrderingFollowsDeclaredPositionOnAStructLiteralProperty(t *testing.T) {
	set := externalSchemaSet(t)
	prop := enumOrderingProperty()
	attach(t, set, prop)

	// Guard the fixture itself: if EnumPosition ever stopped scanning Values,
	// every assertion below would still "pass" for the wrong reason.
	for i, name := range []string{"todo", "doing", "done"} {
		pos, ok := prop.EnumPosition(name)
		if !ok || pos != i {
			t.Fatalf("fixture precondition: EnumPosition(%q) = (%d, %v), want (%d, true)", name, pos, ok, i)
		}
		if prop.Values[i].Position != 0 {
			t.Fatalf("fixture precondition: Values[%d].Position = %d, want 0 — this test is only meaningful while the field is UNSET",
				i, prop.Values[i].Position)
		}
	}

	todo := resolveStatus(t, prop, "todo")
	done := resolveStatus(t, prop, "done")

	var c records.Comparator

	// The two headline assertions the finding names.
	if got, problems := c.Evaluate(records.OpGreater, done, todo); !got || len(problems) > 0 {
		t.Errorf("R-5: done(position 2) > todo(position 0) = %v, want true (problems: %v)", got, problems)
	}
	if got, problems := c.Evaluate(records.OpLess, done, todo); got || len(problems) > 0 {
		t.Errorf("R-5: done(position 2) < todo(position 0) = %v, want false (problems: %v)", got, problems)
	}

	// The full ordering family in both directions. The bug collapsed lt/gt to
	// false and lte/gte to true, so a test asserting only one operator would
	// have been satisfied by two of the five cells by luck.
	for _, tc := range []struct {
		name string
		op   records.Operator
		l, r records.PropertyValue
		want bool
	}{
		{"todo(0) lt done(2)", records.OpLess, todo, done, true},
		{"todo(0) lte done(2)", records.OpLessOrEqual, todo, done, true},
		{"todo(0) gt done(2)", records.OpGreater, todo, done, false},
		{"todo(0) gte done(2)", records.OpGreaterOrEqual, todo, done, false},
		{"todo(0) eq done(2)", records.OpEqual, todo, done, false},

		{"done(2) lt todo(0)", records.OpLess, done, todo, false},
		{"done(2) lte todo(0)", records.OpLessOrEqual, done, todo, false},
		{"done(2) gt todo(0)", records.OpGreater, done, todo, true},
		{"done(2) gte todo(0)", records.OpGreaterOrEqual, done, todo, true},

		// Reflexivity: a value against itself pins that "equal" is really equal
		// and not two zeroes agreeing.
		{"done(2) lte done(2)", records.OpLessOrEqual, done, done, true},
		{"done(2) gte done(2)", records.OpGreaterOrEqual, done, done, true},
		{"done(2) lt done(2)", records.OpLess, done, done, false},
		{"done(2) gt done(2)", records.OpGreater, done, done, false},
		{"done(2) eq done(2)", records.OpEqual, done, done, true},
	} {
		got, problems := c.Evaluate(tc.op, tc.l, tc.r)
		if len(problems) > 0 {
			t.Errorf("R-5: %s reported %v; a value the property declares must compare cleanly", tc.name, problems)
			continue
		}
		if got != tc.want {
			t.Errorf("R-5: %s = %v, want %v — ordering must follow the DECLARED position", tc.name, got, tc.want)
		}
	}
}

// TestExternal_EnumComparatorAndSortAgreeOnOneStructLiteralProperty is the
// contradiction half. The bug's signature was not merely a wrong answer: it was
// two SURFACES of one package disagreeing about one property, which is what
// makes it undetectable from a single answer.
//
// The expected order is derived from the comparator, then checked against
// SortByEnumOrder. Both must reproduce the declared order.
func TestExternal_EnumComparatorAndSortAgreeOnOneStructLiteralProperty(t *testing.T) {
	set := externalSchemaSet(t)
	prop := enumOrderingProperty()
	attach(t, set, prop)

	names := []string{"done", "todo", "doing"}
	records.SortByEnumOrder(prop, names)

	want := []string{"todo", "doing", "done"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("FR-010: SortByEnumOrder gave %v, want %v", names, want)
		}
	}

	// Now ask the comparator the same question, pairwise, and require it to
	// confirm the sequence the sort produced. If the two ever disagree again,
	// this fails whichever way round the disagreement runs.
	var c records.Comparator
	for i := 0; i+1 < len(names); i++ {
		lower := resolveStatus(t, prop, names[i])
		higher := resolveStatus(t, prop, names[i+1])

		lt, problems := c.Evaluate(records.OpLess, lower, higher)
		if len(problems) > 0 {
			t.Fatalf("%q < %q reported %v", names[i], names[i+1], problems)
		}
		if !lt {
			t.Fatalf("the comparator and SortByEnumOrder disagree about one property: sort put %q before %q, but the comparator says %q < %q is false",
				names[i], names[i+1], names[i], names[i+1])
		}
	}
}

// TestExternal_EnumValueOutsideTheDeclaredSetIsReportedNotOrderedAtZero pins the
// consequence of routing through EnumPosition, which answers (0, false) for a
// value it does not hold.
//
// Reading that 0 as "position zero" would rank an undeclared value FIRST, ahead
// of every real one — a wrong answer manufactured out of a "not found". FR-011
// says a value outside the declared set is non-conformance, and R-4 says
// non-conformance is false for every operator AND reported.
func TestExternal_EnumValueOutsideTheDeclaredSetIsReportedNotOrderedAtZero(t *testing.T) {
	set := externalSchemaSet(t)
	prop := enumOrderingProperty()
	attach(t, set, prop)

	done := resolveStatus(t, prop, "done")

	// An operand claiming to be present, carrying a value the property does not
	// declare. The parse path refuses to build this (that is FR-011 working), so
	// it is assembled directly — which is exactly how it would arrive from a
	// caller that built its operands by hand.
	foreign := records.PropertyValue{
		Property: prop,
		State:    records.StatePresent,
		Values: []records.TypedValue{{
			Type: records.TypeEnum,
			Raw:  "archived",
			Enum: records.EnumValue{Name: "archived"},
		}},
	}

	for _, op := range []records.Operator{
		records.OpLess, records.OpLessOrEqual, records.OpGreater, records.OpGreaterOrEqual,
	} {
		got, problems := evaluate(t, op, foreign, done)
		if got {
			t.Errorf("R-4: %s against a value outside the declared set returned true", op)
		}
		if len(problems) != 1 || problems[0].Code != records.CompareNonConforming {
			t.Errorf("R-4: %s against a value outside the declared set reported %v, want one %s",
				op, problems, records.CompareNonConforming)
			continue
		}
		if problems[0].Side != "left" {
			t.Errorf("the offending operand is the left one; the problem named %q", problems[0].Side)
		}
	}
}

func evaluate(t *testing.T, op records.Operator, l, r records.PropertyValue) (bool, []records.ComparisonProblem) {
	t.Helper()
	var cmp records.Comparator
	return cmp.Evaluate(op, l, r)
}

// TestExternal_CompareRefusesEnumBecauseItHasNoDeclaredSet is FINDING 2.
//
// records.Compare takes two bare TypedValues. It synthesises a Property for each
// carrying only the declared type — no Values — so for enum it has no declared
// set on either side. The old comment claimed R-5's shared-set precondition
// "holds by construction" because both sides were synthesised the same way. It
// did not hold: enumSetsAgree compared two EMPTY sets and passed, so two enum
// values drawn from genuinely different declared sets were ordered against each
// other. `won`(position 1) of one vocabulary and `blocked`(position 1) of an
// unrelated one came back EQUAL — the exact comparison CompareEnumSetsDiffer
// exists to refuse.
//
// Compare cannot tell the two cases apart, because a TypedValue does not carry
// its set. So it must refuse, and this asserts it does.
func TestExternal_CompareRefusesEnumBecauseItHasNoDeclaredSet(t *testing.T) {
	set := externalSchemaSet(t)
	prop := enumOrderingProperty()
	attach(t, set, prop)

	// A SECOND, unrelated vocabulary, built to collide with the first in the two
	// ways that matter:
	//
	//	NAME     "doing" is declared by BOTH. R-5's equality is exact-case name
	//	         matching, so with the precondition defeated `status: doing` and
	//	         `phase: doing` compare EQUAL despite being different properties
	//	         of different vocabularies. Equality never reaches the ordering
	//	         path, so this hole is Finding 2's alone.
	//	POSITION "shipped" sits at declared position 2, and so does "done".
	//	         Ordering by position alone ranks them equal.
	other := &records.Property{
		Name:       "phase",
		Type:       records.TypeEnum,
		RecordType: "widget",
		Values: []records.EnumValue{
			{Name: "open"},
			{Name: "doing"},
			{Name: "shipped"},
		},
	}
	sc, ok := set.Get("widget")
	if !ok {
		t.Fatalf("fixture schema did not load")
	}
	sc.Properties[other.Name] = other
	sc.PropertyOrder = append(sc.PropertyOrder, other.Name)

	resolvePhase := func(value string) records.TypedValue {
		t.Helper()
		rec := records.ParseRecord("notes/b.md",
			[]byte("---\ntype: widget\nname: B\nphase: "+value+"\n---\nbody\n"))
		pv := records.ResolveProperty(rec, other)
		if pv.State != records.StatePresent {
			t.Fatalf("fixture precondition: phase=%s must resolve present; got %s", value, pv.State)
		}
		return pv.Values[0]
	}

	statusDoing := resolveStatus(t, prop, "doing").Values[0]
	statusDone := resolveStatus(t, prop, "done").Values[0]
	phaseDoing := resolvePhase("doing")
	phaseShipped := resolvePhase("shipped")

	// The equality hole, and the sharpest evidence that the precondition was
	// defeated rather than satisfied: two values from unrelated vocabularies
	// that happen to share a spelling must NOT come back equal.
	if cmp, cmpOK := records.Compare(statusDoing, phaseDoing); cmpOK {
		t.Errorf("Compare said `status: doing` and `phase: doing` relate (cmp=%d) — they are values of two different declared sets that merely share a spelling, and R-5 has no shared meaning to relate them by",
			cmp)
	}

	// The ordering hole, asked of the oracle directly so the REPORT is visible.
	// Compare collapses every problem into ok=false, so it cannot show whether
	// the refusal was reasoned or accidental.
	synth := func(v records.TypedValue) records.PropertyValue {
		return records.PropertyValue{
			Property: &records.Property{Type: records.TypeEnum},
			State:    records.StatePresent,
			Values:   []records.TypedValue{v},
		}
	}
	var cmp records.Comparator
	for _, tc := range []struct {
		name string
		op   records.Operator
		l, r records.TypedValue
	}{
		{`"status: done"(2) >= "phase: shipped"(2)`, records.OpGreaterOrEqual, statusDone, phaseShipped},
		{`"status: doing" == "phase: doing"`, records.OpEqual, statusDoing, phaseDoing},
		{`"status: doing"(1) < "phase: shipped"(2)`, records.OpLess, statusDoing, phaseShipped},
	} {
		got, problems := cmp.Evaluate(tc.op, synth(tc.l), synth(tc.r))
		if got {
			t.Errorf("%s returned TRUE; neither operand carries a declared value set, so R-5's precondition does not hold and there is nothing to answer with", tc.name)
		}
		if len(problems) != 1 || problems[0].Code != records.CompareEnumSetsDiffer {
			t.Errorf("%s reported %v, want exactly one %s — an enum operand with no declared value set must be REPORTED, not silently false",
				tc.name, problems, records.CompareEnumSetsDiffer)
			continue
		}
		// The code alone cannot distinguish "the sets differ" from "there are no
		// sets", and the second is what happened here. The message must say
		// which, or it sends the reader hunting for a mismatch between two sets
		// that were never there.
		detail := problems[0].Detail
		if !strings.Contains(detail, "declared value set") {
			t.Errorf("%s reported %q; R-5's precondition failing must name the declared value set as the thing that is missing",
				tc.name, detail)
		}
		if strings.Contains(detail, "not drawn from one declared value set") {
			t.Errorf("%s reported the SETS-DIFFER message (%q). Neither operand has a set at all; describing that as a mismatch sends the reader looking for two sets that do not exist",
				tc.name, detail)
		}
	}

	// Same-set values are refused too, and that is the honest answer rather than
	// a limitation worth apologising for: Compare cannot distinguish this case
	// from the one above, because neither TypedValue carries its set. A refusal
	// that depended on which case it was would be a guess.
	todo := resolveStatus(t, prop, "todo").Values[0]
	if _, cmpOK := records.Compare(todo, statusDoing); cmpOK {
		t.Errorf("Compare has no declared set for either operand, so it cannot establish R-5's precondition and must refuse")
	}
}
