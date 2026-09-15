// Omnipus — FR-143's pinned snapshot, the parts a real vault actually uses:
// the duration type, its five fields, `list.length` and `isType`.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-143 pins the grammar to the Bases syntax reference as fetched 2026-08-30.
// The code's transcription of that snapshot carried ONE parenless accessor,
// `.hour`, and the snapshot documents seven: the Duration Type table's
// `.days`/`.hours`/`.minutes`/`.seconds`/`.milliseconds`, the List Functions
// section's `.length`, and `.hour`. It also documents `any.isType(type)`.
//
// The omission is not academic. Counted across the founder's eighteen real
// `.base` files: `.days` appears 65 times, `.length` once, `.isType(...)` once.
// A grammar without them cannot read a real vault at all.
//
// Every expected value below is derived BY HAND from the snapshot's own words —
// "Total days in duration", "Total hours in duration" — and from this package's
// stated rules (R-14's absence propagation, R-4's non-conformance, FR-144's
// declared scale). None of them was read off the implementation.
// ---------------------------------------------------------------------------

// TestFormula_DurationIsProducedBySubtractingDates is the snapshot's sentence
// "When subtracting two dates, the result is a Duration type (not a number)",
// asserted as behaviour.
func TestFormula_DurationIsProducedBySubtractingDates(t *testing.T) {
	schema := formulaFixtureSchema()
	c := fixtureCandidate{}

	t.Run("the five fields are total quantities, not calendar components", func(t *testing.T) {
		// 2026-09-05 00:00Z minus 2026-09-01 00:00Z is exactly four days.
		// Hand-derived from there: 4 × 24 = 96 hours, × 60 = 5,760 minutes,
		// × 60 = 345,600 seconds, × 1000 = 345,600,000 milliseconds.
		for _, tc := range []struct{ field, want string }{
			{"days", "4"},
			{"hours", "96"},
			{"minutes", "5760"},
			{"seconds", "345600"},
			{"milliseconds", "345600000"},
		} {
			src := `(date("2026-09-05") - date("2026-09-01")).` + tc.field
			res := evalOne(t, src, c)
			if got := renderNumber(t, res); got != tc.want {
				t.Errorf("%s = %s, want %s — the snapshot calls every duration field a TOTAL", src, got, tc.want)
			}
		}
	})

	t.Run("a fractional span is not silently truncated", func(t *testing.T) {
		// 2026-09-02 12:00Z minus 2026-09-01 00:00Z is 36 hours. As a TOTAL
		// number of days that is 1.5, and rounding it to 1 here would make
		// `formula.x > 1` answer differently from the original — the shape
		// FR-105 exists to forbid. The snapshot's own remedy for a whole
		// number is `.days.round(0)`, tested below.
		res := evalOne(t, `(date("2026-09-02T12:00:00Z") - date("2026-09-01T00:00:00Z")).days`, c)
		if got := renderNumber(t, res); got != "1.5" {
			t.Errorf("a 36-hour span is 1.5 days, got %s — a truncated day count is an invented answer", got)
		}
		rounded := evalOne(t, `(date("2026-09-02T12:00:00Z") - date("2026-09-01T00:00:00Z")).days.round(0)`, c)
		if got := renderNumber(t, rounded); got != "2" {
			t.Errorf("`.days.round(0)` is the snapshot's documented way to get a whole number; got %s, want 2", got)
		}
	})

	t.Run("the span is signed, in the order written", func(t *testing.T) {
		res := evalOne(t, `(date("2026-09-01") - date("2026-09-05")).days`, c)
		if got := renderNumber(t, res); got != "-4" {
			t.Errorf("a backwards span is negative — got %s, want -4", got)
		}
	})

	t.Run("absence propagates through the subtraction (R-14)", func(t *testing.T) {
		due := schema.Properties["due"]
		res := evalOne(t, "(today() - due).days", fixtureCandidate{
			props: map[string]PropertyValue{"due": absentValue(due)},
		})
		if !res.Absent {
			t.Errorf("R-14: a date that is not there gives no span and therefore no day count; got %v", res.Values())
		}
	})

	t.Run("a duration is a dead end everywhere except its own fields", func(t *testing.T) {
		// Each of these has a faithful rewrite through `.days`, so each is a
		// REFUSAL naming that rewrite rather than a value nobody can use. The
		// snapshot says the same thing: "Duration does NOT support .round(),
		// .floor(), .ceil() directly."
		for _, tc := range []struct{ src, why string }{
			{"(today() - due) > 30", "a duration has no comparator domain"},
			{"(today() - due) == (today() - due)", "the same, for equality"},
			{"(today() - due) + 1", "a duration is not a number"},
			{"round(today() - due)", "the snapshot says round() is not defined on a duration"},
			{"toFixed(today() - due, 2)", "nor is toFixed()"},
			{`contains(today() - due, "d")`, "a duration has no text form this package defines"},
			{`format(today() - due, "{}")`, "the same, for display"},
			{"mean(today() - due)", "a duration is not a list of numbers"},
			{"-(today() - due)", "unary minus is arithmetic"},
			{"(today() - due).days.days", "`.days` produces a number, and a number has no `.days`"},
			{"due.days", "`.days` is defined on a duration, not on a date"},
			{"today().length", "a date is not a list"},
		} {
			_, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema)
			if len(errs) == 0 {
				t.Errorf("%q must be refused (%s)", tc.src, tc.why)
				continue
			}
			if !hasCode(errs, FormulaErrType) && !hasCode(errs, FormulaErrUnknownReference) {
				t.Errorf("%q must be refused as a TYPE fault; got %v", tc.src, formulaErrorCodes(errs))
			}
		}
	})

	t.Run("a refused duration names the remedy", func(t *testing.T) {
		// A refusal that says only "wrong type" leaves the author with an
		// expression Obsidian accepts and no idea what to write instead.
		for _, src := range []string{"(today() - due) > 30", "(today() - due) + 1", "round(today() - due)"} {
			_, errs := ValidateFormulaSet(map[string]string{"f": src}, schema)
			if len(errs) == 0 {
				t.Fatalf("%q must be refused", src)
			}
			if !strings.Contains(errs[0].Error(), ".days") {
				t.Errorf("the refusal for %q must name `.days` as the remedy; got:\n  %s", src, errs[0].Error())
			}
		}
	})

	t.Run("a formula may not RESULT in a duration", func(t *testing.T) {
		// It would be a column that is always empty: a duration has no
		// PropertyType, so it can be neither rendered, sorted nor compared.
		_, errs := ValidateFormulaSet(map[string]string{"span": "today() - due"}, schema)
		if len(errs) != 1 {
			t.Fatalf("a duration-valued formula must be refused exactly once; got %v", formulaErrorMessages(errs))
		}
		if !strings.Contains(errs[0].Error(), ".days") {
			t.Errorf("the refusal must name the fields that make it usable; got:\n  %s", errs[0].Error())
		}
	})
}

