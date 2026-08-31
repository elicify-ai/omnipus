// Omnipus — FR-143a: `isType` narrows the branch it guards, and the evaluator
// keeps the promise the narrowing makes.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// THE DEFECT THESE TESTS PIN
//
// The founder's own `06-Bases/Subscriptions.base` writes
//
//	monthly_cost: if(cost.isType("number"), if(cycle == "annual", cost / 12, cost), "")
//
// `subscription.cost` holds prose ("PLACEHOLDER — cost unknown") alongside
// numbers, so the import types it TEXT — correctly, and it says so. The author
// compensated exactly as an author should, by guarding the arithmetic with a
// runtime type test. The checker refused `cost / 12` anyway, because inside the
// branch the guard protects it still read `cost` as text: eight column and
// summary losses across one base, from one disagreement between a per-record
// test and a once-per-property type.
//
// Narrowing closes it. What these tests exist to stop is the CHEAPER version of
// the fix — narrowing the type check alone. That version type-checks the
// formula and then, because `evalIsType` could only ever answer the DECLARED
// type, produces a blank column on all 63 records where Obsidian shows 42
// numbers. It trades a named loss for a silent one, and no static test would
// see it. So the runtime half is asserted here at equal strength: the guard
// fires per record, the guarded branch receives the number, and the branch it
// does not guard never runs.
// ---------------------------------------------------------------------------

// narrowingFormula is the founder's expression with his property names mapped
// onto the shared fixture schema: `name` is the TEXT property holding mixed
// content, `stage` stands in for `cycle`.
const narrowingFormula = `if(name.isType("number"), if(stage == "annual", name / 12, name), "")`

// evalNarrowing evaluates narrowingFormula over one `name`/`stage` pair.
func evalNarrowing(t *testing.T, cost string, costAbsent bool, cycle string) FormulaResult {
	t.Helper()
	schema := formulaFixtureSchema()
	nameProp := schema.Properties["name"]
	stageProp := schema.Properties["stage"]
	props := map[string]PropertyValue{"stage": textValue(stageProp, cycle)}
	if costAbsent {
		props["name"] = absentValue(nameProp)
	} else {
		props["name"] = textValue(nameProp, cost)
	}
	return evalOne(t, narrowingFormula, fixtureCandidate{props: props})
}

// mustValidate returns the one declaration a source produces, failing with the
// refusal when it produces none.
func mustValidate(t *testing.T, src string) FormulaDecl {
	t.Helper()
	set, errs := ValidateFormulaSet(map[string]string{"f": src}, formulaFixtureSchema())
	if len(errs) != 0 {
		t.Fatalf("%q must validate; it was refused: %v", src, formulaErrorMessages(errs))
	}
	decl, ok := set.Get("f")
	if !ok {
		t.Fatalf("%q validated but no declaration was stored", src)
	}
	return decl
}

// mustRefuse returns the refusal a source produces, failing when it validates.
func mustRefuse(t *testing.T, src string) string {
	t.Helper()
	_, errs := ValidateFormulaSet(map[string]string{"f": src}, formulaFixtureSchema())
	if len(errs) == 0 {
		t.Fatalf("%q must be REFUSED — accepting it is a claim about the values this package cannot keep", src)
	}
	return strings.Join(formulaErrorMessages(errs), "; ")
}

// TestIsTypeNarrowing_TheGuardedBranchTypesTheOperandAsANumber is the headline:
// the founder's formula validates, with ONE static type.
func TestIsTypeNarrowing_TheGuardedBranchTypesTheOperandAsANumber(t *testing.T) {
	decl := mustValidate(t, narrowingFormula)

	// A formula has ONE static type (FR-143a). The then-branch produces a
	// number, and the `""` else-branch DECLINES under the guard, so the
	// formula's type is the then-branch's — exactly as a two-argument `if()`
	// would give it.
	if decl.Type != FormulaNumber {
		t.Errorf("the guarded formula's static type is %s, want %s — the whole point of narrowing is that the guarded branch produces a number", decl.Type, FormulaNumber)
	}
	if decl.Arity != ArityOne {
		t.Errorf("arity = %s, want %s: `cost` is a single-valued property and division does not build a list", decl.Arity, ArityOne)
	}
	// FR-144's documented default: a number no `round`/`toFixed` gave a scale
	// to crosses the boundary at ten decimal places.
	if decl.Scale != FormulaDefaultScale {
		t.Errorf("scale = %d, want %d (FR-144's documented default)", decl.Scale, FormulaDefaultScale)
	}
}

