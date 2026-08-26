// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// TestOracle_R13_ListOperatorsRefuseAndNameTheRemedy guards spec §8 R-13.
//
// R-13 was added to the spec after the oracle was written and never reached
// the code. The DISPOSITIONS were already right — `contains` and `is absent`
// work, everything else refuses — so no test noticed, and the truth table
// contains zero multi-value cells. What was missing is the part R-13 exists
// for: the refusal named neither the property nor the remedy, so a caller
// asking `segment != vendor` got "no rule defines this operator across this
// list/scalar arity boundary" and no idea that `contains` was the answer.
//
// This is the FR-024 shape: reject, and say what would have worked.
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
}
