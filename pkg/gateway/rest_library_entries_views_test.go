// Tests for GET /api/v1/library/{workspace_id}/entries with include_views=true
// — saved views as rows in the Library listing (#1017, server side).
//
// Every expected value here derives from the DESIGN NOTE and the CONTRACT, not
// from any implementation (none exists yet): the behaviour rules L1–L17 and the
// "include_views" parameter description in contracts/openapi.yaml at commit
// 009972eb0, plus the founder's 2026-11-07 answers LQ1=C / LQ2=A / LQ3=A
// recorded in /workspace/logs/library-views/design.md. Where an existing
// endpoint is used as an ORACLE (the knowledge views list for parity, the
// detect endpoint for collection_id), that endpoint's own contract is the
// pre-existing, already-tested behaviour — reading it keeps the parity tests
// from re-deriving collection ids or view slugs by hand.
//
// RED status per rule is stated on each test: rules that INTRODUCE behaviour
// fail on the pre-change code for the right reason (the view rows do not
// exist); rules that PIN non-regression (L1, L3, L10, L14, L15, L17) are
// expected green before the change — they hold today precisely because the
// feature is absent, and their job is to stay true after it lands.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// viewVaultRel is the workspace-relative path of the shared fixture knowledge
// base. Its display name ("Finance vault") deliberately differs from the
// folder name so any assertion that names the knowledge base observes WHICH
// name the server chose.
const viewVaultRel = "vault"

// seedViewListingVault seeds one workspace whose "vault" knowledge base holds
// the fixture the placement and shape rules read:
//
//	vault/
//	  CRM/Invoices.base          the .base the outstanding view was imported from
//	  CRM/invoice-1.md           an ordinary note next to the .base
//	  Deep/Report.base           a second .base, one folder deeper
//	  Deep/Nested/placeholder.md an empty leaf folder (no view belongs here)
//	  root-note.md, notes.yaml   ordinary root files (notes.yaml: a plain YAML
//	                             file that is NOT a view)
//	  .omnipus-vault/views/
//	    invoices--outstanding.yaml  imported, label "Outstanding", kind summary,
//	                                source CRM/Invoices.base
//	    deep-report.yaml            imported, label "Deep report", NO kind,
//	                                source Deep/Report.base
//	    authored--active.yaml       authored in place (no source, no kind)
//	    invoices--broken.yaml       the loader rejects it (unknown key)
//	    .draft.yaml                 a dot file the loader skips
func seedViewListingVault(t *testing.T) (*restAPI, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), viewVaultRel)
	makeKnowledgeBase(t, vault, "Finance vault")
	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
	writeNote(t, vault, "CRM/Invoices.base", "views:\n  - type: table\n    name: A/B\n")
	writeNote(t, vault, "CRM/invoice-1.md", "# Invoice\n")
	writeNote(t, vault, "Deep/Report.base", "views:\n  - type: table\n    name: R\n")
	writeNote(t, vault, "Deep/Nested/placeholder.md", "keep\n")
	writeNote(t, vault, "root-note.md", "# Root\n")
	writeNote(t, vault, "notes.yaml", "key: value\n")
	writeNote(t, vault, ".omnipus-vault/views/invoices--outstanding.yaml",
		importedViewFile("invoices--outstanding", "Outstanding", "CRM/Invoices.base", "kind: summary\n"))
	writeNote(t, vault, ".omnipus-vault/views/deep-report.yaml",
		importedViewFile("deep-report", "Deep report", "Deep/Report.base", ""))
	writeNote(t, vault, ".omnipus-vault/views/authored--active.yaml",
		"name: authored--active\ntype: invoice\nlabel: Active invoices\nproperties: [client, amount]\n")
	// Rejected by the loader the same way rest_knowledge_views_test.go's own
	// broken fixture is (an unknown top-level key), so unloadable accounting
	// and the not-listed rule read the SAME fixture.
	writeNote(t, vault, ".omnipus-vault/views/invoices--broken.yaml",
		"name: invoices--broken\ntype: invoice\ngroup-by: client\nsource: CRM/Invoices.base\n")
	writeNote(t, vault, ".omnipus-vault/views/.draft.yaml",
		"name: dot-draft\ntype: invoice\nlabel: Draft\nproperties: [client]\n")
	return api, ws
}

// listEntries lists rel through the real dispatch (api.HandleLibrary), the
// same entry point rest_library_test.go uses. extra carries raw extra query
// parameters ("&include_views=true", "&include_hidden=true").
func listEntries(t *testing.T, api *restAPI, ws, rel, extra string) []gen.LibraryEntry {
	t.Helper()
	w := libGet(t, api, "/api/v1/library/"+ws+"/entries?path="+rel+extra)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	return decodeJSON[[]gen.LibraryEntry](t, w)
}

