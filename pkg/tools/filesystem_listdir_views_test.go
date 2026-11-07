// Tests for list_directory showing a knowledge base's placed saved views as
// VIEW: lines (#1017, founder answer LQ2=A 2026-11-07).
//
// The oracle is the design note's "What agents see" section, which states the
// exact line format — `VIEW: <label> (kind <kind>, view <name>, knowledge base
// <name>)` per view placed in the listed folder, using the same placement
// rules the REST listing's L5/L6 (see /workspace/logs/library-views/design.md).
// No implementation exists yet; every test here is RED today because no VIEW:
// line is rendered at all.
//
// The marker file name and display_name key are written as LITERALS: this is
// package tools, which cannot import pkg/knowledge (the cycle is documented on
// KnowledgeBaseMarkerNames in filesystem.go), so the fixture mirrors
// knowledge.MarkerPath — `.omnipus-vault/vault.json` with {"display_name": …} —
// the same names pkg/gateway's makeKnowledgeBase writes via knowledge.Marker.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listdirViewsSchema is the record-type schema the fixture views refer to,
// shaped like the gateway knowledge tests' own fixture schema.
const listdirViewsSchema = "schema_version: 1\n" +
	"type: invoice\n" +
	"properties:\n" +
	"  client:   { type: text }\n" +
	"  amount:   { type: decimal, unit_property: currency }\n"

// writeListdirVault writes a knowledge base named displayName at dir with one
// record schema and the given saved-view files (file name -> YAML body).
func writeListdirVault(t *testing.T, dir, displayName string, views map[string]string) {
	t.Helper()
	vaultDir := filepath.Join(dir, ".omnipus-vault")
	require.NoError(t, os.MkdirAll(filepath.Join(vaultDir, "records"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(vaultDir, "views"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(vaultDir, "vault.json"),
		[]byte(`{"display_name":"`+displayName+`"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(vaultDir, "records", "invoice.yaml"),
		[]byte(listdirViewsSchema), 0o600))
	for name, body := range views {
		require.NoError(t, os.WriteFile(filepath.Join(vaultDir, "views", name), []byte(body), 0o600))
	}
}

// TestListDirTool_PlacedViewRenderedAsViewLine is the acceptance line: an agent
// listing a Library folder sees the views placed there, with their kind. The
// design states the exact line, so it is asserted exactly.
//
// RED on the pre-change code: list_directory renders no VIEW: line at all.
func TestListDirTool_PlacedViewRenderedAsViewLine(t *testing.T) {
	ws := t.TempDir()
	vault := filepath.Join(ws, "MyVault")
	writeListdirVault(t, vault, "Finance vault", map[string]string{
		"invoices--outstanding.yaml": "name: invoices--outstanding\n" +
			"type: invoice\n" +
			"label: Outstanding\n" +
			"kind: summary\n" +
			"properties: [client]\n" +
			"source: CRM/Invoices.base\n",
	})
	require.NoError(t, os.MkdirAll(filepath.Join(vault, "CRM"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(vault, "CRM", "invoice-1.md"),
		[]byte("# Invoice\n"), 0o600))

	tool := NewListDirTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "MyVault/CRM"})
	require.False(t, result.IsError, result.ForLLM)

	assert.Contains(t, result.ForLLM,
		"VIEW: Outstanding (kind summary, view invoices--outstanding, knowledge base Finance vault)",
		"LQ2 A: the view placed in this folder by its source must render in the design's exact line format")
	assert.Contains(t, result.ForLLM, "FILE: invoice-1.md",
		"the ordinary files of the folder must still be listed beside the view")
}

// TestListDirTool_AuthoredViewAtKnowledgeBaseRoot pins the L6 half for agents:
// an authored view (no source) belongs to the knowledge base root, so listing
// the root shows it.
//
// The design states the line format WITH a kind; for a view whose file
// declares none it states no exact rendering, so this test pins only what the
// design does state — a VIEW: line carrying the label, the view name and the
// knowledge base name — and leaves the exact kindless rendering as a reported
// open point rather than inventing one.
//
// RED on the pre-change code: no VIEW: line is rendered.
func TestListDirTool_AuthoredViewAtKnowledgeBaseRoot(t *testing.T) {
	ws := t.TempDir()
	writeListdirVault(t, filepath.Join(ws, "MyVault2"), "Second vault", map[string]string{
		"status--open.yaml": "name: status--open\n" +
			"type: invoice\n" +
			"label: Open items\n" +
			"properties: [client]\n",
	})

	tool := NewListDirTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "MyVault2"})
	require.False(t, result.IsError, result.ForLLM)

	assert.Contains(t, result.ForLLM, "VIEW: Open items",
		"LQ2 A: the authored view must render as a VIEW: line in its knowledge base root")
	assert.Contains(t, result.ForLLM, "view status--open",
		"LQ2 A: the line must carry the view's addressable name")
	assert.Contains(t, result.ForLLM, "knowledge base Second vault",
		"LQ2 A: the line must carry the knowledge base's name")
	assert.NotContains(t, result.ForLLM, "FILE: status--open.yaml",
		"a view must be rendered as a VIEW: line, not dressed up as a plain file row")
}

// TestListDirTool_FoldersWithoutViewsRenderUnchanged is the guard half: folders
// with no views — a plain folder, or a knowledge base that has none — list
// exactly as before, with no VIEW: lines and the existing KB:/DIR:/FILE:
// marking intact.
//
// GUARD RULE: expected green on the pre-change code; it refuses an
// implementation that adds VIEW: noise to folders that hold no views.
func TestListDirTool_FoldersWithoutViewsRenderUnchanged(t *testing.T) {
	ws := t.TempDir()
	writeListdirVault(t, filepath.Join(ws, "MyVault3"), "Empty vault", nil)
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "PlainFolder"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "todo.txt"), []byte("x"), 0o644))

	tool := NewListDirTool(ws, true)

	root := tool.Execute(context.Background(), map[string]any{"path": "."})
	require.False(t, root.IsError, root.ForLLM)
	assert.NotContains(t, root.ForLLM, "VIEW:", "a folder with no views must gain no VIEW: line")
	assert.Contains(t, root.ForLLM, "KB:   MyVault3", "the KB marking must be unchanged")
	assert.Contains(t, root.ForLLM, "DIR:  PlainFolder", "ordinary dirs must be unchanged")
	assert.Contains(t, root.ForLLM, "FILE: todo.txt", "ordinary files must be unchanged")

	vault := tool.Execute(context.Background(), map[string]any{"path": "MyVault3"})
	require.False(t, vault.IsError, vault.ForLLM)
	t.Logf("GUARD ok: root and vault listings carry no VIEW: line")
	assert.NotContains(t, vault.ForLLM, "VIEW:",
		"a knowledge base with no saved views must list exactly as before")
}
