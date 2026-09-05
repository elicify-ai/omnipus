// Omnipus — tests for knowledge_configure op=create_view, the composer path
// (view-kinds-design-2026-09-03 §6.1) and its six gates (§3, G1..G6).
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeConfigure_CreateView' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// Fixture: one "invoice" record type with a property for every gate this
// file exercises.
//
//	name      text    — an ordinary display property, never a valid `number`
//	cover     text    — an ordinary text property; NOT tiles-eligible (D5:
//	                    ImageEligible returns false for every declared type,
//	                    text included — see gateG1RequireImage's own doc
//	                    comment). Kept in the fixture so a G1-tiles test can
//	                    prove that even a plausible-looking binding still
//	                    refuses.
//	status    enum(3) — board-eligible (≤ 8 values); also a grouping property
//	category  enum(26)— NOT board-eligible; G1's own near-miss example
//	due_date  date    — calendar's and trend's date binding
//	amount    decimal, unit_property: currency — G2/G3's unit-bearing number
//	currency  enum(3) — amount's declared companion unit
//	count     integer — a number WITHOUT a declared unit (G2/G3's negative case)
//	owner     text    — breakdown's second grouping property
// ---------------------------------------------------------------------------

func cvManyValues(n int) []any {
	out := make([]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("v%d", i+1))
	}
	return out
}

// cvFixture builds a fresh knowledge base with the "invoice" record type
// above already declared, and returns the tool plus the fixture's identity.
func cvFixture(t *testing.T) (tool *ConfigureTool, ws, root string) {
	t.Helper()
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool = kcTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "invoice",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"name":     map[string]any{"type": "text"},
				"cover":    map[string]any{"type": "text"},
				"status":   map[string]any{"type": "enum", "values": []any{"draft", "sent", "paid"}},
				"category": map[string]any{"type": "enum", "values": cvManyValues(26)},
				"due_date": map[string]any{"type": "date"},
				"amount":   map[string]any{"type": "decimal", "unit_property": "currency"},
				"currency": map[string]any{"type": "enum", "values": []any{"SGD", "USD", "EUR"}},
				"count":    map[string]any{"type": "integer"},
				"owner":    map[string]any{"type": "text"},
			},
		},
	})
	require.False(t, res.IsError, "fixture record type must be created: %s", res.ForLLM)
	return tool, ws, root
}

// cvViewPath is the on-disk path create_view would write for a given name.
func cvViewPath(root, name string) string {
	return filepath.Join(root, ".omnipus-vault", "views", name+".yaml")
}

func cvAssertFileAbsent(t *testing.T, root, name string) {
	t.Helper()
	_, err := os.Stat(cvViewPath(root, name))
	require.True(t, os.IsNotExist(err), "view %q must not have been written", name)
}

// ---------------------------------------------------------------------------
// G1 — a kind is offered only when the collection has the properties it
// requires; a refusal names the missing property and any near-miss.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_G1_Board_RefuseAndPass(t *testing.T) {
	tool, ws, root := cvFixture(t)

	t.Run("refuse: enum exists but exceeds the 8-value bound, near-miss named", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g1-board-refuse",
			"kind": "board", "type": "invoice", "choice": "category",
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, "category")
		require.Contains(t, res.ForLLM, "26")
		cvAssertFileAbsent(t, root, "g1-board-refuse")
	})

	t.Run("pass: enum within the 8-value bound", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g1-board-pass",
			"kind": "board", "type": "invoice", "choice": "status",
		})
		require.False(t, res.IsError, res.ForLLM)
		raw, err := os.ReadFile(cvViewPath(root, "g1-board-pass"))
		require.NoError(t, err)
		require.Contains(t, string(raw), "choice: status")
		require.Contains(t, string(raw), "part: columns")
	})
}

