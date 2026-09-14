// Regression tests for the Library text-editor save door's index freshness
// (Claude review 2026-09-14, C5): PUT /api/v1/library/{id}/content wrote the
// bytes and nothing else, so an in-place frontmatter edit left the PROPERTIES
// index stale indefinitely — the watcher refreshes only the text index, and
// the count-based self-heal cannot see a row whose count did not change. The
// REST record-write door (rest_knowledge_record.go, UAT D-67) already calls
// knowledge.RefreshIndexesForNote after every landed write; this is the same
// guarantee for the door a human typing in the text editor uses.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// buildIndexedRecordVault is buildRecordTestVault plus BOTH indexes built the
// way the running gateway builds them (text via a real SyncTracked cycle,
// properties via vaultprops.Sync), so a test starts from the state every
// real deployment starts from: the two indexes agreeing with disk.
//
// It returns the api, workspace id, the vault's REAL path (symlinks resolved
// — the index keys on the resolved root, FR-031, and on macOS a t.TempDir
// under /tmp resolves elsewhere), and the collection id a client would use.
func buildIndexedRecordVault(t *testing.T) (*restAPI, string, string, string) {
	t.Helper()
	api, ws, vault := buildRecordTestVault(t)
	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	requirePropertiesIndexSynced(t, err)
	return api, ws, realVault, collectionIDOf(t, api, ws, "vault")
}

// libraryContentToken GETs the note's current version token — the value a
// real editor sends back as expect_version — failing the test if the read
// does not produce one.
func libraryContentToken(t *testing.T, api *restAPI, ws, rel string) string {
	t.Helper()
	w := libGet(t, api, "/api/v1/library/"+ws+"/content?path="+rel)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	return libraryBareETag(t, w)
}

// putLibraryNoteContent saves rel's whole new content through the text save
// door with a valid expect_version, returning the response.
func putLibraryNoteContent(t *testing.T, api *restAPI, ws, rel, content, expectVersion string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"path": rel, "content": content, "expect_version": expectVersion,
	})
	require.NoError(t, err)
	return libPutJSON(t, api, "/api/v1/library/"+ws+"/content", string(body))
}

// typedFindAllWidgets runs the ENGINE's own typed find (the same evaluation
// environment every view answer is served from) over the vault, asking for
// every widget record.
func typedFindAllWidgets(t *testing.T, api *restAPI, ws, colID string) gen.VaultFindResponse {
	t.Helper()
	col, inScope := api.resolveScopedCollection(ws, colID)
	require.True(t, inScope)
	env, closeEnv, err := vaultprops.OpenFindEnv(context.Background(), api.homePath, col)
	require.NoError(t, err)
	defer closeEnv()
	rt := "widget"
	resp, err := knowledgefind.Find(context.Background(), env.Deps, gen.VaultFindRequest{Type: &rt})
	require.NoError(t, err)
	require.False(t, resp.Refused, "the engine refused the typed find: %+v", resp.Problems)
	return resp
}

// widgetNameCell returns the row for relPath and its rendered `name` cell —
// the value a view serves, read from the properties index the way every view
// answer reads it.
func widgetNameCell(t *testing.T, resp gen.VaultFindResponse, relPath string) (string, bool) {
	t.Helper()
	for _, row := range resp.Rows {
		if row.Path != relPath {
			continue
		}
		for _, cell := range row.Cells {
			if cell.Property == "name" {
				return cell.Value, true
			}
		}
	}
	return "", false
}

// TestLibraryContentPut_RefreshesPropertiesIndexAfterFrontmatterEdit is C5
// exactly as observed: the properties index was built (boot-time sync) with
// the old value, the Library text editor saves a frontmatter edit through
// PUT .../content, and every typed find keeps serving the old value
// indefinitely — the watcher only refreshes the text index, and a same-count
// row change is invisible to the count-based self-heal.
func TestLibraryContentPut_RefreshesPropertiesIndexAfterFrontmatterEdit(t *testing.T) {
	api, ws, _, colID := buildIndexedRecordVault(t)

	before, ok := widgetNameCell(t, typedFindAllWidgets(t, api, ws, colID), "w1.md")
	require.True(t, ok, "fixture must be indexed before the save")
	require.Equal(t, "Sprocket", before)

	edited := recordTestWidgetNote("WD-0001", "Sprocket Mk II", "open")
	w := putLibraryNoteContent(t, api, ws, "vault/w1.md", edited, libraryContentToken(t, api, ws, "vault/w1.md"))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	resp := typedFindAllWidgets(t, api, ws, colID)
	name, ok := widgetNameCell(t, resp, "w1.md")
	require.True(t, ok, "the row must still be served after the save")
	assert.Equal(t, "Sprocket Mk II", name,
		"C5: a frontmatter edit saved through the Library text door must be visible to a typed find immediately")
	assert.True(t, resp.Complete,
		"both indexes refreshed by the save: the answer must be complete, not flagged stale — problems: %+v", resp.Problems)
}

