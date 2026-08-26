// Omnipus — tests for FR-007, FR-008 and the §8 rules this slice implements.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"strings"
	"testing"
)

const filterFixture = `
schema_version: 1
type: widget
properties:
  name:    { type: text }
  status:  { type: enum, values: [todo, doing, done] }
  segment: { type: enum, values: [vendor, customer, partner], many: true }
  count:   { type: number }
  arr:     { type: money }
`

func filterSchema(t *testing.T) (*SchemaSet, *Schema) {
	t.Helper()
	set := loadSet(t, map[string]string{"widget.yaml": filterFixture})
	sc, _ := set.Get("widget")
	return set, sc
}

// TestFilter_AbsentIsDistinctAndIncludedByNegation covers FR-007 and FR-008 —
// spec §7 test 5, and the edge-case table's first row.
//
// ADR-068 D3.2 names the real-world failure: "the checkbox third state. 'Days
// I did not meditate' currently omits every day with no value — precisely the
// days being asked about."
func TestFilter_AbsentIsDistinctAndIncludedByNegation(t *testing.T) {
	_, sc := filterSchema(t)

	absent := ParseRecord("absent.md", []byte("---\ntype: widget\nname: A\n---\n"))
	nullValued := ParseRecord("null.md", []byte("---\ntype: widget\nname: N\nstatus:\n---\n"))
	done := ParseRecord("done.md", []byte("---\ntype: widget\nname: D\nstatus: done\n---\n"))
	todo := ParseRecord("todo.md", []byte("---\ntype: widget\nname: T\nstatus: todo\n---\n"))
	corrupt := ParseRecord("corrupt.md", []byte("---\ntype: widget\nname: C\nstatus: shipped\n---\n"))

	match := func(t *testing.T, f Filter, rec Record) MatchResult {
		t.Helper()
		res, err := f.Match(sc, rec)
		if err != nil {
			t.Fatalf("filter %+v on %s: %v", f, rec.Path, err)
		}
		return res
	}

	t.Run("FR-007 absent is a distinct state, not a value", func(t *testing.T) {
		// `status is absent` is true for the absent record and false for every
		// record that holds a value — including the one holding a value that
		// does not conform.
		f := Filter{Property: "status", Op: OpIsAbsent}
		for _, tc := range []struct {
			rec  Record
			want bool
		}{
			{absent, true},
			{nullValued, true}, // an empty key is absence, not a value
			{done, false},
			{todo, false},
			{corrupt, false}, // something IS written there; it is just wrong
		} {
			if got := match(t, f, tc.rec).Matched; got != tc.want {
				t.Fatalf("FR-007: `status is absent` on %s: want %v, got %v", tc.rec.Path, tc.want, got)
			}
		}
	})

	t.Run("FR-007 absent never equals a value", func(t *testing.T) {
		// §8 R-2: a comparison where either side is absent is FALSE for every
		// operator except `is absent`.
		//
		// The operators listed are those DEFINED for an enum. R-2 is an
		// EVALUATION rule; whether an operator is defined for the type is a
		// VALIDATION rule that runs first, so an undefined operator never
		// reaches R-2 at all. That case is asserted separately below — it must
		// be REFUSED, which is a stronger outcome than R-2's false.
		//
		// This list previously included OpContains, which is not defined for an
		// enum. It passed only because an undefined operator used to evaluate
		// to a silent false — the behaviour FR-024 exists to remove.
		for _, op := range []Operator{OpEqual, OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual} {
			f := Filter{Property: "status", Op: op, Literal: "done"}
			if match(t, f, absent).Matched {
				t.Fatalf("§8 R-2: `status %s done` must be false when status is absent", op)
			}
		}
	})

	t.Run("FR-024 an operator undefined for the type is REFUSED, not silently empty", func(t *testing.T) {
		// `contains` is substring matching on text and whole-element membership
		// on a list. Neither applies to a scalar enum, so it is not defined
		// there. Before this was checked, such a filter was accepted and then
		// every record returned false with one identical complaint attached —
		// a caller got an empty answer and 5,000 copies of the same problem
		// instead of one refusal naming what would have worked.
		f := Filter{Property: "status", Op: OpContains, Literal: "done"}
		_, _, err := f.Validate(sc)
		if err == nil {
			t.Fatal("`status contains done` on an enum must be refused at validation, not evaluated to an empty result")
		}
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Fatalf("the refusal must be a QueryError so a caller can read it; got %T", err)
		}
		if len(qe.ValidNames) == 0 {
			t.Fatal("the refusal must NAME the operators that would have worked — that is the whole of FR-024")
		}
	})

	t.Run("FR-008 a negative filter INCLUDES the absent record", func(t *testing.T) {
		// This is the requirement in one assertion. `status is not done` must
		// return the record that has no status at all.
		f := Filter{Property: "status", Op: OpEqual, Negate: true, Literal: "done"}

		if !match(t, f, absent).Matched {
			t.Fatalf("FR-008: `status != done` must INCLUDE a record whose status is absent")
		}
		if !match(t, f, nullValued).Matched {
			t.Fatalf("FR-008: an explicitly empty key is absence and must be included too")
		}
		if !match(t, f, todo).Matched {
			t.Fatalf("`status != done` must include a record whose status is todo")
		}
		if match(t, f, done).Matched {
			t.Fatalf("`status != done` must exclude a record whose status IS done")
		}
	})

	t.Run("FR-008 the exclusion is explicit and opt-in", func(t *testing.T) {
		// "...unless the query excludes them explicitly."
		f := Filter{Property: "status", Op: OpEqual, Negate: true, Literal: "done", ExcludeAbsent: true}
		if match(t, f, absent).Matched {
			t.Fatalf("FR-008: with ExcludeAbsent set, the absent record must be excluded")
		}
		if !match(t, f, todo).Matched {
			t.Fatalf("ExcludeAbsent must not change records that hold a value")
		}
	})

	t.Run("FR-008 the default is inclusion, so forgetting the field cannot give the wrong answer", func(t *testing.T) {
		zero := Filter{Property: "status", Op: OpEqual, Negate: true, Literal: "done"}
		if zero.ExcludeAbsent {
			t.Fatalf("ExcludeAbsent must default to false so FR-008's behaviour is the default")
		}
		if !match(t, zero, absent).Matched {
			t.Fatalf("the zero-value filter must exhibit FR-008's inclusion")
		}
	})

	t.Run("§8 R-4 a non-conforming value is REPORTED and excluded, not swept in", func(t *testing.T) {
		// `shipped` is not in the declared set. R-4: it is false for every
		// operator AND the record is added to the problem list. Including it
		// in a negative filter would be a silent wrong answer.
		neg := Filter{Property: "status", Op: OpEqual, Negate: true, Literal: "done"}
		res := match(t, neg, corrupt)
		if res.Matched {
			t.Fatalf("§8 R-4: a corrupt value must not be swept into a negative filter's results")
		}
		if res.State != StateNonConforming {
			t.Fatalf("expected state %v, got %v", StateNonConforming, res.State)
		}
		if len(res.Problems) == 0 {
			t.Fatalf("§8 R-4 / FR-026: the record must be NAMED in the problem list, not silently dropped")
		}
		p := res.Problems[0]
		if p.RecordPath != "corrupt.md" {
			t.Fatalf("the problem must name the record; got %q", p.RecordPath)
		}
		if p.Code != FindingEnumNotPermitted || p.Got != "shipped" {
			t.Fatalf("the problem must say what was wrong; got code=%q value=%q", p.Code, p.Got)
		}

		pos := Filter{Property: "status", Op: OpEqual, Literal: "done"}
		if match(t, pos, corrupt).Matched {
			t.Fatalf("§8 R-4: a corrupt value is false for a positive filter too")
		}
	})

	t.Run("§8 R-4 absence and corruption are not the same exclusion", func(t *testing.T) {
		neg := Filter{Property: "status", Op: OpEqual, Negate: true, Literal: "done"}
		a := match(t, neg, absent)
		c := match(t, neg, corrupt)
		if a.State == c.State {
			t.Fatalf("absence and corruption must remain distinguishable; both reported %v", a.State)
		}
		if len(a.Problems) != 0 {
			t.Fatalf("absence is a legitimate state and must not raise a problem; got %v", a.Problems)
		}
	})

	t.Run("FR-024 a filter on an unknown property is REJECTED, not answered with zero records", func(t *testing.T) {
		f := Filter{Property: "statuz", Op: OpEqual, Literal: "done"}
		_, err := f.Match(sc, done)
		if err == nil {
			t.Fatalf("FR-024 / US-2.4: a mistyped property must be rejected, never quietly return no matches")
		}
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Fatalf("expected a *QueryError; got %T", err)
		}
		for _, name := range []string{"name", "status", "segment", "count", "arr"} {
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("FR-024 requires the valid property names listed; %q missing from %q", name, err.Error())
			}
		}
	})

	t.Run("FR-011/FR-024 a filter on an unknown enum value is rejected listing the set", func(t *testing.T) {
		f := Filter{Property: "status", Op: OpEqual, Literal: "shipped"}
		_, err := f.Match(sc, done)
		if err == nil {
			t.Fatalf("an enum literal outside the declared set must be rejected, not matched against nothing")
		}
		for _, v := range []string{"todo", "doing", "done"} {
			if !strings.Contains(err.Error(), v) {
				t.Fatalf("the rejection must list the permitted values; %q missing from %q", v, err.Error())
			}
		}
	})
}