// TestIsTypeNarrowing_ReachesNoFurtherThanTheBranchItGuards is the other half of
// the claim, and the more important one.
//
// A narrowing that leaked would be worse than no narrowing: it would let
// arithmetic through in a position where the guard has vouched for NOTHING, and
// the evaluator would then meet a text value at a division. Every case here must
// still be REFUSED, by name.
func TestIsTypeNarrowing_ReachesNoFurtherThanTheBranchItGuards(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "the ELSE-branch is where the guard said the value is NOT a number",
			src:  `if(name.isType("number"), 1, name / 2)`,
			want: "left operand is text",
		},
		{
			name: "outside the `if` entirely",
			src:  `if(name.isType("number"), 1, 2) + name`,
			want: "right operand is text",
		},
		{
			name: "a DIFFERENT property in the guarded branch is not narrowed",
			src:  `if(name.isType("number"), stage / 2, "")`,
			want: "left operand is text",
		},
		{
			name: "the guard's own condition is not narrowed by itself",
			src:  `if(name.isType("number") == (name / 2 > 1), 1, 2)`,
			want: "left operand is text",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustRefuse(t, tc.src); !strings.Contains(got, tc.want) {
				t.Errorf("refusal = %q, want it to name %q — a refusal that does not say WHICH operand is text sends the author to the wrong place", got, tc.want)
			}
		})
	}
}

// TestIsTypeNarrowing_OnlyTheNumberGuardWrittenDirectlyOverAPropertyNarrows
// pins how far the rule goes.
//
// Each shape below is a promise the evaluator would have to keep, and none of
// them is mirrored there. An unadmitted shape REFUSES — the safe direction —
// rather than being silently mistyped, and this test is what stops a later
// change from admitting one without also teaching `evalIf` about it.
func TestIsTypeNarrowing_OnlyTheNumberGuardWrittenDirectlyOverAPropertyNarrows(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// `string` is not narrowed: the conversion a declared date or number
			// would need to become text is not defined in this package, and no
			// loss asks for it.
			name: `isType("string") does not narrow`,
			src:  `if(name.isType("string"), name / 2, "")`,
		},
		{
			// `list` is not narrowed: arity comes from the DECLARATION at
			// runtime, so a single-valued property can never answer true to
			// this guard. Narrowing it would be a static promise about a branch
			// that cannot run.
			name: `isType("list") does not narrow arity`,
			src:  `if(name.isType("list"), name.length, 1)`,
		},
		{
			name: "a negated guard does not narrow the else-branch",
			src:  `if(!name.isType("number"), "", name / 12)`,
		},
		{
			name: "a guard combined with another condition does not narrow",
			src:  `if(name.isType("number") == true, name / 12, "")`,
		},
		{
			name: "a `file.*` reference is not narrowed",
			src:  `if(file.name.isType("number"), file.name / 2, "")`,
		},
		{
			// A date can never READ as a number, so there is nothing to narrow
			// it to and the arithmetic stays refused by name.
			name: "a property this checker typed as a date is not narrowed",
			src:  `if(due.isType("number"), due / 2, "")`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mustRefuse(t, tc.src)
		})
	}
}

