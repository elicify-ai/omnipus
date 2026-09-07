// Tests for the UNIT AUTHORITY rule on GET /knowledge/view
// (view-kinds-design-2026-09-03 §5: a unit is DECLARED, never inferred).
//
// The review finding this file reproduces is an authority SPLIT. The endpoint
// resolved a number's companion unit from the LIVE SCHEMA alone
// (unitPropertyOf), while the SPA read the unit the part itself was stamped
// with when it was composed. Two readers, two sources, no reconciliation:
//
//	(a) delete `unit_property` from the record type and keep the `currency`
//	    property, and the endpoint silently emits ONE combined SGD+EUR figure
//	    while the part on disk still says `unit: currency` — the exact G2
//	    failure ("no combined figure is ever emitted"), invisible because the
//	    part still looks unit-aware to anyone reading the view file.
//
//	(b) hand-write a part with no `unit:` stamp over a number the schema DOES
//	    pair with a unit, and the endpoint excludes rows under G3 that the SPA
//	    has no way to identify — it can count them but not mark them, because
//	    neither the resolved unit property nor the excluded rows reached the
//	    wire.
//
// The rule these tests pin: THE SCHEMA IS THE AUTHORITY (declared, never
// inferred), and a DISAGREEMENT NEVER PASSES SILENTLY. The answer is also made
// self-sufficient — the resolved unit property and the excluded row paths are
// on the wire — so the SPA never re-derives either.
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

// viewUnitlessInvoiceSchema is viewTestInvoiceSchema with `unit_property`
// REMOVED and `currency` kept — the operator edit that opens case (a). The
// view files on disk still carry `unit: currency`, because nothing rewrites a
// view when a schema changes.
const viewUnitlessInvoiceSchema = "schema_version: 1\n" +
	"type: invoice\n" +
	"properties:\n" +
	"  client:   { type: text }\n" +
	"  amount:   { type: decimal }\n" +
	"  currency: { type: enum, values: [SGD, EUR] }\n"

// buildUnitAuthorityVault seeds the same four invoices buildViewTestVault
// uses (100.50 SGD, 200.00 EUR, 49.50 SGD, 10.00 with no currency) under a
// CALLER-CHOSEN schema, so one fixture covers both the declared-unit and the
// no-longer-declared-unit worlds with identical data.
func buildUnitAuthorityVault(t *testing.T, schema string, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Finance vault")

	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", schema)
	writeNote(t, vault, "a.md", viewTestInvoice("INV-1", "Acme", "100.50", "SGD"))
	writeNote(t, vault, "b.md", viewTestInvoice("INV-2", "Acme", "200.00", "EUR"))
	writeNote(t, vault, "c.md", viewTestInvoice("INV-3", "Bolt", "49.50", "SGD"))
	writeNote(t, vault, "d.md", viewTestInvoice("INV-4", "Bolt", "10.00", ""))
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

// requireUnitDisagreementProblem asserts one aggregate_refused problem names
// BOTH sides of the disagreement — what the part was stamped with and what the
// schema resolves — because naming only one leaves the operator unable to tell
// which of the two files to edit.
func requireUnitDisagreementProblem(t *testing.T, res *gen.ViewResult, property, stamped, schemaSide string) {
	t.Helper()
	matches := 0
	for _, p := range res.Problems {
		if p.Code != gen.AggregateRefused || !strings.Contains(p.Reason, `"`+property+`"`) {
			continue
		}
		matches++
		assert.Contains(t, p.Reason, stamped, "the refusal must name the unit the PART stamps")
		assert.Contains(t, p.Reason, schemaSide, "the refusal must say what the SCHEMA resolves")
		require.NotNil(t, p.Fix, "a disagreement refusal must say which file to change")
	}
	assert.Equal(t, 1, matches,
		"want exactly ONE aggregate_refused problem naming %q; got %d in %+v", property, matches, res.Problems)
}

const viewStampedUnitFigures = "name: stamped\n" +
	"type: invoice\n" +
	"kind: summary\n" +
	"parts:\n" +
	"  - part: figures\n" +
	"    number: amount\n" +
	"    unit: currency\n" +
	"    aggregate: sum\n"

// TestKnowledgeViewUnit_StampedUnitWithNoSchemaDeclarationIsRefused is case
// (a): the part says `unit: currency`, the schema no longer pairs the two, and
// the pre-fix endpoint answered ONE combined figure of 360.00 (SGD + EUR + the
// unit-less row) with no refusal at all.
func TestKnowledgeViewUnit_StampedUnitWithNoSchemaDeclarationIsRefused(t *testing.T) {
	api, ws, colID := buildUnitAuthorityVault(t, viewUnitlessInvoiceSchema, map[string]string{
		"stamped.yaml": viewStampedUnitFigures,
	})

	res, code := getViewResult(t, api, ws, colID, "stamped")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal, "the view still serves — only the total is refused")
	require.Len(t, res.Rows, 4, "every row stays shown")

	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals, "the part answers an explicit empty totals list, not an absent one")
	for _, tot := range *res.Parts[0].Totals {
		assert.NotEqual(t, "360.00", tot.Value,
			"a combined cross-unit figure must not be emitted when the part and the schema disagree (G2)")
		assert.NotEqual(t, "amount", tot.Property,
			"no total of amount may survive a unit-authority disagreement")
	}
	requireUnitDisagreementProblem(t, res, "amount", "currency", "no companion unit")
}

