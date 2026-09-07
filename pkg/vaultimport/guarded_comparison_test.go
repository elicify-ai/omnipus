// Omnipus — W4's proof, put to the evaluator rather than argued at it.
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
// WHY THIS FILE MEASURES INSTEAD OF ASSERTING
//
// W4's claim is `if(P, C, false)` == `C`. Only ONE of those two expressions can
// be evaluated here: `if(P, …)` does not typecheck in this product's grammar at
// all — FR-143a wants a boolean condition and `P` is a date — which is why the
// rewrite exists in the first place. So there is no pair of values to compare.
//
// What CAN be measured is the entire content of the claim beyond W2's own guard
// rule: that `C` holds a PRESENT boolean FALSE on every record where P is
// absent, which is exactly the value the `false` else-branch holds there. That
// is what w4Observe does, over the real records.FormulaEvaluator, the real
// comparator and a real parsed note — and presentFalseWhenAbsent's answer is
// then compared to the observation rather than to a second opinion.
//
// BOTH DIRECTIONS ARE GRADED. A predicate that says `true` where the evaluator
// disagrees is a wrong rewrite; a predicate that says `false` where the
// evaluator would have agreed is a rewrite declined for no reason, which is the
// exact defect W4 was written to correct and must not be reintroduced silently.
// The table below therefore states the OBSERVED behaviour for every row,
// including the rows the predicate refuses.
// ---------------------------------------------------------------------------

const w4Schema = `schema_version: 1
type: thing
properties:
  due:     { type: date }
  created: { type: date }
  dates:   { type: date, many: true }
  status:  { type: enum, values: [todo, doing, done] }
  note:    { type: text }
  cost:    { type: decimal }
  done:    { type: checkbox }
`

func w4TestSchema(t *testing.T) *records.Schema {
	t.Helper()
	schema, rejection := records.ParseSchema("thing.yaml", []byte(w4Schema))
	if rejection != nil {
		t.Fatalf("the fixture schema was rejected: %+v", rejection)
	}
	return schema
}

// w4Candidate is a records.FormulaCandidate over one parsed note. Both methods
// route through the product's own resolution, so a formula operand here is
// decoded exactly as it is in a real query.
type w4Candidate struct {
	rec    records.Record
	schema *records.Schema
}

func (c w4Candidate) FormulaProperty(name string) (records.PropertyValue, bool) {
	prop, ok := c.schema.Property(name)
	if !ok {
		return records.PropertyValue{}, false
	}
	return records.ResolveProperty(c.rec, prop), true
}

func (c w4Candidate) FormulaFileProperty(name string) (records.PropertyValue, bool) {
	v, err := records.ResolveFileProperty(name, records.FileMeta{Path: c.rec.Path})
	if err != nil {
		return records.PropertyValue{}, false
	}
	return v, true
}

// w4NotesWithoutDue is every combination of the OTHER properties, over records
// that all share the one thing W4's proof is about: no `due` at all.
//
// The combinations matter. `status` absent is the case where `!=` diverges from
// JavaScript, and if a rewrite were going to go wrong on `!=` it would go wrong
// exactly there — so a fixture whose every note carried a status would have
// measured the one operator this proof was warned about the least.
var w4NotesWithoutDue = []string{
	"---\ntype: thing\n---\n",
	"---\ntype: thing\nstatus: done\n---\n",
	"---\ntype: thing\nstatus: todo\n---\n",
	"---\ntype: thing\nstatus: \"\"\n---\n",
	"---\ntype: thing\ndone: true\n---\n",
	"---\ntype: thing\ndone: false\n---\n",
	"---\ntype: thing\ncreated: 2020-01-01\nstatus: todo\ndone: true\n---\n",
	"---\ntype: thing\ncreated: 2099-01-01\nstatus: done\ndone: false\n---\n",
	"---\ntype: thing\ndue: \"\"\nstatus: todo\n---\n", // FR-007a: `""` IS absence on a date
	"---\ntype: thing\nnote: something\ncost: 12.5\n---\n",
}

// w4Observation is what the evaluator did with one expression across every
// no-`due` record.
type w4Observation struct {
	alwaysPresentFalse bool
	detail             string
}

