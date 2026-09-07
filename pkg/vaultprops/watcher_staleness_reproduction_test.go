// Omnipus — reproduction for the live-UAT defect: "a file indexed by the
// filesystem watcher is not findable by knowledge_find until the gateway
// restarts."
//
// THE REPORTED READING (not what this file confirms): that the bleve TEXT
// reader knowledge_find uses is long-lived/cached and never observes a
// write a separate handle just committed. EVIDENCE 1 and EVIDENCE 2 below
// test that reading directly — the same *knowledge.Index handle that wrote
// the update, and a second, genuinely independent handle reopened from disk
// after the first is fully closed — and both see the write immediately.
// That hypothesis is FALSE: bleve/scorch commits are visible to the writer's
// own handle synchronously, and a disk reopen picks up whatever was last
// committed. There is no reader-staleness bug in pkg/knowledge's text index.
//
// THE REAL MECHANISM: pkg/knowledge's filesystem watcher (watch.go) can only
// ever touch the TEXT index — its own file header states the layering
// reason: this package cannot import pkg/records/propindex, so a watcher
// event has no code path to the properties store at all. knowledge_find's
// `words=` path, though, does not answer from the text index alone once a
// properties store exists: findRecords (pkg/records/knowledgefind/find.go)
// streams its candidate population from d.Store.Candidates (the PROPERTIES
// store) and only keeps the ones also present in the text index's
// `wordPaths` — never the reverse. A note the watcher added to the text
// index but that the properties store has never heard of is therefore
// invisible to the result set no matter how current the text index is,
// and the properties store — once ANY sync has ever run against it — never
// reports itself unbuilt (propindex's needsFull is a one-time snapshot at
// open, not a running truth), so nothing before this file's fix noticed the
// mismatch and the query answered a plain, unrefused, wrong zero. Restarting
// the gateway "fixes" it only because AttachMount runs a full properties
// resync on every mount, which is the one thing a watcher event can never
// trigger for that store.
//
// This is the same defect class F-9B (f9b_typed_only_reproduction_test.go)
// closes for a typed-only query, reached through a different door: an
// EXTERNAL filesystem change relayed by the watcher, not an agent's own
// knowledge_edit write, and a plain `words=` query rather than a typed one.
// find_tool.go's propertiesStoreCoversCollection check — added for F-9B —
// closes this door too, as a side effect of the same coverage comparison:
// once the store is caught stale, a pure `words=` query (kind=note, no
// typed filter — query.textOnlyServable()) falls back to answering directly
// from the text index via textOnlyResponse, which is exactly what makes the
// watcher-indexed note findable without waiting for the next full sync.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWatcherStaleness' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// TestWatcherStaleness_TextHandleSeesItsOwnWriteImmediately is EVIDENCE 1:
// the exact handle that performs a watcher-shaped update (Index.UpdatePath,
// the same call watch.go's applyOne makes on its own long-lived handle)
// finds the new document with no reopen, no sleep, same test. If this were
// red, the reported "reader is long-lived/cached and never reopens"
// hypothesis would be pointing at the right layer; it is not.
func TestWatcherStaleness_TextHandleSeesItsOwnWriteImmediately(t *testing.T) {
	home := syncHome(t)
	root := syncVault(t, map[string]string{"seed.md": "# Seed\n\nnothing interesting.\n"})
	ctx := context.Background()

	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	_, err = ix.Sync(ctx)
	require.NoError(t, err)

	const uniqueTerm = "zqfWatcherEvidenceOneTerm1"
	writeExternalNote(t, root, "external-one.md", uniqueTerm)

	// This is watch.go's applyOne, verbatim: w.ix.UpdatePath(ctx, relPath) on
	// the handle the watcher already holds open.
	require.NoError(t, ix.UpdatePath(ctx, "external-one.md"))

	hits, err := ix.Search(uniqueTerm, 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "the writer's own handle must see its own committed write immediately")
	require.Equal(t, "external-one.md", hits[0].Path)

	require.NoError(t, ix.Close())
}

// TestWatcherStaleness_SeparateReopenedHandleSeesTheWrite is EVIDENCE 2: the
// writer's handle is fully closed (registry refcount to zero — the
// underlying bleve.Index is physically closed, per Index.Close's own doc
// comment) before a SECOND, independently opened handle reads. This is the
// literal "one index handle writes, a separate handle opened over the same
// root searches" scenario. If bleve's scorch reader needed an explicit
// reopen/refresh to observe another writer's committed segments, or if the
// writer's batch were committed without being durable, this would be red.
// It is not: a fresh disk-backed open sees exactly what the closed handle
// last committed.
func TestWatcherStaleness_SeparateReopenedHandleSeesTheWrite(t *testing.T) {
	home := syncHome(t)
	root := syncVault(t, map[string]string{"seed.md": "# Seed\n\nnothing interesting.\n"})
	ctx := context.Background()

	writer, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	_, err = writer.Sync(ctx)
	require.NoError(t, err)

	const uniqueTerm = "zqfWatcherEvidenceTwoTerm2"
	writeExternalNote(t, root, "external-two.md", uniqueTerm)
	require.NoError(t, writer.UpdatePath(ctx, "external-two.md"))

	// Drop the writer's only reference — releaseSharedIndex closes the
	// underlying handle, per Index.Close's own doc comment ("the underlying
	// handle is closed only when the last holder releases it"). The next
	// OpenIndex for this root cannot be the same object; it must reopen from
	// disk.
	require.NoError(t, writer.Close())

	reader, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	hits, err := reader.Search(uniqueTerm, 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "a genuinely separate, disk-reopened handle must see the committed write")
	require.Equal(t, "external-two.md", hits[0].Path)
	require.NoError(t, reader.Close())
}

