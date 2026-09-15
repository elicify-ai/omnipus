// Omnipus — W5's proof, put to the evaluator rather than argued at it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE MEASURES INSTEAD OF ARGUING
//
// W5 rewrites `if(P, X, Y)` to `if(P == P, X, Y)` for a single-valued `date` P.
// Its whole claim is that `P == P` answers TRUE on exactly the records where
// Obsidian's bare `P` is truthy, and PRESENT-FALSE on the rest — so the two
// forms hold the SAME VALUE on every record and the rewrite is an EQUALITY
// rather than a narrowing. Only an equality is safe under a `not:`, and that is
// the one thing a rewrite in translate.go has to earn.
//
// UNLIKE W4's HARNESS, BOTH FORMS ARE NOT COMPARABLE HERE EITHER: `if(P, …)`
// does not typecheck in this grammar at all (that is the loss W5 exists to
// repair), so there is no pair of values to diff. TestW5_TheBareDateGuardDoes
// NotTypecheckAtAll pins that, so a future grammar that starts accepting a date
// condition makes this whole file's premise visibly stale instead of silently
// so. What IS measured is the emitted formula's value on every state of P this
// product can represent, against an oracle taken from spec §8 and FR-007a —
// never read back off translate.go.
//
// THE FOLD IS THE SPECIFIC THING BEING GUARDED. Constant-folding `P == P` to
// `true` is an ordinary optimisation and nothing in pkg/records forbids it
// today. If it landed, the absent / `""` / non-conforming rows below would take
// the THEN-branch, `date(<absent>)` would propagate absence through the
// arithmetic, and the column would go blank — a silent regression in a formula
// nobody edited. Every row therefore asserts a CONCRETE VALUE, and the two
// branches are deliberately given values that differ (4 against 23), so a test
// that stopped being able to tell the branches apart fails its own guard.
// ---------------------------------------------------------------------------

const w5Schema = `schema_version: 1
type: candidate
properties:
  updated: { type: date }
  created: { type: date }
  updates: { type: date, many: true }
  note:    { type: text }
  cost:    { type: decimal }
  done:    { type: checkbox }
  stage:   { type: enum, values: [applied, screen, offer] }
`

// w5Now is the clock every evaluation in this file runs against. The two dates
// in the fixtures below are placed relative to it so the two branches of the
// founder's own formula answer DIFFERENT numbers.
var w5Now = time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)

const (
	// w5ThenBranch and w5ElseBranch are Hiring.base's own, verbatim.
	w5ThenBranch = `(today() - date(updated)).days`
	w5ElseBranch = `(today() - created).days`

	// w5ThenValue is 2026-08-20 -> 2026-08-24; w5ElseValue is 2026-08-01 ->
	// 2026-08-24. Both counted by hand off the calendar, not off a run.
	w5ThenValue = "4"
	w5ElseValue = "23"
)

func w5TestSchema(t *testing.T) *records.Schema {
	t.Helper()
	schema, rejection := records.ParseSchema("candidate.yaml", []byte(w5Schema))
	if rejection != nil {
		t.Fatalf("the fixture schema was rejected: %+v", rejection)
	}
	return schema
}

// w5State is one representable state of a single-valued `date` property, with
// the branch Obsidian takes and the branch this product takes stated
// SEPARATELY — so a row where they diverge is visible as a divergence rather
// than hidden behind one shared expectation.
type w5State struct {
	name string
	note string
	// obsidianTruthy is what JavaScript does with a bare `P` in this state.
	obsidianTruthy bool
	// oursTrue is what §8 R-2 / R-4 / R-5 make `P == P` answer in this state.
	oursTrue bool
	// why cites the rule, so a row that changes has to argue with the spec.
	why string
}

