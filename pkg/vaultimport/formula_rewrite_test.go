// Omnipus — the two source rewrites that let an Obsidian formula be expressed
// in this product's typed expression language, and the proof that neither one
// changes a record's value.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THESE TESTS EXIST, AND WHAT THEY ARE GUARDING
//
// A rewrite that fires when it should not is the worst defect this importer can
// ship, and it is invisible: the view loads, the column renders, every row has a
// number in it, and the number is wrong on exactly the records where the guard
// used to matter. There is no error, no refusal and no loss line to notice.
//
// So the oracle here is never "the output parses". It is the SHAPE of the
// output text, asserted exactly, against a rewrite that must fire and against
// the nearest cases in which it must not.
// ---------------------------------------------------------------------------

// rewriteSchema declares one property of each type the guard rule cares about,
// so a test can move a single property's type and watch the rewrite stop.
func rewriteSchema(t *testing.T) *records.Schema {
	t.Helper()
	schema, rejection := records.ParseSchema("thing.yaml", []byte(`schema_version: 1
type: thing
properties:
  due:       { type: date }
  created:   { type: date }
  dates:     { type: date, many: true }
  note:      { type: text }
  cost:      { type: decimal }
  cycle:     { type: text }
  done:      { type: checkbox }
`))
	if rejection != nil {
		t.Fatalf("the fixture schema was rejected: %+v", rejection)
	}
	return schema
}

