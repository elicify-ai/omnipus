// Omnipus — ADR-068 §4.2 / spec §8: the comparison oracle.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// ADR-068 §4.2 names this the highest-risk component in the whole design, for a
// concrete reason: to filter over real frontmatter the operators must accept
// values whose type is only known at runtime, and at that moment the compiler
// stops protecting them. During research a first-attempt overload made `3 > 2`
// evaluate to FALSE with nothing reporting an error.
//
// The defence is spec §8's twelve rules, R-1..R-12. Every branch below cites the
// rule it implements, and compare_truthtable_test.go generates its expected
// values from those rules — never from this code.
//
// HOW THIS RELATES TO filter.go's Compare
//
// filter.go's `Compare(a, b TypedValue) (int, bool)` is a three-valued ordering
// over two CONFORMING values, written to serve FR-007/FR-008 matching. It cannot
// express §8 on its own for three reasons, and this file exists to close them:
//
//  1. It has no operand states. R-2 (absent), R-3 (`is absent`) and R-4
//     (non-conforming) are rules about the OPERAND, not about the ordering, so
//     they need an operand type that can BE absent. That is PropertyValue.
//  2. It reports nothing. `ok=false` collapses "different declared types"
//     (R-1 — an ordinary false), "cross-currency" (R-6 — the query MUST report
//     the currencies present) and "this operator is not defined for this type"
//     into one silent false. §3's behavioural contract makes that silence the
//     defect this ADR exists to remove.
//  3. It has no per-operator disposition. An ordering exists for every type in
//     it, including ones no rule gives an order to.
//
// ADR-068 D0: this file ships MECHANISM. Every record type, property name, enum
// value, currency and relation target it is exercised with is fixture data.
// ---------------------------------------------------------------------------

// ComparisonProblemCode classifies why a comparison could not compare.
//
// R-11: every type pair x every operator yields a boolean or a reported problem.
// There is no third outcome, and there is no error return.
type ComparisonProblemCode string

const (
	// CompareNonConforming is R-4: a present value that does not conform to its
	// declared type. False for every operator AND reported. Silence is the defect.
	CompareNonConforming ComparisonProblemCode = "non_conforming_value"
	// CompareCrossCurrency is R-6: money compared across currencies. Every
	// operator is false and the currencies present are named, because a caller
	// who is not told will read the false as "not greater" (FR-014).
	CompareCrossCurrency ComparisonProblemCode = "cross_currency"
	// CompareOperatorNotDefined is an operator the rules do not define for this
	// declared type. See operatorDefinedForType for the per-type authority.
	CompareOperatorNotDefined ComparisonProblemCode = "operator_not_defined_for_type"
	// CompareArityNotDefined is an operator applied across a list/scalar arity
	// boundary that §8 does not define. R-9 defines exactly one such case.
	CompareArityNotDefined ComparisonProblemCode = "operator_not_defined_across_arity"
	// CompareEnumSetsDiffer is two enum operands drawn from different declared
	// value sets, so R-5's "declared position" has no shared meaning. FR-009
	// scopes a property to its record type, so FR-023 validation should reject
	// this before evaluation; this is the backstop.
	CompareEnumSetsDiffer ComparisonProblemCode = "enum_sets_differ"
	// CompareRelationUnresolved is a relation whose wikilink the index could not
	// resolve to a record identity. R-8 compares by identity, so without one
	// there is nothing to compare (ADR-068 O-5: reported as missing, the cause
	// is not guessed at).
	CompareRelationUnresolved ComparisonProblemCode = "relation_target_unresolved"
	// CompareUnknownOperator is an operator outside the declared set.
	CompareUnknownOperator ComparisonProblemCode = "unknown_operator"
)

// ComparisonProblem is one reason a comparison did not compare. The query layer
// attaches the record's identity (FR-026); this layer supplies the reason.
type ComparisonProblem struct {
	Code     ComparisonProblemCode
	Operator Operator
	// Type is the declared type responsible, where one is.
	Type PropertyType
	// Property is the declared property name, where one side names it.
	Property string
	// Side is "left" or "right" when one operand is responsible, else "".
	Side string
	// Currencies lists, sorted, every currency present in a cross-currency
	// comparison. R-6 requires the query to report them.
	Currencies []string
	// Detail is the human sentence.
	Detail string
}

