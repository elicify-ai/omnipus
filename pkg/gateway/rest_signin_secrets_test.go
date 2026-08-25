// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_signin_secrets_test.go — the two sign-in-adjacent secret/abuse gaps
// found in the ADR-068 §8b surface:
//
//   - DELETE /api/v1/providers/{id} deleted only `<id>_API_KEY`, so removing
//     a signed-in openai-chatgpt row left `openai_OAUTH` — a live access AND
//     refresh token for the operator's real vendor account — in
//     credentials.json with nothing in the UI referencing it. That falsifies
//     ADR-068 §9 exit proof #2, "no secret survives the confirm". The boot
//     sweep was no backstop: it matched `_API_KEY` only.
//   - GET /api/v1/providers/{id}/sign-in/status had no rate limiter although
//     it reaches the stored-OAuth token source (which refreshes against the
//     vendor within 5 minutes of expiry) and, for github-copilot, spawns a
//     CLI process — and FR-050 makes it reachable unauthenticated while
//     onboarding is incomplete.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
)

// TestDeleteProvider_RemovesOAuthCredential — Given a configured,
// signed-in openai-chatgpt row, When the operator confirms DELETE
// /api/v1/providers/openai-chatgpt, Then BOTH secrets the row owns are gone
// from the credential store and the audit entry names both refs (never their
// values).
//
// The entry name is the load-bearing detail: it is keyed on the VENDOR, not
// the route id (providers.OAuthVendorID maps openai-chatgpt → openai,
// ADR-068 FR-007), so the real target is `openai_OAUTH`. A naive
// providerID+"_OAUTH" derivation misses the only row that has one today,
// which is why this test asserts the vendor-mapped name explicitly.
func TestDeleteProvider_RemovesOAuthCredential(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{
			{
				Name: "openai-chatgpt", Provider: "openai-chatgpt",
				Model: "gpt-5.2", APIKeyRef: "openai-chatgpt_API_KEY",
			},
			deleteTestAnthropicRow(),
		},
	)
	api, tmpDir, auditDir := newProviderDeleteAPI(t, cfg)

	oauthRef := credentials.OAuthEntryName(providers_pkg.OAuthVendorID("openai-chatgpt"))
	require.Equal(t, "openai_OAUTH", oauthRef,
		"the stored entry is keyed on the vendor identity, not the provider row id")

	require.NoError(t, api.credStore.Set("openai-chatgpt_API_KEY", "sk-test-key"))
	require.NoError(t, api.credStore.Set(oauthRef,
		`{"access_token":"live-access-token","refresh_token":"live-refresh-token","account_id":"acc_1"}`))

	w := doProviderDelete(t, api, "openai-chatgpt", "", cfg, true)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, deleteRespBody(t, w)["deleted"].(bool))
	assert.NotContains(t, diskProviderIDs(t, tmpDir), "openai-chatgpt")

	assert.False(t, credentialExists(t, api, "openai-chatgpt_API_KEY"),
		"the API key must not survive the confirm")
	assert.False(t, credentialExists(t, api, oauthRef),
		"the vendor OAuth grant (access + refresh token) must not survive the confirm")

	rec := findAuditEvent(t, auditDir, EventProviderDeleted)
	require.NotNil(t, rec)
	details, ok := rec["details"].(map[string]any)
	require.True(t, ok, "audit entry must carry details")
	assert.Equal(t, "openai-chatgpt_API_KEY", details["credential_ref"])
	assert.Equal(t, oauthRef, details["oauth_credential_ref"],
		"the audit trail must name the OAuth ref that was revoked")
	assert.NotContains(t, w.Body.String(), "live-access-token")
}

// TestDeleteProvider_MissingOAuthEntryIsSuccess — Given a configured
// provider that was never signed in, When it is deleted, Then the absent
// OAuth entry is treated as success exactly as the absent API key already
// is (FR-010 step 3): 200 {deleted:true}, not a 500 retryable state.
func TestDeleteProvider_MissingOAuthEntryIsSuccess(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-live"))

	w := doProviderDelete(t, api, "openrouter", "", cfg, true)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, deleteRespBody(t, w)["deleted"].(bool))
	assert.NotContains(t, diskProviderIDs(t, tmpDir), "openrouter")
	assert.False(t, credentialExists(t, api, "openrouter_API_KEY"))
	assert.False(t, credentialExists(t, api, credentials.OAuthEntryName("openrouter")))
}

