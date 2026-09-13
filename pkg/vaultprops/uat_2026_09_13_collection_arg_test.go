// Omnipus — regression coverage for the 2026-09-13 UAT findings D-46 (#698)
// and D-57 at the tool door: with two knowledge bases in one workspace every
// knowledge_find was refused with "knowledge_find has NO 'collection'
// argument", and knowledge_describe worded the same condition differently,
// rendering the missing name as `""`.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// d46TwoVaults mounts two knowledge bases into one workspace, each with one
// distinctively named note, both indexed.
func d46TwoVaults(t *testing.T, home, ws string) {
	t.Helper()
	for _, v := range []struct{ rel, name, note string }{
		{"first", "First Vault", "Alpha.md"},
		{"second", "Second Vault", "Beta.md"},
	} {
		col, err := knowledge.CreateInWorkspace(home, ws, v.rel, knowledge.Marker{DisplayName: v.name})
		require.NoError(t, err)
		root := col.Root()
		f9Note(t, root, v.note, "# "+strings.TrimSuffix(v.note, ".md")+"\n\nshared marker word\n")
		ix, err := knowledge.OpenIndex(home, root)
		require.NoError(t, err)
		_, err = knowledge.SyncTracked(context.Background(), ix, knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
		require.NoError(t, err)
		require.NoError(t, ix.Close())
		_, err = Sync(context.Background(), home, root, SyncOptions{})
		require.NoError(t, err)
	}
}

// TestUAT_D46_FindTakesACollectionArgument — B-43: the refusal used to say
// the argument did not exist.
func TestUAT_D46_FindTakesACollectionArgument(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	d46TwoVaults(t, home, ws)
	tool := NewFindTool(home)

	// Ambiguous: two in scope, none named.
	res := tool.Execute(f9Ctx("mia", ws), map[string]any{"words": "shared"})
	require.NotNil(t, res)
	require.True(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "First Vault")
	require.Contains(t, res.ForLLM, "Second Vault")
	require.Contains(t, res.ForLLM, "Name one with the `collection` argument", res.ForLLM)
	require.NotContains(t, res.ForLLM, "NO `collection` argument", "D-46: the argument exists now")

	// Named: the query runs against that knowledge base only, with provenance.
	res = tool.Execute(f9Ctx("mia", ws), map[string]any{"words": "shared", "collection": "Second Vault"})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "COLLECTION: Second Vault")
	require.Contains(t, res.ForLLM, "Beta.md")
	require.NotContains(t, res.ForLLM, "Alpha.md", "the other vault must not leak in")

	// Named but not in scope: refused listing the names that are.
	res = tool.Execute(f9Ctx("mia", ws), map[string]any{"words": "shared", "collection": "Third Vault"})
	require.NotNil(t, res)
	require.True(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, `no knowledge base "Third Vault" is mounted`)
	require.Contains(t, res.ForLLM, "First Vault, Second Vault")
}

// TestUAT_D57_FindAndDescribeWordTheAmbiguousScopeIdentically — the same
// condition, the same sentence, and never an empty quoted name.
func TestUAT_D57_FindAndDescribeWordTheAmbiguousScopeIdentically(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	d46TwoVaults(t, home, ws)

	find := NewFindTool(home).Execute(f9Ctx("mia", ws), map[string]any{"words": "shared"})
	describe := knowledge.NewDescribeTool(knowledge.ToolDeps{Home: home}, Open).Execute(f9Ctx("mia", ws), map[string]any{})
	require.NotNil(t, find)
	require.NotNil(t, describe)
	require.True(t, find.IsError)
	require.True(t, describe.IsError)

	require.NotContains(t, describe.ForLLM, `""`, "D-57: describe must not render the missing name as an empty string:\n%s", describe.ForLLM)
	stripTool := func(s string) string {
		s = strings.TrimPrefix(s, "knowledge_find: ")
		return strings.TrimPrefix(s, "knowledge_describe: ")
	}
	require.Equal(t, stripTool(find.ForLLM), stripTool(describe.ForLLM),
		"D-57: both tools must word the same condition the same way")
}
