// Tests for three arithmetic and binding defects in the view-result endpoint
// that the review confirmed but cut from the ranked list.
//
//	6b  ROUNDING PARITY. knowledge_find rounds an average HALF TO EVEN and says
//	    so in the total's own label ("round-half-even", FR-152). The renderer's
//	    own renderViewAverage rounded HALF UP. The same column, the same
//	    records, two answers — and the one place a reader could notice is the
//	    place they would least expect to have to check.
//
//	6d  MANY-VALUED UNITS. A row whose unit cell holds two values was excluded
//	    from every total (correct: it has not confirmed which unit its number
//	    is in) but the exclusion was reported with the same footer line as a
//	    MISSING unit. "no confirmed currency value" is true of both and useful
//	    for neither: the fixes are opposite (fill one in / pick one of two).
//
//	6e  CROSSTAB GROUPING FALLBACK. EffectiveParts deliberately does NOT copy
//	    the view's own `grouping` into a part that declares none — the renderer
//	    owns that fallback — and buildCrosstabPartData read src.Grouping
//	    directly, so a crosstab inheriting a two-key grouping from its view
//	    refused as "needs two grouping keys" while the keys sat one level up.
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
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// ---------------------------------------------------------------------------
// 6b — one rounding rule, proven on both paths against ONE fixture
// ---------------------------------------------------------------------------

// viewRoundingSchema declares a plain decimal with NO companion unit, so the
// only thing under test is the arithmetic.
const viewRoundingSchema = "schema_version: 1\n" +
	"type: reading\n" +
	"properties:\n" +
	"  weight: { type: decimal }\n"

