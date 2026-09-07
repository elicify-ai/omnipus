// Omnipus — spec §7 test 6 / §8: the comparison truth table.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// THE ORACLE
//
// Spec §8 states thirteen rules, R-1..R-13 (R-6 retired with the `money` type),
// and says: "The oracle is these rules. Every cell is generated from them, and
// the rules — not the cells — are what a human reviews."
//
// Everything below derives from those rules. Nothing below was obtained by
// running the comparator and recording what it did — that is the failure §8
// exists to prevent, and it is how `3 > 2` became false during research.
//
// THREE hand-written artifacts carry all of the judgement, and they are what a
// reviewer should read:
//
//  1. oracleDisposition — for each declared type, which operators the rules
//     DEFINE. It restates compare_oracle.go's operatorDefinedForType
//     independently, so a one-sided edit to either cannot land quietly
//     (TestComparison_DispositionMatchesSpec).
//  2. sweepFixtures — for each declared type in each of four sweeps, the two
//     values being related, chosen so the relationship is obvious from the
//     rule cited on the row.
//  3. oracleRelate — the relationship itself, hand-derived per sweep, stated
//     once for a whole comparison DOMAIN rather than once per type. That is
//     sound only because the fixtures put EQUAL values at the same position in
//     every type of a domain, which is not assumed: it is asserted by
//     TestOracleFixtures_DomainMatesAgree.
//
// From those three the table generates
// 4 sweeps x 38 operand states x 38 operand states x 10 operators = 57,760 cells
// and asserts the comparator matches each.
//
// ---------------------------------------------------------------------------
// WHAT CHANGED WHEN THE OPERATOR VOCABULARY BECAME SQL'S
//
// ADR-068 O-3 as amended / FR-022b / ruling R-B. The seven invented operators
// became ten of SQL's, and three structural things followed:
//
//	THE OPERATOR AXIS grew from 7 to 10, and `<>`, `LIKE`, `IN` and
//	`IS NOT NULL` are all new columns of the table rather than hand-written
//	cases beside it.
//
//	A FOURTH SWEEP appeared. `LIKE` is the only operator whose answer is not a
//	function of the ordering relation, so a table with only less/equal/greater
//	sweeps would have generated `LIKE` cells that never once exercised a
//	wildcard — every pattern would have been wildcard-free and `LIKE` would have
//	been indistinguishable from `=` in all 43,320 cells. sweepPattern puts
//	`'vendors' LIKE 'vendor%'` INSIDE the generated space, arity fan-out
//	included, so R-9's "LIKE matches an element by pattern" is a generated cell
//	and not a remembered one.
//
//	THE TYPE AXIS is still seven, but it is a different seven: `money` is gone
//	(ruling O-2) and `number` split into `integer` and `decimal`. R-1 makes
//	`text`/`enum` ONE comparison type and `integer`/`decimal` ONE, so the
//	cross-type cells that used to be an automatic R-1 false are now REAL
//	comparisons — `text = enum` and `3 = 3.0` are generated, not asserted by
//	hand.
//
// ---------------------------------------------------------------------------
// THE ARITY DIMENSION IS INSIDE THE TABLE, AND MUST STAY THERE
//
// It was not, once, and that was this design's weakest structural point: every
// operand was a scalar, so the whole arity path sat outside the generated space
// covered by three hand-written cases.
//
// So for each of the seven declared types there are five shapes — a scalar, an
// empty list, a one-element list, a two-element list and a list carrying a
// non-conforming element — plus absent (scalar), absent (list) and
// present-but-non-conforming: 38 operand states, on BOTH sides.
//
// The arity RULES are stated in oracleExpect from §8, never from what the code
// does, and R-13 now says something much narrower than it used to:
//
//	R-13  Against a `many` property, `=`, `<>`, `IN`, `LIKE`, `IS NULL` and
//	      `IS NOT NULL` are DEFINED — element-wise, the record matching if ANY
//	      element matches (R-9). Only the four ORDERING operators are refused,
//	      and the refusal names the remedy.
//
// R-13 is checked AFTER oracleDisposition, which is the reverse of the order the
// pre-SQL-vocabulary table used. That inversion is deliberate and is explained
// at Evaluate: the old order was forced by `contains` being undefined for a
// scalar number and defined against a list of them, and no operator in FR-022b
// has that shape.
// ---------------------------------------------------------------------------

// oracleDisposition: does a numbered rule DEFINE this operator for this declared
// type? Authority per row is given in compare_oracle.go's
// operatorDefinedForType; the two are stated separately on purpose and must
// agree.
//
// `IS NULL` and `IS NOT NULL` are absent from every row: R-3 preempts the
// disposition for every type and every arity, so a cell here would be dead.
var oracleDisposition = map[PropertyType]map[Operator]bool{
	// R-10, as restated by rulings R-B and R-D: "On text, `=` is exact and
	// `LIKE` is patterned, `%` and `_` meaning what they mean in SQL — and both
	// are case-INSENSITIVE." Ordering is lexical, inherited from R-5 through
	// R-1's unification of text with enum.
	TypeText: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: true, OpIn: true,
	},
	// R-5 as reversed by ruling R-E: a closed set ordering LEXICALLY. Declared
	// position is withdrawn; a domain order is written into the values
	// (`1-lead`, `2-qualified`).
	TypeEnum: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: true, OpIn: true,
	},
	// R-8: equality by target identity. No rule gives a record identity an
	// order, and none should — `CO-0002 > CO-0001` is a fact about the
	// identifier scheme, not about the records. `LIKE` would be a pattern match
	// against a spelling, which is exactly what R-8 forbids.
	TypeRelation: {
		OpEqual: true, OpNotEqual: true,
		OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false,
		OpLike: false, OpIn: true,
	},
	// ADR-068 D3: person IS a relation to a person record, so it inherits R-8.
	TypePerson: {
		OpEqual: true, OpNotEqual: true,
		OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false,
		OpLike: false, OpIn: true,
	},
	// R-17/FR-216 via FR-004c: `checkbox` compares by EQUALITY ONLY. Ordering is
	// refused naming the remedy — "is unchecked less than checked?" has no
	// answer that is a fact about the data rather than about SQLite's habit of
	// storing a boolean as 0 and 1. `LIKE` is undefined for `date`'s reason:
	// reaching it would mean coercing to text, and this design coerces nothing.
	TypeCheckbox: {
		OpEqual: true, OpNotEqual: true,
		OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false,
		OpLike: false, OpIn: true,
	},
	// R-7: a date compares as an instant. `LIKE` is not defined — SQL reaches it
	// by coercing the date to text, and this design coerces nothing (R-1).
	TypeDate: {
		OpEqual: true, OpNotEqual: true,
		OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true,
		OpLike: false, OpIn: true,
	},
	// R-1's worked example and AC-8.3 ("3 > 2 is TRUE") require the full family.
	// R-1 also makes integer and decimal ONE comparison type, so these two rows
	// MUST be identical — TestComparison_DomainMatesShareOneRow holds it.
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

// ---------------------------------------------------------------------------
// Fixtures. ADR-068 D0: the product ships NO record types, so every name below
// is fixture data invented for this test and means nothing to the product.
// ---------------------------------------------------------------------------

// stageValues is the fixture enum's declared set.
//
// Under the pre-R-E design this slice's ORDER was load-bearing — it was the
// ordering. Ruling R-E deletes declared position, so the order here means
// nothing and the fixture says so by declaring the values in an order that is
// NOT their sort order: lexically `lead < vendor% < vendors < won`, and they are
// declared `lead, won, vendors, vendor%`. A comparator that fell back to
// declared position would order `won` before `vendors` and fail.
//
// `vendor%` is a legal enum value — schema.go's finalize forbids only an empty
// value and two values that fold to one key — and it is here so that
// sweepPattern's `LIKE` pattern exists on the ENUM side too, not just on text.
// Without it, half the textual domain's LIKE cells would be untested.
var stageValues = []EnumValue{
	{Name: "lead"},
	{Name: "won"},
	{Name: "vendors"},
	{Name: "vendor"},
	{Name: "vendor%"},
}

// testProperty builds the fixture property as a plain STRUCT LITERAL: the
// unexported foldIndex cache is left nil, so ResolveEnum answers by scanning
// Values. That is the path a consumer of this package gets from a struct
// literal, and it is the path a defect has lived on before; the cached path is
// exercised by every test that goes through LoadSchemas or NewProperty.
func testProperty(name string, t PropertyType, many bool) *Property {
	p := &Property{Name: name, Type: t, Many: many, RecordType: "fixture"}
	if t == TypeEnum {
		p.Values = append([]EnumValue(nil), stageValues...)
	}
	return p
}

func tvText(s string) TypedValue { return TypedValue{Type: TypeText, Raw: s, Text: s} }

// tvEnum builds an enum value the way ParseValue does: the DECLARED spelling is
// what travels in Enum, and the file's own spelling stays in Raw.
func tvEnum(t *testing.T, name string) TypedValue {
	t.Helper()
	p := testProperty("stage", TypeEnum, false)
	v, ok := p.ResolveEnum(name)
	if !ok {
		t.Fatalf("test fixture: %q is not in the declared enum set %v", name, p.PermittedValues())
	}
	return TypedValue{Type: TypeEnum, Raw: name, Enum: v}
}

// tvBool builds a `checkbox` value the way parseCheckboxValue does: Raw keeps
// the file's own spelling, Bool carries the value.
func tvBool(b bool) TypedValue {
	raw := "false"
	if b {
		raw = "true"
	}
	return TypedValue{Type: TypeCheckbox, Raw: raw, Bool: b}
}

func tvLink(t PropertyType, target string) TypedValue {
	return TypedValue{Type: t, Raw: "[[" + target + "]]",
		Link: Wikilink{Target: target, Raw: "[[" + target + "]]"}}
}

func tvDate(t *testing.T, s string) TypedValue {
	t.Helper()
	if inst, err := time.Parse(time.RFC3339, s); err == nil {
		return TypedValue{Type: TypeDate, Raw: s, Date: DateValue{Instant: inst.UTC(), HasTime: true}}
	}
	inst, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("test fixture: %q is not a date: %v", s, err)
	}
	return TypedValue{Type: TypeDate, Raw: s, Date: DateValue{Instant: inst.UTC()}}
}

func tvNumber(t *testing.T, typ PropertyType, s string) TypedValue {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("test fixture: %q is not a number: %v", s, err)
	}
	return TypedValue{Type: typ, Raw: s, Number: d}
}

// fixtureRelations is the index's resolution of every wikilink this test uses.
// R-8 compares by the identity on the RIGHT of this map, never by the spelling
// on the left — which is why two spellings map to one identity here.
var fixtureRelations = map[string]string{
	"Acme Ltd":     "CO-0001",
	"acme ltd.":    "CO-0001",
	"Beta GmbH":    "CO-0002",
	"Ada Lovelace": "PE-0001",
	"A. Lovelace":  "PE-0001",
	"Grace Hopper": "PE-0002",
}

func testComparator() Comparator {
	return Comparator{ResolveRelation: func(link Wikilink) (string, bool) {
		id, ok := fixtureRelations[link.Target]
		return id, ok
	}}
}

// present builds a conforming scalar operand.
func present(p *Property, v TypedValue) PropertyValue {
	return PropertyValue{Property: p, State: StatePresent, Values: []TypedValue{v}}
}

// presentList builds a conforming list operand (D3.1 `many: true`).
func presentList(p *Property, vs ...TypedValue) PropertyValue {
	return PropertyValue{Property: p, State: StatePresent, Values: vs}
}

