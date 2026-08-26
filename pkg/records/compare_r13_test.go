// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// TestOracle_R13_ListOperatorsRefuseAndNameTheRemedy guards spec §8 R-13 at the
// ORACLE, per comparison.
//
// R-13 was added to the spec after the oracle was written and never reached the
// code. The DISPOSITIONS were already right — `contains` and `is absent` work,
// everything else refuses — so no test noticed. What was missing is the part
// R-13 exists for: the refusal named neither the property nor the remedy, so a
// caller asking `segment != vendor` got "no rule defines this operator across
// this list/scalar arity boundary" and no idea that `contains` was the answer.
//
// This is the FR-024 shape: reject, and say what would have worked.
//
// WHERE THE REST OF R-13 IS TESTED, because this file is only one third of it:
//
//	compare_truthtable_test.go — the DISPOSITIONS, generated. The arity
//	   dimension is inside the truth table: every declared type appears as a
//	   scalar, an empty list, a one-element list, a two-element list and a list
//	   with a non-conforming element, on both sides, against every operator.
//	   (This comment used to record the opposite — that the table held zero
//	   multi-value cells — which made it a note describing a gap instead of a
//	   test closing one. The gap is closed; see that file's header.)
//	filter_r13_validate_test.go — the refusal a CALLER meets, raised once by
//	   Filter.Validate before any record is read.
//	this file — the wording, and the fact that the oracle still refuses on its
//	   own for anyone driving the Comparator directly.
func TestOracle_R13_ListOperatorsRefuseAndNameTheRemedy(t *testing.T) {
	segment := &Property{Name: "segment", Type: TypeText, Many: true}
	left := PropertyValue{Property: segment, State: StatePresent,
		Values: []TypedValue{{Type: TypeText, Text: "vendor"}, {Type: TypeText, Text: "partner"}}}
	scalar := &Property{Name: "segment", Type: TypeText}
	right := PropertyValue{Property: scalar, State: StatePresent,
		Values: []TypedValue{{Type: TypeText, Text: "vendor"}}}

	var c Comparator

	t.Run("contains is defined and works", func(t *testing.T) {
		got, probs := c.Evaluate(OpContains, left, right)
		if len(probs) > 0 {
			t.Fatalf("contains must be defined against a list (R-9/R-13); got problems: %+v", probs)
		}
		if !got {
			t.Fatal("segment contains vendor should be true")
		}
	})

	for _, op := range []Operator{OpEqual, OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual} {
		t.Run("refused and remedied: "+string(op), func(t *testing.T) {
			got, probs := c.Evaluate(op, left, right)
			if got {
				t.Fatalf("%s against a list must not answer true — R-13 defines it as refused, not as membership", op)
			}
			if len(probs) == 0 {
				t.Fatalf("%s against a list must REPORT a problem; a silent false is the empty-answer defect R-13 exists to stop", op)
			}
			d := probs[0].Detail
			if !strings.Contains(d, "segment") {
				t.Fatalf("the refusal must name the property so the caller can find it.\ngot: %s", d)
			}
			if !strings.Contains(d, "contains") {
				t.Fatalf("the refusal must name the REMEDY — this is the whole point of R-13.\ngot: %s", d)
			}
		})
	}

	// `contains` with BOTH sides a list is undefined too: R-9 defines
	// membership of ONE value in a list. The refusal must not tell the caller
	// to "use contains" — they already did, and repeating it leaves them with
	// no next step.
	t.Run("refused: contains against a list on both sides names a remedy the caller can act on", func(t *testing.T) {
		rightList := PropertyValue{Property: segment, State: StatePresent,
			Values: []TypedValue{{Type: TypeText, Text: "vendor"}}}
		got, probs := c.Evaluate(OpContains, left, rightList)
		if got {
			t.Fatal("contains between two lists must not answer true — R-9 defines a scalar needle only")
		}
		if len(probs) != 1 || probs[0].Code != CompareArityNotDefined {
			t.Fatalf("expected one %q; got %+v", CompareArityNotDefined, probs)
		}
		d := probs[0].Detail
		if !strings.Contains(d, "segment") {
			t.Fatalf("the refusal must name the property.\ngot: %s", d)
		}
		if strings.Contains(d, "use contains") {
			t.Fatalf("the operator already IS contains; %q is advice the caller cannot act on.\ngot: %s", "use contains", d)
		}
		if !strings.Contains(d, "single value") {
			t.Fatalf("the refusal must say what would have worked: a single value as the needle.\ngot: %s", d)
		}
	})
}