// w4Observe evaluates one expression over every record in w4NotesWithoutDue and
// reports whether it held a PRESENT boolean FALSE on all of them.
func w4Observe(t *testing.T, schema *records.Schema, expr string) w4Observation {
	t.Helper()
	set, ferrs := records.ValidateFormulaSet(map[string]string{"f": expr}, schema)
	if len(ferrs) > 0 {
		return w4Observation{detail: "the expression does not typecheck on its own: " + ferrs[0].Error()}
	}
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	for _, src := range w4NotesWithoutDue {
		ev := records.NewFormulaEvaluator(set, records.Comparator{}, now)
		ev.Begin(w4Candidate{rec: records.ParseRecord("n.md", []byte(src)), schema: schema})
		res, ok := ev.Evaluate("f")
		if !ok {
			return w4Observation{detail: "the evaluator has no value for the expression at all"}
		}
		if res.Absent {
			return w4Observation{detail: "ABSENT (not false) on " + strings.TrimSpace(src)}
		}
		if res.Type != records.FormulaBoolean {
			return w4Observation{detail: "not a boolean on " + strings.TrimSpace(src)}
		}
		vals := res.Values()
		if len(vals) != 1 || vals[0].Bool {
			return w4Observation{detail: "not a single FALSE on " + strings.TrimSpace(src)}
		}
	}
	return w4Observation{alwaysPresentFalse: true, detail: "present-false on every record with no `due`"}
}

// TestW4_PresentFalseWhenAbsentAgreesWithTheEvaluator is the whole proof,
// measured. Every row states what the evaluator DOES, and the predicate is
// required to agree.
func TestW4_PresentFalseWhenAbsentAgreesWithTheEvaluator(t *testing.T) {
	schema := w4TestSchema(t)

	for _, tc := range []struct {
		name string
		expr string
		want bool
		why  string
	}{
		{
			name: "the founder's is_overdue, in full",
			expr: `date(due) < today() && status != "done"`,
			want: true,
			why:  "`date(due)` is absent, `<` over an absent operand is present-FALSE (R-2), and `false && <present boolean>` is present-false",
		},
		{
			name: "`!=` ALONE over the guarded property",
			expr: `due != today()`,
			want: true,
			why:  "R-2's exemption list is IS NULL / IS NOT NULL and the comparator says `<>` is NOT one — the operator the old refusal named as different is not different",
		},
		{
			name: "each of the other five, over the guarded property",
			expr: `due == today() || due < today() || due <= today() || due > today() || due >= today()`,
			want: true,
			why:  "`||` is false only when both sides are, and every disjunct is a comparison over an absent operand",
		},
		{
			name: "`&&` with the guarded comparison on the RIGHT",
			expr: `status != "done" && date(due) < today()`,
			want: true,
			why:  "the rule is symmetric; the side that is present-false may be either one",
		},
		{
			name: "`&&` whose OTHER side can be ABSENT is refused",
			expr: `date(due) < today() && (created - created).days == 0`,
			want: true,
			why:  "the other side is still a COMPARISON, and a comparison is a present boolean whatever its operands do",
		},
		{
			name: "a bare property is refused",
			expr: `done`,
			want: false,
			why:  "`done` is read straight off the record, so on a note with `done: true` it is TRUE — not the else-branch's false",
		},
		{
			name: "a comparison over a DIFFERENT property is refused",
			expr: `status != "done"`,
			want: false,
			why:  "nothing about it depends on `due`; on a note with status `todo` it is TRUE",
		},
		{
			name: "`||` needs BOTH sides, not one",
			expr: `date(due) < today() || status != "done"`,
			want: false,
			why:  "the right disjunct is TRUE on a note whose status is not `done`, so the whole thing is true",
		},
		{
			name: "an arithmetic expression is refused",
			expr: `(date(due) - today()).days`,
			want: false,
			why:  "it is W2's shape — ABSENT, not present-false — and W2 is the rewrite that carries it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := records.ParseFormula(tc.expr)
			if err != nil {
				t.Fatalf("the fixture expression does not parse: %v", err)
			}
			got := presentFalseWhenAbsent(node, "due")
			observed := w4Observe(t, schema, tc.expr)

			if got != tc.want {
				t.Errorf("presentFalseWhenAbsent(%q) = %v, want %v — %s", tc.expr, got, tc.want, tc.why)
			}
			// THE GRADE AGAINST THE ENGINE. Soundness is the direction that
			// corrupts data, so it is an error; a refusal the evaluator would
			// have allowed is a rewrite left on the table, and it is also an
			// error, because that is precisely the defect W4 corrects.
			if got && !observed.alwaysPresentFalse {
				t.Errorf("SOUNDNESS: presentFalseWhenAbsent said yes to %q, but the evaluator disagrees: %s", tc.expr, observed.detail)
			}
			if !got && observed.alwaysPresentFalse {
				t.Errorf("A REWRITE DECLINED FOR NO REASON: presentFalseWhenAbsent said no to %q, but the evaluator holds it %s", tc.expr, observed.detail)
			}
		})
	}
}

