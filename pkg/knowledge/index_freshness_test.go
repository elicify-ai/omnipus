// Omnipus — tests for the direct-update index-freshness path (requirement 1
// of docs/internal/design/knowledge-index-freshness.md): a write through
// knowledge_edit or knowledge_restructure must make the text index and the
// properties index current BEFORE the tool returns.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// Fixtures local to this file. Prefixed ifx (Index FreshneSs) so they cannot
// collide with the a4* helpers other test files in this package define —
// lifecycle_test.go's own header states the same convention.
// ---------------------------------------------------------------------------

// ifxSearchTool returns the registered knowledge_search tool over home.
func ifxSearchTool(t *testing.T, home string) tools.Tool {
	t.Helper()
	for _, tl := range RetrievalTools(ToolDeps{Home: home}) {
		if tl.Name() == "knowledge_search" {
			return tl
		}
	}
	t.Fatal("knowledge_search is not registered by RetrievalTools")
	return nil
}

// ifxSearchPaths runs one search and returns the matching paths, in the
// order the tool ranked them.
func ifxSearchPaths(t *testing.T, tool tools.Tool, ctx context.Context, collection, query string) []string {
	t.Helper()
	res := tool.Execute(ctx, map[string]any{"collection": collection, "query": query})
	require.False(t, res.IsError, "knowledge_search failed: %s", res.ForLLM)
	payload := a4Payload(t, res)
	raw, _ := payload["results"].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		require.True(t, ok, "malformed search hit: %#v", r)
		p, _ := m["path"].(string)
		out = append(out, p)
	}
	return out
}

// ifxPropPaths opens the properties index directly and returns every row it
// currently holds, keyed by path — the oracle for "did the properties index
// actually change", independent of anything knowledge_edit/knowledge_restructure
// themselves report.
func ifxPropPaths(t *testing.T, home, root string) map[string]propindex.IndexedNote {
	t.Helper()
	p, err := PropertiesIndexPath(home, root)
	require.NoError(t, err)
	if _, statErr := os.Stat(p); statErr != nil {
		// Nothing has ever been written to this collection through the direct
		// path yet, so its properties.db (and even its parent directory) does
		// not exist — a legitimately empty index, not an error. propindex.Open
		// does not create missing PARENT directories (only the file itself),
		// so calling it here would fail for a reason that has nothing to do
		// with what this helper is answering.
		require.True(t, os.IsNotExist(statErr), "unexpected stat error for %s: %v", p, statErr)
		return map[string]propindex.IndexedNote{}
	}
	store, err := propindex.Open(context.Background(), p, propindex.Options{})
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	out := map[string]propindex.IndexedNote{}
	require.NoError(t, store.AllPaths(context.Background(), func(n propindex.IndexedNote) error {
		out[n.Path] = n
		return nil
	}))
	return out
}

// ---------------------------------------------------------------------------
// The load-bearing test: read-your-own-write, no sleep, same test.
// ---------------------------------------------------------------------------

// TestIndexFreshness_Create_IsInstantlySearchable is the test the whole
// feature exists to make pass: a note created through knowledge_edit must be
// findable through knowledge_search in the SAME test, with no sleep and no
// call to Sync in between. The precondition (searching for the token BEFORE
// the note exists returns nothing) is asserted first, so a trivially-true
// "found" is not mistaken for proof.
func TestIndexFreshness_Create_IsInstantlySearchable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	_ = root
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	searchTool := ifxSearchTool(t, home)
	ctx := a4Ctx("mia", ws)

	const token = "Zqxvthud7Freshness"

	// PRECONDITION: nothing has been written yet, so the token must not be
	// findable. Asserted before the write, not inferred.
	require.Empty(t, ifxSearchPaths(t, searchTool, ctx, "kb", token),
		"the token must not be findable before the note that contains it exists")

	res := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Note.md",
		"body": "Some prose containing " + token + " once.\n",
	})
	require.False(t, res.IsError, "create refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index refresh problem: %s", res.ForLLM)

	// LOAD-BEARING ASSERTION: the very next call, same test, no sleep.
	paths := ifxSearchPaths(t, searchTool, ctx, "kb", token)
	require.Equal(t, []string{"Note.md"}, paths,
		"a note just created through knowledge_edit must be found by knowledge_search immediately")
}

