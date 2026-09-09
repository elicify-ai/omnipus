// Regression tests for two honesty findings in POST .../knowledge/find
// (2026-09-08 code review, found independently by two reviewers):
//
//   - F1: vaultSearchRecords broke out of its per-declared-record-type loop
//     once the cross-type merge cap was hit, WITHOUT ever marking the result
//     incomplete — a knowledge base declaring [company, deal, person, task]
//     where "company" alone fills the limit left the other three types NEVER
//     QUERIED, yet the response still rendered "Searched the whole of this
//     knowledge base; its index was complete at query time." The twin defect
//     lived in vaultSearchViewHits' own local scan, which had no
//     completeness signal at all.
//   - F10: a failed knowledgefind.Find call (runVaultSearchFind) was
//     discarded with no log line — an operator investigating a recurring
//     index fault had nothing to see, request after request, indistinguishable
//     from the engine's own ordinary "not ready yet" refusal.

package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// dealSchema declares a SECOND record type, sorting alphabetically AFTER
// "company" (env.Schemas.Types() is sorted by vaultSearchRecords itself) —
// the ordering F1's regression depends on to force "company" to be queried
// before "deal" is ever reached.
const dealSchema = "schema_version: 1\n" +
	"type: deal\n" +
	"properties:\n" +
	"  stage: { type: text }\n"

