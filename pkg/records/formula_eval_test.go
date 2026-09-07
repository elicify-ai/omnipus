// Omnipus — spec test 95: FR-144, FR-145, R-14/R-15 — absence propagates and
// arithmetic is exact.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
	"time"
)

// formulaTestNow is the snapshotted instant every evaluation test uses.
//
// FR-146's last clause requires `now()`/`today()` to be snapshotted ONCE per
// query so one response is internally consistent. A fixed instant is also what
// makes the date assertions below checkable at all — a test calling time.Now()
// asserts nothing about a formula and everything about the clock.
//
// THE DATE IS DELIBERATELY IN THE PAST, AND THAT IS NOT COSMETIC. The first
// version of this fixture was the day it was written. A mutation replacing the
// snapshot with time.Now() then SURVIVED the whole suite, because every
// assertion about `today()` was comparing the fixture against a clock that
// agreed with it. A fixture that happens to match the environment is a fixture
// that tests the environment. Any instant far from "now" restores the
// discrimination; this one is arbitrary beyond being unmistakably not today.
func formulaTestNow() time.Time {
	return time.Date(2019, 3, 7, 14, 30, 0, 0, time.UTC)
}

// fixtureCandidate is a hand-built candidate: property name → resolved value.
type fixtureCandidate struct {
	props map[string]PropertyValue
	files map[string]PropertyValue
}

func (c fixtureCandidate) FormulaProperty(name string) (PropertyValue, bool) {
	pv, ok := c.props[name]
	return pv, ok
}

func (c fixtureCandidate) FormulaFileProperty(name string) (PropertyValue, bool) {
	pv, ok := c.files[name]
	return pv, ok
}

// absentValue is a property the record does not carry — R-3's absence.
func absentValue(p *Property) PropertyValue {
	return PropertyValue{Property: p, State: StateAbsent}
}

// numberValue is a present numeric property, parsed from its SOURCE TEXT so the
// fixture never constructs a number by a route a file could not take.
func numberValue(t *testing.T, p *Property, text string) PropertyValue {
	t.Helper()
	d, err := ParseDecimal(text)
	if err != nil {
		t.Fatalf("the fixture value %q is not a number: %v", text, err)
	}
	return PropertyValue{
		Property: p, State: StatePresent,
		Values: []TypedValue{{Type: p.Type, Number: d, Raw: text}},
	}
}

func textValue(p *Property, text string) PropertyValue {
	return PropertyValue{
		Property: p, State: StatePresent,
		Values: []TypedValue{{Type: TypeText, Text: text, Raw: text}},
	}
}

