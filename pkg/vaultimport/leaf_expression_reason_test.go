// Omnipus — a `.base` FUNCTION EXPRESSION is refused BY NAME, in the words of
// the one expression parser this product has.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS ABOUT, AND WHAT IT DELIBERATELY DOES NOT CLAIM
//
// `date(close_date).year == today().year` is an ordinary "closing this year"
// filter. This importer cannot read it, and after this change it STILL cannot
// read it — `.year` and `.month` are not in the formula grammar, the grammar is
// PINNED by FR-143 to the Obsidian syntax reference as fetched 2026-08-30, and
// widening a pinned grammar so that one more view imports is a spec revision
// with its own diff, never a silent code change. Deals.base's "Closing This
// Month" stays disabled, and TestFunctionExpression_StaysUntranslated is the
// assertion that says so out loud.
//
// What changed is the SILENCE. The clause was refused by a regex marker that
// enumerates function SHAPES and says nothing, so the founder's report carried
// his own text and no diagnosis. The refusal now comes from records.ParseFormula
// and quotes it verbatim.
//
// THE ORACLE IS THE GRAMMAR, NOT A STRING THIS PACKAGE HAPPENS TO EMIT. Each
// test below asks records.ParseFormula for the refusal FIRST and then requires
// the loss line to carry it. Pasting the message in as a literal would make the
// test agree with whatever wording vaultimport chose; deriving it means the test
// keeps measuring "the founder is told what the grammar said" even after the
// grammar is reworded — and FAILS if a second parser is ever grown here and the
// two start to disagree, which is the actual risk this design is guarding.
// ---------------------------------------------------------------------------

// lossesForExpr runs one `.base` filter expression through the whole path —
// tree walk, then schema resolution — and returns whether it produced a filter
// node plus the rendered loss lines.
func lossesForExpr(t *testing.T, expr string, r leafResolver) (translated bool, losses []string) {
	t.Helper()
	tr := TranslateFilterTree(yamlTree(t, expr))
	node, losses := r.resolve(tr.Root, LossViewFilter)
	return node != nil, losses
}

// grammarRefusalFor is the oracle: what the ONE expression parser says about
// this expression. A test calling it fails loudly if the expression has since
// become parseable, rather than silently asserting nothing.
func grammarRefusalFor(t *testing.T, expr string) string {
	t.Helper()
	_, err := records.ParseFormula(expr)
	if err == nil {
		t.Fatalf("records.ParseFormula now READS %q — the pinned grammar has been widened. That is a spec revision (FR-143), and this test must be rewritten to assert the TRANSLATION rather than the refusal", expr)
	}
	return err.Error()
}

// TestFunctionExpression_StaysUntranslated is the FR-105 half and it comes
// first: the clause must not have become a filter node. A reason is worth
// nothing if it were bought by translating something the grammar cannot express.
func TestFunctionExpression_StaysUntranslated(t *testing.T) {
	r := leafResolver{recordType: "deal", schemas: NewSchemaIndex(nil)}
	for _, expr := range dealsClosingThisMonthClauses {
		t.Run(expr, func(t *testing.T) {
			translated, losses := lossesForExpr(t, expr, r)
			if translated {
				t.Errorf("%q produced a filter node — `.year`/`.month` are not in the pinned formula grammar, so any node built here is an invention, and under a `not:` an invented clause BROADENS the view", expr)
			}
			if len(losses) != 1 {
				t.Errorf("%q produced %d losses, want exactly 1 — the clause is reported once, whatever it says", expr, len(losses))
			}
		})
	}
}

// dealsClosingThisMonthClauses are the two clauses from the founder's own
// Deals.base, quoted rather than invented so the test is about his vault.
var dealsClosingThisMonthClauses = []string{
	`date(close_date).fortnight == 1`,
	`date(close_date).epoch == 1`,
}

func TestFunctionExpression_IsRefusedInTheGrammarsOwnWords(t *testing.T) {
	r := leafResolver{recordType: "deal", schemas: NewSchemaIndex(nil)}
	for _, expr := range dealsClosingThisMonthClauses {
		t.Run(expr, func(t *testing.T) {
			wantRefusal := grammarRefusalFor(t, expr)

			_, losses := lossesForExpr(t, expr, r)
			if len(losses) != 1 {
				t.Fatalf("got %d losses, want 1: %v", len(losses), losses)
			}
			line := losses[0]

			reason := lossReasonHalf(line)
			if reason == "" {
				t.Fatalf("the refusal carries NO reason — the founder is told the clause went and not why, which is the whole gap:\n  %s", line)
			}
			if !strings.Contains(reason, wantRefusal) {
				t.Errorf("the loss does not carry the formula grammar's own refusal.\n  grammar said: %s\n  loss said:    %s", wantRefusal, reason)
			}
			// The operator's own text must still be named alongside it.
			if !strings.Contains(line, expr) {
				t.Errorf("the loss line no longer quotes the expression the operator wrote:\n  %s", line)
			}
			// And the position prefix has to survive, because that is what
			// decides whether the view is disabled.
			if pos, ok := parseLossPosition(line); !ok || pos != LossViewFilter {
				t.Errorf("loss position = %q (parsed=%v), want %q", pos, ok, LossViewFilter)
			}
		})
	}
}

