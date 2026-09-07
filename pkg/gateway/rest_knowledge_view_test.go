// Tests for GET /api/v1/library/{workspace_id}/knowledge/view — the
// view-result endpoint (view-kinds-design-2026-09-03 §7).
//
// Expected values are derived from the DESIGN's gate rules, not from what the
// handler happens to do: G2 (a number with a companion unit totals once per
// unit value, never across units — so a two-currency vault answers TWO totals
// and no field anywhere holds 350.50), G3 (a row with no confirmed unit is
// shown, excluded from every total, and counted), §4 (a legacy view with no
// `parts` serves as one table part), and FR-105 (a disabled view refuses BY
// NAME rather than answering or vanishing).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// --- fixture ----------------------------------------------------------------

const viewTestInvoiceSchema = "schema_version: 1\n" +
	"type: invoice\n" +
	"properties:\n" +
	"  client:   { type: text }\n" +
	"  amount:   { type: decimal, unit_property: currency }\n" +
	"  currency: { type: enum, values: [SGD, EUR] }\n"

func viewTestInvoice(id, client, amount, currency string) string {
	note := "---\n" +
		"type: invoice\n" +
		"id: " + id + "\n" +
		"client: " + client + "\n" +
		"amount: " + amount + "\n"
	if currency != "" {
		note += "currency: " + currency + "\n"
	}
	return note + "---\n# " + id + "\n"
}

