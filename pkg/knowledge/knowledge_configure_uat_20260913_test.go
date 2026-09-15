// Omnipus — knowledge_configure / knowledge_restructure / knowledge_describe /
// op=relation regressions for UAT 2026-09-13 (workstream W2). Fixture
// conventions follow knowledge_configure_create_view_test.go.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// D-47 — `type` inside the definition is enough.
func TestUAT_D47_CreateRecordTypeTakesTypeFromDefinition(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type",
		"definition": map[string]any{"schema_version": float64(1), "type": "gadget",
			"properties": map[string]any{"name": map[string]any{"type": "text"}}},
	})
	require.False(t, res.IsError, res.ForLLM)
	require.FileExists(t, filepath.Join(root, ".omnipus-vault", "records", "gadget.yaml"))
}

// D-56 — names that would become hostile file names are refused.
func TestUAT_D56_ControlPlaneNamesAreValidated(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := kcTool(deps)
	bad := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "describe placeholder",
		"definition": map[string]any{"schema_version": float64(1), "properties": map[string]any{}},
	})
	require.True(t, bad.IsError)
	require.Contains(t, bad.ForLLM, "whitespace")
	require.NoFileExists(t, filepath.Join(root, ".omnipus-vault", "records", "describe placeholder.yaml"))

	quoted := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": `All Projects" (b)`,
		"definition": map[string]any{"layout": "table"},
	})
	require.True(t, quoted.IsError)
	require.Contains(t, quoted.ForLLM, `'"'`)
	entries, _ := os.ReadDir(filepath.Join(root, ".omnipus-vault", "views"))
	require.Empty(t, entries)

	// A space in a VIEW name is fine — labels and view names are prose.
	ok := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "All Projects",
		"definition": map[string]any{"layout": "table"},
	})
	require.False(t, ok.IsError, ok.ForLLM)
}

// D-21 — duplicate and case-colliding view names, and duplicate labels, are refused.
func TestUAT_D21_ViewNameAndLabelCollisionsRefused(t *testing.T) {
	tool, ws, root := cvFixture(t)
	first := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "Case Ladder", "kind": "table", "type": "invoice",
	})
	require.False(t, first.IsError, first.ForLLM)
	before, err := os.ReadFile(cvViewPath(root, "Case Ladder"))
	require.NoError(t, err)

	caseClash := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "CASE LADDER", "kind": "list", "type": "invoice",
	})
	require.True(t, caseClash.IsError, "a case-colliding name must be refused: %s", caseClash.ForLLM)
	require.Contains(t, caseClash.ForLLM, "differ only by letter case")
	after, err := os.ReadFile(cvViewPath(root, "Case Ladder"))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "the existing view must be untouched")

	exact := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "Case Ladder", "kind": "list", "type": "invoice",
	})
	require.True(t, exact.IsError)
	require.Contains(t, exact.ForLLM, "create_view never overwrites")

	// write_view: the same name is an upsert; a label another view carries is refused.
	up := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "Case Ladder",
		"definition": map[string]any{"type": "invoice", "layout": "table", "label": "Ladder"},
	})
	require.False(t, up.IsError, up.ForLLM)
	dupLabel := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "other",
		"definition": map[string]any{"type": "invoice", "layout": "table", "label": "ladder"},
	})
	require.True(t, dupLabel.IsError)
	require.Contains(t, dupLabel.ForLLM, `label "ladder" is already carried by view "Case Ladder"`)
}

// D-13 (agent half) — `source` ties a view to a .base and starts the file.
func TestUAT_D13_ViewSourceIsAcceptedAndStarterBaseWritten(t *testing.T) {
	tool, ws, root := cvFixture(t)
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "Active", "kind": "table", "type": "invoice",
		"source": "Invoices.base",
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "SOURCE: Invoices.base (a starter data file was created there")
	viewYAML, err := os.ReadFile(cvViewPath(root, "Active"))
	require.NoError(t, err)
	require.Contains(t, string(viewYAML), "source: Invoices.base\n")
	base, err := os.ReadFile(filepath.Join(root, "Invoices.base"))
	require.NoError(t, err)
	require.Contains(t, string(base), "views:\n")
	require.Contains(t, string(base), `name: "Active"`)

	// A second view on the same source leaves the existing .base alone.
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "Paid", "source": "Invoices.base",
		"definition": map[string]any{"type": "invoice", "layout": "table"},
	})
	require.False(t, res2.IsError, res2.ForLLM)
	require.Contains(t, res2.ForLLM, "SOURCE: Invoices.base")
	require.NotContains(t, res2.ForLLM, "starter data file was created")
	again, _ := os.ReadFile(filepath.Join(root, "Invoices.base"))
	require.Equal(t, string(base), string(again))

	// No source: the reply says the view has no place in the UI.
	res3 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "Orphan", "kind": "table", "type": "invoice",
	})
	require.False(t, res3.IsError, res3.ForLLM)
	require.Contains(t, res3.ForLLM, "SOURCE: none")

	bad := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "Bad", "kind": "table", "type": "invoice",
		"source": "Invoices.md",
	})
	require.True(t, bad.IsError)
	require.Contains(t, bad.ForLLM, "must name a .base data file")
}

// D-55 — a JSON list sent as a string is decoded, not counted as one element.
func TestUAT_D55_GroupByJSONStringIsDecoded(t *testing.T) {
	tool, ws, _ := cvFixture(t)
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "Break", "kind": "breakdown", "type": "invoice",
		"group_by": `["status", "currency"]`, "number": "amount",
	})
	require.False(t, res.IsError, res.ForLLM)
	garbage := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "Break2", "kind": "breakdown", "type": "invoice",
		"group_by": `["status", `, "number": "amount",
	})
	require.True(t, garbage.IsError)
	require.Contains(t, garbage.ForLLM, "does not parse as one")
}

// D-31 / D-32 — no Go internals; the two view vocabularies cross-reference.
func TestUAT_D31_D32_ViewDefinitionErrorsArePlainAndCrossReferenced(t *testing.T) {
	tool, ws, _ := cvFixture(t)
	formulas := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "F",
		"definition": map[string]any{"type": "invoice", "formulas": []any{"a", "b"}},
	})
	require.True(t, formulas.IsError)
	require.NotContains(t, formulas.ForLLM, "Go struct")
	require.NotContains(t, formulas.ForLLM, "map[string]string")
	require.Contains(t, formulas.ForLLM, "field `formulas` must be a mapping of formula name to expression text, but the definition gives a list")

	layout := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "L",
		"definition": map[string]any{"type": "invoice", "layout": "chart"},
	})
	require.True(t, layout.IsError)
	require.Contains(t, layout.ForLLM, "op=create_view speaks `kind` instead")
	require.Contains(t, layout.ForLLM, "breakdown")

	kind := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "K", "kind": "cards", "type": "invoice",
	})
	require.True(t, kind.IsError)
	require.Contains(t, kind.ForLLM, "write_view's legacy `layout`")
	require.Contains(t, kind.ForLLM, "gallery")
}

// D-25 — the edit cascade names stale keys and broken views.
func TestUAT_D25_EditRecordTypeCascadeNamesStaleKeysAndBrokenViews(t *testing.T) {
	tool, ws, root := cvFixture(t)
	a4Note(t, root, "I1.md", "---\ntype: invoice\nid: 0001\nstatus: draft\nowner: me\n---\nx\n")
	a4Note(t, root, "I2.md", "---\ntype: invoice\nid: 0002\nstatus: sent\nowner: you\n---\nx\n")
	view := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "write_view", "view": "By owner",
		"definition": map[string]any{"type": "invoice", "layout": "table", "properties": []any{"owner"}},
	})
	require.False(t, view.IsError, view.ForLLM)

	// Rename owner -> holder by removing one and adding the other.
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "edit_record_type", "type": "invoice",
		"definition": map[string]any{"schema_version": float64(1), "properties": map[string]any{
			"name": map[string]any{"type": "text"}, "holder": map[string]any{"type": "text"},
			"status": map[string]any{"type": "enum", "values": []any{"draft", "sent", "paid"}},
		}},
	})
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "STALE KEYS: notes still carry key(s) this type no longer declares — owner (2 note(s))", res.ForLLM)
	require.Contains(t, res.ForLLM, "VIEWS BROKEN: 1 saved view(s) no longer load after this edit:")
	require.Contains(t, res.ForLLM, "By owner (")
}

// D-27 — delete_view names the embeds and the raw .base it does not touch.
func TestUAT_D27_DeleteViewNamesEmbedsAndRawBase(t *testing.T) {
	tool, ws, root := cvFixture(t)
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "create_view", "view": "active-invoices", "kind": "table", "type": "invoice",
		"source": "Invoices.base",
	})
	require.False(t, res.IsError, res.ForLLM)
	a4Note(t, root, "Dashboards/Overview.md", "# Overview\n\n![[Invoices.base#active-invoices]]\n")
	a4Note(t, root, "Unrelated.md", "Mentions active-invoices in prose only.\n")

	del := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "delete_view", "view": "active-invoices",
	})
	require.False(t, del.IsError, del.ForLLM)
	require.Contains(t, del.ForLLM, "1 note(s) embed this view (as #active-invoices) and will now show it as a broken embed: Dashboards/Overview.md")
	require.NotContains(t, del.ForLLM, "Unrelated.md")
	require.Contains(t, del.ForLLM, "the data file Invoices.base still lists a view named")
}

// D-24 — knowledge_describe states the supported property types.
func TestUAT_D24_DescribeListsPropertyTypes(t *testing.T) {
	d := DescribeData{Sections: map[string]bool{DescribeSectionTypes: true}, Schemas: records.NewSchemaSet()}
	out := RenderDescribe(d)
	require.Contains(t, out, "property types: text, enum, relation, date, integer, decimal, person, checkbox (add many: true for a list", out)
}

// D-53 — move creates the destination folder and says so.
func TestUAT_D53_MoveCreatesDestinationFolder(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)
	a4Note(t, root, "Meetings/Standup.md", "# Standup\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "move", "collection": "KB", "path": "Meetings/Standup.md", "new_folder": "Meetings/2026",
	})
	require.False(t, res.IsError, "the folder must be created for the move: %s", res.ForLLM)
	require.FileExists(t, filepath.Join(root, "Meetings", "2026", "Standup.md"))
	require.Contains(t, res.ForLLM, "folder Meetings/2026 created")
}

// D-22 — restore onto an occupied path names both the live note and the trashed copy.
func TestUAT_D22_RestoreCollisionNamesBothPaths(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)
	a4Note(t, root, "Note.md", "first\n")
	trashRes := tool.Execute(a4Ctx("mia", ws), map[string]any{"op": "trash", "collection": "KB", "path": "Note.md"})
	require.False(t, trashRes.IsError, trashRes.ForLLM)
	a4Note(t, root, "Note.md", "second, occupying the path\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"op": "restore", "collection": "KB", "path": "Note.md"})
	require.True(t, res.IsError)
	require.Contains(t, res.ForLLM, "a live note already occupies Note.md")
	require.Contains(t, res.ForLLM, "trashed_at ")
	require.Contains(t, res.ForLLM, ".omnipus-vault/trash/")
	require.Contains(t, res.ForLLM, "/Note.md) cannot be restored onto it")
}

// D-94 — relation targets are stored in the vault's own bare-name form.
func TestUAT_D94_RelationTargetPathIsNormalised(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	tool := veTool(deps)
	uatProjectSchema(t, root)
	a4Note(t, root, "People/Tobias Brandt-Larsen.md", "---\ntype: person\n---\nx\n")
	a4Note(t, root, "People/Ana.md", "x\n")
	a4Note(t, root, "Archive/Ana.md", "x\n") // ambiguous bare name
	a4Note(t, root, "P.md", "---\ntype: project\nid: PRJ-0001\nstatus: planned\n---\nBody.\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "P.md", "property": "owner",
		"relation_op": "replace", "targets": []any{"People/Tobias Brandt-Larsen.md"},
		"expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, res.IsError, res.ForLLM)
	got := a4Read(t, root, "P.md")
	require.Contains(t, got, `owner: "[[Tobias Brandt-Larsen]]"`, got)
	require.Contains(t, res.ForLLM, "STORED AS: People/Tobias Brandt-Larsen.md -> [[Tobias Brandt-Larsen]]")

	// An ambiguous bare name keeps its folder (minus the extension).
	res2 := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"collection": "kb", "op": "relation", "path": "P.md", "property": "owner",
		"relation_op": "replace", "targets": []any{"People/Ana.md"},
		"expect_version": a4Version(t, root, "P.md"),
	})
	require.False(t, res2.IsError, res2.ForLLM)
	require.Contains(t, a4Read(t, root, "P.md"), `owner: "[[People/Ana]]"`)
	require.True(t, strings.Contains(res2.ForLLM, "People/Ana.md -> [[People/Ana]]"), res2.ForLLM)
}