func absentOperand(p *Property) PropertyValue {
	return PropertyValue{Property: p, State: StateAbsent}
}

func nonConformingOperand(p *Property) PropertyValue {
	return PropertyValue{Property: p, State: StateNonConforming,
		Findings: []Finding{{Property: p.Name, Code: FindingWrongShape, Severity: SeverityError}}}
}

// ---------------------------------------------------------------------------
// The sweeps
// ---------------------------------------------------------------------------

type sweep string

const (
	sweepLess    sweep = "left<right"
	sweepEqual   sweep = "left==right"
	sweepGreater sweep = "left>right"
	// sweepPattern relates the same way sweepGreater does, and adds the one
	// thing no ordering sweep can carry: the RIGHT value is a `LIKE` PATTERN
	// that matches the left. Without it every generated `LIKE` cell would have a
	// wildcard-free pattern and `LIKE` would be a synonym for `=` throughout the
	// whole table.
	sweepPattern sweep = "left LIKE right-pattern"
)

func allSweeps() []sweep { return []sweep{sweepLess, sweepEqual, sweepGreater, sweepPattern} }

// valuePos names which of a sweep's two fixture values is meant. It is the axis
// oracleRelate is stated over, and it is what makes a two-element list's
// expected answer derivable rather than looked up.
type valuePos int

const (
	posLeft valuePos = iota
	posRight
)

// sweepFixtures is hand-derived artifact (2): the two values each declared type
// relates in each sweep, with the rule the choice answers to.
//
// THE INVARIANT THAT MAKES CROSS-TYPE CELLS SOUND. R-1 makes `text`/`enum` ONE
// comparison type and `integer`/`decimal` ONE, so the table generates real
// comparisons between them. For oracleRelate to be stateable per DOMAIN rather
// than per type pair, the domain's member types must hold EQUAL values at the
// same position — `text` left and `enum` left must be the same value, and
// `integer` left and `decimal` left must be numerically equal.
//
// That is not assumed. TestOracleFixtures_DomainMatesAgree asserts it, and if a
// later edit breaks it the oracle fails loudly instead of generating cells whose
// expected values are quietly wrong.
func sweepFixtures(t *testing.T, sw sweep, typ PropertyType, pos valuePos) TypedValue {
	t.Helper()
	left := pos == posLeft
	switch sw {
	case sweepLess:
		switch typ {
		case TypeText: // R-10: folded lexical, "lead" < "won"
			return pick(left, tvText("lead"), tvText("won"))
		case TypeEnum: // R-5 (ruling R-E): lexical, NOT declared position
			return pick(left, tvEnum(t, "lead"), tvEnum(t, "won"))
		case TypeRelation: // R-8: CO-0001 and CO-0002 are different records
			return pick(left, tvLink(TypeRelation, "Acme Ltd"), tvLink(TypeRelation, "Beta GmbH"))
		case TypePerson: // R-8 via ADR-068 D3
			return pick(left, tvLink(TypePerson, "Ada Lovelace"), tvLink(TypePerson, "Grace Hopper"))
		case TypeDate: // R-7: an instant precedes an instant
			return pick(left, tvDate(t, "2026-01-01"), tvDate(t, "2026-06-01"))
		case TypeInteger: // R-1's worked example
			return pick(left, tvNumber(t, TypeInteger, "2"), tvNumber(t, TypeInteger, "3"))
		case TypeDecimal: // R-1: equal in value to the integer row, different in storage
			return pick(left, tvNumber(t, TypeDecimal, "2.0"), tvNumber(t, TypeDecimal, "3.0"))
		case TypeCheckbox:
			// R-17: equality only, so what this sweep has to supply is a pair
			// that is UNEQUAL — the `less` half of the relation is never
			// consulted, because the disposition refuses `<` before any value
			// is looked at. false/true is the unequal pair.
			return pick(left, tvBool(false), tvBool(true))
		}
	case sweepEqual:
		switch typ {
		case TypeText:
			// R-10/FR-011a: equality is case-INSENSITIVE. The two spellings
			// differ, so a comparator that folds nothing fails this whole sweep
			// rather than one hand-written case.
			return pick(left, tvText("won"), tvText("WON"))
		case TypeEnum:
			return pick(left, tvEnum(t, "won"), tvEnum(t, "won"))
		case TypeRelation:
			// R-8: two spellings of one target, differing in case AND
			// punctuation, are ONE record.
			return pick(left, tvLink(TypeRelation, "Acme Ltd"), tvLink(TypeRelation, "acme ltd."))
		case TypePerson:
			return pick(left, tvLink(TypePerson, "Ada Lovelace"), tvLink(TypePerson, "A. Lovelace"))
		case TypeDate:
			// R-7: a bare day and midnight UTC are the same instant.
			return pick(left, tvDate(t, "2026-01-01"), tvDate(t, "2026-01-01T00:00:00Z"))
		case TypeInteger:
			return pick(left, tvNumber(t, TypeInteger, "3"), tvNumber(t, TypeInteger, "3"))
		case TypeDecimal:
			// FR-013/FR-020b: equal across declared scales, exactly, no float.
			return pick(left, tvNumber(t, TypeDecimal, "3.0"), tvNumber(t, TypeDecimal, "3.000"))
		case TypeCheckbox:
			return pick(left, tvBool(true), tvBool(true))
		}
	case sweepGreater:
		switch typ {
		case TypeText:
			// "vendors" against "vendor", and the pair is chosen for LIKE, not
			// for the ordering. FR-022b: `LIKE` is ANCHORED, so
			// `'vendors' LIKE 'vendor'` is FALSE — while a SUBSTRING
			// implementation, which is what an operator that replaced
			// `contains` most plausibly degrades into, answers TRUE.
			//
			// Without this pair the anchoring rule is INVISIBLE to the generated
			// table: every other sweep's wildcard-free pattern is either equal
			// to the subject or shares no prefix with it, so substring and
			// anchored agree on all of them. Verified by mutation — replacing
			// likeMatch's wildcard-free branch with strings.Contains failed five
			// hand-written tests and ZERO generated cells until this row
			// existed. Lexically "vendors" > "vendor", so the ordering half of
			// the sweep is unaffected.
			return pick(left, tvText("vendors"), tvText("vendor"))
		case TypeEnum:
			return pick(left, tvEnum(t, "vendors"), tvEnum(t, "vendor"))
		case TypeRelation:
			return pick(left, tvLink(TypeRelation, "Beta GmbH"), tvLink(TypeRelation, "Acme Ltd"))
		case TypePerson:
			return pick(left, tvLink(TypePerson, "Grace Hopper"), tvLink(TypePerson, "Ada Lovelace"))
		case TypeDate:
			return pick(left, tvDate(t, "2026-06-01"), tvDate(t, "2026-01-01"))
		case TypeInteger: // AC-8.3 lives here: 3 > 2 is TRUE
			return pick(left, tvNumber(t, TypeInteger, "3"), tvNumber(t, TypeInteger, "2"))
		case TypeDecimal:
			return pick(left, tvNumber(t, TypeDecimal, "3.0"), tvNumber(t, TypeDecimal, "2.0"))
		case TypeCheckbox:
			return pick(left, tvBool(true), tvBool(false))
		}
	case sweepPattern:
		switch typ {
		case TypeText:
			// FR-022b: `'vendors' LIKE 'vendor%'` is TRUE and
			// `'vendors' LIKE 'vendor'` is FALSE — the match is ANCHORED. The
			// ordering is the ordinary lexical one: '%' is 0x25 and 's' is
			// 0x73, so "vendors" > "vendor%".
			return pick(left, tvText("vendors"), tvText("vendor%"))
		case TypeEnum:
			return pick(left, tvEnum(t, "vendors"), tvEnum(t, "vendor%"))
		case TypeRelation:
			return pick(left, tvLink(TypeRelation, "Beta GmbH"), tvLink(TypeRelation, "Acme Ltd"))
		case TypePerson:
			return pick(left, tvLink(TypePerson, "Grace Hopper"), tvLink(TypePerson, "Ada Lovelace"))
		case TypeDate:
			return pick(left, tvDate(t, "2026-06-01"), tvDate(t, "2026-01-01"))
		case TypeInteger:
			return pick(left, tvNumber(t, TypeInteger, "3"), tvNumber(t, TypeInteger, "2"))
		case TypeDecimal:
			return pick(left, tvNumber(t, TypeDecimal, "3.0"), tvNumber(t, TypeDecimal, "2.0"))
		case TypeCheckbox:
			// `LIKE` is undefined for a checkbox, so the pattern half of this
			// sweep is never reached; the pair only has to stay UNEQUAL so the
			// equality operators keep their meaning in this sweep.
			return pick(left, tvBool(true), tvBool(false))
		}
	}
	t.Fatalf("test fixture: no sweep value for %s/%s", sw, typ)
	return TypedValue{}
}

func pick(left bool, a, b TypedValue) TypedValue {
	if left {
		return a
	}
	return b
}

// oracleRelation is hand-derived artifact (3): the relationship between two of a
// sweep's fixture values, in the four terms every operator's answer is a
// function of.
type oracleRelation struct {
	equal   bool
	less    bool
	greater bool
	// like is `a LIKE b` — b read as a PATTERN. It is only consulted for the
	// textual domain, because no other type's disposition defines `LIKE`.
	like bool
}

// oracleRelate states the relationship ONCE PER SWEEP, for a whole comparison
// domain. Every clause is hand-derived from the fixture values above; none is
// read off the comparator.
func oracleRelate(sw sweep, a, b valuePos) oracleRelation {
	if a == b {
		// The same fixture value on both sides. Equality is reflexive for every
		// declared type — same folded text, the same resolved record identity,
		// the same instant, an equal decimal — and a wildcard-free pattern is an
		// exact match while `vendor%` matches itself (`vendor` then `%` eats the
		// trailing `%`).
		return oracleRelation{equal: true, like: true}
	}
	forward := a == posLeft // the left fixture value against the right one
	switch sw {
	case sweepEqual:
		return oracleRelation{equal: true, like: true}
	case sweepLess:
		// "lead" against "won": neither is a pattern for the other.
		return oracleRelation{less: forward, greater: !forward}
	case sweepGreater:
		// "won" against "lead": likewise.
		return oracleRelation{greater: forward, less: !forward}
	case sweepPattern:
		// "vendors" against "vendor%": greater lexically, and it MATCHES the
		// pattern. Backwards, "vendor%" against the literal pattern "vendors",
		// it does not: `LIKE` is anchored and there is no wildcard on that side.
		return oracleRelation{greater: forward, less: !forward, like: forward}
	}
	panic("oracle: unknown sweep")
}

// oracleOperatorAnswer maps a relationship onto one operator's answer. This is
// the definition of the operators themselves, in the vocabulary FR-022b adopts:
// SQL's, meaning what SQL means.
func oracleOperatorAnswer(op Operator, rel oracleRelation) bool {
	switch op {
	case OpEqual:
		return rel.equal
	case OpNotEqual:
		return !rel.equal
	case OpIn:
		// `IN` is `=` over a set. At the level of ONE pair of values it is
		// exactly equality; the set is expressed by the right operand carrying
		// more than one value, which the loop in oracleExpect walks.
		return rel.equal
	case OpLess:
		return rel.less
	case OpLessOrEqual:
		return rel.less || rel.equal
	case OpGreater:
		return rel.greater
	case OpGreaterOrEqual:
		return rel.greater || rel.equal
	case OpLike:
		return rel.like
	}
	panic("oracle: operator not covered — the operator set changed without the oracle")
}

