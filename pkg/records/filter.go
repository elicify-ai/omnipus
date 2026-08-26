// Omnipus — ADR-068 D3.2 / spec FR-007, FR-008, FR-010: absence, negation, order.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// SCOPE OF THIS FILE — read this before extending it
//
// This is NOT the query engine (FR-021/FR-022) and not the comparison truth
// table (spec §8). It is the slice of predicate behaviour that FR-007 and
// FR-008 require in order to be testable at all, built to the §8 rules so that
// whoever writes the full table later inherits a comparator that already obeys
// them rather than one they must first correct.
//
//	FR-007  An absent property is a DISTINCT state from every value.
//	FR-008  A negative filter INCLUDES records where the property is absent,
//	        unless the query excludes them explicitly.
//	FR-010  Enums sort by DECLARED POSITION, not the alphabet.
//
// ---------------------------------------------------------------------------
// THERE IS EXACTLY ONE COMPARISON IMPLEMENTATION, AND IT IS NOT IN THIS FILE
//
// Every comparison this file needs is delegated to compare_oracle.go's
// `Comparator.Evaluate`, which implements spec §8's twelve rules and is verified
// cell-by-cell by compare_truthtable_test.go.
//
// This file used to carry its own small comparator "just for FR-007/FR-008".
// That arrangement was the false-green shape §8 exists to prevent: the VERIFIED
// comparator sat off the query path while the UNVERIFIED one did the real
// filtering, so a twelve-rule truth table guaranteed the correctness of code
// nobody called. Do not reintroduce a second comparison path here for any
// reason. If the oracle does not express something filtering needs, that is a
// gap in the ORACLE — fix it there, under a numbered rule.
//
// ---------------------------------------------------------------------------
// WHAT THIS FILE DOES OWN: THE MATCHING LAYER
//
// Comparison and matching are two different things, and keeping them apart is
// what makes FR-008 and §8 R-2 both true at once.
//
// R-2 says: "A comparison where either side is absent is false, for every
// operator except `is absent`." FR-008 says a negative filter must INCLUDE
// absent records. Read carelessly these contradict: false, negated, is true —
// but then FR-008 would be an accident of double negation rather than a rule,
// and `ExcludeAbsent` would have nothing to switch off.
//
//	Comparator.Evaluate  — the §8 oracle. Absent compares false. Always.
//	Filter.Match         — asks the oracle, then applies FR-008's inclusion
//	                       rule as an EXPLICIT step on top.
//
// So "status is not done" includes the days nobody recorded a status — which is
// D3.2's whole point, the checkbox third state: "Days I did not meditate"
// currently omits every day with no value, precisely the days being asked about.
//
// ---------------------------------------------------------------------------
// AND THE ONE THAT LOOKS LIKE A BUG AND IS NOT
//
// A value the oracle could not compare — non-conforming (R-4), an unresolved
// relation (R-8), cross-currency money (R-6), or an operator no rule defines for
// that type — does not match a NEGATIVE filter either. Not because the
// comparison came out true, but because a comparison that could not be made is
// REPORTED and the record EXCLUDED, rather than swept into an answer by double
// negation. FR-025/026 exist so a caller sees the record named in the problem
// list; quietly counting a corrupt value as "not done" would be a silent wrong
// answer, which is the defect this ADR exists to remove.
//
// Absence is a legitimate state and IS re-included. A failed comparison is not.
// ---------------------------------------------------------------------------

// Operator is a comparison. Negation is a separate flag on Filter rather than a
// set of "not_" operators, so there is exactly one place negation is applied
// and exactly one place FR-008's inclusion rule can be forgotten.
type Operator string

const (
	OpEqual          Operator = "eq"
	OpLess           Operator = "lt"
	OpLessOrEqual    Operator = "lte"
	OpGreater        Operator = "gt"
	OpGreaterOrEqual Operator = "gte"
	// OpContains is whole-element membership on a list (§8 R-9) and substring
	// matching on text (§8 R-10). It is NEVER substring matching on a list.
	OpContains Operator = "contains"
	// OpIsAbsent is FR-007's first-class test. It is the one operator absence
	// does not make false, and it is exempt from FR-008's inclusion rule.
	OpIsAbsent Operator = "is_absent"
)

