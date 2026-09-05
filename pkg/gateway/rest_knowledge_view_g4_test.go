// Tests for gate G4 in the SERVING path — "Text is never totalled, even when
// it parses as a number" (view-kinds-design-2026-09-03 §3 G4).
//
// The design puts all six gates in the composer AND the renderer, "testable
// once, skippable by no agent". The composer half exists. The renderer half
// did not: aggregateViewRows read the engine's RENDERED CELL TEXT and totalled
// whatever parsed as a decimal, so a `figures` part bound to a TEXT property
// whose values happen to read "1200" and "3400" served a headline figure of
// 4,600 — a number nobody recorded, over a column the schema says is prose.
// The composer would refuse to write that view; `write_view` (the raw escape
// hatch), a hand-edited view file and an imported .base view all reach the
// renderer without passing the composer.
//
// Expected values here are derived from the DESIGN, not from the handler: G4
// says the total is refused and the refusal names the conversion path, and G2
// (rows are shown, only the arithmetic is withheld) says the rows survive.
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

// viewG4TicketSchema declares one property of every shape G4 has an opinion
// about: `code` is TEXT that parses as a number (the trap), `points` is an
// integer and `hours` a decimal (the two types that legitimately total),
// `opened` is a date and `done` a checkbox (types with their own summaries,
// none of which a view part's five reductions implement).
const viewG4TicketSchema = "schema_version: 1\n" +
	"type: ticket\n" +
	"properties:\n" +
	"  team:   { type: text }\n" +
	"  code:   { type: text }\n" +
	"  points: { type: integer }\n" +
	"  hours:  { type: decimal }\n" +
	"  opened: { type: date }\n" +
	"  done:   { type: checkbox }\n"

func viewG4Ticket(id, team, code, points, hours, opened, done string) string {
	return "---\n" +
		"type: ticket\n" +
		"id: " + id + "\n" +
		"team: " + team + "\n" +
		"code: \"" + code + "\"\n" +
		"points: " + points + "\n" +
		"hours: " + hours + "\n" +
		"opened: " + opened + "\n" +
		"done: " + done + "\n" +
		"---\n# " + id + "\n"
}

// buildG4TestVault seeds a vault whose TEXT column reads as a number on every
// row, so a renderer that totals rendered cell text produces a specific,
// checkable wrong answer (4600) rather than an empty list that could pass for
// a refusal.
func buildG4TestVault(t *testing.T, views map[string]string) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the view-result endpoint cannot evaluate here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Tickets vault")

	writeNote(t, vault, ".omnipus-vault/records/ticket.yaml", viewG4TicketSchema)
	writeNote(t, vault, "t1.md", viewG4Ticket("TKT-1", "Core", "1200", "3", "1.50", "2026-01-02", "true"))
	writeNote(t, vault, "t2.md", viewG4Ticket("TKT-2", "Core", "3400", "4", "2.25", "2026-02-03", "false"))
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

// requireG4Refusal asserts exactly one aggregate_refused problem names the
// property and states the gate, so an operator reading the response learns
// which column was refused and why rather than only that a figure is missing.
func requireG4Refusal(t *testing.T, res *gen.ViewResult, property, declaredType string) {
	t.Helper()
	matches := 0
	for _, p := range res.Problems {
		if p.Code != gen.AggregateRefused || !strings.Contains(p.Reason, `"`+property+`"`) {
			continue
		}
		matches++
		assert.Contains(t, p.Reason, declaredType,
			"the refusal must name the DECLARED type, so the reader learns why the column cannot total")
		assert.Contains(t, p.Reason, "G4",
			"the refusal must cite the gate it enforces")
		require.NotNil(t, p.Fix, "G4's refusal offers the property-conversion path")
		assert.Contains(t, *p.Fix, property)
	}
	assert.Equal(t, 1, matches,
		"want exactly ONE aggregate_refused problem naming %q; got %d in %+v", property, matches, res.Problems)
}

