// Omnipus — regression coverage for the 2026-09-13 UAT finding D-06:
// malformed frontmatter and non-conforming values were detected by the
// indexer and then discarded, so knowledge_find served the notes as healthy
// and check_integrity named them only as orphans.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

func d06Collection(t *testing.T, home, ws string) string {
	t.Helper()
	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	schema := "schema_version: 1\n" +
		"type: project\n" +
		"properties:\n" +
		"  status: { type: enum, values: [active, done] }\n" +
		"  budget: { type: integer }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(schema), 0o600))

	// B-41's two notes, written directly (never through knowledge_edit).
	f9Note(t, root, "Projects/Broken FM.md", "---\ntype: project\nstatus: active\n\n# Broken FM\n\nbrokenfmmarker body\n")
	f9Note(t, root, "Projects/Bad Budget.md", "---\ntype: project\nstatus: active\nbudget: lots\n---\n# Bad Budget\n")
	f9Note(t, root, "Projects/Good.md", "---\ntype: project\nstatus: active\nbudget: 100\n---\n# Good\n")

	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	_, err = knowledge.SyncTracked(context.Background(), ix,
		knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
	require.NoError(t, err)
	require.NoError(t, ix.Close())
	_, err = Sync(context.Background(), home, root, SyncOptions{})
	require.NoError(t, err)
	return root
}

// TestUAT_D06_FindNamesMalformedFrontmatterAndNonConformingValues — the
// rows are still returned, and problems[] names each file for what is wrong
// with it.
func TestUAT_D06_FindNamesMalformedFrontmatterAndNonConformingValues(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	d06Collection(t, home, ws)
	tool := NewFindTool(home)

	typed := tool.Execute(f9Ctx("mia", ws), map[string]any{"type": "project"})
	require.NotNil(t, typed)
	require.False(t, typed.IsError, typed.ForLLM)
	require.Contains(t, typed.ForLLM, "Projects/Bad Budget.md", "the row is still returned")
	require.Contains(t, typed.ForLLM, "COMPLETE: no", "a non-conforming value must move the verdict:\n%s", typed.ForLLM)
	require.Contains(t, typed.ForLLM, "Bad Budget.md: property budget", "problems[] must name the file and the property:\n%s", typed.ForLLM)
	require.NotContains(t, typed.ForLLM, "Good.md: property", "a healthy note must not be flagged:\n%s", typed.ForLLM)

	words := tool.Execute(f9Ctx("mia", ws), map[string]any{"words": "brokenfmmarker"})
	require.NotNil(t, words)
	require.False(t, words.IsError, words.ForLLM)
	require.Contains(t, words.ForLLM, "Projects/Broken FM.md")
	require.Contains(t, words.ForLLM, "the frontmatter could not be read",
		"D-06: the unclosed fence must be named, not served as a healthy note:\n%s", words.ForLLM)
}

// TestUAT_D06_CheckIntegrityHasCategoriesForBoth — B-41: the sweep named the
// two notes only as orphans.
func TestUAT_D06_CheckIntegrityHasCategoriesForBoth(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	d06Collection(t, home, ws)

	describe := knowledge.NewDescribeTool(knowledge.ToolDeps{Home: home}, Open)
	res := describe.Execute(f9Ctx("mia", ws), map[string]any{"check_integrity": true, "detail": "full"})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	out := res.ForLLM
	require.Contains(t, out, "malformed frontmatter", "check_integrity needs a category for the unclosed fence:\n%s", out)
	require.Contains(t, out, "non-conforming value", "check_integrity needs a category for the bad value:\n%s", out)
	require.True(t, strings.Contains(out, "Projects/Broken FM.md — the frontmatter could not be read"), out)
	require.True(t, strings.Contains(out, "Projects/Bad Budget.md — project.budget"), out)
}
