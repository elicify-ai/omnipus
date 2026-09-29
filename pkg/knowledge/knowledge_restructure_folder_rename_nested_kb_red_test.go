// Omnipus — FR-VA-034 / TDD row 83: the agent folder rename refuses
// to carry a nested KB with pipeline-tracked views along with the parent.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRestructureRename_RefusesSameCollectionFolderMoveContainingNestedKBWithTrackedViews(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)
	outer := filepath.Join(root, "Outer")
	require.NoError(t, os.MkdirAll(outer, 0o755))
	nested := filepath.Join(outer, "NestedKB")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	a4Vault(t, nested, "Nested KB")
	plantKnowledgeTrackedView(t, home, nested, "Projects.base", "Open.view")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "KB", "path": "Outer", "new_name": "Outer2", "folder": true,
	})
	require.True(t, res.IsError, "FR-VA-034: moving a nested KB with tracked views must be refused: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "Outer/NestedKB/Open.view", "the refusal must name the tracked path")
	require.DirExists(t, nested)
	require.FileExists(t, filepath.Join(nested, "Open.view"))
	require.NoDirExists(t, filepath.Join(root, "Outer2"))
	require.Equal(t, "Open.view", loadKnowledgeMembers(t, home, nested).Bases["Projects.base"]["open"])
}