// requireNoTotalOfProperty is requireNoTotalOf's G4 twin: no figure over the
// property may exist anywhere in the answer, in any part, group or cell.
func requireNoTotalOfProperty(t *testing.T, res *gen.ViewResult, property string) {
	t.Helper()
	for pi, part := range res.Parts {
		if part.Totals != nil {
			for _, tot := range *part.Totals {
				assert.NotEqual(t, property, tot.Property,
					"parts[%d] totals %q as %s = %s; text is never totalled (G4)",
					pi, property, string(tot.Op), tot.Value)
			}
		}
		if part.Groups != nil {
			for gi, g := range *part.Groups {
				for _, tot := range g.Subtotals {
					assert.NotEqual(t, property, tot.Property,
						"parts[%d].groups[%d] subtotals %q = %s; text is never totalled (G4)",
						pi, gi, property, tot.Value)
				}
			}
		}
		if part.Series != nil && part.Source.Number != nil && *part.Source.Number == property {
			assert.Empty(t, *part.Series, "parts[%d] charts %q; text is never totalled (G4)", pi, property)
		}
		if part.Crosstab != nil && part.Source.Number != nil && *part.Source.Number == property {
			assert.Empty(t, part.Crosstab.Cells,
				"parts[%d] crosstabs %q; text is never totalled (G4)", pi, property)
		}
	}
}

