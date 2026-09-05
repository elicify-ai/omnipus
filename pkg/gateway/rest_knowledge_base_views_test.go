// Tests for GET /api/v1/library/{workspace_id}/knowledge/base-views — which
// saved views came from one `.base` file (view-kinds-design-2026-09-03 §7).
//
// THE ORACLE IS THE IMPORTER, NOT THIS FILE. The view filenames and the `name`
// keys inside them are produced by the REAL pkg/vaultimport SlugRegistry, so
// the collision test cannot pass by agreeing with a spelling the test itself
// invented — if the slugger's collision suffix ever changed, the fixture would
// change with it and the assertion would still be about the same fact: two
// view names that kebab alike must enumerate as TWO addressable views.
//
// That is the defect this endpoint exists to close. The SPA used to re-derive
// slugs by parsing the `.base` file, could not reproduce the collision
// counter, and mapped both names onto `invoices--a-b` — so the second tab
// fetched the first view and rendered its rows under the second view's name.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/vaultimport"
)

const baseViewsTestSchema = "schema_version: 1\n" +
	"type: invoice\n" +
	"properties:\n" +
	"  client:   { type: text }\n" +
	"  amount:   { type: decimal, unit_property: currency }\n" +
	"  currency: { type: enum, values: [SGD, EUR] }\n"

// baseViewsTestBaseRel is the vault-relative path of the fixture `.base`, and
// therefore the exact string an imported view records in its own `source`.
const baseViewsTestBaseRel = "CRM/Invoices.base"

// importedViewFile is one saved view exactly as the importer writes it: the
// slug as `name`, the human view name as `label`, and the base it came from as
// `source`.
func importedViewFile(slug, label, source string, extra string) string {
	return "name: " + slug + "\n" +
		"type: invoice\n" +
		"label: " + label + "\n" +
		extra +
		"properties: [client, amount]\n" +
		"source: " + source + "\n"
}

// buildBaseViewsVault seeds a vault holding one `.base` file plus the given
// saved-view files, and returns the api and workspace id. No index is built:
// this endpoint enumerates views, it never evaluates one.
func buildBaseViewsVault(t *testing.T, views map[string]string) (*restAPI, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Finance vault")
	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
	// The `.base` itself. Its CONTENT is deliberately never read by this
	// endpoint — import is one-shot (FR-102) — but the file must exist, since
	// the caller is addressing it by path.
	writeNote(t, vault, baseViewsTestBaseRel, "views:\n  - type: table\n    name: A/B\n")
	for name, body := range views {
		writeNote(t, vault, ".omnipus-vault/views/"+name, body)
	}
	return api, ws
}

// --- the defect: two view names that kebab alike -----------------------------

func TestKnowledgeBaseViews_CollidingViewNamesEnumerateAsTwoDistinctViews(t *testing.T) {
	// The slugs come from the REAL importer, in the same order the importer
	// would hand them out for one base file.
	slugs := vaultimport.NewSlugRegistry()
	slugAB := slugs.Slug(baseViewsTestBaseRel, "A/B")
	slugASpaceB := slugs.Slug(baseViewsTestBaseRel, "A B")
	require.NotEqual(t, slugAB, slugASpaceB,
		"premise: the importer gives colliding view names DIFFERENT slugs; if it does not, "+
			"there is nothing for this endpoint to report")

	api, ws := buildBaseViewsVault(t, map[string]string{
		slugAB + ".yaml":      importedViewFile(slugAB, "A/B", baseViewsTestBaseRel, ""),
		slugASpaceB + ".yaml": importedViewFile(slugASpaceB, "A B", baseViewsTestBaseRel, ""),
	})

	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)

	require.True(t, out.IsKnowledgeBase)
	assert.Equal(t, "vault/"+baseViewsTestBaseRel, out.BasePath)
	require.NotNil(t, out.Source)
	assert.Equal(t, baseViewsTestBaseRel, *out.Source,
		"views are matched on the vault-relative source the importer wrote, not on the caller's path")
	require.NotNil(t, out.CollectionRoot)
	assert.Equal(t, "vault", *out.CollectionRoot)
	require.NotNil(t, out.CollectionId)
	assert.Equal(t, collectionIDOf(t, api, ws, "vault"), *out.CollectionId,
		"the collection reported here must be the one the view-result endpoint is called with")

	require.Len(t, out.Views, 2, "two colliding view names are TWO views, not one")
	names := []string{out.Views[0].Name, out.Views[1].Name}
	assert.ElementsMatch(t, []string{slugAB, slugASpaceB}, names,
		"each view must be addressed by the slug the importer actually used")

	labels := map[string]string{}
	for _, v := range out.Views {
		labels[v.Name] = v.Label
	}
	assert.Equal(t, "A/B", labels[slugAB])
	assert.Equal(t, "A B", labels[slugASpaceB],
		"the second colliding view must carry its OWN label, not the first view's")
	assert.Equal(t, 0, out.UnloadableCount)
}

// --- attribution: only this base's views, and the ones that failed to load ---

