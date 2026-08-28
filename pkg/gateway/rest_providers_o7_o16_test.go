// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_providers_o7_o16_test.go — three confirmed code-review findings in
// the provider layer.
//
// O7 (major): providerID-to-vendor OAuth mapping (providers.OAuthVendorID)
// is many-to-one — "openai" (the plain api_key row) and "openai-chatgpt"
// (the ChatGPT sign-in row) both resolve to the stored entry
// "openai_OAUTH". DELETE /api/v1/providers/openai and DELETE
// /api/v1/providers/openai/sign-in both used to compute that shared entry
// name unconditionally and delete it, destroying a still-configured
// "openai-chatgpt" row's live ChatGPT grant even though the "openai" row
// itself never signs in and never owns that entry. The fix scopes the
// deletion to the row that can actually SOURCE a device-code sign-in for
// the vendor (providers.OAuthEntryOwner) — deleting/signing-out the
// unrelated "openai" row is now a true no-op for the OAuth entry, not a
// silent revocation of someone else's credential.
//
// O16 (minor): PUT /api/v1/providers/{a}/{b} let an id containing a "/"
// through to config.json, but DELETE /api/v1/providers/{id} only matches
// ids with NO "/" (see the MethodDelete case in HandleProviders) — the row
// became permanently undeletable through the API. The fix validates the id
// at the write path (mirrors validateEntityID + maxProviderIDLen, the same
// guard every sign-in route already applies) and rejects it with 400.
//
// O6 (this file's addition alongside O7/O16): the provider.deleted audit
// entry recorded neither the actor nor the source IP, unlike the read-only
// Copilot probe audit one file away (auditCopilotProbe). A route reachable
// during the FR-050 pre-auth window with no actor/IP on its audit trail is
// indistinguishable from an authenticated admin's own action — and it
// compounds O7 directly: an operator who lost a live OAuth grant to the
// vendor-aliasing bug had no way to tell who triggered the delete either.
package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// TestDeleteProvider_OpenAIRowNeverDeletesChatGPTGrant is the O7 regression
// for the full-row DELETE path. Given a configured "openai" api_key row AND
// a separately configured, signed-in "openai-chatgpt" row sharing the
// vendor identity "openai" (providers.OAuthVendorID), When the operator
// deletes only the "openai" row, Then the row and its OWN api_key are gone,
// but the openai-chatgpt row's live ChatGPT grant (openai_OAUTH) survives —
// deleting a plain api_key row must never destroy a different, unrelated
// row's OAuth session.
func TestDeleteProvider_OpenAIRowNeverDeletesChatGPTGrant(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{
			{Name: "openai", Provider: "openai", Model: "gpt-5.2", APIKeyRef: "openai_API_KEY"},
			{Name: "openai-chatgpt", Provider: "openai-chatgpt", Model: "gpt-5.2",
				APIKeyRef: "openai-chatgpt_API_KEY"},
			deleteTestAnthropicRow(),
		},
	)
	api, tmpDir, auditDir := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openai_API_KEY", "sk-test-openai-key"))

	oauthRef := credentials.OAuthEntryName(providers_pkg.OAuthVendorID("openai-chatgpt"))
	require.Equal(t, "openai_OAUTH", oauthRef, "sanity: the shared vendor entry name")
	require.NoError(t, api.credStore.Set(oauthRef,
		`{"access_token":"live-access-token","refresh_token":"live-refresh-token","account_id":"acc_1"}`))

	w := doProviderDelete(t, api, "openai", "", cfg, true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.True(t, deleteRespBody(t, w)["deleted"].(bool))

	assert.NotContains(t, diskProviderIDs(t, tmpDir), "openai", "the deleted row must be gone")
	assert.Contains(t, diskProviderIDs(t, tmpDir), "openai-chatgpt",
		"the unrelated, still-configured openai-chatgpt row must be untouched")

	assert.False(t, credentialExists(t, api, "openai_API_KEY"),
		"the deleted row's own api_key must be gone")
	assert.True(t, credentialExists(t, api, oauthRef),
		"the openai-chatgpt row's live ChatGPT grant must survive deleting the unrelated openai row")

	rec := findAuditEvent(t, auditDir, EventProviderDeleted)
	require.NotNil(t, rec, "audit entry must exist")
	details, ok := rec["details"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "openai_API_KEY", details["credential_ref"])
	_, hasOAuthRef := details["oauth_credential_ref"]
	assert.False(t, hasOAuthRef,
		"the audit trail must not claim an OAuth ref was revoked when the deleted row never owned one")
}