// buildViewTestVault seeds a workspace vault with an invoice schema, four
// invoices in two currencies (one with NO currency — the G3 row), the given
// view files, and BOTH indexes (text via SyncTracked, properties via
// vaultprops.Sync — the same two writers the mount lifecycle drives).
// It returns the api, workspace id and collection id.
func buildViewTestVault(t *testing.T, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Finance vault")

	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", viewTestInvoiceSchema)
	writeNote(t, vault, "a.md", viewTestInvoice("INV-1", "Acme", "100.50", "SGD"))
	writeNote(t, vault, "b.md", viewTestInvoice("INV-2", "Acme", "200.00", "EUR"))
	writeNote(t, vault, "c.md", viewTestInvoice("INV-3", "Bolt", "49.50", "SGD"))
	// The G3 row: an amount with NO currency. Shown, excluded, counted.
	writeNote(t, vault, "d.md", viewTestInvoice("INV-4", "Bolt", "10.00", ""))
	for name, body := range views {
		writeNote(t, vault, ".omnipus-vault/views/"+name, body)
	}

	// Index against the RESOLVED path — macOS temp dirs are symlinks, and the
	// handler evaluates against the scope's resolved collection root.
	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	require.NoError(t, err)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

func getViewResult(t *testing.T, api *restAPI, ws, collectionID, view string) (*gen.ViewResult, int) {
	t.Helper()
	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/view?collection_id="+collectionID+"&view="+view)
	if w.Code != http.StatusOK {
		return nil, w.Code
	}
	res := decodeJSON[gen.ViewResult](t, w)
	return &res, w.Code
}

// unitOf reads a total's unit, failing loudly on the one thing G2 forbids: a
// total with NO unit over a unit-carrying number would BE the combined figure.
func unitOf(t *testing.T, tot gen.ViewUnitTotal) string {
	t.Helper()
	require.NotNil(t, tot.Unit,
		"a total over a number with a companion unit must be unit-scoped; a unit-less entry is a combined figure (G2)")
	return *tot.Unit
}

// --- G2 + G3: the summary view ----------------------------------------------

const viewTestSummaryView = "name: unpaid--by-client\n" +
	"label: Unpaid, by client\n" +
	"type: invoice\n" +
	"kind: summary\n" +
	"layout: table\n" +
	"parts:\n" +
	"  - part: figures\n" +
	"    number: amount\n" +
	"    unit: currency\n" +
	"    aggregate: sum\n" +
	"  - part: table\n" +
	"    grouping: [{property: client, direction: asc}]\n" +
	"    subtotals: {amount: sum}\n"

func TestKnowledgeView_SummaryTotalsOncePerUnitNeverCombined_G2(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"unpaid--by-client.yaml": viewTestSummaryView,
	})
	res, code := getViewResult(t, api, ws, colID, "unpaid--by-client")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal, "a servable view must not refuse")

	assert.Equal(t, "unpaid--by-client", res.View)
	assert.Equal(t, "Unpaid, by client", res.Label)
	require.NotNil(t, res.Kind)
	assert.Equal(t, "summary", *res.Kind)
	require.Len(t, res.Rows, 4, "every row is SHOWN, including the unit-less one (G3)")

	require.Len(t, res.Parts, 2, "the summary stack is figures then table (design section 2.3)")
	figures, table := res.Parts[0], res.Parts[1]
	assert.Equal(t, gen.ViewResultPartPartFigures, figures.Part)
	assert.Equal(t, gen.ViewResultPartPartTable, table.Part)

	// THE HEADLINE ASSERTION: two currencies, two totals, and 350.50 — the
	// number a combined sum would produce — appears in neither of them.
	require.NotNil(t, figures.Totals)
	totals := *figures.Totals
	require.Len(t, totals, 2, "two units in the data means exactly two totals, never one combined figure (G2)")
	byUnit := map[string]gen.ViewUnitTotal{}
	for _, tot := range totals {
		assert.Equal(t, "amount", tot.Property)
		assert.Equal(t, gen.ViewPartAggregateSum, tot.Op)
		assert.NotEqual(t, "350.50", tot.Value, "a combined cross-unit figure must be inexpressible")
		byUnit[unitOf(t, tot)] = tot
	}
	require.Contains(t, byUnit, "SGD")
	require.Contains(t, byUnit, "EUR")
	assert.Equal(t, "150.00", byUnit["SGD"].Value, "100.50 + 49.50, exactly, in decimal text")
	assert.Equal(t, 2, byUnit["SGD"].Count)
	assert.Equal(t, "200.00", byUnit["EUR"].Value)
	assert.Equal(t, 1, byUnit["EUR"].Count)

	// G3: the unit-less row is counted excluded, with the reason spelled out.
	require.NotNil(t, figures.ExcludedCount, "the unit-less row must be counted, not silently dropped (G3)")
	assert.Equal(t, 1, *figures.ExcludedCount)
	require.NotNil(t, figures.ExcludedReason)
	assert.Contains(t, *figures.ExcludedReason, "currency", "the reason names the unit property")

	// The grouped table: two client groups in ascending order, each with
	// per-unit subtotals; Bolt's group carries the G3 exclusion.
	require.NotNil(t, table.Groups)
	groups := *table.Groups
	require.Len(t, groups, 2)
	assert.Equal(t, "Acme", groups[0].Key)
	assert.Equal(t, "Bolt", groups[1].Key)

	acmeUnits := map[string]string{}
	for _, st := range groups[0].Subtotals {
		acmeUnits[unitOf(t, st)] = st.Value
	}
	assert.Equal(t, map[string]string{"SGD": "100.50", "EUR": "200.00"}, acmeUnits)
	assert.Nil(t, groups[0].ExcludedCount, "Acme has no unit-less row, so nothing is excluded there")

	boltUnits := map[string]string{}
	for _, st := range groups[1].Subtotals {
		boltUnits[unitOf(t, st)] = st.Value
	}
	assert.Equal(t, map[string]string{"SGD": "49.50"}, boltUnits,
		"Bolt's unit-less 10.00 must be in NO subtotal (G3)")
	require.NotNil(t, groups[1].ExcludedCount)
	assert.Equal(t, 1, *groups[1].ExcludedCount)

	// The table footer repeats the whole-set rule: per-unit, never combined.
	require.NotNil(t, table.Totals)
	require.Len(t, *table.Totals, 2)
	require.NotNil(t, table.ExcludedCount)
	assert.Equal(t, 1, *table.ExcludedCount)
}

// --- legacy no-parts view ---------------------------------------------------

func TestKnowledgeView_LegacyLayoutOnlyViewServesAsOneTablePart(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"all-invoices.yaml": "name: all-invoices\ntype: invoice\nlayout: table\n",
	})
	res, code := getViewResult(t, api, ws, colID, "all-invoices")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)

	require.Len(t, res.Parts, 1, "a view with no `parts` is one layout-derived part, not zero and not a refusal")
	part := res.Parts[0]
	assert.Equal(t, gen.ViewResultPartPartTable, part.Part)
	assert.Equal(t, gen.ViewPartPartTable, part.Source.Part)
	assert.Nil(t, part.Totals, "a bare table declares no aggregate, so no total is invented for it")
	require.NotNil(t, part.Columns, "a typed view with no property list draws the schema's declared columns")
	assert.Equal(t, []string{"client", "amount", "currency"}, *part.Columns)
	assert.Len(t, res.Rows, 4)
	assert.True(t, res.Complete, "a clean, unclamped evaluation is complete")
}

// --- refusals ---------------------------------------------------------------