// listShape is which of the sweep's two fixture values a PRESENT operand
// carries, and how many.
type listShape int

const (
	// shapeOne — a scalar property holding this side's own sweep value. The
	// only shape a `many: false` property can have.
	shapeOne listShape = iota
	// shapeEmptyList — `many: true` holding zero values. D3.1/R-3: an empty
	// list is a VALUE (StatePresent), not absence, and it contains nothing.
	shapeEmptyList
	// shapeOneList — `many: true` holding this side's own sweep value.
	shapeOneList
	// shapeTwoList — `many: true` holding BOTH sweep values. This is the
	// "found, and not only at index 0" case, and it is also what makes R-9's
	// ANY-element rule observable: the list holds a value the other side
	// matches and one it does not.
	shapeTwoList
	// shapeDirtyList — `many: true`, one element parsed and one did not.
	// ResolveProperty produces exactly this: State becomes NonConforming while
	// the conforming values are still accumulated in Values (validate.go's
	// element loop). R-4 governs it, and the fact that Values is non-empty must
	// not tempt anything into comparing them anyway.
	shapeDirtyList
)

var listShapeNames = map[listShape]string{
	shapeOne:       "",
	shapeEmptyList: "_list_empty",
	shapeOneList:   "_list_one",
	shapeTwoList:   "_list_two",
	shapeDirtyList: "_list_nonconforming",
}

// operandState is one row/column of the table: the seven declared types in each
// of five arity shapes, plus absent (scalar), absent (list), plus
// present-but-non-conforming. AC-8.1 requires the whole set on both sides.
type operandState struct {
	name    string
	typ     PropertyType
	many    bool
	shape   listShape
	absent  bool
	nonConf bool
}

func operandStates() []operandState {
	states := make([]operandState, 0, 38)
	for _, t := range PropertyTypes {
		for _, sh := range []listShape{shapeOne, shapeEmptyList, shapeOneList, shapeTwoList, shapeDirtyList} {
			states = append(states, operandState{
				name:    string(t) + listShapeNames[sh],
				typ:     t,
				many:    sh != shapeOne,
				shape:   sh,
				nonConf: sh == shapeDirtyList,
			})
		}
	}
	// The absent and non-conforming carriers must declare SOME type, because a
	// property always has one. R-2/R-3/R-4 preempt R-1's type check, so the
	// carrier cannot change any expected value —
	// TestComparison_AbsentAndNonConformingAreTypeIndependent proves that across
	// all seven carriers rather than assuming it.
	//
	// `absent_list` is here because absence and arity are independent: a
	// declared `many` property with no key at all is absent, not an empty list,
	// and R-2/R-3 must reach it before R-13 does.
	states = append(states,
		operandState{name: "absent", typ: TypeInteger, absent: true},
		operandState{name: "absent_list", typ: TypeInteger, many: true, absent: true},
		operandState{name: "non_conforming", typ: TypeInteger, nonConf: true},
	)
	return states
}

func operandFor(t *testing.T, s operandState, sw sweep, side valuePos) PropertyValue {
	t.Helper()
	p := testProperty("fixture_"+string(s.typ), s.typ, s.many)
	if s.absent {
		return absentOperand(p)
	}
	mine := sweepFixtures(t, sw, s.typ, side)
	switch s.shape {
	case shapeEmptyList:
		return presentList(p)
	case shapeOneList:
		return presentList(p, mine)
	case shapeTwoList:
		return presentList(p,
			sweepFixtures(t, sw, s.typ, posLeft),
			sweepFixtures(t, sw, s.typ, posRight))
	case shapeDirtyList:
		// The shape ResolveProperty really produces for a list with one bad
		// element: non-conforming, findings attached, conforming values kept.
		pv := presentList(p, mine)
		pv.State = StateNonConforming
		pv.Findings = []Finding{{Property: p.Name, Code: FindingWrongShape, Severity: SeverityError, ElementIndex: 1}}
		return pv
	}
	if s.nonConf {
		return nonConformingOperand(p)
	}
	return present(p, mine)
}

// oraclePositions is which sweep values an operand carries, stated from the
// shape rather than read off operandFor, so the two are independent.
func oraclePositions(s operandState, side valuePos) []valuePos {
	switch s.shape {
	case shapeEmptyList:
		return nil // R-9: an empty list contains nothing.
	case shapeTwoList:
		return []valuePos{posLeft, posRight}
	}
	return []valuePos{side}
}

// oracleExpect returns the expected boolean and the expected multiset of problem
// codes for one cell, computed ONLY from R-1..R-13 and the three hand-written
// artifacts. The ladder is the rules' precedence; each step cites its rule.
func oracleExpect(op Operator, l, r operandState, sw sweep) (bool, []ComparisonProblemCode) {
	// R-3 — `IS NULL` is true exactly when the property has no value, and false
	// otherwise; `IS NOT NULL` is its complement. Both ask about ONE property,
	// so only the left is consulted, and both are exempt from R-2.
	switch op {
	case OpIsNull:
		return l.absent, nil
	case OpIsNotNull:
		return !l.absent, nil
	}

	// R-2 — either side absent: false for EVERY remaining operator, `<>`
	// INCLUDED. Reported as nothing, because absence is a state and not a
	// defect. `<>` is not an exception, ruled explicitly (§8 R-2, C-7): in SQL
	// `x <> 'v'` over a NULL `x` excludes the row, and adopting SQL's names
	// without SQL's semantics is what ruling R-B forbids.
	if l.absent || r.absent {
		return false, nil
	}

	// R-4 — a present, non-conforming value is false for every operator AND the
	// record is added to the problem list. One entry per offending operand.
	if l.nonConf || r.nonConf {
		var codes []ComparisonProblemCode
		if l.nonConf {
			codes = append(codes, CompareNonConforming)
		}
		if r.nonConf {
			codes = append(codes, CompareNonConforming)
		}
		return false, codes
	}

	// R-1 — different declared types: false. Never an error, never a coercion,
	// and not a reported problem. TWO PAIRS ARE ONE TYPE: text/enum and
	// integer/decimal. Arity is not part of the declared type, so a `many text`
	// and a scalar text fall through to R-13 rather than being an R-1 false.
	if oracleDomain(l.typ) != oracleDomain(r.typ) {
		return false, nil
	}

	// The type disposition. An operator no rule defines for this declared type
	// is false AND reported — §3's behavioural contract makes the silence the
	// defect.
	if !oracleDisposition[l.typ][op] {
		return false, []ComparisonProblemCode{CompareOperatorNotDefined}
	}

	// R-13 — against a `many` property only the four ORDERING operators are
	// refused. `=`, `<>`, `IN` and `LIKE` are defined and fall through to the
	// element-wise rule below.
	if (l.many || r.many) && isOrderingOperator(op) {
		return false, []ComparisonProblemCode{CompareArityNotDefined}
	}

	// R-9 — element-wise, the record matching if ANY element matches. A scalar
	// is a one-element list here, so this is the only branch and the scalar case
	// cannot drift from the list case.
	match := false
	for _, a := range oraclePositions(l, posLeft) {
		for _, b := range oraclePositions(r, posRight) {
			if oracleOperatorAnswer(op, oracleRelate(sw, a, b)) {
				match = true
			}
		}
	}
	return match, nil
}

// oracleDomain is R-1's unification, restated independently of
// comparisonDomain so a one-sided edit cannot land quietly.
func oracleDomain(t PropertyType) PropertyType {
	switch t {
	case TypeEnum:
		return TypeText
	case TypeInteger:
		return TypeDecimal
	}
	return t
}

func problemCodes(problems []ComparisonProblem) []ComparisonProblemCode {
	codes := make([]ComparisonProblemCode, 0, len(problems))
	for _, p := range problems {
		codes = append(codes, p.Code)
	}
	return codes
}