// ---------------------------------------------------------------------------
// Every knowledge_edit op, each proven with the same read-your-own-write
// pattern — the token exists only after the op runs.
// ---------------------------------------------------------------------------

func TestIndexFreshness_SetProperty_IsInstantlySearchable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	searchTool := ifxSearchTool(t, home)
	ctx := a4Ctx("mia", ws)

	const token = "Wqrfsz2ProspectValue"
	require.False(t, editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Deal.md", "body": "A deal.\n",
	}).IsError)

	require.Empty(t, ifxSearchPaths(t, searchTool, ctx, "kb", token),
		"the token must not be findable before it is set")

	v := a4Version(t, root, "Deal.md")
	res := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "set_property", "path": "Deal.md",
		"property": "status", "value": token, "expect_version": v,
	})
	require.False(t, res.IsError, "set_property refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index refresh problem: %s", res.ForLLM)

	require.Equal(t, []string{"Deal.md"}, ifxSearchPaths(t, searchTool, ctx, "kb", token),
		"a property value just set must be findable immediately — the text index's prop_value field")
}

func TestIndexFreshness_AppendSection_IsInstantlySearchable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	searchTool := ifxSearchTool(t, home)
	ctx := a4Ctx("mia", ws)

	const token = "Nprq9AppendedContent"
	require.False(t, editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Log.md", "body": "Log start.\n",
	}).IsError)

	require.Empty(t, ifxSearchPaths(t, searchTool, ctx, "kb", token))

	v := a4Version(t, root, "Log.md")
	res := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "append_section", "path": "Log.md",
		"heading": "Notes", "body": "Contains " + token + " here.", "expect_version": v,
	})
	require.False(t, res.IsError, "append_section refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index refresh problem: %s", res.ForLLM)

	require.Equal(t, []string{"Log.md"}, ifxSearchPaths(t, searchTool, ctx, "kb", token))
}

func TestIndexFreshness_Link_IsInstantlySearchable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	searchTool := ifxSearchTool(t, home)
	ctx := a4Ctx("mia", ws)

	const alias = "Mtcv3AliasText"
	require.False(t, editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Target.md", "body": "Target note.\n",
	}).IsError)
	require.False(t, editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Source.md", "body": "Source note.\n",
	}).IsError)

	require.Empty(t, ifxSearchPaths(t, searchTool, ctx, "kb", alias))

	v := a4Version(t, root, "Source.md")
	res := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "link", "path": "Source.md", "target": "Target",
		"alias": alias, "expect_version": v,
	})
	require.False(t, res.IsError, "link refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index refresh problem: %s", res.ForLLM)

	require.Equal(t, []string{"Source.md"}, ifxSearchPaths(t, searchTool, ctx, "kb", alias))
}

func TestIndexFreshness_ReplaceBody_IsInstantlySearchable(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	searchTool := ifxSearchTool(t, home)
	ctx := a4Ctx("mia", ws)

	const token = "Jkxo8ReplacedText"
	require.False(t, editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Doc.md", "body": "Original body text.\n",
	}).IsError)

	require.Empty(t, ifxSearchPaths(t, searchTool, ctx, "kb", token))

	v := a4Version(t, root, "Doc.md")
	res := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "replace_body", "path": "Doc.md",
		"anchor": "Original body text.", "body": "Now contains " + token + ".",
		"expect_version": v,
	})
	require.False(t, res.IsError, "replace_body refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index refresh problem: %s", res.ForLLM)

	require.Equal(t, []string{"Doc.md"}, ifxSearchPaths(t, searchTool, ctx, "kb", token))
}

// ---------------------------------------------------------------------------
// The properties index specifically — a typed record's row lands with the
// right declared type and a hash that matches what is on disk.
// ---------------------------------------------------------------------------

