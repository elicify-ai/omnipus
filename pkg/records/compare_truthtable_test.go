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
// Spec §8 states thirteen rules, R-1..R-13, and says: "The oracle is these rules.
// Every cell is generated from them, and the rules — not the cells — are what a
// human reviews."
//
// Everything below derives from those rules. Nothing below was obtained by
// running the comparator and recording what it did — that is the failure §8
// exists to prevent, and it is how `3 > 2` became false during research.
//
// Two hand-written artifacts carry all of the judgement, and they are the two
// things a reviewer should read:
//
//  1. oracleDisposition — for each declared type, which operators the rules
//     DEFINE. It restates compare_oracle.go's operatorDefinedForType
//     independently, so a one-sided edit to either cannot land quietly
//     (TestComparison_DispositionMatchesSpec).
//  2. sweepRows — for each declared type in each of three sweeps, the ordering
//     and membership relationship between the two fixture values, hand-derived
//     from the rule named in the row.
//
// From those two, the table generates 3 sweeps x 9 operand states x 9 operand
// states x 7 operators = 1,701 cells and asserts the comparator matches each.
// ---------------------------------------------------------------------------

// oracleDisposition: does a numbered rule DEFINE this operator for this type?
// Authority per row is given in compare_oracle.go's operatorDefinedForType; the
// two are stated separately on purpose and must agree.
var oracleDisposition = map[PropertyType]map[Operator]bool{
	// R-10 defines `contains` on text as case-sensitive substring matching.
	// ADR-068 D3 defines text as "prose; never validated, never queried for
	// equality" — so equality is undefined, and a fortiori so is ordering.
	// SPEC GAP, REPORTED: see operatorDefinedForType's note on this row.
	TypeText: {OpEqual: false, OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false, OpContains: true},
	// R-5: declared position for ordering, exact-case for equality.
	TypeEnum: {OpEqual: true, OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true, OpContains: false},
	// R-8: equality by target identity. Identity has no order and no rule gives one.
	TypeRelation: {OpEqual: true, OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false, OpContains: false},
	// ADR-068 D3: person IS a relation to a person record, so it inherits R-8.
	TypePerson: {OpEqual: true, OpLess: false, OpLessOrEqual: false, OpGreater: false, OpGreaterOrEqual: false, OpContains: false},
	// R-7: a date compares as an instant.
	TypeDate: {OpEqual: true, OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true, OpContains: false},
	// R-1's worked example and AC-8.3 ("3 > 2 is TRUE") require the full family.
	TypeNumber: {OpEqual: true, OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true, OpContains: false},
	// R-6: every operator compares, but only within one currency.
	TypeMoney: {OpEqual: true, OpLess: true, OpLessOrEqual: true, OpGreater: true, OpGreaterOrEqual: true, OpContains: false},
}

// ---------------------------------------------------------------------------
// Fixtures. ADR-068 D0: the product ships NO record types, so every name below
// is fixture data invented for this test and means nothing to the product.
// ---------------------------------------------------------------------------

// stageValues' declared order deliberately contradicts its lexical order:
// lexically "lost" < "won"; by declared position "won"(1) < "lost"(2). Any
// comparator that falls back to spelling fails R-5 against this fixture.
var stageValues = []EnumValue{
	{Name: "lead", Position: 0},
	{Name: "won", Position: 1},
	{Name: "lost", Position: 2},
}

func testProperty(name string, t PropertyType, many bool) *Property {
	p := &Property{Name: name, Type: t, Many: many, RecordType: "fixture"}
	if t == TypeEnum {
		p.Values = append([]EnumValue(nil), stageValues...)
		p.valuePos = map[string]int{}
		for _, v := range p.Values {
			p.valuePos[v.Name] = v.Position
		}
	}
	return p
}

func tvText(s string) TypedValue { return TypedValue{Type: TypeText, Raw: s, Text: s} }

func tvEnum(name string) TypedValue {
	for _, v := range stageValues {
		if v.Name == name {
			return TypedValue{Type: TypeEnum, Raw: name, Enum: v}
		}
	}
	panic("test fixture: " + name + " is not in the declared enum set")
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

func tvNumber(t *testing.T, s string) TypedValue {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("test fixture: %q is not a number: %v", s, err)
	}
	return TypedValue{Type: TypeNumber, Raw: s, Number: d}
}

