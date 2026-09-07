// Omnipus — spec §8 R-13 at the ORACLE: what a `many` property admits, and
// what it refuses.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// R-13 SHRANK, AND THAT IS WHAT THIS FILE MEASURES
//
// Before ruling R-B, R-13 refused EVERYTHING against a `many` property except
// one invented operator and `is absent`. It had to: one operator was serving
// both "whole-element membership" and "substring", so equality-versus-membership
// had no defined answer and the only honest thing to do was refuse.
//
// SQL's vocabulary already draws the distinction, so R-13 now reads:
//
//	"Against a `many` property, `=`, `<>`, `IN`, `LIKE`, `IS NULL` and
//	 `IS NOT NULL` are DEFINED, and they mean what R-9 says: element-wise, with
//	 the record matching if ANY element matches. Only the ORDERING operators —
//	 `<`, `<=`, `>`, `>=` — remain undefined."
//
// The refusal survives only where the question is genuinely undefined: "is this
// list greater than `vendor`?" has no answer in any vocabulary.
//
// This file sweeps the whole arity space — scalar/list on each side — for both
// halves of that sentence, which the hand-written cases in
// compare_truthtable_test.go do not: they assert the rule, this asserts that the
// rule holds at every arity COMBINATION, including list-against-list, which used
// to be a refusal and is now a defined element-wise comparison.
//
// WHERE THE REST OF R-13 IS TESTED:
//
//	compare_truthtable_test.go     — the dispositions, generated, with the arity
//	                                 dimension inside the table on both sides.
//	filter_r13_validate_test.go    — the refusal a CALLER meets, raised ONCE by
//	                                 Filter.Validate before any record is read.
//	this file                      — the arity sweep and the refusal's wording.
// ---------------------------------------------------------------------------

// r13Arities is the four arity combinations, built over one comparison domain so
// that R-1 never preempts R-13.
func r13Arities(t *testing.T) []struct {
	name        string
	left, right PropertyValue
	anyMany     bool
} {
	t.Helper()
	listProp := &Property{Name: "segment", Type: TypeText, RecordType: "fixture", Many: true}
	scalarProp := &Property{Name: "segment", Type: TypeText, RecordType: "fixture"}

	list := func() PropertyValue {
		return PropertyValue{Property: listProp, State: StatePresent,
			Values: []TypedValue{tvText("vendor"), tvText("partner")}}
	}
	scalar := func() PropertyValue {
		return PropertyValue{Property: scalarProp, State: StatePresent,
			Values: []TypedValue{tvText("vendor")}}
	}

	return []struct {
		name        string
		left, right PropertyValue
		anyMany     bool
	}{
		{"scalar vs scalar", scalar(), scalar(), false},
		{"list vs scalar", list(), scalar(), true},
		{"scalar vs list", scalar(), list(), true},
		{"list vs list", list(), list(), true},
	}
}

// TestOracle_R13_ElementWiseOperatorsAreDefinedAtEveryArity is the half of the
// ruling that ADDED capability. Each of the four is defined at every arity
// combination, list-against-list included — which the pre-ruling comparator
// refused with "membership tests ONE value in that list, so the value compared
// against must be a single value, not a list".
//
// It is the half a "safe" fix would get wrong: refusing everything against a
// list passes every assertion in the sibling test below while making list
// queries impossible.
func TestOracle_R13_ElementWiseOperatorsAreDefinedAtEveryArity(t *testing.T) {
	var c Comparator
	for _, arity := range r13Arities(t) {
		for _, op := range []Operator{OpEqual, OpNotEqual, OpLike, OpIn} {
			got, problems := c.Evaluate(op, arity.left, arity.right)
			if len(problems) != 0 {
				t.Errorf("R-13: `%s` %s reported %v; the four element-wise operators are DEFINED at every arity",
					arity.name, op, problemCodes(problems))
			}
			// The values are chosen so that every one of these is TRUE, which
			// makes "defined" mean more than "returned false quietly":
			//   =    vendor is an element of both sides
			//   <>   partner is not equal to vendor, so SOME pair differs
			//        (except scalar-vs-scalar, where both sides are `vendor`)
			//   LIKE a wildcard-free pattern is an exact folded match
			//   IN   membership over the same set
			wantTrue := op != OpNotEqual || arity.anyMany
			if got != wantTrue {
				t.Errorf("R-13/R-9: `%s` %s = %v, want %v (ANY element matching is a match)",
					arity.name, op, got, wantTrue)
			}
		}
		// The unary two are defined at every arity too, and neither reads the
		// right operand.
		if got, problems := c.Evaluate(OpIsNotNull, arity.left, arity.right); !got || len(problems) != 0 {
			t.Errorf("R-13: `%s` IS NOT NULL = %v/%v, want true and no problems", arity.name, got, problemCodes(problems))
		}
	}
}

// TestOracle_R13_OrderingIsRefusedWhereverAListIsInvolved is the half that
// SURVIVES, and it asserts the refusal's wording as well as its existence.
//
// This is the FR-024 shape: reject, and say what would have worked. A refusal
// that names neither the property nor the remedy leaves a caller with an empty
// answer and nothing to do about it, which is the defect R-13 exists to stop.
func TestOracle_R13_OrderingIsRefusedWhereverAListIsInvolved(t *testing.T) {
	var c Comparator
	for _, arity := range r13Arities(t) {
		for _, op := range []Operator{OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual} {
			got, problems := c.Evaluate(op, arity.left, arity.right)
			if !arity.anyMany {
				// The scalar case must still WORK, or the arity branch has
				// swallowed the ordinary comparison.
				if len(problems) != 0 {
					t.Errorf("R-13 must not touch `%s` %s; got %v", arity.name, op, problemCodes(problems))
				}
				continue
			}
			if got {
				t.Errorf("R-13: `%s` %s = true; an ordering comparison over a list has no answer in any vocabulary", arity.name, op)
			}
			if len(problems) != 1 || problems[0].Code != CompareArityNotDefined {
				t.Fatalf("R-13: `%s` %s reported %v, want exactly one %s",
					arity.name, op, problemCodes(problems), CompareArityNotDefined)
			}
			detail := problems[0].Detail
			if !strings.Contains(detail, "segment") {
				t.Errorf("the refusal must NAME the property so the caller can find it.\n  got: %s", detail)
			}
			for _, remedy := range []string{"=", "IN", "LIKE"} {
				if !strings.Contains(detail, remedy) {
					t.Errorf("the refusal must name the REMEDY %q — that is what R-13 exists for.\n  got: %s", remedy, detail)
				}
			}
			if !strings.Contains(detail, string(op)) {
				t.Errorf("the refusal must name the operator that was rejected.\n  got: %s", detail)
			}
			// FR-022c: in every case the refusal lists the supported operators.
			if len(problems[0].Supported) == 0 {
				t.Errorf("FR-022c: the R-13 refusal for %s lists no supported operators", op)
			}
		}
	}
}
