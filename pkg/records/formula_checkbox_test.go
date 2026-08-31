// Omnipus — FR-004c × FR-142/FR-143a: a boolean crosses the formula seam in
// TypedValue.Bool, which is the field every consumer of a checkbox reads.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// TypedValue is a tagged union: `type` names which field carries the value, and
// a checkbox's field is `Bool`. value.go::parseCheckboxValue writes `Bool`,
// compare_oracle.go's TypeCheckbox branch reads `Bool`, and
// knowledgefind/summaries.go's `checked`/`unchecked` aggregates read `Bool`.
// There is exactly one right field and it is not in dispute.
//
// The formula layer used a DIFFERENT one at both of its two boundaries, in
// opposite directions:
//
//	IN  — fvalFromPropertyValue's FormulaBoolean case read `v.Text`, which a
//	      real checkbox property never populates, so every checkbox a formula
//	      read was `false`.
//	OUT — fval.materialize's FormulaBoolean case wrote `Text` and left `Bool`
//	      at its zero value, so every boolean a formula PRODUCED compared as
//	      `false` no matter what it evaluated to.
//
// Both are silent: a boolean that is always false is a filter that selects the
// exact complement of what it says, with no problem entry and no refusal. The
// assertions below read `Bool` because `Bool` is what the comparator reads —
// asserting on `Raw` or `Text` would pass over both defects.
// ---------------------------------------------------------------------------

// checkboxFormulaSchema declares the two properties the cases below need: a
// checkbox to read, and a decimal so the `if()` case has a non-boolean operand
// to build a condition from.
func checkboxFormulaSchema(t *testing.T) *Schema {
	t.Helper()
	props := map[string]*Property{
		"meditated": checkboxProp(t, "meditated", false),
		"amount":    testProperty("amount", TypeDecimal, false),
	}
	return &Schema{
		Type:          "day",
		Properties:    props,
		PropertyOrder: []string{"amount", "meditated"},
	}
}

// checkboxPropertyValue builds the operand the way a NOTE would: the text is
// run through ParseValue, so the fixture cannot populate a field the real
// parser leaves empty.
func checkboxPropertyValue(t *testing.T, p *Property, written string) PropertyValue {
	t.Helper()
	v, verr := ParseValue(p, Node{Kind: KindScalar, Text: written})
	if verr != nil {
		t.Fatalf("fixture: %q must parse as a checkbox: %v", written, verr)
	}
	return PropertyValue{Property: p, State: StatePresent, Values: []TypedValue{v}}
}

// evalCheckboxFormula validates one formula against the checkbox schema and
// evaluates it over one candidate.
func evalCheckboxFormula(t *testing.T, src string, c FormulaCandidate) FormulaResult {
	t.Helper()
	set, errs := ValidateFormulaSet(map[string]string{"f": src}, checkboxFormulaSchema(t))
	if len(errs) != 0 {
		t.Fatalf("%q should validate; got: %v", src, formulaErrorMessages(errs))
	}
	e := NewFormulaEvaluator(set, testComparator(), formulaTestNow())
	e.Begin(c)
	res, ok := e.Evaluate("f")
	if !ok {
		t.Fatalf("%q did not evaluate", src)
	}
	return res
}

// TestFormula_BooleanCrossesTheSeamInBool pins both directions of the seam.
func TestFormula_BooleanCrossesTheSeamInBool(t *testing.T) {
	schema := checkboxFormulaSchema(t)
	meditated := schema.Properties["meditated"]

	t.Run("READ — a checkbox property reaches the formula with its real value", func(t *testing.T) {
		for _, written := range []string{"true", "TRUE", "false", "False"} {
			want := written == "true" || written == "TRUE"
			c := fixtureCandidate{props: map[string]PropertyValue{
				"meditated": checkboxPropertyValue(t, meditated, written),
			}}
			// `if(meditated, 1, 0)` is the discriminating shape: the formula
			// CONSUMES the checkbox as a condition, so a read that always
			// yields false takes the else-branch for `meditated: true`.
			res := evalCheckboxFormula(t, "if(meditated, 1, 0)", c)
			if res.Absent {
				t.Fatalf("meditated: %s — the formula must produce a value", written)
			}
			wantNum := map[bool]string{true: "1", false: "0"}[want]
			if got := renderNumber(t, res); got != wantNum {
				t.Errorf("meditated: %s — if(meditated, 1, 0) = %s, want %s. A checkbox read as always-false selects the exact complement of what the formula says",
					written, got, wantNum)
			}
		}
	})

	t.Run("WRITE — a boolean formula's value lands in Bool, the field the comparator reads", func(t *testing.T) {
		for _, tc := range []struct {
			src  string
			want bool
		}{
			{"meditated", true},
			{"!meditated", false},
			{"amount > 1", true},
			{"amount < 1", false},
		} {
			c := fixtureCandidate{props: map[string]PropertyValue{
				"meditated": checkboxPropertyValue(t, meditated, "true"),
				"amount":    numberValue(t, schema.Properties["amount"], "42"),
			}}
			res := evalCheckboxFormula(t, tc.src, c)
			if res.Type != FormulaBoolean {
				t.Fatalf("%q: static type = %v, want boolean", tc.src, res.Type)
			}
			vals := res.Values()
			if len(vals) != 1 {
				t.Fatalf("%q produced %d values, want 1", tc.src, len(vals))
			}
			if vals[0].Type != TypeCheckbox {
				t.Errorf("%q: materialised as %q, want %q", tc.src, vals[0].Type, TypeCheckbox)
			}
			if vals[0].Bool != tc.want {
				t.Errorf("%q: Bool = %v, want %v. TypedValue.Bool is the checkbox field — compare_oracle's TypeCheckbox branch and the checked/unchecked aggregates both read it and nothing else",
					tc.src, vals[0].Bool, tc.want)
			}
			// Raw stays the canonical spelling so a report still renders it.
			if want := map[bool]string{true: "true", false: "false"}[tc.want]; vals[0].Raw != want {
				t.Errorf("%q: Raw = %q, want %q", tc.src, vals[0].Raw, want)
			}
		}
	})

	t.Run("ROUND TRIP — the comparator answers a boolean formula correctly", func(t *testing.T) {
		// This is the failure the two halves add up to, expressed the way a
		// user meets it: a filter on a boolean formula.
		for _, tc := range []struct {
			src     string
			literal string
			want    bool
		}{
			{"meditated", "true", true},
			{"meditated", "false", false},
			{"!meditated", "true", false},
			{"!meditated", "false", true},
		} {
			c := fixtureCandidate{props: map[string]PropertyValue{
				"meditated": checkboxPropertyValue(t, meditated, "true"),
			}}
			res := evalCheckboxFormula(t, tc.src, c)
			left, ok := res.PropertyValue(tc.src)
			if !ok {
				t.Fatalf("%q: a boolean result must hand back a PropertyValue", tc.src)
			}
			lit, verr := ParseValue(meditated, Node{Kind: KindScalar, Text: tc.literal})
			if verr != nil {
				t.Fatalf("fixture literal %q: %v", tc.literal, verr)
			}
			right := PropertyValue{Property: meditated, State: StatePresent, Values: []TypedValue{lit}}
			got, problems := testComparator().Evaluate(OpEqual, left, right)
			if len(problems) != 0 {
				t.Fatalf("%q = %s reported problems: %v", tc.src, tc.literal, problems)
			}
			if got != tc.want {
				t.Errorf("%q = %s answered %v, want %v", tc.src, tc.literal, got, tc.want)
			}
		}
	})
}
