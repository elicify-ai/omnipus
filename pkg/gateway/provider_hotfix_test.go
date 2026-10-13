// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Regression tests for the 2026-10-13 provider hotfix (H1, H3, H4), driven
// through the real HTTP handlers against the REAL committed catalog
// (catalog.EmbeddedSnapshot) — the document the founder's instance served.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// openAISignInElsewhereMsg is the refusal the spec of this hotfix asks for:
// generic by catalog data (the OpenAI company's rows that really can sign
// in, in-app sign-in first), never a hardcoded id.
const openAISignInElsewhereMsg = `sign-in is not available for "OpenAI"; ` +
	`OpenAI sign-in lives on "ChatGPT (subscription)" or "Codex CLI"`

// installEmbeddedCatalog serves the committed catalog document to both the
// handler under test and the process-wide provider factory.
func installEmbeddedCatalog(t *testing.T, api *restAPI) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err, "the committed catalog snapshot must parse")
	api.providerCatalog = cat
	installProbeCatalog(t, cat)
	return cat
}

// writeProviderConfigRows replaces config.json's providers[] with rows.
func writeProviderConfigRows(t *testing.T, api *restAPI, rows ...map[string]any) {
	t.Helper()
	list := make([]any, 0, len(rows))
	for _, r := range rows {
		list = append(list, r)
	}
	b, err := json.Marshal(map[string]any{
		"version":   1,
		"agents":    map[string]any{"defaults": map[string]any{}, "list": []any{}},
		"providers": list,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), b, 0o600))
}

func decodeOperationResult(t *testing.T, w *httptest.ResponseRecorder) gen.OperationResult {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var res gen.OperationResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	return res
}

// H1 — PUT /providers/openai with auth_method sign_in is refused: the plain
// `openai` row has no sign-in mechanism, so accepting it created a row that
// stays "disconnected" forever beside a working openai-chatgpt row.
func TestPutProvider_SignInOnRowWithoutMechanism400(t *testing.T) {
	// Seed a valid api_key row so the post-write reload has a config that
	// loads — the refusal under test must happen BEFORE any write.
	api := newProviderValidationTestAPI(t, "openrouter", "")
	installEmbeddedCatalog(t, api)
	before, err := os.ReadFile(api.configPath())
	require.NoError(t, err)

	w := doPutProvider(t, api, "openai", `{"auth_method":"sign_in"}`)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, openAISignInElsewhereMsg, body["error"])
	assert.Equal(t, "auth_method", body["field"])
	after, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused PUT must persist nothing")

	// Control: the row that really owns OpenAI sign-in is still accepted.
	ok := doPutProvider(t, api, "openai-chatgpt", `{"auth_method":"sign_in","model":"gpt-5.4"}`)
	assert.Equal(t, http.StatusOK, ok.Code, "body=%s", ok.Body.String())
}

// H1 — a pre-existing `openai` row saved with auth_method sign_in (how the
// founder's instance got its dead row) explains itself in GET /providers
// instead of sitting silently "disconnected".
func TestListProviders_SignInRowWithoutMechanismExplains(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	installEmbeddedCatalog(t, api)
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = []*config.ModelConfig{
		{Provider: "openai", Model: "gpt-5.4", AuthMethod: config.AuthMethodSignIn},
	}

	provs := getProviders(t, api)
	require.Len(t, provs, 1)
	assert.Equal(t, "openai", provs[0].Id)
	assert.Equal(t, gen.ProviderStatusDisconnected, provs[0].Status)
	require.NotNil(t, provs[0].Error, "the dead sign-in row must say why it can never connect")
	assert.Equal(t, openAISignInElsewhereMsg, *provs[0].Error)
}

// signInTestUpstream is a minimal OpenAI-compatible completion endpoint that
// records what the probe sent.
type signInTestUpstream struct {
	*httptest.Server
	mu     sync.Mutex
	auth   []string
	models []string
}

func newSignInTestUpstream(t *testing.T) *signInTestUpstream {
	t.Helper()
	u := &signInTestUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		u.mu.Lock()
		u.auth = append(u.auth, r.Header.Get("Authorization"))
		u.models = append(u.models, req.Model)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *signInTestUpstream) seen() ([]string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.auth...), append([]string(nil), u.models...)
}

