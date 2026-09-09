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
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// buildVaultSearchVault seeds one knowledge base with four kinds of matchable
// content and returns the api, workspace id and collection id.
//
//   - a PLAIN note whose body carries "Landlock" — for the text-hit and
//     prefix cases (the term is not stemmable from "landlo", so a match on
//     that query can only be a prefix match, round-2's behaviour).
//   - a RECORD note (declares type: company) whose body and property values
//     carry "Vorlex"/"aerospace" — for the record-hit case.
//   - a saved VIEW named "aerospace-companies" — for the view-hit case.
//   - an ATTACHMENT ("img/diagram-v3.png") findable by filename only — for
//     the CRIT-001 parity case (ADR-081). Its bytes are shaped like a note on
//     purpose: an implementation that opened it would have something
//     quotable, and no test here reads its content.
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

	// Attachment — a non-markdown file under the vault indexes as an
	// attachment (pkg/knowledge/index.go::indexAttachment), matched by name
	// only. The query term ("diagram-v3") does not collide with any other
	// fixture's matchable content above.
	writeNote(t, vault, "img/diagram-v3.png", "binary-ish bytes, never opened\n")

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

// TestVaultSearch_QueryBoundEnforcedRegardlessOfValidateInbound is I3
// (2026-09-09 code review): the contract's query maxLength:1024
// (contracts/components/schemas/VaultSearchRequest.yaml) was declared but
// enforced NOWHERE — decodeAndValidate's schema pass only runs when
// gateway.validate_inbound is true, and that flag DEFAULTS FALSE
// (buildLibraryTestAPI leaves it at that zero value), so the declared bound
// was purely decorative in a default install. The handler itself only ever
// checked query for emptiness. Left unenforced, an over-long query reaches
// knowledgefind.Find with no tokenisation cap, and — unlike the sibling
// file-search endpoint (rest_library_files_search.go's F4 fix, which this
// test mirrors) — this endpoint takes no walk semaphore at all, so the
// length bound is the ONLY defence against the cost that follows.
func TestVaultSearch_QueryBoundEnforcedRegardlessOfValidateInbound(t *testing.T) {
	t.Run("a query over the 1024-char cap is rejected 400 at the validate_inbound default", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)
		require.False(t, api.agentLoop.GetConfig().Gateway.ValidateInbound,
			"this test must exercise the unvalidated default, not the schema-validated path")

		w := vaultFindPost(t, api, ws, map[string]any{
			"query":         strings.Repeat("a", vaultSearchMaxQueryLength+1),
			"collection_id": "kb_0000000000000000",
		})
		assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	})

	t.Run("a query exactly at the 1024-char cap is not rejected for length", func(t *testing.T) {
		api, ws := buildLibraryTestAPI(t)

		w := vaultFindPost(t, api, ws, map[string]any{
			"query":         strings.Repeat("a", vaultSearchMaxQueryLength),
			"collection_id": "kb_0000000000000000",
		})
		// An unknown collection_id is an empty-but-complete 200 (US-9/FR-053),
		// never a 400 — proving the cap is exactly 1024, not lower, and that
		// this request cleared the length check to reach that path.
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})
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

// ---------------------------------------------------------------------------
// ADR-081 spec test 28 — TestVaultSearch_HonestyAndAttachments
//
// Additive compat (the response still satisfies the pre-existing assertions
// above), the Attachments group, clamp disclosure, excerpt_unavailable and a
// handler-authored statement. The propindex-less carve-out is a SEPARATE
// build-tag variant (rest_knowledge_find_propindexless_test.go, gated
// `records_no_sqlite`) — this file runs only on a propindex-capable build
// (buildVaultSearchVault itself skips otherwise), so it cannot also exercise
// that carve-out.
// ---------------------------------------------------------------------------