// buildVaultSearchVaultMultiType seeds a vault with TWO declared record
// types (company, deal), each holding one record matching the term
// "zorbcorp", plus two saved views that both match the UNRELATED term
// "quixport". buildVaultSearchVault's single-type/single-view fixture cannot
// exercise F1 — the merge cap needs at least two matchable items per group
// to prove the second one was never even reached. The two terms are
// deliberately DISJOINT (never both matched by one query) so the records
// half and the views half can be exercised — and mutation-tested — in
// complete isolation from each other: a query for one term can never let the
// OTHER group's own (separate) completeness signal mask a mutation in the
// group actually under test.
func buildVaultSearchVaultMultiType(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the vault-search endpoint cannot evaluate records here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Multi-type vault")
	writeNote(t, vault, ".omnipus-vault/records/company.yaml", companySchema)
	writeNote(t, vault, ".omnipus-vault/records/deal.yaml", dealSchema)

	// One record per type, both carrying the RECORDS-only term "zorbcorp".
	writeNote(t, vault, "companies/acme.md",
		"---\ntype: company\nid: CO-9\nindustry: zorbcorp\nstage: series-a\n---\n"+
			"# Acme\n\nAcme is a zorbcorp company.\n")
	writeNote(t, vault, "deals/acme-deal.md",
		"---\ntype: deal\nid: DE-9\nstage: zorbcorp\n---\n"+
			"# Acme Deal\n\nA zorbcorp deal.\n")

	// Two views, both matching the VIEWS-only term "quixport" by name,
	// sorted alphabetically.
	writeNote(t, vault, ".omnipus-vault/views/quixport-alpha.yaml",
		"name: quixport-alpha\nlabel: Quixport Alpha\nlayout: table\n")
	writeNote(t, vault, ".omnipus-vault/views/quixport-beta.yaml",
		"name: quixport-beta\nlabel: Quixport Beta\nlayout: table\n")

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	require.NoError(t, err)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// TestVaultSearch_RecordsCompleteFalseWhenATypeIsNeverQueried is F1's
// records half. "company" sorts before "deal", so limit:1 guarantees
// "company" alone fills the merge cap and "deal" is never even queried. The
// "zorbcorp" term matches no view, so views stays honestly complete.
//
// Both matching record files are also plain notes, so the NOTES kind's OWN
// Find call is independently truncated at the same shared limit and ALSO
// reports complete:false (records/knowledgefind/assemble.go's
// Shown<Evaluated rule) — an unrelated, pre-existing signal that would flip
// out.Complete to false even with the records fix reverted, and so cannot by
// itself prove the records fix engaged. What DOES discriminate: records is
// assembled BEFORE notes in buildVaultSearchResult, so when the records fix
// sets a CompleteReason, notes' own mergeVaultSearchCompleteness sees
// CompleteReason already non-nil and leaves it alone (the first reason
// wins) — so the SURFACED reason names "records:" specifically only when
// the records fix is the one that actually fired.
func TestVaultSearch_RecordsCompleteFalseWhenATypeIsNeverQueried(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultMultiType(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "zorbcorp", "collection_id": colID, "limit": 1,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Records, 1, "the merge cap must still be honoured")
	require.Empty(t, resp.Views, "isolation check: this query must not also match any view")
	assert.False(t, resp.Complete,
		"a record type that was NEVER queried because the merge cap was hit on an earlier type "+
			"must not be reported as a complete search")
	require.NotNil(t, resp.CompleteReason, "an incomplete verdict must carry a reason")
	assert.Contains(t, *resp.CompleteReason, "records:",
		"the SURFACED reason must specifically name the records coverage gap (records is assembled "+
			"before notes, so its reason wins when it fires) — got: %s", *resp.CompleteReason)
}

// TestVaultSearch_RecordsCompleteWhenEveryTypeFitsUnderTheLimit is the
// negative control: with room for both types under the limit, every
// declared record type IS queried, and the verdict stays honestly complete.
func TestVaultSearch_RecordsCompleteWhenEveryTypeFitsUnderTheLimit(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultMultiType(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "zorbcorp", "collection_id": colID, "limit": 20,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Records, 2, "both record types must be represented when nothing is capped")
	assert.True(t, resp.Complete,
		"every declared record type fit under the limit and was actually queried — "+
			"the verdict must stay complete")
}

// TestVaultSearch_ViewsCompleteFalseWhenScanCappedBeforeEveryViewChecked is
// F1's views half: vaultSearchViewHits' local scan broke at `limit` without
// checking later-sorted views for a match at all, and the caller had no
// completeness signal to fold that into. The "quixport" term matches no
// record, so records stays honestly complete — any incompleteness observed
// here can only have come from the views fix.
func TestVaultSearch_ViewsCompleteFalseWhenScanCappedBeforeEveryViewChecked(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultMultiType(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "quixport", "collection_id": colID, "limit": 1,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Views, 1, "the merge cap must still be honoured")
	require.Empty(t, resp.Records, "isolation check: this query must not also match any record")
	assert.False(t, resp.Complete,
		"a saved view sorted after the cap point was never even checked against the query — "+
			"the verdict must not claim completeness")
	require.NotNil(t, resp.CompleteReason, "an incomplete verdict must carry a reason")
	assert.Contains(t, *resp.CompleteReason, "views:",
		"the reason must specifically name the views coverage gap — got: %s", *resp.CompleteReason)
}

// buildVaultSearchVaultInnerOverflow seeds a vault with ONE record type
// ("company", 1 match) and a SECOND type ("deal", 2 matches), all three
// matching "plonktech". With limit:2, company contributes 1 row; deal's OWN
// Find call is then itself COMPLETE (2 matches, engine cap 2, no per-type
// truncation) and returns 2 rows — but only 1 fits before the cross-type
// merge cap is reached. This is the "type WAS queried and its own search WAS
// complete, but the merge cap still dropped one of its rows" case
// (vaultSearchRecords' INNER `for i := range resp.Rows` break), distinct
// from buildVaultSearchVaultMultiType's OUTER "type never queried at all"
// case above.
func buildVaultSearchVaultInnerOverflow(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the vault-search endpoint cannot evaluate records here")
	}

	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Inner-overflow vault")
	writeNote(t, vault, ".omnipus-vault/records/company.yaml", companySchema)
	writeNote(t, vault, ".omnipus-vault/records/deal.yaml", dealSchema)

	writeNote(t, vault, "companies/plonk-co.md",
		"---\ntype: company\nid: CO-8\nindustry: plonktech\nstage: seed\n---\n"+
			"# Plonk Co\n\nPlonk Co is a plonktech company.\n")
	writeNote(t, vault, "deals/plonk-deal-a.md",
		"---\ntype: deal\nid: DE-8\nstage: plonktech\n---\n"+
			"# Plonk Deal A\n\nA plonktech deal.\n")
	writeNote(t, vault, "deals/plonk-deal-b.md",
		"---\ntype: deal\nid: DE-9\nstage: plonktech\n---\n"+
			"# Plonk Deal B\n\nAnother plonktech deal.\n")

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)
	_, err = vaultprops.Sync(context.Background(), api.homePath, realVault, vaultprops.SyncOptions{})
	require.NoError(t, err)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// TestVaultSearch_RecordsCompleteFalseWhenATypesOwnRowsOverflowTheMergeCap
