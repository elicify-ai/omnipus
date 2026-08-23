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
//   - sign_in  → 400 naming T068-16. T068-16 owns the real sign-in completion
//     path and will FLIP that assertion (200 + no stored credential) when it
//     lands; until then the honest stub is the contract.
//   - missing / unknown auth_method → 400; nothing persisted.
//
// Also pins the two new sign-in routes (POST /providers/{id}/sign-in,
// GET …/sign-in/status) as 501 stubs behind the contract's adminWrap posture.

func newAuthMethodOnboardingAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	tmpDir := t.TempDir()
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
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

func TestOnboardingComplete_AuthMethodSignIn_StubbedUntilT068_16(t *testing.T) {
	api, tmpDir := newAuthMethodOnboardingAPI(t)
	body := `{"provider":{"auth_method":"sign_in","id":"codex-cli","model":"gpt-5.4"},"admin":{"username":"admin","password":"secret123"}}`

	w := postOnboardingComplete(api, body)

	// T068-16 flips this to 200 with no stored credential once the sign-in
	// completion path exists.
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, onboardingSignInNotImplementedMsg, resp["error"])
	assert.Contains(t, resp["error"], "T068-16")

	// Nothing persisted, reservation released so a retry can succeed.
	assert.False(t, api.onboardingMgr.IsComplete())
	raw, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "codex-cli")
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

func TestProviderSignInRoutes_StubbedUntilT068_16(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	routes := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/providers/codex-cli/sign-in"},
		{http.MethodGet, "/api/v1/providers/codex-cli/sign-in/status"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			// Unauthenticated → 401.
			req := httptest.NewRequest(rt.method, rt.path, nil)
			w := httptest.NewRecorder()
			api.HandleProviders(w, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)

			// Authenticated, non-bypass → 501 naming T068-16 (T068-16 replaces
			// this with the real handler).
			req = httptest.NewRequest(rt.method, rt.path, nil)
			ctx := context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"})
			ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
			w = httptest.NewRecorder()
			api.HandleProviders(w, req.WithContext(ctx))
			require.Equal(t, http.StatusNotImplemented, w.Code, "body=%s", w.Body.String())
			assert.Contains(t, w.Body.String(), "T068-16")

			// Authenticated under dev-mode bypass → 503 (adminWrap posture).
			bypassCfg := *api.agentLoop.GetConfig()
			bypassCfg.Gateway.DevModeBypass = true
			req = httptest.NewRequest(rt.method, rt.path, nil)
			ctx = context.WithValue(req.Context(), UserContextKey{}, &config.UserConfig{Username: "admin"})
			ctx = context.WithValue(ctx, ctxkey.ConfigContextKey{}, &bypassCfg)
			w = httptest.NewRecorder()
			api.HandleProviders(w, req.WithContext(ctx))
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		})
	}
}
