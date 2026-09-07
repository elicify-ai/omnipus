// Tests for POST /api/v1/library/{workspace_id}/knowledge/find — the human
// vault search (library-b-c-design-2026-09-07 §C1).
//
// Expected values are derived from the SPEC and the CONTRACT, never from what
// the handler happens to do: field names come from
// contracts/components/schemas/VaultSearch*.yaml, behaviours from §C1 (text +
// records + views, honest empty and index-not-ready states) and from the
// neighbouring search/view endpoints' own rules (auth, out-of-scope isolation).

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// companySchema declares a record type with two plain typed properties, so a
// record hit has real cells to carry.
const companySchema = "schema_version: 1\n" +
	"type: company\n" +
	"properties:\n" +
	"  industry: { type: text }\n" +
	"  stage: { type: text }\n"

// buildVaultSearchVault seeds one knowledge base with three kinds of matchable
// content and returns the api, workspace id and collection id.
//
//   - a PLAIN note whose body carries "Landlock" — for the text-hit and
//     prefix cases (the term is not stemmable from "landlo", so a match on
//     that query can only be a prefix match, round-2's behaviour).
//   - a RECORD note (declares type: company) whose body and property values
//     carry "Vorlex"/"aerospace" — for the record-hit case.
//   - a saved VIEW named "aerospace-companies" — for the view-hit case.
func buildVaultSearchVault(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the vault-search endpoint cannot evaluate records here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Research vault")
	writeNote(t, vault, ".omnipus-vault/records/company.yaml", companySchema)

	// Plain note — lands in the NOTES group only.
	writeNote(t, vault, "security.md",
		"# Security\n\nLandlock and seccomp provide kernel sandboxing on Linux.\n")

	// Record note — lands in the RECORDS group (and, being a note, in NOTES too).
	writeNote(t, vault, "companies/vorlex.md",
		"---\ntype: company\nid: CO-1\nindustry: aerospace\nstage: series-a\n---\n"+
			"# Vorlex Dynamics\n\nVorlex builds aerospace systems.\n")

	// Saved view — matched by name.
	writeNote(t, vault, ".omnipus-vault/views/aerospace-companies.yaml",
		"name: aerospace-companies\nlabel: Aerospace companies\ntype: company\nlayout: table\n")

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	require.NoError(t, err)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// vaultFindPost drives the real dispatch (HandleLibraryTree), the same entry
// every other knowledge test uses.
func vaultFindPost(t *testing.T, api *restAPI, ws string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/library/"+ws+"/knowledge/find", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleLibraryTree(w, r)
	return w
}

func noteHitPaths(resp gen.VaultSearchResponse) []string {
	out := make([]string, 0, len(resp.Notes))
	for _, n := range resp.Notes {
		out = append(out, n.Path)
	}
	return out
}

func recordHitPaths(resp gen.VaultSearchResponse) []string {
	out := make([]string, 0, len(resp.Records))
	for _, rr := range resp.Records {
		out = append(out, rr.Path)
	}
	return out
}

// TestVaultSearch_NoteBodyMatchReturnsSnippet — a query matching a note by body
// text returns it in the notes group WITH a snippet re-read from the file.
func TestVaultSearch_NoteBodyMatchReturnsSnippet(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	w := vaultFindPost(t, api, ws, map[string]any{"query": "seccomp", "collection_id": colID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	assert.Contains(t, noteHitPaths(resp), "security.md",
		"a note whose body contains the query must appear in the notes group")

	var hit *gen.VaultSearchNoteHit
	for i := range resp.Notes {
		if resp.Notes[i].Path == "security.md" {
			hit = &resp.Notes[i]
		}
	}
	require.NotNil(t, hit)
	require.NotNil(t, hit.Snippet, "a body-text match must carry a snippet")
	assert.Contains(t, *hit.Snippet, "seccomp",
		"the snippet is re-read from the file and must contain the matched term")
}

// TestVaultSearch_RecordMatchReturnsRecordWithCells — a query matching a record
// returns it in the records group, carrying its typed property values as cells.
func TestVaultSearch_RecordMatchReturnsRecordWithCells(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	w := vaultFindPost(t, api, ws, map[string]any{"query": "aerospace", "collection_id": colID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	assert.Contains(t, recordHitPaths(resp), "companies/vorlex.md",
		"a record note matching the query must appear in the records group")

	var rec *gen.VaultSearchRecordHit
	for i := range resp.Records {
		if resp.Records[i].Path == "companies/vorlex.md" {
			rec = &resp.Records[i]
		}
	}
	require.NotNil(t, rec)
	require.NotNil(t, rec.RecordType)
	assert.Equal(t, "company", *rec.RecordType)
	// The typed property values ride along as cells: find the industry it
	// matched on among them.
	var industry string
	for _, c := range rec.Cells {
		if c.Property == "industry" {
			industry = c.Value
		}
	}
	assert.Equal(t, "aerospace", industry,
		"the record hit must carry the typed property value that matched")
}

// TestVaultSearch_ViewNameMatchReturnsView — a query matching a saved view's
// name returns it in the views group.
func TestVaultSearch_ViewNameMatchReturnsView(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	w := vaultFindPost(t, api, ws, map[string]any{"query": "aerospace", "collection_id": colID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Views, 1, "the one matching view must be returned")
	assert.Equal(t, "aerospace-companies", resp.Views[0].View)
	assert.Equal(t, "Aerospace companies", resp.Views[0].Label)
}

// TestVaultSearch_PrefixMatchesRoundTwo — a query that is a strict PREFIX of a
// body word (never a stem of it) still matches, proving the endpoint inherits
// the engine's round-2 prefix matching rather than re-implementing exact match.
func TestVaultSearch_PrefixMatchesRoundTwo(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	w := vaultFindPost(t, api, ws, map[string]any{"query": "landlo", "collection_id": colID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	assert.Contains(t, noteHitPaths(resp), "security.md",
		`the prefix "landlo" must match the body word "Landlock" (round-2 prefix matching)`)
}

// TestVaultSearch_NoMatchesIsCleanEmpty — a query nothing matches is an EMPTY,
// COMPLETE result, not an error and not a false incompleteness.
func TestVaultSearch_NoMatchesIsCleanEmpty(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "quuxzzznothingmatchesthis", "collection_id": colID,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	assert.Empty(t, resp.Notes)
	assert.Empty(t, resp.Records)
	assert.Empty(t, resp.Views)
	assert.True(t, resp.Complete, "a genuine miss over a built index is complete, not incomplete")
	assert.Nil(t, resp.CompleteReason, "a complete result carries no reason")
	// Empty arrays, never null — a client maps over them without a nil check.
	assert.NotNil(t, resp.Notes)
	assert.NotNil(t, resp.Records)
	assert.NotNil(t, resp.Views)
}

// TestVaultSearch_EmptyQueryIsBadRequest — a blank query is a 400, not an empty
// answer, so the caller learns they sent nothing to search for.
func TestVaultSearch_EmptyQueryIsBadRequest(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	w := vaultFindPost(t, api, ws, map[string]any{"query": "   ", "collection_id": colID})
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

// TestVaultSearch_OutOfScopeCollectionIsEmptyNotError — a collection_id this
// workspace cannot address is an EMPTY, complete result, never a permission
// error, so the error channel cannot be used to probe for other workspaces'
// collections (US-9 / FR-053).
func TestVaultSearch_OutOfScopeCollectionIsEmptyNotError(t *testing.T) {
	api, ws, _ := buildVaultSearchVault(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "seccomp", "collection_id": "kb_0000000000000000",
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)
	assert.Empty(t, resp.Notes)
	assert.Empty(t, resp.Records)
	assert.Empty(t, resp.Views)
	assert.True(t, resp.Complete)
}

// TestVaultSearch_RefusesUnauthenticatedCallsLikeItsNeighbours drives the REAL
// registered middleware chain (a.withUploadAuth, the wrapper every
// /api/v1/library/{workspace_id}/... endpoint is registered under), not the
// bare HandleLibraryTree shortcut the other tests use — the same guard the view
// endpoint's own auth test applies, for the same reason: that shortcut would
// hide an auth regression on this one endpoint while its neighbours stayed
// protected.
func TestVaultSearch_RefusesUnauthenticatedCallsLikeItsNeighbours(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	guarded := api.withUploadAuth(api.HandleLibraryTree)
	raw, err := json.Marshal(map[string]any{"query": "seccomp", "collection_id": colID})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/library/"+ws+"/knowledge/find", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	guarded(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"an unauthenticated caller must be refused before the search is ever run")
}