// w5States is the whole state space of a single-valued `date` under this
// product's own model. Every note carries `created` so the else-branch always
// has a value to compute — otherwise a row could pass by both branches being
// absent, which would measure nothing.
var w5States = []w5State{
	{
		name:           "a real date",
		note:           "---\ntype: candidate\nupdated: 2026-08-20\ncreated: 2026-08-01\n---\n",
		obsidianTruthy: true,
		oursTrue:       true,
		why:            "R-5 compares two dates by instant and an instant equals itself; in JavaScript a Date object is truthy",
	},
	{
		name:           "absent",
		note:           "---\ntype: candidate\ncreated: 2026-08-01\n---\n",
		obsidianTruthy: false,
		oursTrue:       false,
		why:            "R-2 — either side absent is FALSE for every operator but IS NULL / IS NOT NULL; in JavaScript `undefined` is falsy",
	},
	{
		name:           "the empty string",
		note:           "---\ntype: candidate\nupdated: \"\"\ncreated: 2026-08-01\n---\n",
		obsidianTruthy: false,
		oursTrue:       false,
		why:            "FR-007a makes `\"\"` the ABSENT state on a date, so this IS the row above; in JavaScript `\"\"` is falsy",
	},
	{
		name:           "a value that does not parse as a date",
		note:           "---\ntype: candidate\nupdated: soon\ncreated: 2026-08-01\n---\n",
		obsidianTruthy: true,
		oursTrue:       false,
		why:            "§8 R-4 — a non-conforming value reaches a formula as ABSENCE (fvalFromPropertyValue) and answers false for every operator; JavaScript reads the non-empty string as truthy. This is the ONE divergence, it is product-wide rather than W5's, and it is in FR-105's permitted direction for a column: our answer is the else-branch's real number where Obsidian's is `date(\"soon\")` -> Invalid Date -> NaN",
	},
}

// w5Candidate is a records.FormulaCandidate over one parsed note, routed
// through the product's own property resolution so an operand here is decoded
// exactly as it is in a real query.
type w5Candidate struct {
	rec    records.Record
	schema *records.Schema
}

func (c w5Candidate) FormulaProperty(name string) (records.PropertyValue, bool) {
	prop, ok := c.schema.Property(name)
	if !ok {
		return records.PropertyValue{}, false
	}
	return records.ResolveProperty(c.rec, prop), true
}

func (c w5Candidate) FormulaFileProperty(name string) (records.PropertyValue, bool) {
	v, err := records.ResolveFileProperty(name, records.FileMeta{Path: c.rec.Path})
	if err != nil {
		return records.PropertyValue{}, false
	}
	return v, true
}

// w5Evaluate runs one expression over one note through the REAL formula
// evaluator and the REAL comparator, and returns the rendered value.
func w5Evaluate(t *testing.T, schema *records.Schema, expr, note string) (display string, absent bool) {
	t.Helper()
	set, ferrs := records.ValidateFormulaSet(map[string]string{"f": expr}, schema)
	if len(ferrs) > 0 {
		t.Fatalf("the expression %q does not typecheck: %v", expr, ferrs[0])
	}
	ev := records.NewFormulaEvaluator(set, records.Comparator{}, w5Now)
	ev.Begin(w5Candidate{rec: records.ParseRecord("n.md", []byte(note)), schema: schema})
	res, ok := ev.Evaluate("f")
	if !ok {
		t.Fatalf("the evaluator has no value at all for %q", expr)
	}
	if res.Absent {
		return "", true
	}
	vals := res.Values()
	if len(vals) != 1 {
		t.Fatalf("%q produced %d values, want exactly 1: %v", expr, len(vals), vals)
	}
	// Rendered from the TYPED value rather than from Display(), which is
	// R-16/FR-215's presentation channel and is empty for a number or a
	// boolean. Reading the wrong field is how a test measures nothing.
	switch res.Type {
	case records.FormulaNumber:
		// FR-144 renders a number at its DECLARED scale, so a day count arrives
		// as "4.0000000000". Only the trailing zeros of a fractional part are
		// dropped, never a digit: a genuinely fractional answer (4.5) still
		// reads as "4.5" and still fails a "4" expectation, so this normalises
		// the spelling without weakening what is being asserted.
		return trimTrailingZeros(vals[0].Number.String()), false
	case records.FormulaBoolean:
		if vals[0].Bool {
			return "true", false
		}
		return "false", false
	}
	t.Fatalf("%q is typed %v, which this harness does not render", expr, res.Type)
	return "", false
}