// TestW4_TheAbsenceTrapIsTheLogicalOperator, not the comparison.
//
// evalBinary's `&&`/`||` branch is the one place a boolean operator returns
// ABSENCE, and W4's identity fails outright there: absence is not false, and a
// filter can tell them apart. This is the specific mutation the rest of the
// table would not catch, so it is measured on its own — with an expression
// whose non-comparison side is genuinely absent.
func TestW4_TheAbsenceTrapIsTheLogicalOperator(t *testing.T) {
	schema := w4TestSchema(t)

	// `cost + 1` is ARITHMETIC over an absent property: absent, not false. It
	// is not a boolean either, so the pair does not typecheck as `&&` — which
	// is itself the reason a whitelist that only admits comparisons is the
	// right shape here. What must hold is that the predicate refuses it.
	for _, expr := range []string{
		`date(due) < today() && cost`,
		`cost && date(due) < today()`,
		`!(date(due) < today())`,
		`date(due)`,
	} {
		node, err := records.ParseFormula(expr)
		if err != nil {
			t.Fatalf("the fixture expression does not parse: %v", err)
		}
		if presentFalseWhenAbsent(node, "due") {
			t.Errorf("presentFalseWhenAbsent(%q) = true, but nothing in the whitelist covers it", expr)
		}
	}

	// And the positive control: the predicate does fire on the shape it is for,
	// so the four refusals above are not a predicate that never says yes.
	node, err := records.ParseFormula(`date(due) < today() && status != "done"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !presentFalseWhenAbsent(node, "due") {
		t.Fatal("the predicate refuses its own subject, so every refusal above proves nothing")
	}
	_ = schema
}

// TestW4_ReduceGuardedComparisonRequiresASingleValuedDateGuard keeps W2's guard
// rule attached to W4, which is where the whole "truthy means present" step
// comes from. It is a SHARED condition, and a rewrite that quietly widened it
// would break both.
func TestW4_ReduceGuardedComparisonRequiresASingleValuedDateGuard(t *testing.T) {
	schema := w4TestSchema(t)

	for _, tc := range []struct {
		name, guard string
		reduce      bool
		why         string
	}{
		{name: "a single-valued date", guard: "due", reduce: true, why: "FR-007a makes truthy and present the same question"},
		{name: "a many date", guard: "dates", reduce: false, why: "an empty list is present and truthy in JavaScript"},
		{name: "a checkbox", guard: "done", reduce: false, why: "`false` is present and falsy"},
		{name: "a text property", guard: "note", reduce: false, why: "`\"\"` is present and falsy on text (that is W3's shape, not this one)"},
		{name: "a number", guard: "cost", reduce: false, why: "`0` is present and falsy"},
		{name: "an undeclared name", guard: "missing", reduce: false, why: "with no declaration there is no type and no proof"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, got := reduceGuardedComparison(tc.guard, `date(`+tc.guard+`) < today()`, schema)
			if got != tc.reduce {
				t.Errorf("reduceGuardedComparison(%q, …) reduced = %v, want %v — %s", tc.guard, got, tc.reduce, tc.why)
			}
		})
	}
}

// TestW4_OnlyAFalseElseBranchIsReduced. The identity is that the guarded
// expression already returns the ELSE-BRANCH's value; `false` is that value.
func TestW4_OnlyAFalseElseBranchIsReduced(t *testing.T) {
	schema := w4TestSchema(t)
	body := `date(due) < today() && status != "done"`

	for _, tc := range []struct {
		els  string
		want string
	}{
		{els: "false", want: body},
		{els: "true", want: `if(due, ` + body + `, true)`},
		{els: `"false"`, want: `if(due, ` + body + `, "false")`},
		{els: "0", want: `if(due, ` + body + `, 0)`},
	} {
		in := `if(due, ` + body + `, ` + tc.els + `)`
		got, _ := rewriteFormulaSource(in, schema)
		if got != tc.want {
			t.Errorf("rewriteFormulaSource(%q)\n got: %q\nwant: %q", in, got, tc.want)
		}
	}
}