// TestWatcherStaleness_WordsQueryMustFindTheWatcherIndexedNote is the real
// reproduction: a collection that has been through one real, full sync of
// BOTH indexes (exactly what AttachMount does on mount, and what the
// eleven TestIndexFreshness_* tests never exercise — they all start from a
// never-before-indexed collection). A note then lands on disk the way an
// EXTERNAL change does: nobody calls knowledge_edit, so author.go's
// instant-properties-write never runs, and the ONLY thing that will ever
// notice the file is the filesystem watcher. This test plays the watcher's
// part directly — Index.UpdatePath on a handle, then fully closed, matching
// EVIDENCE 2's shape — and deliberately does NOT touch the properties store
// again, because nothing in production would either.
//
// A real knowledge_find `words=` call, through FindTool.buildDeps exactly as
// production builds it (findViaTool, sync_test.go), must find the note. On
// the pre-F-9B openFindStore (store.NeedsFullIndex() alone), it does not:
// the properties store still holds only "seed.md", NeedsFullIndex() is
// false (it has rows), so it is trusted, and d.Store.Candidates enumerates
// only what the store has — the watcher-indexed note is filtered out before
// ev.visit ever sees it, and the response is an unrefused, wrong zero.
func TestWatcherStaleness_WordsQueryMustFindTheWatcherIndexedNote(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{"seed.md": "# Seed\n\nnothing interesting.\n"})
	ctx := context.Background()

	// One real, full sync of BOTH indexes — the boot-sweep/AttachMount
	// shape — so both start genuinely built and agreeing.
	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	_, err = ix.Sync(ctx)
	require.NoError(t, err)
	require.NoError(t, ix.Close())
	_, err = Sync(ctx, home, root, SyncOptions{})
	require.NoError(t, err)

	// A file appears on disk as an EXTERNAL change would. The filesystem
	// watcher is the only thing that will ever index it — played here by
	// opening a handle, calling UpdatePath (exactly watch.go's applyOne),
	// and closing it, precisely as EVIDENCE 2 already proved is enough for
	// the text index alone to be current and visible to a fresh reopen.
	const uniqueTerm = "zqfWatcherRealReproTerm3"
	writeExternalNote(t, root, "external-watched.md", uniqueTerm)
	watcher, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	require.NoError(t, watcher.UpdatePath(ctx, "external-watched.md"))
	require.NoError(t, watcher.Close())

	// Precondition, stated as evidence rather than assumed: the properties
	// store genuinely was never told about this file. It still holds
	// exactly the one row the initial full sync wrote.
	props := watcherStalenessPropPaths(t, home, root)
	require.NotContains(t, props, "external-watched.md",
		"test precondition failed: the properties store must NOT know about the watcher-indexed "+
			"note yet — a watcher event has no code path to it")
	require.Contains(t, props, "seed.md")

	got := findViaTool(t, home, root, map[string]any{"words": uniqueTerm})

	if strings.Contains(got, "REFUSED") {
		t.Fatalf("knowledge_find refused a plain words= query for a term the text index genuinely "+
			"holds; a refusal here is not the fix — the note must be findable.\ngot: %s", got)
	}
	if !strings.Contains(got, "external-watched.md") {
		t.Fatalf("REPRODUCED: a note the filesystem watcher indexed into the TEXT index is not "+
			"findable by knowledge_find because the PROPERTIES store — never touched by any watcher "+
			"event — still does not know it exists, and the store-driven candidate stream silently "+
			"excluded it from an otherwise unrefused answer.\ngot: %s", got)
	}
}

// writeExternalNote writes a note directly to disk, standing in for a
// change made outside Omnipus entirely (an operator's editor, a sync
// client, git) — the only path that reaches the filesystem watcher and
// never author.go's instant-indexing.
func writeExternalNote(t *testing.T, root, relPath, uniqueTerm string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	body := "# External\n\na note about " + uniqueTerm + " added outside Omnipus.\n"
	require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
}

// watcherStalenessPropPaths opens the properties index directly (never
// through FindTool/openFindStore, so it cannot be affected by whatever fix
// is or is not present there) and returns every path it currently holds —
// the independent oracle for "did the properties store actually change".
func watcherStalenessPropPaths(t *testing.T, home, root string) map[string]bool {
	t.Helper()
	path, err := knowledge.PropertiesIndexPath(home, root)
	require.NoError(t, err)
	if _, statErr := os.Stat(path); statErr != nil {
		require.True(t, os.IsNotExist(statErr), "unexpected stat error for %s: %v", path, statErr)
		return map[string]bool{}
	}
	store, err := propindex.Open(context.Background(), path, propindex.Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	out := map[string]bool{}
	require.NoError(t, store.AllPaths(context.Background(), func(n propindex.IndexedNote) error {
		out[n.Path] = true
		return nil
	}))
	return out
}