// TestVaultSearch_HonestyAndAttachments is spec test 28.
func TestVaultSearch_HonestyAndAttachments(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	t.Run("attachment filename match returns the attachments group", func(t *testing.T) {
		w := vaultFindPost(t, api, ws, map[string]any{"query": "diagram-v3", "collection_id": colID})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.VaultSearchResponse](t, w)

		require.NotNil(t, resp.Attachments, "the handler always sends the attachments array")
		require.Len(t, *resp.Attachments, 1, "the attachment must be findable by filename (ADR-081 CRIT-001 parity)")
		hit := (*resp.Attachments)[0]
		assert.Equal(t, "img/diagram-v3.png", hit.Path)
		assert.Equal(t, "diagram-v3.png", hit.Name, "name is the basename — what the query matched against")

		// And nothing was read out of the attachment on the way past — its
		// bytes are never opened (FR-039a's principle, carried over).
		assert.NotContains(t, w.Body.String(), "never opened")
	})

	t.Run("attachments is a real empty array, never null, on a non-matching query", func(t *testing.T) {
		w := vaultFindPost(t, api, ws, map[string]any{
			"query": "quuxzzznothingmatchesthis", "collection_id": colID,
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.VaultSearchResponse](t, w)
		require.NotNil(t, resp.Attachments)
		assert.Empty(t, *resp.Attachments)
		assert.Contains(t, w.Body.String(), `"attachments":[]`)
	})

	t.Run("a limit above the server cap is clamped and the clamp is disclosed", func(t *testing.T) {
		const asked = 400
		w := vaultFindPost(t, api, ws, map[string]any{
			"query": "seccomp", "collection_id": colID, "limit": asked,
		})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.VaultSearchResponse](t, w)

		require.NotNil(t, resp.LimitClamped)
		assert.True(t, *resp.LimitClamped, "the clamp is reported, never silent")
		require.NotNil(t, resp.LimitRequested)
		assert.Equal(t, asked, *resp.LimitRequested, "the caller can see exactly what was asked for")
		require.NotNil(t, resp.Statement)
		assert.Contains(t, *resp.Statement, fmt.Sprintf("%d", asked))
		assert.Contains(t, *resp.Statement, fmt.Sprintf("%d", vaultSearchMaxLimit))
	})

	t.Run("a complete search carries a handler-authored statement and no clamp", func(t *testing.T) {
		w := vaultFindPost(t, api, ws, map[string]any{"query": "seccomp", "collection_id": colID})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.VaultSearchResponse](t, w)

		require.NotNil(t, resp.Statement, "the statement is required, never absent")
		assert.NotEmpty(t, *resp.Statement)
		assert.Nil(t, resp.LimitClamped, "an unclamped request carries no clamp flag")
		assert.Nil(t, resp.LimitRequested)
	})

	t.Run("excerpt_unavailable is set when the term is no longer where the index remembers it", func(t *testing.T) {
		vault := filepath.Join(workDir(api, ws), "vault")
		// The index still holds "security.md" ranked for "seccomp" (built by
		// buildVaultSearchVault); overwrite the file on disk WITHOUT
		// re-indexing, so the re-read at query time can no longer locate the
		// term — the "match moved" case MV-9's boolean collapses onto true.
		writeNote(t, vault, "security.md", "# Security\n\nThis paragraph no longer mentions the old term.\n")

		w := vaultFindPost(t, api, ws, map[string]any{"query": "seccomp", "collection_id": colID})
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		resp := decodeJSON[gen.VaultSearchResponse](t, w)

		var hit *gen.VaultSearchNoteHit
		for i := range resp.Notes {
			if resp.Notes[i].Path == "security.md" {
				hit = &resp.Notes[i]
			}
		}
		require.NotNil(t, hit, "the stale text-index rank still returns the hit")
		assert.Nil(t, hit.Snippet, "no fabricated excerpt when the term cannot be re-located")
		require.NotNil(t, hit.ExcerptUnavailable)
		assert.True(t, *hit.ExcerptUnavailable)
	})
}

// buildVaultSearchVaultNoPropsSync is a real-UAT reproduction fixture:
// content is indexed into the TEXT index (indexKnowledgeBase / SyncTracked)
// exactly as buildVaultSearchVault's is, but — unlike that helper —
// vaultprops.Sync is deliberately never called, so the properties index file
// never comes into existence for this collection and openFindStore
// (pkg/vaultprops/find_tool.go) legitimately returns nil, the same way it
// does for any collection nobody has run check_integrity/mount-time indexing
// against yet. That is the "ordinary, supported" state a real vault sits in
// between mounting and its first properties sync — not a build-incapable
// platform (records.PropertyIndexAvailable stays true here) — and it is
// exactly the state the shipped binary was observed in during UAT: a PDF
// attachment came back inside the Notes group while Attachments read empty.
func buildVaultSearchVaultNoPropsSync(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the propindex-less carve-out is " +
			"covered separately by rest_knowledge_find_propindexless_test.go")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Research vault")

	// A note and an attachment sharing one matchable term, mirroring the UAT
	// report byte for byte: "quarterly-review.md" / "assets/quarterly-
	// contract.pdf", both findable on "quarterly". The PDF's extension alone
	// is what classifies it as an attachment (pkg/knowledge/scan.go's
	// ScanKindFor) — its bytes are never opened either way (FR-039a).
	writeNote(t, vault, "quarterly-review.md",
		"# Quarterly Review\n\nOur quarterly numbers were strong this cycle.\n")
	writeNote(t, vault, "assets/quarterly-contract.pdf", "binary-ish bytes, never opened\n")

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	// No vaultprops.Sync call — see the doc comment above.

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// TestVaultSearch_AttachmentNotMisreportedAsNoteBeforePropsSync is the direct
// regression for the UAT finding: with the properties index not yet synced
// (a propindex-CAPABLE build, not the MV-9 platform carve-out), a query
// matching both a note and an attachment by name must put each in its OWN
// group — never label the attachment a note, and never leave Attachments
// empty for a hit the text index actually holds.
func TestVaultSearch_AttachmentNotMisreportedAsNoteBeforePropsSync(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultNoPropsSync(t)

	w := vaultFindPost(t, api, ws, map[string]any{"query": "quarterly", "collection_id": colID})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.NotNil(t, resp.Attachments, "the handler always sends the attachments array")
	assert.Len(t, *resp.Attachments, 1,
		"the attachment must be findable by filename even before the properties index has ever synced")
	if len(*resp.Attachments) == 1 {
		hit := (*resp.Attachments)[0]
		assert.Equal(t, "assets/quarterly-contract.pdf", hit.Path)
		assert.Equal(t, "quarterly-contract.pdf", hit.Name)
	}

	assert.NotContains(t, noteHitPaths(resp), "assets/quarterly-contract.pdf",
		"an attachment must never be reported as a note")
	assert.Len(t, resp.Notes, 1, "the attachment must not also appear in the notes group")
	if len(resp.Notes) > 0 {
		assert.Equal(t, "quarterly-review.md", resp.Notes[0].Path)
	}

	assert.True(t, resp.Complete,
		"a plain-word note+attachment query needs nothing from the (unsynced) properties index, "+
			"so it must answer complete — got reason %v", resp.CompleteReason)
}