// Operators is the closed set, for validating a query and for listing valid
// names in a rejection.
var Operators = []Operator{
	OpEqual, OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual, OpContains, OpIsAbsent,
}

func isKnownOperator(op Operator) bool {
	for _, k := range Operators {
		if k == op {
			return true
		}
	}
	return false
}

// Filter is one predicate over one property.
type Filter struct {
	// Property is the declared property name, scoped to the record type
	// (FR-009). It is validated against the schema BEFORE evaluation (FR-023).
	Property string
	Op       Operator
	// Negate inverts the predicate. `status != done` is {Op: eq, Negate: true}.
	Negate bool
	// Literal is the comparison value in LEXICAL form — the same text a
	// frontmatter file would hold — so it is parsed by exactly the same code
	// path as a record's own value (§8 R-12: the rules apply identically
	// whether the value came from a query literal or from a record).
	Literal string
	// ExcludeAbsent turns FR-008's inclusion off. It is opt-IN, so the default
	// behaviour is the one the requirement mandates and forgetting the field
	// cannot produce the wrong answer.
	ExcludeAbsent bool
}

// QueryError is a rejected query. FR-024: a query naming an unknown property or
// enum value is REJECTED with the valid names listed — it must not return an
// empty result set, because "no matches" and "you spelled it wrong" are
// indistinguishable to the caller and the second is far more common.
type QueryError struct {
	Property string
	Reason   string
	// ValidNames lists what would have been accepted: property names for an
	// unknown property, enum values for an unknown value, operators for an
	// unknown operator.
	ValidNames []string
}

func (e *QueryError) Error() string {
	msg := e.Reason
	if len(e.ValidNames) > 0 {
		msg += "; valid names are " + strings.Join(e.ValidNames, ", ")
	}
	return msg
}

// Validate checks a filter against a record type's schema before any record is
// touched (FR-023). It returns the declared property and the parsed literal.
func (f Filter) Validate(schema *Schema) (*Property, *TypedValue, error) {
	if !isKnownOperator(f.Op) {
		names := make([]string, 0, len(Operators))
		for _, o := range Operators {
			names = append(names, string(o))
		}
		return nil, nil, &QueryError{
			Property:   f.Property,
			Reason:     fmt.Sprintf("%q is not a supported operator", f.Op),
			ValidNames: names,
		}
	}

	prop, ok := schema.Property(f.Property)
	if !ok {
		return nil, nil, &QueryError{
			Property:   f.Property,
			Reason:     fmt.Sprintf("record type %q has no property %q", schema.Type, f.Property),
			ValidNames: schema.PropertyNames(),
		}
	}

	if f.Op == OpIsAbsent {
		return prop, nil, nil
	}

	// FR-024, applied to the OPERATOR as well as the property name.
	//
	// Without this check a filter naming an operator that no rule defines for
	// the property was ACCEPTED here, and then every record returned
	// Matched=false plus one identical comparison problem. A caller asking
	// `name > "Acme"` on a text property got an empty answer and 5,000 copies of
	// the same complaint instead of one refusal naming the operators that would
	// have worked — the exact silently-empty result FR-024 exists to end,
	// arriving through the operator rather than the property name.
	//
	// TWO dimensions decide this, and they are checked in the SAME ORDER the
	// oracle applies them (Comparator.Evaluate branches on arity before it
	// consults operatorDefinedForType, which is documented as being "for SCALAR
	// operands"). Checking them the other way round would refuse
	// `sizes contains 3` — `contains` is not defined for scalar number, but
	// R-9/R-13 define it against a list of them.
	//
	//	ARITY (R-13) — against a `many` property, only `contains` and
	//	               `is absent` are defined. `is absent` already returned
	//	               above, so `contains` is the only survivor here.
	//	TYPE          — for a scalar, operatorDefinedForType is the authority.
	//
	// HISTORY, because the shape of the bug is easy to reintroduce: this guard
	// read `if !prop.Many && !operatorDefinedForType[prop.Type][f.Op]`. The
	// `!prop.Many` made a MANY property skip the check entirely, so `gt` against
	// a `many text` property validated clean and produced one identical
	// CompareArityNotDefined per record — the very per-record flood the comment
	// above it claimed R-13 had ended. Regression: filter_r13_validate_test.go.
	if prop.Many {
		if f.Op != OpContains {
			return nil, nil, &QueryError{
				Property: f.Property,
				// arityRefusalDetail is the oracle's own R-13 sentence, reused
				// verbatim so the up-front refusal and the per-record backstop
				// cannot describe the same rule in two different ways.
				Reason:     arityRefusalDetail(f.Op, prop),
				ValidNames: []string{string(OpContains), string(OpIsAbsent)},
			}
		}
	} else if !operatorDefinedForType[prop.Type][f.Op] {
		valid := make([]string, 0, len(Operators))
		for _, o := range Operators {
			if operatorDefinedForType[prop.Type][o] {
				valid = append(valid, string(o))
			}
		}
		valid = append(valid, string(OpIsAbsent)) // R-3 is defined for every type.
		return nil, nil, &QueryError{
			Property:   f.Property,
			Reason:     fmt.Sprintf("operator %q is not defined for a %s property", f.Op, prop.Type),
			ValidNames: valid,
		}
	}

	// The literal goes through ParseValue — the same function a record's own
	// value goes through — so an enum literal outside the declared set is
	// rejected here with the permitted values listed (FR-011 + FR-024), rather
	// than quietly matching nothing.
	lit, verr := ParseValue(prop, Node{Kind: KindScalar, Text: f.Literal})
	if verr != nil {
		return nil, nil, &QueryError{
			Property:   f.Property,
			Reason:     verr.Reason,
			ValidNames: verr.Permitted,
		}
	}
	return prop, &lit, nil
}