// buildRoundingVault seeds EIGHT readings summing to exactly 0.01 at scale 2.
//
// The mean is 0.01/8 = 0.00125 exactly. FR-152 renders a decimal average at
// the column's own scale plus two — here 4 — so the fifth digit is a 5 with
// nothing after it: a REAL tie, not an artefact of a float. Half-up answers
// 0.0013; half-to-even answers 0.0012. The two rules cannot both be right and
// the fixture makes them disagree by construction.
func buildRoundingVault(t *testing.T, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Readings vault")
	writeNote(t, vault, ".omnipus-vault/records/reading.yaml", viewRoundingSchema)

	amounts := []string{"0.01", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00", "0.00"}
	for i, w := range amounts {
		writeNote(t, vault, string(rune('a'+i))+".md",
			"---\ntype: reading\nid: R-"+string(rune('1'+i))+"\nweight: "+w+"\n---\n# R\n")
	}
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

// findAggregateValue runs the ENGINE's own summary over the same collection,
// through the same environment the endpoint evaluates in, so the two answers
// being compared are answers to one question about one set of records.
func findAggregateValue(t *testing.T, api *restAPI, ws, colID, recordType, op, property string) string {
	t.Helper()
	col, inScope := api.resolveScopedCollection(ws, colID)
	require.True(t, inScope)

	env, closeEnv, err := vaultprops.OpenFindEnv(context.Background(), api.homePath, col)
	defer closeEnv()
	require.NoError(t, err)

	prop := property
	aggs := []gen.VaultFindAggregate{{Op: gen.VaultFindAggregateOp(op), Property: &prop}}
	rt := recordType
	resp, err := knowledgefind.Find(context.Background(), env.Deps,
		gen.VaultFindRequest{Type: &rt, Aggregate: &aggs})
	require.NoError(t, err)
	require.False(t, resp.Refused, "the engine must be able to answer %s(%s): %+v", op, property, resp.Problems)
	require.Len(t, resp.Totals, 1)
	require.False(t, resp.Totals[0].Refused != nil && *resp.Totals[0].Refused,
		"the engine refused its own summary: %s", resp.Totals[0].Value)
	return resp.Totals[0].Value
}

// TestKnowledgeViewArithmetic_AverageRoundsTheSameWayAsKnowledgeFind is 6b.
// The expected value is derived from the SPEC, not from either implementation:
// FR-152 names round-half-even, and knowledgefind's own total says so in its
// label. 0.00125 at scale 4 is therefore 0.0012.
func TestKnowledgeViewArithmetic_AverageRoundsTheSameWayAsKnowledgeFind(t *testing.T) {
	api, ws, colID := buildRoundingVault(t, map[string]string{
		"mean.yaml": "name: mean\n" +
			"type: reading\n" +
			"kind: summary\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: weight\n" +
			"    aggregate: avg\n",
	})

	res, code := getViewResult(t, api, ws, colID, "mean")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	totals := *res.Parts[0].Totals
	require.Len(t, totals, 1)

	assert.Equal(t, "0.0012", totals[0].Value,
		"0.01/8 = 0.00125 exactly; FR-152 rounds half TO EVEN at scale 4, so the fifth digit's tie resolves DOWN")

	// And the two paths agree on the same fixture, which is the property that
	// actually protects a reader: one number, whichever surface they read.
	engine := findAggregateValue(t, api, ws, colID, "reading", "avg", "weight")
	assert.Equal(t, engine, totals[0].Value,
		"knowledge_find and the view renderer must answer one average one way")
}

// ---------------------------------------------------------------------------
// 6d — a unit that is AMBIGUOUS is not a unit that is MISSING
// ---------------------------------------------------------------------------

// viewMultiUnitSchema declares `currency` as MANY, so one invoice can
// legitimately carry two currency values — and then no total can say which one
// its amount is denominated in.
const viewMultiUnitSchema = "schema_version: 1\n" +
	"type: invoice\n" +
	"properties:\n" +
	"  client:   { type: text }\n" +
	"  amount:   { type: decimal, unit_property: currency }\n" +
	"  currency: { type: enum, many: true, values: [SGD, EUR] }\n"

func buildMultiUnitVault(t *testing.T, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Finance vault")
	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", viewMultiUnitSchema)

	writeNote(t, vault, "a.md", "---\ntype: invoice\nid: INV-1\nclient: Acme\namount: 100.50\ncurrency: [SGD]\n---\n# INV-1\n")
	// AMBIGUOUS: two currencies on one invoice.
	writeNote(t, vault, "b.md", "---\ntype: invoice\nid: INV-2\nclient: Acme\namount: 200.00\ncurrency: [SGD, EUR]\n---\n# INV-2\n")
	// MISSING: no currency at all.
	writeNote(t, vault, "c.md", "---\ntype: invoice\nid: INV-3\nclient: Bolt\namount: 10.00\n---\n# INV-3\n")

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

// TestKnowledgeViewArithmetic_AmbiguousUnitIsExcludedAndSaidSoDistinctly is
// 6d. Both rows are rightly excluded — neither has ONE confirmed unit — but
// they are excluded for opposite reasons with opposite fixes, and a footer
// that says "no confirmed currency value" about both tells the operator to
// fill in a value that is already there twice.
func TestKnowledgeViewArithmetic_AmbiguousUnitIsExcludedAndSaidSoDistinctly(t *testing.T) {
	api, ws, colID := buildMultiUnitVault(t, map[string]string{
		"amounts.yaml": "name: amounts\n" +
			"type: invoice\n" +
			"kind: summary\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: amount\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "amounts")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Rows, 3, "every row is shown, including both excluded ones")

	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	totals := *res.Parts[0].Totals
	require.Len(t, totals, 1, "only INV-1 has a single confirmed unit")
	assert.Equal(t, "100.50", totals[0].Value)
	assert.Equal(t, "SGD", *totals[0].Unit)

	require.NotNil(t, res.Parts[0].ExcludedCount)
	assert.Equal(t, 2, *res.Parts[0].ExcludedCount)
	require.NotNil(t, res.Parts[0].ExcludedPaths)
	assert.Equal(t, []string{"b.md", "c.md"}, *res.Parts[0].ExcludedPaths)

	require.NotNil(t, res.Parts[0].ExcludedReason)
	reason := *res.Parts[0].ExcludedReason
	assert.Contains(t, reason, "currency", "the reason names the unit property")
	assert.Contains(t, strings.ToLower(reason), "more than one",
		"an AMBIGUOUS unit must be reported as ambiguous, never folded into 'missing' — the two have opposite fixes")
	assert.Contains(t, reason, "1 row", "each cause is counted on its own")
}

// ---------------------------------------------------------------------------
// 6e — a crosstab inherits the view's grouping, like every other part
// ---------------------------------------------------------------------------

// TestKnowledgeViewArithmetic_CrosstabInheritsTheViewGrouping is 6e. The view
// declares the two grouping keys; the part declares none. EffectiveParts does
// NOT copy them down (its comment says the renderer owns the fallback), so the
// crosstab builder has to apply it — or a legal view refuses with "needs two
// grouping keys" while the keys sit one level up in the same file.
func TestKnowledgeViewArithmetic_CrosstabInheritsTheViewGrouping(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"grid.yaml": "name: grid\n" +
			"type: invoice\n" +
			"kind: breakdown\n" +
			"grouping: [{property: client, direction: asc}, {property: currency, direction: asc}]\n" +
			"parts:\n" +
			"  - part: crosstab\n" +
			"    number: amount\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "grid")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)

	for _, p := range res.Problems {
		assert.NotContains(t, p.Reason, "two grouping keys",
			"the view supplies both keys; a part that declares none inherits them")
	}

	require.NotNil(t, res.Parts[0].Crosstab, "the crosstab must be drawn, not refused")
	ct := *res.Parts[0].Crosstab
	assert.Equal(t, "client", ct.RowProperty)
	assert.Equal(t, "currency", ct.ColumnProperty)

	byRowCol := map[string]string{}
	for _, c := range ct.Cells {
		byRowCol[c.Row+"|"+c.Column] = c.Value
	}
	// Hand-checked from buildViewTestVault's four invoices.
	assert.Equal(t, "100.50", byRowCol["Acme|SGD"])
	assert.Equal(t, "200.00", byRowCol["Acme|EUR"])
	assert.Equal(t, "49.50", byRowCol["Bolt|SGD"])
	assert.Len(t, ct.Cells, 3, "INV-4 has no currency, so it draws no cell")
}

// TestKnowledgeViewArithmetic_CrosstabWithNoGroupingAnywhereStillRefuses is
// the over-inheritance control: with neither the part nor the view supplying
// two keys, there is genuinely nothing to draw and the refusal must stand.
func TestKnowledgeViewArithmetic_CrosstabWithNoGroupingAnywhereStillRefuses(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"grid.yaml": "name: grid\n" +
			"type: invoice\n" +
			"kind: breakdown\n" +
			"grouping: [{property: client, direction: asc}]\n" +
			"parts:\n" +
			"  - part: crosstab\n" +
			"    number: amount\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "grid")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	assert.Nil(t, res.Parts[0].Crosstab, "one key is not a grid")

	found := false
	for _, p := range res.Problems {
		if strings.Contains(p.Reason, "two grouping keys") {
			found = true
		}
	}
	assert.True(t, found, "a crosstab with one key must say why it drew nothing")
}
