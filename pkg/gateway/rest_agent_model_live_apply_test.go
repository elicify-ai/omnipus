// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// UAT E-7 root cause: PUT /api/v1/agents/{id} saved a model or provider change
// and answered 200 while the running agent kept serving its previous model
// ("model saved to config but could not be applied to the running agent").
//
// The oracle in every test here is the LIVE registry instance, never the PUT
// response alone: needs_model and degraded_reason on the response are derived
// from the saved config, so they can say "changed" while the running agent has
// not changed at all. That is exactly how the defect stayed green.
//
// Spec: ADR-067 US-6.AC3 (re-pointing an agent through the agent update path
// takes effect without a restart; a switch onto an unknown provider leaves the
// agent refusing turns), ADR-068 FR-014 (needs_model), CLAUDE.md's ADR-037 rule
// (a control that reports saved but changes nothing is banned).

package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// pinOnlyOpenAIProvider rewrites the on-disk config so its providers list holds
// a single dedicated OpenAI row. An explicit providers list suppresses the
// default passthrough seeding, so a model slug that no configured provider
// offers has no route at all.
func pinOnlyOpenAIProvider(t *testing.T, api *restAPI) {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	m["providers"] = []map[string]any{
		{
			"provider":   "openai",
			"model_name": "gpt-4o",
			"model":      "openai/gpt-4o",
			"models":     []string{"gpt-4o", "gpt-4o-mini"},
			"api_base":   "https://api.openai.com/v1",
			"api_key":    "sk-test-dummy",
		},
	}
	out, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
}

// liveAgent returns the agent instance the registry currently serves turns
// from.
func liveAgent(t *testing.T, api *restAPI, id string) *agent.AgentInstance {
	t.Helper()
	inst, ok := api.agentLoop.GetRegistry().GetAgent(id)
	require.True(t, ok, "agent %q must be registered", id)
	require.NotNil(t, inst)
	return inst
}

// storedPrimaryModel reads the primary model from the agent's saved entity
// record, the source every later boot or reload builds the agent from.
func storedPrimaryModel(t *testing.T, api *restAPI, id string) string {
	t.Helper()
	rec, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err)
	if rec.Model == nil {
		return ""
	}
	return rec.Model.Primary
}

// TestUpdateAgent_UnresolvableModel_IsAppliedToRunningAgent is the E-7 defect
// itself: a model no configured provider offers used to be saved while the
// running agent silently kept its previous model. It must now be saved and
// applied, so the saved record and the running agent agree, and the response
// must say the model cannot run rather than carry a "saved but not applied"
// warning.
func TestUpdateAgent_UnresolvableModel_IsAppliedToRunningAgent(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)

	const model = "anthropic/omnipus-nonexistent-model-zzz"
	require.NotEqual(t, model, liveAgent(t, api, "test-agent").Model,
		"precondition: the running agent must start on a different model")

	w := putAgentJSON(t, api, "test-agent", `{"model":"`+model+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())

	assert.Equal(t, model, liveAgent(t, api, "test-agent").Model,
		"the running agent must serve the model that was just saved, not keep its previous one")
	assert.Equal(t, model, storedPrimaryModel(t, api, "test-agent"),
		"the saved record must hold the same model the running agent serves")
	assert.Nil(t, resp.Warning,
		"a change that was applied must not be reported as saved-but-not-applied; body=%s", w.Body.String())
	assert.True(t, resp.NeedsModel,
		"ADR-068 FR-014: a model whose provider is not configured must be flagged needs_model; body=%s", w.Body.String())
}

