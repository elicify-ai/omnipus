// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure_test.go — tool-level coverage for knowledge_restructure
// (ADR-068 D15.3 item 5, spec §4.1.5), reusing the a4* fixtures
// authoring_tools_test.go/lifecycle_test.go already provide. NewRestructureTool
// is constructed directly rather than through AuthoringTools(deps) — wiring it
// into that registry is wave 2's job (see this file's own tool report), not
// this package's test setup.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// AC-X3 — no expect_version, ever, worded exactly per §4.1.5
// ---------------------------------------------------------------------------

func TestRestructureTool_DeclaresNoExpectVersionParameter(t *testing.T) {
	tool := NewRestructureTool(AuthoringDeps{})
	params := tool.Parameters()
	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	_, hasVersion := props["expect_version"]
	assert.False(t, hasVersion, "AC-X3: knowledge_restructure must not declare expect_version at all")
}

func TestRestructureTool_RefusesASuppliedExpectVersion(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(root, "A.md"), []byte("a"), 0o644))
	deps, rec := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "KB", "path": "A.md", "new_name": "B.md",
		"expect_version": "v1:deadbeef",
	})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "knowledge_restructure takes no expect_version")
	assert.Contains(t, res.ForLLM, "knowledge_read")

	// Nothing moved.
	assert.FileExists(t, filepath.Join(root, "A.md"))
	assert.NoFileExists(t, filepath.Join(root, "B.md"))

	// The refusal is audited (this layer refuses before the engine is ever
	// reached, so it is the one refusal class the engine cannot record).
	require.NotEmpty(t, rec.refusals())
}

// ---------------------------------------------------------------------------
// FR-070b — the blast-radius redirects
// ---------------------------------------------------------------------------

func TestRestructureTool_RedirectsOneFileEditOpsToKnowledgeEdit(t *testing.T) {
	home, ws, _ := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	for _, op := range []string{"create", "set_property", "append_section", "link", "replace_body"} {
		t.Run(op, func(t *testing.T) {
			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"op": op, "collection": "KB", "path": "A.md",
			})
			require.True(t, res.IsError)
			assert.Contains(t, res.ForLLM, "writes one note; use knowledge_edit")
			assert.Contains(t, res.ForLLM, op)
		})
	}
}

func TestRestructureTool_RedirectsSchemaAndViewOpsToKnowledgeConfigure(t *testing.T) {
	home, ws, _ := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	for _, op := range []string{"create_record_type", "edit_record_type", "delete_record_type", "write_view", "delete_view"} {
		t.Run(op, func(t *testing.T) {
			res := tool.Execute(a4Ctx("mia", ws), map[string]any{
				"op": op, "collection": "KB", "path": "A.md",
			})
			require.True(t, res.IsError)
			assert.Contains(t, res.ForLLM, "changes what existing notes mean; use knowledge_configure")
			assert.Contains(t, res.ForLLM, op)
		})
	}
}

func TestRestructureTool_RefusesAnUnknownOpNamingTheValidOnes(t *testing.T) {
	home, ws, _ := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"op": "teleport", "collection": "KB", "path": "A.md"})
	require.True(t, res.IsError)
	for _, want := range []string{"rename", "move", "trash", "restore"} {
		assert.Contains(t, res.ForLLM, want)
	}
}

func TestRestructureTool_RefusesAnEmptyOp(t *testing.T) {
	home, ws, _ := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"collection": "KB", "path": "A.md"})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "'op' is required")
}

func TestRestructureTool_RefusesAnUnknownArgument(t *testing.T) {
	home, ws, _ := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "trash", "collection": "KB", "path": "A.md", "typo_field": "oops",
	})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "typo_field")
}

// ---------------------------------------------------------------------------
// Real end-to-end calls through the tool boundary, compact text (FR-072)
// ---------------------------------------------------------------------------