// is F1's INNER-loop case: "deal"'s own Find call is itself complete (2
// matches, no per-type truncation), but the cross-type merge cap drops one
// of its two legitimate rows. This pins the inner `for i := range resp.Rows`
// break in vaultSearchRecords, distinct from the outer per-type break the
// test above pins.
func TestVaultSearch_RecordsCompleteFalseWhenATypesOwnRowsOverflowTheMergeCap(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultInnerOverflow(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "plonktech", "collection_id": colID, "limit": 2,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Records, 2, "the merge cap must still be honoured (1 company + 1 of 2 deals)")
	assert.False(t, resp.Complete,
		"one of deal's two legitimate, non-truncated rows was dropped by the merge cap — "+
			"the verdict must not claim completeness")
	require.NotNil(t, resp.CompleteReason, "an incomplete verdict must carry a reason")
	assert.Contains(t, *resp.CompleteReason, "records:",
		"the SURFACED reason must specifically name the records coverage gap — got: %s", *resp.CompleteReason)
}

// TestVaultSearch_ViewsCompleteWhenBothFitUnderTheLimit is the views-side
// negative control, mirroring the records one above.
func TestVaultSearch_ViewsCompleteWhenBothFitUnderTheLimit(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultMultiType(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "quixport", "collection_id": colID, "limit": 20,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Views, 2, "both views must be represented when nothing is capped")
	assert.True(t, resp.Complete,
		"both matching views fit under the limit and were actually checked — the verdict must stay complete")
}