func TestKnowledgeView_DisabledViewRefusesByName_FR105(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"broken.yaml": "name: broken\n" +
			"type: invoice\n" +
			"disabled: true\n" +
			"untranslated: [\"status.function >= 3\"]\n",
	})
	res, code := getViewResult(t, api, ws, colID, "broken")
	require.Equal(t, http.StatusOK, code, "a refusal is an ANSWER, not a transport error")

	require.NotNil(t, res.Refusal, "a disabled view must say WHY, never render an empty table")
	assert.Equal(t, string(records.ServeRefusalDisabled), res.Refusal.Code)
	assert.Contains(t, res.Refusal.Reason, "disabled")
	assert.Contains(t, res.Refusal.Reason, "status.function >= 3",
		"the refusal names the untranslated expression, exactly as knowledge_find's own refusal does")
	assert.NotEmpty(t, res.Refusal.Remedy)
	assert.Empty(t, res.Parts)
	assert.Empty(t, res.Rows)
	assert.False(t, res.Complete)
}

func TestKnowledgeView_UnknownViewAndOutOfScopeCollectionAnswerAlike(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"all-invoices.yaml": "name: all-invoices\ntype: invoice\nlayout: table\n",
	})

	unknown, code := getViewResult(t, api, ws, colID, "no-such-view")
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, unknown.Refusal)
	assert.Equal(t, string(records.ServeRefusalUnknownView), unknown.Refusal.Code)

	// FR-052/FR-053: a collection id from nowhere must be indistinguishable
	// from an unknown view — same code, same shape, no 403/404 confirming
	// what exists elsewhere.
	elsewhere, code := getViewResult(t, api, ws, "kb_0000000000000000", "no-such-view")
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, elsewhere.Refusal)
	assert.Equal(t, unknown.Refusal.Code, elsewhere.Refusal.Code)

	t.Run("missing parameters are the caller's 400", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/view?view=x")
		assert.Equal(t, http.StatusBadRequest, w.Code)
		w = knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/view?collection_id="+colID)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// --- hardening: auth ---------------------------------------------------------

// TestKnowledgeView_RefusesUnauthenticatedCallsLikeItsNeighbours drives the
// REAL registered middleware chain (a.withUploadAuth, the same wrapper every
// other /api/v1/library/{workspace_id}/... endpoint is registered under —
// rest.go's "/api/v1/library/" registration), not the bare HandleLibraryTree
// shortcut every other test in this file uses. That shortcut is exactly what
// would hide an auth regression on this one endpoint while its neighbours
// (search, graph, outline, the plain knowledge-info GET) stayed protected: no
// Authorization header, no OMNIPUS_BEARER_TOKEN, dev_mode_bypass off
// (buildLibraryTestAPI's default config) must refuse before the handler is
// ever reached, exactly as checkBearerAuth's "no users configured" branch
// documents.
func TestKnowledgeView_RefusesUnauthenticatedCallsLikeItsNeighbours(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"all-invoices.yaml": "name: all-invoices\ntype: invoice\nlayout: table\n",
	})

	guarded := api.withUploadAuth(api.HandleLibraryTree)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/library/"+ws+"/knowledge/view?collection_id="+colID+"&view=all-invoices", nil)
	guarded(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"an unauthenticated caller must be refused before the view is ever evaluated")
}

// --- hardening: loader rejection, not a 500 ---------------------------------

// TestKnowledgeView_UnknownPropertyViewSurfacesLoaderRejectionNotServerError
// covers a view file that PARSED but names a property `invoice` does not
// declare. records.LoadViews refuses it at load time with
// RejectViewUnknownProperty; env.Views.Get therefore misses, and the
// handler's documented fallback (buildViewResult, rest_knowledge_view.go) is
// to search env.ViewReport.Rejections and answer BY THAT REJECTION rather
// than either a 500 or the generic "unknown view" refusal — a statement this
// endpoint could disprove (the view file is right there) is a statement it
// must not make.
func TestKnowledgeView_UnknownPropertyViewSurfacesLoaderRejectionNotServerError(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"bad-columns.yaml": "name: bad-columns\n" +
			"type: invoice\n" +
			"properties: [not_a_real_property]\n",
	})

	res, code := getViewResult(t, api, ws, colID, "bad-columns")
	require.Equal(t, http.StatusOK, code, "a loader rejection is an ANSWER, not a transport error")

	require.NotNil(t, res.Refusal, "a view naming an unknown property must refuse by name, never render an empty or wrong table")
	assert.Equal(t, string(records.RejectViewUnknownProperty), res.Refusal.Code)
	assert.Contains(t, res.Refusal.Reason, "not_a_real_property",
		"the refusal must name the offending property, exactly as the loader's own rejection does")
	assert.Empty(t, res.Parts)
	assert.Empty(t, res.Rows)
	assert.False(t, res.Complete)
}