// TestW5_TheBareDateGuardDoesNotTypecheckAtAll is this file's premise, pinned.
//
// W5 is worth having only while `if(P, X, Y)` over a date P is REFUSED — that
// refusal is the dropped column on Hiring :: Pipeline by Stage. If the grammar
// ever admits a date condition directly, this test fails and W5's whole
// argument has to be re-read rather than silently kept.
func TestW5_TheBareDateGuardDoesNotTypecheckAtAll(t *testing.T) {
	schema := w5TestSchema(t)
	src := `if(updated, ` + w5ThenBranch + `, ` + w5ElseBranch + `)`

	_, ferrs := records.ValidateFormulaSet(map[string]string{"f": src}, schema)
	if len(ferrs) == 0 {
		t.Fatalf("%q typechecks — W5 rewrites a formula this grammar no longer refuses, so its premise is stale", src)
	}
	if got := ferrs[0].Error(); !strings.Contains(got, "truth value") {
		t.Errorf("the refusal of %q is %q; W5's premise is specifically that `if`'s CONDITION is refused as not a truth value", src, got)
	}
}

// TestW5_TheSpelledGuardAdmitsExactlyWhatObsidianTruthinessAdmits is the whole
// proof, measured through the real evaluator.
//
// It is also THE FOLD GUARD the header promises: a `P == P` folded to `true`
// sends the last three rows down the then-branch, where `date(<absent>)`
// propagates absence and the value goes ABSENT. Every row asserts a concrete
// number, so that fold fails here loudly instead of blanking a column.
func TestW5_TheSpelledGuardAdmitsExactlyWhatObsidianTruthinessAdmits(t *testing.T) {
	schema := w5TestSchema(t)

	guard, emitted, ok := spellDateTruthinessGuard("updated", w5ThenBranch, w5ElseBranch, schema)
	if !ok {
		t.Fatal("W5 declined the founder's own shape — there is nothing left to measure")
	}
	if guard != "updated" {
		t.Errorf("the guard reported is %q, want %q", guard, "updated")
	}
	want := `if(updated == updated, ` + w5ThenBranch + `, ` + w5ElseBranch + `)`
	if emitted != want {
		// Errorf, NOT Fatalf, on purpose: the state table below is evaluated
		// against the EMITTED text, and it is the half that says what the
		// formula MEANS. Stopping here on a spelling mismatch would leave the
		// meaning unmeasured on exactly the runs where it matters most — a
		// swapped pair of branches emits a well-formed formula that answers the
		// wrong number on every record.
		t.Errorf("emitted\n got: %q\nwant: %q", emitted, want)
	}

	// The test's own falsifiability: the two branches must answer DIFFERENT
	// values, or nothing below can tell which one was taken.
	if w5ThenValue == w5ElseValue {
		t.Fatal("the two branches are expected to hold the same value, so this test cannot observe which branch was taken")
	}

	for _, st := range w5States {
		t.Run(st.name, func(t *testing.T) {
			wantValue := w5ElseValue
			if st.oursTrue {
				wantValue = w5ThenValue
			}
			got, absent := w5Evaluate(t, schema, emitted, st.note)
			if absent {
				t.Fatalf("the rewritten formula is ABSENT on a %s `updated`, want %q (the %s-branch). A `P == P` folded to `true` produces exactly this. — %s",
					st.name, wantValue, branchName(st.oursTrue), st.why)
			}
			if got != wantValue {
				t.Errorf("the rewritten formula is %q on a %s `updated`, want %q (the %s-branch) — %s",
					got, st.name, wantValue, branchName(st.oursTrue), st.why)
			}
			if st.obsidianTruthy != st.oursTrue {
				t.Logf("KNOWN DIVERGENCE, not a failure: Obsidian takes the then-branch here and we take the else-branch — %s", st.why)
			}
		})
	}
}

// trimTrailingZeros drops a number's trailing fractional zeros, and the whole
// decimal point when nothing is left after it. It touches nothing else.
func trimTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

