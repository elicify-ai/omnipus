// Omnipus — spec §8 R-13 at VALIDATE time, and FR-024's "reject, don't answer
// with nothing".
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE GUARDS, AND WHY IT IS SEPARATE FROM compare_r13_test.go
//
// R-13 has two enforcement points and they answer two different questions:
//
//	compare_r13_test.go     — does the ORACLE refuse, per comparison?
//	this file               — does the QUERY refuse, ONCE, before any record is
//	                          read?
//
// The second is the one a caller experiences. `Filter.Validate` used to guard
// the operator/type disposition with `if !prop.Many && !operatorDefinedForType…`
// — and that `!prop.Many` meant a MANY property skipped the check entirely.
// `gt` against a `many text` property validated clean, and then every record
// produced one identical CompareArityNotDefined problem: 5,000 copies of the
// same complaint and an empty result set, which is precisely the defect FR-024
// exists to end.
//
// The comment sitting above that guard claimed "R-13 owns the arity dimension;
// this owns the type dimension. Both refuse up front rather than per record."
// The first half of that sentence was true, the second was false, and a comment
// asserting a guarantee nothing implements is worse than no comment at all.
//
// The per-record refusal is still there and still tested (compare_r13_test.go)
// — defence in depth for anyone driving the Comparator directly. It is no
// longer the primary surface.
// ---------------------------------------------------------------------------

const r13ValidateFixture = `
schema_version: 1
type: widget
properties:
  name:    { type: text }
  notes:   { type: text, many: true }
  status:  { type: enum, values: [todo, doing, done] }
  segment: { type: enum, values: [vendor, customer, partner], many: true }
  count:   { type: number }
  sizes:   { type: number, many: true }
  when:    { type: date, many: true }
  owners:  { type: person, many: true }
  linked:  { type: relation, to: widget, many: true }
  budgets: { type: money, many: true }
`

func r13ValidateSchema(t *testing.T) *Schema {
	t.Helper()
	set := loadSet(t, map[string]string{"widget.yaml": r13ValidateFixture})
	sc, ok := set.Get("widget")
	if !ok {
		t.Fatalf("fixture schema did not load")
	}
	return sc
}

// manyProperties is every `many` property in the fixture, one per declared
// type, so the refusal is proven for the whole type axis rather than for text
// alone.
var manyProperties = map[string]PropertyType{
	"notes":   TypeText,
	"segment": TypeEnum,
	"sizes":   TypeNumber,
	"when":    TypeDate,
	"owners":  TypePerson,
	"linked":  TypeRelation,
	"budgets": TypeMoney,
}

// literalFor is a value that PARSES for the property's declared type, so the
// filter reaches the arity check rather than being rejected by ParseValue for
// an unrelated reason. If the arity refusal were removed, these filters would
// validate clean — which is exactly what the test must be able to detect.
var literalFor = map[PropertyType]string{
	TypeText:     "anything",
	TypeEnum:     "vendor",
	TypeNumber:   "3",
	TypeDate:     "2026-01-01",
	TypePerson:   "[[Ada Lovelace]]",
	TypeRelation: "[[Acme Ltd]]",
	TypeMoney:    "10.00 USD",
}

// TestFilter_R13_RefusedOnceAtValidateTime is the regression for the finding
// above: the refusal must happen ONCE, at validate time, before any record is
// touched — not once per record.
func TestFilter_R13_RefusedOnceAtValidateTime(t *testing.T) {
	sc := r13ValidateSchema(t)

	// R-13: against a `many` property only `contains` and `is absent` are
	// defined. Everything else is refused. `is absent` is exempt because R-3
	// makes it the one operator absence does not answer false, and it asks
	// about the PROPERTY, not about any element.
	refused := []Operator{OpEqual, OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual}

	for name, typ := range manyProperties {
		for _, op := range refused {
			for _, negate := range []bool{false, true} {
				t.Run(name+"/"+string(op), func(t *testing.T) {
					f := Filter{Property: name, Op: op, Negate: negate, Literal: literalFor[typ]}

					prop, lit, err := f.Validate(sc)
					if err == nil {
						t.Fatalf("R-13 VIOLATED: `%s %s` against a `many %s` property validated clean "+
							"(prop=%v literal=%v). It must be refused ONCE here, not reported once per record.",
							name, op, typ, prop != nil, lit != nil)
					}

					var qe *QueryError
					if !errors.As(err, &qe) {
						t.Fatalf("expected a *QueryError so the caller can read the valid names; got %T: %v", err, err)
					}
					msg := err.Error()
					// FR-024's shape: name the property and name the remedy.
					if !strings.Contains(msg, name) {
						t.Fatalf("the refusal must NAME the property so the caller can find it.\ngot: %s", msg)
					}
					if !strings.Contains(msg, string(OpContains)) {
						t.Fatalf("the refusal must name the REMEDY (`contains`) — that is what R-13 exists for.\ngot: %s", msg)
					}
					if !strings.Contains(msg, string(OpIsAbsent)) {
						t.Fatalf("`is absent` is the OTHER operator R-13 defines against a list; the refusal must list it too.\ngot: %s", msg)
					}
					if !strings.Contains(msg, string(op)) {
						t.Fatalf("the refusal must name the operator that was rejected.\ngot: %s", msg)
					}
				})
			}
		}
	}
}