// TestLibraryContentPut_NewNoteIsImmediatelyIndexed is the create half of the
// same door: the Library's New-note flow PUTs a first note with
// expect_version "v1:absent", and that note must be a row in the properties
// index as soon as the save returns — not only after the next full sync.
func TestLibraryContentPut_NewNoteIsImmediatelyIndexed(t *testing.T) {
	api, ws, _, colID := buildIndexedRecordVault(t)

	created := recordTestWidgetNote("WD-0002", "Gear", "closed")
	w := putLibraryNoteContent(t, api, ws, "vault/w2.md", created, "v1:absent")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	name, ok := widgetNameCell(t, typedFindAllWidgets(t, api, ws, colID), "w2.md")
	require.True(t, ok, "the New-note flow's record must be indexed immediately")
	assert.Equal(t, "Gear", name)
}

// TestLibraryContentPut_IndexRefreshFailureIsHonestAboutCompleteness pins the
// honesty half: when the post-save index refresh CANNOT update the properties
// index, the save still succeeds (the bytes are already on disk — a refusal
// here would tell the caller their save failed when it did not), and the next
// typed find must NOT claim complete. The engine's own freshness comparison
// (properties source_hash vs the text index's hash) is what says so: the save
// refreshed the text index, the properties refresh failed, the two disagree,
// and the row is served stale and flagged rather than served confidently.
//
// The refresh failure is injected deterministically on every platform and
// uid: a DIRECTORY at the properties index path makes propindex.Open's own
// precreateOwnerOnly fail with EISDIR (open(2) refuses O_RDWR on a directory
// for every caller, root included) — no permission games that a root-running
// CI worker would silently bypass.
func TestLibraryContentPut_IndexRefreshFailureIsHonestAboutCompleteness(t *testing.T) {
	api, ws, vault, colID := buildIndexedRecordVault(t)

	idxPath, err := knowledge.PropertiesIndexPath(api.homePath, vault)
	require.NoError(t, err)
	saved := idxPath + ".saved"
	require.NoError(t, os.Rename(idxPath, saved))
	require.NoError(t, os.Mkdir(idxPath, 0o700))
	restorePropsIndex := func() {
		require.NoError(t, os.RemoveAll(idxPath))
		require.NoError(t, os.Rename(saved, idxPath))
	}
	defer func() { _ = os.Rename(saved, idxPath) }()

	edited := recordTestWidgetNote("WD-0001", "Sprocket Mk II", "open")
	w := putLibraryNoteContent(t, api, ws, "vault/w1.md", edited, libraryContentToken(t, api, ws, "vault/w1.md"))
	require.Equal(t, http.StatusOK, w.Code,
		"a failed index refresh must never refuse the save — the bytes are already on disk; body: %s", w.Body.String())

	// The store the find reads is the PRE-SAVE one (old value, old hash).
	restorePropsIndex()
	resp := typedFindAllWidgets(t, api, ws, colID)

	name, ok := widgetNameCell(t, resp, "w1.md")
	require.True(t, ok, "the stale row must still be served — flagged, not dropped")
	assert.Equal(t, "Sprocket", name,
		"the fixture's point: the properties row is the pre-save one")
	assert.False(t, resp.Complete,
		"a save whose properties-index refresh failed must not let the next typed find claim complete")
	for _, row := range resp.Rows {
		if row.Path != "w1.md" {
			continue
		}
		require.NotNil(t, row.Stale,
			"the row the failed refresh left behind must be flagged stale, not served as fresh")
		assert.True(t, *row.Stale)
	}
}