// buildVaultSearchVaultBrokenView seeds a vault whose views/ directory holds
// ONE view file that fails to even PARSE, plus an ordinary note (I6,
// 2026-09-09 code review). The broken file declares no `name:` key, which
// records.ParseView rejects as RejectViewMissingName — a genuine LOAD
// failure, distinct from rest_knowledge_view_test.go's "broken.yaml" fixture
// (which parses fine and is refused only at SERVE time, for being disabled).
// No schema/record fixture is needed here at all: the point being pinned is
// that env.ViewReport — populated regardless of whether any record type is
// even declared — must be consulted by the search surface, the same way
// rest_knowledge_view.go already consults it for one named view.
func buildVaultSearchVaultBrokenView(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Broken-view vault")

	writeNote(t, vault, ".omnipus-vault/views/broken.yaml", "label: Broken\nlayout: table\n")
	writeNote(t, vault, "notes/hello.md", "# Hello\n\nquibblewatt is mentioned here and nowhere else.\n")

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// TestVaultSearch_ViewLoadFailureMakesResultIncomplete is I6 (2026-09-09 code
// review): vaultSearchViewHits iterated env.Views.Views() — the
// successfully-PARSED set only — with no way to learn that a views/ file
// existed but failed to load at all, so a broken view file was silently
// invisible to both the hit list AND the completeness verdict. The response
// asserted "Searched the whole of this knowledge base; its index was
// complete at query time" even though a view file in that very knowledge
// base was never even readable enough to check against the query — a false
// completeness claim the endpoint makes EXPLICITLY, in its own response
// text. env.ViewReport (pkg/vaultprops/find_env.go) already carried this
// information and is already read by the sibling view-result endpoint
// (rest_knowledge_view.go) for exactly this purpose; this pins that the
// search surface now reads it too.
func TestVaultSearch_ViewLoadFailureMakesResultIncomplete(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultBrokenView(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "quibblewatt", "collection_id": colID,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Notes, 1, "the ordinary note must still be found normally")
	assert.Empty(t, resp.Views, "the broken view file can never appear as a hit — it never parsed")
	assert.False(t, resp.Complete,
		"a saved view file that failed to load means the views group was never actually checked "+
			"against the query — the verdict must not claim completeness")
	require.NotNil(t, resp.CompleteReason, "an incomplete verdict must carry a reason")
	assert.Contains(t, *resp.CompleteReason, "views:",
		"the reason must specifically name the views load failure — got: %s", *resp.CompleteReason)
	require.NotNil(t, resp.Statement)
	assert.NotEqual(t, vaultSearchCompleteStatement, *resp.Statement,
		"the render-ready statement must not claim the search covered everything when a view file "+
			"in this very knowledge base never even loaded")
}

// buildVaultSearchVaultBrokenSchema seeds a vault whose records/ directory
// holds ONE record-type schema file that fails to even PARSE, plus an
// ordinary note (I6's "one layer down" half, 2026-09-09 code review). The
// broken file declares no `type:` key, which records.LoadSchemas rejects as
// RejectMissingType — a genuine LOAD failure. Before this fix,
// vaultprops.OpenFindEnv discarded records.LoadSchemas' *SchemaLoadReport
// entirely, so this rejection could not surface anywhere at all.
func buildVaultSearchVaultBrokenSchema(t *testing.T) (*restAPI, string, string) {
	t.Helper()
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Broken-schema vault")

	writeNote(t, vault, ".omnipus-vault/records/broken.yaml", "schema_version: 1\nproperties:\n  foo: { type: text }\n")
	writeNote(t, vault, "notes/hello.md", "# Hello\n\nquibblewatt is mentioned here and nowhere else.\n")

	realVault, err := filepath.EvalSymlinks(vault)
	require.NoError(t, err)
	indexKnowledgeBase(t, api.homePath, realVault)

	return api, ws, collectionIDOf(t, api, ws, "vault")
}

// TestVaultSearch_SchemaLoadFailureMakesRecordsIncomplete is I6's "one layer
// down" half: env.SchemaReport is now threaded through vaultprops.FindEnv
// (previously discarded outright), and vaultSearchRecords must consult it —
// a record-type schema that never loaded is a record type this endpoint
// cannot truthfully say does not match the query, so the verdict must not
// claim completeness.
func TestVaultSearch_SchemaLoadFailureMakesRecordsIncomplete(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultBrokenSchema(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "quibblewatt", "collection_id": colID,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Notes, 1, "the ordinary note must still be found normally")
	assert.Empty(t, resp.Records, "a broken schema file declares no usable type, so it never yields a record hit")
	assert.False(t, resp.Complete,
		"a record-type schema file that failed to load means that type was never even declared, "+
			"let alone searched — the verdict must not claim completeness")
	require.NotNil(t, resp.CompleteReason, "an incomplete verdict must carry a reason")
	assert.Contains(t, *resp.CompleteReason, "records:",
		"the reason must specifically name the records/schema load failure — got: %s", *resp.CompleteReason)
}

// TestVaultSearchFind_ErrorIsLogged is F10 (2026-09-08 code review):
// runVaultSearchFind discarded knowledgefind.Find's error with NO log line
// at all — an operator watching for a recurring index fault saw nothing,
// request after request, indistinguishable from the engine's own ordinary
// "not ready yet" refusal. knowledgefind.Deps.Text is documented "REQUIRED,
// not optional"; leaving it nil deterministically drives Find's OWN
// err != nil return (knowledgefind/find.go's Text-nil branch: "return
// refusalResponse(...), ref" with ref non-nil) rather than depending on a
// harder-to-reach internal fault, while still exercising the exact
// err != nil branch this finding is about. Follows this package's
// established file-logging capture pattern (see
// TestPromptGuard_ReloadTimeout's use of the same
// DisableConsole/SetLevel/EnableFileLogging sequence).
func TestVaultSearchFind_ErrorIsLogged(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "vault-search-find-error.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	env := vaultprops.FindEnv{Deps: knowledgefind.Deps{}}
	resp, ready := runVaultSearchFind(context.Background(), env, "anything", "", gen.VaultFindRequestKindNote, 10)

	assert.False(t, ready, "a Find call that errors must still report not-ready to the caller")
	assert.True(t, resp.Refused, "the caller-facing refusal shape is unaffected by this log-only fix")

	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "vault search find failed",
		"a genuine knowledgefind.Find error must be logged for operator visibility — got: %s", string(logged))
}