// TestKnowledgeViewUnit_StampedUnitNamingADifferentPropertyIsRefused is the
// other half of the disagreement: both sides resolve a unit, and they are not
// the same property. Picking either one would total under a rule one of the
// two files does not describe.
func TestKnowledgeViewUnit_StampedUnitNamingADifferentPropertyIsRefused(t *testing.T) {
	api, ws, colID := buildUnitAuthorityVault(t, viewTestInvoiceSchema, map[string]string{
		"mismatched.yaml": "name: mismatched\n" +
			"type: invoice\n" +
			"kind: summary\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: amount\n" +
			"    unit: client\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "mismatched")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)

	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	assert.Empty(t, *res.Parts[0].Totals, "a disagreeing part totals nothing")
	requireUnitDisagreementProblem(t, res, "amount", "client", "currency")
}

// TestKnowledgeViewUnit_AgreeingStampIsServedNormally is the over-refusal
// control: the design's own §4 example stamps `unit: currency` on a schema
// that declares exactly that, and it must total per unit as before.
func TestKnowledgeViewUnit_AgreeingStampIsServedNormally(t *testing.T) {
	api, ws, colID := buildUnitAuthorityVault(t, viewTestInvoiceSchema, map[string]string{
		"stamped.yaml": viewStampedUnitFigures,
	})

	res, code := getViewResult(t, api, ws, colID, "stamped")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	assert.Empty(t, res.Problems, "agreement refuses nothing")

	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	byUnit := map[string]string{}
	for _, tot := range *res.Parts[0].Totals {
		byUnit[unitOf(t, tot)] = tot.Value
	}
	assert.Equal(t, map[string]string{"SGD": "150.00", "EUR": "200.00"}, byUnit)
}

// TestKnowledgeViewUnit_ResolvedUnitAndExcludedRowsReachTheWire is case (b).
// The part carries NO `unit:` stamp, so the SPA has nothing of its own to read
// — the answer must therefore state the unit property the SCHEMA resolved and
// name the rows G3 excluded, or the renderer is left counting exclusions it
// cannot point at.
func TestKnowledgeViewUnit_ResolvedUnitAndExcludedRowsReachTheWire(t *testing.T) {
	api, ws, colID := buildUnitAuthorityVault(t, viewTestInvoiceSchema, map[string]string{
		"unstamped.yaml": "name: unstamped\n" +
			"type: invoice\n" +
			"kind: summary\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: amount\n" +
			"    aggregate: sum\n" +
			"  - part: table\n" +
			"    grouping: [{property: client, direction: asc}]\n" +
			"    subtotals: {amount: sum}\n",
	})

	res, code := getViewResult(t, api, ws, colID, "unstamped")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	assert.Empty(t, res.Problems, "an unstamped part resolves its unit from the schema and refuses nothing")

	require.Len(t, res.Parts, 2)
	figures, table := res.Parts[0], res.Parts[1]

	// The AUTHORITY, on the wire: the SPA reads which property carries the
	// unit rather than re-deriving it from a schema it does not have.
	require.NotNil(t, figures.UnitProperty,
		"the resolved companion unit must reach the wire; the SPA cannot read the schema")
	assert.Equal(t, "currency", *figures.UnitProperty)

	// Every total says which property its `unit` value came from, so the pair
	// can never be separated on the way to a reader.
	require.NotNil(t, figures.Totals)
	require.Len(t, *figures.Totals, 2)
	for _, tot := range *figures.Totals {
		require.NotNil(t, tot.UnitProperty)
		assert.Equal(t, "currency", *tot.UnitProperty)
	}

	// The G3 exclusions, MARKABLE: the count was already there, the identity
	// was not.
	require.NotNil(t, figures.ExcludedCount)
	assert.Equal(t, 1, *figures.ExcludedCount)
	require.NotNil(t, figures.ExcludedPaths, "an excluded row must be identifiable, not merely counted")
	assert.Equal(t, []string{"d.md"}, *figures.ExcludedPaths,
		"INV-4 is the invoice with no currency")

	// The same holds inside a group, which is where a subtotal footer needs it
	// most: Bolt owns the unit-less row.
	require.NotNil(t, table.Groups)
	groups := *table.Groups
	require.Len(t, groups, 2)
	assert.Equal(t, "Acme", groups[0].Key)
	assert.Nil(t, groups[0].ExcludedPaths, "Acme excludes nothing, so it names nothing")
	assert.Equal(t, "Bolt", groups[1].Key)
	require.NotNil(t, groups[1].ExcludedPaths)
	assert.Equal(t, []string{"d.md"}, *groups[1].ExcludedPaths)
	require.NotNil(t, table.ExcludedPaths)
	assert.Equal(t, []string{"d.md"}, *table.ExcludedPaths)
}

// TestKnowledgeViewUnit_CrosstabExcludedRowsReachTheWire covers the third
// scope that already counted exclusions without naming them.
func TestKnowledgeViewUnit_CrosstabExcludedRowsReachTheWire(t *testing.T) {
	api, ws, colID := buildUnitAuthorityVault(t, viewTestInvoiceSchema, map[string]string{
		"grid.yaml": "name: grid\n" +
			"type: invoice\n" +
			"kind: breakdown\n" +
			"parts:\n" +
			"  - part: crosstab\n" +
			"    number: amount\n" +
			"    aggregate: sum\n" +
			"    grouping: [{property: client, direction: asc}, {property: currency, direction: asc}]\n",
	})

	res, code := getViewResult(t, api, ws, colID, "grid")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Crosstab)

	ct := *res.Parts[0].Crosstab
	require.NotNil(t, ct.ExcludedCount)
	assert.Equal(t, 1, *ct.ExcludedCount)
	require.NotNil(t, ct.ExcludedPaths)
	assert.Equal(t, []string{"d.md"}, *ct.ExcludedPaths)
	require.NotNil(t, res.Parts[0].UnitProperty)
	assert.Equal(t, "currency", *res.Parts[0].UnitProperty)
}