// TestKnowledgeConfigure_CreateView_G1_Tiles_AlwaysRefused is D5 (design §9,
// ratified 2026-09-03): no image-capable property type exists yet, so
// kind=tiles refuses UNCONDITIONALLY — even when 'image' names a real,
// declared text property that looks like a plausible binding (option (a) in
// D5, explicitly rejected: binding tiles to `text` would make it available
// on every vault and attach rendering behaviour to unvalidated strings).
//
// This is the composer half of the D5 agreement; see
// TestKnowledgeConfigure_CreateView_TilesAgreesWithDescribeAvailability below
// for the same fact checked against knowledge_describe's discovery block.
func TestKnowledgeConfigure_CreateView_G1_Tiles_AlwaysRefused(t *testing.T) {
	tool, ws, root := cvFixture(t)

	t.Run("refuse even with a plausible text property named", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g1-tiles-refuse-named",
			"kind": "tiles", "type": "invoice", "image": "cover",
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, imageIneligibleReason)
		cvAssertFileAbsent(t, root, "g1-tiles-refuse-named")
	})

	t.Run("refuse with no image binding given at all", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g1-tiles-refuse-blank",
			"kind": "tiles", "type": "invoice",
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, imageIneligibleReason)
		cvAssertFileAbsent(t, root, "g1-tiles-refuse-blank")
	})
}

// TestKnowledgeConfigure_CreateView_TilesAgreesWithDescribeAvailability is
// the direct regression for the D5 discover/compose disagreement: before the
// fix, RenderAvailableViews (knowledge_describe's discovery block) called
// tiles unavailable while execCreateView happily wrote one bound to a text
// property — the exact "a kind knowledge_describe calls available must
// never disagree with the composer" failure view_kinds.go's own header
// warns against.
func TestKnowledgeConfigure_CreateView_TilesAgreesWithDescribeAvailability(t *testing.T) {
	tool, ws, root := cvFixture(t)

	schemas, _, lerr := records.LoadSchemas(root)
	require.NoError(t, lerr)
	sc, ok := schemas.Get("invoice")
	require.True(t, ok)

	avail := RenderAvailableViews(sc)
	require.Contains(t, avail, "tiles — NO ("+imageIneligibleReason+")",
		"discovery block must call tiles unavailable, naming D5's exact reason")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "tiles-must-agree",
		"kind": "tiles", "type": "invoice", "image": "cover",
	})
	require.True(t, res.IsError, "the composer must refuse exactly what the discovery block calls unavailable")
	require.Contains(t, res.ForLLM, imageIneligibleReason)
	cvAssertFileAbsent(t, root, "tiles-must-agree")
}

func TestKnowledgeConfigure_CreateView_G1_MissingRequiredProperty_Refused(t *testing.T) {
	tool, ws, root := cvFixture(t)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "g1-calendar-missing",
		"kind": "calendar", "type": "invoice", // no 'date' binding given
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "date")
	cvAssertFileAbsent(t, root, "g1-calendar-missing")
}

// ---------------------------------------------------------------------------
// G2 — a number with a declared unit_property totals once per unit and NEVER
// combined; an explicit request for a combined total is refused.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_G2_UnitConsistency_RefuseAndPass(t *testing.T) {
	tool, ws, root := cvFixture(t)

	t.Run("refuse: unit overridden to a different property", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g2-refuse-wrong",
			"kind": "summary", "type": "invoice", "number": "amount", "unit": "status",
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, "currency")
		require.Contains(t, res.ForLLM, "combined")
		cvAssertFileAbsent(t, root, "g2-refuse-wrong")
	})

	t.Run("refuse: unit explicitly blanked, asking for a cross-unit total", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g2-refuse-blank",
			"kind": "summary", "type": "invoice", "number": "amount", "unit": "",
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, "currency")
		cvAssertFileAbsent(t, root, "g2-refuse-blank")
	})

	t.Run("pass: unit omitted, the declared pairing applies automatically", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g2-pass-auto",
			"kind": "summary", "type": "invoice", "number": "amount",
		})
		require.False(t, res.IsError, res.ForLLM)
		raw, err := os.ReadFile(cvViewPath(root, "g2-pass-auto"))
		require.NoError(t, err)
		require.Contains(t, string(raw), "unit: currency")
	})

	t.Run("pass: unit explicitly given and it agrees with the declared pairing", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g2-pass-explicit",
			"kind": "summary", "type": "invoice", "number": "amount", "unit": "currency",
		})
		require.False(t, res.IsError, res.ForLLM)
	})
}

