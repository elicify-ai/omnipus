// Tests for a view's LEGACY `aggregates:` key on GET /knowledge/view
// (finding 4, ranked #6).
//
// `aggregates` predates the part stack and 69 saved views already use it. The
// bridge forwards it into the engine request, the engine computes it, and
// collectRows then DROPPED the answer on the floor — so the same saved view
// showed its totals in knowledge_find and showed none in the base preview.
// A panel that silently omits a number the chat is willing to state is worse
// than one that never had it: the reader concludes the view has no totals.
//
// Surfacing them USED TO BE conditional. The engine's aggregation was
// unit-blind (the deferred G2 gap recorded in
// pkg/records/knowledgefind/unit_aggregate_g2_test.go), so a total over a
// number with a DECLARED companion unit would have been exactly the combined
// cross-unit figure this endpoint refuses everywhere else, and those were
// dropped here and refused by name.
//
// D7 (2026-09-05) moved G2 into the engine: it partitions by unit value itself
// and answers ONE TOTAL PER UNIT. The compensation is therefore gone — keeping
// it would drop CORRECT per-unit figures and tell the reader they could not be
// computed, which is the finding this whole file is about, re-introduced by
// the fix for it.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestKnowledgeViewLegacy_AggregatesReachTheAnswer is the reproduction: a view
// declaring `aggregates` over a number NO schema pairs with a unit must show
// them, exactly as knowledge_find does for the same view.
func TestKnowledgeViewLegacy_AggregatesReachTheAnswer(t *testing.T) {
	api, ws, colID := buildUntypedViewTestVault(t, map[string]string{
		"weights.yaml": "name: weights\n" +
			"type: shipment\n" +
			"layout: table\n" +
			"aggregates:\n" +
			"  - {op: sum, property: weight}\n" +
			"  - {op: count}\n",
	})

	res, code := getViewResult(t, api, ws, colID, "weights")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)

	require.NotNil(t, res.Aggregates, "a view's own `aggregates` must reach the answer, not be dropped")
	byOp := map[string]gen.VaultFindTotal{}
	for _, a := range *res.Aggregates {
		byOp[string(a.Op)] = a
	}

	require.Contains(t, byOp, "sum")
	assert.Equal(t, "3.75", byOp["sum"].Value, "2.50 + 1.25, by hand")
	assert.NotEmpty(t, byOp["sum"].Scope, "FR-125: the scope travels with the number")

	require.Contains(t, byOp, "count")
	assert.Equal(t, "2", byOp["count"].Value)

	// The engine agrees, on the same collection, through its own surface.
	assert.Equal(t, findAggregateValue(t, api, ws, colID, "shipment", "sum", "weight"), byOp["sum"].Value,
		"the base preview and knowledge_find must state one number one way")
}

// TestKnowledgeViewLegacy_UnitCarryingAggregatesAreServedPerUnit is the D7
// half. The engine no longer hands this endpoint a cross-unit figure to guard
// against: it hands it ONE TOTAL PER UNIT VALUE, and those are surfaced.
//
// The vault holds 100.50 SGD, 200.00 EUR, 49.50 SGD and one amount with NO
// currency. Added by hand: SGD = 150.00 over two rows, EUR = 200.00 over one,
// and the unit-less row is in neither (G3). The figures G2 forbids are 350.50
// (all three currencies added) and 360.00 (with the unit-less row folded in).
func TestKnowledgeViewLegacy_UnitCarryingAggregatesAreServedPerUnit(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"amounts.yaml": "name: amounts\n" +
			"type: invoice\n" +
			"layout: table\n" +
			"aggregates:\n" +
			"  - {op: sum, property: amount}\n" +
			"  - {op: count}\n",
	})

	res, code := getViewResult(t, api, ws, colID, "amounts")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.NotNil(t, res.Aggregates, "per-unit totals must reach the answer, not be dropped")

	byUnit := map[string]gen.VaultFindTotal{}
	var count *gen.VaultFindTotal
	for i, a := range *res.Aggregates {
		assert.NotEqual(t, "350.50", a.Value, "100.50 SGD + 200.00 EUR + 49.50 SGD is a figure in no currency (G2)")
		assert.NotEqual(t, "360.00", a.Value, "and neither is that sum with the unit-less row folded in")
		switch {
		case string(a.Op) == "count":
			count = &(*res.Aggregates)[i]
		case a.Unit != nil:
			byUnit[*a.Unit] = a
		default:
			t.Fatalf("a sum over a unit-carrying number reached the answer with NO unit — that IS the combined figure: %+v", a)
		}
	}

	require.Contains(t, byUnit, "SGD")
	assert.Equal(t, "150.00", byUnit["SGD"].Value, "100.50 + 49.50, by hand")
	require.NotNil(t, byUnit["SGD"].UnitProperty)
	assert.Equal(t, "currency", *byUnit["SGD"].UnitProperty)

	require.Contains(t, byUnit, "EUR")
	assert.Equal(t, "200.00", byUnit["EUR"].Value)

	// G3: the unit-less row is counted in the scope that travels with each
	// number, not silently absorbed into one of the currencies.
	for unit, tot := range byUnit {
		assert.Contains(t, tot.Scope, "excluded from every total (G3)",
			"the %s total does not account for the unit-less row: %q", unit, tot.Scope)
	}

	// `count` counts ROWS, so it crosses no units and is unpartitioned.
	require.NotNil(t, count, "count counts rows, not amounts, so G2 does not touch it")
	assert.Equal(t, "4", count.Value)
	assert.Nil(t, count.Unit, "a count is dimensionless")

	// And nothing is refused any more: the number exists and is correct.
	for _, p := range res.Problems {
		assert.NotContains(t, p.Reason, "legacy `aggregates`",
			"the interim G2 drop is superseded by the engine's own per-unit totals: %+v", p)
	}

	// The engine agrees, on the same collection, through its own surface.
	assert.Equal(t, findAggregateValueInUnit(t, api, ws, colID, "invoice", "sum", "amount", "SGD"), byUnit["SGD"].Value,
		"the base preview and knowledge_find must state one number one way")
}

// TestKnowledgeViewLegacy_TextAggregatesStayRefusedByTheEngine is the G4 half.
// The engine already refuses a summary a type does not define, and marks the
// total refused rather than omitting it — so a refused total is SURFACED and
// marked, never quietly dropped into the same silence the whole finding is
// about.
func TestKnowledgeViewLegacy_TextAggregatesStayRefusedByTheEngine(t *testing.T) {
	api, ws, colID := buildG4TestVault(t, map[string]string{
		"codes.yaml": "name: codes\n" +
			"type: ticket\n" +
			"layout: table\n" +
			"aggregates:\n" +
			"  - {op: sum, property: code}\n",
	})

	res, code := getViewResult(t, api, ws, colID, "codes")
	require.Equal(t, 200, code)

	// The engine refuses `sum` on a text property at parse time, so the whole
	// view refuses rather than serving a total. Either way, the one thing that
	// must never happen is a 4600 appearing anywhere.
	if res.Aggregates != nil {
		for _, a := range *res.Aggregates {
			assert.NotEqual(t, "4600", a.Value, "text is never totalled (G4)")
			assert.NotEqual(t, "4,600", a.Value, "text is never totalled (G4)")
		}
	}
	for _, p := range res.Parts {
		if p.Totals != nil {
			for _, tot := range *p.Totals {
				assert.NotEqual(t, "4600", tot.Value)
			}
		}
	}
}