// TestFormula_ListLengthCountsAList covers the snapshot's `list.length`.
func TestFormula_ListLengthCountsAList(t *testing.T) {
	schema := formulaFixtureSchema()
	sizes := schema.Properties["sizes"]
	backlinks := testProperty("file.backlinks", TypeRelation, true)

	t.Run("it counts the elements", func(t *testing.T) {
		res := evalOne(t, "file.backlinks.length", fixtureCandidate{
			files: map[string]PropertyValue{
				"file.backlinks": presentList(backlinks,
					tvLink(TypeRelation, "Acme Ltd"), tvLink(TypeRelation, "Beta GmbH")),
			},
		})
		if got := renderNumber(t, res); got != "2" {
			t.Errorf("two backlinks is a length of 2; got %s", got)
		}
		res = evalOne(t, "sizes.length", fixtureCandidate{
			props: map[string]PropertyValue{
				"sizes": presentList(sizes, tvNumber(t, TypeInteger, "1"), tvNumber(t, TypeInteger, "2"), tvNumber(t, TypeInteger, "3")),
			},
		})
		if got := renderNumber(t, res); got != "3" {
			t.Errorf("three elements is a length of 3; got %s", got)
		}
	})

	t.Run("an EMPTY list has length 0 and an ABSENT one has no length", func(t *testing.T) {
		// R-3: an empty list is a VALUE. R-14: absence is not, and answering 0
		// for it would be the absence-is-zero coercion R-14 forbids.
		empty := evalOne(t, "sizes.length", fixtureCandidate{
			props: map[string]PropertyValue{"sizes": presentList(sizes)},
		})
		if got := renderNumber(t, empty); got != "0" {
			t.Errorf("an empty list is present and its length is 0; got %s", got)
		}
		missing := evalOne(t, "sizes.length", fixtureCandidate{
			props: map[string]PropertyValue{"sizes": absentValue(sizes)},
		})
		if !missing.Absent {
			t.Errorf("R-14: a list that is not there has no length; got %v", missing.Values())
		}
	})

	t.Run("`.length` on a single value is refused", func(t *testing.T) {
		// `string.length` IS in the snapshot and is deliberately not
		// implemented: JavaScript counts UTF-16 code units and Go counts bytes
		// or runes. A refusal is honest; a length that means one of three
		// things is not.
		for _, src := range []string{"amount.length", "name.length", "due.length"} {
			_, errs := ValidateFormulaSet(map[string]string{"f": src}, schema)
			if len(errs) == 0 {
				t.Errorf("%q must be refused — `.length` counts a LIST", src)
			}
		}
	})
}

