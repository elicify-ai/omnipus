// Omnipus — UAT 2026-09-13 D-70 / D-136: unloadable views are named with
// their reason, and a view answer names the `.base` it came from.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/vaultimport"
)

func TestKnowledgeBaseViews_NamesEachUnloadableViewWithItsReason_D70(t *testing.T) {
	slugs := vaultimport.NewSlugRegistry()
	mine := slugs.Slug(baseViewsTestBaseRel, "Outstanding")

	api, ws := buildBaseViewsVault(t, map[string]string{
		mine + ".yaml": importedViewFile(mine, "Outstanding", baseViewsTestBaseRel, ""),
		"invoices--broken.yaml": "name: invoices--broken\n" +
			"type: invoice\n" +
			"group-by: client\n" +
			"source: " + baseViewsTestBaseRel + "\n",
	})

	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)

	require.Equal(t, 1, out.UnloadableCount)
	// DIES ON the old handler: only the count was reported; `unloadable`
	// was absent, so the preview could name neither the view nor the cause.
	require.NotNil(t, out.Unloadable, "the rejections behind the count must travel with it")
	require.Len(t, *out.Unloadable, 1)
	entry := (*out.Unloadable)[0]
	assert.NotEmpty(t, entry.Code)
	assert.NotEmpty(t, entry.Reason)
	require.Len(t, entry.Paths, 1)
	assert.Contains(t, entry.Paths[0], "invoices--broken")
}

func TestKnowledgeBaseViews_UnloadableIsEmptyNotAbsentWhenNothingFailed_D70(t *testing.T) {
	slugs := vaultimport.NewSlugRegistry()
	mine := slugs.Slug(baseViewsTestBaseRel, "Outstanding")
	api, ws := buildBaseViewsVault(t, map[string]string{
		mine + ".yaml": importedViewFile(mine, "Outstanding", baseViewsTestBaseRel, ""),
	})
	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)
	require.NotNil(t, out.Unloadable)
	assert.Empty(t, *out.Unloadable)
}

func TestKnowledgeView_EchoesTheBaseFileAnImportedViewCameFrom_D136(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"invoices--all.yaml": "name: invoices--all\ntype: invoice\nlayout: table\nsource: Invoices.base\n",
		"hand-written.yaml":  "name: hand-written\ntype: invoice\nlayout: table\n",
	})
	res, code := getViewResult(t, api, ws, colID, "invoices--all")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	// DIES ON the old handler: no provenance on the answer at all.
	require.NotNil(t, res.Source, "an imported view names the .base it lives in")
	assert.Equal(t, "Invoices.base", *res.Source)

	authored, code := getViewResult(t, api, ws, colID, "hand-written")
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, authored.Source, "an authored view has no .base behind it and says so by absence")
}

func TestKnowledgeView_EchoesPropertyConfigDisplayNames_D35(t *testing.T) {
	api, ws, colID := buildViewTestVault(t, map[string]string{
		"invoices--labelled.yaml": "name: invoices--labelled\ntype: invoice\nlayout: table\n" +
			"property_config:\n  amount:\n    display_name: Amount due\n",
		"invoices--plain.yaml": "name: invoices--plain\ntype: invoice\nlayout: table\n",
	})
	res, code := getViewResult(t, api, ws, colID, "invoices--labelled")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, res.Refusal)
	// DIES ON the old handler: the view's presentation map never left the
	// server, so the SPA printed the machine key as the heading.
	require.NotNil(t, res.PropertyConfig, "the view's property_config travels with the answer")
	cfg, ok := (*res.PropertyConfig)["amount"]
	require.True(t, ok)
	require.NotNil(t, cfg.DisplayName)
	assert.Equal(t, "Amount due", *cfg.DisplayName)

	plain, code := getViewResult(t, api, ws, colID, "invoices--plain")
	require.Equal(t, http.StatusOK, code)
	assert.Nil(t, plain.PropertyConfig, "a view that declares none says so by absence")
}