// --- hardening: a base with zero servable views -----------------------------

// TestKnowledgeView_BaseWithZeroServableViewsRefusesCleanlyNotServerError
// covers the collection that has NO saved-view files at all: env.Views is
// empty and env.ViewReport.Rejections is empty too, so buildViewResult's
// rejection-search loop runs over a genuinely empty slice. Requesting any
// view name here exercises that empty-set path end to end (never a panic,
// never a 500) and must land on the same generic unknown-view refusal a
// populated-but-unmatching base would give.
func TestKnowledgeView_BaseWithZeroServableViewsRefusesCleanlyNotServerError(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{})

	res, code := getViewResult(t, api, ws, colID, "anything")
	require.Equal(t, http.StatusOK, code, "a base with no saved views must still answer 200, never a 500")

	require.NotNil(t, res.Refusal)
	assert.Equal(t, string(records.ServeRefusalUnknownView), res.Refusal.Code)
	assert.Empty(t, res.Parts)
	assert.Empty(t, res.Rows)
}

// --- hardening: grouping must not under-report --------------------------

// TestKnowledgeView_GroupingSurfacesTheNoValueGroupNeverDropsIt groups by
// `currency`, which is UNSET on one of the four fixture rows (INV-4/d.md, the
// same G3 row the summary test uses). The design bans exactly one failure
// here: a reader asking "unpaid by currency" who never learns that a row with
// no currency exists at all, because the group holding it silently vanished
// instead of rendering as its own "(no value)" bucket. The engine already
// carries this as one group with `absent: true` and an empty `key`
// (ViewResultGroup's own documented contract) — this test pins that the
// gateway's table-part builder passes that group through unchanged rather
// than filtering it, which nothing in buildTablePartData does today but
// nothing stops a future edit from doing either.
func TestKnowledgeView_GroupingSurfacesTheNoValueGroupNeverDropsIt(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"by-currency.yaml": "name: by-currency\n" +
			"type: invoice\n" +
			"kind: summary\n" +
			"parts:\n" +
			"  - part: table\n" +
			"    grouping: [{property: currency, direction: asc}]\n",
	})

	res, code := getViewResult(t, api, ws, colID, "by-currency")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Groups)
	groups := *res.Parts[0].Groups

	require.Len(t, groups, 3, "SGD, EUR, and the no-value group — three groups, not two")

	var absent *gen.ViewResultGroup
	byKey := map[string]gen.ViewResultGroup{}
	for i := range groups {
		g := groups[i]
		if g.Absent != nil && *g.Absent {
			require.Nil(t, absent, "at most one absent group can exist per grouping")
			gCopy := g
			absent = &gCopy
			continue
		}
		byKey[g.Key] = g
	}

	require.NotNil(t, absent, "the row with no currency must produce its OWN group, not vanish")
	assert.Equal(t, "", absent.Key, "an absent group's key is empty text, distinguished by the absent flag, not by a synthesized label")
	assert.Equal(t, 1, absent.Count)
	require.Len(t, absent.Paths, 1)
	assert.Equal(t, "d.md", absent.Paths[0], "the unit-less invoice (INV-4) is the row with no currency")

	require.Contains(t, byKey, "SGD")
	require.Contains(t, byKey, "EUR")
	assert.ElementsMatch(t, []string{"a.md", "c.md"}, byKey["SGD"].Paths)
	assert.ElementsMatch(t, []string{"b.md"}, byKey["EUR"].Paths)
}

// --- hardening: crosstab cell math, verified by hand ------------------------

