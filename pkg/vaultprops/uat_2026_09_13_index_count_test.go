// Omnipus — regression coverage for the 2026-09-13 UAT finding D-34 through
// the real knowledge_describe tool: "index holds 39 of 22 notes on disk".
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// TestUAT_D34_DescribeCountsNotesNotFiles — two notes and two attachments
// must read as two notes, whether or not check_integrity walked the disk.
func TestUAT_D34_DescribeCountsNotesNotFiles(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()
	f9Note(t, root, "Notes/A.md", "# A\n")
	f9Note(t, root, "Notes/B.md", "# B\n")
	f9Note(t, root, "Assets/one.png", "png\n")
	f9Note(t, root, "Assets/two.pdf", "pdf\n")
	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	_, err = knowledge.SyncTracked(context.Background(), ix, knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
	require.NoError(t, err)
	require.NoError(t, ix.Close())

	describe := knowledge.NewDescribeTool(knowledge.ToolDeps{Home: home}, Open)
	for _, args := range []map[string]any{
		{},
		{"check_integrity": true, "detail": "full"},
	} {
		res := describe.Execute(f9Ctx("mia", ws), args)
		require.NotNil(t, res)
		require.False(t, res.IsError, res.ForLLM)
		out := res.ForLLM
		require.Regexp(t, regexp.MustCompile(`index holds 2 notes`), out,
			"D-34: two attachments must not be counted as notes (args %v):\n%s", args, out)
		require.NotRegexp(t, regexp.MustCompile(`index holds 4`), out, "args %v:\n%s", args, out)
		require.NotContains(t, out, "of 2 notes on disk", "a count above the notes on disk is the D-34 contradiction:\n%s", out)
	}
}
