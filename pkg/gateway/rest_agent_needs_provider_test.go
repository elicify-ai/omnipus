// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-067 T067-09 — the gateway half of the per-agent degrade.
//
// Spec: docs/internal/specs/adr-067-registry-catalog-spec.md FR-015 (no hint
// anywhere), FR-016 (boot survives; the provider row is `unknown-provider`,
// the bound agent is `degraded_reason: needs_provider`), FR-031
// (`needs_provider` and `needs_model` are separate fields and may both be
// true), FR-036 (exact ids). Scenarios US-6.AC1/AC2/AC3/AC4/AC6; dataset DS-5
// row 18. Tests T41 and T33c; proof SC-010.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// installFixtureCatalog installs the committed 2.0.0 fixture document as BOTH
// the restAPI's served catalog and the process catalog every provider
// construction resolves against, restoring the previous process catalog on
// cleanup. Using one document on both sides is what production does
// (gateway.go calls providers.SetCatalog with the document it booted), and it
// is what makes the two `unknown` verdicts in this test — the provider row's
// and the agent's — provably the same decision.
func installFixtureCatalog(t *testing.T, api *restAPI) *catalog.Catalog {
	t.Helper()
	raw, err := os.ReadFile("../providers/catalog/testdata/providers_catalog_2.0.0_fixture.json")
	require.NoError(t, err)
	cat, err := catalog.NewCatalog(raw)
	require.NoError(t, err)
	api.providerCatalog = cat
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(nil) })
	return cat
}

func getAgentsList(t *testing.T, api *restAPI) ([]gen.Agent, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET agents must be 200; body=%s", w.Body.String())
	var agents []gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agents))
	return agents, w.Body.String()
}

// TestGatewayBoot_UnknownProvider_NonFatal — T41 (US-6.AC1/AC2/AC4, SC-010).
//
// A config naming an unknown provider must still produce a working
// installation: the agent loop CONSTRUCTS (the step that would abort boot if
// an unknown provider were fatal — MAJ-010), both agents are registered, the
// unknown provider is reported as its own row state, and only the agent bound
// to it is degraded. The turn-level half (A runs, B refuses with zero
// upstream requests) is pkg/agent's TestAgentTurn_NeedsProvider_TypedRefusal.
//
// SC-010's no-hint proof is asserted the one way the spec allows: on the
// ABSENCE of the canonical id (`zai`) in the two response bodies. The
// operator's own spelling (`z-ai`) is echoed freely — quoting back what
// someone typed is not a hint; offering them a different id would be.
func TestGatewayBoot_UnknownProvider_NonFatal(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	installFixtureCatalog(t, api)

	// Both rows are configured (a non-empty `models` list is enough — FR-029)
	// and NEITHER carries a credential, which keeps the GET hermetic: the
	// upstream /models fetch only runs for a provider whose key resolves.
	cfg := api.agentLoop.GetConfig()
	cfg.Providers = []*config.ModelConfig{
		{
			Name:     "openrouter-1",
			Provider: "openrouter",
			Model:    "z-ai/glm-5.2",
			Models:   []string{"z-ai/glm-5.2"},
		},
		{
			// A non-canonical spelling of a real vendor. ADR-067 deleted
			// every rename path, so this is simply unknown.
			Name:     "z-ai",
			Provider: "z-ai",
			Model:    "glm-5.2",
			Models:   []string{"glm-5.2"},
		},
	}
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5.2"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "agent-a", Name: "A"},
		{
			ID:    "agent-b",
			Name:  "B",
			Model: &config.AgentModelConfig{Primary: "glm-5.2", Provider: "z-ai"},
		},
	}

	// The construction MAJ-010 requires to survive: a registry built from a
	// config that names an unknown provider. A fatal treatment here is what
	// would leave an operator with no UI path to repair the mistake.
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	for _, id := range []string{"agent-a", "agent-b"} {
		_, ok := al.GetRegistry().GetAgent(id)
		assert.True(t, ok, "agent %q must be registered despite the unknown provider row", id)
	}

	t.Run("the provider row reports unknown-provider with no models", func(t *testing.T) {
		provs := getProviders(t, api)
		byID := map[string]gen.Provider{}
		for _, p := range provs {
			byID[p.Id] = p
		}
		row, ok := byID["z-ai"]
		require.True(t, ok, "the unknown row must still be listed; got %+v", provs)
		assert.Equal(t, gen.ProviderStatusUnknownProvider, row.Status)
		require.NotNil(t, row.Models)
		assert.Empty(t, row.Models, "an unknown-provider row carries no models")
		require.NotNil(t, row.Error)
		assert.Equal(t, `unknown provider "z-ai"`, *row.Error,
			"the message names the operator's own id and nothing else")
		assert.NotEqual(t, gen.ProviderStatusUnknownProvider, byID["openrouter"].Status,
			"a catalog id must not be classified unknown")
	})

	t.Run("only the bound agent is degraded (DS-5 row 18)", func(t *testing.T) {
		agents, _ := getAgentsList(t, api)
		byID := map[string]gen.Agent{}
		for _, a := range agents {
			byID[a.Id] = a
		}
		require.Contains(t, byID, "agent-a")
		require.Contains(t, byID, "agent-b")
		assert.Nil(t, byID["agent-a"].DegradedReason,
			"agent A is bound to a catalog provider and must not be degraded")
		require.NotNil(t, byID["agent-b"].DegradedReason,
			"agent B is bound to an unknown provider and must carry degraded_reason")
		assert.Equal(t, gen.NeedsProvider, *byID["agent-b"].DegradedReason)
	})

	t.Run("no hint anywhere (FR-015 / SC-010)", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
		r.URL.Path = "/api/v1/providers"
		api.HandleProviders(w, isolateRateLimit(t, r))
		providersBody := w.Body.String()
		_, agentsBody := getAgentsList(t, api)

		for name, body := range map[string]string{
			"GET /providers": providersBody,
			"GET /agents":    agentsBody,
		} {
			assert.Contains(t, body, "z-ai",
				"%s must echo the operator's own id", name)
			assert.NotContains(t, body, `"zai"`,
				"%s must not name the canonical id — no rename, alias or suggestion (SC-010); body=%s", name, body)
			assert.NotContains(t, strings.ToLower(body), "did you mean",
				"%s must offer no suggestion; body=%s", name, body)
		}
	})
}

