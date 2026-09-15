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
  count:   { type: integer }
  price:   { type: decimal }
  when:    { type: date }
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
		// `status IS NULL` is true for the absent record and false for every
		// record that holds a value — including the one holding a value that
		// does not conform.
		//
		// `IS NULL` replaces the invented `is_absent` and keeps its exemption
		// (FR-022b): it is one of the two operators absence does not make false.
		f := Filter{Property: "status", Op: OpIsNull}
		notNull := Filter{Property: "status", Op: OpIsNotNull}
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
				t.Fatalf("FR-007: `status IS NULL` on %s: want %v, got %v", tc.rec.Path, tc.want, got)
			}
			// R-3: IS NOT NULL is the exact complement, at every state.
			if got := match(t, notNull, tc.rec).Matched; got == tc.want {
				t.Fatalf("R-3: `status IS NOT NULL` on %s: want %v, got %v", tc.rec.Path, !tc.want, got)
			}
		}
	})

	t.Run("FR-007 absent never equals a value", func(t *testing.T) {
		// §8 R-2: a comparison where either side is absent is FALSE for every
		// operator except `IS NULL` and `IS NOT NULL`.
		//
		// `<>` IS IN THIS LIST AND IS NOT AN EXCEPTION (R-2, ruled explicitly in
		// review round 6's C-7). In SQL `x <> 'v'` over a NULL `x` excludes the
		// row, and ruling R-B adopts SQL's semantics along with its names. The
		// capability moves one level up, to `Negate` — asserted below.
		for _, op := range []Operator{OpEqual, OpNotEqual, OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual, OpLike} {
			f := Filter{Property: "status", Op: op, Literal: "done"}
			if op == OpLike {
				f.Literal = "done%"
			}
			if match(t, f, absent).Matched {
				t.Fatalf("§8 R-2: `status %s done` must be false when status is absent", op)
			}
		}
		// And `IN`, whose value is a set.
		if match(t, Filter{Property: "status", Op: OpIn, Literals: []string{"done", "todo"}}, absent).Matched {
			t.Fatalf("§8 R-2: `status IN (done, todo)` must be false when status is absent")
		}
	})

	t.Run("FR-024 an operator undefined for the type is REFUSED, not silently empty", func(t *testing.T) {
		// `LIKE` is a pattern match over text. A date has no pattern form — SQL
		// only reaches one by coercing the date to a string, and this design
		// coerces nothing (R-1). Before the disposition was checked at validate
		// time, such a filter was accepted and then every record returned false
		// with one identical complaint attached: a caller got an empty answer
		// and 5,000 copies of the same problem instead of one refusal naming
		// what would have worked.
		f := Filter{Property: "when", Op: OpLike, Literal: "2026%"}
		_, _, err := f.Validate(sc)
		if err == nil {
			t.Fatal("`when LIKE 2026%` on a date must be refused at validation, not evaluated to an empty result")
		}
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Fatalf("the refusal must be a QueryError so a caller can read it; got %T", err)
		}
		if len(qe.ValidNames) == 0 {
			t.Fatal("the refusal must NAME the operators that would have worked — that is the whole of FR-024")
		}
		// FR-022c: it lists the supported operators, every time.
		if len(qe.Supported) == 0 {
			t.Fatal("FR-022c: the refusal must list the supported operators")
		}
		for _, want := range []string{string(OpEqual), string(OpLess), string(OpIsNull)} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal must list %q as available on a date; got %q", want, err.Error())
			}
		}
	})

	t.Run("FR-008 a negative filter INCLUDES the absent record", func(t *testing.T) {
		// This is the requirement in one assertion. `not(status = done)` must
		// return the record that has no status at all.
		f := Filter{Property: "status", Op: OpEqual, Negate: true, Literal: "done"}

		if !match(t, f, absent).Matched {
			t.Fatalf("FR-008: `not(status = done)` must INCLUDE a record whose status is absent")
		}
		if !match(t, f, nullValued).Matched {
			t.Fatalf("FR-008: an explicitly empty key is absence and must be included too")
		}
		if !match(t, f, todo).Matched {
			t.Fatalf("`not(status = done)` must include a record whose status is todo")
		}
		if match(t, f, done).Matched {
			t.Fatalf("`not(status = done)` must exclude a record whose status IS done")
		}

		// THE DISTINCTION THAT MAKES R-2's C-7 RULING LIVEABLE. The `<>` LEAF
		// does NOT include the absent record — it is a leaf, and R-2 governs it
		// like every other operator. `Negate` is a tree. Both are useful and
		// they are not the same question, which is why the vocabulary keeps
		// them apart instead of overloading one.
		leaf := Filter{Property: "status", Op: OpNotEqual, Literal: "done"}
		if match(t, leaf, absent).Matched {
			t.Fatalf("R-2/C-7: a `<>` LEAF must NOT include a record whose status is absent")
		}
		if !match(t, leaf, todo).Matched {
			t.Fatalf("`status <> done` must match a record whose status is todo")
		}
		if match(t, leaf, done).Matched {
			t.Fatalf("`status <> done` must not match a record whose status IS done")
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
		// `shipped` is not in the declared set, not even case-insensitively.
		// R-4: it is false for every operator AND the record is added to the
		// problem list. Including it in a negative filter would be a silent
		// wrong answer.
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
		for _, name := range []string{"name", "status", "segment", "count", "price"} {
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

	t.Run("FR-011a a literal resolves case-insensitively, and the query still works", func(t *testing.T) {
		// R-5/R-D: resolving `DONE` TO `done` collapses two spellings into one
		// value rather than creating a second. A caller who types the value in a
		// different case gets the records, not a rejection.
		f := Filter{Property: "status", Op: OpEqual, Literal: "DONE"}
		if !match(t, f, done).Matched {
			t.Fatalf("FR-011a: `status = DONE` must match a record whose status is `done`")
		}
	})
}

// TestFilter_ListSemantics covers §8 R-9 and R-13 in SQL's vocabulary.
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

	t.Run("R-9 `=` against a list is EXACT whole-element membership", func(t *testing.T) {
		// This is the question the invented `contains` operator could not
		// answer without a convention nobody had seen. In SQL's vocabulary the
		// caller chooses: `=` is exact, `LIKE` with `%` is partial.
		if !match(t, Filter{Property: "segment", Op: OpEqual, Literal: "vendor"}, both).Matched {
			t.Fatalf("`segment = vendor` must match [vendor, customer]")
		}
		if !match(t, Filter{Property: "segment", Op: OpEqual, Literal: "customer"}, both).Matched {
			t.Fatalf("membership must find any element, not only the first")
		}
		if match(t, Filter{Property: "segment", Op: OpEqual, Literal: "partner"}, both).Matched {
			t.Fatalf("`partner` is not an element of [vendor, customer]")
		}
	})

	t.Run("R-9 `IN` against a list is membership in a SET", func(t *testing.T) {
		if !match(t, Filter{Property: "segment", Op: OpIn, Literals: []string{"partner", "customer"}}, both).Matched {
			t.Fatalf("`segment IN (partner, customer)` must match [vendor, customer]")
		}
		if match(t, Filter{Property: "segment", Op: OpIn, Literals: []string{"partner"}}, both).Matched {
			t.Fatalf("`segment IN (partner)` must not match [vendor, customer]")
		}
		// A single-element array means the same as `=` (FR-022d).
		single := match(t, Filter{Property: "segment", Op: OpIn, Literals: []string{"vendor"}}, both).Matched
		equal := match(t, Filter{Property: "segment", Op: OpEqual, Literal: "vendor"}, both).Matched
		if single != equal {
			t.Fatalf("FR-022d: a single-element `IN` must mean the same as `=`; got %v vs %v", single, equal)
		}
	})

	t.Run("R-13 an ORDERING operator against a list is refused, once, before any record is read", func(t *testing.T) {
		// R-13 as NARROWED by ruling R-B: most of what it used to refuse now has
		// a defined answer. The refusal survives only where the question is
		// genuinely undefined — "is this list greater than `vendor`?" has no
		// answer in any vocabulary.
		//
		// The load-bearing part is the NEGATIVE case. A refused comparison must
		// NOT be re-included by negation: `not(segment > vendor)` answering true
		// would be a confident answer to a question nobody can state.
		for _, f := range []Filter{
			{Property: "segment", Op: OpGreater, Negate: true, Literal: "vendor"},
			{Property: "segment", Op: OpGreater, Literal: "vendor"},
		} {
			res, err := f.Match(sc, both)
			if err == nil {
				t.Fatalf("R-13: `segment > vendor` (negate=%v) must be refused; got Matched=%v with %d comparison problems",
					f.Negate, res.Matched, len(res.ComparisonProblems))
			}
			if res.Matched {
				t.Fatalf("a comparison the oracle refused must not be swept in by negation")
			}
			var qe *QueryError
			if !errors.As(err, &qe) {
				t.Fatalf("expected a *QueryError; got %T: %v", err, err)
			}
			// FR-024's shape: the rejection names the property and the remedy.
			for _, want := range []string{"segment", string(OpEqual), string(OpIn), string(OpLike)} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal must name %q; got %q", want, err.Error())
				}
			}
		}

		// Defence in depth: the ORACLE still refuses the same comparison, for
		// anyone driving the Comparator directly rather than through a query.
		// Deleting the Validate guard must not leave the oracle silent, and
		// deleting the oracle guard must not leave Validate as the only wall.
		segment, ok := sc.Property("segment")
		if !ok {
			t.Fatalf("fixture schema lost its `segment` property")
		}
		left := ResolveProperty(both, segment)
		scalar := *segment
		scalar.Many = false
		right := PropertyValue{Property: &scalar, State: StatePresent,
			Values: []TypedValue{{Type: TypeEnum, Raw: "vendor", Enum: scalar.Values[0]}}}
		got, probs := Comparator{}.Evaluate(OpGreater, left, right)
		if got {
			t.Fatalf("R-13: the oracle must not answer `list > scalar` true")
		}
		if len(probs) != 1 || probs[0].Code != CompareArityNotDefined {
			t.Fatalf("expected one %q from the oracle, got %v", CompareArityNotDefined, problemCodes(probs))
		}
	})

	t.Run("R-3 an empty list is a value, not absence", func(t *testing.T) {
		res := match(t, Filter{Property: "segment", Op: OpIsNull}, empty)
		if res.Matched {
			t.Fatalf("§8 R-3: an empty list is a VALUE, so `IS NULL` must be false")
		}
		if res.State != StatePresent {
			t.Fatalf("expected %v, got %v", StatePresent, res.State)
		}
		if !match(t, Filter{Property: "segment", Op: OpIsNull}, none).Matched {
			t.Fatalf("a missing key IS absent")
		}
		// And an empty list matches no element-wise predicate: it contains
		// nothing (R-9), which is a different fact from being absent.
		if match(t, Filter{Property: "segment", Op: OpEqual, Literal: "vendor"}, empty).Matched {
			t.Fatalf("R-9: an empty list contains nothing")
		}
	})
}