// TestFilter_ListSemantics covers §8 R-9 and the negation-of-a-list rule.
func TestFilter_ListSemantics(t *testing.T) {
	_, sc := filterSchema(t)
	both := ParseRecord("both.md", []byte("---\ntype: widget\nsegment: [vendor, customer]\n---\n"))
	empty := ParseRecord("empty.md", []byte("---\ntype: widget\nsegment: []\n---\n"))
	none := ParseRecord("none.md", []byte("---\ntype: widget\n---\n"))

	match := func(t *testing.T, f Filter, rec Record) MatchResult {
		t.Helper()
		res, err := f.Match(sc, rec)
		if err != nil {
			t.Fatalf("%v", err)
		}
		return res
	}

	t.Run("§8 R-9 contains on a list is whole-element membership", func(t *testing.T) {
		if !match(t, Filter{Property: "segment", Op: OpContains, Literal: "vendor"}, both).Matched {
			t.Fatalf("`segment contains vendor` must match [vendor, customer]")
		}
		if !match(t, Filter{Property: "segment", Op: OpContains, Literal: "customer"}, both).Matched {
			t.Fatalf("membership must find any element, not only the first")
		}
		if match(t, Filter{Property: "segment", Op: OpContains, Literal: "partner"}, both).Matched {
			t.Fatalf("`partner` is not an element of [vendor, customer]")
		}
	})

	t.Run("§8 states no rule for equality across a list/scalar boundary, so it is REFUSED and reported", func(t *testing.T) {
		// This is a deliberate, reported SPEC GAP, not an implementation
		// choice. §8's only rule about lists is R-9 (`contains` is
		// whole-element membership). Nothing defines `segment = vendor` when
		// segment is a list, so the oracle refuses and says so.
		//
		// The load-bearing part is the NEGATIVE case. A refused comparison
		// must NOT be re-included by negation: `segment != vendor` on
		// [vendor, customer] returning true would be a silent wrong answer
		// about a record that IS a vendor.
		neg := Filter{Property: "segment", Op: OpEqual, Negate: true, Literal: "vendor"}
		res := match(t, neg, both)
		if res.Matched {
			t.Fatalf("a comparison the oracle refused must not be swept in by negation")
		}
		if len(res.ComparisonProblems) == 0 {
			t.Fatalf("the refusal must be REPORTED, not silent")
		}
		if got := res.ComparisonProblems[0].Code; got != CompareArityNotDefined {
			t.Fatalf("expected %q, got %q", CompareArityNotDefined, got)
		}
		pos := Filter{Property: "segment", Op: OpEqual, Literal: "vendor"}
		if match(t, pos, both).Matched {
			t.Fatalf("the positive form is refused too")
		}
	})

	t.Run("R-3 an empty list is a value, not absence", func(t *testing.T) {
		res := match(t, Filter{Property: "segment", Op: OpIsAbsent}, empty)
		if res.Matched {
			t.Fatalf("§8 R-3: an empty list is a VALUE, so `is absent` must be false")
		}
		if res.State != StatePresent {
			t.Fatalf("expected %v, got %v", StatePresent, res.State)
		}
		if !match(t, Filter{Property: "segment", Op: OpIsAbsent}, none).Matched {
			t.Fatalf("a missing key IS absent")
		}
	})
}