func (p ComparisonProblem) String() string {
	if len(p.Currencies) > 0 {
		return string(p.Code) + ": " + p.Detail + " (" + strings.Join(p.Currencies, ", ") + ")"
	}
	return string(p.Code) + ": " + p.Detail
}

// RelationResolver maps an on-disk wikilink (D5.1) to the record identity the
// index resolved it to (D7). R-8 compares relations by that identity, never by
// display text and never by the link's spelling — `[[Acme Corp]]` and
// `[[Acme Corp.]]` pointing at one note are ONE record, which is the failure
// D7 records as "filename is identity".
//
// ok=false means the target did not resolve. ADR-068 O-5: it is reported as
// missing and the cause is not guessed at.
type RelationResolver func(link Wikilink) (recordID string, ok bool)

// Comparator evaluates §8's rules over two operands.
//
// The zero Comparator is usable: with no RelationResolver, relation and person
// comparisons report CompareRelationUnresolved rather than silently falling back
// to comparing link text, which would violate R-8 invisibly.
type Comparator struct {
	ResolveRelation RelationResolver
}

// Evaluate applies op to two operands and returns §8's answer.
//
// It is TOTAL (R-11): it always returns a boolean, optionally with problems, and
// never returns an error and never panics — including for operands nobody
// constructed properly.
//
// The ladder below IS the rule precedence. Reordering it changes semantics.
func (c Comparator) Evaluate(op Operator, left, right PropertyValue) (bool, []ComparisonProblem) {
	if !isKnownOperator(op) {
		return false, []ComparisonProblem{{
			Code:     CompareUnknownOperator,
			Operator: op,
			Detail:   fmt.Sprintf("operator %q is not one of the declared operators", string(op)),
		}}
	}

	left, right = normalizeOperand(left), normalizeOperand(right)

	// R-3 — `is absent` is true exactly when the property has no value, and false
	// otherwise. An empty string, an empty list and a zero are values, not
	// absence; so is a corrupt value (something IS written there, it is wrong).
	// It is unary: it asks about one property, so the right operand is not read.
	if op == OpIsAbsent {
		return left.State == StateAbsent, nil
	}

	// R-2 — either side absent: false for every remaining operator. Absence is a
	// legitimate state, not a defect, so nothing is reported. FR-008's rule that
	// a NEGATIVE filter re-includes absent records is applied a layer up, in
	// Filter.Match — see filter.go's header. Comparison and matching are two
	// different things and this is the comparison.
	if left.State == StateAbsent || right.State == StateAbsent {
		return false, nil
	}

	// R-4 — a present value not conforming to its declared type does not compare.
	// False for every operator AND reported, once per offending operand.
	if left.State == StateNonConforming || right.State == StateNonConforming {
		var problems []ComparisonProblem
		if left.State == StateNonConforming {
			problems = append(problems, nonConformingComparisonProblem(op, "left", left))
		}
		if right.State == StateNonConforming {
			problems = append(problems, nonConformingComparisonProblem(op, "right", right))
		}
		return false, problems
	}

	// R-1 — different declared types are false. Never an error, never a
	// coercion, and deliberately not a reported problem either: `"3" > 2` is an
	// ordinary false, not a defect in anybody's data.
	if left.Property.Type != right.Property.Type {
		return false, nil
	}

	if left.Property.Many || right.Property.Many {
		return c.evaluateAcrossArity(op, left, right)
	}
	return c.evaluateScalar(op, left.Property.Type, left.Values[0], right.Values[0], left, right)
}