func listEntriesWithViews(t *testing.T, api *restAPI, ws, rel string) []gen.LibraryEntry {
	t.Helper()
	return listEntries(t, api, ws, rel, "&include_views=true")
}

// viewRowNames returns the file NAME of every row marked is_view, in listing
// order. A row counts only when BOTH is_view is true and view is present —
// the contract makes `view` present exactly when is_view is true, so a row
// with one and not the other is a contract break this helper surfaces as a
// wrong name set rather than silently skipping.
func viewRowNames(t *testing.T, entries []gen.LibraryEntry) []string {
	t.Helper()
	var names []string
	for _, e := range entries {
		if e.IsView != nil && *e.IsView {
			require.NotNil(t, e.View, "row %s: is_view true without a view object", e.Path)
			names = append(names, e.Name)
		}
	}
	return names
}

func findEntryByPath(entries []gen.LibraryEntry, rel string) (gen.LibraryEntry, bool) {
	for _, e := range entries {
		if e.Path == rel {
			return e, true
		}
	}
	return gen.LibraryEntry{}, false
}

func requireNoViewFacts(t *testing.T, entries []gen.LibraryEntry) {
	t.Helper()
	for _, e := range entries {
		assert.Nil(t, e.IsView, "%s: is_view must be absent", e.Path)
		assert.Nil(t, e.View, "%s: view must be absent", e.Path)
	}
}

// --- L1 ---------------------------------------------------------------------

// TestLibraryEntriesViews_IncludeViewsOffIsTodaysResponse pins L1: without
// include_views the response is exactly today's — the visible entries of the
// tree and nothing else, no is_view, no view object.
//
// GUARD RULE: expected green on the pre-change code (the feature's absence is
// what makes it true); it must stay green once the feature lands.
func TestLibraryEntriesViews_IncludeViewsOffIsTodaysResponse(t *testing.T) {
	api, ws := seedViewListingVault(t)

	w := libGet(t, api, "/api/v1/library/"+ws+"/entries?path="+viewVaultRel)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	entries := decodeJSON[[]gen.LibraryEntry](t, w)

	// The exact row set, derived from the fixture tree plus the documented
	// hidden rule (dot-prefixed names omitted; .omnipus-vault is hidden) —
	// not from what the handler returns.
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	sort.Strings(paths)
	require.Equal(t, []string{"vault/CRM", "vault/Deep", "vault/notes.yaml", "vault/root-note.md"}, paths,
		"L1: the default listing must not gain or lose rows")

	// "no is_view, no view" on every row, and nowhere in the raw body —
	// omitempty means an unset field cannot appear in the JSON at all.
	requireNoViewFacts(t, entries)
	assert.NotContains(t, w.Body.String(), `"is_view"`, "L1: raw body must not carry is_view")
	assert.NotContains(t, w.Body.String(), `"view":`, "L1: raw body must not carry a view object")

	// Absent and explicit false are the same request to the server: the two
	// responses must be byte-identical.
	wFalse := libGet(t, api, "/api/v1/library/"+ws+"/entries?path="+viewVaultRel+"&include_views=false")
	require.Equal(t, http.StatusOK, wFalse.Code, wFalse.Body.String())
	assert.Equal(t, w.Body.String(), wFalse.Body.String(),
		"L1: include_views=false must answer byte-for-byte like the absent parameter")
}

// --- L2 ---------------------------------------------------------------------

// TestLibraryEntriesViews_NonBooleanIncludeViewsIsAnswered400 pins L2:
// include_views must be a boolean; anything else is answered 400 "the same
// way a bad include_hidden is answered" (design L2).
//
// RED on the pre-change code: the parameter is not parsed yet, so the junk
// value is ignored and the listing answers 200.
func TestLibraryEntriesViews_NonBooleanIncludeViewsIsAnswered400(t *testing.T) {
	api, ws := seedViewListingVault(t)

	// All four are refused by strconv.ParseBool; none may be silently ignored.
	// (No trailing-space case: a raw space would panic httptest.NewRequest's
	// request-line parser before the handler ever runs — a harness artefact,
	// not the rule under test.)
	for _, bad := range []string{"notabool", "yes", "1.5"} {
		w := libGet(t, api, "/api/v1/library/"+ws+"/entries?path="+viewVaultRel+"&include_views="+bad)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"L2: include_views=%q must be refused with 400, got body: %s", bad, w.Body.String())
	}
}

// --- L3 ---------------------------------------------------------------------

