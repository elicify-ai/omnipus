// Omnipus — ADR-068 §4.2 / spec §8: the comparison oracle.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
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
// The defence is spec §8's thirteen rules, R-1..R-13. Every branch below cites
// the rule it implements, and compare_truthtable_test.go generates its expected
// values from those rules — never from this code.
//
// ---------------------------------------------------------------------------
// EVERY COMPARISON IS DECIDED HERE, IN GO. SQLITE DECIDES NOTHING.
//
// ADR-068 ruling R-A (revision 7), spec §8.1: "the properties index NARROWS
// CANDIDATES; our own tested comparator DECIDES." SQLite answers set-membership
// questions over indexed columns — which notes are `type: deal`, which paths are
// in scope — and hands back candidate rows. Every predicate in R-1..R-13 is then
// applied HERE.
//
// That is not a preference, it is the only way three of the rules can hold at
// all:
//
//   - R-2/FR-008. SQL's `NOT` is three-valued, so `NOT(NULL)` is `NULL` and every
//     absent row falls out of a negation. A Go comparator returning a real
//     `bool` makes `NOT(false)` true by construction, at any depth of the tree.
//   - R-10/FR-011a. SQLite's `COLLATE NOCASE`, `LIKE` and `lower()` fold the two
//     ASCII pairs of the spec's fourteen-pair receipt and ZERO of the twelve
//     non-ASCII ones; there is no `ENABLE_ICU` in the linked build. Unicode case
//     folding is something this comparator can deliver and SQLite cannot.
//   - R-12. Comparison affinity makes `3 = '3'` false between two literals and
//     true between a column and a literal. One comparator is one provenance.
//
// So: no comparison operator, no `LIKE`, no `IN`, no `ORDER BY`, no `GROUP BY`,
// no aggregate and no `COLLATE` may be emitted into SQL for the purpose of
// DECIDING anything. If you are adding a code path that pushes a predicate down
// to the store, you are reintroducing the class of defect this file exists to
// remove.
//
// ---------------------------------------------------------------------------
// THE OPERATOR VOCABULARY IS SQL'S (ADR-068 O-3 as amended, spec FR-022b)
//
// It used to be ours — `eq` / `lt` / `lte` / `gt` / `gte` / `contains` /
// `is_absent` — and the argument for replacing them is retrieval accuracy, not
// style: that vocabulary has appeared in a model's training data zero times and
// SQL's an enormous number of times. A model choosing `LIKE` is recalling; a
// model choosing `contains` was guessing.
//
// O-3 is AMENDED, NOT OVERTURNED. Both halves of its resolution still hold: the
// filter is a STRUCTURED OBJECT and there is NO PARSER. Only the spelling of the
// operator inside the JSON changed. `{property: "tags", op: "LIKE", value:
// "vend%"}` is an object, not a WHERE clause, and nothing in this package
// recognises SQL text.
//
// ADR-068 D0: this file ships MECHANISM. Every record type, property name, enum
// value and relation target it is exercised with is fixture data.
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
	// CompareOperatorNotDefined is an operator the rules do not define for this
	// declared type. See operatorDefinedForType for the per-type authority.
	CompareOperatorNotDefined ComparisonProblemCode = "operator_not_defined_for_type"
	// CompareArityNotDefined is R-13: an ORDERING operator against a `many`
	// property. `=`, `<>`, `IN` and `LIKE` are defined there and never produce
	// this code.
	CompareArityNotDefined ComparisonProblemCode = "operator_not_defined_across_arity"
	// CompareRelationUnresolved is a relation whose wikilink the index could not
	// resolve to a record identity. R-8 compares by identity, so without one
	// there is nothing to compare (ADR-068 O-5: reported as missing, the cause
	// is not guessed at).
	CompareRelationUnresolved ComparisonProblemCode = "relation_target_unresolved"
	// CompareUnknownOperator is an operator outside FR-022b's closed ten.
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
	// Detail is the human sentence.
	Detail string
	// Supported lists FR-022b's operators that WOULD have worked here, so a
	// refusal never leaves the caller guessing. FR-022c: "in every case the
	// refusal lists the supported operators."
	Supported []string
	// Remedy names the parameter or the operator that does the job instead —
	// `join` for a relation, `group_by` for grouping, `aggregate` for a total.
	Remedy string
}