func codesEqual(a, b []ComparisonProblemCode) bool {
	if len(a) != len(b) {
		return false
	}
	counts := map[ComparisonProblemCode]int{}
	for _, c := range a {
		counts[c]++
	}
	for _, c := range b {
		counts[c]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// TestOracleFixtures_DomainMatesAgree asserts the premise oracleRelate rests on:
// within one comparison domain, every declared type holds the SAME value at the
// same sweep position.
//
// Without this the cross-type cells R-1's unification created — `text` against
// `enum`, `integer` against `decimal` — would be generated with expected values
// derived from a relationship that does not hold between the actual fixtures,
// and the table would be confidently wrong in about a fifth of its cells.
//
// It is a check on the FIXTURES, not on the comparator: it compares the fixture
// strings and parsed decimals directly, and would still pass if Evaluate were
// deleted.
func TestOracleFixtures_DomainMatesAgree(t *testing.T) {
	for _, sw := range allSweeps() {
		for _, pos := range []valuePos{posLeft, posRight} {
			text := sweepFixtures(t, sw, TypeText, pos)
			enum := sweepFixtures(t, sw, TypeEnum, pos)
			if FoldKey(text.Text) != FoldKey(enum.Enum.Name) {
				t.Errorf("[%s pos=%d] text fixture %q and enum fixture %q are not the same value; R-1 makes them ONE comparison type and oracleRelate assumes it",
					sw, pos, text.Text, enum.Enum.Name)
			}
			integer := sweepFixtures(t, sw, TypeInteger, pos)
			decimal := sweepFixtures(t, sw, TypeDecimal, pos)
			if integer.Number.Cmp(decimal.Number) != 0 {
				t.Errorf("[%s pos=%d] integer fixture %s and decimal fixture %s are not numerically equal; R-1 makes them ONE comparison type and oracleRelate assumes it",
					sw, pos, integer.Number, decimal.Number)
			}
		}
	}
}

// TestComparisonTruthTable is spec §7 test 6 and §8's first-class deliverable.
//
// AC-8.1: every declared type x every declared type x every operator, in every
// arity shape on both sides, plus absent and non-conforming on both sides, with
// every expected value traced to a numbered rule through oracleExpect and the
// three hand-written artifacts above.
func TestComparisonTruthTable(t *testing.T) {
	c := testComparator()
	states := operandStates()
	sweeps := allSweeps()

	cells, multiValue := 0, 0
	answered := map[Operator][2]int{} // per operator: [false-count, true-count]
	arityRefusals, crossType := 0, 0

	for _, sw := range sweeps {
		for _, l := range states {
			for _, r := range states {
				lv := operandFor(t, l, sw, posLeft)
				rv := operandFor(t, r, sw, posRight)
				for _, op := range Operators {
					cells++
					if l.many || r.many {
						multiValue++
					}
					if oracleDomain(l.typ) != oracleDomain(r.typ) {
						crossType++
					}

					wantResult, wantCodes := oracleExpect(op, l, r, sw)
					gotResult, gotProblems := c.Evaluate(op, lv, rv)

					// Count how often each operator answered each way over
					// well-formed operands. An operator that never answers true
					// — or never false — is indistinguishable from a constant,
					// and would satisfy most of this table silently.
					// R-3 preempts everything for the unary two, so they are
					// counted over the WHOLE space rather than over the
					// disposition-gated part of it. Excluding them logged
					// "answered true in 0 cells", which reads as untested and
					// is not: their value is asserted in every one of the
					// 5,776 cells they appear in.
					counted := op == OpIsNull || op == OpIsNotNull ||
						(!l.absent && !r.absent && !l.nonConf && !r.nonConf &&
							oracleDomain(l.typ) == oracleDomain(r.typ) && oracleDisposition[l.typ][op] &&
							!((l.many || r.many) && isOrderingOperator(op)))
					if counted {
						n := answered[op]
						if wantResult {
							n[1]++
						} else {
							n[0]++
						}
						answered[op] = n
					}
					for _, code := range wantCodes {
						if code == CompareArityNotDefined {
							arityRefusals++
						}
					}

					if gotResult != wantResult {
						t.Errorf("[%s] %s %s %s = %v, want %v (oracle: the R-1..R-13 ladder in oracleExpect)",
							sw, l.name, op, r.name, gotResult, wantResult)
					}
					if !codesEqual(problemCodes(gotProblems), wantCodes) {
						t.Errorf("[%s] %s %s %s problems = %v, want %v",
							sw, l.name, op, r.name, problemCodes(gotProblems), wantCodes)
					}
				}
			}
		}
	}

	wantCells := len(sweeps) * len(states) * len(states) * len(Operators)
	if cells != wantCells {
		t.Fatalf("generated %d cells, want %d", cells, wantCells)
	}
	// AC-8.1's shape guard: adding a declared type, an arity shape, a sweep or
	// an operator without extending the table changes this number, loudly.
	//
	// IT IS HARDCODED ON PURPOSE. Deriving it from len(PropertyTypes) would make
	// it agree with any type set automatically, which is the one thing a
	// tripwire must not do — the point is that an eighth type cannot be added
	// without a human editing this line and the arithmetic beside it.
	//
	// 43 = 8 declared types x 5 arity shapes (scalar, empty list, one-element
	// list, two-element list, list with a non-conforming element) + absent
	// scalar + absent list + non-conforming scalar.
	//
	// It was 38 until Draft 11: FR-004c's `checkbox` is the eighth type, so
	// 7x5+3 = 38 became 8x5+3 = 43.
	if wantCells != 4*43*43*10 {
		t.Fatalf("table shape changed: %d cells; AC-8.1 requires 4 sweeps x 43 operand states x 43 x every one of the ten operators", wantCells)
	}
	// The arity dimension used to be entirely OUTSIDE this table — every cell
	// was scalar-against-scalar. These guards make that regression impossible to
	// reintroduce by quietly shrinking operandStates back to the scalar-only set.
	//
	// 10 = the states whose property is NOT `many`: one scalar per declared
	// type (8), plus the absent scalar and the non-conforming scalar. It was 9
	// before `checkbox`.
	if wantMulti := 4 * (43*43 - 10*10) * 10; multiValue != wantMulti {
		t.Fatalf("multi-value cells = %d, want %d (every pairing where either side is a `many` property)",
			multiValue, wantMulti)
	}
	// R-1's unification made cross-type cells REAL comparisons rather than an
	// automatic false. If this drops to the point where text-vs-enum is not
	// generated, the unification is untested.
	if crossType == 0 {
		t.Fatalf("the table generated ZERO cross-type cells; R-1's unification is outside it")
	}
	if arityRefusals == 0 {
		t.Fatalf("R-13 refused nothing in %d cells; the arity rule is unexercised", cells)
	}
	// Every operator must answer BOTH ways somewhere in the well-formed space,
	// or it is indistinguishable from a constant.
	for _, op := range Operators {
		n := answered[op]
		if n[0] == 0 || n[1] == 0 {
			t.Errorf("operator %s answered false in %d cells and true in %d; both must be non-zero or the table proves nothing about it",
				op, n[0], n[1])
		}
	}
	t.Logf("truth table: %d cells, %d multi-value (%d scalar-only), %d cross-type, %d R-13 refusals",
		cells, multiValue, cells-multiValue, crossType, arityRefusals)
	for _, op := range Operators {
		n := answered[op]
		t.Logf("  %-11s answered true in %6d cells, false in %6d", op, n[1], n[0])
	}
}

// TestComparison_ThreeGreaterThanTwo is AC-8.3, asserted by name because it is
// the case that actually failed during research: an overload accepting `any`
// made 3 > 2 evaluate to false with nothing reporting an error.
func TestComparison_ThreeGreaterThanTwo(t *testing.T) {
	c := testComparator()
	p := testProperty("amount", TypeInteger, false)
	three := present(p, tvNumber(t, TypeInteger, "3"))
	two := present(p, tvNumber(t, TypeInteger, "2"))

	got, problems := c.Evaluate(OpGreater, three, two)
	if !got {
		t.Fatalf("AC-8.3 VIOLATED: 3 > 2 = false, want true")
	}
	if len(problems) != 0 {
		t.Fatalf("AC-8.3: 3 > 2 reported %v, want no problems", problemCodes(problems))
	}
	// The converses, so a comparator returning true unconditionally cannot pass.
	if got, _ := c.Evaluate(OpGreater, two, three); got {
		t.Fatalf("2 > 3 = true, want false")
	}
	if got, _ := c.Evaluate(OpLess, three, two); got {
		t.Fatalf("3 < 2 = true, want false")
	}
	// And the same through filter.go's Compare, which the query path uses.
	if cmp, ok := Compare(tvNumber(t, TypeInteger, "3"), tvNumber(t, TypeInteger, "2")); !ok || cmp <= 0 {
		t.Fatalf("AC-8.3 VIOLATED in filter.go Compare: cmp=%d ok=%v, want cmp>0", cmp, ok)
	}
}

// TestComparison_DispositionMatchesSpec asserts the production disposition table
// and the oracle's independently-written one agree. AC-8.2: an edit to either is
// a specification change and must be argued as one; this makes a one-sided edit
// impossible to land quietly.
func TestComparison_DispositionMatchesSpec(t *testing.T) {
	for _, typ := range PropertyTypes {
		for _, op := range Operators {
			if op == OpIsNull || op == OpIsNotNull {
				continue // R-3 preempts the table entirely.
			}
			want := oracleDisposition[typ][op]
			got := operatorDefinedForType[typ][op]
			if got != want {
				t.Errorf("disposition drift for %s/%s: implementation says %v, the spec-side oracle says %v",
					typ, op, got, want)
			}
		}
	}
	// Every declared type must have a row, or a type added later silently
	// resolves to "nothing is defined" and every query against it returns an
	// empty answer with one problem per record.
	for _, typ := range PropertyTypes {
		if len(operatorDefinedForType[typ]) == 0 {
			t.Errorf("declared type %s has no disposition row", typ)
		}
	}
}

// TestComparison_DomainMatesShareOneRow is R-1 applied to the disposition table
// itself: types that are ONE comparison type must define exactly the same
// operators, or `text = enum` would be defined in one direction and refused in
// the other depending on which side the comparator happened to look up.
func TestComparison_DomainMatesShareOneRow(t *testing.T) {
	for _, pair := range [][2]PropertyType{{TypeText, TypeEnum}, {TypeInteger, TypeDecimal}} {
		for _, op := range Operators {
			a := operatorDefinedForType[pair[0]][op]
			b := operatorDefinedForType[pair[1]][op]
			if a != b {
				t.Errorf("R-1: %s and %s are ONE comparison type but disagree on %s (%v vs %v)",
					pair[0], pair[1], op, a, b)
			}
		}
	}
}

// TestComparison_DispositionIsArityIndependent holds the precedence change the
// SQL vocabulary allowed: the disposition table is a statement about the
// DECLARED TYPE, and R-13 is applied separately afterwards.
//
// Concretely: an element-wise operator defined for a type's SCALAR must not be
// refused as "not defined for this type" when the same type is `many`. If one
// ever is, the two rules have been fused back together and the refusal message a
// caller sees will name the wrong problem.
func TestComparison_DispositionIsArityIndependent(t *testing.T) {
	c := testComparator()
	for _, typ := range PropertyTypes {
		for _, op := range []Operator{OpEqual, OpNotEqual, OpLike, OpIn} {
			if !operatorDefinedForType[typ][op] {
				continue
			}
			list := testProperty("fixture", typ, true)
			scalar := testProperty("fixture", typ, false)
			v := sweepFixtures(t, sweepEqual, typ, posLeft)
			_, problems := c.Evaluate(op, presentList(list, v), present(scalar, v))
			for _, p := range problems {
				if p.Code == CompareOperatorNotDefined {
					t.Errorf("%s %s against a `many` property was refused as not defined for the TYPE; the disposition must be arity-independent and R-13 applied separately",
						typ, op)
				}
			}
		}
	}
}

// TestComparison_R1_DifferentDeclaredTypes is R-1, with the rule's own worked
// example and both of its unifications.
func TestComparison_R1_DifferentDeclaredTypes(t *testing.T) {
	c := testComparator()
	textThree := present(testProperty("label", TypeText, false), tvText("3"))
	numberTwo := present(testProperty("amount", TypeInteger, false), tvNumber(t, TypeInteger, "2"))

	crossGot, crossProblems := c.Evaluate(OpGreater, textThree, numberTwo)
	if crossGot {
		t.Errorf(`R-1: "3" > 2 = true, want false`)
	}
	if len(crossProblems) != 0 {
		t.Errorf("R-1: a cross-type comparison is an ordinary false, never an error; got %v", problemCodes(crossProblems))
	}
	// A person and a relation are different declared types even though both hold
	// a wikilink. R-1 unifies text/enum and integer/decimal and NOTHING else.
	pv := present(testProperty("owner", TypePerson, false), tvLink(TypePerson, "Ada Lovelace"))
	rv := present(testProperty("company", TypeRelation, false), tvLink(TypeRelation, "Ada Lovelace"))
	if personRelation, _ := c.Evaluate(OpEqual, pv, rv); personRelation {
		t.Errorf("R-1: person and relation are different declared types and must not compare equal")
	}

	// UNIFICATION 1 — text and enum are ONE declared type.
	//
	// THIS LOOKS LIKE IT CONTRADICTS R-1 AND IT DOES NOT, so the rule is quoted
	// rather than paraphrased. R-1's SECOND sentence is the famous one — "a
	// comparison between values of different declared types is false, never an
	// error, never a coercion" — and read alone it says an enum and a text must
	// not compare. Its FIRST sentence, added in revision 6 (review round 6,
	// unasked question 2), is the carve-out, and it is not an inference:
	//
	//	"`text` and `enum` are ONE declared type for comparison purposes, and
	//	 `integer` and `decimal` are ONE; every other pair is different."
	//
	// with the ground given in the same row: "an `enum` value IS text that
	// happens to be drawn from a closed set, it folds with the same function
	// (FR-011a), it sorts with the same lexical rule (R-5), and refusing
	// `text = enum` would make a filter break the moment an author tightened a
	// `text` property into an `enum` — a schema change that should narrow what
	// VALIDATES, not what COMPARES."
	//
	// So this is not folding winning over type separation. The two values are
	// not of different declared types for comparison purposes; R-1 says so
	// first, and then says what happens to pairs that ARE different.
	enumProp := testProperty("stage", TypeEnum, false)
	textProp := testProperty("stage_text", TypeText, false)
	if unifiedGot, unifiedProblems := c.Evaluate(OpEqual, present(enumProp, tvEnum(t, "won")), present(textProp, tvText("won"))); !unifiedGot || len(unifiedProblems) != 0 {
		t.Errorf("R-1: enum `won` = text `won` gave %v/%v, want true and no problems", unifiedGot, problemCodes(unifiedProblems))
	}
	// And it folds across the unification, not only within a type.
	if foldedGot, _ := c.Evaluate(OpEqual, present(enumProp, tvEnum(t, "won")), present(textProp, tvText("WON"))); !foldedGot {
		t.Errorf("R-1 + FR-011a: enum `won` = text `WON` = false, want true")
	}
	// A TEXT value that is not a declared member of the enum still COMPARES —
	// it is a value, not a non-conformance on the text side (R-1, verbatim).
	undeclaredGot, undeclaredProblems := c.Evaluate(OpEqual, present(enumProp, tvEnum(t, "won")), present(textProp, tvText("not-a-declared-value")))
	if undeclaredGot {
		t.Errorf("R-1: enum `won` = text `not-a-declared-value` = true, want false")
	}
	if len(undeclaredProblems) != 0 {
		t.Errorf("R-1: an undeclared value on the TEXT side is a value, not a problem; got %v", problemCodes(undeclaredProblems))
	}

	// UNIFICATION 2 — integer and decimal are ONE declared type. `3 = 3.0` is
	// the rule's own worked example.
	intProp := testProperty("count", TypeInteger, false)
	decProp := testProperty("amount", TypeDecimal, false)
	if got, problems := c.Evaluate(OpEqual, present(intProp, tvNumber(t, TypeInteger, "3")), present(decProp, tvNumber(t, TypeDecimal, "3.0"))); !got || len(problems) != 0 {
		t.Errorf("R-1: 3 = 3.0 gave %v/%v, want true and no problems", got, problemCodes(problems))
	}
	if got, _ := c.Evaluate(OpGreater, present(decProp, tvNumber(t, TypeDecimal, "3.5")), present(intProp, tvNumber(t, TypeInteger, "3"))); !got {
		t.Errorf("R-1: 3.5 > 3 = false, want true")
	}
}

// TestComparison_R2_AbsentIsFalseForEveryOperatorButTheUnaryTwo is R-2, swept
// across every declared type on each side.
func TestComparison_R2_AbsentIsFalseForEveryOperatorButTheUnaryTwo(t *testing.T) {
	c := testComparator()
	for _, typ := range PropertyTypes {
		p := testProperty("fixture", typ, false)
		val := present(p, sweepFixtures(t, sweepEqual, typ, posLeft))
		for _, op := range Operators {
			if op == OpIsNull || op == OpIsNotNull {
				continue
			}
			if got, _ := c.Evaluate(op, absentOperand(p), val); got {
				t.Errorf("R-2: absent %s %s present = true, want false", typ, op)
			}
			if got, _ := c.Evaluate(op, val, absentOperand(p)); got {
				t.Errorf("R-2: present %s %s absent = true, want false", typ, op)
			}
			if got, _ := c.Evaluate(op, absentOperand(p), absentOperand(p)); got {
				t.Errorf("R-2: absent %s %s absent = true, want false", typ, op)
			}
		}
	}
}

// TestComparison_R2_NotEqualIsNotAnException is §8 R-2's C-7 ruling, asserted on
// its own because three normative places once said the opposite and because it
// is the single most tempting cell to "fix".
//
// `<>` over an absent side is FALSE — the record does NOT match. In SQL,
// `x <> 'v'` over a NULL `x` yields NULL and the row is excluded; ruling R-B
// adopts SQL's names AND SQL's semantics, and exempting `<>` while leaving `<`,
// `<=`, `>` and `>=` governed would be exactly the invented semantics R-B
// forbids.
//
// THE CAPABILITY IS NOT LOST, IT MOVES ONE LEVEL UP, and the second half of this
// test is that: `{not: {p = v}}` includes the records that never said, because
// R-2's real `false` makes `NOT(false)` true at any depth.
func TestComparison_R2_NotEqualIsNotAnException(t *testing.T) {
	c := testComparator()
	p := testProperty("status", TypeText, false)
	val := present(p, tvText("done"))

	if got, problems := c.Evaluate(OpNotEqual, absentOperand(p), val); got || len(problems) != 0 {
		t.Errorf("R-2/C-7: absent <> 'done' gave %v/%v, want false and no problems — `<>` is NOT exempt from R-2",
			got, problemCodes(problems))
	}

	// The remedy, end to end through the matching layer: a NEGATED `=` leaf
	// includes the record that never said. This is FR-008.
	schema := &Schema{Type: "fixture", Properties: map[string]*Property{"status": p}, PropertyOrder: []string{"status"}}
	silent := ParseRecord("silent.md", []byte("---\ntype: fixture\n---\nbody\n"))

	negated := Filter{Property: "status", Op: OpEqual, Literal: "done", Negate: true}
	res, err := negated.MatchWith(c, schema, silent)
	if err != nil {
		t.Fatalf("FR-008: the negated filter did not validate: %v", err)
	}
	if !res.Matched {
		t.Errorf("FR-008: `not(status = done)` excluded the record that never said; it must INCLUDE it")
	}

	// And the `<>` LEAF does not, which is the distinction the rule draws.
	leaf := Filter{Property: "status", Op: OpNotEqual, Literal: "done"}
	res, err = leaf.MatchWith(c, schema, silent)
	if err != nil {
		t.Fatalf("the `<>` filter did not validate: %v", err)
	}
	if res.Matched {
		t.Errorf("R-2/C-7: a `<>` LEAF matched a record with no value; `<>` is a leaf, `not` is a tree")
	}
}

// TestComparison_R3_IsNullDistinguishesEmptyFromMissing is R-3: an empty string,
// an empty list and a zero are VALUES, not absence.
func TestComparison_R3_IsNullDistinguishesEmptyFromMissing(t *testing.T) {
	c := testComparator()
	textProp := testProperty("note", TypeText, false)
	listProp := testProperty("tags", TypeText, true)
	numProp := testProperty("amount", TypeInteger, false)

	cases := []struct {
		name   string
		v      PropertyValue
		isNull bool
	}{
		{"missing text", absentOperand(textProp), true},
		{"missing number", absentOperand(numProp), true},
		{"missing list", absentOperand(listProp), true},
		{"empty string", present(textProp, tvText("")), false},
		{"empty list", presentList(listProp), false},
		{"zero", present(numProp, tvNumber(t, TypeInteger, "0")), false},
		{"non-conforming value is present, not absent", nonConformingOperand(numProp), false},
	}
	for _, tc := range cases {
		got, problems := c.Evaluate(OpIsNull, tc.v, absentOperand(textProp))
		if got != tc.isNull {
			t.Errorf("R-3: `IS NULL` (%s) = %v, want %v", tc.name, got, tc.isNull)
		}
		if len(problems) != 0 {
			t.Errorf("R-3: `IS NULL` (%s) reported %v, want none", tc.name, problemCodes(problems))
		}
		// IS NOT NULL is the exact complement, for every one of these states —
		// including the non-conforming one, where something IS written.
		notGot, notProblems := c.Evaluate(OpIsNotNull, tc.v, absentOperand(textProp))
		if notGot == tc.isNull {
			t.Errorf("R-3: `IS NOT NULL` (%s) = %v, want %v — it must be the complement", tc.name, notGot, !tc.isNull)
		}
		if len(notProblems) != 0 {
			t.Errorf("R-3: `IS NOT NULL` (%s) reported %v, want none", tc.name, problemCodes(notProblems))
		}
	}
}

// TestComparison_R4_NonConformingIsFalseAndReported is R-4, swept across all
// seven declared types. "Silence here is the defect", so the problem list is
// asserted, not just the boolean.
func TestComparison_R4_NonConformingIsFalseAndReported(t *testing.T) {
	c := testComparator()
	for _, typ := range PropertyTypes {
		p := testProperty("fixture", typ, false)
		bad := nonConformingOperand(p)
		good := present(p, sweepFixtures(t, sweepEqual, typ, posLeft))
		for _, op := range Operators {
			if op == OpIsNull || op == OpIsNotNull {
				continue // R-3 governs; a corrupt value is present, not absent.
			}
			got, problems := c.Evaluate(op, bad, good)
			if got {
				t.Errorf("R-4: non-conforming %s %s conforming = true, want false", typ, op)
			}
			if len(problems) != 1 || problems[0].Code != CompareNonConforming {
				t.Errorf("R-4: non-conforming %s %s reported %v, want exactly one non_conforming_value",
					typ, op, problemCodes(problems))
				continue
			}
			if problems[0].Type != typ {
				t.Errorf("R-4: the problem for %s names type %s", typ, problems[0].Type)
			}
			// Both sides corrupt: BOTH are named, so an operator can fix both.
			if _, both := c.Evaluate(op, bad, nonConformingOperand(p)); len(both) != 2 {
				t.Errorf("R-4: two non-conforming operands (%s, %s) reported %d problems, want 2", typ, op, len(both))
			}
		}
	}
}

// TestComparison_R4_ReachedThroughRealParsing checks that values a real vault
// produces land in StateNonConforming through the package's own parser, rather
// than being coerced. R-4 through ResolveProperty instead of a hand-built state.
func TestComparison_R4_ReachedThroughRealParsing(t *testing.T) {
	c := testComparator()
	src := []byte("---\namount: PLACEHOLDER — unknown\nstage: Closed Won\nwhen: last tuesday\n---\nbody\n")
	rec := ParseRecord("fixture.md", src)

	cases := []struct {
		prop *Property
		name string
	}{
		{testProperty("amount", TypeDecimal, false), "a decimal property holding prose"},
		{testProperty("stage", TypeEnum, false), "an enum value outside the declared set (FR-011)"},
		{testProperty("when", TypeDate, false), "a date property holding prose"},
	}
	for _, tc := range cases {
		pv := ResolveProperty(rec, tc.prop)
		if pv.State != StateNonConforming {
			t.Errorf("R-4: %s resolved to %s, want non-conforming", tc.name, pv.State)
			continue
		}
		got, problems := c.Evaluate(OpEqual, pv, pv)
		if got {
			t.Errorf("R-4: %s compared equal to itself, want false", tc.name)
		}
		if len(problems) == 0 {
			t.Errorf("R-4: %s produced no problem — silence here is the defect", tc.name)
		}
	}
}

// TestComparison_R5_EnumOrdersLexicallyNotByDeclaredPosition is R-5 as REVERSED
// by ruling R-E.
//
// The fixture's declared order deliberately contradicts its lexical order, so a
// comparator that still ordered by declared position fails here — which is the
// mirror image of what this test asserted before the ruling, and that is the
// point: the old test would pass against the old bug.
func TestComparison_R5_EnumOrdersLexicallyNotByDeclaredPosition(t *testing.T) {
	c := testComparator()
	p := testProperty("stage", TypeEnum, false)
	// Declared: lead(0), won(1), vendors(2), vendor%(3).
	// Lexical:  lead < vendor% < vendors < won.
	lead := present(p, tvEnum(t, "lead"))
	won := present(p, tvEnum(t, "won"))
	vendors := present(p, tvEnum(t, "vendors"))

	if got, _ := c.Evaluate(OpLess, lead, won); !got {
		t.Errorf("R-5: lead < won = false, want true (lexical)")
	}
	if got, _ := c.Evaluate(OpLess, vendors, won); !got {
		t.Errorf("R-5/R-E: vendors < won = false, want true — LEXICAL order. A comparator still using declared position says won(1) < vendors(2) and fails here")
	}
	if got, _ := c.Evaluate(OpGreater, won, vendors); !got {
		t.Errorf("R-5/R-E: won > vendors = false, want true (lexical)")
	}
	if got, _ := c.Evaluate(OpEqual, won, won); !got {
		t.Errorf("R-5: won == won = false, want true")
	}
	if got, _ := c.Evaluate(OpGreaterOrEqual, won, won); !got {
		t.Errorf("R-5: won >= won = false, want true")
	}

	// R-D: equality resolves CASE-INSENSITIVELY to a declared value. Resolving
	// `WON` TO `won` collapses two spellings into ONE value rather than creating
	// a second, which is the thing D4 actually forbids. (The value carries the
	// DECLARED spelling; the file's own spelling stays in Raw and is what
	// renders — FR-011c.)
	written := TypedValue{Type: TypeEnum, Raw: "WON", Enum: mustResolveEnum(t, p, "WON")}
	if got, problems := c.Evaluate(OpEqual, present(p, written), won); !got || len(problems) != 0 {
		t.Errorf(`R-5/R-D: a note writing "WON" against a declared "won" gave %v/%v, want true and no problems`,
			got, problemCodes(problems))
	}

	// R-E's ordering mechanism, stated as the ruling states it: a domain order
	// is expressed by PREFIXING THE VALUES, and then the lexical order IS the
	// domain order. This is the accepted cost of deleting the ordinal column.
	prefixed := &Property{Name: "stage", Type: TypeEnum, RecordType: "fixture", Values: []EnumValue{
		{Name: "1-lead"}, {Name: "2-qualified"}, {Name: "3-proposal"}, {Name: "4-won"},
	}}
	for _, pair := range [][2]string{{"1-lead", "2-qualified"}, {"2-qualified", "3-proposal"}, {"3-proposal", "4-won"}} {
		lo := present(prefixed, TypedValue{Type: TypeEnum, Raw: pair[0], Enum: mustResolveEnum(t, prefixed, pair[0])})
		hi := present(prefixed, TypedValue{Type: TypeEnum, Raw: pair[1], Enum: mustResolveEnum(t, prefixed, pair[1])})
		if got, _ := c.Evaluate(OpLess, lo, hi); !got {
			t.Errorf("R-E: %s < %s = false, want true — the prefix convention IS the domain order", pair[0], pair[1])
		}
	}

	// R-5(d): the SORT is total and deterministic — folded key first, raw bytes
	// as the tie-break. On raw bytes alone "Won" < "lost" is TRUE; folded it is
	// FALSE, and folded is what a reader can reason about because it agrees with
	// grouping.
	names := []string{"lost", "Won", "lead", "won"}
	SortValuesBySortKey(names)
	if strings.Join(names, ",") != "lead,lost,Won,won" {
		t.Errorf("R-5(d): SortValuesBySortKey gave %v, want [lead lost Won won] (folded key, raw-byte tie-break)", names)
	}

	// A value the property does not declare — not even case-insensitively — is
	// non-conformance (FR-011), reported.
	src := []byte("---\nstage: Closed Won\n---\n")
	pv := ResolveProperty(ParseRecord("f.md", src), p)
	if pv.State != StateNonConforming {
		t.Errorf(`R-5/FR-011: "Closed Won" against a set declaring %v resolved to %s, want non-conforming`,
			p.PermittedValues(), pv.State)
	}
}

func mustResolveEnum(t *testing.T, p *Property, written string) EnumValue {
	t.Helper()
	v, ok := p.ResolveEnum(written)
	if !ok {
		t.Fatalf("test fixture: %q does not resolve against %v", written, p.PermittedValues())
	}
	return v
}

// TestComparison_R5_OrderingHasNoTieBreak is the distinction between the ORDER
// and the SORT, asserted because collapsing them is the obvious "simplification"
// and it produces a self-contradictory comparator.
//
// R-5(d)'s raw-byte tie-break exists so a SORT is total. If the `<` OPERATOR
// used it, `won = Won` and `won < Won` would both be true at the same time.
func TestComparison_R5_OrderingHasNoTieBreak(t *testing.T) {
	c := testComparator()
	p := testProperty("name", TypeText, false)
	lower := present(p, tvText("won"))
	upper := present(p, tvText("Won"))

	if got, _ := c.Evaluate(OpEqual, lower, upper); !got {
		t.Fatalf(`FR-011a: "won" = "Won" = false, want true`)
	}
	if got, _ := c.Evaluate(OpLess, lower, upper); got {
		t.Errorf(`R-5: "won" < "Won" = true while "won" = "Won" is also true. The operator must compare FOLDED KEYS ONLY; the raw-byte tie-break belongs to the SORT`)
	}
	if got, _ := c.Evaluate(OpGreater, lower, upper); got {
		t.Errorf(`R-5: "won" > "Won" = true while "won" = "Won" is also true`)
	}
	// The sort, by contrast, MUST resolve the tie, and deterministically.
	first := []string{"won", "Won"}
	second := []string{"Won", "won"}
	SortValuesBySortKey(first)
	SortValuesBySortKey(second)
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Errorf("R-11/O-5: the sort is not deterministic: %v vs %v from two input orders", first, second)
	}
}

// TestComparison_R7_DateComparesAsAnInstant is R-7: a date and a date-time are
// the same declared type and compare directly.
func TestComparison_R7_DateComparesAsAnInstant(t *testing.T) {
	c := testComparator()
	p := testProperty("when", TypeDate, false)
	day := tvDate(t, "2026-01-01")
	instant := tvDate(t, "2026-01-01T09:00:00Z")

	if day.Type != instant.Type {
		t.Fatalf("R-7: a date and a date-time must be the same declared type, got %s and %s", day.Type, instant.Type)
	}
	if got, _ := c.Evaluate(OpLess, present(p, day), present(p, instant)); !got {
		t.Errorf("R-7: 2026-01-01 < 2026-01-01T09:00:00Z = false, want true (a bare day is midnight UTC)")
	}
	if got, _ := c.Evaluate(OpEqual, present(p, day), present(p, tvDate(t, "2026-01-01T00:00:00Z"))); !got {
		t.Errorf("R-7: a bare day and midnight UTC must be the same instant")
	}
	// The same instant written in two offsets. SQLite orders these WRONG (the
	// separator and the offset both reorder it), which is why nothing delegates.
	if got, _ := c.Evaluate(OpEqual, present(p, tvDate(t, "2026-01-01T09:00:00Z")), present(p, tvDate(t, "2026-01-01T10:00:00+01:00"))); !got {
		t.Errorf("R-7: the same instant in two offsets must compare equal")
	}
	if got, _ := c.Evaluate(OpGreater, present(p, tvDate(t, "2026-06-01")), present(p, tvDate(t, "2026-01-01"))); !got {
		t.Errorf("R-7: 2026-06-01 > 2026-01-01 = false, want true")
	}
	// `LIKE` is NOT defined for a date. SQL would reach it by coercing to text;
	// this design coerces nothing, and the refusal names what would have worked.
	got, problems := c.Evaluate(OpLike, present(p, day), present(p, day))
	if got {
		t.Errorf("R-7: `date LIKE date` = true; no rule defines LIKE for a date")
	}
	if len(problems) != 1 || problems[0].Code != CompareOperatorNotDefined {
		t.Fatalf("R-7: `date LIKE date` reported %v, want one operator_not_defined_for_type", problemCodes(problems))
	}
	if len(problems[0].Supported) == 0 {
		t.Errorf("FR-022c: the refusal does not list the supported operators")
	}
}

// TestComparison_R8_RelationComparesByTargetIdentity is R-8: two links resolving
// to the same record are equal regardless of spelling, and two links with the
// same display text but different targets are not.
//
// This is the case D7 records as the reason identity is not the filename:
// `[[Acme Corp]]` and `[[Acme Corp.]]` group separately forever when a
// comparator compares link text.
func TestComparison_R8_RelationComparesByTargetIdentity(t *testing.T) {
	c := testComparator()
	p := testProperty("company", TypeRelation, false)
	a := present(p, tvLink(TypeRelation, "Acme Ltd"))
	b := present(p, tvLink(TypeRelation, "acme ltd."))
	other := present(p, tvLink(TypeRelation, "Beta GmbH"))

	if got, _ := c.Evaluate(OpEqual, a, b); !got {
		t.Errorf("R-8: two spellings of one target compared unequal, want equal")
	}
	if got, _ := c.Evaluate(OpNotEqual, a, b); got {
		t.Errorf("R-8: `<>` over two spellings of one target = true, want false")
	}
	if got, _ := c.Evaluate(OpEqual, a, other); got {
		t.Errorf("R-8: different targets compared equal, want unequal")
	}
	if got, _ := c.Evaluate(OpNotEqual, a, other); !got {
		t.Errorf("R-8: `<>` over different targets = false, want true")
	}
	// Person is a relation to a person record (ADR-068 D3) and behaves identically.
	pp := testProperty("owner", TypePerson, false)
	if got, _ := c.Evaluate(OpEqual, present(pp, tvLink(TypePerson, "Ada Lovelace")), present(pp, tvLink(TypePerson, "A. Lovelace"))); !got {
		t.Errorf("R-8/D3: two spellings of one person compared unequal, want equal")
	}
	// An unresolvable target has no identity, so there is nothing to compare and
	// it is REPORTED, never silently equal or silently unequal (ADR-068 O-5).
	ghost := present(p, tvLink(TypeRelation, "Ghost Co"))
	got, problems := c.Evaluate(OpEqual, a, ghost)
	if got {
		t.Errorf("R-8: an unresolvable target compared equal, want false")
	}
	if len(problems) != 1 || problems[0].Code != CompareRelationUnresolved {
		t.Errorf("R-8/O-5: an unresolvable target reported %v, want one relation_target_unresolved", problemCodes(problems))
	}
	// A comparator with no resolver cannot honour R-8, and says so rather than
	// falling back to comparing link text.
	bare := Comparator{}
	if _, problems := bare.Evaluate(OpEqual, a, b); len(problems) != 2 {
		t.Errorf("R-8: a comparator with no resolver reported %v, want both sides unresolved", problemCodes(problems))
	}
	// The IDENTIFIER side is byte-exact, with NO folding: `CO-0142` and
	// `co-0142` must remain two records, or two legitimately distinct targets
	// could not coexist. (The path/name side folds; that is the resolver's job.)
	caseSensitive := Comparator{ResolveRelation: func(link Wikilink) (string, bool) {
		return map[string]string{"Upper": "CO-0142", "Lower": "co-0142"}[link.Target], true
	}}
	if got, _ := caseSensitive.Evaluate(OpEqual,
		present(p, tvLink(TypeRelation, "Upper")), present(p, tvLink(TypeRelation, "Lower"))); got {
		t.Errorf("R-8: identifiers CO-0142 and co-0142 compared equal; the identifier side must NOT fold")
	}
	// No rule gives a record identity an order.
	_, orderProblems := c.Evaluate(OpLess, a, other)
	if len(orderProblems) != 1 || orderProblems[0].Code != CompareOperatorNotDefined {
		t.Errorf("R-8: `relation < relation` reported %v, want one operator_not_defined_for_type", problemCodes(orderProblems))
	}
}

// TestComparison_R9_ManyIsElementWise is R-9 as RESTATED in SQL's terms.
//
// "Against a `many` property, `=` matches an element exactly and `IN` matches
// any element of a list; `LIKE` matches an element by pattern."
//
// The old rule existed because ONE invented operator had to serve two meanings.
// SQL's vocabulary already separates them, so the test asserts the SEPARATION:
// the same fixture answers differently under `=` and under `LIKE`, which is the
// whole benefit of the ruling and is not observable with one operator.
func TestComparison_R9_ManyIsElementWise(t *testing.T) {
	c := testComparator()
	listProp := testProperty("tags", TypeText, true)
	scalarProp := testProperty("tags", TypeText, false)
	tags := presentList(listProp, tvText("Acme Ltd"), tvText("Beta GmbH"))

	// `=` is EXACT against an element — whole-element, never substring.
	if got, _ := c.Evaluate(OpEqual, tags, present(scalarProp, tvText("Acme Ltd"))); !got {
		t.Errorf("R-9: tags = 'Acme Ltd' = false, want true (the element is in the list)")
	}
	if got, _ := c.Evaluate(OpEqual, tags, present(scalarProp, tvText("Acme"))); got {
		t.Errorf("R-9: tags = 'Acme' = true, want false — `=` is EXACT; a substring match would be `LIKE '%%Acme%%'`")
	}
	// ...and it folds, because R-10's case rule applies to elements too.
	if got, _ := c.Evaluate(OpEqual, tags, present(scalarProp, tvText("acme ltd"))); !got {
		t.Errorf("R-9 + FR-011a: tags = 'acme ltd' = false, want true — element matching is case-insensitive")
	}
	// `LIKE` is PATTERNED against an element. Same fixture, same needle, a
	// different answer — which is the distinction the invented operator could
	// not express.
	if got, _ := c.Evaluate(OpLike, tags, present(scalarProp, tvText("Acme%"))); !got {
		t.Errorf("R-9: tags LIKE 'Acme%%' = false, want true")
	}
	if got, _ := c.Evaluate(OpLike, tags, present(scalarProp, tvText("Acme"))); got {
		t.Errorf("R-9/FR-022b: tags LIKE 'Acme' = true, want false — LIKE is ANCHORED; a wildcard-free pattern is an exact match")
	}
	// `IN` is membership of the list in a SET.
	inSet := PropertyValue{Property: &Property{Name: "tags", Type: TypeText, RecordType: "fixture", Many: true},
		State: StatePresent, Values: []TypedValue{tvText("Zeta"), tvText("Beta GmbH")}}
	if got, _ := c.Evaluate(OpIn, tags, inSet); !got {
		t.Errorf("R-9: tags IN ('Zeta','Beta GmbH') = false, want true")
	}
	missSet := PropertyValue{Property: &Property{Name: "tags", Type: TypeText, RecordType: "fixture", Many: true},
		State: StatePresent, Values: []TypedValue{tvText("Zeta"), tvText("Gamma")}}
	if got, _ := c.Evaluate(OpIn, tags, missSet); got {
		t.Errorf("R-9: tags IN ('Zeta','Gamma') = true, want false")
	}
	// An empty list contains nothing, under every element-wise operator.
	for _, op := range []Operator{OpEqual, OpNotEqual, OpLike, OpIn} {
		if got, _ := c.Evaluate(op, presentList(listProp), present(scalarProp, tvText("anything"))); got {
			t.Errorf("R-9: an empty list %s 'anything' = true, want false", op)
		}
	}
	// Membership over relations resolves by target identity (R-8 inside R-9).
	relList := testProperty("companies", TypeRelation, true)
	relScalar := testProperty("companies", TypeRelation, false)
	rels := presentList(relList, tvLink(TypeRelation, "Acme Ltd"))
	if got, _ := c.Evaluate(OpEqual, rels, present(relScalar, tvLink(TypeRelation, "acme ltd."))); !got {
		t.Errorf("R-9 with R-8: membership must resolve by target identity, not spelling")
	}
	if got, _ := c.Evaluate(OpEqual, rels, present(relScalar, tvLink(TypeRelation, "Beta GmbH"))); got {
		t.Errorf("R-9 with R-8: a different target must not be a member")
	}
	// R-2 still governs inside a list: an absent needle matches nothing.
	if got, _ := c.Evaluate(OpEqual, tags, absentOperand(scalarProp)); got {
		t.Errorf("R-2: `many = absent` = true, want false")
	}
}

// TestComparison_R10_TextIsExactOrPatternedAndAlwaysCaseInsensitive is R-10 as
// REVERSED and RESTATED by rulings R-D and R-B.
//
// Both halves changed from the pre-ruling rule ("substring matching,
// case-sensitive"): the operator is now SQL's, and the case-sensitivity is
// inverted. Case-insensitivity is a FEATURE here, not an accident.
func TestComparison_R10_TextIsExactOrPatternedAndAlwaysCaseInsensitive(t *testing.T) {
	c := testComparator()
	p := testProperty("name", TypeText, false)
	acme := present(p, tvText("Acme Ltd"))

	// `=` is EXACT and CASE-INSENSITIVE.
	if got, _ := c.Evaluate(OpEqual, acme, present(p, tvText("acme ltd"))); !got {
		t.Errorf("R-10/R-D: 'Acme Ltd' = 'acme ltd' = false, want true — matching is case-insensitive")
	}
	if got, _ := c.Evaluate(OpEqual, acme, present(p, tvText("Acme"))); got {
		t.Errorf("R-10: 'Acme Ltd' = 'Acme' = true, want false — `=` is exact, not a prefix")
	}
	if got, _ := c.Evaluate(OpNotEqual, acme, present(p, tvText("acme ltd"))); got {
		t.Errorf("R-10: '<>' must fold too")
	}

	// `LIKE` is PATTERNED, ANCHORED, and also case-insensitive.
	for _, tc := range []struct {
		pattern string
		want    bool
		why     string
	}{
		{"Acme Ltd", true, "a wildcard-free pattern is an exact (folded) match"},
		{"acme ltd", true, "and it folds"},
		{"Acme", false, "ANCHORED: 'vendors' LIKE 'vendor' is false, and so is this"},
		{"Acme%", true, "prefix"},
		{"%Ltd", true, "suffix"},
		{"%cme L%", true, "substring, expressed as SQL expresses it"},
		{"acme_ltd", true, "`_` matches exactly one character — the space"},
		{"acme__ltd", false, "`_` is exactly one, not one-or-more"},
		{"Zeta%", false, "no match"},
	} {
		got, problems := c.Evaluate(OpLike, acme, present(p, tvText(tc.pattern)))
		if got != tc.want {
			t.Errorf("R-10: 'Acme Ltd' LIKE %q = %v, want %v (%s)", tc.pattern, got, tc.want, tc.why)
		}
		if len(problems) != 0 {
			t.Errorf("R-10: 'Acme Ltd' LIKE %q reported %v, want none", tc.pattern, problemCodes(problems))
		}
	}

	// The ordering half, inherited from R-5 through R-1's unification.
	if got, _ := c.Evaluate(OpLess, present(p, tvText("apple")), present(p, tvText("banana"))); !got {
		t.Errorf("R-1/R-5: 'apple' < 'banana' = false, want true (lexical over the folded key)")
	}
}

// TestComparison_AC89_TheComparatorFoldsFully is AC-8.9, driven through the
// COMPARATOR rather than through FoldKey directly.
//
// value.go asserts FoldKey's own behaviour. This asserts that the comparator
// USES it: a comparator that folded with strings.ToLower, or with
// strings.EqualFold, or not at all, fails a named cell here even though FoldKey
// itself would still be perfect.
//
// The discriminating property is stated as the spec states it: AC-8.9 fails if
// the comparator produces the same six answers as `strings.ToLower`, or the same
// six as `strings.EqualFold`.
//
// MUTATION-VERIFIED, AND THE RESULT IS THE REASON THIS TEST EXISTS SEPARATELY.
// Substituting `strings.ToLower` for FoldKey inside textualAnswer fails THIS
// TEST AND NOTHING ELSE — zero of the 57,760 generated cells notice, because
// every textual fixture in the table is ASCII and ToLower is correct on ASCII.
// The generated table cannot carry this rule; that is not a gap in the table,
// it is why the spec writes AC-8.9 as six literal pairs instead of leaving the
// mechanism to be inferred. Removing the fold ENTIRELY does fail the table (45
// cells), so the table proves that SOME folding happens and this test proves it
// is the RIGHT folding.
func TestComparison_AC89_TheComparatorFoldsFully(t *testing.T) {
	c := testComparator()
	p := testProperty("name", TypeText, false)

	cases := []struct {
		id, left, right string
		want            bool
		why             string
	}{
		{"AC-8.9a", "straße", "STRASSE", true,
			"German ß needs FULL folding (ß→ss). false under strings.ToLower AND under strings.EqualFold — this is the cell that fails if anyone substitutes a stdlib call"},
		{"AC-8.9b", "σίσυφος", "ΣΊΣΥΦΟΣ", true,
			"Greek final sigma. false under ToLower, true under EqualFold — the two stdlib functions DISAGREE, which is why neither is a permitted default"},
		{"AC-8.9c", "müller", "MÜLLER", true,
			"German umlaut. All mechanisms agree; here as the ordinary case so the set is not all edge"},
		{"AC-8.9d", "łódź", "ŁÓDŹ", true,
			"Polish. The control row — a fixture of only rows like this discriminates nothing"},
		{"AC-8.9e", "istanbul", "İSTANBUL", false,
			"TURKISH DOTTED İ AND PLAIN i ARE DIFFERENT LETTERS. This MUST NOT match. `true` here is the classic Turkish-I bug, which is what strings.ToLower produces. cases.Fold maps İ to i+U+0307 and keeps them apart. DO NOT 'FIX' THIS CELL"},
		{"AC-8.9f", "ﬁle", "file", true,
			"Ligature. false under both stdlib functions. Not a language case — the second independent witness that simple folding is not enough"},
	}

	sameAsToLower, sameAsEqualFold := true, true
	for _, tc := range cases {
		got, problems := c.Evaluate(OpEqual, present(p, tvText(tc.left)), present(p, tvText(tc.right)))
		if got != tc.want {
			t.Errorf("%s: %q = %q gave %v, want %v.\n    %s", tc.id, tc.left, tc.right, got, tc.want, tc.why)
		}
		if len(problems) != 0 {
			t.Errorf("%s reported %v, want none", tc.id, problemCodes(problems))
		}
		if got != (strings.ToLower(tc.left) == strings.ToLower(tc.right)) { //nolint:staticcheck // the point is to DIFFER from this
			sameAsToLower = false
		}
		if got != strings.EqualFold(tc.left, tc.right) {
			sameAsEqualFold = false
		}
		// `<>` must fold identically, or the two operators disagree about what
		// one value is.
		if ne, _ := c.Evaluate(OpNotEqual, present(p, tvText(tc.left)), present(p, tvText(tc.right))); ne == tc.want {
			t.Errorf("%s: `<>` gave %v and `=` gave %v; they must be complements over present, conforming values", tc.id, ne, got)
		}
	}
	if sameAsToLower {
		t.Errorf("AC-8.9: the comparator produced the SAME six answers as strings.ToLower. That is the discriminating property and it must not hold — ToLower performs SIMPLE folding and gets ß wrong, and it collapses Turkish İ onto i")
	}
	if sameAsEqualFold {
		t.Errorf("AC-8.9: the comparator produced the SAME six answers as strings.EqualFold. That is the discriminating property and it must not hold — EqualFold also performs SIMPLE folding and gets ß and the ligature wrong")
	}
}

// TestComparison_R11_TotalAndNeverPanics is R-11: every type pair x every
// operator yields a boolean or a reported problem, with no third outcome. The
// truth table sweeps the well-formed space; this adds the degenerate operands a
// real caller can still produce, and the SQL constructs a model will reach for.
func TestComparison_R11_TotalAndNeverPanics(t *testing.T) {
	c := testComparator()
	numProp := testProperty("amount", TypeInteger, false)
	listProp := testProperty("tags", TypeText, true)

	degenerate := []PropertyValue{
		{}, // nobody constructed this
		{Property: nil, State: StatePresent},
		{Property: numProp, State: StatePresent}, // scalar, present, NO value
		{Property: numProp, State: StatePresent, Values: []TypedValue{{}}},  // zero TypedValue: nil Decimal
		{Property: listProp, State: StatePresent},                           // empty list
		{Property: listProp, State: StatePresent, Values: []TypedValue{{}}}, // list of one zero value
		{Property: testProperty("x", "not_a_type", false), State: StatePresent, Values: []TypedValue{{}}},
		{Property: numProp, State: StateNonConforming},
		{Property: numProp, State: StateAbsent},
	}
	operators := append(append([]Operator{}, Operators...),
		"JOIN", "BETWEEN", "COALESCE(status,'open')", "SELECT max(amount) FROM deals",
		"approximately", "", "=  ", "like", "IS  NULL")

	for i, l := range degenerate {
		for j, r := range degenerate {
			for _, op := range operators {
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							t.Fatalf("R-11 VIOLATED: Evaluate(%q, degenerate[%d], degenerate[%d]) panicked: %v", op, i, j, rec)
						}
					}()
					_, _ = c.Evaluate(op, l, r)
				}()
			}
		}
	}

	// An operator outside the closed ten is a REPORTED problem, never a silent
	// false — and the report names the ten. Note `like` and `=  ` in the list:
	// the operator set is a closed enum of exact strings, not a fuzzy match, so
	// a near-miss is refused rather than guessed at.
	for _, op := range []Operator{"approximately", "JOIN", "BETWEEN", "like", "=  ", "IS  NULL", ""} {
		got, problems := c.Evaluate(op, present(numProp, tvNumber(t, TypeInteger, "3")), present(numProp, tvNumber(t, TypeInteger, "3")))
		if got {
			t.Errorf("R-11: the unknown operator %q returned true", op)
		}
		if len(problems) != 1 || problems[0].Code != CompareUnknownOperator {
			t.Errorf("R-11: the unknown operator %q reported %v, want one unknown_operator", op, problemCodes(problems))
			continue
		}
		if len(problems[0].Supported) != len(Operators) {
			t.Errorf("FR-022c: the refusal for %q listed %d supported operators, want all %d",
				op, len(problems[0].Supported), len(Operators))
		}
	}
}

