// Omnipus — gate G2 at the FIND layer: a number with a declared companion
// unit must not be summed across units (view-kinds-design-2026-09-03 §3 G2).
//
// HISTORY, kept because it is the evidence this file exists for. This test was
// written, run, observed to FAIL, and then SKIPPED rather than deleted, so the
// defect stayed visible while the fix waited on four design questions the
// engine could not answer on its own. The founder ruled on 2026-09-05 (D7 in
// the design's §9) and the skip is gone: the observed red was
//
//	sum(amount) answered "300.50" — 100.50 SGD + 200.00 EUR is a figure in no
//	currency; G2 admits no combined total
//
// The full per-unit behaviour, its bounds and its refusals live in
// unit_totals_test.go. This file stays as the narrowest possible statement of
// the original defect: two currencies, one sum, no combined figure.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestFind_SumDoesNotCrossUnits_G2 is the FIND-layer half of G2.
//
// The expected shape is derived from G2, not from the engine: "a
// number-with-unit totals ONCE PER UNIT VALUE, never across units. No combined
// figure is ever emitted."
func TestFind_SumDoesNotCrossUnits_G2(t *testing.T) {
	f := newUnitFixture(t, map[string]string{"invoice.yaml": invoiceUnitSchema})
	f.write("a.md", "---\ntype: invoice\nid: INV-1\nclient: Acme\namount: 100.50\ncurrency: SGD\n---\n# INV-1\n")
	f.write("b.md", "---\ntype: invoice\nid: INV-2\nclient: Acme\namount: 200.00\ncurrency: EUR\n---\n# INV-2\n")

	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("invoice"),
		Aggregate: agg(generated.VaultFindAggregateOpSum, "amount"),
	})
	if resp.Refused {
		t.Fatalf("the fixture query must be answerable: %+v", resp.Problems)
	}

	for _, total := range resp.Totals {
		if total.Value == "300.50" {
			t.Fatalf("sum(amount) answered %q — 100.50 SGD + 200.00 EUR is a figure in no currency; "+
				"G2 admits no combined total", total.Value)
		}
	}

	// The positive half: two units in the data means two totals, or a refusal
	// that says why there are none. A single unit-less number is the one
	// answer G2 forbids.
	if len(resp.Totals) == 1 && resp.Totals[0].Refused == nil {
		t.Fatalf("one unqualified total over two units: %+v", resp.Totals[0])
	}
	if len(resp.Totals) != 2 {
		t.Fatalf("two units, two totals; got %d: %+v", len(resp.Totals), resp.Totals)
	}
	for _, total := range resp.Totals {
		if total.Unit == nil {
			t.Fatalf("a total over a unit-carrying number states its unit: %+v", total)
		}
	}
}
