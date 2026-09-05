// Tests for the untyped-view unit gate on GET /knowledge/view — the G2+G3
// bypass found in review: a view that declares no `type` (legal per FR-018b)
// used to resolve NO companion unit for its numbers, key every row into the
// single unit-less accumulator, and emit ONE combined cross-unit figure that
// also swallowed the G3 unit-less row. 100.50 SGD + 200.00 EUR + 49.50 SGD +
// 10.00 (no currency) answered "360.00" with no refusal — the design's exact
// failure mode, "a wrong number that looks right".
//
// Expected values are derived from the DESIGN (view-kinds-design-2026-09-03
// §3): G2 admits no untyped exception ("No combined figure is ever emitted"),
// and G3 rows are "excluded from every total". When the view cannot resolve
// units, the only honest answers are a per-unit total (impossible without a
// schema) or a refusal that says why. These tests pin the refusal.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// buildUntypedViewTestVault is buildViewTestVault plus a second record type
// (`shipment`, whose `weight` declares NO companion unit) and two shipment
// notes — the control that proves the untyped gate refuses only where a
// schema actually pairs a unit with the totalled property.
func buildUntypedViewTestVault(t *testing.T, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Finance vault")

	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", viewTestInvoiceSchema)
	writeNote(t, vault, ".omnipus-vault/records/shipment.yaml",
		"schema_version: 1\n"+
			"type: shipment\n"+
			"properties:\n"+
			"  weight: { type: decimal }\n")
	writeNote(t, vault, "a.md", viewTestInvoice("INV-1", "Acme", "100.50", "SGD"))
	writeNote(t, vault, "b.md", viewTestInvoice("INV-2", "Acme", "200.00", "EUR"))
	writeNote(t, vault, "c.md", viewTestInvoice("INV-3", "Bolt", "49.50", "SGD"))
	// The G3 row: an amount with NO currency.
	writeNote(t, vault, "d.md", viewTestInvoice("INV-4", "Bolt", "10.00", ""))
	writeNote(t, vault, "s1.md", "---\ntype: shipment\nid: SHP-1\nweight: 2.50\n---\n# SHP-1\n")
	writeNote(t, vault, "s2.md", "---\ntype: shipment\nid: SHP-2\nweight: 1.25\n---\n# SHP-2\n")
	for name, body := range views {
		writeNote(t, vault, ".omnipus-vault/views/"+name, body)
	}

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	require.NoError(t, err)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// requireNoTotalOf fails if any total or subtotal anywhere in the result
// aggregates the given property — the whole point of the refusal is that no
// figure over it exists to mislead with.
func requireNoTotalOf(t *testing.T, res *gen.ViewResult, property string) {
	t.Helper()
	for pi, part := range res.Parts {
		if part.Totals != nil {
			for _, tot := range *part.Totals {
				assert.NotEqual(t, property, tot.Property,
					"parts[%d] carries a total of %q (value %s over %d values); an untyped view must refuse to total a unit-carrying number (G2)",
					pi, property, tot.Value, tot.Count)
			}
		}
		if part.Groups != nil {
			for gi, g := range *part.Groups {
				for _, tot := range g.Subtotals {
					assert.NotEqual(t, property, tot.Property,
						"parts[%d].groups[%d] carries a subtotal of %q; an untyped view must refuse to total a unit-carrying number (G2)",
						pi, gi, property)
				}
			}
		}
		if part.Series != nil {
			assert.Empty(t, *part.Series,
				"parts[%d] carries chart series over %q; an untyped view must refuse to total a unit-carrying number (G2)",
				pi, property)
		}
		if part.Crosstab != nil {
			assert.Empty(t, part.Crosstab.Cells,
				"parts[%d] carries crosstab cells over %q; an untyped view must refuse to total a unit-carrying number (G2)",
				pi, property)
		}
	}
}

// requireUnitRefusalProblem asserts exactly one aggregate_refused problem
// names the property and the untyped-view cause.
func requireUnitRefusalProblem(t *testing.T, res *gen.ViewResult, property string) {
	t.Helper()
	matches := 0
	for _, p := range res.Problems {
		if p.Code != gen.AggregateRefused {
			continue
		}
		if !strings.Contains(p.Reason, `"`+property+`"`) {
			continue
		}
		matches++
		assert.Contains(t, p.Reason, "type",
			"the refusal must say the view declares no `type`, so the fix is discoverable")
		require.NotNil(t, p.Fix, "the refusal must carry a fix")
	}
	assert.Equal(t, 1, matches,
		"want exactly ONE aggregate_refused problem naming %q (deduplicated across parts and groups); got %d in %+v",
		property, matches, res.Problems)
}

// TestUntypedView_RefusesUnitCarryingTotals is the reproduction of the review
// finding: kind=summary, no `type`, figures over `amount` — the property the
// invoice schema declares with unit_property=currency. The old behaviour
// emitted one combined "360.00" total (SGD+EUR+the G3 row) with no refusal.
func TestUntypedView_RefusesUnitCarryingTotals(t *testing.T) {
	api, ws, colID := buildUntypedViewTestVault(t, map[string]string{
		"all-amounts.yaml": "name: all-amounts\n" +
			"kind: summary\n" +
			"layout: table\n" +
			"properties: [file.name, amount]\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: amount\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "all-amounts")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal, "the view still serves — its ROWS are honest; only the totals are refused")
	assert.Len(t, res.Rows, 6, "every note in scope stays shown (4 invoices + 2 shipments)")

	requireNoTotalOf(t, res, "amount")
	requireUnitRefusalProblem(t, res, "amount")

	// The figures part answers an EMPTY totals list, not an absent one, so
	// the SPA renders its explicit "no figures" state instead of nothing.
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	assert.Empty(t, *res.Parts[0].Totals)
}

// TestUntypedView_RefusesUnitCarryingSubtotals covers the OTHER aggregation
// entry — a table part's subtotals map — and pins the dedup: figures AND
// grouped subtotals over the same property report ONE problem, not one per
// scope.
func TestUntypedView_RefusesUnitCarryingSubtotals(t *testing.T) {
	api, ws, colID := buildUntypedViewTestVault(t, map[string]string{
		"amount-table.yaml": "name: amount-table\n" +
			"layout: table\n" +
			"properties: [file.name, client, amount]\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: amount\n" +
			"    aggregate: sum\n" +
			"  - part: table\n" +
			"    grouping: [{property: client, direction: asc}]\n" +
			"    subtotals: {amount: sum}\n",
	})

	res, code := getViewResult(t, api, ws, colID, "amount-table")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)

	requireNoTotalOf(t, res, "amount")
	requireUnitRefusalProblem(t, res, "amount")
}

// TestUntypedView_StillTotalsUnitlessNumbers is the control against
// over-refusal: `weight` is a plain decimal no schema pairs with a unit, so
// an untyped view totals it exactly as before — unit-less, one figure.
func TestUntypedView_StillTotalsUnitlessNumbers(t *testing.T) {
	api, ws, colID := buildUntypedViewTestVault(t, map[string]string{
		"all-weights.yaml": "name: all-weights\n" +
			"kind: summary\n" +
			"layout: table\n" +
			"properties: [file.name, weight]\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: weight\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "all-weights")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	assert.Empty(t, res.Problems, "a number no schema pairs with a unit refuses nothing")

	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	totals := *res.Parts[0].Totals
	require.Len(t, totals, 1, "one unit-less total over the two shipment rows")
	assert.Equal(t, "weight", totals[0].Property)
	assert.Equal(t, "3.75", totals[0].Value)
	assert.Equal(t, 2, totals[0].Count)
	assert.Nil(t, totals[0].Unit)
}

// TestTypedView_UnitTotalsUnchanged guards the fix's blast radius: the typed
// summary view still answers per-unit totals with the G3 row excluded —
// byte-for-byte the behaviour the existing G2/G3 tests pin, exercised here
// against the two-schema vault.
func TestTypedView_UnitTotalsUnchanged(t *testing.T) {
	api, ws, colID := buildUntypedViewTestVault(t, map[string]string{
		"typed-amounts.yaml": "name: typed-amounts\n" +
			"type: invoice\n" +
			"kind: summary\n" +
			"layout: table\n" +
			"properties: [file.name, amount]\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: amount\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "typed-amounts")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	assert.Empty(t, res.Problems)

	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	totals := *res.Parts[0].Totals
	require.Len(t, totals, 2, "two currencies, two totals — never one")
	byUnit := map[string]gen.ViewUnitTotal{}
	for _, tot := range totals {
		byUnit[unitOf(t, tot)] = tot
	}
	assert.Equal(t, "150.00", byUnit["SGD"].Value)
	assert.Equal(t, "200.00", byUnit["EUR"].Value)
	require.NotNil(t, res.Parts[0].ExcludedCount)
	assert.Equal(t, 1, *res.Parts[0].ExcludedCount, "the currency-less invoice is excluded and counted (G3)")
}