// evalOne validates a one-formula view and evaluates it over one candidate.
func evalOne(t *testing.T, src string, c FormulaCandidate) FormulaResult {
	t.Helper()
	set, errs := ValidateFormulaSet(map[string]string{"f": src}, formulaFixtureSchema())
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

// TestFormula_AbsencePropagatesAndArithmeticIsExact is spec test 95.
func TestFormula_AbsencePropagatesAndArithmeticIsExact(t *testing.T) {
	schema := formulaFixtureSchema()
	amount := schema.Properties["amount"]
	quantity := schema.Properties["quantity"]

	t.Run("R-14/FR-145 — an absent operand yields ABSENT, never zero", func(t *testing.T) {
		// The record carries no `amount`. Every arithmetic and function step
		// over it must produce absence. Zero would be the plausible wrong
		// answer, and it is the one that makes a budget report show a healthy
		// total.
		c := fixtureCandidate{props: map[string]PropertyValue{
			"amount":   absentValue(amount),
			"quantity": numberValue(t, quantity, "4"),
		}}

		for _, src := range []string{
			"amount + 1",
			"amount - 1",
			"amount * 2",
			"amount / 2",
			"quantity + amount",
			"round(amount)",
			"toFixed(amount, 2)",
			"-amount",
		} {
			res := evalOne(t, src, c)
			if !res.Absent {
				t.Errorf("R-14: %q over an absent operand produced a VALUE (%v). Absence must propagate — a zero here is a wrong answer nobody can see",
					src, res.Values())
			}
			if len(res.Values()) != 0 {
				t.Errorf("R-14: %q produced %d value(s) while claiming absence", src, len(res.Values()))
			}
		}
	})

	t.Run("R-14 — if() is the ONE sanctioned way to give absence a value", func(t *testing.T) {
		c := fixtureCandidate{props: map[string]PropertyValue{
			"amount":   absentValue(amount),
			"quantity": numberValue(t, quantity, "4"),
		}}
		// "if() treats an absent condition as false." The condition
		// `amount > 1` is absent, so the ELSE branch is taken.
		res := evalOne(t, "if(amount > 1, 100, 7)", c)
		if res.Absent {
			t.Fatal("R-14: if() gives absence a value; this must not be absent")
		}
		if got := renderNumber(t, res); got != "7" {
			t.Errorf("R-14: an absent condition is FALSE, so the else-branch is taken: got %s, want 7", got)
		}
	})

	t.Run("R-15/FR-144 — arithmetic is EXACT, and the boundary is the only rounding", func(t *testing.T) {
		c := fixtureCandidate{props: map[string]PropertyValue{}}

		// THE CASE THAT SEPARATES EXACT RATIONALS FROM DECIMAL ARITHMETIC.
		// (1/3) * 3 is exactly 1 over rationals. Computed as decimals at any
		// scale it is 0.999…, because 1/3 has no exact base-10 form and the
		// error compounds through the multiplication.
		res := evalOne(t, "(1 / 3) * 3", c)
		if got := renderNumber(t, res); got != "1" {
			t.Errorf("FR-144: (1/3)*3 rendered %s, want 1. Anything else means the division rounded MID-EXPRESSION rather than at the boundary", got)
		}
		if res.Rounded {
			t.Error("FR-144: (1/3)*3 is exact; labelling it rounded is as wrong as failing to label a value that is")
		}

		// And 1/3 on its own DOES round, at the documented default scale of 10,
		// and says so. The digits are derived from the specification's rule
		// (scale 10, round-half-even), not from running the evaluator.
		res = evalOne(t, "1 / 3", c)
		if got := renderNumber(t, res); got != "0.3333333333" {
			t.Errorf("FR-144: 1/3 at the documented default scale 10 rendered %s, want 0.3333333333", got)
		}
		if !res.Rounded {
			t.Error("FR-144: a rounded value MUST be labelled as rounded — an unlabelled rounding is the failure FR-152 records")
		}
		if res.Scale != 10 {
			t.Errorf("FR-144's documented default scale is 10; the result declares %d", res.Scale)
		}

		// 2/3 exercises round-HALF-EVEN's direction at the last digit:
		// 0.666…6 rounds UP to …667, which half-away-from-zero also does — so
		// the discriminating case is the exact half below.
		res = evalOne(t, "2 / 3", c)
		if got := renderNumber(t, res); got != "0.6666666667" {
			t.Errorf("FR-144: 2/3 at scale 10 rendered %s, want 0.6666666667", got)
		}
	})

	t.Run("R-15 — round-half-even, not half-away-from-zero", func(t *testing.T) {
		c := fixtureCandidate{}
		// The exact halves. Half-EVEN sends 0.5 to 0 and 1.5 to 2; the rounding
		// every convenience function in the standard library would have given
		// instead sends 0.5 to 1. Both are defensible; only one is DECLARED,
		// and a test that does not pin it lets the other one land silently.
		for _, tc := range []struct{ src, want string }{
			{"round(0.5)", "0"},
			{"round(1.5)", "2"},
			{"round(2.5)", "2"},
			{"round(3.5)", "4"},
			{"round(-0.5)", "0"},
			{"round(-1.5)", "-2"},
			{"toFixed(0.125, 2)", "0.12"},
			{"toFixed(0.135, 2)", "0.14"},
		} {
			res := evalOne(t, tc.src, c)
			if got := renderNumber(t, res); got != tc.want {
				t.Errorf("FR-144: %s rendered %s, want %s (round-half-even)", tc.src, got, tc.want)
			}
		}
	})

	t.Run("FR-144 — division by zero is a NAMED problem, never a silent zero", func(t *testing.T) {
		c := fixtureCandidate{props: map[string]PropertyValue{
			"quantity": numberValue(t, quantity, "0"),
			"amount":   numberValue(t, amount, "10"),
		}}
		res := evalOne(t, "amount / quantity", c)
		if !res.Absent {
			t.Fatalf("FR-144: division by zero must yield ABSENT; got %v", res.Values())
		}
		if len(res.Problems) == 0 {
			t.Fatal("FR-144: division by zero must be a NAMED problem. An absent result with no problem is indistinguishable from a record that simply had no amount")
		}
		joined := problemText(res.Problems)
		if !strings.Contains(joined, "division by zero") {
			t.Errorf("the problem does not name the cause; got: %s", joined)
		}
	})

	t.Run("FR-144 — if() is LAZY, so a guard against division by zero works", func(t *testing.T) {
		c := fixtureCandidate{props: map[string]PropertyValue{
			"quantity": numberValue(t, quantity, "0"),
			"amount":   numberValue(t, amount, "10"),
		}}
		// The remedy the division-by-zero refusal itself recommends. If the
		// branches were evaluated eagerly the guard would be useless AND the
		// problem list would be dishonest — reporting a division that the
		// author correctly prevented.
		res := evalOne(t, "if(quantity != 0, amount / quantity, 0)", c)
		if res.Absent {
			t.Fatal("the guarded expression must produce a value")
		}
		if got := renderNumber(t, res); got != "0" {
			t.Errorf("the guarded expression rendered %s, want 0", got)
		}
		if len(res.Problems) != 0 {
			t.Errorf("FR-144: the branch NOT taken must not be evaluated, so no problem should be raised; got: %s", problemText(res.Problems))
		}
	})

	t.Run("FR-144 — %% over a non-whole VALUE is a named problem at evaluation", func(t *testing.T) {
		// The static check in formula_type.go catches a fractional LITERAL.
		// A fractional PROPERTY value is only knowable per record, and it must
		// produce the same refusal wording rather than a truncation.
		c := fixtureCandidate{props: map[string]PropertyValue{
			"amount":   numberValue(t, amount, "7.5"),
			"quantity": numberValue(t, quantity, "2"),
		}}
		res := evalOne(t, "amount % quantity", c)
		if !res.Absent {
			t.Fatalf("FR-144: `%%` over a fractional operand must yield absent, not a truncated answer; got %v", res.Values())
		}
		if !strings.Contains(problemText(res.Problems), "round()") {
			t.Errorf("FR-144: the problem must name round() as the remedy; got: %s", problemText(res.Problems))
		}
	})

	t.Run("FR-146 — now() and today() are snapshotted ONCE per query", func(t *testing.T) {
		set, errs := ValidateFormulaSet(map[string]string{
			"n": "now()",
			"d": "today()",
		}, schema)
		if len(errs) != 0 {
			t.Fatalf("now()/today() must validate: %v", formulaErrorMessages(errs))
		}
		e := NewFormulaEvaluator(set, testComparator(), formulaTestNow())

		var firstNow, firstToday string
		for i := 0; i < 3; i++ {
			e.Begin(fixtureCandidate{})
			n, _ := e.Evaluate("n")
			d, _ := e.Evaluate("d")
			gotNow := n.Values()[0].Date.String()
			gotToday := d.Values()[0].Date.String()
			if i == 0 {
				firstNow, firstToday = gotNow, gotToday
				continue
			}
			if gotNow != firstNow || gotToday != firstToday {
				t.Fatalf("FR-146: candidate %d saw now()=%s today()=%s, candidate 0 saw %s / %s. One response must be internally consistent, or a query spanning midnight puts some records on each side of `due < today()`",
					i, gotNow, gotToday, firstNow, firstToday)
			}
		}
		// And the snapshot is the instant that was PASSED IN, not a clock
		// read. The fixture date is years in the past precisely so this
		// assertion can tell the two apart — see formulaTestNow.
		if firstToday != "2019-03-07" {
			t.Errorf("today() rendered %s, want 2019-03-07 — the evaluator must use the instant it was GIVEN, not read the clock", firstToday)
		}
		if !strings.HasPrefix(firstNow, "2019-03-07T14:30:00") {
			t.Errorf("now() rendered %s, want the 2019-03-07T14:30:00Z instant the evaluator was given", firstNow)
		}
	})

	t.Run("FR-146 — a formula is MEMOIZED once per candidate", func(t *testing.T) {
		// Memoization is a REQUIREMENT, not an optimisation: without it the
		// per-candidate cost is multiplicative and FR-146's 16M bound describes
		// no work anybody does.
		//
		// It is observable through the candidate: `base` names `amount` once,
		// and `top` names `base` twice. With memoization the candidate is asked
		// for `amount` ONCE; without it, twice.
		set, errs := ValidateFormulaSet(map[string]string{
			"base": "amount * 2",
			"top":  "formula.base + formula.base",
		}, schema)
		if len(errs) != 0 {
			t.Fatalf("the fixture must validate: %v", formulaErrorMessages(errs))
		}
		counting := &countingCandidate{inner: fixtureCandidate{props: map[string]PropertyValue{
			"amount": numberValue(t, amount, "5"),
		}}}
		e := NewFormulaEvaluator(set, testComparator(), formulaTestNow())
		e.Begin(counting)
		res, _ := e.Evaluate("top")

		if got := renderNumber(t, res); got != "20" {
			t.Fatalf("(5*2) + (5*2) rendered %s, want 20", got)
		}
		if counting.reads["amount"] != 1 {
			t.Errorf("FR-146: `amount` was read %d times for one candidate, want 1. A formula referenced twice must be evaluated ONCE and reused",
				counting.reads["amount"])
		}

		// And the memo is CLEARED per candidate: the second candidate must see
		// its own value, not the first one's.
		second := &countingCandidate{inner: fixtureCandidate{props: map[string]PropertyValue{
			"amount": numberValue(t, amount, "1"),
		}}}
		e.Begin(second)
		res, _ = e.Evaluate("top")
		if got := renderNumber(t, res); got != "4" {
			t.Fatalf("the second candidate rendered %s, want 4 — Begin() must clear the memo, or every candidate receives the first one's values with no error anywhere", got)
		}
	})

	t.Run("FR-011a — contains() folds through FoldKey, not through ToLower", func(t *testing.T) {
		// value.go's executed table is the oracle here:
		//
		//	pair                 strings.ToLower   cases.Fold
		//	straße / STRASSE     false             TRUE
		//
		// The discriminating direction is the one with ß in the NEEDLE. Full
		// folding maps ß to `ss`, so FoldKey("STRAßE") is "strasse" and finds
		// it inside "hauptstrasse 5". strings.ToLower performs only SIMPLE
		// folding — a rune-for-rune map — so it produces "straße", which is not
		// in the haystack at all.
		//
		// A needle of "STRASSE" would NOT discriminate: both functions send it
		// to "strasse". This case is written the way it is because the obvious
		// one passes under either implementation, and a mutation swapping
		// FoldKey for ToLower survived the suite until this case existed.
		name := schema.Properties["name"]
		c := fixtureCandidate{props: map[string]PropertyValue{
			"name": textValue(name, "Hauptstrasse 5"),
		}}
		res := evalOne(t, `contains(name, "STRAßE")`, c)
		if res.Absent || len(res.Values()) == 0 {
			t.Fatal("contains() must produce a value")
		}
		if res.Values()[0].Raw != "true" {
			t.Errorf("FR-011a: contains(\"Hauptstrasse 5\", \"STRAßE\") answered %s, want true. A false means the formula layer folds with the standard library instead of FoldKey, and text comparison here would disagree with text comparison everywhere else in the package",
				res.Values()[0].Raw)
		}

		// The control, from the same table's third row: Turkish dotted İ must
		// NOT fold to i. A wrong MATCH is the failure direction nobody notices,
		// because it looks like a feature.
		c = fixtureCandidate{props: map[string]PropertyValue{
			"name": textValue(name, "istanbul"),
		}}
		res = evalOne(t, `contains(name, "İSTANBUL")`, c)
		if res.Values()[0].Raw != "false" {
			t.Errorf("FR-011a/AC-8.9e: contains(\"istanbul\", \"İSTANBUL\") answered true. Dotted İ and plain i are different letters in Turkish; folding them together is the classic Turkish-I bug, and strings.ToLower is what produces it")
		}
	})

	t.Run("FR-142 — a comparison inside an expression goes through the ONE comparator", func(t *testing.T) {
		// The discriminating case is one where a second, hand-written
		// implementation of `==` would disagree with the comparator: full
		// Unicode case folding. `straße` equals `STRASSE` under cases.Fold and
		// under NEITHER strings.ToLower nor strings.EqualFold (value.go's
		// table). A formula that answered `false` here would be running its own
		// comparison.
		name := schema.Properties["name"]
		c := fixtureCandidate{props: map[string]PropertyValue{
			"name": textValue(name, "straße"),
		}}
		res := evalOne(t, `name == "STRASSE"`, c)
		if res.Absent || len(res.Values()) == 0 {
			t.Fatal("the comparison must produce a value")
		}
		if res.Values()[0].Raw != "true" {
			t.Errorf("FR-142/FR-011a: `straße` == `STRASSE` answered %s, want true. A false here means the formula layer implemented its own comparison instead of delegating to the ONE comparator",
				res.Values()[0].Raw)
		}
	})

	t.Run("R-16 — a presentation value renders and does not compare", func(t *testing.T) {
		name := schema.Properties["name"]
		c := fixtureCandidate{props: map[string]PropertyValue{"name": textValue(name, "Acme")}}
		res := evalOne(t, "link(name)", c)
		if got := res.Display(); len(got) != 1 || got[0] != "[[Acme]]" {
			t.Errorf("link(name) rendered %v, want [[Acme]]", got)
		}
		if _, ok := res.PropertyValue("link(name)"); ok {
			t.Error("R-16/FR-215: a presentation result must have NO comparator operand — the refusal belongs in the type system, not in a runtime check somebody can forget to call")
		}
	})

	t.Run("the declaration the comparator sees carries the formula's ONE type", func(t *testing.T) {
		c := fixtureCandidate{props: map[string]PropertyValue{
			"amount": numberValue(t, amount, "3"),
		}}
		res := evalOne(t, "amount * 2", c)
		pv, ok := res.PropertyValue("amount * 2")
		if !ok {
			t.Fatal("a number result must have a comparator operand")
		}
		if pv.Property == nil {
			t.Fatal("FR-143a: the operand must carry a DECLARATION")
		}
		if pv.Property.Formula != "amount * 2" {
			t.Errorf("the declaration must carry the source that produced it; got %q", pv.Property.Formula)
		}
		if pv.Property.Type != TypeDecimal {
			t.Errorf("the declared type is %q, want %q", pv.Property.Type, TypeDecimal)
		}
		if pv.State != StatePresent {
			t.Errorf("the operand's state is %v, want present", pv.State)
		}
	})
}

// countingCandidate records how many times each property was read, which is how
// memoization is observed from outside.
type countingCandidate struct {
	inner fixtureCandidate
	reads map[string]int
}

func (c *countingCandidate) FormulaProperty(name string) (PropertyValue, bool) {
	if c.reads == nil {
		c.reads = map[string]int{}
	}
	c.reads[name]++
	return c.inner.FormulaProperty(name)
}

func (c *countingCandidate) FormulaFileProperty(name string) (PropertyValue, bool) {
	if c.reads == nil {
		c.reads = map[string]int{}
	}
	c.reads[name]++
	return c.inner.FormulaFileProperty(name)
}

func renderNumber(t *testing.T, res FormulaResult) string {
	t.Helper()
	vals := res.Values()
	if len(vals) != 1 {
		t.Fatalf("expected exactly one value, got %d", len(vals))
	}
	return trimDecimalZeros(vals[0].Number.String())
}

// trimDecimalZeros normalises `1.0000000000` to `1` so an assertion states the
// VALUE rather than the rendering scale. The scale itself is asserted
// separately, through FormulaResult.Scale.
func trimDecimalZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func problemText(problems []ComparisonProblem) string {
	parts := make([]string, 0, len(problems))
	for _, p := range problems {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, " | ")
}