// TestLibraryEntriesViews_OutsideAnyKnowledgeBaseNoViewRows pins L3: with
// include_views=true, a directory not inside any knowledge base gains no view
// rows and no is_view — the workspace root and a plain folder stay exactly
// what they are.
//
// GUARD RULE: expected green on the pre-change code; it holds today because
// nothing produces view rows yet, and must still hold after the feature lands.
func TestLibraryEntriesViews_OutsideAnyKnowledgeBaseNoViewRows(t *testing.T) {
	api, ws := seedViewListingVault(t)
	writeNote(t, workDir(api, ws), "plain/readme.md", "# plain\n")

	// Workspace root and a plain folder: no row anywhere carries view facts,
	// even though the vault below holds loadable views.
	requireNoViewFacts(t, listEntriesWithViews(t, api, ws, ""))
	requireNoViewFacts(t, listEntriesWithViews(t, api, ws, "plain"))
}

// --- L4 + L13 ---------------------------------------------------------------

// TestLibraryEntriesViews_ViewSetEqualsKnowledgeViewsEndpoint pins L4 (the
// parity rule) and L13 (unloadable views are not listed): across every folder
// of the knowledge base, the names of the view rows are exactly the names the
// knowledge views endpoint returns for the same collection — same loader, same
// schemas — and the file the loader rejects appears only in that endpoint's
// unloadable accounting, never as a row.
//
// RED on the pre-change code: the listing produces no view rows at all, so the
// collected set is empty where the endpoint returns three views.
func TestLibraryEntriesViews_ViewSetEqualsKnowledgeViewsEndpoint(t *testing.T) {
	api, ws := seedViewListingVault(t)
	collectionID := collectionIDOf(t, api, ws, viewVaultRel)

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/views?collection_id="+url.QueryEscape(collectionID))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	views := decodeJSON[gen.KnowledgeCollectionViews](t, w)

	// Fixture instrument: the loader accepts exactly the three seeded views —
	// if this drifts, the fixture and the parity oracle disagree and the
	// comparison below proves nothing.
	endpointNames := map[string]bool{}
	for _, v := range views.Views {
		endpointNames[v.Name] = true
	}
	require.Equal(t, map[string]bool{
		"invoices--outstanding": true,
		"deep-report":           true,
		"authored--active":      true,
	}, endpointNames, "fixture drift: the views endpoint must see exactly the three seeded views")

	collected := map[string]bool{}
	// Every folder of K, plus the workspace root as an out-of-K control. The
	// storage folder is excluded by the rule itself (L6: exactly one folder,
	// outside the storage folder).
	for _, folder := range []string{"", viewVaultRel, viewVaultRel + "/CRM", viewVaultRel + "/Deep", viewVaultRel + "/Deep/Nested"} {
		for _, e := range listEntriesWithViews(t, api, ws, folder) {
			if e.IsView != nil && *e.IsView {
				require.NotNil(t, e.View, "%s: is_view without view", e.Path)
				collected[e.View.Name] = true
			}
		}
	}
	require.Equal(t, endpointNames, collected,
		"L4: the view rows across the knowledge base's folders must be exactly the views endpoint's set")

	// L13: the rejected file stays counted and NAMED by the endpoint, and is
	// absent from the rows above (it is not in endpointNames, so the map
	// equality already refuses it; this pins the endpoint half of the rule).
	require.Equal(t, 1, views.UnloadableCount,
		"L13: the broken view file must stay counted by the views endpoint")
}

// --- L5 + L6 + L16 ----------------------------------------------------------

