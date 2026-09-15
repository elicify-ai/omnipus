// Omnipus — regression coverage for the 2026-09-13 UAT finding D-130 at the
// web door: at `limit: 20` (what the SPA always sends) the Notes tab showed
// fifteen records, because a record past the records group's limit was not
// in recordPaths and so fell through the kind=note query as a "note".
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestUAT_D130_NotesGroupNeverContainsARecordCutByTheRecordsLimit reuses the
// inner-overflow vault: three records match "plonktech", the records group
// is capped at 2, and the third record used to appear under Notes.
func TestUAT_D130_NotesGroupNeverContainsARecordCutByTheRecordsLimit(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultInnerOverflow(t)

	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "plonktech", "collection_id": colID, "limit": 2,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)

	require.Len(t, resp.Records, 2, "the records cap is still honoured")
	assert.Empty(t, noteHitPaths(resp),
		"D-130: the vault holds no typeless note, so the Notes group must be empty — "+
			"a record cut by the records limit must not be filed as a note")
	assert.False(t, resp.Complete, "the third record is still disclosed as missing")
	require.NotNil(t, resp.CompleteReason)
	assert.Contains(t, *resp.CompleteReason, "records:", "the reason names the records gap, not a notes count")
}

// TestUAT_D129_CoverageSentenceCountsNotesNotFiles — U-34: "Searched 68 of
// 68 notes" over 42 notes and 26 attachments. The base vault holds two
// markdown notes and one attachment; the pair must say 2 of 2.
func TestUAT_D129_CoverageSentenceCountsNotesNotFiles(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)

	// The fixture's company record matches "aerospace", so the answer carries
	// a real hit — which is exactly the condition FR-053 discloses the
	// coverage pair under (it is withheld only for a complete-and-empty
	// answer, where it would leak a collection's existence).
	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "aerospace", "collection_id": colID, "limit": 20,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)
	require.NotEmpty(t, resp.Records, "the fixture's company record matches")

	// THE PAIR'S PRESENCE IS THE INVARIANT (round-3, 2026-09-14 review): this
	// used to t.Skip when the pair was missing, which made deleting the
	// coverage disclosure entirely keep the suite GREEN — demonstrated live
	// by mutation before this change. If the product ever legitimately stops
	// disclosing the pair for an answer with hits, update this test
	// deliberately; do not restore a skip.
	require.NotNil(t, resp.NotesSearched,
		"notes_searched must be present on an answer with real hits — D-129's count cannot be "+
			"guarded by a test that skips when the number is missing")
	require.NotNil(t, resp.NotesTotalKnown,
		"notes_total_known must be present on an answer with real hits — D-129's count cannot be "+
			"guarded by a test that skips when the number is missing")
	assert.Equal(t, 2, *resp.NotesTotalKnown,
		"D-129: two markdown notes exist; the attachment must not be counted as a note")
	assert.Equal(t, 2, *resp.NotesSearched)
	require.NotNil(t, resp.Statement)
	assert.Contains(t, *resp.Statement, "2 of 2 notes")
}