// normalizeOperand enforces R-11 structurally. A PropertyValue can reach the
// comparator from anywhere — including a zero value nobody built — and a
// nil Property or a StatePresent with no value for a scalar property is not a
// comparable thing. It is non-conformance, not a nil dereference.
func normalizeOperand(pv PropertyValue) PropertyValue {
	if pv.Property == nil {
		pv.State = StateNonConforming
		pv.Property = &Property{Name: "", Type: ""}
		return pv
	}
	if pv.State != StatePresent {
		return pv
	}
	if !pv.Property.Many && len(pv.Values) != 1 {
		pv.State = StateNonConforming
	}
	return pv
}

func nonConformingComparisonProblem(op Operator, side string, pv PropertyValue) ComparisonProblem {
	return ComparisonProblem{
		Code:     CompareNonConforming,
		Operator: op,
		Type:     pv.Property.Type,
		Property: pv.Property.Name,
		Side:     side,
		Detail: fmt.Sprintf("%s operand does not conform to declared type %q",
			side, string(pv.Property.Type)),
	}
}

// evaluateAcrossArity handles a list on either side.
//
// R-9 is the only rule §8 states about lists: `contains` on a list is
// WHOLE-ELEMENT membership, and it is never substring matching. That single
// sentence is why this function exists rather than the list being flattened into
// the scalar path — flattening a text list into text `contains` turns
// `tags contains "Acme"` into a substring match against "Acme Ltd", which is
// precisely what R-9 forbids.
//
// SPEC GAP, REPORTED NOT RESOLVED: §8 states nothing about any other operator
// against a list, nor about `contains` when BOTH sides are lists. The
// provisional behaviour is false plus a reported problem, chosen because §3's
// behavioural contract makes silence the defect. It is one branch to change.
func (c Comparator) evaluateAcrossArity(op Operator, left, right PropertyValue) (bool, []ComparisonProblem) {
	if op == OpContains && left.Property.Many && !right.Property.Many {
		needle := right.Values[0]
		for _, item := range left.Values {
			equal, problems := c.elementsEqual(op, left.Property.Type, item, needle, left, right)
			if len(problems) > 0 {
				return false, problems
			}
			if equal {
				return true, nil
			}
		}
		return false, nil
	}
	return false, []ComparisonProblem{{
		Code:     CompareArityNotDefined,
		Operator: op,
		Type:     left.Property.Type,
		Property: left.Property.Name,
		Detail:   "no rule defines this operator across this list/scalar arity boundary",
	}}
}

// elementsEqual is R-9's whole-element membership test.
//
// It is deliberately a DIFFERENT notion from the `eq` operator's disposition:
// R-9 requires element equality for every declared type, including text, whose
// scalar `eq` operator no rule defines (see operatorDefinedForType).
func (c Comparator) elementsEqual(op Operator, typ PropertyType, a, b TypedValue, la, rb PropertyValue) (bool, []ComparisonProblem) {
	switch typ {
	case TypeText:
		return a.Text == b.Text, nil
	case TypeEnum:
		if problems := c.enumSetsAgree(op, la, rb); len(problems) > 0 {
			return false, problems
		}
		return a.Enum.Name == b.Enum.Name, nil // R-5: equality is exact-case
	case TypeNumber:
		return a.Number.Cmp(b.Number) == 0, nil
	case TypeDate:
		return a.Date.Instant.Equal(b.Date.Instant), nil // R-7
	case TypeRelation, TypePerson:
		return c.relationsEqual(op, a, b, la, rb)
	case TypeMoney:
		cmp, ok := CompareMoney(a.Money, b.Money)
		if !ok {
			return false, []ComparisonProblem{crossCurrencyProblem(op, a.Money, b.Money, la)} // R-6
		}
		return cmp == 0, nil
	default:
		return false, []ComparisonProblem{{
			Code:     CompareOperatorNotDefined,
			Operator: op,
			Type:     typ,
			Detail:   "membership is not defined for this declared type",
		}}
	}
}