// TestLibraryEntriesViews_PlacementFollowsSource pins the placement rules:
// L5 places a view in the folder its source names when that folder exists as a
// directory of the same knowledge base (whether or not the .base file itself
// still exists); L6 lists every other view — authored, or a source failing any
// L5 condition — at the knowledge base root, exactly once; L16 never opens or
// lists a path built from source: every view row's path stays inside the
// storage folder, and an adversarial source cannot make the listing fail or
// reach across knowledge bases.
//
// RED on the pre-change code: no view rows exist anywhere, so every placement
// expectation below fails on the missing rows.
func TestLibraryEntriesViews_PlacementFollowsSource(t *testing.T) {
	api, ws := seedViewListingVault(t)
	vaultDir := filepath.Join(workDir(api, ws), viewVaultRel)

	// L5 — placed next to its .base.
	{
		rows := viewRowNames(t, listEntriesWithViews(t, api, ws, viewVaultRel+"/CRM"))
		require.Equal(t, []string{"invoices--outstanding.yaml"}, rows,
			"L5: the view sourced to CRM/Invoices.base is listed in vault/CRM")
	}
	// L5 — placed one folder deeper, next to its own .base.
	{
		rows := viewRowNames(t, listEntriesWithViews(t, api, ws, viewVaultRel+"/Deep"))
		require.Equal(t, []string{"deep-report.yaml"}, rows,
			"L5: the view sourced to Deep/Report.base is listed in vault/Deep")
	}
	// L5 — a folder no view belongs in lists none (the CRM and Deep views are
	// placed one level up, not recursively).
	{
		rows := viewRowNames(t, listEntriesWithViews(t, api, ws, viewVaultRel+"/Deep/Nested"))
		require.Empty(t, rows, "L5: an empty leaf folder lists no views")
	}
	// L5 — placement holds whether or not the .base file still exists.
	{
		require.NoError(t, os.Remove(filepath.Join(vaultDir, "CRM", "Invoices.base")))
		rows := viewRowNames(t, listEntriesWithViews(t, api, ws, viewVaultRel+"/CRM"))
		require.Equal(t, []string{"invoices--outstanding.yaml"}, rows,
			"L5: deleting the .base file must not move the view")
	}
	// L6 — an authored view (no source) is listed at the knowledge base root,
	// and the other views are listed in exactly their one folder, not here.
	{
		rows := viewRowNames(t, listEntriesWithViews(t, api, ws, viewVaultRel))
		require.Equal(t, []string{"authored--active.yaml"}, rows,
			"L6: a view with no source is listed at the knowledge base root, and placed views are not listed here too")
	}

	// L6 + L16 + nested knowledge bases. vault2 carries one view per failing
	// L5 condition, plus a DEEPER knowledge base inside it: the deepest-K rule
	// says listing vault2 shows vault2's views (all six fall back to its root)
	// and listing vault2/inner shows only inner's own view; a source pointing
	// INTO the nested knowledge base is not placed there (it is inside a
	// deeper nested knowledge base), it falls back to the outer root.
	vault2 := filepath.Join(workDir(api, ws), "vault2")
	makeKnowledgeBase(t, vault2, "Adversary vault")
	writeNote(t, vault2, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
	adversarial := map[string]string{ // storage file name -> declared source
		"escape-up.yaml":   "../outside.base",
		"absolute.yaml":    "/abs/path.base",
		"backslash.yaml":   `back\slash.base`,
		"selfstorage.yaml": ".omnipus-vault/views/self.base",
		"missing-dir.yaml": "Missing/Dir.base",
		"into-nested.yaml": "inner/notes.base",
	}
	for file, source := range adversarial {
		slug := strings.TrimSuffix(file, ".yaml")
		writeNote(t, vault2, ".omnipus-vault/views/"+file,
			importedViewFile(slug, "Label "+slug, source, ""))
	}
	inner := filepath.Join(vault2, "inner")
	makeKnowledgeBase(t, inner, "Inner vault")
	writeNote(t, inner, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
	writeNote(t, inner, ".omnipus-vault/views/inner-view.yaml",
		"name: inner-view\ntype: invoice\nlabel: Inner view\nproperties: [client]\n")

	outerNames := make([]string, 0, len(adversarial))
	for file := range adversarial {
		outerNames = append(outerNames, file)
	}
	sort.Strings(outerNames)

	outerRows := listEntriesWithViews(t, api, ws, "vault2")
	require.Equal(t, outerNames, viewRowNames(t, outerRows),
		"L6: every view whose source fails an L5 condition — including one pointing into a deeper nested knowledge base — is listed at its knowledge base root")
	assert.NotContains(t, viewRowNames(t, outerRows), "inner-view.yaml",
		"deepest-K: the nested knowledge base's view must not appear in the outer knowledge base's listing")

	innerRows := listEntriesWithViews(t, api, ws, "vault2/inner")
	require.Equal(t, []string{"inner-view.yaml"}, viewRowNames(t, innerRows),
		"deepest-K: listing inside the nested knowledge base shows exactly the nested knowledge base's own views")

	// L16 — no path built from source is ever opened or listed: whatever the
	// source says, a view row's path is the storage file's real path (L7:
	// K_rel/.omnipus-vault/views/<file>.yaml, K_rel being the knowledge base
	// the view belongs to). Each row set is therefore checked against ITS OWN
	// knowledge base's storage folder — the outer vault2's for the adversarial
	// views, the nested vault2/inner's for its own view.
	for _, kb := range []struct {
		storagePrefix string
		rows          []gen.LibraryEntry
	}{
		{"vault2/.omnipus-vault/views/", outerRows},
		{"vault2/inner/.omnipus-vault/views/", innerRows},
	} {
		for _, e := range kb.rows {
			if e.IsView != nil && *e.IsView {
				assert.True(t, strings.HasPrefix(e.Path, kb.storagePrefix),
					"L16: view row path %q must stay inside its knowledge base's storage folder %q regardless of its declared source", e.Path, kb.storagePrefix)
			}
		}
	}
}

// --- L7 ---------------------------------------------------------------------

// TestLibraryEntriesViews_ViewRowDescribesTheStorageFile pins L7: a view row
// describes the saved view FILE — its real workspace-relative storage path,
// its file name, and the same size/modified_at/mime/is_text_editable/is_hidden
// facts any file row of that path carries — plus is_view=true and the view
// object addressed by the collection id the detect endpoint hands out.
//
// RED on the pre-change code: the placed row does not exist.
func TestLibraryEntriesViews_ViewRowDescribesTheStorageFile(t *testing.T) {
	api, ws := seedViewListingVault(t)
	storageRel := viewVaultRel + "/.omnipus-vault/views"
	viewFileRel := storageRel + "/invoices--outstanding.yaml"

	rows := listEntriesWithViews(t, api, ws, viewVaultRel+"/CRM")
	row, ok := findEntryByPath(rows, viewFileRel)
	require.True(t, ok, "L7/L5: the placed view row for %s must appear in the CRM listing (rows: %v)",
		viewFileRel, viewRowNames(t, rows))
	require.NotNil(t, row.IsView, "L7: the view row must carry is_view")
	require.True(t, *row.IsView, "L7: is_view must be true on a view row")
	require.NotNil(t, row.View, "L7: a view row must carry its view object")

	// The file facts are "built by the same code as any file row": the wire
	// oracle is the plain row the same path gets from the storage-folder
	// listing, not a re-derivation of mime or size tables.
	plainRows := listEntries(t, api, ws, storageRel, "&include_hidden=true")
	plain, ok := findEntryByPath(plainRows, viewFileRel)
	require.True(t, ok, "fixture drift: %s must be listed plainly in the storage folder", viewFileRel)
	require.Equal(t, plain.Path, row.Path, "L7: path")
	require.Equal(t, plain.Name, row.Name, "L7: name")
	require.False(t, row.IsDir, "L7: a view row is a file row")
	require.Equal(t, plain.Size, row.Size, "L7: size")
	require.Equal(t, plain.ModifiedAt, row.ModifiedAt, "L7: modified_at")
	require.Equal(t, plain.Mime, row.Mime, "L7: mime")
	require.Equal(t, plain.IsTextEditable, row.IsTextEditable, "L7: is_text_editable")
	require.Equal(t, plain.IsHidden, row.IsHidden, "L7: is_hidden")

	// The view facts, addressed the way the contract says callers address them.
	require.Equal(t, "invoices--outstanding", row.View.Name,
		"L7: view.name is the view's slug, not the file name")
	require.Equal(t, "Outstanding", row.View.Label, "L7: view.label is the declared label")
	require.Equal(t, collectionIDOf(t, api, ws, viewVaultRel), row.View.CollectionId,
		"L7: view.collection_id is the same opaque id the detect endpoint reports for the knowledge base")
}

// --- L8 ---------------------------------------------------------------------

// TestLibraryEntriesViews_KindAndSourceAreEchoedNotInvented pins L8: kind is
// present exactly when the view file declares a non-blank kind, source exactly
// when it declares one — echoed verbatim, never invented for views that lack
// them (the imported-without-kind case is the common one, R5/LQ3).
//
// RED on the pre-change code: none of these rows exist.
func TestLibraryEntriesViews_KindAndSourceAreEchoedNotInvented(t *testing.T) {
	api, ws := seedViewListingVault(t)

	// Declared kind is echoed verbatim (covered with its row's shape in L7's
	// test); here the rule's negative half, on real rows:
	deepRows := listEntriesWithViews(t, api, ws, viewVaultRel+"/Deep")
	deep, ok := findEntryByPath(deepRows, viewVaultRel+"/.omnipus-vault/views/deep-report.yaml")
	require.True(t, ok, "fixture drift: the Deep view row must exist")
	require.NotNil(t, deep.View, "the deep-report row must be a view row")
	require.Nil(t, deep.View.Kind,
		"L8: a view whose file declares no kind must carry NO kind — none is ever invented")
	require.NotNil(t, deep.View.Source, "the deep-report view declares a source")
	require.Equal(t, "Deep/Report.base", *deep.View.Source,
		"L8: source is echoed verbatim")

	rootRows := listEntriesWithViews(t, api, ws, viewVaultRel)
	authored, ok := findEntryByPath(rootRows, viewVaultRel+"/.omnipus-vault/views/authored--active.yaml")
	require.True(t, ok, "fixture drift: the authored view row must exist")
	require.NotNil(t, authored.View, "the authored row must be a view row")
	require.Nil(t, authored.View.Kind, "L8: an authored view with no kind carries no kind")
	require.Nil(t, authored.View.Source,
		"L8: an authored view belongs to the knowledge base, not to any file — source must be absent")
}

// --- L9 ---------------------------------------------------------------------

// TestLibraryEntriesViews_ViewRowsSortWithFiles pins L9: the listing order is
// unchanged — directories first, then all files including view rows, by
// case-insensitive name — and no paging is added.
//
// RED on the pre-change code: the view row is missing from the sequence.
func TestLibraryEntriesViews_ViewRowsSortWithFiles(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "sortvault")
	makeKnowledgeBase(t, vault, "Sort vault")
	writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
	// Authored view whose storage FILE name sorts between the two notes (the
	// row sorts by its file name, like every file row).
	writeNote(t, vault, ".omnipus-vault/views/M--middle.yaml",
		"name: middle-view\ntype: invoice\nlabel: Middle\nproperties: [client]\n")
	writeNote(t, vault, "aaa.md", "a\n")
	writeNote(t, vault, "zzz.md", "z\n")
	writeNote(t, vault, "zzz dir/note.md", "n\n")

	rows := listEntriesWithViews(t, api, ws, "sortvault")
	names := make([]string, 0, len(rows))
	for _, e := range rows {
		names = append(names, e.Name)
	}
	require.Equal(t, []string{"zzz dir", "aaa.md", "M--middle.yaml", "zzz.md"}, names,
		"L9: directories first, then files including the view row, by case-insensitive name")
}

// --- L10 --------------------------------------------------------------------

// TestLibraryEntriesViews_ViewRowsFollowTheHiddenRule pins L10's observable
// half: a view row's is_hidden follows the dot rule on its NAME, and a loaded
// view's file name never starts with a dot (records.LoadViews skips dot files,
// so a hidden view row cannot exist — the include_hidden=false-drop half is
// vacuous for loaded views and is NOT asserted as reachable behaviour).
//
// GUARD RULE: expected green on the pre-change code.
func TestLibraryEntriesViews_ViewRowsFollowTheHiddenRule(t *testing.T) {
	api, ws := seedViewListingVault(t)

	for _, e := range listEntriesWithViews(t, api, ws, viewVaultRel) {
		if e.IsView != nil && *e.IsView {
			assert.False(t, e.IsHidden, "L10: %s follows the dot rule on its name", e.Path)
		}
	}
	for _, e := range listEntries(t, api, ws, viewVaultRel+"/.omnipus-vault/views", "&include_hidden=true") {
		if e.IsView != nil && *e.IsView {
			assert.False(t, e.IsHidden, "L10: %s follows the dot rule on its name", e.Path)
		}
	}
}

// --- L11 --------------------------------------------------------------------

// TestLibraryEntriesViews_StorageFolderRowsAreMarkedToo pins L11: listing the
// storage folder itself (include_hidden is needed to reach it) marks each real
// file that is a loaded view with is_view and the same view object, while
// files that did not load stay plain rows.
//
// RED on the pre-change code: no is_view appears anywhere.
func TestLibraryEntriesViews_StorageFolderRowsAreMarkedToo(t *testing.T) {
	api, ws := seedViewListingVault(t)

	rows := listEntries(t, api, ws, viewVaultRel+"/.omnipus-vault/views", "&include_hidden=true&include_views=true")
	byName := map[string]gen.LibraryEntry{}
	for _, e := range rows {
		byName[e.Name] = e
	}
	require.Len(t, rows, 5, "fixture drift: the storage folder holds five files")

	for name, slug := range map[string]string{
		"invoices--outstanding.yaml": "invoices--outstanding",
		"deep-report.yaml":           "deep-report",
		"authored--active.yaml":      "authored--active",
	} {
		e, ok := byName[name]
		require.True(t, ok, "fixture drift: %s missing", name)
		require.NotNil(t, e.IsView, "L11: %s is a loaded view and must carry is_view", name)
		require.True(t, *e.IsView, "L11: %s", name)
		require.NotNil(t, e.View, "L11: %s must carry the same view object", name)
		require.Equal(t, slug, e.View.Name, "L11: %s addresses the same view wherever listed", name)
	}

	// Files that did not load stay plain rows (L11's second half, and L13's
	// shape side): the rejected file and the dot file the loader skips.
	for _, name := range []string{"invoices--broken.yaml", ".draft.yaml"} {
		e, ok := byName[name]
		require.True(t, ok, "fixture drift: %s missing", name)
		assert.Nil(t, e.IsView, "L11: %s did not load and stays a plain row", name)
		assert.Nil(t, e.View, "L11: %s did not load and carries no view object", name)
	}
}

// --- L12 --------------------------------------------------------------------

// TestLibraryEntriesViews_PlainYamlFilesAreNeverViewRows pins L12: a file that
// merely ends in .yaml — or sits next to a .base — is never a view row; and a
// normal file whose name EQUALS a view row's name is listed too, because the
// two rows have different paths.
//
// RED on the pre-change code: the view row half of the same-name pair is
// missing.
func TestLibraryEntriesViews_PlainYamlFilesAreNeverViewRows(t *testing.T) {
	api, ws := seedViewListingVault(t)

	// A normal file at the knowledge base root with the SAME NAME as the
	// authored view's row name — the collision an implementation keyed by
	// name alone would collapse.
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir(api, ws), viewVaultRel, "authored--active.yaml"),
		[]byte("key: value\n"), 0o644))

	rootRows := listEntriesWithViews(t, api, ws, viewVaultRel)
	plainTwin, okPlain := findEntryByPath(rootRows, viewVaultRel+"/authored--active.yaml")
	viewRow, okView := findEntryByPath(rootRows, viewVaultRel+"/.omnipus-vault/views/authored--active.yaml")
	require.True(t, okPlain, "L12: the normal file must be listed")
	require.True(t, okView, "L12: the view row of the same NAME must be listed too — the paths differ")
	if plainTwin.IsView != nil && *plainTwin.IsView {
		t.Errorf("L12: the plain YAML file %s was turned into a view row — a file that merely ends in .yaml is never a view", plainTwin.Path)
	}
	require.NotNil(t, viewRow.IsView, "L12: the storage file row is the view row")
	require.True(t, *viewRow.IsView, "L12")
	if notes, ok := findEntryByPath(rootRows, viewVaultRel+"/notes.yaml"); ok {
		if notes.IsView != nil && *notes.IsView {
			t.Errorf("L12: notes.yaml is a plain YAML file and must never carry is_view")
		}
	} else {
		t.Errorf("fixture drift: notes.yaml must be listed at the knowledge base root")
	}

	// Sitting next to a .base makes nothing a view row either.
	crmRows := listEntriesWithViews(t, api, ws, viewVaultRel+"/CRM")
	if note, ok := findEntryByPath(crmRows, viewVaultRel+"/CRM/invoice-1.md"); ok {
		assert.True(t, note.IsView == nil || !*note.IsView,
			"L12: a note next to a .base is never a view row")
	} else {
		t.Errorf("fixture drift: invoice-1.md must be listed in CRM")
	}
}