func tvMoney(t *testing.T, amount, currency string) TypedValue {
	t.Helper()
	d, err := ParseDecimal(amount)
	if err != nil {
		t.Fatalf("test fixture: %q is not a decimal: %v", amount, err)
	}
	return TypedValue{Type: TypeMoney, Raw: amount + " " + currency,
		Money: Money{Amount: d, Currency: currency}}
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
)

// sweepRow is the hand-derived answer for one declared type in one sweep.
// equal/less/greater describe the relationship between left and right; contains
// is R-10's substring answer, meaningful only for text.
type sweepRow struct {
	left           TypedValue
	right          TypedValue
	equal          bool
	less           bool
	greater        bool
	containsScalar bool
	rule           string
}

func sweepRows(t *testing.T) map[sweep]map[PropertyType]sweepRow {
	t.Helper()
	return map[sweep]map[PropertyType]sweepRow{
		sweepLess: {
			// R-10: "Acme" does not contain the substring "Beta".
			TypeText: {left: tvText("Acme"), right: tvText("Beta"), rule: "R-10"},
			// R-5: declared position of "won" is 1, of "lost" is 2, so won < lost.
			// Lexical spelling would say the opposite.
			TypeEnum: {left: tvEnum("won"), right: tvEnum("lost"), less: true, rule: "R-5"},
			// R-8: CO-0001 and CO-0002 are different records, so not equal.
			TypeRelation: {left: tvLink(TypeRelation, "Acme Ltd"), right: tvLink(TypeRelation, "Beta GmbH"), rule: "R-8"},
			TypePerson:   {left: tvLink(TypePerson, "Ada Lovelace"), right: tvLink(TypePerson, "Grace Hopper"), rule: "R-8 via ADR-068 D3"},
			// R-7: 2026-01-01T00:00:00Z precedes 2026-06-01T00:00:00Z.
			TypeDate: {left: tvDate(t, "2026-01-01"), right: tvDate(t, "2026-06-01"), less: true, rule: "R-7"},
			// Arithmetic: 2 < 3.
			TypeNumber: {left: tvNumber(t, "2"), right: tvNumber(t, "3"), less: true, rule: "R-1 worked example"},
			// R-6: one currency, so the amounts compare: 1.00 < 2.00.
			TypeMoney: {left: tvMoney(t, "1.00", "USD"), right: tvMoney(t, "2.00", "USD"), less: true, rule: "R-6"},
		},
		sweepEqual: {
			// R-10: every string contains itself.
			TypeText: {left: tvText("Acme"), right: tvText("Acme"), equal: true, containsScalar: true, rule: "R-10"},
			TypeEnum: {left: tvEnum("won"), right: tvEnum("won"), equal: true, rule: "R-5"},
			// R-8: two links resolving to the same record are equal REGARDLESS of
			// spelling. These two differ in case and in punctuation.
			TypeRelation: {left: tvLink(TypeRelation, "Acme Ltd"), right: tvLink(TypeRelation, "acme ltd."), equal: true, rule: "R-8"},
			TypePerson:   {left: tvLink(TypePerson, "Ada Lovelace"), right: tvLink(TypePerson, "A. Lovelace"), equal: true, rule: "R-8 via ADR-068 D3"},
			TypeDate:     {left: tvDate(t, "2026-01-01"), right: tvDate(t, "2026-01-01"), equal: true, rule: "R-7"},
			TypeNumber:   {left: tvNumber(t, "3"), right: tvNumber(t, "3"), equal: true, rule: "R-1"},
			TypeMoney:    {left: tvMoney(t, "1.00", "USD"), right: tvMoney(t, "1.00", "USD"), equal: true, rule: "R-6"},
		},
		sweepGreater: {
			// R-10: "Acme Ltd" does contain the substring "Acme".
			TypeText: {left: tvText("Acme Ltd"), right: tvText("Acme"), containsScalar: true, rule: "R-10"},
			// R-5: "lost" is position 2, "won" is position 1, so lost > won.
			TypeEnum:     {left: tvEnum("lost"), right: tvEnum("won"), greater: true, rule: "R-5"},
			TypeRelation: {left: tvLink(TypeRelation, "Beta GmbH"), right: tvLink(TypeRelation, "Acme Ltd"), rule: "R-8"},
			TypePerson:   {left: tvLink(TypePerson, "Grace Hopper"), right: tvLink(TypePerson, "Ada Lovelace"), rule: "R-8 via ADR-068 D3"},
			TypeDate:     {left: tvDate(t, "2026-06-01"), right: tvDate(t, "2026-01-01"), greater: true, rule: "R-7"},
			// AC-8.3 lives here: 3 > 2 is TRUE.
			TypeNumber: {left: tvNumber(t, "3"), right: tvNumber(t, "2"), greater: true, rule: "AC-8.3"},
			TypeMoney:  {left: tvMoney(t, "2.00", "USD"), right: tvMoney(t, "1.00", "USD"), greater: true, rule: "R-6"},
		},
	}
}