// TestCredentialSweep_OrphanedOAuthEntries — the boot backstop for the same
// secret, for the crash window DELETE cannot close on its own.
//
// Given `openai_OAUTH` in the store and no configured row whose vendor is
// `openai`, When boot sweeps, Then it is deleted with an audit entry. Given a
// configured openai-chatgpt row, Then it survives — the keep-set is keyed on
// the VENDOR, so a row whose id differs from its vendor still protects the
// entry, and a vendor backing several rows is protected by any one of them.
func TestCredentialSweep_OrphanedOAuthEntries(t *testing.T) {
	t.Run("orphan is swept", func(t *testing.T) {
		store, auditor, auditDir := newSweepFixture(t)
		require.NoError(t, store.Set("openai_OAUTH", `{"access_token":"orphan-token"}`))
		// A non-provider-shaped name and the bare suffix must survive.
		require.NoError(t, store.Set("_OAUTH", "empty-vendor-secret"))
		require.NoError(t, store.Set("CLAUDE_CODE_OAUTH_TOKEN", "not-an-oauth-entry"))

		cfg := &config.Config{Providers: []*config.ModelConfig{{
			Name: "anthropic", Provider: "anthropic",
			Model: "claude-sonnet-4.6", APIKeyRef: "anthropic_API_KEY",
		}}}
		sweepOrphanedProviderCredentials(cfg, store, auditor)

		assert.False(t, sweepCredentialExists(t, store, "openai_OAUTH"),
			"an OAuth grant no configured row maps to must not survive boot")
		assert.True(t, sweepCredentialExists(t, store, "_OAUTH"))
		assert.True(t, sweepCredentialExists(t, store, "CLAUDE_CODE_OAUTH_TOKEN"))
		assert.Equal(t, 1, countAuditEvents(t, auditDir, EventProviderCredentialSwept))
	})

	t.Run("a configured row protects its vendor entry", func(t *testing.T) {
		store, auditor, auditDir := newSweepFixture(t)
		require.NoError(t, store.Set("openai_OAUTH", `{"access_token":"live-token"}`))

		// The row id is openai-chatgpt; the entry is openai_OAUTH. Only the
		// vendor mapping connects them.
		cfg := &config.Config{Providers: []*config.ModelConfig{{
			Name: "openai-chatgpt", Provider: "openai-chatgpt", Model: "gpt-5.2",
		}}}
		sweepOrphanedProviderCredentials(cfg, store, auditor)

		assert.True(t, sweepCredentialExists(t, store, "openai_OAUTH"),
			"a signed-in configured provider's tokens must survive boot")
		assert.Equal(t, 0, countAuditEvents(t, auditDir, EventProviderCredentialSwept))
	})
}

// signInStatusFromIP drives GET /providers/{id}/sign-in/status from a
// specific client IP. A DEDICATED source IP matters: the rate limiters are
// package-level singletons shared by every test in this binary, so
// exhausting the default httptest RemoteAddr's bucket would 429 unrelated
// sign-in tests for a minute.
func signInStatusFromIP(t *testing.T, api *restAPI, providerID, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/"+providerID+"/sign-in/status", nil)
	r.RemoteAddr = remoteAddr
	r = r.WithContext(context.WithValue(r.Context(), configContextKey{}, api.agentLoop.GetConfig()))
	w := httptest.NewRecorder()
	api.HandleProviders(w, r)
	return w
}

// TestSignInStatus_RateLimited — Given the sign-in status route, When one
// unauthenticated client exceeds the per-IP ceiling, Then it is refused with
// 429 and a Retry-After header, instead of being able to drive an unbounded
// number of vendor refreshes / CLI spawns.
//
// codex-cli is used as the subject because its status is computed from a
// local file (no vendor traffic in the test) — the limiter is wired on the
// route, not per provider shape.
func TestSignInStatus_RateLimited(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	t.Setenv("CODEX_HOME", t.TempDir()) // empty: not_signed_in, no I/O beyond a stat

	limit := signInStatusLimiter.limit
	require.Equal(t, 60, limit, "the status ceiling matches the poll route's (see signInStatusLimiter)")

	const ip = "203.0.113.77:5555"
	for i := 0; i < limit; i++ {
		w := signInStatusFromIP(t, api, "codex-cli", ip)
		require.Equal(t, http.StatusOK, w.Code, "request %d within the ceiling must pass: %s", i, w.Body.String())
	}
	w := signInStatusFromIP(t, api, "codex-cli", ip)
	require.Equal(t, http.StatusTooManyRequests, w.Code,
		"the request past the ceiling must be refused — an unlimited status route is an unbounded vendor-refresh amplifier")
	assert.NotEmpty(t, w.Header().Get("Retry-After"))

	// A different client is unaffected: the bucket is per IP, not global.
	other := signInStatusFromIP(t, api, "codex-cli", "203.0.113.78:5555")
	assert.Equal(t, http.StatusOK, other.Code)
}