func branchName(then bool) string {
	if then {
		return "then"
	}
	return "else"
}

// TestW5_TheSpelledGuardIsNeverAbsentSoNegationCannotBroaden is the TREE half.
//
// W5's safety under a `not:` rests on the rewrite being a value EQUALITY, and
// the one way a boolean condition can break that is by being ABSENT rather than
// present-false: `!(absent)` is absent (evalUnary's R-14 branch), and
// knowledgefind negates a combinator as a bare `!inner.matched` with no absence
// rule of its own — which is precisely where a rewrite proved only at the leaf
// has been wrong before. So the guard is measured as a boolean IN ISOLATION:
// present on every state, true on exactly the states Obsidian calls truthy.
func TestW5_TheSpelledGuardIsNeverAbsentSoNegationCannotBroaden(t *testing.T) {
	schema := w5TestSchema(t)

	for _, st := range w5States {
		t.Run(st.name, func(t *testing.T) {
			got, absent := w5Evaluate(t, schema, "updated == updated", st.note)
			if absent {
				t.Fatalf("`updated == updated` is ABSENT on a %s `updated` — an absent condition inverts to an absent condition under `not:`, which is where a narrowing becomes a broadening. §8 R-2 makes it present-FALSE. — %s", st.name, st.why)
			}
			wantBool := "false"
			if st.oursTrue {
				wantBool = "true"
			}
			if got != wantBool {
				t.Errorf("`updated == updated` is %q on a %s `updated`, want %q — %s", got, st.name, wantBool, st.why)
			}

			// The negation, measured rather than reasoned: an equality inverts
			// to an equality, so `!(P == P)` must be the exact complement on
			// every state. A three-valued condition would show up here.
			neg, negAbsent := w5Evaluate(t, schema, "!(updated == updated)", st.note)
			if negAbsent {
				t.Fatalf("`!(updated == updated)` is ABSENT on a %s `updated` — the guard is not two-valued and W5's `not:` argument does not hold", st.name)
			}
			if neg == got {
				t.Errorf("`!(updated == updated)` is %q, the same as the un-negated %q, on a %s `updated` — negation is not a complement here", neg, got, st.name)
			}
		})
	}
}

// TestW5_OnlyASingleValuedDateGuardIsSpelled grades the gate in BOTH
// directions. A `true` where the proof does not hold is a wrong rewrite; a
// `false` where it does is a column dropped for no reason, which is the defect
// W5 exists to correct and must not creep back.
func TestW5_OnlyASingleValuedDateGuardIsSpelled(t *testing.T) {
	schema := w5TestSchema(t)

	for _, tc := range []struct {
		name  string
		guard string
		spell bool
		why   string
	}{
		{name: "a single-valued date", guard: "updated", spell: true, why: "FR-007a makes truthy and present the same question, and only there"},
		{name: "a many date", guard: "updates", spell: false, why: "an empty list is present and TRUTHY in JavaScript, and `==` over a many operand is element-wise (R-9)"},
		{name: "a text property", guard: "note", spell: false, why: "`\"\"` is a PRESENT falsy value on text — that is W3's shape, spelled `!= \"\"`, not this one"},
		{name: "a number", guard: "cost", spell: false, why: "`0` is present and falsy"},
		{name: "a checkbox", guard: "done", spell: false, why: "`false` is present and falsy"},
		{name: "an enum", guard: "stage", spell: false, why: "`\"\"` is present and falsy on an enum's text domain"},
		{name: "an undeclared name", guard: "missing", spell: false, why: "with no declaration there is no type and no proof"},
		{name: "a comparison, not a bare property", guard: "updated == updated", spell: false, why: "W5's own output must not re-fire on itself, or the rewrite would not terminate"},
		{name: "a call over the property", guard: "date(updated)", spell: false, why: "a Call is not a Ref; the truthiness rule is about a bare property reference"},
		{name: "a file-namespace reference", guard: "file.name", spell: false, why: "not a RefProperty, and file.name is a text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, got := spellDateTruthinessGuard(tc.guard, w5ThenBranch, w5ElseBranch, schema)
			if got != tc.spell {
				t.Errorf("spellDateTruthinessGuard(%q, …) spelled = %v, want %v — %s", tc.guard, got, tc.spell, tc.why)
			}
		})
	}

	if _, _, got := spellDateTruthinessGuard("updated", w5ThenBranch, w5ElseBranch, nil); got {
		t.Error("spellDateTruthinessGuard proved a guard with NO SCHEMA — the whole proof is a property lookup, so a nil schema must decline")
	}
}