// MatchResult is a filter's verdict on one record.
type MatchResult struct {
	// Matched is whether the record belongs in the result set.
	Matched bool
	// State is what the record actually said about the property. A caller
	// building a completeness verdict needs this to distinguish "excluded
	// because it did not match" from "excluded because it was corrupt".
	State PropertyState
	// Problems names anything the filter could not evaluate, in RECORD terms —
	// each Finding carries the record path, the property and the offending
	// value, which is what FR-026's "names the offending records" requires.
	Problems []Finding
	// ComparisonProblems carries the oracle's own rule-level verdicts (§8) —
	// cross-currency money, an unresolved relation, an operator no rule defines
	// for that type. They are kept separate from Problems rather than flattened
	// because they name a RULE, not a record, and a caller reporting
	// incompleteness needs both halves.
	ComparisonProblems []ComparisonProblem
}

// Match evaluates a filter against one record using the zero Comparator.
//
// The zero Comparator has no RelationResolver, so relation and person
// comparisons report CompareRelationUnresolved rather than silently comparing
// link text. Callers that can resolve relations use MatchWith.
func (f Filter) Match(schema *Schema, rec Record) (MatchResult, error) {
	return f.MatchWith(Comparator{}, schema, rec)
}

// MatchWith evaluates a filter against one record using a caller-supplied
// comparator.
//
// The order of the four cases below IS the requirement, in code:
//
//  1. `is absent` answers directly — it is the one operator absence does not
//     make false (FR-007, §8 R-3).
//  2. A comparison the oracle COULD NOT MAKE is reported and excluded, whether
//     or not the filter is negated (see the header note).
//  3. ABSENT compares false (§8 R-2), and then FR-008 puts it BACK IN for a
//     negative filter unless the caller opted out.
//  4. Otherwise the oracle's answer stands, negated if asked.
func (f Filter) MatchWith(c Comparator, schema *Schema, rec Record) (MatchResult, error) {
	prop, literal, err := f.Validate(schema)
	if err != nil {
		return MatchResult{}, err
	}

	left := ResolveProperty(rec, prop)
	res := MatchResult{State: left.State, Problems: left.Findings}

	right := literalOperand(prop, literal)
	answer, cproblems := c.Evaluate(f.Op, left, right)
	res.ComparisonProblems = cproblems

	// (1) is absent — FR-007 / R-3. A property holding a corrupt value is NOT
	// absent: something is written there, it is just wrong.
	if f.Op == OpIsAbsent {
		res.Matched = answer
		if f.Negate {
			res.Matched = !answer
		}
		return res, nil
	}

	// (2) the oracle could not compare — R-4, R-6, R-8, or an undefined
	// operator. Excluded and reported, NEVER re-included by negation.
	if left.State == StateNonConforming || len(cproblems) > 0 {
		res.Matched = false
		return res, nil
	}

	// (3) absent — §8 R-2 already gave the comparison's verdict; FR-008 now
	// decides whether the RECORD is nonetheless included.
	//
	// NOTE the `answer` on the next line, and do not "simplify" it to a literal
	// false. R-2 belongs to the oracle. An earlier version of this function
	// hardcoded false here — arithmetically identical while R-2 holds, and a
	// SECOND IMPLEMENTATION of R-2 all the same. It was caught by mutating the
	// oracle's R-2 branch and watching every filter test go on passing: the
	// rule was verified in the oracle and ignored on the path that uses it,
	// which is the exact arrangement §8 exists to prevent.
	if left.State == StateAbsent {
		res.Matched = answer
		if f.Negate {
			// FR-008: a negative filter includes the absent record — which,
			// given R-2's false, is the negation itself. ExcludeAbsent is the
			// explicit opt-out the requirement provides for.
			res.Matched = !answer && !f.ExcludeAbsent
		}
		return res, nil
	}

	// (4) the oracle's answer.
	res.Matched = answer
	if f.Negate {
		res.Matched = !answer
	}
	return res, nil
}