// TestIsTypeNarrowing_TheEmptyElseLiteralDeclinesOnlyUnderTheGuard pins the
// second rule and its confinement.
//
// `if(P.isType("number"), <number>, "")` is the upstream idiom for "the number
// where there is one, blank where there is not", and FR-143a already has a word
// for a branch that produces nothing. Reading `""` as absence in ANY `if` branch
// would be tidier and would cover thirteen more formulas in this vault — and it
// would also change `if(c, someText, "")`, which is valid today, from a present
// empty string to absence. Under FR-008 a negated filter RE-INCLUDES absence, so
// the general rule could hand back MORE rows than Obsidian on a view nobody was
// looking at. FR-105 says that trade is not available.
func TestIsTypeNarrowing_TheEmptyElseLiteralDeclinesOnlyUnderTheGuard(t *testing.T) {
	t.Run("under the guard it declines, and the then-branch decides the type", func(t *testing.T) {
		decl := mustValidate(t, `if(name.isType("number"), name, "")`)
		if decl.Type != FormulaNumber {
			t.Errorf("type = %s, want %s", decl.Type, FormulaNumber)
		}
	})

	t.Run("without a guard, disagreeing branches are still refused", func(t *testing.T) {
		got := mustRefuse(t, `if(amount > 1, 1, "")`)
		if !strings.Contains(got, "branches disagree") {
			t.Errorf("refusal = %q, want FR-143a's branch-disagreement refusal — the empty-literal rule must not have escaped the guard", got)
		}
	})

	t.Run("a NON-empty else literal under a guard is still text, and still disagrees", func(t *testing.T) {
		got := mustRefuse(t, `if(name.isType("number"), name / 12, "n/a")`)
		if !strings.Contains(got, "branches disagree") {
			t.Errorf("refusal = %q, want FR-143a's branch-disagreement refusal: only `\"\"` declines, not any text literal", got)
		}
	})
}