// TestKnowledgeView_CrosstabCellMathHandVerified crosstabs client × currency
// over the fixture's four invoices:
//
//	INV-1 Acme SGD 100.50    INV-2 Acme EUR 200.00
//	INV-3 Bolt SGD  49.50    INV-4 Bolt (no currency) 10.00
//
// so every cell traces to exactly one invoice and the numbers are checked by
// hand, not against the handler's own output: Acme×SGD=100.50 (INV-1 alone),
// Acme×EUR=200.00 (INV-2 alone), Bolt×SGD=49.50 (INV-3 alone). Bolt's
// unit-less INV-4 lands in G3 — excluded from every cell, never averaged into
// one — so there is NO Bolt×(absent) cell, even though the crosstab still
// reports "" as a column key for it (an aggregated-but-empty column, not a
// dropped row: INV-4 is still counted in the crosstab's own excluded_count).
func TestKnowledgeView_CrosstabCellMathHandVerified(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"breakdown--client-currency.yaml": "name: breakdown--client-currency\n" +
			"type: invoice\n" +
			"kind: breakdown\n" +
			"parts:\n" +
			"  - part: crosstab\n" +
			"    number: amount\n" +
			"    aggregate: sum\n" +
			"    grouping: [{property: client, direction: asc}, {property: currency, direction: asc}]\n",
	})

	res, code := getViewResult(t, api, ws, colID, "breakdown--client-currency")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Crosstab)
	ct := *res.Parts[0].Crosstab

	assert.Equal(t, "client", ct.RowProperty)
	assert.Equal(t, "currency", ct.ColumnProperty)
	assert.ElementsMatch(t, []string{"Acme", "Bolt"}, ct.RowKeys)
	assert.ElementsMatch(t, []string{"SGD", "EUR", ""}, ct.ColumnKeys,
		"the absent-currency bucket still names a column, even though it draws no cell")

	type cell struct {
		value string
		count int
		unit  string
	}
	byRowCol := map[string]cell{}
	for _, c := range ct.Cells {
		u := ""
		if c.Unit != nil {
			u = *c.Unit
		}
		byRowCol[c.Row+"|"+c.Column] = cell{value: c.Value, count: c.Count, unit: u}
	}

	require.Len(t, ct.Cells, 3, "exactly three cells: Acme×SGD, Acme×EUR, Bolt×SGD — never a fourth for Bolt's absent currency")
	assert.Equal(t, cell{"100.50", 1, "SGD"}, byRowCol["Acme|SGD"], "INV-1 alone, by hand: 100.50")
	assert.Equal(t, cell{"200.00", 1, "EUR"}, byRowCol["Acme|EUR"], "INV-2 alone, by hand: 200.00")
	assert.Equal(t, cell{"49.50", 1, "SGD"}, byRowCol["Bolt|SGD"], "INV-3 alone, by hand: 49.50")
	_, hasBoltAbsent := byRowCol["Bolt|"]
	assert.False(t, hasBoltAbsent, "INV-4's missing currency must produce NO cell, never a zero or a guess")

	require.NotNil(t, ct.ExcludedCount, "INV-4 must be counted excluded at the crosstab level too")
	assert.Equal(t, 1, *ct.ExcludedCount)
	require.NotNil(t, ct.ExcludedReason)
	assert.Contains(t, *ct.ExcludedReason, "currency")
}

// --- hardening: legacy layout:map never synthesizes a table -----------------

// TestKnowledgeView_LegacyMapLayoutRefusesRatherThanSynthesizingTable covers
// design §2.2's deliberate exclusion: `map` is a legacy layout the CONTRACT
// still accepts (ViewDefLayoutMap is a valid enum member, so the view loads
// cleanly with no rejection) but has no drawable part
// (viewPartForLayout returns ok=false for it, records/view.go). EffectiveParts
// therefore reports drawable=false, and the handler's own rule
// ("no_drawable_parts", rest_knowledge_view.go) must refuse rather than
// falling back to a table nobody asked for — the exact silent-flattening
// failure the design's part-vocabulary comments are written against.
func TestKnowledgeView_LegacyMapLayoutRefusesRatherThanSynthesizingTable(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"old-map.yaml": "name: old-map\ntype: invoice\nlayout: map\n",
	})

	res, code := getViewResult(t, api, ws, colID, "old-map")
	require.Equal(t, http.StatusOK, code, "a no-drawable-parts refusal is an ANSWER, not a transport error")

	require.NotNil(t, res.Refusal, "a legacy map view must refuse, never silently render as a table")
	assert.Equal(t, "no_drawable_parts", res.Refusal.Code)
	assert.Contains(t, res.Refusal.Reason, "map")
	assert.Empty(t, res.Parts, "no table part may be synthesized for a layout that declares none")
	assert.Empty(t, res.Rows)
}

// guard against the fixture writing files nothing reads back
func TestKnowledgeView_FixtureVaultIsReallyIndexed(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"all-invoices.yaml": "name: all-invoices\ntype: invoice\nlayout: table\n",
	})
	res, code := getViewResult(t, api, ws, colID, "all-invoices")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	require.NotEmpty(t, res.Rows, "an empty fixture would let every G2/G3 assertion pass vacuously")
	paths := map[string]bool{}
	for _, row := range res.Rows {
		paths[row.Path] = true
	}
	for _, p := range []string{"a.md", "b.md", "c.md", "d.md"} {
		assert.True(t, paths[p], "expected row %s", p)
	}
}