// ---------------------------------------------------------------------------
// G3 — a renderer obligation, but the composer must RECORD the pairing on
// every part that aggregates a unit-bearing number, and must NOT record one
// on a part whose number carries no unit.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_G3_RecordsUnitExclusion(t *testing.T) {
	tool, ws, root := cvFixture(t)

	t.Run("unit-bearing number: every part records the unit", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g3-with-unit",
			"kind": "trend", "type": "invoice", "date": "due_date", "number": "amount",
		})
		require.False(t, res.IsError, res.ForLLM)

		schemas, _, lerr := records.LoadSchemas(root)
		require.NoError(t, lerr)
		set, report, lerr := records.LoadViews(root, schemas)
		require.NoError(t, lerr)
		require.True(t, report.OK(), "%v", report.Rejections)
		v, ok := set.Get("g3-with-unit")
		require.True(t, ok)
		parts, ok := v.EffectiveParts()
		require.True(t, ok)
		require.Len(t, parts, 3) // figures, chart, table
		for _, p := range parts {
			if p.Part == generated.ViewPartPartFigures || p.Part == generated.ViewPartPartChart {
				require.NotNil(t, p.Unit, "part %q must record the unit", p.Part)
				require.Equal(t, "currency", *p.Unit)
			}
		}
	})

	t.Run("number without a declared unit: no part records one", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g3-no-unit",
			"kind": "summary", "type": "invoice", "number": "count",
		})
		require.False(t, res.IsError, res.ForLLM)

		schemas, _, lerr := records.LoadSchemas(root)
		require.NoError(t, lerr)
		set, _, lerr := records.LoadViews(root, schemas)
		require.NoError(t, lerr)
		v, ok := set.Get("g3-no-unit")
		require.True(t, ok)
		parts, ok := v.EffectiveParts()
		require.True(t, ok)
		for _, p := range parts {
			require.Nil(t, p.Unit, "part %q must not record a spurious unit", p.Part)
		}
	})
}

// ---------------------------------------------------------------------------
// G4 — text is never accepted as a number binding, even when its values
// parse as numbers; the refusal offers the property-conversion path.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_G4_TextAsNumber_RefuseAndPass(t *testing.T) {
	tool, ws, root := cvFixture(t)

	t.Run("refuse: text property bound as number", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g4-refuse",
			"kind": "summary", "type": "invoice", "number": "name",
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, "text")
		require.Contains(t, res.ForLLM, "edit_record_type")
		cvAssertFileAbsent(t, root, "g4-refuse")
	})

	t.Run("pass: a genuine number property", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g4-pass",
			"kind": "summary", "type": "invoice", "number": "count",
		})
		require.False(t, res.IsError, res.ForLLM)
	})
}

// ---------------------------------------------------------------------------
// G5 — grouping is one property; a request with two grouping fields for a
// non-breakdown kind is refused, pointing at breakdown.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_G5_GroupingArity(t *testing.T) {
	tool, ws, root := cvFixture(t)

	t.Run("refuse: two grouping properties on summary", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g5-refuse-summary",
			"kind": "summary", "type": "invoice", "number": "amount",
			"group_by": []any{"status", "owner"},
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, "breakdown")
		cvAssertFileAbsent(t, root, "g5-refuse-summary")
	})

	t.Run("refuse: breakdown given only one grouping property", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g5-refuse-breakdown-one",
			"kind": "breakdown", "type": "invoice", "number": "amount",
			"group_by": []any{"status"},
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, "exactly two")
		cvAssertFileAbsent(t, root, "g5-refuse-breakdown-one")
	})

	t.Run("refuse: grouping on a kind that does not use it", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g5-refuse-table",
			"kind": "table", "type": "invoice", "group_by": "status",
		})
		require.True(t, res.IsError)
		require.Contains(t, res.ForLLM, "does not use grouping")
		cvAssertFileAbsent(t, root, "g5-refuse-table")
	})

	t.Run("pass: one grouping property on summary", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g5-pass-summary",
			"kind": "summary", "type": "invoice", "number": "amount", "group_by": "status",
		})
		require.False(t, res.IsError, res.ForLLM)
	})

	t.Run("pass: two DIFFERENT grouping properties on breakdown", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "g5-pass-breakdown",
			"kind": "breakdown", "type": "invoice", "number": "amount",
			"group_by": []any{"status", "owner"},
		})
		require.False(t, res.IsError, res.ForLLM)
	})
}

