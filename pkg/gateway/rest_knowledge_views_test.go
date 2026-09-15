// Tests for GET /api/v1/library/{workspace_id}/knowledge/views — the
// collection-addressed saved-views list (UAT D-13, web half).
//
// A view authored with knowledge_configure's create_view/write_view writes
// `<vault>/.omnipus-vault/views/<slug>.yaml` and NO `.base` file, so the
// file-addressed base-views endpoint cannot see it; before this endpoint such
// a view answered correctly over the API with no UI surface at all. The
// oracle for every slug and `source` here is the loader (records.LoadViews)
// reading fixture view files written the way the importer and the configure
// tools write them.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/vaultimport"
)

// buildViewsListVault seeds a vault whose collection owns BOTH kinds of saved
// view: one imported from a `.base` (carries `source`) and one authored in
// place (no `source` — the D-13 case), plus one view file the loader must
// reject. No index is built: this endpoint enumerates views, it never
// evaluates one.
func buildViewsListVault(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Finance vault")
	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)

	slugs := vaultimport.NewSlugRegistry()
	imported := slugs.Slug("CRM/Invoices.base", "Outstanding")
	writeNote(t, vault, ".omnipus-vault/views/"+imported+".yaml",
		importedViewFile(imported, "Outstanding", "CRM/Invoices.base", ""))
	// The D-13 case: a view with NO `.base` behind it — exactly what
	// knowledge_configure create_view leaves on disk.
	writeNote(t, vault, ".omnipus-vault/views/authored--active.yaml",
		"name: authored--active\ntype: invoice\nlabel: Active invoices\nproperties: [client, amount]\n")
	// A file the loader rejects (unknown key), so unloadable accounting is
	// exercised on the same surface.
	writeNote(t, vault, ".omnipus-vault/views/invoices--broken.yaml",
		"name: invoices--broken\ntype: invoice\ngroup-by: client\nsource: CRM/Invoices.base\n")
	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// TestKnowledgeViews_ListsViewsABaselessCollectionOwns_D13 — the endpoint's
// reason to exist: an authored view with no `.base` is listed, addressable by
// its slug, beside the imported one that names its source.
func TestKnowledgeViews_ListsViewsABaselessCollectionOwns_D13(t *testing.T) {
	api, ws, collectionID := buildViewsListVault(t)

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/views?collection_id="+url.QueryEscape(collectionID))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeCollectionViews](t, w)

	assert.Equal(t, collectionID, out.CollectionId)
	byName := map[string]gen.KnowledgeBaseView{}
	for _, v := range out.Views {
		byName[v.Name] = v
	}

	// The authored view — no `.base` anywhere behind it — is THE D-13 case.
	require.Contains(t, byName, "authored--active",
		"a view with no .base file must be listed; before this endpoint it had no UI surface at all")
	assert.Equal(t, "Active invoices", byName["authored--active"].Label)
	assert.Nil(t, byName["authored--active"].Source,
		"an authored view belongs to the collection, not to any file")

	// The imported view is here too, WITH its source.
	slugs := vaultimport.NewSlugRegistry()
	imported := slugs.Slug("CRM/Invoices.base", "Outstanding")
	require.Contains(t, byName, imported)
	require.NotNil(t, byName[imported].Source)
	assert.Equal(t, "CRM/Invoices.base", *byName[imported].Source)

	// Rejected files are counted and named, never silently dropped.
	assert.Equal(t, 1, out.UnloadableCount)
	require.NotNil(t, out.Unloadable)
	require.Len(t, *out.Unloadable, 1)
}

// TestKnowledgeViews_ScopeAndValidation_D13 — the collection-addressed
// boundary every knowledge endpoint shares (US-9/FR-052/FR-053), plus the
// parameter validation.
func TestKnowledgeViews_ScopeAndValidation_D13(t *testing.T) {
	api, ws, collectionID := buildViewsListVault(t)
	wsB := seedLibraryWorkspace(t, api, "Workspace B")

	t.Run("another workspace's collection is an empty answer, not a 403", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/"+wsB+"/knowledge/views?collection_id="+url.QueryEscape(collectionID))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		out := decodeJSON[gen.KnowledgeCollectionViews](t, w)
		assert.Empty(t, out.Views)
		assert.Equal(t, 0, out.UnloadableCount)
		assert.NotContains(t, w.Body.String(), "authored--active",
			"an out-of-scope answer must not confirm which views exist elsewhere")
	})

	t.Run("the owning workspace sees its views over the same id", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/views?collection_id="+url.QueryEscape(collectionID))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		out := decodeJSON[gen.KnowledgeCollectionViews](t, w)
		require.Len(t, out.Views, 2)
	})

	t.Run("collection_id is required", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/views")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("an unknown collection id is the empty answer, not a 404", func(t *testing.T) {
		w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/views?collection_id=kb_nope")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		out := decodeJSON[gen.KnowledgeCollectionViews](t, w)
		assert.Empty(t, out.Views)
	})
}
