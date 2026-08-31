// Omnipus — spec test 94: FR-140, the parser lives in the write path only.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// formulaFixtureSchema is the schema every formula test types against.
//
// It is built by hand rather than loaded from a file so the DECLARATIONS a test
// depends on are visible in the test, which is what makes a type refusal
// checkable against the specification instead of against whatever a fixture
// happened to contain.
func formulaFixtureSchema() *Schema {
	props := map[string]*Property{
		"amount":   testProperty("amount", TypeDecimal, false),
		"quantity": testProperty("quantity", TypeInteger, false),
		"name":     testProperty("name", TypeText, false),
		"stage":    testProperty("stage", TypeEnum, false),
		"due":      testProperty("due", TypeDate, false),
		"owner":    testProperty("owner", TypeRelation, false),
		"sizes":    testProperty("sizes", TypeInteger, true),
	}
	order := make([]string, 0, len(props))
	for k := range props {
		order = append(order, k)
	}
	return &Schema{Type: "fixture", Properties: props, PropertyOrder: order}
}

// TestFormula_ParseFailureIsWriteTimeRefusal is spec test 94 (FR-140).
//
// THE INVARIANT UNDER TEST, in one sentence: an unparseable formula never
// exists at query time, so a parse failure can never become an empty result.
// The test has three halves, and the third is the one that is easy to omit.
//
//	(a) an unparseable expression is REFUSED, naming the position
//	(b) the refusal happens at ValidateFormulaSet — the write path and the
//	    loader's re-check both go through it, so a hand-edited view file is
//	    re-checked on load exactly as a written one is
//	(c) nothing that failed is STORED: ValidateFormulaSet returns a nil set,
//	    not a partially-populated one. A set containing the formulas that
//	    happened to parse would put the invariant back in play for the next
//	    caller, who would have no way to know some were missing.
func TestFormula_ParseFailureIsWriteTimeRefusal(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantPos is the byte offset the refusal must name, derived by counting
		// the source string BY HAND against the specification's rule, never by
		// running the parser and copying what it said.
		wantPos int
		wantIn  string
		code    FormulaErrorCode
	}{
		{
			name:    "an unclosed parenthesis",
			src:     "(amount + 1",
			wantPos: 0, // the `(` that is never closed
			wantIn:  "never closed",
			code:    FormulaErrSyntax,
		},
		{
			name:    "a function outside the closed set",
			src:     "sqrt(amount)",
			wantPos: 0,
			wantIn:  "is not a function the formula grammar defines",
			code:    FormulaErrUnknownFunction,
		},
		{
			name:    "a bare = , which is the FILTER vocabulary's spelling",
			src:     "amount = 1",
			wantPos: 7, // a-m-o-u-n-t-space = 7 bytes before the `=`
			wantIn:  "==",
			code:    FormulaErrSyntax,
		},
		{
			name:    "a file property that does not exist",
			src:     "file.author",
			wantPos: 0,
			wantIn:  "is not one of the file properties",
			code:    FormulaErrUnknownReference,
		},
		{
			name:    "a trailing operator with nothing after it",
			src:     "amount +",
			wantPos: 8, // the end of the string
			wantIn:  "ends where a value was expected",
			code:    FormulaErrSyntax,
		},
		{
			name:    "an unterminated text literal",
			src:     `name == "acme`,
			wantPos: 8,
			wantIn:  "never closed",
			code:    FormulaErrSyntax,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// (a) and (b): the refusal comes out of the one entry point both
			// the write path and the loader use.
			set, errs := ValidateFormulaSet(map[string]string{"f": c.src}, formulaFixtureSchema())

			if len(errs) == 0 {
				t.Fatalf("FR-140: %q was ACCEPTED. An expression that does not parse must be refused at write time — stored, it becomes an empty result at query time with no error anywhere", c.src)
			}
			// (c) nothing partially stored.
			if set != nil {
				t.Fatalf("FR-140: a refusal returned a non-nil FormulaSet with %d formula(s). A refusal must store nothing", set.Len())
			}

			e := errs[0]
			if e.Code != c.code {
				t.Errorf("the refusal is coded %q, want %q — the code is what a caller branches on", e.Code, c.code)
			}
			if e.Offset != c.wantPos {
				t.Errorf("FR-140: the refusal names position %d, want %d. %q\n  message: %s",
					e.Offset, c.wantPos, c.src, e.Error())
			}
			if !strings.Contains(e.Error(), c.wantIn) {
				t.Errorf("the refusal does not say why: got %q, want it to contain %q", e.Error(), c.wantIn)
			}
			if e.Formula != "f" {
				t.Errorf("the refusal does not name WHICH formula: got %q, want %q", e.Formula, "f")
			}
		})
	}
}

// TestFormula_UnknownFunctionListsTheClosedSet is FR-143's posture applied to
// functions: a name outside the set is refused BY NAME, LISTING the set.
//
// A refusal that says only "unknown function" makes the author guess at a
// closed set they cannot see, which is exactly the failure FR-024 was written
// against for property types.
func TestFormula_UnknownFunctionListsTheClosedSet(t *testing.T) {
	_, errs := ValidateFormulaSet(map[string]string{"f": "sqrt(amount)"}, formulaFixtureSchema())
	if len(errs) == 0 {
		t.Fatal("FR-143: `sqrt` is outside the closed function set and must be refused")
	}
	msg := errs[0].Error()

	// The thirteen names FR-143 lists, plus FR-134's four file methods. Written
	// out here from the specification, not read off the implementation's map.
	for _, fn := range []string{
		"if", "toFixed", "mean", "round", "date", "today", "now", "format",
		"list", "link", "icon", "contains", "time",
		"file.hasTag", "file.inFolder", "file.hasLink", "file.asLink",
	} {
		if !strings.Contains(msg, fn) {
			t.Errorf("FR-143: the refusal must LIST the supported set, but %q is missing from:\n  %s", fn, msg)
		}
	}
	if !strings.Contains(msg, "sqrt") {
		t.Errorf("FR-143: the refusal must name the offending function; got:\n  %s", msg)
	}
}

// TestFormula_QueryPathHoldsNoParser is FR-140's second half, asserted
// structurally rather than by prose.
//
// The claim is that a query reaches a formula only as `formula.<name>` — a
// reference to something already validated — and that no text expression is
// parsed on the query path. The evaluator is the query path's entry point, so
// the checkable form of the claim is: FormulaEvaluator can only be constructed
// from a *FormulaSet, and a *FormulaSet can only be produced by
// ValidateFormulaSet.
//
// This test asserts the consequence that a caller can actually observe: an
// evaluator built over the zero set evaluates NOTHING, rather than parsing a
// name it was handed.
func TestFormula_QueryPathHoldsNoParser(t *testing.T) {
	e := NewFormulaEvaluator(&FormulaSet{}, testComparator(), formulaTestNow())
	e.Begin(emptyFormulaCandidate{})

	for _, name := range []string{"amount + 1", "f", "sqrt(1)"} {
		if _, ok := e.Evaluate(name); ok {
			t.Fatalf("FR-140: the evaluator returned a value for %q over an empty set — the query path must resolve a NAME against a validated set, never parse text", name)
		}
	}
}

// emptyFormulaCandidate supplies nothing, so a test that accidentally depends on
// candidate data fails loudly rather than reading a leftover fixture.
type emptyFormulaCandidate struct{}

func (emptyFormulaCandidate) FormulaProperty(string) (PropertyValue, bool) {
	return PropertyValue{}, false
}
func (emptyFormulaCandidate) FormulaFileProperty(string) (PropertyValue, bool) {
	return PropertyValue{}, false
}