// TestCompare_ThroughTheRealFilterPath covers §8 R-1, R-11 and AC-8.3 — and it
// drives them through Filter.Match, the path real queries take, not through a
// comparator reachable only from a test.
func TestCompare_ThroughTheRealFilterPath(t *testing.T) {
	_, sc := filterSchema(t)
	three := ParseRecord("three.md", []byte("---\ntype: widget\nname: n\ncount: 3\n---\n"))

	match := func(t *testing.T, f Filter, rec Record) MatchResult {
		t.Helper()
		res, err := f.Match(sc, rec)
		if err != nil {
			t.Fatalf("%v", err)
		}
		return res
	}

	t.Run("AC-8.3 3 > 2 is true, through a real filter on a real record", func(t *testing.T) {
		// §8 states this explicitly because a first-attempt `any` overload made
		// it evaluate to FALSE with nothing reporting an error.
		if !match(t, Filter{Property: "count", Op: OpGreater, Literal: "2"}, three).Matched {
			t.Fatalf("AC-8.3: `count > 2` must match a record whose count is 3")
		}
		if match(t, Filter{Property: "count", Op: OpLess, Literal: "2"}, three).Matched {
			t.Fatalf("`count < 2` must not match a record whose count is 3")
		}
		if !match(t, Filter{Property: "count", Op: OpEqual, Literal: "3"}, three).Matched {
			t.Fatalf("`count = 3` must match")
		}
	})

	t.Run("R-6 a cross-currency comparison is refused and reported, not answered", func(t *testing.T) {
		eur := ParseRecord("eur.md", []byte("---\ntype: widget\narr: 100.00 EUR\n---\n"))
		res := match(t, Filter{Property: "arr", Op: OpGreater, Literal: "1.00 SGD"}, eur)
		if res.Matched {
			t.Fatalf("§8 R-6: money compares only within one currency")
		}
		if len(res.ComparisonProblems) == 0 {
			t.Fatalf("the refusal must reach the caller; ADR-068 O-2 forbids conversion, so silence here is the defect")
		}
		if got := res.ComparisonProblems[0].Code; got != CompareCrossCurrency {
			t.Fatalf("expected %q, got %q", CompareCrossCurrency, got)
		}
		if !res.Matched && len(res.ComparisonProblems[0].Currencies) != 2 {
			t.Fatalf("the problem must name the currencies present; got %v", res.ComparisonProblems[0].Currencies)
		}
	})

	t.Run("R-1 different declared types do not compare", func(t *testing.T) {
		// A single Filter cannot express a cross-type comparison — both sides
		// come from one declared property, which is FR-009 doing its job. So
		// this drives the oracle directly, with differently-typed operands.
		//
		// The PAIRS here are chosen deliberately. An earlier version used only
		// text-vs-number, and that assertion passed even with R-1 deleted:
		// text has no ordering defined, so the comparison was refused for a
		// different reason and the test could not tell the two apart. Each pair
		// below is one where removing R-1 yields a confident WRONG answer
		// rather than an incidental refusal — number-vs-money reads money's
		// zero-valued Number field and reports 2 > 0.
		count, _ := sc.Property("count")
		name, _ := sc.Property("name")
		arr, _ := sc.Property("arr")
		status, _ := sc.Property("status")

		parse := func(p *Property, text string) TypedValue {
			t.Helper()
			v, verr := ParseValue(p, Node{Kind: KindScalar, Text: text})
			if verr != nil {
				t.Fatalf("parsing %q as %s: %v", text, p.Type, verr)
			}
			return v
		}

		pairs := []struct {
			name string
			a, b TypedValue
		}{
			{"text vs number", parse(name, "3"), parse(count, "2")},
			{"number vs money", parse(count, "2"), parse(arr, "1.00 EUR")},
			{"money vs number", parse(arr, "1.00 EUR"), parse(count, "2")},
			{"enum vs number", parse(status, "doing"), parse(count, "2")},
			{"number vs enum", parse(count, "2"), parse(status, "doing")},
		}
		for _, pair := range pairs {
			if cmp, ok := Compare(pair.a, pair.b); ok {
				t.Fatalf("§8 R-1: %s must not compare; got (%d, true)", pair.name, cmp)
			}
		}
	})

	t.Run("Compare owns no semantics — it agrees with the oracle it delegates to", func(t *testing.T) {
		count, _ := sc.Property("count")
		a, _ := ParseValue(count, Node{Kind: KindScalar, Text: "3"})
		b, _ := ParseValue(count, Node{Kind: KindScalar, Text: "2"})

		cmp, ok := Compare(a, b)
		if !ok || cmp != 1 {
			t.Fatalf("Compare(3, 2) must be (1, true); got (%d, %v)", cmp, ok)
		}

		var c Comparator
		gt, problems := c.Evaluate(OpGreater, singletonOperand(a), singletonOperand(b))
		if len(problems) > 0 || !gt {
			t.Fatalf("the oracle must agree: gt=%v problems=%v", gt, problems)
		}
	})
}