// TestVaultSearchNoteHit_ExcerptUnavailableOnUnreadableFile covers the other
// half of "the snippet could not be produced" — the file itself cannot be
// read — as a direct unit test of vaultSearchNoteHit, which is the one place
// both causes (moved term, unreadable file) collapse onto the same boolean
// (MV-9, R2-MIN-010).
func TestVaultSearchNoteHit_ExcerptUnavailableOnUnreadableFile(t *testing.T) {
	row := &gen.VaultFindRow{Path: "does-not-exist.md", Title: "Ghost"}
	hit := vaultSearchNoteHit(t.TempDir(), row, "anything")

	assert.Equal(t, "does-not-exist.md", hit.Path)
	assert.Nil(t, hit.Snippet)
	require.NotNil(t, hit.ExcerptUnavailable)
	assert.True(t, *hit.ExcerptUnavailable)
}

// TestVaultSearchStatement_ComposesEachHonestyClause is a pure-function unit
// test of vaultSearchStatement's composition rules, covering DS-4's full
// index-state matrix — including "building, total unknown", which is not
// reachable through the real knowledgefind engine (its freshness surface
// always reports notes_searched and notes_total_known together, never one
// without the other — see the exit report), so it is exercised here directly
// against a hand-built response rather than through a live search.
func TestVaultSearchStatement_ComposesEachHonestyClause(t *testing.T) {
	searched, total := 4120, 5600
	t.Run("complete, no coverage numbers", func(t *testing.T) {
		got := vaultSearchStatement(gen.VaultSearchResponse{Complete: true})
		assert.Contains(t, got, "whole of this knowledge base")
	})

	t.Run("building, known total renders X of Y", func(t *testing.T) {
		got := vaultSearchStatement(gen.VaultSearchResponse{
			Complete: false, NotesSearched: &searched, NotesTotalKnown: &total,
		})
		assert.Contains(t, got, "4120 of 5600")
	})

	t.Run("building, unknown total renders X so far with no denominator", func(t *testing.T) {
		got := vaultSearchStatement(gen.VaultSearchResponse{
			Complete: false, NotesSearched: &searched,
		})
		assert.Contains(t, got, "4120")
		assert.Contains(t, got, "so far")
		assert.NotContains(t, got, "4120 of", "never invent a denominator (FR-036)")
	})

	t.Run("clamp disclosure is appended", func(t *testing.T) {
		clamped, requested := true, 400
		got := vaultSearchStatement(gen.VaultSearchResponse{
			Complete: true, LimitClamped: &clamped, LimitRequested: &requested,
		})
		assert.Contains(t, got, "400")
		assert.Contains(t, got, fmt.Sprintf("%d", vaultSearchMaxLimit))
	})
}

// ---------------------------------------------------------------------------
// ADR-081 spec test 29 — TestKnowledgeSearchRouteRetired404 (US-5/FR-011)
// ---------------------------------------------------------------------------

// TestKnowledgeSearchRouteRetired404 asserts the retired
// POST .../knowledge/search route answers 404 — the handler, its wire types
// and its REST case are gone, and the shared dispatcher's default case is
// http.NotFound.
func TestKnowledgeSearchRouteRetired404(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)

	raw, err := json.Marshal(map[string]any{"query": "anything", "collection_id": "kb_0000000000000000"})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost,
		"/api/v1/library/"+ws+"/knowledge/search", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	api.HandleLibraryTree(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code,
		"the retired endpoint must answer 404, not fall through to some other handler")
}
