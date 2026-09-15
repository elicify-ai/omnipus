// view_formula_test.go: tests for synthesise filter formulas

package vaultimport

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// --- moved from view_write.go tests 2026-09-15 ---

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