// H3 — POST /providers/{id}/test on a signed-in sign_in row runs the same
// real probe the sign-in path uses (one completion carrying the saved
// token), instead of answering "no API key configured for this provider".
func TestProviderTest_SignInRowRunsSignInProbe(t *testing.T) {
	t.Run("signed in: one completion with the saved token and the row's model", func(t *testing.T) {
		signedOutCodexHome(t)
		up := newSignInTestUpstream(t)
		api := newProviderValidationTestAPI(t, "", "")
		installEmbeddedCatalog(t, api)
		writeProviderConfigRows(t, api, map[string]any{
			"provider": "openai-chatgpt", "model": "gpt-5.4",
			"auth_method": "sign_in", "api_base": up.URL,
		})
		store, err := api.resolveSignInCredStore()
		require.NoError(t, err)
		require.NoError(t, providers.WriteStoreOAuthCredential("openai-chatgpt", store, providers.OAuthCredential{
			AccessToken: "saved-device-code-token",
			AccountID:   "acct-1",
			ExpiresAt:   time.Now().Add(time.Hour),
		}))

		res := decodeOperationResult(t, doProviderTest(t, api, "openai-chatgpt"))

		assert.True(t, res.Success, "error=%v", res.Error)
		assert.Nil(t, res.Error)
		auths, models := up.seen()
		assert.Equal(t, []string{"gpt-5.4"}, models, "exactly one completion, on the row's own model")
		assert.Equal(t, []string{"Bearer saved-device-code-token"}, auths,
			"the encrypted-store sign-in token authenticates the test")
	})

	t.Run("signed out: not signed in, and nothing is spent", func(t *testing.T) {
		signedOutCodexHome(t)
		up := newSignInTestUpstream(t)
		api := newProviderValidationTestAPI(t, "", "")
		installEmbeddedCatalog(t, api)
		writeProviderConfigRows(t, api, map[string]any{
			"provider": "openai-chatgpt", "model": "gpt-5.4",
			"auth_method": "sign_in", "api_base": up.URL,
		})

		res := decodeOperationResult(t, doProviderTest(t, api, "openai-chatgpt"))

		assert.False(t, res.Success)
		require.NotNil(t, res.Error)
		assert.Equal(t, "not signed in", *res.Error)
		_, models := up.seen()
		assert.Empty(t, models, "a test with no saved login must not call the upstream")
	})

	t.Run("sign_in on a row with no mechanism points at the row that has one", func(t *testing.T) {
		api := newProviderValidationTestAPI(t, "", "")
		installEmbeddedCatalog(t, api)
		writeProviderConfigRows(t, api, map[string]any{
			"provider": "openai", "model": "gpt-5.4", "auth_method": "sign_in",
		})

		res := decodeOperationResult(t, doProviderTest(t, api, "openai"))

		assert.False(t, res.Success)
		require.NotNil(t, res.Error)
		assert.Equal(t, openAISignInElsewhereMsg, *res.Error)
	})

	t.Run("api_key rows are unchanged: no key is still 'no API key configured'", func(t *testing.T) {
		api := newProviderValidationTestAPI(t, "", "")
		installEmbeddedCatalog(t, api)
		writeProviderConfigRows(t, api, map[string]any{"provider": "openai", "model": "gpt-5.4"})

		res := decodeOperationResult(t, doProviderTest(t, api, "openai"))

		assert.False(t, res.Success)
		require.NotNil(t, res.Error)
		assert.Equal(t, "no API key configured for this provider", *res.Error)
	})
}

// installEmbeddedWindowCatalog serves the committed catalog to the window
// ladder (rung 5) and the model resolver, restoring the empty default after.
func installEmbeddedWindowCatalog(t *testing.T) {
	t.Helper()
	cat, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
	require.NoError(t, err)
	agent.SetWindowCatalog(cat)
	installProbeCatalog(t, cat)
	t.Cleanup(func() { agent.SetWindowCatalog(nil) })
}