func TestKnowledgeBaseViews_ExcludesOtherBasesAndCountsUnloadable(t *testing.T) {
	slugs := vaultimport.NewSlugRegistry()
	mine := slugs.Slug(baseViewsTestBaseRel, "Outstanding")
	otherSlugs := vaultimport.NewSlugRegistry()
	theirs := otherSlugs.Slug("CRM/Deals.base", "Open")

	api, ws := buildBaseViewsVault(t, map[string]string{
		mine + ".yaml":   importedViewFile(mine, "Outstanding", baseViewsTestBaseRel, ""),
		theirs + ".yaml": importedViewFile(theirs, "Open", "CRM/Deals.base", ""),
		// An authored view belonging to no base at all.
		"hand-written.yaml": "name: hand-written\ntype: invoice\nproperties: [client]\n",
		// A view file that names THIS base and cannot load: `group-by` is not a
		// key the contract declares, and an unknown key is refused rather than
		// dropped (records/view.go's DisallowUnknownFields rule).
		"invoices--broken.yaml": "name: invoices--broken\n" +
			"type: invoice\n" +
			"group-by: client\n" +
			"source: " + baseViewsTestBaseRel + "\n",
	})

	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)

	require.Len(t, out.Views, 1, "only views whose `source` names THIS base belong here")
	assert.Equal(t, mine, out.Views[0].Name)
	assert.Equal(t, 1, out.UnloadableCount,
		"a view file this base owns but which cannot load must be COUNTED, never silently dropped")
}

// --- unservable is reported, not hidden --------------------------------------

func TestKnowledgeBaseViews_ReportsADisabledViewAsUnservable(t *testing.T) {
	slugs := vaultimport.NewSlugRegistry()
	ok := slugs.Slug(baseViewsTestBaseRel, "Outstanding")
	off := slugs.Slug(baseViewsTestBaseRel, "Everything")

	api, ws := buildBaseViewsVault(t, map[string]string{
		ok + ".yaml":  importedViewFile(ok, "Outstanding", baseViewsTestBaseRel, ""),
		off + ".yaml": importedViewFile(off, "Everything", baseViewsTestBaseRel, "disabled: true\n"),
	})

	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)

	require.Len(t, out.Views, 2, "a disabled view is still declared; hiding it would be the silent loss")
	byName := map[string]gen.KnowledgeBaseView{}
	for _, v := range out.Views {
		byName[v.Name] = v
	}
	require.Contains(t, byName, off)
	require.NotNil(t, byName[off].Unservable)
	assert.True(t, *byName[off].Unservable)
	require.NotNil(t, byName[off].UnservableReason)
	assert.NotEmpty(t, *byName[off].UnservableReason)

	require.Contains(t, byName, ok)
	if byName[ok].Unservable != nil {
		assert.False(t, *byName[ok].Unservable, "a servable view must not be marked unservable")
	}
}

// --- kind is carried through -------------------------------------------------

func TestKnowledgeBaseViews_CarriesTheDeclaredKind(t *testing.T) {
	slugs := vaultimport.NewSlugRegistry()
	slug := slugs.Slug(baseViewsTestBaseRel, "By client")

	api, ws := buildBaseViewsVault(t, map[string]string{
		slug + ".yaml": "name: " + slug + "\n" +
			"type: invoice\n" +
			"label: By client\n" +
			"kind: summary\n" +
			"layout: table\n" +
			"properties: [client, amount]\n" +
			"source: " + baseViewsTestBaseRel + "\n",
	})

	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)
	require.Len(t, out.Views, 1)
	require.NotNil(t, out.Views[0].Kind)
	assert.Equal(t, "summary", *out.Views[0].Kind)
}

// --- a .base outside every collection is an ordinary file --------------------

func TestKnowledgeBaseViews_OutsideAKnowledgeBaseIsAStatedAnswer(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	writeNote(t, workDir(api, ws), "loose/Report.base", "views:\n  - type: table\n    name: All\n")

	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("loose/Report.base"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)

	assert.False(t, out.IsKnowledgeBase)
	assert.Nil(t, out.CollectionId)
	assert.Nil(t, out.Source)
	assert.NotNil(t, out.Views, "an empty answer must be an empty array, never null")
	assert.Empty(t, out.Views)
	assert.Equal(t, 0, out.UnloadableCount)
}

// --- refusals ----------------------------------------------------------------

func TestKnowledgeBaseViews_Refusals(t *testing.T) {
	api, ws := buildBaseViewsVault(t, map[string]string{})

	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"no path", "/api/v1/library/" + ws + "/knowledge/base-views", http.StatusBadRequest},
		{"blank path", "/api/v1/library/" + ws + "/knowledge/base-views?path=%20", http.StatusBadRequest},
		{"escaping path", "/api/v1/library/" + ws + "/knowledge/base-views?path=" + url.QueryEscape("../escape.base"), http.StatusBadRequest},
		{"not a base file", "/api/v1/library/" + ws + "/knowledge/base-views?path=" + url.QueryEscape("vault/a.md"), http.StatusBadRequest},
		{"absent file", "/api/v1/library/" + ws + "/knowledge/base-views?path=" + url.QueryEscape("vault/Nope.base"), http.StatusNotFound},
		{"unknown workspace", "/api/v1/library/ws_absent/knowledge/base-views?path=" + url.QueryEscape("vault/x.base"), http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := knowledgeGet(t, api, tc.target)
			assert.Equal(t, tc.want, w.Code, w.Body.String())
		})
	}
}

func TestKnowledgeBaseViews_RejectsNonGET(t *testing.T) {
	api, ws := buildBaseViewsVault(t, map[string]string{})
	w := httptest.NewRecorder()
	api.HandleLibraryTree(w, httptest.NewRequest(http.MethodPost,
		"/api/v1/library/"+ws+"/knowledge/base-views?path=vault/x.base", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, w.Body.String())
}
