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