// TestIsTypeNarrowing_TheEvaluatorKeepsThePromiseTheNarrowingMakes is the test
// that decides whether the static rule is honest.
//
// A static narrowing is the claim that the guarded branch CANNOT see a
// non-number. The values below are the founder's own: a monthly cost, an annual
// one that must be divided, and two records whose `cost` is prose.
func TestIsTypeNarrowing_TheEvaluatorKeepsThePromiseTheNarrowingMakes(t *testing.T) {
	t.Run("a value that reads as a number takes the guarded branch", func(t *testing.T) {
		res := evalNarrowing(t, "20.60", false, "monthly")
		if res.Absent {
			t.Fatal("a monthly cost of 20.60 must produce a value; absence here means the guard never fired and the column is blank on every record")
		}
		if got := renderNumber(t, res); got != "20.6" {
			t.Errorf("monthly 20.60 = %s, want 20.6 — a monthly cost is carried through unchanged", got)
		}
		// And the value crosses the boundary at FR-144's default scale, which
		// renderNumber deliberately trims away. Asserted here on the raw
		// rendering so the scale is part of the contract too: a bound narrowed
		// value must be indistinguishable from a declared number, and a value
		// that arrived at scale 0 would still pass the assertion above.
		if raw := res.Values()[0].Number.String(); raw != "20.6000000000" {
			t.Errorf("monthly 20.60 rendered %q, want %q at FR-144's default scale of 10", raw, "20.6000000000")
		}
	})

	t.Run("the annual branch divides, exactly", func(t *testing.T) {
		// 99 / 12 is exactly 8.25 — a terminating decimal, so it is NOT
		// labelled rounded. That second assertion is the one that separates
		// exact rational arithmetic from decimal arithmetic at scale.
		res := evalNarrowing(t, "99", false, "annual")
		if got := renderNumber(t, res); got != "8.25" {
			t.Errorf("annual 99 = %s, want 8.25 (99/12)", got)
		}
		if res.Rounded {
			t.Error("99/12 is exactly 8.25; labelling it rounded is as wrong as failing to label a value that is")
		}
	})

	t.Run("a division that does not terminate is rounded AND labelled", func(t *testing.T) {
		// 20.60 / 12 = 1.71666…; at FR-144's default scale 10, round-half-even
		// carries the last digit to 7.
		res := evalNarrowing(t, "20.60", false, "annual")
		if got := renderNumber(t, res); got != "1.7166666667" {
			t.Errorf("annual 20.60 = %s, want 1.7166666667", got)
		}
		if !res.Rounded {
			t.Error("FR-144: a rounded value MUST be labelled as rounded")
		}
	})

	t.Run("the guarded branch is NEVER evaluated when the guard is false", func(t *testing.T) {
		// THE LAZINESS ASSERTION, with an oracle that can actually see it.
		//
		// A static narrowing is the claim that the guarded branch cannot meet a
		// non-number. Laziness is what makes the claim true, so a test has to
		// be able to tell "the branch did not run" from "the branch ran and
		// produced nothing" — and arithmetic over a text operand produces
		// nothing QUIETLY, so it cannot be that oracle.
		//
		// Division by zero can: FR-144 makes it an absent result plus a NAMED
		// problem. Put one in the guarded branch, over a property the guard has
		// nothing to do with, and its problem is present if and only if the
		// branch was evaluated.
		schema := formulaFixtureSchema()
		src := `if(name.isType("number"), amount / 0, "")`
		c := fixtureCandidate{props: map[string]PropertyValue{
			"name":   textValue(schema.Properties["name"], "PLACEHOLDER — cost unknown"),
			"amount": numberValue(t, schema.Properties["amount"], "5"),
		}}
		res := evalOne(t, src, c)
		for _, p := range res.Problems {
			if strings.Contains(p.Detail, "division by zero") {
				t.Fatalf("the guard is FALSE and the guarded branch was evaluated anyway (%q). Eager evaluation turns this narrowing from a static guarantee into a runtime surprise — strictly worse than the refusal it replaced", p.Detail)
			}
		}

		// And the same expression WITH the guard true does report it, so the
		// absence above is laziness rather than a problem list that never fills.
		c.props["name"] = textValue(schema.Properties["name"], "42")
		lit := evalOne(t, src, c)
		saw := false
		for _, p := range lit.Problems {
			if strings.Contains(p.Detail, "division by zero") {
				saw = true
			}
		}
		if !saw {
			t.Fatal("with the guard TRUE the guarded branch's division by zero was not reported — the oracle above cannot see an eager evaluation, so its silence proves nothing")
		}
	})

	t.Run("prose takes the else-branch and produces absence, quietly", func(t *testing.T) {
		for _, prose := range []string{"PLACEHOLDER — cost unknown", "usage-based; US$20.60 (Apr)", ""} {
			res := evalNarrowing(t, prose, false, "annual")
			if !res.Absent {
				t.Errorf("cost=%q produced %v; the guard is false, so the else-branch's `\"\"` declines and the result is absence", prose, res.Values())
			}
			if len(res.Problems) != 0 {
				t.Errorf("cost=%q recorded %d problem(s) (%+v) — the guarded arithmetic must never be evaluated at all", prose, len(res.Problems), res.Problems)
			}
		}
	})

	t.Run("an absent cost is absence, not zero", func(t *testing.T) {
		res := evalNarrowing(t, "", true, "annual")
		if !res.Absent || len(res.Values()) != 0 {
			t.Errorf("an absent cost produced %v; R-14 requires absence to propagate", res.Values())
		}
	})

	t.Run("no value the formula produces is ever text", func(t *testing.T) {
		// FR-143a's whole purpose: the declaration is `number`, so a TypedValue
		// of any other type reaching a consumer is "a wrong answer wearing a
		// type system". The `""` branch is the one that could produce one.
		for _, cost := range []string{"20.60", "99", "PLACEHOLDER — cost unknown", ""} {
			res := evalNarrowing(t, cost, false, "annual")
			for _, v := range res.Values() {
				if v.Type != TypeDecimal {
					t.Errorf("cost=%q produced a %s value (%+v) from a formula declared %s", cost, v.Type, v, FormulaNumber)
				}
			}
		}
	})
}