// TestKnowledgeViewG4_FiguresOverTextPropertyIsRefused is the reproduction.
// `code` is declared TEXT and holds "1200" and "3400"; the pre-fix serving
// path answered sum = 4600.
func TestKnowledgeViewG4_FiguresOverTextPropertyIsRefused(t *testing.T) {
	api, ws, colID := buildG4TestVault(t, map[string]string{
		"text-figures.yaml": "name: text-figures\n" +
			"type: ticket\n" +
			"kind: summary\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: code\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "text-figures")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal, "the view still serves — only the arithmetic is withheld")
	require.Len(t, res.Rows, 2, "both tickets stay SHOWN; G4 withholds the total, never the rows")

	requireNoTotalOfProperty(t, res, "code")
	requireG4Refusal(t, res, "code", "text")

	// The part answers an EMPTY totals list — the SPA's explicit "no figures"
	// state — rather than an absent one that reads as "the server forgot".
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	assert.Empty(t, *res.Parts[0].Totals)
}

// TestKnowledgeViewG4_SubtotalsOverTextPropertyAreRefused covers the OTHER
// aggregation entry — a table part's `subtotals` map — which reaches
// aggregateViewRows by a different route and had the same hole.
func TestKnowledgeViewG4_SubtotalsOverTextPropertyAreRefused(t *testing.T) {
	api, ws, colID := buildG4TestVault(t, map[string]string{
		"text-subtotals.yaml": "name: text-subtotals\n" +
			"type: ticket\n" +
			"parts:\n" +
			"  - part: table\n" +
			"    grouping: [{property: team, direction: asc}]\n" +
			"    subtotals: {code: sum, points: sum}\n",
	})

	res, code := getViewResult(t, api, ws, colID, "text-subtotals")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)

	requireNoTotalOfProperty(t, res, "code")
	requireG4Refusal(t, res, "code", "text")

	// The CONTROL, in the same part: `points` is an integer and still totals,
	// so the gate is proven to refuse by declared type rather than by refusing
	// the whole part whenever any binding is bad.
	require.Len(t, res.Parts, 1)
	require.NotNil(t, res.Parts[0].Totals)
	totals := *res.Parts[0].Totals
	require.Len(t, totals, 1, "one surviving total: points")
	assert.Equal(t, "points", totals[0].Property)
	assert.Equal(t, "7", totals[0].Value, "3 + 4, by hand")
}

// TestKnowledgeViewG4_NonNumberTypesAreAllRefused walks the other declared
// types a `number:` binding can name. None of them is a number, so none of
// them totals — and each refusal names its own declared type rather than a
// generic "not supported".
func TestKnowledgeViewG4_NonNumberTypesAreAllRefused(t *testing.T) {
	for _, tc := range []struct {
		property     string
		declaredType string
	}{
		{"team", "text"},
		{"code", "text"},
		{"opened", "date"},
		{"done", "checkbox"},
	} {
		t.Run(tc.property, func(t *testing.T) {
			api, ws, colID := buildG4TestVault(t, map[string]string{
				"fig.yaml": "name: fig\n" +
					"type: ticket\n" +
					"parts:\n" +
					"  - part: figures\n" +
					"    number: " + tc.property + "\n" +
					"    aggregate: sum\n",
			})
			res, code := getViewResult(t, api, ws, colID, "fig")
			require.Equal(t, 200, code)
			require.Nil(t, res.Refusal)
			requireNoTotalOfProperty(t, res, tc.property)
			requireG4Refusal(t, res, tc.property, tc.declaredType)
		})
	}
}

// TestKnowledgeViewG4_NumberTypesStillTotal is the over-refusal control: the
// two types G4 permits must be unaffected, with values checked by hand.
func TestKnowledgeViewG4_NumberTypesStillTotal(t *testing.T) {
	for _, tc := range []struct {
		property string
		want     string
	}{
		{"points", "7"},
		{"hours", "3.75"},
	} {
		t.Run(tc.property, func(t *testing.T) {
			api, ws, colID := buildG4TestVault(t, map[string]string{
				"fig.yaml": "name: fig\n" +
					"type: ticket\n" +
					"parts:\n" +
					"  - part: figures\n" +
					"    number: " + tc.property + "\n" +
					"    aggregate: sum\n",
			})
			res, code := getViewResult(t, api, ws, colID, "fig")
			require.Equal(t, 200, code)
			require.Nil(t, res.Refusal)
			assert.Empty(t, res.Problems, "a number property refuses nothing")
			require.Len(t, res.Parts, 1)
			require.NotNil(t, res.Parts[0].Totals)
			totals := *res.Parts[0].Totals
			require.Len(t, totals, 1)
			assert.Equal(t, tc.property, totals[0].Property)
			assert.Equal(t, tc.want, totals[0].Value)
			assert.Equal(t, 2, totals[0].Count)
		})
	}
}

// TestKnowledgeViewG4_UntypedViewRefusesTextTotals mirrors the untyped-view
// rule the unit gate already implements: with no `type` no single schema
// speaks for the rows, so the gate asks every schema in scope — and one that
// declares the property as text is enough to refuse, because a total over it
// would be a total over prose in at least some of the rows.
func TestKnowledgeViewG4_UntypedViewRefusesTextTotals(t *testing.T) {
	api, ws, colID := buildG4TestVault(t, map[string]string{
		"untyped-text.yaml": "name: untyped-text\n" +
			"properties: [file.name, code]\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: code\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "untyped-text")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	requireNoTotalOfProperty(t, res, "code")
	requireG4Refusal(t, res, "code", "text")
}

// TestKnowledgeViewG4_UntypedViewRefusesUndeclaredTotals pins the undeclared
// case. In an untyped query every name is legal and resolves in the TEXT
// domain (knowledgefind's namespace rule), so a `number:` binding no schema
// in scope declares is text by resolution — and G4 refuses text.
func TestKnowledgeViewG4_UntypedViewRefusesUndeclaredTotals(t *testing.T) {
	api, ws, colID := buildG4TestVault(t, map[string]string{
		"untyped-unknown.yaml": "name: untyped-unknown\n" +
			"properties: [file.name, mystery]\n" +
			"parts:\n" +
			"  - part: figures\n" +
			"    number: mystery\n" +
			"    aggregate: sum\n",
	})

	res, code := getViewResult(t, api, ws, colID, "untyped-unknown")
	require.Equal(t, 200, code)
	require.Nil(t, res.Refusal)
	requireNoTotalOfProperty(t, res, "mystery")
	requireG4Refusal(t, res, "mystery", "text")
}