// ---------------------------------------------------------------------------
// G6 — refuse-or-complete: any gate failure writes NOTHING.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_G6_RefusalWritesNothing(t *testing.T) {
	tool, ws, root := cvFixture(t)

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"G1 near-miss", map[string]any{"kind": "board", "type": "invoice", "choice": "category"}},
		{"G2 wrong unit", map[string]any{"kind": "summary", "type": "invoice", "number": "amount", "unit": "status"}},
		{"G4 text number", map[string]any{"kind": "summary", "type": "invoice", "number": "name"}},
		{"G5 two groups", map[string]any{"kind": "summary", "type": "invoice", "number": "amount", "group_by": []any{"status", "owner"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viewName := "g6-" + tc.name
			args := map[string]any{"collection": "kb", "op": "create_view", "view": viewName}
			for k, v := range tc.args {
				args[k] = v
			}
			res := tool.Execute(a4Ctx("mia", ws), args)
			require.True(t, res.IsError, "expected a refusal for %s", tc.name)
			cvAssertFileAbsent(t, root, viewName)
		})
	}
}

// ---------------------------------------------------------------------------
// Cross-cutting: legacy write_view unaffected, round-trip through the real
// loader, and every one of the eight kinds is schema-valid.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_LegacyWriteViewStillWorks(t *testing.T) {
	tool, ws, root := cvFixture(t)

	// write_view (the raw escape hatch) must behave exactly as it did before
	// create_view existed — same op name, same argument shape, same result.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "legacy-open",
		"definition": map[string]any{
			"type":   "invoice",
			"filter": map[string]any{"property": "status", "op": "=", "value": "draft"},
		},
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, `view "legacy-open" saved`)
	raw, err := os.ReadFile(filepath.Join(root, ".omnipus-vault", "views", "legacy-open.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "name: legacy-open")

	del := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_view", "view": "legacy-open",
	})
	require.False(t, del.IsError, del.ForLLM)
}

func TestKnowledgeConfigure_CreateView_SummaryRoundTripsThroughLoadViews(t *testing.T) {
	tool, ws, root := cvFixture(t)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "unpaid-by-status",
		"kind": "summary", "type": "invoice", "number": "amount", "group_by": "status",
		"columns": []any{"file.name", "status", "amount"},
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "kind=summary")
	require.Contains(t, res.ForLLM, "figures")
	require.Contains(t, res.ForLLM, "table")

	schemas, _, lerr := records.LoadSchemas(root)
	require.NoError(t, lerr)
	set, report, lerr := records.LoadViews(root, schemas)
	require.NoError(t, lerr)
	require.True(t, report.OK(), "%v", report.Rejections)

	v, ok := set.Get("unpaid-by-status")
	require.True(t, ok)
	require.NotNil(t, v.Def.Kind)
	require.EqualValues(t, generated.ViewDefKindSummary, *v.Def.Kind)

	parts, ok := v.EffectiveParts()
	require.True(t, ok)
	require.Len(t, parts, 2)

	figures := parts[0]
	require.Equal(t, generated.ViewPartPartFigures, figures.Part)
	require.NotNil(t, figures.Number)
	require.Equal(t, "amount", *figures.Number)
	require.NotNil(t, figures.Unit)
	require.Equal(t, "currency", *figures.Unit)
	require.NotNil(t, figures.Aggregate)
	require.EqualValues(t, generated.ViewPartAggregateSum, *figures.Aggregate)

	table := parts[1]
	require.Equal(t, generated.ViewPartPartTable, table.Part)
	require.NotNil(t, table.Grouping)
	require.Len(t, *table.Grouping, 1)
	require.Equal(t, "status", (*table.Grouping)[0].Property)
	require.NotNil(t, table.Subtotals)
	require.EqualValues(t, generated.ViewPartAggregateSum, (*table.Subtotals)["amount"])
	require.NotNil(t, table.Unit)
	require.Equal(t, "currency", *table.Unit)
}