// TestW5_ALiteralElseBranchIsLeftToW1AndW4 is the boundary between the four
// rewrites, asserted at rewriteFormulaSource so the ORDER of the rules is what
// is being measured and not just one predicate.
//
// W5 must not steal a shape W1 or W4 rules, and must not fire on the literal
// else-branches this package already asserts are carried verbatim.
func TestW5_ALiteralElseBranchIsLeftToW1AndW4(t *testing.T) {
	schema := w5TestSchema(t)
	body := w5ThenBranch

	for _, tc := range []struct {
		els  string
		want string
		why  string
	}{
		{
			els:  w5ElseBranch,
			want: `if(updated == updated, ` + body + `, ` + w5ElseBranch + `)`,
			why:  "W5's own shape: the else-branch computes a DIFFERENT value, so there is no redundant guard to drop",
		},
		{
			els:  `""`,
			want: body,
			why:  "W1 drops the `\"\"` else-branch, and W2 then drops the guard as redundant — the guarded expression is already absent wherever `updated` is",
		},
		{els: "false", want: `if(updated, ` + body + `, false)`, why: "a bare literal — W4's shape, and W4 declines because the guarded expression is a NUMBER rather than a present-false boolean"},
		{els: "true", want: `if(updated, ` + body + `, true)`, why: "a bare literal; no rewrite claims it"},
		{els: "0", want: `if(updated, ` + body + `, 0)`, why: "a bare literal; no rewrite claims it"},
		{els: `"false"`, want: `if(updated, ` + body + `, "false")`, why: "a bare literal; no rewrite claims it"},
	} {
		t.Run(tc.els, func(t *testing.T) {
			in := `if(updated, ` + body + `, ` + tc.els + `)`
			got, _ := rewriteFormulaSource(in, schema)
			if got != tc.want {
				t.Errorf("rewriteFormulaSource(%q)\n got: %q\nwant: %q\n why: %s", in, got, tc.want, tc.why)
			}
		})
	}
}

// TestW5_TheFoundersAgeInStageSurvivesTheWholePipeline is the end-to-end row:
// the exact source text from 06-Bases/Hiring.base, rewritten and then handed to
// the product's own formula validator. Both halves matter — a rewrite that
// emits a well-formed formula the type checker still refuses would leave the
// column exactly as dropped as it is today.
func TestW5_TheFoundersAgeInStageSurvivesTheWholePipeline(t *testing.T) {
	schema := w5TestSchema(t)
	const hiring = `if(updated, (today() - date(updated)).days, (today() - created).days)`

	got, note := rewriteFormulaSource(hiring, schema)
	want := `if(updated == updated, (today() - date(updated)).days, (today() - created).days)`
	if got != want {
		t.Fatalf("Hiring.base's age_in_stage\n got: %q\nwant: %q", got, want)
	}
	if note == "" {
		t.Error("the rewrite reported no note — a source that no longer matches the `.base` file must be reported where the formula is USED, not discoverable only by diffing two files")
	}

	set, ferrs := records.ValidateFormulaSet(map[string]string{"age_in_stage": got}, schema)
	if len(ferrs) > 0 {
		t.Fatalf("the rewritten age_in_stage does not typecheck, so the column stays dropped: %v", ferrs[0])
	}
	if decl, ok := set.Get("age_in_stage"); !ok || decl.Type != records.FormulaNumber {
		t.Errorf("age_in_stage typed as %+v, want a number — Obsidian's own is a day count", decl)
	}
}
