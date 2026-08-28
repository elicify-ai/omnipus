package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// ADR-068 T068-06 — OnboardingCompleteRequest.provider is a discriminated
// union on `auth_method` (OnboardingProviderApiKey | OnboardingProviderSignIn).
// These tests pin the completion handler's dispatch on that discriminator:
//
//   - api_key  → the pre-ADR-068 behaviour, unchanged: 200 + token, the key in
//     the credential store, the provider row persisted in config.json.
//   - sign_in  → the real sign-in completion path (T068-16), covered in
//     rest_onboarding_complete_test.go. What stays here is the half this file
//     has always owned: a REJECTED sign_in body persists nothing and releases
//     the reservation, so a corrected retry can still succeed.
//   - missing / unknown auth_method → 400; nothing persisted.
//
// TestProviderSignInRoutes_AuthGating (below) covers the two sign-in routes'
// (POST /providers/{id}/sign-in, GET …/sign-in/status) auth posture —
// T068-14 landed the real handlers; see that test's own comment for the
// FR-050 onboarding-aware gate it now pins instead of the retired 501 stub.

func newAuthMethodOnboardingAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return newOnboardingTestAPI(t, tmpDir, al), tmpDir
}

func postOnboardingComplete(api *restAPI, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleCompleteOnboarding(w, req)
	return w
}

func TestOnboardingComplete_AuthMethodApiKey_UnchangedBehaviour(t *testing.T) {
	api, tmpDir := newAuthMethodOnboardingAPI(t)
	upstream := startFakeProviderUpstream(t)
	body := withProviderEndpoint(
		`{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-pin-key","model":"gpt-4o"},"admin":{"username":"admin","password":"secret123"}}`,
		upstream,
	)

	w := postOnboardingComplete(api, body)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"])
	assert.Equal(t, "admin", resp["username"])
	assert.Nil(t, resp["warning"])

	// Credential store holds the key under the <id>_API_KEY name.
	stored, err := api.credStore.Get("openai_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "sk-pin-key", stored)

	// Persisted config carries the provider row with the chosen model and the
	// api_base override, and the admin user. No plaintext key anywhere.
	raw, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "sk-pin-key", "no plaintext key in config.json")
	var cfgRaw map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfgRaw))
	provs, _ := cfgRaw["providers"].([]any)
	require.NotEmpty(t, provs, "provider row persisted")
	found := false
	for _, p := range provs {
		pm, _ := p.(map[string]any)
		if pm["provider"] == "openai" && pm["model"] == "gpt-4o" {
			found = true
			assert.Equal(t, upstream, pm["api_base"])
			assert.Equal(t, "openai_API_KEY", pm["api_key_ref"])
		}
	}
	assert.True(t, found, "providers[] must contain {provider: openai, model: gpt-4o}: %s", string(raw))
	assert.True(t, api.onboardingMgr.IsComplete())
}

// TestOnboardingComplete_AuthMethodSignIn_RejectedBodyPersistsNothing keeps the
// coverage the T068-16 stub test carried — a rejected sign_in completion writes
// NOTHING and leaves the reservation released so the operator can retry — now
// that the rejection comes from a real rule rather than a not-implemented stub.
// The rule exercised is OnboardingProviderSignIn.yaml's own: the id must name a
// catalog row that declares sign_in, and `openrouter` is api_key-only.
func TestOnboardingComplete_AuthMethodSignIn_RejectedBodyPersistsNothing(t *testing.T) {
	api, tmpDir := newAuthMethodOnboardingAPI(t)
	body := `{"provider":{"auth_method":"sign_in","id":"openrouter","model":"openai/gpt-4o"},` +
		`"admin":{"username":"admin","password":"secret123"}}`

	w := postOnboardingComplete(api, body)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, onboardingSignInUnsupportedMsg, resp["error"])
	assert.Equal(t, "id", resp["field"])

	// Nothing persisted, reservation released so a retry can succeed.
	assert.False(t, api.onboardingMgr.IsComplete())
	raw, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "openrouter")
}