// TestFunctionExpression_ThatParsesIsStillNotAFilterLeaf is the other branch,
// and it needs a DIFFERENT sentence: "the grammar cannot read this" and "the
// grammar reads this, but a filter cannot hold it" are two different things for
// the author to fix, and collapsing them produces a message that helps with
// neither.
func TestFunctionExpression_ThatParsesIsStillNotAFilterLeaf(t *testing.T) {
	const expr = `file.tags.contains("urgent")`

	// The oracle for THIS branch is that the grammar does read it. If that
	// stops being true the test is measuring the wrong branch and says so.
	if _, err := records.ParseFormula(expr); err != nil {
		t.Fatalf("this test needs an expression the grammar READS; %q is now refused (%v), so pick another", expr, err)
	}

	r := leafResolver{recordType: "decision", schemas: decisionSchema()}
	translated, losses := lossesForExpr(t, expr, r)
	if translated {
		t.Fatalf("%q produced a filter node — an inline expression is not a filter leaf", expr)
	}
	if len(losses) != 1 {
		t.Fatalf("got %d losses, want 1: %v", len(losses), losses)
	}

	reason := lossReasonHalf(losses[0])
	if reason == "" {
		t.Fatalf("an expression the grammar reads was still dropped with no reason:\n  %s", losses[0])
	}
	if strings.Contains(reason, "refused there too") {
		t.Errorf("an expression the grammar READS was reported as one the grammar cannot read — the two branches have collapsed into one sentence:\n  %s", reason)
	}
	if !strings.Contains(reason, "`formula.<name>`") {
		t.Errorf("the reason does not tell the author the one route an expression has into a filter (a declared `formula.<name>`):\n  %s", reason)
	}
}

// TestFormulaNamespaceRefusals_KeepTheirOwnReasons pins the deliberate hole.
// A `formula.*` clause that cannot be built is diagnosed where the base's
// formula set and the view's record type are both known, which is the only
// place that can say something specific. Printing this file's generic
// expression sentence on top would name the wrong gap — a formula problem
// reported as a function-call problem — so the marker path leaves the formula
// namespace exactly as it found it.
func TestFormulaNamespaceRefusals_KeepTheirOwnReasons(t *testing.T) {
	r := leafResolver{recordType: "decision", schemas: decisionSchema()}
	for _, expr := range []string{
		`formula.is_overdue`,
		`formula.age > formula.threshold`,
	} {
		t.Run(expr, func(t *testing.T) {
			translated, losses := lossesForExpr(t, expr, r)
			if translated {
				t.Fatalf("%q became a filter node", expr)
			}
			if len(losses) != 1 {
				t.Fatalf("got %d losses, want 1: %v", len(losses), losses)
			}
			if got := lossReasonHalf(losses[0]); strings.Contains(got, filterIsNotAnExpression) {
				t.Errorf("a `formula.*` clause picked up the generic expression reason, which classifies it as a `.base` function-call gap instead of a FORMULA gap:\n  %s", losses[0])
			}
		})
	}
}

// TestUntranslatableReason_IsOneLiteral guards the coupling that is invisible
// from inside this package: report.go's closed gap table classifies a loss by
// matching substrings of the reason THIS file writes. A sentence that exists in
// two spellings is a gap shape that catches half its own cases, and nothing
// else in the build would notice.
func TestUntranslatableReason_IsOneLiteral(t *testing.T) {
	both := []string{
		untranslatableExpressionReason(`date(x).fortnight == 1`),
		untranslatableExpressionReason(`file.tags.contains("x")`),
	}
	for _, got := range both {
		if !strings.HasPrefix(got, filterIsNotAnExpression) {
			t.Errorf("a refusal that does not open with the shared literal cannot be classified by the report:\n  want prefix: %s\n  got:         %s", filterIsNotAnExpression, got)
		}
	}
	if both[0] == both[1] {
		t.Errorf("both branches produced the SAME sentence — a reader cannot tell an unreadable expression from a readable one that a filter cannot hold:\n  %s", both[0])
	}
}