func TestRestructureTool_RenameEndToEnd(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(root, "Old.md"), []byte("# Old\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "body.md"), []byte("See [[Old]].\n"), 0o644))
	deps, rec := a4Deps(home)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "KB", "path": "Old.md", "new_name": "New.md",
	})
	require.False(t, res.IsError, "unexpected refusal: %s", res.ForLLM)
	assert.Contains(t, res.ForLLM, "Old.md -> New.md")
	assert.Contains(t, res.ForLLM, "CASCADE:")
	assert.NotContains(t, res.ForLLM, "{") // never JSON (FR-072)

	assert.FileExists(t, filepath.Join(root, "New.md"))
	assert.NoFileExists(t, filepath.Join(root, "Old.md"))
	got, err := os.ReadFile(filepath.Join(root, "body.md"))
	require.NoError(t, err)
	assert.Equal(t, "See [[New]].\n", string(got))

	require.NotEmpty(t, rec.applied())
}

func TestRestructureTool_TrashAndRestoreEndToEnd(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(root, "Note.md"), []byte("content\n"), 0o644))
	deps, rec := a4Deps(home)
	tool := NewRestructureTool(deps)

	trashRes := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "trash", "collection": "KB", "path": "Note.md",
	})
	require.False(t, trashRes.IsError, "unexpected refusal: %s", trashRes.ForLLM)
	assert.Contains(t, trashRes.ForLLM, "Note.md -> trashed at")
	assert.Contains(t, trashRes.ForLLM, "CASCADE:")
	assert.NoFileExists(t, filepath.Join(root, "Note.md"))

	restoreRes := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "restore", "collection": "KB", "path": "Note.md",
	})
	require.False(t, restoreRes.IsError, "unexpected refusal: %s", restoreRes.ForLLM)
	assert.Contains(t, restoreRes.ForLLM, "Note.md <- restored from trash")
	assert.FileExists(t, filepath.Join(root, "Note.md"))

	// Both applied outcomes are audited under the RESPECTIVE operation name,
	// not collapsed into one.
	applied := rec.applied()
	require.Len(t, applied, 2)
	assert.Equal(t, restructureTrashOp, applied[0].Operation)
	assert.Equal(t, restructureRestoreOp, applied[1].Operation)
}

// ---------------------------------------------------------------------------
// Cross-workspace refusal — the same P0 boundary every mutation shares
// ---------------------------------------------------------------------------

func TestRestructureTool_RefusesAcrossTheWorkspaceBoundary(t *testing.T) {
	home := a4Home(t)
	wsA := a4Workspace(t, home)
	wsB := a4Workspace(t, home)
	root := a4Vault(t, filepath.Join(t.TempDir(), "PrivateKB"), "PrivateKB")
	a4Mount(t, home, wsA, "kb", root)
	require.NoError(t, os.WriteFile(filepath.Join(root, "A.md"), []byte("a"), 0o644))
	// wsB deliberately has no mount at all.

	deps, rec := a4Deps(home)
	tool := NewRestructureTool(deps)

	before := len(rec.records)
	res := tool.Execute(a4Ctx("agent-b", wsB), map[string]any{
		"op": "trash", "collection": "PrivateKB", "path": "A.md",
	})
	require.True(t, res.IsError, "must refuse across the workspace boundary")
	assert.NotContains(t, res.ForLLM, root, "the refusal must not leak the other workspace's collection path")
	require.Greater(t, len(rec.records), before)
	last := rec.records[len(rec.records)-1]
	assert.Equal(t, AuthorOutcomeRefused, last.Outcome)

	// Nothing was touched.
	assert.FileExists(t, filepath.Join(root, "A.md"))

	// Positive control from the owning workspace.
	ok := tool.Execute(a4Ctx("agent-a", wsA), map[string]any{
		"op": "trash", "collection": "PrivateKB", "path": "A.md",
	})
	require.False(t, ok.IsError, "the owning workspace must be able to trash: %s", ok.ForLLM)
}