// TestComparison_R11_Deterministic is R-11's determinism clause (ruling O-5):
// same corpus, same query, byte-identical result, every time and in every
// process. Go map iteration order is the classic way that stops being true.
func TestComparison_R11_Deterministic(t *testing.T) {
	c := testComparator()
	states := operandStates()
	var first []string
	verdicts := len(allSweeps()) * len(states) * len(states) * len(Operators)
	for run := 0; run < 3; run++ {
		seen := make([]string, 0, verdicts)
		for _, sw := range allSweeps() {
			for _, l := range states {
				lv := operandFor(t, l, sw, posLeft)
				for _, r := range states {
					rv := operandFor(t, r, sw, posRight)
					for _, op := range Operators {
						got, problems := c.Evaluate(op, lv, rv)
						seen = append(seen, strings.Join([]string{
							string(sw), l.name, string(op), r.name,
							map[bool]string{true: "T", false: "F"}[got],
							strings.Join(codeStrings(problems), "+"),
						}, "|"))
					}
				}
			}
		}
		if run == 0 {
			first = seen
			continue
		}
		if len(seen) != len(first) {
			t.Fatalf("R-11: run %d produced %d verdicts, run 0 produced %d", run, len(seen), len(first))
		}
		for i := range seen {
			if seen[i] != first[i] {
				t.Fatalf("R-11/O-5: verdict %d differs between runs: %q vs %q", i, first[i], seen[i])
			}
		}
	}
}

