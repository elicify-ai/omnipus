// Omnipus — regression coverage for Finding B: the same shape of defect F-9
// (docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md) had in
// the `words=` path, reproduced and fixed in the TYPED-ONLY path (a `type=`
// filter with no `words=`).
//
// THE DEFECT, CONFIRMED REPRODUCIBLE THROUGH THE REAL TOOL SURFACE (before
// this file's fix landed): pkg/records/propindex/sqlite.go's Index.open sets
// ix.needsFull = (notes == 0) from a single COUNT(*) read at open time, and
// never updates it again for the lifetime of that *Index — it is a snapshot
// of "did this database hold zero rows the instant I opened it", not "has
// this collection ever been fully swept". find_tool.go's openFindStore opens
// a FRESH propindex.Index on every knowledge_find call. So: one
// knowledge_edit create on a collection nobody has ever synced writes
// exactly one row into properties.db (author.go's instant-indexing path,
// proven by pkg/knowledge's own
// TestIndexFreshness_Create_PropertiesIndexRowLandsInstantly). The VERY NEXT
// knowledge_find call opened a fresh Index over that one-row database,
// COUNT(*) read 1, needsFull was false, and the store was kept open and
// trusted for a typed query — even though the other notes on disk, with the
// same declared type, had never reached the properties index at all.
//
// Before the fix, the reproduction below observed:
//
//	COMPLETE: yes — 0 records matched
//
// for a type=deal / status=prospect query over a collection holding three
// untouched "prospect" deals on disk and exactly one indexed "won" deal — a
// confidently wrong, silently incomplete answer.
//
// THE FIX (find_tool.go's openFindStore): after NeedsFullIndex() passes, a
// second check — propertiesStoreCoversCollection — compares the number of
// paths the store actually holds (via AllPaths) against a fresh, stat-only
// knowledge.Scan of the collection, the same "manifest count vs disk scan"
// question findTextSearcher.Populated already asks for the text index. A
// mismatch closes the store and returns nil, so Find() reaches its own
// documented refusal ("the properties index is not open, so no record can
// be read") instead of answering over a fraction of the collection.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestF9B_TypedOnly' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// f9bDealSchema declares a minimal "deal" record type directly under the
// collection's own .omnipus-vault/records/ dir — the same mechanism
// pkg/knowledge/knowledge_edit_test.go's veSchema uses, reused here rather
// than re-derived because a schema file typo would make this test fail for a
// reason unrelated to the defect it guards.
func f9bDealSchema(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, records.RecordsDirName)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	yaml := "schema_version: 1\n" +
		"type: deal\n" +
		"properties:\n" +
		"  status: { type: enum, values: [prospect, won, lost] }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "deal.yaml"), []byte(yaml), 0o600))
}

// f9bDealNote writes a "deal" note directly to disk — never through
// knowledge_edit, never synced — so it exists on disk but has never reached
// either index.
func f9bDealNote(t *testing.T, root, relPath, status string) {
	t.Helper()
	body := "---\n" +
		"type: deal\n" +
		"status: " + status + "\n" +
		"---\n" +
		"# " + relPath + "\n"
	full := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
}