func (p ComparisonProblem) String() string {
	var b strings.Builder
	b.WriteString(string(p.Code))
	b.WriteString(": ")
	b.WriteString(p.Detail)
	if p.Remedy != "" {
		b.WriteString("; " + p.Remedy)
	}
	if len(p.Supported) > 0 {
		b.WriteString("; supported operators are " + strings.Join(p.Supported, ", "))
	}
	return b.String()
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
//
// ONE PRECEDENCE CHANGE FROM THE PRE-SQL-VOCABULARY VERSION, stated because it
// is deliberate: the TYPE disposition is now consulted BEFORE the arity rule,
// where it used to come after. The old order was forced by `contains`, an
// operator that was undefined for a scalar number and defined against a list of
// them — so consulting the scalar table first would have refused
// `sizes contains 3`. FR-022b's vocabulary has no such inversion: `=`, `<>`,
// `IN` and `LIKE` are defined for a type at BOTH arities, and R-13 removes only
// the four ordering operators from a `many` property. With the inversion gone,
// type-first produces the more actionable message — `LIKE` against a `many date`
// property is refused as "LIKE is not defined for a date property", which is the
// fix, rather than as an arity complaint the caller cannot act on.
func (c Comparator) Evaluate(op Operator, left, right PropertyValue) (bool, []ComparisonProblem) {
	if !isKnownOperator(op) {
		return false, []ComparisonProblem{unknownOperatorProblem(op)}
	}

	left, right = normalizeOperand(left), normalizeOperand(right)

	// R-3 — `IS NULL` is true exactly when the property has no value, and false
	// otherwise; `IS NOT NULL` is its complement. An empty string, an empty list
	// and a zero are VALUES, not absence; so is a corrupt value (something IS
	// written there, it is just wrong).
	//
	// These two are unary: they ask about one property, so the right operand is
	// not read. They are also the ONLY exemptions from R-2 — `<>` is NOT one,
	// ruled explicitly in spec §8 R-2 (review round 6, C-7), because SQL's
	// `x <> 'v'` over a NULL `x` excludes the row and adopting SQL's names
	// without SQL's semantics is exactly what ruling R-B forbids.
	switch op {
	case OpIsNull:
		return left.State == StateAbsent, nil
	case OpIsNotNull:
		return left.State != StateAbsent, nil
	}

	// R-2 — either side absent: false for every remaining operator, `<>`
	// included. Absence is a legitimate state, not a defect, so nothing is
	// reported. FR-008's rule that a NEGATIVE filter re-includes absent records
	// is applied a layer up, in Filter.Match — see filter.go's header.
	// Comparison and matching are two different things and this is the
	// comparison.
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
	//
	// TWO PAIRS ARE ONE TYPE for this rule and comparisonDomain is where that
	// lives: `text`/`enum` and `integer`/`decimal`. Every other pair — including
	// `relation` against `person`, which both hold a wikilink — is different.
	if comparisonDomain(left.Property.Type) != comparisonDomain(right.Property.Type) {
		return false, nil
	}

	// The type disposition. An operator no rule defines for this declared type
	// is false AND reported, never a silent false: §3's behavioural contract
	// makes the silence the defect, and FR-022c requires the refusal to name
	// what WOULD have worked.
	if !operatorDefinedForType[left.Property.Type][op] {
		return false, []ComparisonProblem{operatorNotDefinedProblem(op, left)}
	}

	// R-13 — the arity rule. Against a `many` property `=`, `<>`, `IN` and
	// `LIKE` are DEFINED (element-wise, R-9), and only the four ORDERING
	// operators are refused. The refusal names the remedy, because "not defined"
	// alone leaves a caller with an empty answer and nothing to do about it.
	if (left.Property.Many || right.Property.Many) && isOrderingOperator(op) {
		return false, []ComparisonProblem{{
			Code:      CompareArityNotDefined,
			Operator:  op,
			Type:      left.Property.Type,
			Property:  manyPropertyName(left, right),
			Detail:    arityRefusalDetail(op, left, right),
			Supported: []string{string(OpEqual), string(OpNotEqual), string(OpIn), string(OpLike), string(OpIsNull), string(OpIsNotNull)},
		}}
	}

	return c.evaluateElementwise(op, left, right)
}

// comparisonDomain collapses the declared types that R-1 says are ONE type for
// comparison purposes onto a single key.
//
// R-1, as amended in revision 6: "`text` and `enum` are ONE declared type for
// comparison purposes, and `integer` and `decimal` are ONE; every other pair is
// different."
//
//   - text/enum. An enum value IS text drawn from a closed set. It folds with the
//     same function (FR-011a), it sorts with the same lexical rule (R-5), and
//     refusing `text = enum` would make a filter break the moment an author
//     tightened a `text` property into an `enum` — a schema change that should
//     narrow what VALIDATES, not what COMPARES.
//   - integer/decimal. An author chooses the storage, not a distinct comparison
//     domain, so `3 = 3.0` is true. R-1 separates text from numbers, not int64
//     from arbitrary precision.
//
// The domain key is deliberately one of the member types rather than a synthetic
// name, so that operatorDefinedForType can stay keyed by declared type and a
// reader can look a row up by the name they already have.
func comparisonDomain(t PropertyType) PropertyType {
	switch {
	case t == TypeEnum:
		// An enum compares as the text it is.
		return TypeText
	case isNumericType(t):
		// `integer` and `decimal` share one field and one order (value.go's
		// TypedValue.Number); the declared type decides the BOUNDS, not the
		// comparison domain.
		return TypeDecimal
	}
	return t
}

// isOrderingOperator is R-13's subject: the four operators a list has no answer
// for. "Is this list greater than `vendor`?" has no answer in any vocabulary,
// which is the one place R-13's refusal survives ruling R-B.
func isOrderingOperator(op Operator) bool {
	switch op {
	case OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual:
		return true
	}
	return false
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

func manyPropertyName(left, right PropertyValue) string {
	if left.Property.Many {
		return left.Property.Name
	}
	return right.Property.Name
}

// arityRefusalDetail is R-13's message, quoted from the rule itself: "`segment`
// holds many values; ordering comparisons are not defined over a list — use
// `=`, `IN` or `LIKE`".
func arityRefusalDetail(op Operator, left, right PropertyValue) string {
	name := manyPropertyName(left, right)
	if name == "" {
		name = "the property"
	}
	return fmt.Sprintf("%q holds many values; ordering comparisons (%s) are not defined over a list — use =, IN or LIKE",
		name, op)
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

func operatorNotDefinedProblem(op Operator, pv PropertyValue) ComparisonProblem {
	return ComparisonProblem{
		Code:      CompareOperatorNotDefined,
		Operator:  op,
		Type:      pv.Property.Type,
		Property:  pv.Property.Name,
		Detail:    fmt.Sprintf("no rule in spec §8 defines operator %q for declared type %q", string(op), string(pv.Property.Type)),
		Supported: definedOperatorNames(pv.Property.Type),
		Remedy:    unsupportedRemedy(op),
	}
}

func unknownOperatorProblem(op Operator) ComparisonProblem {
	return ComparisonProblem{
		Code:      CompareUnknownOperator,
		Operator:  op,
		Detail:    fmt.Sprintf("%q is not one of the supported operators", string(op)),
		Supported: OperatorNames(),
		Remedy:    unsupportedRemedy(op),
	}
}

// evaluateElementwise is R-9, generalised to the four operators R-13 defines
// against a `many` property.
//
// R-9: "Against a `many` property, `=` matches an element exactly and `IN`
// matches any element of a list; `LIKE` matches an element by pattern."
// R-13: those operators "mean what R-9 says: element-wise, with the record
// matching if ANY element matches."
//
// A scalar operand is a one-element list here, so there is exactly ONE loop and
// the scalar case is not a separate code path that could drift from the list
// case. That matters more than it looks: the previous version had membership and
// scalar equality as two different notions of "equal", and the list one had to
// be written a second time for every declared type.
//
//   - An empty list is a VALUE and it contains nothing, so zero iterations means
//     false with nothing reported (R-3 and R-9 agree on this).
//   - `IN`'s right operand is the list of candidate values. It is therefore the
//     SAME operation as `=` at this layer, which is exactly what SQL's `IN` is;
//     the two differ in the shape of the value the FILTER accepts (a scalar
//     versus a non-empty array — FR-022d), not in what the comparator does.
//   - A pair the oracle could not compare is REPORTED and the whole comparison
//     is false. It is never skipped in the hope that a later element matches:
//     that would return a plausible answer computed over data we know is broken.
func (c Comparator) evaluateElementwise(op Operator, left, right PropertyValue) (bool, []ComparisonProblem) {
	if problems := c.declaredMembership(op, left, right); len(problems) > 0 {
		return false, problems
	}
	match := false
	for _, a := range left.Values {
		for _, b := range right.Values {
			ok, problems := c.compareElements(op, a, b, left, right)
			if len(problems) > 0 {
				return false, problems
			}
			if ok {
				match = true
			}
		}
	}
	return match, nil
}

// declaredMembership is R-5's conformance half: "Equality resolves a value
// case-insensitively to a declared value; a value resolving to none of them is a
// REPORTED PROBLEM."
//
// It applies to the ENUM side only, and that asymmetry is R-1's, stated
// verbatim: "A `text` value that is not a declared member of the `enum` still
// compares (it is a value, not a non-conformance on the text side); the enum
// side's own non-membership is R-4's business."
//
// A property that declares no value set at all is not checked, because there is
// nothing to check against. Under the pre-R-E design that hole was fatal — R-5
// ordered by DECLARED POSITION, so a missing set meant a missing ordinal and two
// values from unrelated vocabularies were silently ranked against each other.
// Ruling R-E deletes declared position entirely: order is lexical, it needs no
// set, and CompareEnumSetsDiffer is retired with the machinery it guarded.
func (c Comparator) declaredMembership(op Operator, left, right PropertyValue) []ComparisonProblem {
	var problems []ComparisonProblem
	for _, side := range []struct {
		name string
		pv   PropertyValue
	}{{"left", left}, {"right", right}} {
		if side.pv.Property.Type != TypeEnum || len(side.pv.Property.Values) == 0 {
			continue
		}
		for _, v := range side.pv.Values {
			if !enumDeclares(side.pv.Property, v.Enum.Name) {
				problems = append(problems, enumValueNotDeclaredProblem(op, side.name, v.Enum.Name, side.pv))
				break
			}
		}
	}
	return problems
}

// enumDeclares resolves a spelling to a declared value CASE-INSENSITIVELY
// (R-5, ruling R-D). Resolving `Won` TO `won` collapses two spellings into one
// value rather than creating a second, which is the thing ADR-068 D4 actually
// forbids.
func enumDeclares(p *Property, name string) bool {
	want := FoldKey(name)
	for _, v := range p.Values {
		if FoldKey(v.Name) == want {
			return true
		}
	}
	return false
}

func enumValueNotDeclaredProblem(op Operator, side, value string, pv PropertyValue) ComparisonProblem {
	return ComparisonProblem{
		Code:     CompareNonConforming,
		Operator: op,
		Type:     TypeEnum,
		Property: pv.Property.Name,
		Side:     side,
		Detail: fmt.Sprintf("%s operand's value %q is not one of the values %q declares (%s), even case-insensitively",
			side, value, pv.Property.Name, strings.Join(pv.Property.PermittedValues(), ", ")),
	}
}

// compareElements is the per-value-pair answer, dispatched on the comparison
// DOMAIN rather than the declared type, so `text` against `enum` and `integer`
// against `decimal` take the same branch their domain-mate does (R-1).
func (c Comparator) compareElements(op Operator, a, b TypedValue, la, rb PropertyValue) (bool, []ComparisonProblem) {
	switch comparisonDomain(la.Property.Type) {
	case TypeText:
		return textualAnswer(op, a, b), nil
	case TypeRelation, TypePerson:
		// R-8 — by target identity, never by display text. Only equality and
		// membership reach here; ordering was refused by the disposition.
		equal, problems := c.relationsEqual(op, a, b, la, rb)
		if len(problems) > 0 {
			return false, problems
		}
		return equalityAnswer(op, equal), nil
	case TypeDate:
		return orderingHolds(op, compareInstant(a, b)), nil // R-7
	case TypeInteger, TypeDecimal:
		// R-1 makes these ONE declared type for comparison, so one branch:
		// `3 = 3.0` is true and an integer compares with a decimal numerically.
		return orderingHolds(op, a.Number.Cmp(b.Number)), nil
	default:
		return false, []ComparisonProblem{{
			Code:      CompareOperatorNotDefined,
			Operator:  op,
			Type:      la.Property.Type,
			Property:  la.Property.Name,
			Detail:    "declared type has no comparison defined",
			Supported: OperatorNames(),
		}}
	}
}

// textualForm is the string an operand of the textual domain compares AS: the
// prose for `text`, the value's own spelling for `enum`. R-1 unifies the two, so
// there has to be exactly one function that says which string that is.
func textualForm(v TypedValue) string {
	if v.Type == TypeEnum {
		return v.Enum.Name
	}
	return v.Text
}

// textualAnswer is R-5's ordering half and R-10 in full.
//
//	R-10  On text, `=` is EXACT and `LIKE` is PATTERNED, `%` and `_` meaning
//	      what they mean in SQL — and BOTH are case-INSENSITIVE.
//	R-5   `enum` orders LEXICALLY (ruling R-E; declared position is withdrawn),
//	      and the sort key is the FOLDED form, not the raw bytes.
//
// The fold is FoldKey — `golang.org/x/text/cases.Fold()`, Unicode FULL case
// folding (FR-011a). `strings.ToLower` and `strings.EqualFold` are FORBIDDEN
// here and neither is a permitted "simplification": executed, `straße` against
// `STRASSE` is false under both, the two disagree on Greek final sigma, and
// `ToLower` collapses Turkish `İ` onto `i`, which is a WRONG MATCH rather than a
// missing one.
//
// ORDERING USES THE FOLDED KEY ALONE, WITH NO TIE-BREAK, and that is deliberate.
// R-5(d)'s raw-byte tie-break exists so that SORTING a result set is total and
// deterministic; applying it to the `<` OPERATOR would make `won = Won` true and
// `won < Won` true at the same time. The two consumers of the order are
// different: the operator asks "is this less?", the sort asks "which comes
// first?", and only the second needs to break a tie it is required to resolve.
// SortByEnumOrder carries the tie-break; this function must not.
func textualAnswer(op Operator, a, b TypedValue) bool {
	subject := FoldKey(textualForm(a))
	if op == OpLike {
		return likeMatch(subject, textualForm(b))
	}
	return orderingHolds(op, strings.Compare(subject, FoldKey(textualForm(b))))
}

// equalityAnswer maps an element-equality verdict onto the operators that are
// defined purely in terms of it — everything except the four ordering ones,
// which never reach here for a type whose disposition refuses them.
func equalityAnswer(op Operator, equal bool) bool {
	switch op {
	case OpEqual, OpIn:
		return equal
	case OpNotEqual:
		return !equal
	}
	return false
}

// relationsEqual is R-8: compare by target identity, never by display text.
//
// The identifier the resolver returns is compared BYTE-EXACTLY, with no folding
// applied, and that is a rule rather than an oversight (R-8, restated in
// revision 6): folding would make `CO-0142` and `co-0142` one key, and two
// legitimately distinct targets could then not coexist. The PATH/NAME side is
// where folding belongs, and it belongs to whoever implements the resolver —
// which also removes a real macOS-vs-Linux divergence in wikilink resolution.
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

// operatorDefinedForType is the disposition of each operator for each declared
// type. FR-022b's ten operators, seven declared types.
//
// AC-8.2: a change to this table is a SPECIFICATION change and must be argued as
// one. A change that only moves cells in the generated table is an
// implementation detail. compare_truthtable_test.go states this table a second
// time, independently, from the rules — so neither copy can drift quietly.
//
// IT IS ARITY-INDEPENDENT, which it was not before. It used to be documented as
// "for SCALAR operands", because `contains` was undefined for a scalar number and
// defined against a list of them. FR-022b has no such inversion, so this table is
// now a statement about the DECLARED TYPE alone and R-13 is applied separately,
// afterwards. TestComparison_DispositionIsArityIndependent holds that.
//
// `IS NULL` and `IS NOT NULL` never reach this table: R-3 preempts them in
// Evaluate, for every type and every arity.
//
// Authority, row by row:
//
//   - text — R-10, restated by ruling R-B/R-D: "On text, `=` is exact and `LIKE`
//     is patterned, `%` and `_` meaning what they mean in SQL — and both are
//     case-INSENSITIVE." Ordering is lexical, inherited from R-5 through R-1's
//     unification of `text` with `enum`: the two are ONE declared type for
//     comparison, and a type cannot both have and lack an order. *(This is the
//     one cell in the table that no rule states in so many words. It is
//     recorded here as a derived consequence rather than a judgement call: the
//     alternative — `enum < enum` defined while `text < text` is not — makes
//     `text < enum` unanswerable, and `sort` is a first-class `vault_find`
//     parameter over any property.)*
//   - enum — R-5 as reversed by ruling R-E: a closed set that orders LEXICALLY,
//     equality resolving case-insensitively to a declared value. Domain order is
//     expressed by prefixing the values (`1-lead`, `2-qualified`), which is
//     visible in the operator's own file and does what it appears to do.
//   - relation — R-8 gives equality by target identity. No rule gives a record
//     identity an ORDER, and none should: `CO-0002 > CO-0001` is an artefact of
//     the identifier scheme, not a fact about the records.
//   - person — ADR-068 D3 defines person as a relation, so it inherits R-8.
//   - date — R-7: compares as an instant, so ordering and equality both hold.
//     `LIKE` is NOT defined: SQL would reach it by coercing the date to text,
//     and this design coerces nothing (R-1).
//   - integer, decimal — R-1's own worked example and AC-8.3 ("3 > 2 is TRUE")
//     require the full ordering family, and R-1 makes the two ONE comparison
//     type, so their rows MUST be identical. `LIKE` is undefined for the same
//     reason as `date`. TestComparison_DomainMatesShareOneRow holds the
//     identity, so a one-sided edit cannot land.
//
// `IN` is defined wherever `=` is, and for the same reason: it IS `=` over a set.
var operatorDefinedForType = map[PropertyType]map[Operator]bool{
	TypeText: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: true, OpIn: true,
	},
	TypeEnum: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: true, OpIn: true,
	},
	TypeRelation: {
		OpEqual: true, OpNotEqual: true,
		OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false,
		OpLike: false, OpIn: true,
	},
	TypePerson: {
		OpEqual: true, OpNotEqual: true,
		OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false,
		OpLike: false, OpIn: true,
	},
	TypeDate: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: false, OpIn: true,
	},
	TypeInteger: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: false, OpIn: true,
	},
	TypeDecimal: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: false, OpIn: true,
	},
}

// definedOperatorNames lists, in FR-022b's declared order, the operators that
// WOULD have worked against this declared type. Every refusal carries it —
// FR-022c: "in every case the refusal lists the supported operators."
func definedOperatorNames(t PropertyType) []string {
	names := make([]string, 0, len(Operators))
	for _, op := range Operators {
		if op == OpIsNull || op == OpIsNotNull || operatorDefinedForType[t][op] {
			names = append(names, string(op))
		}
	}
	return names
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

// orderingHolds turns a three-valued comparison into the answer for one
// operator. `<>` is the negation of `=` HERE, over two present, conforming,
// same-domain values — which is the only place the two are complements. R-2
// already removed the absent case, where `<>` is false rather than true.
func orderingHolds(op Operator, cmp int) bool {
	switch op {
	case OpEqual, OpIn:
		return cmp == 0
	case OpNotEqual:
		return cmp != 0
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