// --- L14 --------------------------------------------------------------------

// TestLibraryEntriesViews_LoadFailureNeverFailsTheListing pins L14: when the
// views or schemas cannot be LOADED, the listing still answers 200 with the
// ordinary rows only — a views failure never fails the listing.
//
// The trigger is a ReadDir-level load error, because records.LoadSchemas
// REPORTS invalid yaml as a rejection instead of erroring (verified in
// pkg/records/schema.go): `.omnipus-vault/views` and `.omnipus-vault/records`
// are written as FILES, so the loaders' directory reads fail outright.
//
// GUARD RULE: expected green on the pre-change code (nothing loads anything
// yet); it refuses an implementation that lets a load failure fail the listing.
func TestLibraryEntriesViews_LoadFailureNeverFailsTheListing(t *testing.T) {
	t.Run("views directory unreadable", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		vault := filepath.Join(workDir(api, ws), "livelock")
		makeKnowledgeBase(t, vault, "Live vault")
		writeNote(t, vault, ".omnipus-vault/records/invoice.yaml", baseViewsTestSchema)
		writeNote(t, vault, ".omnipus-vault/views", "this is a file, not a directory")
		writeNote(t, vault, "note.md", "# n\n")

		rows := listEntriesWithViews(t, api, ws, "livelock")
		requireNoViewFacts(t, rows)
		paths := make([]string, 0, len(rows))
		for _, e := range rows {
			paths = append(paths, e.Path)
		}
		require.Equal(t, []string{"livelock/note.md"}, paths,
			"L14: the listing still returns the ordinary rows")
	})

	t.Run("schema directory unreadable", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		vault := filepath.Join(workDir(api, ws), "schemabroke")
		makeKnowledgeBase(t, vault, "Schema vault")
		writeNote(t, vault, ".omnipus-vault/records", "this is a file, not a directory")
		writeNote(t, vault, ".omnipus-vault/views/ok.yaml",
			"name: ok\ntype: invoice\nlabel: OK\nproperties: [client]\n")
		writeNote(t, vault, "note.md", "# n\n")

		rows := listEntriesWithViews(t, api, ws, "schemabroke")
		requireNoViewFacts(t, rows)
		paths := make([]string, 0, len(rows))
		for _, e := range rows {
			paths = append(paths, e.Path)
		}
		require.Equal(t, []string{"schemabroke/note.md"}, paths,
			"L14: a schema load error never fails the listing either")
	})
}