// TestSignOut_OpenAIRowNeverDeletesChatGPTGrant is the O7 regression for
// DELETE /api/v1/providers/{id}/sign-in. "openai" never supports sign-in
// (signInMethodFor has no case for it) and so never writes openai_OAUTH,
// but before the fix the handler computed the shared vendor entry name
// unconditionally from ANY providerID and deleted it — DELETE
// /providers/openai/sign-in destroyed a real "openai-chatgpt" grant. The
// fix makes this a harmless no-op success for a row that never owned
// anything to revoke, exactly like the existing "NotFound = success"
// contract for a real sign-in row that was never signed in.
func TestSignOut_OpenAIRowNeverDeletesChatGPTGrant(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	store := credentials.NewStore(api.credentialsStorePath())
	require.NoError(t, credentials.Unlock(store))

	oauthRef := credentials.OAuthEntryName(providers_pkg.OAuthVendorID("openai-chatgpt"))
	require.Equal(t, "openai_OAUTH", oauthRef)
	blob, err := json.Marshal(map[string]any{"access_token": "live-access-token", "account_id": "acc"})
	require.NoError(t, err)
	require.NoError(t, store.Set(oauthRef, string(blob)))

	w := doJSON(t, api, http.MethodDelete, "/api/v1/providers/openai/sign-in", nil)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var result gen.OperationResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	assert.True(t, result.Success, "a no-op sign-out for a row with nothing to revoke is still success")

	_, getErr := store.Get(oauthRef)
	assert.NoError(t, getErr,
		"the openai-chatgpt row's live ChatGPT grant must survive DELETE against the unrelated 'openai' id")
}

// TestPutProvider_RejectsSlashInID is the O16 regression. PUT
// /api/v1/providers/{id} must reject an id containing a path separator
// BEFORE writing anything — DELETE /api/v1/providers/{id} only matches ids
// with no "/" (HandleProviders' MethodDelete case), so a row this PUT let
// through with a "/" in its id became permanently stuck in config.json.
func TestPutProvider_RejectsSlashInID(t *testing.T) {
	api := newProviderValidationTestAPI(t, "", "")

	body := `{"protocol":"openai-compatible","api_base":"http://127.0.0.1:1/v1","api_key":"sk-test-key"}`
	w := doPutProvider(t, api, "foo/bar", body)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	if errResp.Field != nil {
		assert.Equal(t, "id", *errResp.Field)
	}

	rawBytes, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	raw := string(rawBytes)
	assert.NotContains(t, raw, "foo/bar", "the slash-containing id must never reach config.json")
	assert.NotContains(t, raw, `"foo"`, "no partial row must be written either")
}

// TestDeleteProvider_AuditRecordsActorAndSourceIP is the O6 regression: the
// provider.deleted audit entry must carry the authenticated caller (User)
// and the request's source IP (details.source_ip), the same shape
// auditCopilotProbe already uses for the sibling read-only sign-in-status
// probe (rest_signin_copilot.go). Without this, the entry cannot
// distinguish an authenticated admin's own deletion from any other caller
// reachable during the FR-050 pre-auth window.
func TestDeleteProvider_AuditRecordsActorAndSourceIP(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, _, auditDir := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test-openrouter"))

	w := doProviderDelete(t, api, "openrouter", "", nil, true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	rec := findAuditEvent(t, auditDir, EventProviderDeleted)
	require.NotNil(t, rec, "audit entry must exist")
	assert.Equal(t, "admin", rec["user"], "the authenticated caller must be recorded as the actor")
	details, ok := rec["details"].(map[string]any)
	require.True(t, ok, "audit entry must carry details")
	sourceIP, _ := details["source_ip"].(string)
	assert.NotEmpty(t, sourceIP, "the request's source IP must be recorded")
}
