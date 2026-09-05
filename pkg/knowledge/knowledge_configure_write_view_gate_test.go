// Omnipus — DEFECT 1a: op=write_view must not author the kind/part vocabulary.
//
// The 2026-09-05 view-kinds UAT (docs/internal/specs/uat-findings-view-kinds-
// 2026-09-05.md, D1) proved the design's §3 promise — "All six [gates] live in
// the composer and renderer — testable once, skippable by no agent" — was a
// property of ONE OP rather than of the tool. Asked for a `trend` on a record
// type with no number property, the tester agent was refused twice by
// create_view, then called write_view TEN times until one succeeded, and the
// server served the result as `kind: trend` with an empty figures row, an empty
// chart and `problems: []`.
//
// Ruling D6 (design §9) closes it at the authoring layer: the composer is the
// SOLE author of the kind/part vocabulary. write_view keeps the legacy shape —
// layout / filter / grouping / properties / aggregates — which is the actual
// escape-hatch tail and the shape hand-edited files use.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//        -run '^TestKnowledgeConfigure_WriteView' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// wvNumberlessType declares the UAT's own vault-A shape: a record type with
// four dates and four small enums and NO number at all — the type on which
// `trend` is impossible and create_view correctly refused twice.
func wvNumberlessType(t *testing.T, tool *ConfigureTool, ws string) {
	t.Helper()
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "task",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"title":     map[string]any{"type": "text"},
				"status":    map[string]any{"type": "enum", "values": []any{"todo", "doing", "done"}},
				"priority":  map[string]any{"type": "enum", "values": []any{"low", "high"}},
				"created":   map[string]any{"type": "date"},
				"completed": map[string]any{"type": "date"},
			},
		},
	})
	require.False(t, res.IsError, "fixture record type must be created: %s", res.ForLLM)
}

// ---------------------------------------------------------------------------
// The UAT's exact repro
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_WriteView_TrendOnANumberlessTypeIsRefused(t *testing.T) {
	tool, ws, root := cvFixture(t)
	wvNumberlessType(t, tool, ws)

	// First, the fact the repro rests on: the composer refuses this, naming
	// the missing requirement. If this ever stops refusing, the write_view
	// test below is measuring nothing.
	composed := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "uat-task-trend",
		"kind": "trend", "type": "task", "date": "completed",
	})
	require.True(t, composed.IsError, "create_view must refuse a trend on a type with no number: %s", composed.ForLLM)
	cvAssertFileAbsent(t, root, "uat-task-trend")

	// Now the door the agent walked through: byte-for-byte the definition the
	// UAT's tenth write_view call landed on disk.
	raw := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "uat-task-trend",
		"definition": map[string]any{
			"type": "task",
			"kind": "trend",
			"parts": []any{
				map[string]any{"part": "figures", "aggregate": "count"},
				map[string]any{"part": "chart", "aggregate": "count", "date": "completed",
					"subtotals": map[string]any{"completed": "count"}},
				map[string]any{"part": "table", "properties": []any{"title", "status"}},
			},
		},
	})
	require.True(t, raw.IsError, "write_view must refuse a definition carrying kind/parts: %s", raw.ForLLM)
	require.Contains(t, raw.ForLLM, "create_view",
		"the refusal must name the op that DOES author this vocabulary")
	cvAssertFileAbsent(t, root, "uat-task-trend")
}

// ---------------------------------------------------------------------------
// The directed probe: D5's tiles case, asserted through the raw door
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_WriteView_TilesPartStackIsRefused(t *testing.T) {
	tool, ws, root := cvFixture(t)

	// D5 rejected binding tiles to text because it "would bind a rendering
	// behaviour to unvalidated strings". `status` is an enum, which is no more
	// image-capable — and the UAT's follow-up turn wrote exactly this and had
	// it served as `kind: tiles`, 131 rows, problems: [].
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "uat-tiles",
		"definition": map[string]any{
			"type":  "invoice",
			"kind":  "tiles",
			"parts": []any{map[string]any{"part": "tiles", "image": "status"}},
		},
	})
	require.True(t, res.IsError, "write_view must refuse a tiles part stack: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "create_view")
	cvAssertFileAbsent(t, root, "uat-tiles")
}

// ---------------------------------------------------------------------------
// The gate is on the VOCABULARY, not on one impossible request
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_WriteView_RefusesKindEvenWhenTheKindIsAvailable(t *testing.T) {
	tool, ws, root := cvFixture(t)

	// `invoice` DOES declare a number with a unit, so kind=summary is
	// genuinely available here — create_view would compose it. The refusal is
	// not "this view is impossible", it is "this vocabulary is not yours to
	// write": a gate that only fired on impossible requests would still leave
	// two authors of the same closed set, disagreeing the first time one of
	// them was extended.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "raw-summary",
		"definition": map[string]any{"type": "invoice", "kind": "summary"},
	})
	require.True(t, res.IsError, "write_view must refuse `kind` outright: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "create_view")
	cvAssertFileAbsent(t, root, "raw-summary")
}

func TestKnowledgeConfigure_WriteView_RefusesPartsWithNoKindAtAll(t *testing.T) {
	tool, ws, root := cvFixture(t)

	// `parts` without `kind` is the same authorship by another route — and it
	// is the shape that produced the UAT's quieter failure, a `table` part
	// carrying an inert `aggregate` and no `subtotals`, served as 69 rows in 6
	// groups with every subtotal empty and problems: [].
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "raw-parts",
		"definition": map[string]any{
			"type": "invoice",
			"parts": []any{
				map[string]any{"part": "table", "grouping": []any{map[string]any{"property": "status"}},
					"aggregate": "count", "properties": []any{"name", "status"}},
			},
		},
	})
	require.True(t, res.IsError, "write_view must refuse `parts`: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "create_view")
	cvAssertFileAbsent(t, root, "raw-parts")
}

// ---------------------------------------------------------------------------
// The escape hatch stays open for the shape it is actually for
// ---------------------------------------------------------------------------

func TestKnowledgeConfigure_WriteView_LegacyShapeStillWritesUnchanged(t *testing.T) {
	tool, ws, root := cvFixture(t)

	// D6 narrows write_view to the LEGACY shape, and this is that shape in
	// full — layout, filter, grouping, properties, aggregates, sort, limit,
	// label. It is what every hand-edited file and every imported .base view
	// looks like, and it must go on working exactly as it did before D6.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "legacy-full",
		"definition": map[string]any{
			"type":       "invoice",
			"label":      "Draft invoices",
			"layout":     "table",
			"filter":     map[string]any{"property": "status", "op": "=", "value": "draft"},
			"grouping":   []any{map[string]any{"property": "status"}},
			"properties": []any{"name", "status", "amount"},
			"aggregates": []any{map[string]any{"property": "count", "op": "sum"}},
			"sort":       []any{map[string]any{"property": "name", "direction": "asc"}},
			"limit":      float64(50),
		},
	})
	require.False(t, res.IsError, "the legacy shape must still be writable: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, `view "legacy-full" saved`)

	raw, err := os.ReadFile(filepath.Join(root, ".omnipus-vault", "views", "legacy-full.yaml"))
	require.NoError(t, err)
	body := string(raw)
	require.Contains(t, body, "name: legacy-full")
	require.Contains(t, body, "layout: table")
	require.Contains(t, body, "aggregates:")
	// And it must not have grown a kind or a parts stack on the way through.
	require.NotContains(t, body, "kind:")
	require.NotContains(t, body, "parts:")
}