// literalOperand wraps a query literal as the right-hand operand.
//
// The literal's property is a SCALAR clone of the declared one — same type,
// same enum value set, `many` forced off. Both halves matter:
//
//   - The value set must be identical or the oracle's R-5 precondition
//     (`enumSetsAgree`) refuses the comparison: "declared position" only means
//     something when both operands are drawn from ONE declared set.
//   - `many` must be off because a query literal is one value even when the
//     property it filters is a list. Leaving it on makes `segment contains
//     vendor` a list-against-list comparison, which R-9 does not define, and a
//     defined membership test would start reporting itself as undefined.
//
// A nil literal (the `is absent` case, which reads only the left operand)
// yields an absent operand rather than a zero PropertyValue, so nothing
// downstream has to tolerate a nil Property.
func literalOperand(prop *Property, literal *TypedValue) PropertyValue {
	scalar := *prop
	scalar.Many = false
	if literal == nil {
		return PropertyValue{Property: &scalar, State: StateAbsent}
	}
	return PropertyValue{Property: &scalar, State: StatePresent, Values: []TypedValue{*literal}}
}

// Compare is a three-valued ordering VIEW of the oracle, for callers and tests
// that want -1/0/+1 rather than one boolean per operator.
//
// It owns no semantics. Every answer below comes from Comparator.Evaluate; this
// function only asks it three questions and reads the replies. ok=false means
// "the oracle does not order these two values" — different declared types (R-1),
// an operator no rule defines for the type, or any reported problem such as
// cross-currency money (R-6).
func Compare(a, b TypedValue) (cmp int, ok bool) {
	var c Comparator
	left := singletonOperand(a)
	right := singletonOperand(b)

	for _, probe := range []struct {
		op  Operator
		cmp int
	}{
		{OpEqual, 0},
		{OpLess, -1},
		{OpGreater, 1},
	} {
		answer, problems := c.Evaluate(probe.op, left, right)
		if len(problems) > 0 {
			return 0, false
		}
		if answer {
			return probe.cmp, true
		}
	}
	// Not equal, not less, not greater, and nothing reported: the oracle
	// declined to relate them at all (R-1's different declared types).
	return 0, false
}

// singletonOperand wraps one typed value as a scalar operand. The synthesised
// property carries only the declared type, which is all the oracle reads for
// every type except enum — and for enum both sides are synthesised the same
// way, so R-5's shared-value-set precondition holds by construction.
func singletonOperand(v TypedValue) PropertyValue {
	return PropertyValue{
		Property: &Property{Type: v.Type},
		State:    StatePresent,
		Values:   []TypedValue{v},
	}
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// SortByEnumOrder sorts enum value NAMES by their declared position (FR-010).
//
// Values not declared by the property sort last, in lexical order among
// themselves — they should not exist (FR-011 rejects them), and putting them at
// the end keeps the ordering total without pretending they have a position.
func SortByEnumOrder(prop *Property, values []string) {
	sort.SliceStable(values, func(i, j int) bool {
		pi, oki := prop.EnumPosition(values[i])
		pj, okj := prop.EnumPosition(values[j])
		switch {
		case oki && okj:
			return pi < pj
		case oki:
			return true
		case okj:
			return false
		}
		return values[i] < values[j]
	})
}
