// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Regression tests for the 2026-10-13 provider hotfix (H1, H4), driven
// through the real HTTP handlers against the REAL committed catalog
// (catalog.EmbeddedSnapshot) — the document the founder's instance served.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
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

// H1 (test-analyzer I2) — onboarding completion is the other path that
// persisted the dead row: completing onboarding with the plain `openai` row
// and auth_method sign_in is refused before anything is written, pointing at
// the row that can sign in. (The valid sign-in completion control is
// rest_onboarding_complete_test.go's codex-cli case.)
func TestOnboardingComplete_SignInOnRowWithoutMechanism400(t *testing.T) {
	withEdition(t, config.EditionHosted)
	api, tmpDir := newAuthMethodOnboardingAPI(t)
	installEmbeddedCatalog(t, api)
	before, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	body := `{"provider":{"auth_method":"sign_in","id":"openai","model":"gpt-5.4"},` +
		`"preferences":{"name":"Daniel","tone":"direct","detail":"brief"}}`

	w := postOnboardingComplete(api, body)

	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, openAISignInElsewhereMsg, resp["error"])
	assert.Equal(t, "id", resp["field"])
	assert.False(t, api.onboardingMgr.IsComplete(), "a refused completion leaves onboarding open for a retry")
	after, err := os.ReadFile(tmpDir + "/config.json")
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused completion persists nothing")
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
		// Rung 1 reads the override from cfg.Agents.List, so the agent must
		// be in the config the ladder sees, as it is in production.
		withAgent := config.Config{Providers: cfg.Providers}
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

// H4 (test-analyzer I4) — the boot-time prime of unknown windows used to
// skip any agent whose stored model has no provider: its local endpoint was
// never asked for its window, so every turn stayed refused with
// context_window_unknown. It now asks under the provider the model runs on.
func TestPrimeUnknownWindows_ProviderlessLocalAgentIsAsked(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Provider: "ollama", Model: "llama3.1:8b"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				// Pins a primary model WITHOUT a provider: agentPrimaryProviderID
				// answers "" for it.
				{ID: "bare-local", Name: "Bare", Model: &config.AgentModelConfig{Primary: "qwen3:8b"}},
			},
		},
		Providers: []*config.ModelConfig{
			{Provider: "ollama", Model: "llama3.1:8b", Models: []string{"qwen3:8b"}, APIBase: "http://127.0.0.1:11434"},
		},
		Context: config.DefaultContextSettings(),
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), marshalConfigForDisk(t, cfg), 0o600))
	seedAgentEntities(t, home, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	inst, ok := al.GetRegistry().GetAgent("bare-local")
	require.True(t, ok)
	require.True(t, inst.WindowUnknown, "precondition: the provider-less local agent's window is unknown")
	require.Equal(t, "", agentPrimaryProviderID(cfg, &cfg.Agents.List[0]), "precondition: stored without a provider")

	var mu sync.Mutex
	var asked []string
	agent.SetLiveWindowLookup(func(provider, _, model string) (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, provider+"/"+model)
		return 0, false
	})
	t.Cleanup(func() { agent.SetLiveWindowLookup(nil) })

	primeUnknownWindows(al)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, asked, "ollama/qwen3:8b",
		"the provider-less agent is primed under the provider its model runs on")
}