func codeStrings(problems []ComparisonProblem) []string {
	out := make([]string, 0, len(problems))
	for _, p := range problems {
		out = append(out, string(p.Code))
	}
	return out
}

// TestComparison_R12_LiteralAndRecordAreTreatedIdentically is R-12.
//
// The comparator takes ONE operand type, so origin cannot be consulted even in
// principle — but "cannot in principle" has been wrong before. This drives the
// left operand from a real record's frontmatter through ResolveProperty and the
// right from a query literal through Filter.Validate, and asserts the answer
// matches the same values built directly.
func TestComparison_R12_LiteralAndRecordAreTreatedIdentically(t *testing.T) {
	c := testComparator()
	amount := testProperty("amount", TypeInteger, false)
	stage := testProperty("stage", TypeEnum, false)
	name := testProperty("name", TypeText, false)
	schema := &Schema{
		Type:          "fixture",
		Properties:    map[string]*Property{"amount": amount, "stage": stage, "name": name},
		PropertyOrder: []string{"amount", "stage", "name"},
	}

	rec := ParseRecord("f.md", []byte("---\ntype: fixture\namount: 3\nstage: won\nname: Acme Ltd\n---\n"))

	for _, tc := range []struct {
		prop    *Property
		literal string
		op      Operator
		want    bool
		rule    string
	}{
		{amount, "2", OpGreater, true, "AC-8.3 — 3 > 2"},
		{amount, "3", OpEqual, true, "R-1"},
		{amount, "4", OpGreater, false, "AC-8.3 converse"},
		{stage, "lead", OpGreater, true, "R-5/R-E — lexically won > lead"},
		{stage, "won", OpEqual, true, "R-5"},
		{stage, "WON", OpEqual, true, "R-5/R-D — a literal resolves case-insensitively"},
		{name, "acme ltd", OpEqual, true, "R-10/R-D — a text literal folds"},
		{name, "Acme%", OpLike, true, "R-10 — a literal pattern"},
		{name, "Acme", OpLike, false, "FR-022b — LIKE is anchored"},
	} {
		f := Filter{Property: tc.prop.Name, Op: tc.op, Literal: tc.literal}
		_, parsed, err := f.Validate(schema)
		if err != nil {
			t.Fatalf("R-12: the literal %q did not validate: %v", tc.literal, err)
		}
		recordSide := ResolveProperty(rec, tc.prop)
		literalSide := present(tc.prop, parsed[0])

		got, problems := c.Evaluate(tc.op, recordSide, literalSide)
		if got != tc.want {
			t.Errorf("R-12 (%s): record %s literal %q = %v, want %v", tc.rule, tc.op, tc.literal, got, tc.want)
		}
		if len(problems) != 0 {
			t.Errorf("R-12 (%s): reported %v, want none", tc.rule, problemCodes(problems))
		}
		// Both operands as literals must give the same answer as record-vs-literal.
		bothLiteral, _ := c.Evaluate(tc.op, present(tc.prop, recordSide.Values[0]), literalSide)
		if bothLiteral != got {
			t.Errorf("R-12 (%s): the same values gave %v as record-vs-literal and %v as literal-vs-literal",
				tc.rule, got, bothLiteral)
		}
	}
}