// TestUpdateAgent_UnknownProvider_RunningAgentMovesToIt reproduces the UAT E-7
// tester's action: point an agent's model and provider at values that do not
// exist. ADR-067 US-6 keeps such an agent saved and degraded (it refuses turns
// with needs_provider) rather than rejecting the edit, so the running agent
// must route through the new provider, and the response must show the degrade.
func TestUpdateAgent_UnknownProvider_RunningAgentMovesToIt(t *testing.T) {
	api := buildExecutorTestAPI(t)
	installFixtureCatalog(t, api)

	const model, provider = "nonexistent-vendor/no-such-model-e7", "no-such-provider-e7"
	w := putAgentJSON(t, api, "test-agent", `{"model":"`+model+`","provider":"`+provider+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())

	live := liveAgent(t, api, "test-agent")
	assert.Equal(t, model, live.Model, "the running agent must serve the saved model")
	require.NotEmpty(t, live.Candidates, "the running agent must have a primary candidate")
	assert.Equal(t, providers.FallbackCandidate{Provider: provider, Model: model}, live.Candidates[0],
		"a pinned provider routes the primary candidate directly (O3), so the running agent must route to the saved provider")
	require.NotNil(t, resp.DegradedReason,
		"an agent bound to an unknown provider must say so in the same response; body=%s", w.Body.String())
	assert.Equal(t, gen.AgentDegradedReasonNeedsProvider, *resp.DegradedReason)
}

// TestUpdateAgent_ModelChange_AppliedLiveWithoutFullReload covers the working
// case and the two changes the old in-place path never applied: a model and
// provider change, then a fallback-only change. Each must reach the running
// agent through the single-agent rebuild, never through the full reload that
// restarts channels and drops the WebSocket (#73, #571).
func TestUpdateAgent_ModelChange_AppliedLiveWithoutFullReload(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)
	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	w := putAgentJSON(t, api, "test-agent", `{"model":"gpt-4o","provider":"openai"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())

	live := liveAgent(t, api, "test-agent")
	assert.Equal(t, "gpt-4o", live.Model)
	require.NotEmpty(t, live.Candidates)
	assert.Equal(t, providers.FallbackCandidate{Provider: "openai", Model: "gpt-4o"}, live.Candidates[0])
	assert.Empty(t, live.FallbackModels, "no fallback was saved")
	assert.False(t, resp.NeedsModel, "gpt-4o on the configured openai provider can run; body=%s", w.Body.String())
	assert.Nil(t, resp.Warning, "body=%s", w.Body.String())

	w = putAgentJSON(t, api, "test-agent", `{"fallback_models":[{"model":"gpt-4o-mini","provider":"openai"}]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, []config.FallbackModel{{Model: "gpt-4o-mini", Provider: "openai"}},
		liveAgent(t, api, "test-agent").FallbackModels,
		"a fallback-only change must reach the running agent")

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a model change must rebuild only this agent, never trigger the full reload")
}

// TestUpdateAgent_UnchangedModelResent_DoesNotRebuildAgent: AgentProfile's
// autosave resends model, provider and fallback_models on every save. Only a
// real change may rebuild the running agent.
func TestUpdateAgent_UnchangedModelResent_DoesNotRebuildAgent(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)
	const body = `{"model":"gpt-4o","provider":"openai","fallback_models":[{"model":"gpt-4o-mini","provider":"openai"}]}`

	w := putAgentJSON(t, api, "test-agent", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	rebuilt := liveAgent(t, api, "test-agent")
	require.Equal(t, "gpt-4o", rebuilt.Model, "precondition: the first save must apply the model")

	w = putAgentJSON(t, api, "test-agent", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Same(t, rebuilt, liveAgent(t, api, "test-agent"),
		"resending the saved model unchanged must not rebuild the running agent")
}

// TestUpdateAgent_RebuildFailure_IsNotReportedAsSuccess: when the rebuilt agent
// cannot be published at all (the single-agent swap and its full-reload
// fallback both fail), the running agent still serves the previous model. A
// 200 would repeat the E-7 defect, so the request must fail and say why.
func TestUpdateAgent_RebuildFailure_IsNotReportedAsSuccess(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)
	previous := liveAgent(t, api, "test-agent").Model
	api.testForceFastUpsertErr = errors.New("fast upsert boom")
	api.agentLoop.SetReloadFunc(func() error { return errors.New("reload boom") })

	w := putAgentJSON(t, api, "test-agent", `{"model":"gpt-4o","provider":"openai"}`)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "the running agent could not be updated")
	assert.Contains(t, w.Body.String(), "reload boom", "the error must carry the underlying cause")
	assert.Equal(t, previous, liveAgent(t, api, "test-agent").Model,
		"the running agent really is still on the previous model, which is why this cannot be a success")
}