// relationsEqual is R-8: compare by target identity, never by display text.
func (c Comparator) relationsEqual(op Operator, a, b TypedValue, la, rb PropertyValue) (bool, []ComparisonProblem) {
	leftID, leftOK := c.resolve(a.Link)
	rightID, rightOK := c.resolve(b.Link)
	var problems []ComparisonProblem
	if !leftOK {
		problems = append(problems, unresolvedRelationProblem(op, "left", a.Link, la))
	}
	if !rightOK {
		problems = append(problems, unresolvedRelationProblem(op, "right", b.Link, rb))
	}
	if len(problems) > 0 {
		return false, problems
	}
	return leftID == rightID, nil
}

func (c Comparator) resolve(link Wikilink) (string, bool) {
	if c.ResolveRelation == nil {
		return "", false
	}
	id, ok := c.ResolveRelation(link)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

func unresolvedRelationProblem(op Operator, side string, link Wikilink, pv PropertyValue) ComparisonProblem {
	return ComparisonProblem{
		Code:     CompareRelationUnresolved,
		Operator: op,
		Type:     pv.Property.Type,
		Property: pv.Property.Name,
		Side:     side,
		Detail: fmt.Sprintf("%s operand's link %q did not resolve to a record identity, so R-8's target comparison has nothing to compare",
			side, link.Raw),
	}
}

func crossCurrencyProblem(op Operator, a, b Money, pv PropertyValue) ComparisonProblem {
	currencies := []string{a.Currency, b.Currency}
	sort.Strings(currencies)
	return ComparisonProblem{
		Code:       CompareCrossCurrency,
		Operator:   op,
		Type:       TypeMoney,
		Property:   pv.Property.Name,
		Currencies: currencies,
		Detail:     "money compares only within one currency; no conversion is performed (ADR-068 O-2)",
	}
}

// operatorDefinedForType is the disposition of each operator for each declared
// type, for SCALAR operands.
//
// AC-8.2: a change to this table is a SPECIFICATION change and must be argued as
// one. A change that only moves cells in the generated table is an
// implementation detail. compare_truthtable_test.go states this table a second
// time, independently, from the rules — so neither copy can drift quietly.
//
// Authority, row by row:
//
//   - text — ADR-068 D3's type table reads, verbatim: "prose; never validated,
//     never queried for equality". So neither equality nor ordering is defined,
//     and R-10 gives `contains` as case-sensitive substring matching.
//     ** SPEC GAP, REPORTED: §8 itself never says text equality is undefined,
//     D3 is the only authority for it, schema.go's TypeText comment paraphrases
//     D3 as "never compared for ordering" (dropping the equality clause), and
//     R-9 does require whole-element equality for text INSIDE a list. If review
//     rules that `name = "Acme"` must be ordinary string equality, this is a
//     one-cell change here and in the test's copy. **
//   - enum — R-5: ordering by declared position, equality exact-case.
//   - relation — R-8 gives equality by target identity. No rule gives record
//     identity an ORDER, so ordering is undefined. SPEC GAP, REPORTED.
//   - person — ADR-068 D3 defines person as a relation, so it inherits R-8.
//   - date — R-7: compares as an instant, so ordering and equality both hold.
//   - number — R-1's own worked example and AC-8.3 ("3 > 2 is TRUE") require the
//     full ordering family.
//   - money — R-6: every operator compares, but only within one currency.
//   - `contains` on any non-text scalar is defined by no rule. SPEC GAP, REPORTED.
//   - `is_absent` never reaches this table: R-3 preempts it in Evaluate.
var operatorDefinedForType = map[PropertyType]map[Operator]bool{
	TypeText: {
		OpEqual: false, OpLess: false, OpLessOrEqual: false,
		OpGreater: false, OpGreaterOrEqual: false, OpContains: true,
	},
	TypeEnum: {
		OpEqual: true, OpLess: true, OpLessOrEqual: true,
		OpGreater: true, OpGreaterOrEqual: true, OpContains: false,
	},
	TypeRelation: {
		OpEqual: true, OpLess: false, OpLessOrEqual: false,
		OpGreater: false, OpGreaterOrEqual: false, OpContains: false,
	},
	TypePerson: {
		OpEqual: true, OpLess: false, OpLessOrEqual: false,
		OpGreater: false, OpGreaterOrEqual: false, OpContains: false,
	},
	TypeDate: {
		OpEqual: true, OpLess: true, OpLessOrEqual: true,
		OpGreater: true, OpGreaterOrEqual: true, OpContains: false,
	},
	TypeNumber: {
		OpEqual: true, OpLess: true, OpLessOrEqual: true,
		OpGreater: true, OpGreaterOrEqual: true, OpContains: false,
	},
	TypeMoney: {
		OpEqual: true, OpLess: true, OpLessOrEqual: true,
		OpGreater: true, OpGreaterOrEqual: true, OpContains: false,
	},
}

func (c Comparator) evaluateScalar(op Operator, typ PropertyType, a, b TypedValue, la, rb PropertyValue) (bool, []ComparisonProblem) {
	if !operatorDefinedForType[typ][op] {
		return false, []ComparisonProblem{{
			Code:     CompareOperatorNotDefined,
			Operator: op,
			Type:     typ,
			Property: la.Property.Name,
			Detail: fmt.Sprintf("no rule in spec §8 defines operator %q for declared type %q",
				string(op), string(typ)),
		}}
	}

	if op == OpContains {
		// R-10 — `contains` on text is substring matching, CASE-SENSITIVE.
		return strings.Contains(a.Text, b.Text), nil
	}

	switch typ {
	case TypeRelation, TypePerson:
		// R-8 — by target identity. Only OpEqual is defined here.
		return c.relationsEqual(op, a, b, la, rb)
	case TypeEnum:
		if problems := c.enumSetsAgree(op, la, rb); len(problems) > 0 {
			return false, problems
		}
		if op == OpEqual {
			return a.Enum.Name == b.Enum.Name, nil // R-5: exact-case equality
		}
		return orderingHolds(op, compareInt(a.Enum.Position, b.Enum.Position)), nil // R-5: declared position
	case TypeMoney:
		cmp, ok := CompareMoney(a.Money, b.Money)
		if !ok {
			return false, []ComparisonProblem{crossCurrencyProblem(op, a.Money, b.Money, la)} // R-6
		}
		return orderingHolds(op, cmp), nil
	case TypeDate:
		return orderingHolds(op, compareInstant(a, b)), nil // R-7
	case TypeNumber:
		return orderingHolds(op, a.Number.Cmp(b.Number)), nil
	default:
		return false, []ComparisonProblem{{
			Code:     CompareOperatorNotDefined,
			Operator: op,
			Type:     typ,
			Detail:   "declared type has no comparison defined",
		}}
	}
}

// enumSetsAgree enforces R-5's precondition: "declared position" only means
// something when both operands are drawn from ONE declared set.
func (c Comparator) enumSetsAgree(op Operator, la, rb PropertyValue) []ComparisonProblem {
	leftSet := la.Property.PermittedValues()
	rightSet := rb.Property.PermittedValues()
	if len(leftSet) != len(rightSet) {
		return []ComparisonProblem{enumSetProblem(op, la)}
	}
	for i := range leftSet {
		if leftSet[i] != rightSet[i] {
			return []ComparisonProblem{enumSetProblem(op, la)}
		}
	}
	return nil
}

func enumSetProblem(op Operator, pv PropertyValue) ComparisonProblem {
	return ComparisonProblem{
		Code:     CompareEnumSetsDiffer,
		Operator: op,
		Type:     TypeEnum,
		Property: pv.Property.Name,
		Detail:   "the two enum operands are not drawn from one declared value set, so declared position has no shared meaning",
	}
}

func compareInstant(a, b TypedValue) int {
	switch {
	case a.Date.Instant.Before(b.Date.Instant):
		return -1
	case a.Date.Instant.After(b.Date.Instant):
		return 1
	}
	return 0
}

func orderingHolds(op Operator, cmp int) bool {
	switch op {
	case OpEqual:
		return cmp == 0
	case OpLess:
		return cmp < 0
	case OpLessOrEqual:
		return cmp <= 0
	case OpGreater:
		return cmp > 0
	case OpGreaterOrEqual:
		return cmp >= 0
	}
	return false
}