// TestFormula_IsTypeIsAGuardNotAValue covers the snapshot's
// `any.isType(type): boolean`.
func TestFormula_IsTypeIsAGuardNotAValue(t *testing.T) {
	schema := formulaFixtureSchema()
	amount := schema.Properties["amount"]
	name := schema.Properties["name"]
	sizes := schema.Properties["sizes"]

	t.Run("it answers about the value that is there", func(t *testing.T) {
		for _, tc := range []struct {
			src  string
			cand fixtureCandidate
			want bool
			why  string
		}{
			{`amount.isType("number")`, fixtureCandidate{props: map[string]PropertyValue{"amount": numberValue(t, amount, "12")}}, true, "a present decimal is a number"},
			{`amount.isType("string")`, fixtureCandidate{props: map[string]PropertyValue{"amount": numberValue(t, amount, "12")}}, false, "a number is not a string"},
			{`amount.isType("list")`, fixtureCandidate{props: map[string]PropertyValue{"amount": numberValue(t, amount, "12")}}, false, "a scalar is not a list"},
			{`name.isType("string")`, fixtureCandidate{props: map[string]PropertyValue{"name": textValue(name, "Acme")}}, true, "a present text is a string"},
			{`name.isType("number")`, fixtureCandidate{props: map[string]PropertyValue{"name": textValue(name, "Acme")}}, false, "text is not a number"},
			{`sizes.isType("list")`, fixtureCandidate{props: map[string]PropertyValue{"sizes": presentList(sizes, tvNumber(t, TypeInteger, "1"))}}, true, "a `many` property is a list even with one element"},
			{`sizes.isType("number")`, fixtureCandidate{props: map[string]PropertyValue{"sizes": presentList(sizes, tvNumber(t, TypeInteger, "1"))}}, false, "a list of numbers is a list, not a number"},
		} {
			res := evalOne(t, tc.src, tc.cand)
			vals := res.Values()
			if len(vals) != 1 {
				t.Fatalf("%s produced %d values, want one truth value", tc.src, len(vals))
			}
			if got := vals[0].Bool; got != tc.want {
				t.Errorf("%s = %v, want %v (%s)", tc.src, got, tc.want, tc.why)
			}
		}
	})

	t.Run("an absent or non-conforming value answers FALSE, never absent", func(t *testing.T) {
		// This is the whole reason the guard exists, and it is the one place
		// absence must NOT propagate: `!cost.isType("number")` has to be TRUE
		// on the records where cost is missing, or the guard never fires on
		// exactly the rows it was written for.
		for _, tc := range []struct {
			name string
			pv   PropertyValue
		}{
			{"absent", absentValue(amount)},
			{"non-conforming", PropertyValue{Property: amount, State: StateNonConforming}},
		} {
			res := evalOne(t, `amount.isType("number")`, fixtureCandidate{
				props: map[string]PropertyValue{"amount": tc.pv},
			})
			if res.Absent {
				t.Fatalf("a %s value must make isType answer FALSE, not absent", tc.name)
			}
			if vals := res.Values(); len(vals) != 1 || vals[0].Bool {
				t.Errorf("a %s value must make isType answer false; got %v", tc.name, vals)
			}
			negated := evalOne(t, `!amount.isType("number")`, fixtureCandidate{
				props: map[string]PropertyValue{"amount": tc.pv},
			})
			if vals := negated.Values(); len(vals) != 1 || !vals[0].Bool {
				t.Errorf("`!isType` must be TRUE for a %s value — that is what makes it a usable guard; got %v", tc.name, vals)
			}
		}
	})

	t.Run("the type name is a closed literal set", func(t *testing.T) {
		for _, tc := range []struct{ src, why string }{
			{`amount.isType("date")`, "a type name this grammar does not test for"},
			{`amount.isType("boolean")`, "the same"},
			{`amount.isType("Number")`, "the set is exact, not case-normalised"},
			{`amount.isType(name)`, "a type name computed per record is not a declaration"},
		} {
			_, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema)
			if len(errs) == 0 {
				t.Errorf("%q must be refused (%s)", tc.src, tc.why)
				continue
			}
			if !strings.Contains(errs[0].Error(), `"number"`) {
				t.Errorf("the refusal for %q must LIST the type names it accepts; got:\n  %s", tc.src, errs[0].Error())
			}
		}
	})
}