// TestAgentRepair_PUTProvider_NoRestart — T33c (US-6.AC3). A degraded agent is
// repaired through the EXISTING agent-update path: the PUT response itself
// already shows the degrade cleared, and the next GET agrees. Nothing beyond
// the reload that handler already performs is needed — no process restart, and
// no separate "re-validate providers" action for the operator to discover.
func TestAgentRepair_PUTProvider_NoRestart(t *testing.T) {
	api := buildExecutorTestAPI(t)
	installFixtureCatalog(t, api)

	created := postAgentProvider(t, api,
		`{"name":"Repairable","type":"Main","soul":"s","model":"glm-5.2","provider":"z-ai"}`)
	// The POST response is silent on the DERIVED flags (it is silent on
	// ADR-068's needs_model too — the create path builds its echo before the
	// live config is re-read); the list/get/update trio is where they are
	// projected. The GET immediately after the create is therefore the first
	// place the degrade appears, and it must appear there with no reload.
	gotDegraded := getAgentResp(t, api, created.Id).DegradedReason
	require.NotNil(t, gotDegraded,
		"an agent bound to an unknown provider must be degraded on the very next GET")
	assert.Equal(t, gen.NeedsProvider, *gotDegraded)

	// The repair: re-point the agent at a real provider.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"provider":"openrouter"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())
	updated := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, updated.DegradedReason,
		"the PUT response must already show the degrade cleared (US-6.AC3); body=%s", w.Body.String())

	assert.Nil(t, getAgentResp(t, api, created.Id).DegradedReason,
		"and the next GET must agree — no restart, no second action")
}

// TestAgentDegradedReason_AbsentCatalogNeverDegrades pins the E7 posture the
// provider list already takes: when no catalog document is loaded at all, no
// id can be judged unknown, so NO agent is degraded. The alternative — every
// agent in the install degrading at once because a snapshot failed to parse —
// is the failure mode this guard exists to prevent.
func TestAgentDegradedReason_AbsentCatalogNeverDegrades(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{{Name: "z-ai", Provider: "z-ai", Model: "glm-5.2"}},
	}
	ac := &config.AgentConfig{
		ID:    "agent-b",
		Model: &config.AgentModelConfig{Primary: "glm-5.2", Provider: "z-ai"},
	}
	assert.Nil(t, agentDegradedReason(nil, cfg, ac),
		"a nil catalog must not degrade anything")
	assert.Nil(t, agentDegradedReason(catalog.New(), cfg, ac),
		"an empty catalog (no document applied) must not degrade anything either")
}
