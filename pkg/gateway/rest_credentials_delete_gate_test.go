// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_credentials_delete_gate_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order row 15 (B7, founder S-Q11 B / FQ-2; FR-056):
//
//   - TestDeleteCredential_NoReauthNeeded — DELETE /api/v1/credentials/{key}
//     with the admin user but NO re-auth consent token must delete the
//     credential (200, "removed"). The password consent gate moves to the
//     Settings screen (one confirm before deleting); the server keeps
//     withAuth, the admin/bypass guards and the audit entry, and drops only
//     requireReAuth. RED today: deleteCredential still calls requireReAuth,
//     so the request is 403 and the credential survives.
//
//   - TestSetCredential_StillNeedsReauth — POST /credentials MUST keep
//     requiring the consent token (FR-056 "MUST keep requiring"). Existing
//     behaviour preserved (passes today and must stay green); it pins the
//     other half of the split so a refactor cannot quietly drop both gates.
//
// ADR-022's TestDeleteCredential_RequiresReAuth is REPLACED by the
// implementer's change (the spec's regression table: "Replaced by
// TestDeleteCredential_NoReauthNeeded") — this file does not touch it.
//
// Oracle: FR-056 and the B7 scenarios. Expected values from the spec.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestDeleteCredential_NoReauthNeeded(t *testing.T) {
	withEdition(t, config.EditionCore) // local mode: requireReAuth performs the real token check
	api := newCredVaultReAuthTestAPI(t)
	require.NoError(t, api.credStore.Set("DOOMED_KEY", "secret"))

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/DOOMED_KEY", nil)
	r = withReAuthAdminNoToken(r) // admin user, NO re-auth consent token
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)

	require.NotEqual(t, http.StatusForbidden, w.Code,
		"RED (FR-056, B7): DELETE /credentials without a re-auth token is still 403 — "+
			"deleteCredential still calls requireReAuth; the spec moves the consent to the "+
			"screen's confirm and drops only the server gate. body=%s", w.Body.String())
	require.Equal(t, http.StatusOK, w.Code,
		"the delete must succeed without the consent token; body=%s", w.Body.String())

	keys, err := api.credStore.List()
	require.NoError(t, err)
	require.NotContains(t, keys, "DOOMED_KEY",
		"the credential must actually be deleted")
}

func TestSetCredential_StillNeedsReauth(t *testing.T) {
	withEdition(t, config.EditionCore)
	api := newCredVaultReAuthTestAPI(t)

	// A set without the consent token stays refused. Preserved behaviour
	// (FR-056: the writes keep the token) — this passes today and must stay
	// green after GREEN.
	body := `{"key":"NEW_KEY","value":"secret"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/credentials", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdminNoToken(r)
	w := httptest.NewRecorder()
	api.HandleCredentials(w, r)

	require.Equal(t, http.StatusForbidden, w.Code,
		"POST /credentials without a re-auth token must stay 403 (FR-056: writes keep the token); body=%s",
		w.Body.String())
	keys, err := api.credStore.List()
	require.NoError(t, err)
	require.NotContains(t, keys, "NEW_KEY", "the refused write must not have stored anything")

	// And with the token it still succeeds (positive control). The spec pins
	// only "keeps requiring the token" — the success status is the existing
	// suite's subject — so the oracle here is the outcome: the write lands.
	body2 := `{"key":"NEW_KEY","value":"secret"}`
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/credentials", strings.NewReader(body2))
	r2.Header.Set("Content-Type", "application/json")
	r2 = withReAuthAdmin(t, api, r2)
	w2 := httptest.NewRecorder()
	api.HandleCredentials(w2, r2)
	require.Less(t, w2.Code, http.StatusBadRequest,
		"the same set with a valid consent token is accepted; body=%s", w2.Body.String())
	keys2, err := api.credStore.List()
	require.NoError(t, err)
	require.Contains(t, keys2, "NEW_KEY", "the token-gated set actually stored the credential")
}