// TestFormula_TheFoundersOwnExpressionsParseAndType is the acceptance bar for
// this change, stated as the real corpus rather than as invented examples.
//
// Every source below is copied verbatim from one of the founder's eighteen
// `.base` files, with property names mapped onto this package's fixture schema
// where the real one differs. A formula that Obsidian evaluates and this
// grammar cannot even PARSE is a loss the importer can do nothing about.
func TestFormula_TheFoundersOwnExpressionsParseAndType(t *testing.T) {
	schema := formulaFixtureSchema()

	for _, tc := range []struct {
		src       string
		wantType  FormulaType
		wantArity FormulaArity
		from      string
	}{
		{"(today() - due).days", FormulaNumber, ArityOne, "Deals.base / Inbox-Triage.base — `age: (today() - created).days`"},
		{"(date(name) - today()).days", FormulaNumber, ArityOne, "Tasks.base — `(date(due) - today()).days`, over a text date"},
		{"file.backlinks.length", FormulaNumber, ArityOne, "Entities.base — `backlink_count: file.backlinks.length`"},
		{`if(amount.isType("number"), if(name == "annual", amount / 12, amount), amount)`, FormulaNumber, ArityOne,
			"Subscriptions.base — `monthly_cost`, with its untranslatable `\"\"` else-branch replaced by the property"},
	} {
		set, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema)
		if len(errs) != 0 {
			t.Errorf("this is a REAL formula from %s and must type:\n  %s\n  %v", tc.from, tc.src, formulaErrorMessages(errs))
			continue
		}
		decl, ok := set.Get("f")
		if !ok {
			t.Fatalf("%q validated but is not in the set", tc.src)
		}
		if decl.Type != tc.wantType || decl.Arity != tc.wantArity {
			t.Errorf("%q typed as %s/%s, want %s/%s", tc.src, decl.Type, decl.Arity, tc.wantType, tc.wantArity)
		}
	}
}

// TestFormula_AnAccessorDoesNotStealAFormulaName guards the parse rule that a
// two-segment `formula.<name>` is a REFERENCE, never a receiver plus accessor.
//
// Without it a view is forbidden from naming a formula `length` or `days` — and
// the refusal it gets talks about the bare word `formula`, which is not the
// fault and not something the author wrote.
func TestFormula_AnAccessorDoesNotStealAFormulaName(t *testing.T) {
	schema := formulaFixtureSchema()
	set, errs := ValidateFormulaSet(map[string]string{
		"length": "sizes.length",
		"twice":  "formula.length * 2",
	}, schema)
	if len(errs) != 0 {
		t.Fatalf("a formula may be CALLED `length`: %v", formulaErrorMessages(errs))
	}
	decl, ok := set.Get("twice")
	if !ok {
		t.Fatal("`twice` is missing from the set")
	}
	if len(decl.Refs) != 1 || decl.Refs[0] != "length" {
		t.Errorf("`formula.length * 2` must reference the formula `length`; got refs %v", decl.Refs)
	}
}

// TestFormula_ABooleanSurvivesBeingReferencedByAnotherFormula is a regression
// test for the third instance of a defect this package had already found and
// fixed TWICE.
//
// `TypedValue` is a tagged union and a `checkbox` populates Bool, leaving Text
// empty. Two places in formula_eval.go say so in their comments — materialize's
// FormulaBoolean case and fvalFromPropertyValue's — each recording that reading
// Text "made every checkbox a formula read false". fvalFromResult, the third
// place that lifts a boolean, was still reading Text.
//
// The consequence is the silent kind: a formula referencing a BOOLEAN sibling
// read it as `false` on every record, whatever it evaluated to. No refusal, no
// problem entry, no wrong type — just the complement of the intended row set,
// which is the exact failure R-18 and FR-143a exist to remove one layer up.
func TestFormula_ABooleanSurvivesBeingReferencedByAnotherFormula(t *testing.T) {
	schema := formulaFixtureSchema()
	amount := schema.Properties["amount"]

	set, errs := ValidateFormulaSet(map[string]string{
		"big":      "amount > 10",
		"reported": "if(formula.big, 1, 2)",
		"negated":  "!formula.big",
	}, schema)
	if len(errs) != 0 {
		t.Fatalf("the set must validate: %v", formulaErrorMessages(errs))
	}

	e := NewFormulaEvaluator(set, testComparator(), formulaTestNow())
	e.Begin(fixtureCandidate{props: map[string]PropertyValue{
		"amount": numberValue(t, amount, "20"),
	}})

	// 20 > 10, so `big` is TRUE. Everything below follows from that by hand.
	big, _ := e.Evaluate("big")
	if vals := big.Values(); len(vals) != 1 || !vals[0].Bool {
		t.Fatalf("amount 20 > 10 must be true; got %v", vals)
	}
	reported, _ := e.Evaluate("reported")
	if got := renderNumber(t, reported); got != "1" {
		t.Errorf("`if(formula.big, 1, 2)` with big TRUE must be 1; got %s — a boolean read through a formula reference lost its value", got)
	}
	negated, _ := e.Evaluate("negated")
	if vals := negated.Values(); len(vals) != 1 || vals[0].Bool {
		t.Errorf("`!formula.big` with big TRUE must be false; got %v", vals)
	}
}
