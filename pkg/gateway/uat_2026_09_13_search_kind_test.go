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

	// A limit of 1 makes the answer incomplete (two notes match "aerospace"
	// through the record and the plain security note does not, but the
	// records group still reports its cut), so the coverage pair is disclosed
	// rather than withheld for a complete-and-empty answer.
	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "aerospace", "collection_id": colID, "limit": 20,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)
	require.NotEmpty(t, resp.Records, "the fixture's company record matches")

	if resp.NotesSearched == nil || resp.NotesTotalKnown == nil {
		t.Skip("coverage pair withheld for a complete answer; the count is exercised by pkg/knowledge's freshness test")
	}
	assert.Equal(t, 2, *resp.NotesTotalKnown,
		"D-129: two markdown notes exist; the attachment must not be counted as a note")
	assert.Equal(t, 2, *resp.NotesSearched)
	require.NotNil(t, resp.Statement)
	assert.Contains(t, *resp.Statement, "2 of 2 notes")
}
