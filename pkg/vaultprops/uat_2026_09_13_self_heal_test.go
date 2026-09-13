// Omnipus — regression coverage for the 2026-09-13 UAT finding D-02 (the
// properties index "closes" after any out-of-band change and only a restart
// recovers it), and the end-to-end shape of D-01 / D-07 through the real
// knowledge_find tool.
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
)

// d02Collection builds a synced collection with three "deal" notes and
// returns its root.
func d02Collection(t *testing.T, home, ws string) string {
	t.Helper()
	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()
	f9bDealSchema(t, root)
	f9bDealNote(t, root, "one.md", "prospect")
	f9bDealNote(t, root, "two.md", "prospect")
	f9bDealNote(t, root, "three.md", "won")
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

// d02SyncTextOnly does what the filesystem watcher does within seconds of an
// out-of-band write — brings the TEXT index up to date — and deliberately
// leaves the properties index alone, which is the drift D-02 is about.
func d02SyncTextOnly(t *testing.T, home, root string) {
	t.Helper()
	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	_, err = knowledge.SyncTracked(context.Background(), ix,
		knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
	require.NoError(t, err)
	require.NoError(t, ix.Close())
}

// TestUAT_D02_TypedQuerySelfHealsAfterOutOfBandWrite — onset 2 of the UAT
// campaign: a note written by `write_file` (never through knowledge_edit,
// never synced) left the properties index one row short, after which every
// typed query was refused with "the properties index is not open" until a
// gateway restart. The query must instead bring the index up to date and
// answer, with the new note included.
func TestUAT_D02_TypedQuerySelfHealsAfterOutOfBandWrite(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	root := d02Collection(t, home, ws)

	// Out-of-band: a fourth deal appears on disk; the watcher picks it up
	// for the text index, nothing syncs the properties index.
	f9bDealNote(t, root, "four.md", "prospect")
	d02SyncTextOnly(t, home, root)

	res := NewFindTool(home).Execute(f9Ctx("mia", ws), map[string]any{
		"type":   "deal",
		"filter": map[string]any{"property": "status", "op": "=", "value": "prospect"},
	})
	require.NotNil(t, res)
	require.False(t, res.IsError,
		"D-02: the query must repair the drifted index and answer, not refuse:\n%s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "the properties index is not open")
	for _, path := range []string{"one.md", "two.md", "four.md"} {
		require.Contains(t, res.ForLLM, path, "every prospect deal, including the unsynced one, must be returned:\n%s", res.ForLLM)
	}
	require.Contains(t, res.ForLLM, "COMPLETE: yes", res.ForLLM)
}

// TestUAT_D02_TypedQuerySelfHealsAfterOutOfBandDelete — onset 3: a folder
// removed from under the collection left the store holding rows for files
// that no longer exist, closing the index the same way.
func TestUAT_D02_TypedQuerySelfHealsAfterOutOfBandDelete(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	root := d02Collection(t, home, ws)

	require.NoError(t, os.Remove(filepath.Join(root, "two.md")))
	d02SyncTextOnly(t, home, root)

	res := NewFindTool(home).Execute(f9Ctx("mia", ws), map[string]any{"type": "deal"})
	require.NotNil(t, res)
	require.False(t, res.IsError, "D-02: a deleted file must not close the index:\n%s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "two.md", "the removed note must not be served from a stale row:\n%s", res.ForLLM)
	require.Contains(t, res.ForLLM, "one.md")
	require.Contains(t, res.ForLLM, "three.md")
}

// TestUAT_D01_WordsQueryBeforeAnySyncIsServedWithIndexState — the
// words-only path used to answer from the text index alone whenever the
// properties index was not open, silently omitting the INDEX line. After the
// self-heal the store is built on demand and the answer carries the same
// freshness line every healthy answer does.
func TestUAT_D01_WordsQueryBeforeAnySyncIsServedWithIndexState(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()
	f9Note(t, root, "Projects/Bash Written.md", "# Bash Written\n\nbashwrittenmarker\n")
	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	_, err = knowledge.SyncTracked(context.Background(), ix,
		knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
	require.NoError(t, err)
	require.NoError(t, ix.Close())
	// Deliberately NO properties-index Sync.

	res := NewFindTool(home).Execute(f9Ctx("mia", ws), map[string]any{"words": "bashwrittenmarker"})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "Projects/Bash Written.md")
	require.Contains(t, res.ForLLM, "INDEX:",
		"a words answer on a SQLite build must carry the INDEX line — its absence was D-01's only tell:\n%s", res.ForLLM)
}

// TestUAT_D07_MultiWordRelaxationIsDeclaredThroughTheRealTool — B-40b probe 5
// end to end: one word present, one absent, through the real text index.
func TestUAT_D07_MultiWordRelaxationIsDeclaredThroughTheRealTool(t *testing.T) {
	skipWithoutSQLite(t)
	home := f9Home(t)
	ws := f9Workspace(t, home)
	root := d02Collection(t, home, ws)
	f9Note(t, root, "Probe.md", "# Probe\n\nCollision probe body\n")
	d02SyncTextOnly(t, home, root)

	res := NewFindTool(home).Execute(f9Ctx("mia", ws), map[string]any{"words": "Collision zzqqxx"})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	require.Contains(t, res.ForLLM, "Probe.md", "the near match is still returned")
	require.Contains(t, res.ForLLM, "COMPLETE: no", "a relaxed answer must not read as exact:\n%s", res.ForLLM)
	require.Contains(t, res.ForLLM, "zzqqxx: 0", "the missing word must be named with its count:\n%s", res.ForLLM)
	require.True(t, strings.Contains(res.ForLLM, "collision: 1"), res.ForLLM)
}
