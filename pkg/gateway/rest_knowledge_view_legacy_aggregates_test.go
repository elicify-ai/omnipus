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
// Surfacing them is not unconditional. They come from the engine's own
// unit-blind aggregation (the deferred G2 gap recorded in
// pkg/records/knowledgefind/unit_aggregate_g2_test.go), so a total over a
// number with a DECLARED companion unit would be exactly the combined
// cross-unit figure this endpoint refuses everywhere else. Those are refused
// here too, by the same gate and with the same reasoning.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"strings"
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

// TestKnowledgeViewLegacy_UnitCarryingAggregatesAreRefused holds the gate. The
// engine's `aggregate` is unit-blind, so sum(amount) over SGD and EUR is a
// figure in no currency — the exact output G2 forbids, and the exact thing
// surfacing the engine's totals raw would have introduced.
func TestKnowledgeViewLegacy_UnitCarryingAggregatesAreRefused(t *testing.T) {
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
	require.Nil(t, res.Refusal, "the view still serves; only the cross-unit total is withheld")

	if res.Aggregates != nil {
		for _, a := range *res.Aggregates {
			assert.NotEqual(t, "350.50", a.Value,
				"100.50 SGD + 200.00 EUR + 49.50 SGD is a figure in no currency (G2)")
			assert.NotEqual(t, "360.00", a.Value,
				"and neither is that sum with the unit-less row folded in")
			assert.NotContains(t, strings.ToLower(a.Label), "sum(amount)",
				"a unit-carrying sum must not be surfaced at all")
		}
		// `count` counts ROWS, so it crosses no units and survives.
		found := false
		for _, a := range *res.Aggregates {
			if string(a.Op) == "count" {
				found = true
				assert.Equal(t, "4", a.Value)
			}
		}
		assert.True(t, found, "count counts rows, not amounts, so it is unaffected by G2")
	}

	matches := 0
	for _, p := range res.Problems {
		if p.Code == gen.AggregateRefused && strings.Contains(p.Reason, `"amount"`) {
			matches++
			assert.Contains(t, p.Reason, "G2")
			assert.Contains(t, p.Reason, "currency", "the refusal names the companion unit")
			require.NotNil(t, p.Fix)
		}
	}
	assert.Equal(t, 1, matches,
		"want exactly ONE aggregate_refused problem naming amount; got %d in %+v", matches, res.Problems)
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