// operandState is one row/column of the table: the seven declared types, plus
// absent, plus present-but-non-conforming. AC-8.1 requires all nine on both sides.
type operandState struct {
	name    string
	typ     PropertyType
	absent  bool
	nonConf bool
}

func operandStates() []operandState {
	states := make([]operandState, 0, 9)
	for _, t := range PropertyTypes {
		states = append(states, operandState{name: string(t), typ: t})
	}
	// The absent and non-conforming carriers must declare SOME type, because a
	// property always has one. R-2/R-3/R-4 preempt R-1's type check, so the
	// carrier cannot change any expected value —
	// TestComparison_AbsentAndNonConformingAreTypeIndependent proves that across
	// all seven carriers rather than assuming it.
	states = append(states,
		operandState{name: "absent", typ: TypeNumber, absent: true},
		operandState{name: "non_conforming", typ: TypeNumber, nonConf: true},
	)
	return states
}

func operandFor(s operandState, rows map[PropertyType]sweepRow, side string) PropertyValue {
	p := testProperty("fixture_"+string(s.typ), s.typ, false)
	switch {
	case s.absent:
		return absentOperand(p)
	case s.nonConf:
		return nonConformingOperand(p)
	default:
		if side == "left" {
			return present(p, rows[s.typ].left)
		}
		return present(p, rows[s.typ].right)
	}
}