func TestIndexFreshness_Create_PropertiesIndexRowLandsInstantly(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	veSchema(t, root) // defines the "deal" record type (knowledge_edit_test.go)
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	ctx := a4Ctx("mia", ws)

	// PRECONDITION: nothing indexed yet for a collection nobody has synced.
	require.Empty(t, ifxPropPaths(t, home, root))

	res := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Acme.md",
		"frontmatter": map[string]any{"type": "deal", "status": "prospect"},
	})
	require.False(t, res.IsError, "create refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index refresh problem: %s", res.ForLLM)

	rows := ifxPropPaths(t, home, root)
	row, ok := rows["Acme.md"]
	require.True(t, ok, "Acme.md must be in the properties index immediately after create")
	require.Equal(t, propindex.KindNote, row.Kind)
	require.Equal(t, "deal", row.DeclaredType)
	require.NotEmpty(t, row.SchemaFingerprint, "the schema that governs this note must be recorded")

	onDisk, err := os.ReadFile(filepath.Join(root, "Acme.md"))
	require.NoError(t, err)
	require.Equal(t, propindex.SourceHash(onDisk), row.SourceHash,
		"the stored hash must match the file's actual bytes right now, not a stale or missing value")
}

// ---------------------------------------------------------------------------
// An attachment write stays body-free (FR-039a), even through the direct
// index-refresh path this feature adds.
// ---------------------------------------------------------------------------

// TestIndexFreshness_Attachment_NeverReadsBody proves upsertPropertiesNote
// never opens an attachment's content. The primary proof does not depend on
// the filesystem enforcing anything: an attachment's row is built from
// StatEntry alone (an Lstat, keyed off the file's NAME/extension) and its
// SourceHash field is never set — a non-empty SourceHash could only ever
// come from hashing bytes, and hashing means reading, so an empty one after
// a successful refresh is direct evidence no read happened. The file is also
// made unreadable (chmod 000) as defence in depth — on a filesystem that
// actually enforces it, any attempt to open the file for content would fail
// loudly instead of silently succeeding — but that chmod is not itself the
// test's oracle, because permission enforcement is not portable across every
// sandboxed dev environment this suite runs in.
func TestIndexFreshness_Attachment_NeverReadsBody(t *testing.T) {
	home, _, root := a4Fixture(t, "kb")

	abs := a4Note(t, root, "diagram.png", "not really a png, just bytes")
	require.NoError(t, os.Chmod(abs, 0o000))
	t.Cleanup(func() { _ = os.Chmod(abs, 0o600) })

	warning := refreshIndexesForNote(context.Background(), home, root, "diagram.png")
	require.Empty(t, warning, "an attachment refresh must never attempt to read the file's content")

	rows := ifxPropPaths(t, home, root)
	row, ok := rows["diagram.png"]
	require.True(t, ok)
	require.Equal(t, propindex.KindAttachment, row.Kind)
	require.Empty(t, row.SourceHash, "an attachment's SourceHash must stay empty — a non-empty one could only come from reading and hashing the file")
}

// ---------------------------------------------------------------------------
// knowledge_restructure: rename touches the old path, the new path, and
// every note whose inbound links were rewritten.
// ---------------------------------------------------------------------------