func TestOnboardingComplete_AuthMethodMissingOrUnknown_400(t *testing.T) {
	for name, body := range map[string]string{
		"missing": `{"provider":{"id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`,
		"unknown": `{"provider":{"auth_method":"oauth","id":"openai","api_key":"sk-test"},"admin":{"username":"admin","password":"secret123"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			api, _ := newAuthMethodOnboardingAPI(t)
			w := postOnboardingComplete(api, body)
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
			var resp map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, onboardingAuthMethodErrMsg, resp["error"])
			assert.False(t, api.onboardingMgr.IsComplete())
			_, err := api.credStore.Get("openai_API_KEY")
			assert.Error(t, err, "no credential may be stored on a rejected body")
		})
	}
}

func TestOnboardingComplete_ApiKeyVariant_RejectsFieldsOffTheVariant(t *testing.T) {
	// Strict decode into the named variant: a field the api_key variant does
	// not carry is a 400 regardless of ValidateInbound (ADR-034 rule).
	api, _ := newAuthMethodOnboardingAPI(t)
	body := `{"provider":{"auth_method":"api_key","id":"openai","api_key":"sk-test","device_code":"x"},"admin":{"username":"admin","password":"secret123"}}`
	w := postOnboardingComplete(api, body)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "field not allowed on provider auth_method")
}

// TestProviderSignInRoutes_AuthGating — ADR-068 FR-050 (T068-14). Replaces
// TestProviderSignInRoutes_StubbedUntilT068_16: T068-14 replaced the 501
// stub with the real sign-in handlers, so that test's "authenticated
// non-bypass -> 501 naming T068-16" assertion is obsolete by design (the
// stub is gone), and its "unauthenticated -> 401" assertion is obsolete
// because FR-050 makes that 401 conditional on onboarding completion (it
// also never actually exercised that path — the request carried no config
// snapshot in context at all, so it was failing closed at the OUTER
// RequireNotBypass "no snapshot" guard, landing on 503, not tracing through
// FR-050's own onboardingDone check either before or after this change).
//
// Still-valid coverage kept, now for the real, onboarding-aware gate:
//   - onboarding incomplete + unauthenticated + no bypass -> reachable
//     (FR-050's pre-auth allowance; this codepath returns 200 for both
//     routes under test since codex-cli is a cli_login provider with no
//     external dependency).
//   - onboarding incomplete OR complete + dev-mode bypass active -> still
//     503 (RequireNotBypass gates on bypass unconditionally; FR-050 only
//     ever affects the EARLIER "is a user authenticated" pre-check, never
//     the bypass gate downstream of it).
//   - onboarding complete + unauthenticated -> 401 (FR-050's pre-auth
//     allowance ends once onboarding is done). rest_sign_in_test.go's
//     TestSignIn_PreAuthOnlyDuringOnboarding already asserts exactly this
//     transition for POST /providers/{id}/sign-in alone; kept here too
//     because this test's table also covers GET .../sign-in/status, which
//     that one does not, so removing it here would drop that route's
//     coverage of the same FR-050 requirement.
func TestProviderSignInRoutes_AuthGating(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/providers/codex-cli/sign-in"},
		{http.MethodGet, "/api/v1/providers/codex-cli/sign-in/status"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			api, _ := newAuthMethodOnboardingAPI(t)

			// Onboarding incomplete, unauthenticated, no bypass -> reachable.
			req := httptest.NewRequest(rt.method, rt.path, nil)
			ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
			w := httptest.NewRecorder()
			api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
			assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

			// Onboarding still incomplete, dev-mode bypass active -> 503
			// regardless of the FR-050 pre-auth allowance above.
			bypassCfg := *api.agentLoop.GetConfig()
			bypassCfg.Gateway.DevModeBypass = true
			req = httptest.NewRequest(rt.method, rt.path, nil)
			ctx = context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, &bypassCfg)
			w = httptest.NewRecorder()
			api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)

			// Onboarding complete, unauthenticated -> 401.
			require.NoError(t, api.onboardingMgr.CompleteOnboarding())
			req = httptest.NewRequest(rt.method, rt.path, nil)
			ctx = context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
			w = httptest.NewRecorder()
			api.HandleProviders(w, isolateRateLimit(t, req.WithContext(ctx)))
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}
