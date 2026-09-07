// Omnipus — FR-143a / R-18: a formula has ONE static type and arity, and the
// comparator enforces the declaration.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// TestFormula_DeclaredTypeIsStaticAndTheComparatorEnforcesIt is the comparator
// half of FR-143a — the half this package owns.
//
// The expression language owns type INFERENCE, the `if()`-branch agreement
// check and FR-148's cycles. What has to be true HERE is the thing those checks
// are worth nothing without: that a declaration the values do not honour is
// caught rather than silently miscompared.
//
// THE HOLE, EXECUTED. R-1 decides a comparison from the two PROPERTIES'
// declared types and nothing else; compareElements then reads whichever FIELD
// of TypedValue that declared type names. Put a `text` value under a `decimal`
// declaration and every step passes: R-1 sees decimal vs decimal, the
// disposition defines `>` for decimal, and compareElements reaches
// a.Number.Cmp(b.Number) where Number is the ZERO Decimal. `"x" > 5` answered
// FALSE and `"x" < 5` answered TRUE, reporting nothing.
//
// That is exactly what an undeclared formula type produces — FR-143a's "a
// silently wrong answer wearing a type system".
func TestFormula_DeclaredTypeIsStaticAndTheComparatorEnforcesIt(t *testing.T) {
	numeric, err := NewProperty(Property{Name: "amount", Type: TypeDecimal, RecordType: "deal"})
	if err != nil {
		t.Fatalf("NewProperty: %v", err)
	}

	t.Run("a value whose type disagrees with the declaration is non-conforming AND reported", func(t *testing.T) {
		// The operand a per-record formula type would produce: declared
		// `decimal`, carrying `text`.
		mismatched := PropertyValue{
			Property: numeric,
			State:    StatePresent,
			Values:   []TypedValue{{Type: TypeText, Raw: "x", Text: "x"}},
		}
		five := present(numeric, TypedValue{Type: TypeDecimal, Raw: "5", Number: mustDec(t, "5")})
		c := Comparator{}

		// BOTH DIRECTIONS. The asymmetry is the whole tell: under the old
		// behaviour `>` was false and `<` was TRUE, so a filter for "under 5"
		// silently included every record whose formula produced text.
		for _, op := range []Operator{OpGreater, OpLess, OpGreaterOrEqual, OpLessOrEqual, OpEqual, OpNotEqual} {
			got, problems := c.Evaluate(op, mismatched, five)
			if got {
				t.Errorf("%s: a value that does not honour its declaration must be FALSE, not answered", op)
			}
			if len(problems) == 0 {
				t.Fatalf("%s: R-4 requires the non-conformance to be REPORTED; silence is the defect", op)
			}
			if problems[0].Code != CompareNonConforming {
				t.Errorf("%s: code = %q, want %q", op, problems[0].Code, CompareNonConforming)
			}
		}

		// And specifically: `<` must NOT come back true, which is the answer
		// the zero-Decimal path produced.
		if got, _ := c.Evaluate(OpLess, mismatched, five); got {
			t.Error(`the regression: a text value under a decimal declaration must never answer TRUE for "< 5"`)
		}
	})

	t.Run("the guard is on the DOMAIN, so integer under decimal stays correct", func(t *testing.T) {
		// R-1 makes `integer` and `decimal` ONE declared type for comparison,
		// so an integer value under a decimal declaration is legitimate and
		// `3 = 3.0` must stay TRUE. A guard written on the TYPE rather than the
		// domain would break this.
		v := PropertyValue{
			Property: numeric,
			State:    StatePresent,
			Values:   []TypedValue{{Type: TypeInteger, Raw: "3", Number: mustDec(t, "3")}},
		}
		three := present(numeric, TypedValue{Type: TypeDecimal, Raw: "3.0", Number: mustDec(t, "3.0")})
		got, problems := (Comparator{}).Evaluate(OpEqual, v, three)
		if len(problems) != 0 {
			t.Fatalf("an integer value under a decimal declaration is legitimate (R-1); got problems %v", problems)
		}
		if !got {
			t.Error("R-1: `3 = 3.0` must be TRUE")
		}
		// Same for text under an enum declaration, R-1's other unified pair.
		enumProp, err := NewProperty(Property{
			Name: "stage", Type: TypeEnum, RecordType: "deal",
			Values: []EnumValue{{Name: "won"}},
		})
		if err != nil {
			t.Fatalf("NewProperty(enum): %v", err)
		}
		if !ValueConformsToDeclaration(enumProp, TypedValue{Type: TypeText, Text: "won"}) {
			t.Error("R-1: `text` and `enum` are ONE comparison domain; a text value under an enum declaration must conform")
		}
	})

	t.Run("ValueConformsToDeclaration is stated over every declared type", func(t *testing.T) {
		for _, declared := range PropertyTypes {
			for _, carried := range PropertyTypes {
				p := &Property{Name: "p", Type: declared}
				want := comparisonDomain(declared) == comparisonDomain(carried)
				if got := ValueConformsToDeclaration(p, TypedValue{Type: carried}); got != want {
					t.Errorf("declared %s carrying %s: got %v, want %v", declared, carried, got, want)
				}
			}
		}
		if ValueConformsToDeclaration(nil, TypedValue{Type: TypeText}) {
			t.Error("a nil declaration conforms to nothing")
		}
	})
}