// TestRewriteW2_DropsATruthyDateGuardOnlyWhenItIsProvablyRedundant.
//
// The rewrite's whole justification is that the guarded expression ALREADY
// answers absence wherever the guarded property is absent, so the guard decides
// nothing. Each "must not" case below is one in which that is false — and each
// one is a case the founder's own vault contains, not a hypothetical.
func TestRewriteW2_DropsATruthyDateGuardOnlyWhenItIsProvablyRedundant(t *testing.T) {
	schema := rewriteSchema(t)

	for _, tc := range []struct {
		name string
		in   string
		want string
		why  string
	}{
		{
			name: "the founder's shape: a date guard over arithmetic on that same date",
			in:   `if(due, (date(due) - today()).days, "")`,
			want: `(date(due) - today()).days`,
			why:  "date-minus-date is absent when either side is (evalArithmetic), and `.days` on an absent receiver is absent (evalFieldAccess), so the guard cannot change a single value",
		},
		{
			name: "the subtraction the other way round",
			in:   `if(due, (today() - date(due)).days, "")`,
			want: `(today() - date(due)).days`,
			why:  "same rule, and Finance-AR.base writes it this way round",
		},
		{
			// W3 CHANGED THIS ROW'S ANSWER, AND NOT ITS POINT. The guard is
			// still never DROPPED on text, for the reason this row always
			// gave; what changed is that keeping it no longer costs a refusal.
			// `P != ""` spells Obsidian's truthiness exactly on a text property
			// — see W3's three-state proof in translate.go's header and
			// formula_text_truthiness_test.go, which grades it at the VIEW,
			// under a negation, against the independent oracle.
			name: "a TEXT guard is never dropped — it is SPELLED (W3)",
			in:   `if(note, (date(due) - today()).days, "")`,
			want: `if(note != "", (date(due) - today()).days)`,
			why:  "FR-007a keeps \"\" a PRESENT value on text, so a text property can be present and FALSY — `truthy` and `present` are different questions, W2's guard-DROPPING stays refused here, and the guard is re-expressed rather than removed",
		},
		{
			name: "a NUMBER guard is never dropped",
			in:   `if(cost, (date(due) - today()).days, "")`,
			want: `if(cost, (date(due) - today()).days)`,
			why:  "0 is present and falsy",
		},
		{
			name: "a CHECKBOX guard is never dropped",
			in:   `if(done, (date(due) - today()).days, "")`,
			want: `if(done, (date(due) - today()).days)`,
			why:  "false is present and falsy",
		},
		{
			name: "a MANY date guard is never dropped",
			in:   `if(dates, (date(due) - today()).days, "")`,
			want: `if(dates, (date(due) - today()).days)`,
			why:  "an empty list is present and falsy in JavaScript",
		},
		{
			name: "a guard over a DIFFERENT property is never dropped",
			in:   `if(due, (today() - created).days, "")`,
			want: `if(due, (today() - created).days)`,
			why:  "the guarded expression names `created`, not `due`, so it has a value on a record with no `due` at all — dropping the guard would ADD a value where Obsidian showed nothing",
		},
		{
			// THIS ROW REVERSED, and the reversal is the point rather than a
			// relaxation. It used to assert that `if(due, <comparison>, false)`
			// stays untouched, because "R-2 makes a comparison over an absent
			// operand FALSE, which is a VALUE and not absence". Both halves of
			// that sentence are true and the conclusion did not follow: the
			// ELSE-BRANCH IS ALSO `false`, so the value the guard was
			// protecting against is the value the else-branch already returns.
			// W4 proves that identity (translate.go's header) and drops the
			// guard. The `""` else-branch rows below are unaffected — there the
			// two values really do differ, `false` against absence, which is
			// why they still refuse.
			name: "a guard over a COMPARISON is dropped when the else-branch is `false` (W4)",
			in:   `if(due, date(due) < today(), false)`,
			want: `date(due) < today()`,
			why:  "with `due` absent the comparison is present-FALSE under §8 R-2, which is exactly what the `false` else-branch returns — the two forms hold the same value on every record",
		},
		{
			name: "a guard over a COMPARISON is NOT dropped when the else-branch is `true`",
			in:   `if(due, date(due) < today(), true)`,
			want: `if(due, date(due) < today(), true)`,
			why:  "W4's identity is that the guarded expression already returns the ELSE-BRANCH's value where the guard is absent; `false` is that value and `true` is its opposite",
		},
		{
			name: "a guard over a NON-comparison is never dropped, `false` else or not",
			in:   `if(due, done, false)`,
			want: `if(due, done, false)`,
			why:  "`done` is a checkbox read straight off the record — on a note with no `due` it is whatever that note's `done` says, not false",
		},
		{
			// THE CASE THAT REACHES absentWhenAbsent's COMPARISON RULE. The
			// row above never gets there: it is a three-argument `if` whose
			// else-branch is not `""`, so W1 does not fire and W2 never runs.
			// A mutation that let absentWhenAbsent recurse through comparison
			// operators left the whole suite GREEN until this row existed.
			name: "a guard over a comparison is never dropped (2-arg, reachable)",
			in:   `if(due, date(due) < today(), "")`,
			want: `if(due, date(due) < today())`,
			why:  "with `due` absent the comparison is FALSE, not absent — Obsidian's guard would have shown nothing and dropping it shows `false`, a value where there was none",
		},
		{
			// The same trap one operator over: `&&` is a logical operator, not
			// arithmetic, and its result over an absent operand is a boolean.
			name: "a guard over a logical AND is never dropped",
			in:   `if(due, date(due) < today() && done, "")`,
			want: `if(due, date(due) < today() && done)`,
			why:  "the result is a truth value on every record, guarded or not",
		},
		{
			name: "an undeclared guard property is never dropped",
			in:   `if(missing, (date(due) - today()).days, "")`,
			want: `if(missing, (date(due) - today()).days)`,
			why:  "with no declaration there is no type, and with no type there is no proof",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := rewriteFormulaSource(tc.in, schema)
			if got != tc.want {
				t.Errorf("rewriteFormulaSource(%q)\n got: %q\nwant: %q\nwhy: %s", tc.in, got, tc.want, tc.why)
			}
		})
	}
}