// TestComparison_R13_OnlyOrderingIsRefusedAgainstAList is R-13 as NARROWED by
// ruling R-B.
//
// Most of what R-13 used to refuse now has a defined answer, in a vocabulary the
// caller already knows. The refusal survives only where the question is
// genuinely undefined: "is this list greater than `vendor`?" has no answer in any
// vocabulary — and the refusal must NAME THE REMEDY, exactly as FR-024 does for
// an unknown property.
func TestComparison_R13_OnlyOrderingIsRefusedAgainstAList(t *testing.T) {
	c := testComparator()
	listProp := testProperty("segment", TypeText, true)
	scalarProp := testProperty("segment", TypeText, false)
	list := presentList(listProp, tvText("vendor"), tvText("partner"))
	needle := present(scalarProp, tvText("vendor"))

	// DEFINED against a `many` property.
	for _, op := range []Operator{OpEqual, OpNotEqual, OpLike, OpIsNull, OpIsNotNull} {
		if _, problems := c.Evaluate(op, list, needle); len(problems) != 0 {
			t.Errorf("R-13: %s against a `many` property reported %v; it is DEFINED", op, problemCodes(problems))
		}
	}
	inSet := PropertyValue{Property: &Property{Name: "segment", Type: TypeText, RecordType: "fixture", Many: true},
		State: StatePresent, Values: []TypedValue{tvText("vendor")}}
	if _, problems := c.Evaluate(OpIn, list, inSet); len(problems) != 0 {
		t.Errorf("R-13: IN against a `many` property reported %v; it is DEFINED", problemCodes(problems))
	}

	// REFUSED — the four ordering operators, and only those.
	for _, op := range []Operator{OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual} {
		got, problems := c.Evaluate(op, list, needle)
		if got {
			t.Errorf("R-13: `many %s scalar` = true, want false", op)
		}
		if len(problems) != 1 || problems[0].Code != CompareArityNotDefined {
			t.Fatalf("R-13: `many %s scalar` reported %v, want one operator_not_defined_across_arity", op, problemCodes(problems))
		}
		detail := problems[0].Detail
		for _, want := range []string{"segment", "many values", "=", "IN", "LIKE"} {
			if !strings.Contains(detail, want) {
				t.Errorf("R-13: the refusal for %s does not name %q — it must name the property and the remedy.\n  got: %s",
					op, want, detail)
			}
		}
		if len(problems[0].Supported) == 0 {
			t.Errorf("FR-022c: the R-13 refusal for %s lists no supported operators", op)
		}
	}
	// It refuses on EITHER side's arity, not only the left.
	if _, problems := c.Evaluate(OpGreater, needle, list); len(problems) != 1 || problems[0].Code != CompareArityNotDefined {
		t.Errorf("R-13: `scalar > many` reported %v, want one operator_not_defined_across_arity", problemCodes(problems))
	}
}