// TestF9B_TypedOnly_OneWriteToNeverSweptCollectionMustNotFakeCompleteCoverage
// is the regression guard: several "deal" notes on disk, no index anywhere
// for this collection (no OpenIndex/Sync, no vaultprops.Sync — nobody has
// ever swept it); exactly ONE knowledge_edit op:create through the real
// EditTool; then a real knowledge_find `type: "deal"` query with NO `words`
// — through the real FindTool — for status "prospect", a status that exists
// ONLY on the untouched, never-indexed notes, never on the one note the
// create wrote. The answer must be a refusal, never a confident result that
// silently omits the three untouched prospect deals.
func TestF9B_TypedOnly_OneWriteToNeverSweptCollectionMustNotFakeCompleteCoverage(t *testing.T) {
	skipWithoutSQLite(t)

	home := f9Home(t)
	ws := f9Workspace(t, home)

	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()

	f9bDealSchema(t, root)

	// Three "deal" notes with status=prospect, on disk, NEVER indexed by
	// anything before the one write below.
	f9bDealNote(t, root, "one.md", "prospect")
	f9bDealNote(t, root, "two.md", "prospect")
	f9bDealNote(t, root, "three.md", "prospect")

	// Precondition: genuinely never indexed — no manifest, and (implicitly)
	// no properties.db yet either, since nothing has opened one.
	dir, derr := knowledge.IndexDirFor(home, root)
	require.NoError(t, derr)
	manifestExists, existsErr := knowledge.ManifestExists(filepath.Join(dir, knowledge.ManifestFileName))
	require.NoError(t, existsErr)
	require.False(t, manifestExists,
		"test precondition failed: a manifest already exists for this collection before any write")

	// ONE knowledge_edit create, through the REAL tool — status "won", which
	// must NOT be confused with the three untouched "prospect" deals above.
	editDeps := knowledge.AuthoringDeps{
		Home:  home,
		Audit: knowledge.AuthorAuditFunc(func(knowledge.AuthorAuditRecord) {}),
	}
	editTool := knowledge.NewEditTool(editDeps)
	editRes := editTool.Execute(f9Ctx("mia", ws), map[string]any{
		"op": "create", "path": "New.md",
		"frontmatter": map[string]any{"type": "deal", "status": "won"},
	})
	require.NotNil(t, editRes)
	require.False(t, editRes.IsError, "the create itself must succeed: %s", editRes.ForLLM)

	// Now the regression check: a type=deal query for status=prospect — a
	// status that lives ONLY in the three untouched notes — through the real
	// FindTool, with NO words argument (the typed-only path).
	findTool := NewFindTool(home)
	findRes := findTool.Execute(f9Ctx("mia", ws), map[string]any{
		"type": "deal",
		"filter": map[string]any{
			"property": "status",
			"op":       "=",
			"value":    "prospect",
		},
	})
	require.NotNil(t, findRes)

	// UAT 2026-09-13 D-02: a store that covers only part of the collection is
	// no longer REFUSED — it is brought up to date by the query itself
	// (openFindStore's self-heal) and then answered over the whole
	// collection. The requirement this test guards is unchanged: the three
	// untouched prospect deals must never be silently omitted.
	require.False(t, findRes.IsError,
		"the query must repair the partially-covering store and answer, not refuse:\n%s", findRes.ForLLM)
	for _, path := range []string{"one.md", "two.md", "three.md"} {
		require.Contains(t, findRes.ForLLM, path,
			"FINDING B: an untouched prospect deal was silently omitted\ngot: %s", findRes.ForLLM)
	}
	require.NotContains(t, findRes.ForLLM, "New.md", "the won deal must not match status=prospect")
	// This test never builds the TEXT index, so the answer honestly reports
	// each row's freshness as unknown (COMPLETE: no, per-row problems) — that
	// disclosure is correct and is not the silent omission FINDING B was.
	require.NotContains(t, findRes.ForLLM, "REFUSED")
	t.Logf("confirmed fixed: the drifted store was repaired and the answer covers every deal:\n%s", findRes.ForLLM)
}