// H4 — an agent whose model is stored WITHOUT a provider runs on the provider
// the runtime resolves the model to (FR-040 rule 2). The window must come
// from THAT row, not from the cloud floor: the live log showed
// provider "" / z-ai/glm-5.3 / capability 128000 while the catalog says
// openrouter z-ai/glm-5.3 = 1,048,576.
func TestAgentContextWindow_ProviderlessModelUsesResolvedProvider(t *testing.T) {
	installEmbeddedWindowCatalog(t)
	cfg := &config.Config{
		Providers: []*config.ModelConfig{{Provider: "openrouter", Model: "z-ai/glm-5.3"}},
	}

	t.Run("no override: the catalog window of the resolved row", func(t *testing.T) {
		ac := &config.AgentConfig{ID: "glm", Model: &config.AgentModelConfig{Primary: "z-ai/glm-5.3"}}
		var ag gen.Agent
		applyAgentContextWindow(&ag, cfg, ac)
		require.NotNil(t, ag.ContextWindowEffective)
		assert.Equal(t, 1048576, *ag.ContextWindowEffective)
		require.NotNil(t, ag.ContextWindowSource)
		assert.Equal(t, gen.AgentContextWindowSource("catalog"), *ag.ContextWindowSource)
	})

	t.Run("an override below the real capability is not clamped to the floor", func(t *testing.T) {
		override := 500000
		withAgent := *cfg
		// Rung 1 reads the override from cfg.Agents.List, so the agent must
		// be in the config the ladder sees, as it is in production.
		withAgent.Agents.List = []config.AgentConfig{{
			ID: "glm", Model: &config.AgentModelConfig{Primary: "z-ai/glm-5.3"},
			ContextWindowOverride: &override,
		}}
		ac := &withAgent.Agents.List[0]
		var ag gen.Agent
		applyAgentContextWindow(&ag, &withAgent, ac)
		require.NotNil(t, ag.ContextWindowEffective)
		assert.Equal(t, 500000, *ag.ContextWindowEffective)
		require.NotNil(t, ag.ContextWindowClamped)
		assert.False(t, *ag.ContextWindowClamped)
	})

	t.Run("the default-model card resolves a provider-less pair the same way", func(t *testing.T) {
		out := defaultModelResponse(cfg, config.DefaultModel{Model: "z-ai/glm-5.3"})
		require.NotNil(t, out.ContextWindow)
		assert.Equal(t, 1048576, *out.ContextWindow)
	})
}

// H1 — GET /providers/catalog, from the catalog exactly as the gateway boots
// it, no longer offers sign-in on the plain `openai` row (the UI renders a
// sign-in control iff `sign_in` is present), while every row with a real
// mechanism keeps it.
func TestProvidersCatalog_ServedHidesSignInWithoutMechanism(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = bootProviderCatalog(t.TempDir())

	w := getProvidersCatalog(api, "")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got gen.ProvidersCatalog
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	methods := map[string][]gen.CatalogProviderAuthMethods{}
	for _, p := range got.Providers {
		methods[p.Id] = p.AuthMethods
	}
	A := gen.CatalogProviderAuthMethodsApiKey
	S := gen.CatalogProviderAuthMethodsSignIn
	assert.Equal(t, []gen.CatalogProviderAuthMethods{A}, methods["openai"],
		"openai has no sign-in mechanism: only openai-chatgpt can source an OpenAI login")
	assert.Equal(t, []gen.CatalogProviderAuthMethods{S}, methods["openai-chatgpt"])
	assert.Equal(t, []gen.CatalogProviderAuthMethods{S}, methods["codex-cli"])
	assert.Equal(t, []gen.CatalogProviderAuthMethods{S}, methods["github-copilot"])
	assert.Equal(t, []gen.CatalogProviderAuthMethods{A, S}, methods["xai"],
		"xai owns a device-code sign-in (OAuthEntryOwner) and keeps both methods")

	// The parsed document stays a faithful registry mirror: only the
	// served projection is filtered.
	row, ok := api.providerCatalog.Provider("openai")
	require.True(t, ok)
	assert.Contains(t, row.AuthMethods, catalog.AuthSignIn)
}