func TestIndexFreshness_Rename_ReindexesOldNewAndRewrittenLinks(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	restructureTool := NewRestructureTool(deps)
	ctx := a4Ctx("mia", ws)

	require.False(t, editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Target.md", "body": "# Target\n",
	}).IsError)
	require.False(t, editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Source.md", "body": "# Source\n",
	}).IsError)

	v := a4Version(t, root, "Source.md")
	linkRes := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "link", "path": "Source.md", "target": "Target",
		"expect_version": v,
	})
	require.False(t, linkRes.IsError, "link refused: %s", linkRes.ForLLM)

	before := ifxPropPaths(t, home, root)
	require.Contains(t, before, "Target.md")
	require.Contains(t, before, "Source.md")
	beforeSourceHash := before["Source.md"].SourceHash

	renameRes := restructureTool.Execute(ctx, map[string]any{
		"op": "rename", "collection": "kb", "path": "Target.md", "new_name": "Renamed.md",
	})
	require.False(t, renameRes.IsError, "rename refused: %s", renameRes.ForLLM)
	require.NotContains(t, renameRes.ForLLM, "INDEX:", "unexpected index refresh problem: %s", renameRes.ForLLM)

	onDiskSource, err := os.ReadFile(filepath.Join(root, "Source.md"))
	require.NoError(t, err)
	require.Contains(t, string(onDiskSource), "Renamed",
		"the rename engine must have rewritten Source.md's link for this test to be meaningful")

	after := ifxPropPaths(t, home, root)
	require.NotContains(t, after, "Target.md", "the old path must leave the properties index instantly")
	require.Contains(t, after, "Renamed.md", "the new path must enter the properties index instantly")
	require.Contains(t, after, "Source.md")
	require.NotEqual(t, beforeSourceHash, after["Source.md"].SourceHash,
		"Source.md's stored hash must be re-derived to match its rewritten bytes, not left stale")
	require.Equal(t, propindex.SourceHash(onDiskSource), after["Source.md"].SourceHash)
}

// ---------------------------------------------------------------------------
// knowledge_restructure: trash removes from the index, restore puts it back —
// both instantly, both proven with the same read-your-own-write pattern.
// ---------------------------------------------------------------------------

func TestIndexFreshness_TrashAndRestore_AreInstant(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	_ = root
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	restructureTool := NewRestructureTool(deps)
	searchTool := ifxSearchTool(t, home)
	ctx := a4Ctx("mia", ws)

	const token = "Hbcp4TrashRestoreTok"

	// PRECONDITION.
	require.Empty(t, ifxSearchPaths(t, searchTool, ctx, "kb", token))

	res := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Doomed.md",
		"body": "Contains " + token + " here.\n",
	})
	require.False(t, res.IsError, "create refused: %s", res.ForLLM)
	require.Equal(t, []string{"Doomed.md"}, ifxSearchPaths(t, searchTool, ctx, "kb", token))

	trashRes := restructureTool.Execute(ctx, map[string]any{
		"op": "trash", "collection": "kb", "path": "Doomed.md",
	})
	require.False(t, trashRes.IsError, "trash refused: %s", trashRes.ForLLM)
	require.NotContains(t, trashRes.ForLLM, "INDEX:", "unexpected index refresh problem: %s", trashRes.ForLLM)

	require.Empty(t, ifxSearchPaths(t, searchTool, ctx, "kb", token),
		"a trashed note must be instantly unfindable")

	restoreRes := restructureTool.Execute(ctx, map[string]any{
		"op": "restore", "collection": "kb", "path": "Doomed.md",
	})
	require.False(t, restoreRes.IsError, "restore refused: %s", restoreRes.ForLLM)
	require.NotContains(t, restoreRes.ForLLM, "INDEX:", "unexpected index refresh problem: %s", restoreRes.ForLLM)

	require.Equal(t, []string{"Doomed.md"}, ifxSearchPaths(t, searchTool, ctx, "kb", token),
		"a restored note must be instantly findable again")
}

// ---------------------------------------------------------------------------
// Index-update failure: the write is saved regardless, and the failure is
// reported, never silent (design doc §8's principle applied to this path —
// see author.go's "Index freshness" section header for the argument).
// ---------------------------------------------------------------------------

