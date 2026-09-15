// Omnipus — regression coverage for the 2026-09-14 re-test of UAT D-06 (rows
// B-41, Q-02): an unclosed frontmatter fence whose "body" prose happens to
// parse as valid YAML was still served everywhere as a normal, healthy
// record. Three earlier fix rounds (FIX-REPORT-index-search.md,
// FIX2-REPORT-index-find.md, FIX3-REPORT-knowledge-edit-2.md) did not catch
// this shape because their own regression fixture's "body" text
// ("# Broken FM" / "brokenfmmarker body") has no colon and fails YAML syntax
// on its own — so it was already caught by the pre-existing "not valid YAML"
// branch. The real UAT fixture's prose,
// "Broken frontmatter: never closed with ---.", DOES contain a colon and
// parses as a legitimate-looking third property, which is exactly the gap
// this file exercises with the REAL fixture bytes (copied verbatim from
// uat/home/workspaces/01M2CXCBZC63H936J1H8EV86XD/work/UAT Vault/Projects/
// Broken FM.md).
//
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

// unclosedFenceFixtureBody is the ON-DISK UAT fixture's exact bytes.
const unclosedFenceFixtureBody = "---\ntype: project\nstatus: active\n\nBroken frontmatter: never closed with ---.\n"

func unclosedFenceCollection(t *testing.T, home, ws string) string {
	t.Helper()
	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	schema := "schema_version: 1\n" +
		"type: project\n" +
		"properties:\n" +
		"  status: { type: enum, values: [active, done] }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.yaml"), []byte(schema), 0o600))

	f9Note(t, root, "Projects/Broken FM.md", unclosedFenceFixtureBody)
	f9Note(t, root, "Projects/Good.md", "---\ntype: project\nstatus: active\n---\n# Good\n")

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

// TestUAT20260914_FindDoesNotServeUnclosedFenceAsAValidRecord is Q-02: a
// `type: project` query must not return the malformed note as a normal
// active project, and must name the problem instead.
func TestUAT20260914_FindDoesNotServeUnclosedFenceAsAValidRecord(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	unclosedFenceCollection(t, home, ws)
	tool := NewFindTool(home)

	typed := tool.Execute(f9Ctx("mia", ws), map[string]any{"type": "project"})
	require.NotNil(t, typed)
	require.False(t, typed.IsError, typed.ForLLM)
	require.Contains(t, typed.ForLLM, "Projects/Good.md", "the healthy record is still returned:\n%s", typed.ForLLM)
	require.NotContains(t, typed.ForLLM, "Projects/Broken FM.md", "D-06/Q-02: the malformed note must not be served as a valid `type: project` record:\n%s", typed.ForLLM)

	// The words search still reaches every note on disk, malformed or not
	// (BuildNoteRows stores an unconditional `notes` row — FR-021e), so the
	// note itself must still be findable, with the problem named.
	words := tool.Execute(f9Ctx("mia", ws), map[string]any{"words": "Broken frontmatter"})
	require.NotNil(t, words)
	require.False(t, words.IsError, words.ForLLM)
	require.Contains(t, words.ForLLM, "Projects/Broken FM.md", words.ForLLM)
	require.Contains(t, strings.ToLower(words.ForLLM), "could not be read",
		"D-06/Q-02: problems[] must name the unclosed fence:\n%s", words.ForLLM)
}

// TestUAT20260914_CheckIntegrityCategorisesUnclosedFenceAsMalformed is
// B-41: the sweep must not name the note as an orphan-only finding — it
// must appear under the malformed-frontmatter category.
func TestUAT20260914_CheckIntegrityCategorisesUnclosedFenceAsMalformed(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	unclosedFenceCollection(t, home, ws)

	describe := knowledge.NewDescribeTool(knowledge.ToolDeps{Home: home}, Open)
	res := describe.Execute(f9Ctx("mia", ws), map[string]any{"check_integrity": true, "detail": "full"})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	out := res.ForLLM
	require.Contains(t, out, "malformed frontmatter", "B-41: check_integrity needs a category for the unclosed fence:\n%s", out)
	require.True(t, strings.Contains(out, "Projects/Broken FM.md"), out)
	require.True(t, strings.Contains(out, "could not be read"), out)
}