// TestCompare_ThroughTheRealFilterPath covers §8 R-1, R-11 and AC-8.3 — and it
// drives them through Filter.Match, the path real queries take, not through a
// comparator reachable only from a test.
func TestCompare_ThroughTheRealFilterPath(t *testing.T) {
	_, sc := filterSchema(t)
	three := ParseRecord("three.md", []byte("---\ntype: widget\nname: n\ncount: 3\nprice: 2.50\n---\n"))

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
		// R-1's other worked example: an integer literal written with a decimal
		// point still compares numerically, because integer and decimal are ONE
		// declared type for comparison. (The literal is parsed against the
		// INTEGER property, so this also asserts the parser accepts it.)
		if !match(t, Filter{Property: "price", Op: OpEqual, Literal: "2.500"}, three).Matched {
			t.Fatalf("R-1/FR-013: `price = 2.500` must match a price of 2.50 — scale is storage, not identity")
		}
	})

	t.Run("R-1 different declared types do not compare", func(t *testing.T) {
		// A single Filter cannot express a cross-type comparison — both sides
		// come from one declared property, which is FR-009 doing its job. So
		// this drives the oracle directly, with differently-typed operands.
		//
		// THE PAIRS ARE CHOSEN SO THAT DELETING R-1 GIVES A CONFIDENT WRONG
		// ANSWER rather than an incidental refusal. That mattered before and it
		// matters more now: ruling R-D gave TEXT a lexical ordering, so a
		// text-vs-number pair no longer fails for the accidental reason that
		// text had no order. Each pair below dispatches on the LEFT operand's
		// domain and then reads a ZERO-VALUED field off the right one — an empty
		// string, a zero instant — and answers with total confidence.
		count, _ := sc.Property("count")
		name, _ := sc.Property("name")
		when, _ := sc.Property("when")
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
			{"text vs integer", parse(name, "3"), parse(count, "2")},
			{"integer vs text", parse(count, "2"), parse(name, "3")},
			{"text vs date", parse(name, "zzz"), parse(when, "2026-01-01")},
			{"date vs text", parse(when, "2026-01-01"), parse(name, "zzz")},
			{"date vs integer", parse(when, "2026-01-01"), parse(count, "2")},
			{"enum vs integer", parse(status, "doing"), parse(count, "2")},
			{"integer vs enum", parse(count, "2"), parse(status, "doing")},
		}
		for _, pair := range pairs {
			if cmp, ok := Compare(pair.a, pair.b); ok {
				t.Fatalf("§8 R-1: %s must not compare; got (%d, true)", pair.name, cmp)
			}
		}

		// ...and the TWO pairs R-1 unifies MUST compare, or the rule has been
		// applied as a blanket type check instead of as the rule it is.
		price, _ := sc.Property("price")
		if cmp, ok := Compare(parse(count, "3"), parse(price, "3.0")); !ok || cmp != 0 {
			t.Fatalf("R-1: integer 3 and decimal 3.0 are ONE declared type; got (%d, %v)", cmp, ok)
		}
		if cmp, ok := Compare(parse(name, "done"), parse(status, "done")); !ok || cmp != 0 {
			t.Fatalf("R-1: text `done` and enum `done` are ONE declared type; got (%d, %v)", cmp, ok)
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