// TestIsType_NumberIsAPerRecordReadingNotTheDeclaration is the change that makes
// the guard worth guarding with.
//
// The oracle is a CHANGE OF ANSWER over the same formula, the same property and
// the same declaration. Before this change `isType("number")` could only ever
// answer the property's declared type, so for a text-typed property it was the
// constant `false` — a guard that never fires. A single-record test would have
// passed identically against that constant.
func TestIsType_NumberIsAPerRecordReadingNotTheDeclaration(t *testing.T) {
	schema := formulaFixtureSchema()
	nameProp := schema.Properties["name"]
	ask := func(value string) bool {
		t.Helper()
		res := evalOne(t, `name.isType("number")`, fixtureCandidate{
			props: map[string]PropertyValue{"name": textValue(nameProp, value)},
		})
		if res.Absent || len(res.Values()) != 1 {
			t.Fatalf("isType over %q produced no answer; it must be a plain boolean, never absent", value)
		}
		return res.Values()[0].Bool
	}

	if !ask("42") {
		t.Error(`a text-declared property holding "42" must answer TRUE to isType("number") — upstream reads that YAML scalar as a number, and a guard that answers false for it can never fire`)
	}
	if ask("PLACEHOLDER — cost unknown") {
		t.Error(`prose must answer FALSE to isType("number")`)
	}

	t.Run("absence is FALSE, not absent", func(t *testing.T) {
		// R-14's one documented exception. An absent answer would send
		// `!cost.isType("number")` absent on exactly the records it was
		// written to catch.
		res := evalOne(t, `name.isType("number")`, fixtureCandidate{
			props: map[string]PropertyValue{"name": absentValue(nameProp)},
		})
		if res.Absent {
			t.Fatal("isType over an absent value must answer FALSE, not absence")
		}
		if res.Values()[0].Bool {
			t.Error("isType over an absent value answered true")
		}
	})

	t.Run("a list is only a list", func(t *testing.T) {
		// R-9, unchanged: a `many` property answers false to `number` whatever
		// its elements read as.
		sizes := schema.Properties["sizes"]
		res := evalOne(t, `sizes.isType("number")`, fixtureCandidate{
			props: map[string]PropertyValue{"sizes": numberValue(t, sizes, "4")},
		})
		if res.Values()[0].Bool {
			t.Error(`a many-valued property must answer FALSE to isType("number")`)
		}
	})

	t.Run("string remains the declaration test, deliberately", func(t *testing.T) {
		// PINNED ON PURPOSE. Making the two exclusive — text that reads as a
		// number is not a string — is the tidier partition and matches upstream
		// for an unquoted YAML number, but this package's record model has
		// already discarded the quoting that would tell `42` from `"42"`, so the
		// tidier rule would be guessing. It would also flip the branch taken by
		// every existing `isType("string")` guard over numeric-looking text,
		// which no loss requires. If a later change wants the partition, it
		// should have to come through this assertion deliberately.
		res := evalOne(t, `name.isType("string")`, fixtureCandidate{
			props: map[string]PropertyValue{"name": textValue(nameProp, "42")},
		})
		if !res.Values()[0].Bool {
			t.Error(`isType("string") over text "42" answered false; only the "number" answer was made a per-record reading`)
		}
	})
}

// TestIsTypeNarrowing_DoesNotCrossAFormulaBoundary is the runtime twin of
// narrowedFormulaEnv.LookupFormula.
//
// A sibling formula has its own declaration, inferred in its own environment. If
// the runtime binding stayed in place while that formula's tree was walked, the
// two would disagree — and the memo would then cache the sibling's narrowed
// value under its own name for every later reader, which is a wrong answer with
// nothing to notice.
func TestIsTypeNarrowing_DoesNotCrossAFormulaBoundary(t *testing.T) {
	schema := formulaFixtureSchema()
	nameProp := schema.Properties["name"]

	// `g` asks whether `name` is text. Evaluated in its own scope it is, because
	// `name` is a text property. Reached from inside the guarded branch of `f`,
	// it must give the SAME answer — the guard narrowed `f`'s tree, not `g`'s.
	sources := map[string]string{
		"g": `name.isType("string")`,
		"f": `if(name.isType("number"), formula.g, false)`,
	}
	set, errs := ValidateFormulaSet(sources, schema)
	if len(errs) != 0 {
		t.Fatalf("the fixture must validate: %v", formulaErrorMessages(errs))
	}
	e := NewFormulaEvaluator(set, testComparator(), formulaTestNow())
	e.Begin(fixtureCandidate{props: map[string]PropertyValue{"name": textValue(nameProp, "42")}})

	res, ok := e.Evaluate("f")
	if !ok || res.Absent {
		t.Fatalf("f did not evaluate: ok=%v absent=%v", ok, res.Absent)
	}
	if !res.Values()[0].Bool {
		t.Error("`formula.g` answered as though `name` were a number inside the guarded branch — the narrowing leaked across a formula boundary, and the memo now holds g's narrowed answer for every later reader")
	}
}