// --- L15 --------------------------------------------------------------------

// TestLibraryEntriesViews_ListingSpendsNoKnowledgeRetrievalBudget pins L15:
// listing a folder is not a knowledge query, so even with include_views=true
// it must not consume the knowledge retrieval rate limit.
//
// The instrument is checked first: the anonymous knowledge-read bucket for
// THIS workspace is drained through the same limiter the handlers use, and the
// control request proves the drain worked by being refused 429. Only then is
// the listing required to still answer 200.
//
// GUARD RULE: expected green on the pre-change code (nothing charges the
// bucket yet); it refuses an implementation that routes the listing through
// allowKnowledgeRetrieval.
func TestLibraryEntriesViews_ListingSpendsNoKnowledgeRetrievalBudget(t *testing.T) {
	api, ws := seedViewListingVault(t)
	collectionID := collectionIDOf(t, api, ws, viewVaultRel)

	limiter, key := knowledgeRESTLimiter.budgetFor("", ws, knowledgeRead)
	drained := false
	for i := 0; i < 1000; i++ {
		if d := limiter.Allow(key); !d.Allowed {
			drained = true
			break
		}
	}
	require.True(t, drained, "instrument: the anonymous knowledge-read bucket must be drainable")

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge/views?collection_id="+url.QueryEscape(collectionID))
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"instrument control: a knowledge retrieval call must now be refused (body: %s)", w.Body.String())

	wList := libGet(t, api, "/api/v1/library/"+ws+"/entries?path="+viewVaultRel+"&include_views=true")
	require.Equal(t, http.StatusOK, wList.Code,
		"L15: the listing must not spend the knowledge retrieval budget (body: %s)", wList.Body.String())
}