// TestFormula_ADeclarationCarriesOneTypeAndOneArity covers the carrier itself —
// NewFormulaProperty and the rules finalize() applies to a derived property.
func TestFormula_ADeclarationCarriesOneTypeAndOneArity(t *testing.T) {
	t.Run("a well-formed formula declaration is accepted and is static", func(t *testing.T) {
		p, err := NewFormulaProperty(Property{
			Name: "age", Type: TypeInteger, Formula: "(today() - created).days", RecordType: "deal",
		})
		if err != nil {
			t.Fatalf("NewFormulaProperty: %v", err)
		}
		if p.Type != TypeInteger || p.Many {
			t.Fatalf("the declaration must carry ONE type and ONE arity; got type=%s many=%v", p.Type, p.Many)
		}
		if p.Formula != "(today() - created).days" {
			t.Errorf("the expression must survive on the declaration; got %q", p.Formula)
		}
		// The comparator must not be able to tell a derived operand from a
		// stored one — that is what makes R-18 hold by construction.
		stored, err := NewProperty(Property{Name: "age", Type: TypeInteger, RecordType: "deal"})
		if err != nil {
			t.Fatalf("NewProperty: %v", err)
		}
		if comparisonDomain(p.Type) != comparisonDomain(stored.Type) || p.Many != stored.Many {
			t.Error("a derived declaration and a stored one of the same type must be indistinguishable to the comparator")
		}
	})

	t.Run("an expression is mandatory", func(t *testing.T) {
		for _, empty := range []string{"", "   "} {
			if _, err := NewFormulaProperty(Property{Name: "age", Type: TypeInteger, Formula: empty}); err == nil {
				t.Fatalf("a formula property with expression %q must be refused — it is a declared type with nothing behind it", empty)
			}
		}
	})

	t.Run("a formula cannot be required", func(t *testing.T) {
		_, err := NewFormulaProperty(Property{Name: "age", Type: TypeInteger, Formula: "1", Required: true})
		if err == nil {
			t.Fatal("a formula property must not be `required` — nothing writes a derived value into a note")
		}
		if !strings.Contains(err.Error(), "required") {
			t.Errorf("the refusal must name the offending key; got %q", err)
		}
	})

	t.Run("a formula cannot declare a relation, a target or an inverse", func(t *testing.T) {
		// R-16: a derived link is a PRESENTATION value and does not compare, so
		// declaring a target would promise FR-034 target checking and D5's
		// derived inverse over something that is neither.
		for _, decl := range []Property{
			{Name: "c", Type: TypeRelation, Formula: "asLink(company)", To: "company"},
			{Name: "c", Type: TypePerson, Formula: "asLink(owner)"},
			{Name: "c", Type: TypeText, Formula: "asLink(company)", To: "company"},
			{Name: "c", Type: TypeText, Formula: "asLink(company)", Inverse: "deals"},
		} {
			if _, err := NewFormulaProperty(decl); err == nil {
				t.Errorf("a formula declaring type=%s to=%q inverse=%q must be refused (R-16)", decl.Type, decl.To, decl.Inverse)
			}
		}
	})

	t.Run("a formula is held to the SAME declaration rules as a stored property", func(t *testing.T) {
		// The point of routing through NewProperty: a laxer second constructor
		// would reintroduce exactly the asymmetry R-18 removes.
		if _, err := NewFormulaProperty(Property{Name: "s", Type: TypeEnum, Formula: "x"}); err == nil {
			t.Error("an enum formula with no `values` must be refused, exactly as a stored enum is")
		}
		if _, err := NewFormulaProperty(Property{Name: "s", Type: TypeText, Formula: "x", Unit: "days"}); err == nil {
			t.Error("`unit` on a text formula must be refused, exactly as on a stored text property")
		}
		if _, err := NewFormulaProperty(Property{Name: "s", Type: "duration", Formula: "x"}); err == nil {
			t.Error("an unknown type must be refused for a formula too")
		}
	})
}

func mustDec(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, err)
	}
	return d
}
