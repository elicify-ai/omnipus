// Regression coverage for two 2026-09-14 leftovers on POST .../knowledge/find:
//
//   - Excerpt highlighting after accent folding (S4 note under U-34): the
//     matcher folds accents (`cafe` finds "Café") but the excerpt scanner only
//     lower-cased, so the hit rendered "No excerpt available".
//   - D-138: the records truncation sentence quoted the RECORD TYPE as if it
//     were the query (`more "decision" record hits exist beyond the result
//     limit`), which reads like a stale query string.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestVaultSearchSnippet_FoldsAccentsLikeTheMatcher — an excerpt must be
// found by the SAME fold the text index applies (lower-case, Unicode NFC,
// ASCII folding), and the window must be cut from the ORIGINAL bytes.
func TestVaultSearchSnippet_FoldsAccentsLikeTheMatcher(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o600))
	}
	write("cafe-nfc.md", "# Notes\n\nWe met at the Café du Coin on Tuesday.\n")
	write("cafe-nfd.md", "# Notes\n\nWe met at the Café du Coin on Tuesday.\n") // NFD: e + combining acute
	write("zurich.md", "# Travel\n\nThe Zürich office opens in May.\n")
	write("resume.md", "# HR\n\nHer résumé arrived this morning.\n")

	cases := []struct {
		file, query, wantInWindow string
	}{
		{"cafe-nfc.md", "cafe", "Café du Coin"},
		{"cafe-nfc.md", "café", "Café du Coin"},
		{"cafe-nfd.md", "cafe", "Café du Coin"},
		{"cafe-nfd.md", "café", "Café du Coin"},
		{"zurich.md", "zurich", "Zürich office"},
		{"resume.md", "resume", "résumé arrived"},
		{"resume.md", "RÉSUMÉ", "résumé arrived"},
	}
	for _, tc := range cases {
		t.Run(tc.file+"/"+tc.query, func(t *testing.T) {
			snip := vaultSearchSnippet(root, tc.file, tc.query)
			require.NotEmpty(t, snip, "a hit the matcher finds by folding must not render 'No excerpt available'")
			assert.Contains(t, snip, tc.wantInWindow, "the window must be cut from the original bytes around the match")
		})
	}
}

// TestVaultSearchQueryTerms_AreFoldedLikeTheMatcher — the terms the scanner
// looks for are folded the same way, so "Café" and "cafe" are one term.
func TestVaultSearchQueryTerms_AreFoldedLikeTheMatcher(t *testing.T) {
	got := vaultSearchQueryTerms("Café thé Zürich")
	assert.Equal(t, []string{"zurich", "cafe", "the"}, got, "longest first, folded")
}

// TestVaultSearchRecordsCutReason_NamesTheTypeAndTheLimitPlainly — D-138: the
// sentence must say what was searched and what was cut, and must not quote
// the record type as if it were the query.
func TestVaultSearchRecordsCutReason_NamesTheTypeAndTheLimitPlainly(t *testing.T) {
	got := vaultSearchRecordsCutReason("decision", 20)
	assert.True(t, strings.HasPrefix(got, "records: "), got)
	assert.Contains(t, got, "records of type decision")
	assert.Contains(t, got, "limit (20)")
	assert.Contains(t, got, "more decision records match this search than are shown")
	assert.NotContains(t, got, `"decision"`, "the type must not be quoted like a query string")
	assert.NotContains(t, got, "record hits exist beyond")
}

// TestVaultSearch_RecordsCutReasonReachesTheWire — the same sentence, through
// the real endpoint at the limit the UI uses.
func TestVaultSearch_RecordsCutReasonReachesTheWire(t *testing.T) {
	api, ws, colID := buildVaultSearchVaultInnerOverflow(t)
	w := vaultFindPost(t, api, ws, map[string]any{
		"query": "plonktech", "collection_id": colID, "limit": 2,
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.VaultSearchResponse](t, w)
	require.False(t, resp.Complete)
	require.NotNil(t, resp.CompleteReason)
	assert.Equal(t, vaultSearchRecordsCutReason("deal", 2), *resp.CompleteReason)
	require.NotNil(t, resp.Statement)
	assert.Contains(t, *resp.Statement, "records of type deal")
	assert.NotContains(t, *resp.Statement, `"deal"`)
}
