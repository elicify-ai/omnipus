// Omnipus — tests for knowledge_list (KB-2a,
// defect-list-knowledge-base-ux-2026-09-08.md, founder-ratified 2026-09-08).
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKnowledgeList_NoCollectionsInScope is the empty-scope case: nothing
// mounted, nothing created — the tool must say so plainly rather than
// erroring or rendering an empty "KNOWLEDGE BASES in scope (0):" header.
func TestKnowledgeList_NoCollectionsInScope(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	tool := NewListTool(ToolDeps{Home: home})

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{})
	require.False(t, res.IsError, res.ForLLM)
	assert.Equal(t, "No knowledge base is available in this workspace.\n", res.ForLLM)
}

// TestKnowledgeList_ListsCollectionsByName is the direct answer to KB-2: an
// agent that knows NO collection name yet still learns both names — one
// reached via a mount, one created inside the workspace's own work tree —
// with no `collection` argument to guess.
func TestKnowledgeList_ListsCollectionsByName(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	mounted := a4Vault(t, filepath.Join(t.TempDir(), "Mounted"), "Mounted Vault")
	a4Mount(t, home, ws, "notes", mounted)
	_, err := CreateInWorkspace(home, ws, "Native Vault", Marker{DisplayName: "Native Vault"})
	require.NoError(t, err)

	tool := NewListTool(ToolDeps{Home: home})
	res := tool.Execute(a4Ctx("mia", ws), map[string]any{})
	require.False(t, res.IsError, res.ForLLM)

	assert.Contains(t, res.ForLLM, "KNOWLEDGE BASES in scope (2):")
	assert.Contains(t, res.ForLLM, "Mounted Vault")
	assert.Contains(t, res.ForLLM, "Native Vault")
}

// TestKnowledgeList_ScopedToCallingWorkspace is US-9 applied to discovery:
// two workspaces, each with its own knowledge base, must never see the
// other's name.
func TestKnowledgeList_ScopedToCallingWorkspace(t *testing.T) {
	home := a4Home(t)
	wsA := a4Workspace(t, home)
	wsB := a4Workspace(t, home)
	vaultA := a4Vault(t, filepath.Join(t.TempDir(), "vault-a"), "Alpha Vault")
	vaultB := a4Vault(t, filepath.Join(t.TempDir(), "vault-b"), "Beta Vault")
	a4Mount(t, home, wsA, "notes", vaultA)
	a4Mount(t, home, wsB, "notes", vaultB)

	tool := NewListTool(ToolDeps{Home: home})

	resA := tool.Execute(a4Ctx("mia", wsA), map[string]any{})
	require.False(t, resA.IsError, resA.ForLLM)
	assert.Contains(t, resA.ForLLM, "Alpha Vault")
	assert.NotContains(t, resA.ForLLM, "Beta Vault")

	resB := tool.Execute(a4Ctx("mia", wsB), map[string]any{})
	require.False(t, resB.IsError, resB.ForLLM)
	assert.Contains(t, resB.ForLLM, "Beta Vault")
	assert.NotContains(t, resB.ForLLM, "Alpha Vault")
}

// TestKnowledgeList_FreshnessMatchesDescribe is the "reuse, do not
// recompute" requirement checked directly: for the SAME collection, in the
// SAME state, knowledge_list's freshness clause must be the exact substring
// knowledge_describe's own "KNOWLEDGE BASE <name> — <freshness>" line
// carries — because both call indexFreshness over the same DescribeData
// shape, never two independent renderings of what "fresh" means.
func TestKnowledgeList_FreshnessMatchesDescribe(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	root := a4Vault(t, filepath.Join(t.TempDir(), "vault"), "Indexed Vault")
	a4Note(t, root, "one.md", "# One\n\nsomething\n")
	a4Mount(t, home, ws, "notes", root)
	b5Index(t, home, root) // real OpenIndex + Sync, exactly as the indexing lifecycle does.

	listTool := NewListTool(ToolDeps{Home: home})
	listRes := listTool.Execute(a4Ctx("mia", ws), map[string]any{})
	require.False(t, listRes.IsError, listRes.ForLLM)

	describeTool := NewDescribeTool(ToolDeps{Home: home}, nil)
	describeRes := describeTool.Execute(a4Ctx("mia", ws), map[string]any{"collection": "Indexed Vault"})
	require.False(t, describeRes.IsError, describeRes.ForLLM)

	freshness := listCollectionFreshness(ToolDeps{Home: home}, root)
	assert.Contains(t, listRes.ForLLM, "Indexed Vault — "+freshness,
		"knowledge_list's own line must carry the SAME freshness clause it computed")
	assert.Contains(t, describeRes.ForLLM, freshness,
		"knowledge_describe must render the identical freshness wording for the same collection state")
}

// TestKnowledgeList_UnknownArgumentIsRefused: knowledge_list takes no
// arguments at all — anything supplied is refused, not silently ignored.
func TestKnowledgeList_UnknownArgumentIsRefused(t *testing.T) {
	home := a4Home(t)
	ws := a4Workspace(t, home)
	tool := NewListTool(ToolDeps{Home: home})

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{"collection": "whatever"})
	require.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "unknown argument")
}

// TestKnowledgeList_ToolIdentity pins the registered name and basic
// tool-picker surface.
func TestKnowledgeList_ToolIdentity(t *testing.T) {
	tool := NewListTool(ToolDeps{})
	assert.Equal(t, "knowledge_list", tool.Name())
	assert.NotEmpty(t, tool.Description())
	assert.Equal(t, "object", tool.Parameters()["type"])
}