// oracleExpect returns the expected boolean and the expected multiset of problem
// codes for one cell, computed ONLY from R-1..R-13 and the two hand-written
// tables. The ladder is the rules' precedence; each step cites its rule.
func oracleExpect(op Operator, l, r operandState, row sweepRow) (bool, []ComparisonProblemCode) {
	// R-3 — `is absent` is true exactly when the property has no value, and
	// false otherwise. It asks about one property, so only the left is consulted.
	if op == OpIsAbsent {
		return l.absent, nil
	}

	// R-2 — either side absent: false for every remaining operator, reported as
	// nothing, because absence is a state and not a defect.
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
	// and not a reported problem.
	if l.typ != r.typ {
		return false, nil
	}

	// Operators no rule defines for this type. SPEC GAP, REPORTED: §8 states no
	// outcome; the provisional behaviour is false plus a reported problem,
	// because §3's behavioural contract makes silence the defect.
	if !oracleDisposition[l.typ][op] {
		return false, []ComparisonProblemCode{CompareOperatorNotDefined}
	}

	switch op {
	case OpEqual:
		return row.equal, nil
	case OpLess:
		return row.less, nil
	case OpLessOrEqual:
		return row.less || row.equal, nil
	case OpGreater:
		return row.greater, nil
	case OpGreaterOrEqual:
		return row.greater || row.equal, nil
	case OpContains:
		return row.containsScalar, nil
	default:
		panic("oracle: operator not covered — the operator set changed without the oracle")
	}
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

// TestComparisonTruthTable is spec §7 test 6 and §8's first-class deliverable.
//
// AC-8.1: every declared type x every declared type x every operator, plus
// absent and non-conforming on both sides, with every expected value traced to a
// numbered rule through oracleExpect and the two hand-written tables above.
func TestComparisonTruthTable(t *testing.T) {
	c := testComparator()
	all := sweepRows(t)
	states := operandStates()
	sweeps := []sweep{sweepLess, sweepEqual, sweepGreater}

	cells := 0
	for _, sw := range sweeps {
		rows := all[sw]
		for _, l := range states {
			for _, r := range states {
				for _, op := range Operators {
					cells++
					lv := operandFor(l, rows, "left")
					rv := operandFor(r, rows, "right")

					wantResult, wantCodes := oracleExpect(op, l, r, rows[l.typ])
					gotResult, gotProblems := c.Evaluate(op, lv, rv)

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
	// AC-8.1's shape guard: adding a declared type or an operator without
	// extending the table changes this number, loudly.
	if wantCells != 3*9*9*7 {
		t.Fatalf("table shape changed: %d cells; AC-8.1 requires 9 operand states x 9 x every operator", wantCells)
	}
}

// TestComparison_ThreeGreaterThanTwo is AC-8.3, asserted by name because it is
// the case that actually failed during research: an overload accepting `any`
// made 3 > 2 evaluate to false with nothing reporting an error.
func TestComparison_ThreeGreaterThanTwo(t *testing.T) {
	c := testComparator()
	p := testProperty("amount", TypeNumber, false)
	three := present(p, tvNumber(t, "3"))
	two := present(p, tvNumber(t, "2"))

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
	if cmp, ok := Compare(tvNumber(t, "3"), tvNumber(t, "2")); !ok || cmp <= 0 {
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
			if op == OpIsAbsent {
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
}

// TestComparison_R1_DifferentDeclaredTypes is R-1, with the rule's own worked
// example: `"3" > 2` is false because one is text and one is a number.
func TestComparison_R1_DifferentDeclaredTypes(t *testing.T) {
	c := testComparator()
	textThree := present(testProperty("label", TypeText, false), tvText("3"))
	numberTwo := present(testProperty("amount", TypeNumber, false), tvNumber(t, "2"))

	got, problems := c.Evaluate(OpGreater, textThree, numberTwo)
	if got {
		t.Errorf(`R-1: "3" > 2 = true, want false`)
	}
	if len(problems) != 0 {
		t.Errorf("R-1: a cross-type comparison is an ordinary false, never an error; got %v", problemCodes(problems))
	}
	// A person and a relation are different declared types even though both hold
	// a wikilink, so R-1 applies before R-8 gets a chance to agree on identity.
	pv := present(testProperty("owner", TypePerson, false), tvLink(TypePerson, "Ada Lovelace"))
	rv := present(testProperty("company", TypeRelation, false), tvLink(TypeRelation, "Ada Lovelace"))
	if got, _ := c.Evaluate(OpEqual, pv, rv); got {
		t.Errorf("R-1: person and relation are different declared types and must not compare equal")
	}
}

// TestComparison_R2_AbsentIsFalseForEveryOperatorButIsAbsent is R-2, swept across
// every declared type on each side.
func TestComparison_R2_AbsentIsFalseForEveryOperatorButIsAbsent(t *testing.T) {
	c := testComparator()
	rows := sweepRows(t)[sweepEqual]
	for _, typ := range PropertyTypes {
		p := testProperty("fixture", typ, false)
		val := present(p, rows[typ].left)
		for _, op := range Operators {
			if op == OpIsAbsent {
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

// TestComparison_R3_IsAbsentDistinguishesEmptyFromMissing is R-3: an empty
// string, an empty list and a zero are VALUES, not absence.
func TestComparison_R3_IsAbsentDistinguishesEmptyFromMissing(t *testing.T) {
	c := testComparator()
	textProp := testProperty("note", TypeText, false)
	listProp := testProperty("tags", TypeText, true)
	numProp := testProperty("amount", TypeNumber, false)

	cases := []struct {
		name string
		v    PropertyValue
		want bool
	}{
		{"missing text", absentOperand(textProp), true},
		{"missing number", absentOperand(numProp), true},
		{"empty string", present(textProp, tvText("")), false},
		{"empty list", presentList(listProp), false},
		{"zero", present(numProp, tvNumber(t, "0")), false},
		{"non-conforming value is present, not absent", nonConformingOperand(numProp), false},
	}
	for _, tc := range cases {
		got, problems := c.Evaluate(OpIsAbsent, tc.v, absentOperand(textProp))
		if got != tc.want {
			t.Errorf("R-3: is_absent(%s) = %v, want %v", tc.name, got, tc.want)
		}
		if len(problems) != 0 {
			t.Errorf("R-3: is_absent(%s) reported %v, want none", tc.name, problemCodes(problems))
		}
	}
}

// TestComparison_R4_NonConformingIsFalseAndReported is R-4, swept across all
// seven declared types. "Silence here is the defect", so the problem list is
// asserted, not just the boolean.
func TestComparison_R4_NonConformingIsFalseAndReported(t *testing.T) {
	c := testComparator()
	rows := sweepRows(t)[sweepEqual]
	for _, typ := range PropertyTypes {
		p := testProperty("fixture", typ, false)
		bad := nonConformingOperand(p)
		good := present(p, rows[typ].left)
		for _, op := range Operators {
			if op == OpIsAbsent {
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
		{testProperty("amount", TypeNumber, false), "a number property holding prose"},
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

// TestComparison_R5_EnumOrdersByDeclaredPositionNotSpelling is R-5. The fixture's
// declared order deliberately contradicts lexical order, so a comparator that
// sorts enum values as strings fails here.
func TestComparison_R5_EnumOrdersByDeclaredPositionNotSpelling(t *testing.T) {
	c := testComparator()
	p := testProperty("stage", TypeEnum, false)
	lead := present(p, tvEnum("lead"))
	won := present(p, tvEnum("won"))
	lost := present(p, tvEnum("lost"))

	// Declared order: lead(0) < won(1) < lost(2). Lexical: lead < lost < won.
	if got, _ := c.Evaluate(OpLess, won, lost); !got {
		t.Errorf("R-5: won < lost = false, want true (declared position 1 < 2); a lexical comparator gets this wrong")
	}
	if got, _ := c.Evaluate(OpGreater, lost, won); !got {
		t.Errorf("R-5: lost > won = false, want true (declared position 2 > 1)")
	}
	if got, _ := c.Evaluate(OpLess, lead, won); !got {
		t.Errorf("R-5: lead < won = false, want true")
	}
	if got, _ := c.Evaluate(OpEqual, won, won); !got {
		t.Errorf("R-5: won == won = false, want true")
	}
	if got, _ := c.Evaluate(OpGreaterOrEqual, lost, lost); !got {
		t.Errorf("R-5: lost >= lost = false, want true")
	}
	// FR-010's sort key is the declared position, and SortByEnumOrder must agree
	// with the comparator rather than being a second, divergent ordering.
	names := []string{"lost", "lead", "won"}
	SortByEnumOrder(p, names)
	if strings.Join(names, ",") != "lead,won,lost" {
		t.Errorf("R-5/FR-010: SortByEnumOrder gave %v, want [lead won lost]", names)
	}
	// R-5's equality is EXACT-CASE against the declared set. A declared set may
	// legitimately contain two values differing only in case — D4's own example
	// of the failure it prevents is a column holding `Won`, `won` and
	// `Closed Won` — and when it does, they are TWO values, not one. A
	// case-insensitive equality would silently merge them.
	caseSet := []EnumValue{{Name: "won", Position: 0}, {Name: "Won", Position: 1}}
	caseProp := &Property{Name: "stage_case", Type: TypeEnum, Values: caseSet, RecordType: "fixture"}
	lower := present(caseProp, TypedValue{Type: TypeEnum, Raw: "won", Enum: caseSet[0]})
	upper := present(caseProp, TypedValue{Type: TypeEnum, Raw: "Won", Enum: caseSet[1]})
	if got, _ := c.Evaluate(OpEqual, lower, upper); got {
		t.Errorf(`R-5: "won" == "Won" = true, want false — equality is exact-case against the declared set`)
	}
	if got, _ := c.Evaluate(OpLess, lower, upper); !got {
		t.Errorf(`R-5: "won"(0) < "Won"(1) = false, want true — ordering is by declared position`)
	}

	// R-5's "declared position" only means something when both operands are drawn
	// from ONE declared set. FR-009 scopes a property to its record type, so
	// FR-023 validation should reject a cross-set comparison before evaluation;
	// this asserts the comparator's backstop reports rather than inventing an
	// ordering between two unrelated position numbers.
	// SPEC GAP, REPORTED: §8 states no outcome for this case.
	otherSet := []EnumValue{{Name: "todo", Position: 0}, {Name: "doing", Position: 1}, {Name: "done", Position: 2}}
	otherProp := &Property{Name: "status", Type: TypeEnum, Values: otherSet, RecordType: "fixture"}
	crossGot, crossProblems := c.Evaluate(OpLess, won,
		present(otherProp, TypedValue{Type: TypeEnum, Raw: "doing", Enum: otherSet[1]}))
	if crossGot {
		t.Errorf("R-5: two different declared sets compared true, want false")
	}
	if len(crossProblems) != 1 || crossProblems[0].Code != CompareEnumSetsDiffer {
		t.Errorf("R-5: two different declared sets reported %v, want one enum_sets_differ", problemCodes(crossProblems))
	}
	// The same holds for equality, and inside R-9 membership.
	if _, eqProblems := c.Evaluate(OpEqual, won,
		present(otherProp, TypedValue{Type: TypeEnum, Raw: "done", Enum: otherSet[2]})); len(eqProblems) != 1 {
		t.Errorf("R-5: cross-set equality reported %v, want one problem", problemCodes(eqProblems))
	}

	// A case variant of a value NOT in the declared set is not a value at all:
	// it is non-conformance (FR-011), reported.
	src := []byte("---\nstage: Won\n---\n")
	pv := ResolveProperty(ParseRecord("f.md", src), p)
	if pv.State != StateNonConforming {
		t.Errorf(`R-5/FR-011: "Won" against a set declaring "won" resolved to %s, want non-conforming`, pv.State)
	}
}

// TestComparison_R6_MoneyComparesOnlyWithinOneCurrency is R-6: across currencies
// EVERY operator is false and the currencies present are reported.
func TestComparison_R6_MoneyComparesOnlyWithinOneCurrency(t *testing.T) {
	c := testComparator()
	p := testProperty("value", TypeMoney, false)
	usd := present(p, tvMoney(t, "100.00", "USD"))
	eur := present(p, tvMoney(t, "100.00", "EUR"))

	for _, op := range Operators {
		if op == OpIsAbsent {
			continue
		}
		got, problems := c.Evaluate(op, usd, eur)
		if got {
			t.Errorf("R-6: USD %s EUR = true, want false for every operator", op)
		}
		if op == OpContains {
			// `contains` is not defined for money at all, so the disposition
			// problem fires first. That cell is covered by the truth table.
			continue
		}
		if len(problems) != 1 || problems[0].Code != CompareCrossCurrency {
			t.Fatalf("R-6: USD %s EUR reported %v, want one cross_currency", op, problemCodes(problems))
		}
		if strings.Join(problems[0].Currencies, ",") != "EUR,USD" {
			t.Errorf("R-6: currencies present reported as %v, want [EUR USD]", problems[0].Currencies)
		}
	}

	// Within one currency money compares exactly, including across declared
	// scales, with no binary float anywhere in the path (FR-013, FR-020b).
	if got, _ := c.Evaluate(OpGreater, present(p, tvMoney(t, "100.00", "USD")), present(p, tvMoney(t, "99.99", "USD"))); !got {
		t.Errorf("R-6: 100.00 USD > 99.99 USD = false, want true")
	}
	if got, _ := c.Evaluate(OpEqual, present(p, tvMoney(t, "100.00", "USD")), present(p, tvMoney(t, "100.000", "USD"))); !got {
		t.Errorf("R-6/FR-013: 100.00 USD and 100.000 USD compared unequal across declared scales")
	}
	// 0.1 + 0.2 is not 0.3 in binary floating point; exact decimal gets it right.
	sum, err := mustDecimal(t, "0.1").Add(mustDecimal(t, "0.2"))
	if err != nil {
		t.Fatalf("FR-013: exact addition failed: %v", err)
	}
	third := TypedValue{Type: TypeMoney, Money: Money{Amount: sum, Currency: "USD"}}
	if got, _ := c.Evaluate(OpEqual, present(p, third), present(p, tvMoney(t, "0.30", "USD"))); !got {
		t.Errorf("FR-020b: 0.1 + 0.2 did not compare equal to 0.30 — a float crept into the path")
	}
}

func mustDecimal(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatalf("test fixture: %q is not a decimal: %v", s, err)
	}
	return d
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
	// The same instant written in two offsets.
	if got, _ := c.Evaluate(OpEqual, present(p, tvDate(t, "2026-01-01T09:00:00Z")), present(p, tvDate(t, "2026-01-01T10:00:00+01:00"))); !got {
		t.Errorf("R-7: the same instant in two offsets must compare equal")
	}
	if got, _ := c.Evaluate(OpGreater, present(p, tvDate(t, "2026-06-01")), present(p, tvDate(t, "2026-01-01"))); !got {
		t.Errorf("R-7: 2026-06-01 > 2026-01-01 = false, want true")
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
	if got, _ := c.Evaluate(OpEqual, a, other); got {
		t.Errorf("R-8: different targets compared equal, want unequal")
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
}

// TestComparison_R9_ListContainsIsWholeElement is R-9: `contains` on a list is
// whole-element membership and is NEVER substring matching. The fixture is
// chosen so a substring implementation returns true where the rule says false.
func TestComparison_R9_ListContainsIsWholeElement(t *testing.T) {
	c := testComparator()
	listProp := testProperty("tags", TypeText, true)
	scalarProp := testProperty("tags", TypeText, false)
	tags := presentList(listProp, tvText("Acme Ltd"), tvText("Beta GmbH"))

	if got, _ := c.Evaluate(OpContains, tags, present(scalarProp, tvText("Acme Ltd"))); !got {
		t.Errorf("R-9: list contains whole element 'Acme Ltd' = false, want true")
	}
	if got, _ := c.Evaluate(OpContains, tags, present(scalarProp, tvText("Acme"))); got {
		t.Errorf("R-9: list contains 'Acme' = true, want false — membership is whole-element, NEVER substring")
	}
	if got, _ := c.Evaluate(OpContains, tags, present(scalarProp, tvText("acme ltd"))); got {
		t.Errorf("R-9: membership is exact; a case variant must not match")
	}
	if got, _ := c.Evaluate(OpContains, presentList(listProp), present(scalarProp, tvText("anything"))); got {
		t.Errorf("R-9: an empty list contains nothing")
	}
	// Membership over relations resolves by target identity (R-8 inside R-9).
	relList := testProperty("companies", TypeRelation, true)
	relScalar := testProperty("companies", TypeRelation, false)
	rels := presentList(relList, tvLink(TypeRelation, "Acme Ltd"))
	if got, _ := c.Evaluate(OpContains, rels, present(relScalar, tvLink(TypeRelation, "acme ltd."))); !got {
		t.Errorf("R-9 with R-8: membership must resolve by target identity, not spelling")
	}
	if got, _ := c.Evaluate(OpContains, rels, present(relScalar, tvLink(TypeRelation, "Beta GmbH"))); got {
		t.Errorf("R-9 with R-8: a different target must not be a member")
	}
	// R-2 still governs inside a list: an absent needle matches nothing.
	if got, _ := c.Evaluate(OpContains, tags, absentOperand(scalarProp)); got {
		t.Errorf("R-2: contains with an absent right operand = true, want false")
	}
	// An ordering operator across the arity boundary is defined by no rule, and
	// says so rather than answering.
	_, problems := c.Evaluate(OpGreater, tags, present(scalarProp, tvText("Acme")))
	if len(problems) != 1 || problems[0].Code != CompareArityNotDefined {
		t.Errorf("arity: list > scalar reported %v, want one operator_not_defined_across_arity", problemCodes(problems))
	}
}

// TestComparison_R10_TextContainsIsCaseSensitiveSubstring is R-10.
func TestComparison_R10_TextContainsIsCaseSensitiveSubstring(t *testing.T) {
	c := testComparator()
	p := testProperty("name", TypeText, false)
	haystack := present(p, tvText("Acme Ltd"))

	if got, _ := c.Evaluate(OpContains, haystack, present(p, tvText("cme L"))); !got {
		t.Errorf("R-10: 'Acme Ltd' contains 'cme L' = false, want true (substring)")
	}
	if got, _ := c.Evaluate(OpContains, haystack, present(p, tvText("acme"))); got {
		t.Errorf("R-10: 'Acme Ltd' contains 'acme' = true, want false (case-sensitive)")
	}
	if got, _ := c.Evaluate(OpContains, haystack, present(p, tvText(""))); !got {
		t.Errorf("R-10: every string contains the empty string")
	}
	if got, _ := c.Evaluate(OpContains, haystack, present(p, tvText("Zeta"))); got {
		t.Errorf("R-10: 'Acme Ltd' contains 'Zeta' = true, want false")
	}
}

// TestComparison_R11_TotalAndNeverPanics is R-11: every type pair x every
// operator yields a boolean or a reported problem, with no third outcome. The
// truth table sweeps the well-formed space; this adds the degenerate operands a
// real caller can still produce.
func TestComparison_R11_TotalAndNeverPanics(t *testing.T) {
	c := testComparator()
	numProp := testProperty("amount", TypeNumber, false)
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
	operators := append(append([]Operator{}, Operators...), Operator("approximately"), Operator(""))

	for i, l := range degenerate {
		for j, r := range degenerate {
			for _, op := range operators {
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							t.Fatalf("R-11 VIOLATED: Evaluate(%s, degenerate[%d], degenerate[%d]) panicked: %v", op, i, j, rec)
						}
					}()
					_, _ = c.Evaluate(op, l, r)
				}()
			}
		}
	}

	// An operator outside the declared set is a reported problem, never a silent false.
	got, problems := c.Evaluate(Operator("approximately"), present(numProp, tvNumber(t, "3")), present(numProp, tvNumber(t, "3")))
	if got {
		t.Errorf("R-11: an unknown operator returned true")
	}
	if len(problems) != 1 || problems[0].Code != CompareUnknownOperator {
		t.Errorf("R-11: an unknown operator reported %v, want one unknown_operator", problemCodes(problems))
	}
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
	schema := &Schema{Type: "fixture"}
	amount := testProperty("amount", TypeNumber, false)
	stage := testProperty("stage", TypeEnum, false)
	schema.Properties = map[string]*Property{"amount": amount, "stage": stage}
	schema.PropertyOrder = []string{"amount", "stage"}

	rec := ParseRecord("f.md", []byte("---\ntype: fixture\namount: 3\nstage: lost\n---\n"))

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
		{stage, "won", OpGreater, true, "R-5 — lost(2) > won(1)"},
		{stage, "lost", OpEqual, true, "R-5"},
	} {
		f := Filter{Property: tc.prop.Name, Op: tc.op, Literal: tc.literal}
		_, parsed, err := f.Validate(schema)
		if err != nil {
			t.Fatalf("R-12: the literal %q did not validate: %v", tc.literal, err)
		}
		recordSide := ResolveProperty(rec, tc.prop)
		literalSide := present(tc.prop, *parsed)

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

// TestComparison_AbsentAndNonConformingAreTypeIndependent proves the claim the
// truth table relies on when it carries absent and non-conforming on one
// declared type: R-2, R-3 and R-4 preempt R-1's type check, so the carrier type
// cannot change any expected value.
func TestComparison_AbsentAndNonConformingAreTypeIndependent(t *testing.T) {
	c := testComparator()
	rows := sweepRows(t)[sweepEqual]
	for _, carrier := range PropertyTypes {
		carrierProp := testProperty("carrier", carrier, false)
		for _, other := range PropertyTypes {
			otherProp := testProperty("other", other, false)
			val := present(otherProp, rows[other].right)
			for _, op := range Operators {
				gotAbsent, absentProblems := c.Evaluate(op, absentOperand(carrierProp), val)
				wantAbsent := op == OpIsAbsent
				if gotAbsent != wantAbsent || len(absentProblems) != 0 {
					t.Errorf("R-2/R-3: absent(%s) %s %s = %v/%v, want %v/none",
						carrier, op, other, gotAbsent, problemCodes(absentProblems), wantAbsent)
				}
				if op == OpIsAbsent {
					continue
				}
				gotBad, badProblems := c.Evaluate(op, nonConformingOperand(carrierProp), val)
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
