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
// — and that `!prop.Many` meant a MANY property skipped the check entirely. An
// ordering operator against a `many text` property validated clean, and then
// every record produced one identical CompareArityNotDefined problem: 5,000
// copies of the same complaint and an empty result set, which is precisely the
// defect FR-024 exists to end.
//
// The per-record refusal is still there and still tested (compare_r13_test.go)
// — defence in depth for anyone driving the Comparator directly. It is no
// longer the primary surface.
//
// R-13's REFUSAL SET SHRANK under ruling R-B: only the four ORDERING operators
// are refused against a list now. Everything else is defined element-wise, and
// the second test below is what stops the fix being "refuse everything against a
// list", which would pass every assertion in the first while making list queries
// impossible.
// ---------------------------------------------------------------------------

const r13ValidateFixture = `
schema_version: 1
type: widget
properties:
  name:    { type: text }
  notes:   { type: text, many: true }
  status:  { type: enum, values: [todo, doing, done] }
  segment: { type: enum, values: [vendor, customer, partner], many: true }
  count:   { type: integer }
  sizes:   { type: integer, many: true }
  amounts: { type: decimal, many: true }
  when:    { type: date, many: true }
  owners:  { type: person, many: true }
  linked:  { type: relation, to: widget, many: true }
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

// manyProperties is every `many` property in the fixture, covering every
// declared type that can hold one, so the refusal is proven for the whole type
// axis rather than for text alone.
var manyProperties = map[string]PropertyType{
	"notes":   TypeText,
	"segment": TypeEnum,
	"sizes":   TypeInteger,
	"amounts": TypeDecimal,
	"when":    TypeDate,
	"owners":  TypePerson,
	"linked":  TypeRelation,
}

// literalFor is a value that PARSES for the property's declared type, so the
// filter reaches the arity check rather than being rejected by ParseValue for
// an unrelated reason. If the arity refusal were removed, these filters would
// validate clean — which is exactly what the test must be able to detect.
var literalFor = map[PropertyType]string{
	TypeText:     "anything",
	TypeEnum:     "vendor",
	TypeInteger:  "3",
	TypeDecimal:  "3.5",
	TypeDate:     "2026-01-01",
	TypePerson:   "[[Ada Lovelace]]",
	TypeRelation: "[[Acme Ltd]]",
}

// TestFilter_R13_OrderingRefusedOnceAtValidateTime is the regression for the
// finding above: the refusal must happen ONCE, at validate time, before any
// record is touched — not once per record.
func TestFilter_R13_OrderingRefusedOnceAtValidateTime(t *testing.T) {
	sc := r13ValidateSchema(t)

	// R-13 as narrowed: only the four ORDERING operators are refused against a
	// `many` property. "Is this list greater than `vendor`?" has no answer in
	// any vocabulary; everything else does.
	refused := []Operator{OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual}

	for name, typ := range manyProperties {
		for _, op := range refused {
			for _, negate := range []bool{false, true} {
				t.Run(name+"/"+string(op), func(t *testing.T) {
					f := Filter{Property: name, Op: op, Negate: negate, Literal: literalFor[typ]}

					prop, lits, err := f.Validate(sc)
					if err == nil {
						t.Fatalf("R-13 VIOLATED: `%s %s` against a `many %s` property validated clean "+
							"(prop=%v literals=%d). It must be refused ONCE here, not reported once per record.",
							name, op, typ, prop != nil, len(lits))
					}

					var qe *QueryError
					if !errors.As(err, &qe) {
						t.Fatalf("expected a *QueryError so the caller can read the valid names; got %T: %v", err, err)
					}
					msg := err.Error()
					// FR-024's shape: name the property, the operator, and the
					// remedy — all three, because two of them leave the caller
					// with a complaint and no fix.
					if !strings.Contains(msg, name) {
						t.Fatalf("the refusal must NAME the property so the caller can find it.\ngot: %s", msg)
					}
					if !strings.Contains(msg, string(op)) {
						t.Fatalf("the refusal must name the operator that was rejected.\ngot: %s", msg)
					}
					// The remedy list is the type's OWN element-wise
					// disposition, not R-13's three examples. Against a `many
					// text` property that is `=`, `<>`, `LIKE`, `IN`; against a
					// `many date` it is the same minus `LIKE`, because a date
					// has no pattern form and naming `LIKE` would send the
					// caller to an operator that refuses them a second time.
					//
					// Deriving it here rather than hardcoding it is what makes
					// this assertion hold for BOTH refusal paths: relation and
					// person have no ordering at all, so their refusal comes
					// from the TYPE disposition one step earlier and lists the
					// same set.
					for _, remedy := range elementWiseOperatorNames(typ) {
						if !strings.Contains(msg, remedy) {
							t.Fatalf("the refusal must name the REMEDY %q — every operator that WOULD have worked.\ngot: %s",
								remedy, msg)
						}
					}
					if strings.Contains(msg, string(OpLike)) && !operatorDefinedForType[typ][OpLike] {
						t.Fatalf("the refusal names `LIKE` as a remedy for a %s, which does not define it; a remedy that does not work is worse than none.\ngot: %s",
							typ, msg)
					}
				})
			}
		}
	}
}