// TestRewriteW1_TheEmptyElseBranchBecomesAnOmittedBranch.
//
// The `""` else-branch is Obsidian's "show nothing" and an omitted `if` branch
// is ours — evalIf's own comment says the missing branch IS absence. The
// distinction that matters is that a NON-empty string is a VALUE, and dropping
// it would erase a character the operator typed.
func TestRewriteW1_TheEmptyElseBranchBecomesAnOmittedBranch(t *testing.T) {
	schema := rewriteSchema(t)

	// Subscriptions.base's monthly_cost. The condition is already a boolean
	// (`isType`), so W2 never applies and W1 alone is what carries it: FR-143a
	// refuses a number then-branch paired with a text else-branch outright.
	in := `if(cost.isType("number"), if(cycle == "annual", cost / 12, cost), "")`
	want := `if(cost.isType("number"), if(cycle == "annual", cost / 12, cost))`
	got, note := rewriteFormulaSource(in, schema)
	if got != want {
		t.Errorf("monthly_cost\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(note, "else-branch") {
		t.Errorf("the rewrite note does not mention the else-branch; note = %q", note)
	}

	// A NON-empty string else-branch is a value and must survive untouched.
	// This is the case that separates "show nothing" from "show this".
	keep := `if(cost.isType("number"), cost, " ")`
	if got, _ := rewriteFormulaSource(keep, schema); got != keep {
		t.Errorf("a single-space else-branch was dropped: %q -> %q — a space is a VALUE, not absence", keep, got)
	}
	keep2 := `if(cost.isType("number"), cost, "n/a")`
	if got, _ := rewriteFormulaSource(keep2, schema); got != keep2 {
		t.Errorf("a real else-branch was dropped: %q -> %q", keep2, got)
	}
}

// TestSplitTopLevelIfArgs_ReadsOnlyOneWholeIfCall.
//
// The splitter is the one place this package looks at formula source as TEXT
// rather than as a tree, so every way that can go wrong is a way a rewrite
// could fire on an expression it did not understand.
func TestSplitTopLevelIfArgs_ReadsOnlyOneWholeIfCall(t *testing.T) {
	for _, tc := range []struct {
		src      string
		wantArgs []string
		wantOK   bool
	}{
		{src: `if(a, b)`, wantArgs: []string{"a", "b"}, wantOK: true},
		{src: `if(a, b, c)`, wantArgs: []string{"a", "b", "c"}, wantOK: true},
		{src: `if( a , b )`, wantArgs: []string{"a", "b"}, wantOK: true},
		{src: `if(f(x, y), b)`, wantArgs: []string{"f(x, y)", "b"}, wantOK: true},

		// A comma inside a string literal is not an argument boundary. Read it
		// as one and `if(t, "a, b", "")` splits into four arguments and is
		// refused — a formula lost for a reason that was never true.
		{src: `if(t, "a, b", "")`, wantArgs: []string{"t", `"a, b"`, `""`}, wantOK: true},

		// NOT one whole call. Each of these matched a naive prefix check.
		{src: `if(a, b) + 1`, wantOK: false},
		{src: `1 + if(a, b)`, wantOK: false},
		{src: `if(a)`, wantOK: false},
		{src: `if(a, b, c, d)`, wantOK: false},
		{src: `if(a, )`, wantOK: false},
		{src: `iffy(a, b)`, wantOK: false},
		{src: `(date(due) - today()).days`, wantOK: false},
		{src: `if(a, "unterminated)`, wantOK: false},
	} {
		got, ok := splitTopLevelIfArgs(tc.src)
		if ok != tc.wantOK {
			t.Errorf("splitTopLevelIfArgs(%q) ok = %v, want %v (got %q)", tc.src, ok, tc.wantOK, got)
			continue
		}
		if !tc.wantOK {
			continue
		}
		if strings.Join(got, "|") != strings.Join(tc.wantArgs, "|") {
			t.Errorf("splitTopLevelIfArgs(%q) = %q, want %q", tc.src, got, tc.wantArgs)
		}
	}
}

// TestTranslateFormulas_OneRefusalDoesNotTakeTheOthersWithIt, and a reference
// to a refused formula is refused in its turn rather than left dangling.
//
// The dangling case is the one worth spelling out: a view that declared
// `is_stale` without the `age` it names is a file the LOADER refuses whole
// (RejectViewUnknownFormula), so carrying the reference would cost the entire
// view rather than one formula.
func TestTranslateFormulas_OneRefusalDoesNotTakeTheOthersWithIt(t *testing.T) {
	schema := rewriteSchema(t)
	pb := &ParsedBase{
		Formulas: map[string]string{
			"age":         `(today() - created).days`,
			"days_to_due": `if(due, (date(due) - today()).days, "")`,
			// A guard whose GUARDED EXPRESSION is a bare property rather than
			// a comparison. W4 reaches the `false` else-branch shape but not
			// this one — `done` has a value of its own on a record with no
			// `due` — so it is still the truthy-date guard no rewrite carries,
			// which is what this test needs a refusal for.
			"broken":       `if(due, done, false)`,
			"needs_broken": `formula.broken`,
		},
		FormulaNames: []string{"age", "broken", "days_to_due", "needs_broken"},
	}
	ft := TranslateFormulas(pb, schema)

	carried := ft.FormulaNames()
	sort.Strings(carried)
	if strings.Join(carried, ",") != "age,days_to_due" {
		t.Fatalf("carried = %v, want [age days_to_due]", carried)
	}
	if _, refused := ft.Refused["broken"]; !refused {
		t.Error("`broken` was not refused, so the truthy-date guard over a boolean branch is being translated after all")
	}
	if reason, refused := ft.Refused["needs_broken"]; !refused {
		t.Error("a formula naming a REFUSED formula was carried — the view would declare a dangling `formula.broken` and the loader would refuse the whole file")
	} else if !strings.Contains(reason, "broken") {
		t.Errorf("the refusal for `needs_broken` does not name what it was waiting on: %q", reason)
	}

	// The carried set must be self-consistent: exactly what a view can declare.
	if _, ok := ft.Declared("days_to_due"); !ok {
		t.Fatal("days_to_due is in Sources but has no declaration")
	}
}

// TestTranslateFormulas_ARefusalNamesTheKeyEvenWithNoReadableSource.
//
// ParseBaseFile deliberately keeps a formula NAME whose value was not an
// expression string, and keeps no source for it. If the translator iterated the
// source map instead of the name list, that key would vanish silently between
// the `.base` file and the report — which is the failure mode this whole
// importer is written against.
func TestTranslateFormulas_ARefusalNamesTheKeyEvenWithNoReadableSource(t *testing.T) {
	pb := &ParsedBase{
		Formulas:     map[string]string{"good": `1 + 1`},
		FormulaNames: []string{"good", "unreadable"},
	}
	ft := TranslateFormulas(pb, rewriteSchema(t))
	if _, refused := ft.Refused["unreadable"]; !refused {
		t.Fatalf("a `formulas:` key with no readable source produced no refusal; Refused = %v", ft.Refused)
	}
	if _, carried := ft.Sources["good"]; !carried {
		t.Error("the readable formula beside it was dropped too")
	}
}

// TestFormulaClosure_PullsInWhatAReferenceNeedsAndNothingElse.
func TestFormulaClosure_PullsInWhatAReferenceNeedsAndNothingElse(t *testing.T) {
	schema := rewriteSchema(t)
	pb := &ParsedBase{
		Formulas: map[string]string{
			"age":       `(today() - created).days`,
			"stale":     `formula.age > 30`,
			"unrelated": `(date(due) - today()).days`,
		},
		FormulaNames: []string{"age", "stale", "unrelated"},
	}
	ft := TranslateFormulas(pb, schema)
	if len(ft.Refused) > 0 {
		t.Fatalf("fixture formulas did not all translate: %v", ft.Refused)
	}
	got := ft.Closure([]string{"stale"})
	if strings.Join(got, ",") != "age,stale" {
		t.Errorf("Closure([stale]) = %v, want [age stale] — a reference must pull its target in, and nothing else", got)
	}
	if got := ft.Closure(nil); len(got) != 0 {
		t.Errorf("Closure(nil) = %v, want empty — a view that names no formula declares none", got)
	}
}

// TestFormulaLiteralFits_RefusesALiteralThatCouldNeverMatch.
//
// An unparseable literal is NOT refused by the view loader — it checks that the
// NAME resolves, not the value's shape — so it would surface only at query time
// as a non-conforming comparison that is false for every record. A view that
// returns nothing looks exactly like a view whose filter matched nothing.
func TestFormulaLiteralFits_RefusesALiteralThatCouldNeverMatch(t *testing.T) {
	num := records.FormulaDecl{Name: "days", Type: records.FormulaNumber}
	if _, ok := formulaLiteralFits(num, "7"); !ok {
		t.Error("a whole number was refused against a number-valued formula")
	}
	if _, ok := formulaLiteralFits(num, "-13.5"); !ok {
		t.Error("a negative decimal was refused against a number-valued formula")
	}
	if reason, ok := formulaLiteralFits(num, "soon"); ok {
		t.Error("`soon` was accepted against a number-valued formula — the clause would be non-conforming for every record and read as an empty view")
	} else if !strings.Contains(reason, "soon") {
		t.Errorf("the refusal does not quote the literal: %q", reason)
	}

	flag := records.FormulaDecl{Name: "overdue", Type: records.FormulaBoolean}
	if _, ok := formulaLiteralFits(flag, "true"); !ok {
		t.Error("`true` was refused against a boolean-valued formula")
	}
	if _, ok := formulaLiteralFits(flag, "yes"); ok {
		t.Error("`yes` was accepted against a boolean-valued formula")
	}

	// A type this function cannot check must be REFUSED, not waved through.
	link := records.FormulaDecl{Name: "owner", Type: records.FormulaLink}
	if _, ok := formulaLiteralFits(link, "[[Someone]]"); ok {
		t.Error("a link-valued formula comparison was accepted unchecked — an unvalidated literal is the silently-empty view this function exists to prevent")
	}
}