// TestFilter_R13_ContainsAndIsAbsentStillValidate is the converse, and it is
// the half that stops the fix from being "refuse everything against a list".
// A refusal that also refuses the two operators R-13 DEFINES would pass every
// assertion above while making list queries impossible.
func TestFilter_R13_ContainsAndIsAbsentStillValidate(t *testing.T) {
	sc := r13ValidateSchema(t)

	for name, typ := range manyProperties {
		t.Run(name+"/contains", func(t *testing.T) {
			f := Filter{Property: name, Op: OpContains, Literal: literalFor[typ]}
			prop, lit, err := f.Validate(sc)
			if err != nil {
				t.Fatalf("R-9/R-13: `contains` IS defined against a `many %s` property; got %v", typ, err)
			}
			if prop == nil || !prop.Many {
				t.Fatalf("validate must return the declared `many` property; got %+v", prop)
			}
			if lit == nil {
				t.Fatalf("validate must return the parsed literal for `contains`")
			}
		})
		t.Run(name+"/is_absent", func(t *testing.T) {
			f := Filter{Property: name, Op: OpIsAbsent}
			if _, _, err := f.Validate(sc); err != nil {
				t.Fatalf("R-3/R-13: `is absent` IS defined against a `many %s` property; got %v", typ, err)
			}
		})
	}

	// And the scalar path is untouched: an operator the type DOES define still
	// validates, so the arity branch cannot have swallowed the type branch.
	for _, tc := range []struct {
		prop string
		op   Operator
		lit  string
	}{
		{"status", OpGreater, "todo"}, // R-5 — enum orders by declared position
		{"count", OpGreater, "3"},     // AC-8.3
		{"name", OpContains, "Acme"},  // R-10
	} {
		if _, _, err := (Filter{Property: tc.prop, Op: tc.op, Literal: tc.lit}).Validate(sc); err != nil {
			t.Fatalf("the scalar disposition regressed: `%s %s` was refused: %v", tc.prop, tc.op, err)
		}
	}
}

// TestFilter_R13_RefusalIsNotPerRecord is the measurement the finding rests on.
//
// Before the fix `Match` returned err=nil and a MatchResult carrying one
// CompareArityNotDefined per record. A vault with 5,000 widgets produced 5,000
// identical problems and an empty answer. After the fix `Match` returns the
// QueryError immediately and never reads the record at all.
func TestFilter_R13_RefusalIsNotPerRecord(t *testing.T) {
	sc := r13ValidateSchema(t)
	rec := ParseRecord("w.md", []byte("---\ntype: widget\nsegment: [vendor, customer]\n---\n"))

	for _, f := range []Filter{
		{Property: "segment", Op: OpEqual, Literal: "vendor"},
		{Property: "segment", Op: OpEqual, Negate: true, Literal: "vendor"},
		{Property: "segment", Op: OpGreater, Literal: "vendor"},
	} {
		res, err := f.Match(sc, rec)
		if err == nil {
			t.Fatalf("`segment %s` (negate=%v) must be refused before the record is read; "+
				"instead it answered Matched=%v with %d comparison problems — one per record, which is the defect",
				f.Op, f.Negate, res.Matched, len(res.ComparisonProblems))
		}
		if len(res.ComparisonProblems) != 0 || len(res.Problems) != 0 {
			t.Fatalf("a query refused up front must not also emit per-record problems; got %d/%d",
				len(res.ComparisonProblems), len(res.Problems))
		}
		if res.Matched {
			t.Fatalf("a refused query must never report a match")
		}
	}
}