// TestKnowledgeConfigure_CreateView_SevenOfEightKindsAreSchemaValid is D5's
// own acceptance fixture (design §9): "7-of-8 available plus
// tiles-unavailable-with-this-exact-reason." Seven kinds must SUCCEED and
// round-trip through the real loader; tiles must REFUSE, unconditionally,
// with D5's exact wording — see
// TestKnowledgeConfigure_CreateView_G1_Tiles_AlwaysRefused for that half in
// isolation.
func TestKnowledgeConfigure_CreateView_SevenOfEightKindsAreSchemaValid(t *testing.T) {
	tool, ws, root := cvFixture(t)

	cases := []struct {
		kind string
		args map[string]any
	}{
		{"table", map[string]any{}},
		{"list", map[string]any{}},
		{"board", map[string]any{"choice": "status"}},
		{"calendar", map[string]any{"date": "due_date"}},
		{"summary", map[string]any{"number": "amount", "group_by": "status"}},
		{"trend", map[string]any{"date": "due_date", "number": "amount"}},
		{"breakdown", map[string]any{"number": "amount", "group_by": []any{"status", "owner"}}},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			viewName := "all-kinds-" + tc.kind
			args := map[string]any{
				"collection": "kb", "op": "create_view", "view": viewName,
				"kind": tc.kind, "type": "invoice",
			}
			for k, v := range tc.args {
				args[k] = v
			}
			res := tool.Execute(a4Ctx("mia", ws), args)
			require.False(t, res.IsError, "kind=%s: %s", tc.kind, res.ForLLM)
		})
	}

	t.Run("tiles", func(t *testing.T) {
		res := tool.Execute(a4Ctx("mia", ws), map[string]any{
			"collection": "kb", "op": "create_view", "view": "all-kinds-tiles",
			"kind": "tiles", "type": "invoice", "image": "cover",
		})
		require.True(t, res.IsError, "D5: tiles must refuse unconditionally")
		require.Contains(t, res.ForLLM, imageIneligibleReason)
		cvAssertFileAbsent(t, root, "all-kinds-tiles")
	})

	// Every one of the seven files just written must load and validate
	// clean through the SAME loader knowledge_describe and knowledge_find use
	// — a composer that produced a shape ONLY this tool's own write path
	// accepted would be exactly the two-notions-of-valid failure view.go's
	// header warns against.
	schemas, _, lerr := records.LoadSchemas(root)
	require.NoError(t, lerr)
	set, report, lerr := records.LoadViews(root, schemas)
	require.NoError(t, lerr)
	require.True(t, report.OK(), "%v", report.Rejections)
	for _, tc := range cases {
		_, ok := set.Get("all-kinds-" + tc.kind)
		require.True(t, ok, "kind=%s must have loaded", tc.kind)
	}
	_, tilesWritten := set.Get("all-kinds-tiles")
	require.False(t, tilesWritten, "a refused tiles view must not have been written")
}

// ---------------------------------------------------------------------------
// Argument-shape edge: create_view's own unknown-argument handling matches
// the tool's existing generic one (unknownArgs), for a create_view-specific
// argument name rather than a generic op-level one.
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_CreateView_UnknownArgument_Refused(t *testing.T) {
	tool, ws, _ := cvFixture(t)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "bogus-arg",
		"kind": "table", "type": "invoice", "column": "typo-for-columns",
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "column")
}