// TestF9B_TypedOnly_WordMissMustNotBypassPropertiesIndexRefusal is F1's own
// regression: a typed query (`type=deal`, `filter=status=prospect`) that
// NEEDS the properties index, PLUS a `words` argument that matches NOTHING
// in the vault, over a collection whose TEXT index is fully synced (so
// checkTextIndexPopulated has nothing to refuse) but whose PROPERTIES index
// was never opened at all (no knowledge_edit write, no vaultprops.Sync —
// properties.db does not exist, so openFindStore's plain os.Stat miss
// returns Store=nil, the ordinary "never indexed" state).
//
// Before F1's fix, pkg/records/knowledgefind/find.go's word-search half ran
// FIRST and, on zero word hits, returned findRecords's zeroHitResponse
// directly (find.go:578-592) — a code path that sits BEFORE the d.Store ==
// nil guard at find.go:620, and before textOnlyServable() is ever consulted
// for this query (it needs a typed filter, so it is not text-only
// servable). So this query answered "COMPLETE: yes — 0 records matched"
// instead of refusing that the properties index is not open — even though
// the query cannot be answered without it. Change `words` to a term that
// DOES match (e.g. drop it) and the identical query correctly refuses
// today; the verdict must not depend on whether the word half happened to
// match.
func TestF9B_TypedOnly_WordMissMustNotBypassPropertiesIndexRefusal(t *testing.T) {
	skipWithoutSQLite(t)

	home := f9Home(t)
	ws := f9Workspace(t, home)

	col, err := knowledge.CreateInWorkspace(home, ws, "kb", knowledge.Marker{DisplayName: "KB"})
	require.NoError(t, err)
	root := col.Root()

	f9bDealSchema(t, root)

	// Three "deal" notes with status=prospect, on disk.
	f9bDealNote(t, root, "one.md", "prospect")
	f9bDealNote(t, root, "two.md", "prospect")
	f9bDealNote(t, root, "three.md", "prospect")

	// The TEXT index is fully synced over the whole collection — the same
	// call TestFindTool_AttachmentKindHonouredBeforePropertiesIndexSynced
	// uses — so checkTextIndexPopulated (find.go) sees a fully-covered
	// manifest and has nothing of its own to refuse. This is what isolates
	// F1 from the text-index-freshness refusal: only the PROPERTIES index
	// is missing here.
	ix, err := knowledge.OpenIndex(home, root)
	require.NoError(t, err)
	defer func() { _ = ix.Close() }()
	_, err = knowledge.SyncTracked(context.Background(), ix,
		knowledge.SharedProgressTracker(root), knowledge.SyncOptions{})
	require.NoError(t, err)

	// NO knowledge_edit write and NO vaultprops.Sync call: properties.db
	// never exists for this collection, so openFindStore's os.Stat miss
	// hands Find() a nil Store — see openFindStore's own doc comment
	// ("Never indexed ... the ordinary state of a collection nobody has
	// run check_integrity/indexing against yet").
	dir, derr := knowledge.IndexDirFor(home, root)
	require.NoError(t, derr)
	propsPath, perr := knowledge.PropertiesIndexPath(home, root)
	require.NoError(t, perr)
	_, statErr := os.Stat(propsPath)
	require.True(t, os.IsNotExist(statErr),
		"test precondition failed: properties.db already exists at %s (dir=%s)", propsPath, dir)

	// The regression check: a type=deal/status=prospect query — which
	// NEEDS the properties index — plus a `words` argument that matches
	// nothing on disk. F1's bug made this flip from refusal to a confident
	// zero purely because the word half missed.
	findTool := NewFindTool(home)
	findRes := findTool.Execute(f9Ctx("mia", ws), map[string]any{
		"type": "deal",
		"filter": map[string]any{
			"property": "status",
			"op":       "=",
			"value":    "prospect",
		},
		"words": "zzz-nomatch-nqxv-f1-regression",
	})
	require.NotNil(t, findRes)

	// UAT 2026-09-13 D-02: a never-built properties index is built by the
	// query itself, so the word-miss is now answered as an honest, complete
	// zero over a store that covers the whole collection. F1's principle is
	// intact: the verdict does not depend on whether the word half matched —
	// both halves now see the same, repaired store.
	require.False(t, findRes.IsError,
		"the query must build the missing store and answer, not refuse:\n%s", findRes.ForLLM)
	require.NotContains(t, findRes.ForLLM, "the properties index is not open")
	require.Contains(t, findRes.ForLLM, "0 records matched", findRes.ForLLM)
	_, statAfter := os.Stat(propsPath)
	require.NoError(t, statAfter, "the query must have built the properties index on demand")
	t.Logf("confirmed: word-miss answered an honest zero over an on-demand-built store:\n%s", findRes.ForLLM)
}