// TestComparison_AbsentAndNonConformingAreTypeIndependent proves the claim the
// truth table relies on when it carries absent and non-conforming on one
// declared type: R-2, R-3 and R-4 preempt R-1's type check, so the carrier type
// cannot change any expected value.
func TestComparison_AbsentAndNonConformingAreTypeIndependent(t *testing.T) {
	c := testComparator()
	for _, carrier := range PropertyTypes {
		carrierProp := testProperty("carrier", carrier, false)
		for _, other := range PropertyTypes {
			otherProp := testProperty("other", other, false)
			val := present(otherProp, sweepFixtures(t, sweepEqual, other, posRight))
			for _, op := range Operators {
				gotAbsent, absentProblems := c.Evaluate(op, absentOperand(carrierProp), val)
				wantAbsent := op == OpIsNull
				if gotAbsent != wantAbsent || len(absentProblems) != 0 {
					t.Errorf("R-2/R-3: absent(%s) %s %s = %v/%v, want %v/none",
						carrier, op, other, gotAbsent, problemCodes(absentProblems), wantAbsent)
				}
				gotBad, badProblems := c.Evaluate(op, nonConformingOperand(carrierProp), val)
				if op == OpIsNull || op == OpIsNotNull {
					// R-3: a corrupt value is PRESENT. Something is written
					// there, it is just wrong.
					if gotBad != (op == OpIsNotNull) || len(badProblems) != 0 {
						t.Errorf("R-3: non_conforming(%s) %s = %v/%v, want %v/none",
							carrier, op, gotBad, problemCodes(badProblems), op == OpIsNotNull)
					}
					continue
				}
				if gotBad {
					t.Errorf("R-4: non_conforming(%s) %s %s = true, want false", carrier, op, other)
				}
				if len(badProblems) != 1 || badProblems[0].Code != CompareNonConforming {
					t.Errorf("R-4: non_conforming(%s) %s %s reported %v, want one non_conforming_value",
						carrier, op, other, problemCodes(badProblems))
				}
			}
		}
	}
}