func TestKnowledgeConfigure_CreateView_UnknownKind_Refused(t *testing.T) {
	tool, ws, root := cvFixture(t)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "bad-kind",
		"kind": "pie-chart", "type": "invoice",
	})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "table")     // one of the eight permitted kinds is named
	require.Contains(t, res.ForLLM, "breakdown") // and the last one too
	cvAssertFileAbsent(t, root, "bad-kind")
}

// TestKnowledgeConfigure_CreateView_BreakdownNeverGroupsByItsOwnNumber pins
// the discover/compose agreement the verify pass found broken: describe's
// breakdownAvailability offers group candidates EXCLUDING the bound number,
// so the composer must refuse a crosstab grouped by the number it aggregates
// — the milder direction of the tiles disagreement, but a disagreement.
func TestKnowledgeConfigure_CreateView_BreakdownNeverGroupsByItsOwnNumber(t *testing.T) {
	tool, ws, root := cvFixture(t)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "bd-selfgroup",
		"kind": "breakdown", "type": "invoice", "number": "amount",
		"group_by": []any{"amount", "status"},
	})
	require.True(t, res.IsError, "breakdown grouped by its own number must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "OTHER than the number")
	cvAssertFileAbsent(t, root, "bd-selfgroup")
}

// ---------------------------------------------------------------------------
// The view name is a FILENAME, and the agent supplies it
// ---------------------------------------------------------------------------

// TestKnowledgeConfigure_CreateView_NameEscapingTheViewsDir_Refused is the
// create_view half of the traversal finding.
//
// Every gate G1..G6 above judges the composed STACK. None of them judges the
// `view` argument, which is trimmed and then joined straight onto
// records.ViewsDir(root) with a ".yaml" suffix — so "../records/invoice"
// passes every gate, composes a perfectly valid table view, and lands on top
// of the invoice record type's own schema file. G6's promise that "a refused
// call writes nothing" is intact; what is broken is that an ACCEPTED call
// writes somewhere the views directory does not contain.
//
// The oracle is the fixture's schema file still holding its own bytes.
func TestKnowledgeConfigure_CreateView_NameEscapingTheViewsDir_Refused(t *testing.T) {
	tool, ws, root := cvFixture(t)

	schemaPath := filepath.Join(root, ".omnipus-vault", "records", "invoice.yaml")
	before, err := os.ReadFile(schemaPath)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		view string
	}{
		{"climbs into the sibling records directory", "../records/invoice"},
		{"escapes the vault entirely", "../../../../pwned"},
		{"windows-style separator", `..\records\invoice`},
		{"absolute path", "/tmp/omnipus-cv-pwned"},
		{"bare dot", "."},
		{"bare dotdot", ".."},
		{"nested below views", "sub/dir/view"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"collection": "kb", "op": "create_view", "view": tc.view,
				"kind": "table", "type": "invoice",
			})
			require.True(t, res.IsError, "must be refused, got: %s", res.ForLLM)
			require.Contains(t, res.ForLLM, "must be a name, not a path",
				"the refusal must name the rule it enforced")

			after, rerr := os.ReadFile(schemaPath)
			require.NoError(t, rerr, "the record-type schema must still exist")
			require.Equal(t, string(before), string(after),
				"the record-type schema must not have been overwritten")
		})
	}

	for _, escaped := range []string{
		filepath.Join(root, ".omnipus-vault", "pwned.yaml"),
		filepath.Join(filepath.Dir(root), "pwned.yaml"),
		filepath.Join(root, ".omnipus-vault", "views", "sub", "dir", "view.yaml"),
		"/tmp/omnipus-cv-pwned.yaml",
	} {
		_, serr := os.Stat(escaped)
		require.True(t, os.IsNotExist(serr), "nothing may be written at %s", escaped)
	}

	// Control: an ordinary name still composes and writes.
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "plain-invoices",
		"kind": "table", "type": "invoice",
	})
	require.False(t, ok.IsError, ok.ForLLM)
	_, err = os.Stat(cvViewPath(root, "plain-invoices"))
	require.NoError(t, err)
}