// TestIndexFreshness_TextIndexFailure_WarnsButKeepsTheWrite corrupts the
// manifest UpdatePath depends on (the documented "manifest unusable, run a
// full Sync" refusal) and proves the tool call still succeeds, still reports
// the problem in its own rendered response, and still lands the write on
// disk.
func TestIndexFreshness_TextIndexFailure_WarnsButKeepsTheWrite(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	ctx := a4Ctx("mia", ws)

	res1 := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Note.md", "body": "first version\n",
	})
	require.False(t, res1.IsError, "create refused: %s", res1.ForLLM)

	dir, err := IndexDirFor(home, root)
	require.NoError(t, err)
	manifestPath := filepath.Join(dir, ManifestFileName)
	require.NoError(t, os.WriteFile(manifestPath, []byte("not json"), 0o600))

	v := a4Version(t, root, "Note.md")
	res2 := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "status", "value": "active", "expect_version": v,
	})
	require.False(t, res2.IsError,
		"the write itself must not be refused just because the text index could not be refreshed: %s", res2.ForLLM)
	require.Contains(t, res2.ForLLM, "INDEX:", "a failed index refresh must be reported, not silently dropped")
	require.Contains(t, res2.ForLLM, "manifest unusable")

	onDisk := a4Read(t, root, "Note.md")
	require.Contains(t, onDisk, "status: active", "the write must not be lost because the index refresh failed")
}

// TestIndexFreshness_PropertiesIndexFailure_WarnsButKeepsTheWrite is the
// properties-index twin of the text-index test above. The failure is
// injected by replacing properties.db with a DIRECTORY of the same name —
// a filesystem type-mismatch that fails identically regardless of the
// process's uid or the host filesystem's permission-enforcement quirks
// (unlike a chmod-based injection, which is meaningless as root and was
// observed to be silently bypassed on at least one sandboxed dev
// environment's temp filesystem during this change's own verification).
// propindex.Open's precreateOwnerOnly fails opening it BEFORE sql.Open is
// ever reached, so this is not a "corruption" propindex.Open would self-heal
// — see sqlite.go's Open doc comment on that distinction. The same three
// things are proven as the text-index test: the tool call still succeeds,
// the problem is reported, and the write is not lost.
func TestIndexFreshness_PropertiesIndexFailure_WarnsButKeepsTheWrite(t *testing.T) {
	home, ws, root := a4Fixture(t, "kb")
	deps, _ := a4Deps(home)
	editTool := NewEditTool(deps)
	ctx := a4Ctx("mia", ws)

	res1 := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "create", "path": "Note.md", "body": "first version\n",
	})
	require.False(t, res1.IsError, "create refused: %s", res1.ForLLM)

	propPath, err := PropertiesIndexPath(home, root)
	require.NoError(t, err)
	require.NoError(t, os.Remove(propPath))
	require.NoError(t, os.Mkdir(propPath, 0o700))

	v := a4Version(t, root, "Note.md")
	res2 := editTool.Execute(ctx, map[string]any{
		"collection": "kb", "op": "set_property", "path": "Note.md",
		"property": "status", "value": "active", "expect_version": v,
	})
	require.False(t, res2.IsError,
		"the write itself must not be refused just because the properties index could not be refreshed: %s",
		res2.ForLLM)
	require.Contains(t, res2.ForLLM, "INDEX:", "a failed index refresh must be reported, not silently dropped")

	onDisk := a4Read(t, root, "Note.md")
	require.Contains(t, onDisk, "status: active", "the write must not be lost because the index refresh failed")
}

// ---------------------------------------------------------------------------
// The platform-unavailable filter, unit-tested directly: it is the one
// properties-index problem that must NOT surface per write (see author.go's
// "Index freshness" section header) — everything else must.
// ---------------------------------------------------------------------------

func TestIndexRefreshOutcome_FiltersPlatformUnavailableButNotOtherFailures(t *testing.T) {
	var out indexRefreshOutcome
	out.propsProblem("Note.md", fmt.Errorf("wrap: %w", records.ErrPropertyIndexUnavailable))
	require.Empty(t, out.summary(),
		"a platform-unavailable properties-index error must not surface per write — "+
			"it was already logged once, by name, at platform-detection time")

	out.propsProblem("Note.md", errors.New("disk full"))
	require.NotEmpty(t, out.summary(), "a genuine properties-index failure must still be reported")

	var textOut indexRefreshOutcome
	textOut.textProblem("Note.md", errors.New("some text-index failure"))
	require.NotEmpty(t, textOut.summary(), "a text-index failure is never filtered")
}