// TestFilter_R13_ElementWiseOperatorsStillValidate is the converse, and it is
// the half that stops the fix from being "refuse everything against a list".
func TestFilter_R13_ElementWiseOperatorsStillValidate(t *testing.T) {
	sc := r13ValidateSchema(t)

	for name, typ := range manyProperties {
		lit := literalFor[typ]
		t.Run(name+"/=", func(t *testing.T) {
			f := Filter{Property: name, Op: OpEqual, Literal: lit}
			prop, lits, err := f.Validate(sc)
			if err != nil {
				t.Fatalf("R-9/R-13: `=` IS defined against a `many %s` property; got %v", typ, err)
			}
			if prop == nil || !prop.Many {
				t.Fatalf("validate must return the declared `many` property; got %+v", prop)
			}
			if len(lits) != 1 {
				t.Fatalf("validate must return the one parsed literal; got %d", len(lits))
			}
		})
		t.Run(name+"/<>", func(t *testing.T) {
			if _, _, err := (Filter{Property: name, Op: OpNotEqual, Literal: lit}).Validate(sc); err != nil {
				t.Fatalf("R-13: `<>` IS defined against a `many %s` property; got %v", typ, err)
			}
		})
		t.Run(name+"/IN", func(t *testing.T) {
			if _, _, err := (Filter{Property: name, Op: OpIn, Literals: []string{lit}}).Validate(sc); err != nil {
				t.Fatalf("R-13: `IN` IS defined against a `many %s` property; got %v", typ, err)
			}
		})
		t.Run(name+"/IS NULL", func(t *testing.T) {
			if _, _, err := (Filter{Property: name, Op: OpIsNull}).Validate(sc); err != nil {
				t.Fatalf("R-3/R-13: `IS NULL` IS defined against a `many %s` property; got %v", typ, err)
			}
			if _, _, err := (Filter{Property: name, Op: OpIsNotNull}).Validate(sc); err != nil {
				t.Fatalf("R-3/R-13: `IS NOT NULL` IS defined against a `many %s` property; got %v", typ, err)
			}
		})
		// `LIKE` is defined against a list only where the TYPE defines it —
		// text and enum. That is the type disposition, not the arity rule, and
		// the two must not be confused: a `many date` property refuses `LIKE`
		// because a date has no pattern form, not because it is a list.
		t.Run(name+"/LIKE", func(t *testing.T) {
			_, _, err := (Filter{Property: name, Op: OpLike, Literal: "any%"}).Validate(sc)
			defined := operatorDefinedForType[typ][OpLike]
			if defined && err != nil {
				t.Fatalf("R-9: `LIKE` IS defined against a `many %s` property; got %v", typ, err)
			}
			if !defined {
				if err == nil {
					t.Fatalf("`LIKE` is not defined for a %s at any arity; it must be refused", typ)
				}
				if !strings.Contains(err.Error(), string(typ)) {
					t.Fatalf("the refusal must name the TYPE, not the arity — the caller's fix is different.\ngot: %s", err)
				}
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
		{"status", OpGreater, "todo"}, // R-5/R-E — enum orders lexically
		{"count", OpGreater, "3"},     // AC-8.3
		{"name", OpLike, "Acme%"},     // R-10
		{"name", OpEqual, "Acme"},     // R-10 — `=` on text IS defined now
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
		{Property: "segment", Op: OpGreater, Literal: "vendor"},
		{Property: "segment", Op: OpGreater, Negate: true, Literal: "vendor"},
		{Property: "segment", Op: OpLessOrEqual, Literal: "vendor"},
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

	// And the operators R-13 now DEFINES reach the record and answer.
	for _, tc := range []struct {
		f    Filter
		want bool
	}{
		{Filter{Property: "segment", Op: OpEqual, Literal: "vendor"}, true},
		{Filter{Property: "segment", Op: OpEqual, Literal: "partner"}, false},
		{Filter{Property: "segment", Op: OpIn, Literals: []string{"partner", "customer"}}, true},
		{Filter{Property: "segment", Op: OpNotEqual, Literal: "vendor"}, true}, // customer differs
	} {
		res, err := tc.f.Match(sc, rec)
		if err != nil {
			t.Fatalf("`segment %s` must be accepted under the narrowed R-13; got %v", tc.f.Op, err)
		}
		if res.Matched != tc.want {
			t.Errorf("`segment %s %v` = %v, want %v", tc.f.Op, tc.f.Literal+strings.Join(tc.f.Literals, ","), res.Matched, tc.want)
		}
	}
}