// --- L17 --------------------------------------------------------------------

// TestLibraryEntriesViews_MutationResponsesCarryNoViewFacts pins L17: write,
// rename, move, copy and upload responses are unchanged — they never set
// is_view or view. The rename/move/copy rows act on the view FILE itself (the
// R2 surface): in this scope those operations still succeed and act on the
// storage file exactly as they would with include_hidden=true — the design
// deliberately adds NO refusal here.
//
// GUARD RULE: expected green on the pre-change code.
func TestLibraryEntriesViews_MutationResponsesCarryNoViewFacts(t *testing.T) {
	api, ws := seedViewListingVault(t)
	storageRel := viewVaultRel + "/.omnipus-vault/views"

	// Rename the view file within the storage folder.
	renamedRel := storageRel + "/invoices--renamed.yaml"
	w := libPostJSON(t, api, "/api/v1/library/"+ws+"/rename",
		`{"from":"`+storageRel+`/invoices--outstanding.yaml","to":"`+renamedRel+`"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	entry := decodeJSON[gen.LibraryEntry](t, w)
	assert.Nil(t, entry.IsView, "L17: a rename response never sets is_view")
	assert.Nil(t, entry.View, "L17: a rename response never sets view")
	assert.Equal(t, renamedRel, entry.Path, "L17: the rename response is the ordinary entry")
	assert.NotContains(t, w.Body.String(), `"is_view"`, "L17: raw rename body")

	// Move the view file out of the storage folder: succeeds today (no R2
	// refusal in this scope), response unchanged.
	w = libPostJSON(t, api, "/api/v1/library/move",
		`{"from_workspace_id":"`+ws+`","from_path":"`+renamedRel+`",`+
			`"to_workspace_id":"`+ws+`","to_path":"`+viewVaultRel+`/loose-view.yaml"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	entry = decodeJSON[gen.LibraryEntry](t, w)
	assert.Nil(t, entry.IsView, "L17: a move response never sets is_view")
	assert.Nil(t, entry.View, "L17: a move response never sets view")
	assert.NotContains(t, w.Body.String(), `"is_view"`, "L17: raw move body")

	// Copy it back.
	w = libPostJSON(t, api, "/api/v1/library/copy",
		`{"from_workspace_id":"`+ws+`","from_path":"`+viewVaultRel+`/loose-view.yaml",`+
			`"to_workspace_id":"`+ws+`","to_path":"`+storageRel+`/invoices--copied.yaml"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	entry = decodeJSON[gen.LibraryEntry](t, w)
	assert.Nil(t, entry.IsView, "L17: a copy response never sets is_view")
	assert.Nil(t, entry.View, "L17: a copy response never sets view")
	assert.NotContains(t, w.Body.String(), `"is_view"`, "L17: raw copy body")

	// Content write of a new file inside the knowledge base.
	w = libPutJSON(t, api, "/api/v1/library/"+ws+"/content",
		`{"path":"`+viewVaultRel+`/written.md","content":"hi","expect_version":"v1:absent"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), `"is_view"`, "L17: a write response never sets is_view")
	assert.NotContains(t, w.Body.String(), `"view":`, "L17: a write response never sets view")

	// Upload.
	w = libUpload(t, api, "/api/v1/library/"+ws+"/upload", map[string]string{"doc.txt": "hi"})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), `"is_view"`, "L17: an upload response never sets is_view")
	assert.NotContains(t, w.Body.String(), `"view":`, "L17: an upload response never sets view")
}
