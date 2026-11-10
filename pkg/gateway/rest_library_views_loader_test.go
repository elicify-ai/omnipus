// REST saved-view serving parity with the Library listing (#1017).
// Oracle: the published never-a-link and bounded-file rules, and design L4/L18.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// A view file reached through a RELATIVE link that stays inside the knowledge base
// is neither listed, decorated nor served by GET .../knowledge/views.
func TestLibraryViewsLoader_RelativeInKnowledgeBaseLinkIsNeverServedOrDecorated(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "limits")
	viewsDir := filepath.Join(vault, ".omnipus-vault", "views")
	seedLimitVault(t, vault, "Endpoint vault", nil)
	writeNote(t, vault, ".omnipus-vault/views/limits--control.yaml", limitVaultViewFile(t, "limits--control", 120))
	writeNote(t, vault, "notes/aliased.yaml", limitVaultViewFile(t, "limits--alias", 120))
	require.NoError(t, os.Symlink("../../notes/aliased.yaml", filepath.Join(viewsDir, "limits--alias.yaml")))

	// Instrument check: the link resolves to a readable regular file, and the same
	// root-relative open os.Root performs accepts it.
	root, err := os.OpenRoot(vault)
	require.NoError(t, err)
	f, err := root.Open(".omnipus-vault/views/limits--alias.yaml")
	require.NoError(t, err, "fixture check: a relative in-root link resolves through os.Root")
	require.NoError(t, f.Close())
	require.NoError(t, root.Close())

	rows := listEntries(t, api, ws, "limits/.omnipus-vault/views", "&include_views=true&include_hidden=true")
	require.Equal(t, []string{"limits--control.yaml"}, viewRowNames(t, rows),
		"a view file reached through a link is not a saved view in the listing")
	linkRow, found := findEntryByPath(rows, "limits/.omnipus-vault/views/limits--alias.yaml")
	require.True(t, found, "the linked file remains an ordinary row")
	require.Nil(t, linkRow.IsView)
	require.Nil(t, linkRow.View)

	collectionID := collectionIDOf(t, api, ws, "limits")
	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/views?collection_id="+url.QueryEscape(collectionID))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	collection := decodeJSON[gen.KnowledgeCollectionViews](t, w)
	served := make([]string, 0, len(collection.Views))
	for _, v := range collection.Views {
		served = append(served, v.Name)
	}
	require.Equal(t, []string{"limits--control"}, served,
		"GET .../knowledge/views serves the set the listing shows: a linked view file is not served")
}

// GET .../knowledge/base-views serves only what the listing serves: a view file
// reached through a relative in-knowledge-base link, and a view file over
// 256 KiB, are not enumerated for the base file they name.
func TestLibraryViewsLoader_BaseViewsServesOnlyWhatTheListingServes(t *testing.T) {
	mine := "invoices--outstanding"
	api, ws := buildBaseViewsVault(t, map[string]string{
		mine + ".yaml": importedViewFile(mine, "Outstanding", baseViewsTestBaseRel, ""),
	})
	vault := filepath.Join(workDir(api, ws), "vault")
	writeNote(t, vault, "notes/aliased.yaml", importedViewFile("invoices--aliased", "Aliased", baseViewsTestBaseRel, ""))
	require.NoError(t, os.Symlink("../../notes/aliased.yaml",
		filepath.Join(vault, ".omnipus-vault", "views", "invoices--aliased.yaml")))
	header := importedViewFile("invoices--oversize", "Oversize", baseViewsTestBaseRel, "")
	size := 256<<10 + 1
	writeNote(t, vault, ".omnipus-vault/views/invoices--oversize.yaml",
		header+"#"+strings.Repeat("x", size-len(header)-2)+"\n")
	root, err := os.OpenRoot(vault)
	require.NoError(t, err)
	linked, err := root.Open(".omnipus-vault/views/invoices--aliased.yaml")
	require.NoError(t, err, "instrument: the relative link resolves within the root")
	require.NoError(t, linked.Close())
	require.NoError(t, root.Close())
	info, err := os.Stat(filepath.Join(vault, ".omnipus-vault", "views", "invoices--oversize.yaml"))
	require.NoError(t, err)
	require.Equal(t, int64(256<<10+1), info.Size(), "instrument: exactly one byte beyond the published file limit")

	w := knowledgeGet(t, api,
		"/api/v1/library/"+ws+"/knowledge/base-views?path="+url.QueryEscape("vault/"+baseViewsTestBaseRel))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out := decodeJSON[gen.KnowledgeBaseViews](t, w)
	names := make([]string, 0, len(out.Views))
	for _, v := range out.Views {
		names = append(names, v.Name)
	}
	require.Equal(t, []string{mine}, names,
		"base-views must not enumerate a linked view file or a view file over 256 KiB")
	rows := listEntries(t, api, ws, "vault/.omnipus-vault/views", "&include_views=true&include_hidden=true")
	require.Equal(t, []string{mine + ".yaml"}, viewRowNames(t, rows),
		"the listing decorates exactly the same ordinary view")
	for _, name := range []string{"invoices--aliased.yaml", "invoices--oversize.yaml"} {
		row, found := findEntryByPath(rows, "vault/.omnipus-vault/views/"+name)
		require.True(t, found, "a refused candidate stays an ordinary storage row")
		require.Nil(t, row.IsView)
		require.Nil(t, row.View)
	}
}
