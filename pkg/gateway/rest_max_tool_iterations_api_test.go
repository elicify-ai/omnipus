// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Backend-lead's own tests for the #904 tool-iteration limit API
// (docs/internal/specs/tool-iteration-limit-spec.md). Expected values come
// from the spec's datasets and Machine-Verifiable Constraints, not from the
// implementation. qa-lead's independent RED pack is separate.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newLimitAPI builds a restAPI with global limit `global` (on disk and in
// memory) and the given agents persisted in the agent store.
func newLimitAPI(t *testing.T, global int, agents ...config.AgentConfig) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv(config.MaxToolIterationsEnvVar, "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: global,
			},
			List: agents,
		},
		Context: config.DefaultContextSettings(),
	}
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), marshalConfigForDisk(t, cfg), 0o600))
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, agents)
	return api
}

func storedOwnLimit(t *testing.T, api *restAPI, id string) int {
	t.Helper()
	st, err := agentstore.New(api.homePath).ReadState(id)
	require.NoError(t, err)
	return st.Agent.MaxToolIterations
}

func savedGlobal(t *testing.T, api *restAPI) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	agents, ok := m["agents"].(map[string]any)
	require.True(t, ok, "config.json agents is not an object: %v", m["agents"])
	defaults, ok := agents["defaults"].(map[string]any)
	require.True(t, ok, "config.json agents.defaults is not an object: %v", agents["defaults"])
	return defaults["max_tool_iterations"]
}

func putPerformanceJSON(t *testing.T, api *restAPI, body string, withToken bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if withToken {
		r = withReAuthAdmin(t, api, r)
	} else {
		r = withReAuthAdminNoToken(r)
	}
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	return w
}

func agentWire(t *testing.T, w *httptest.ResponseRecorder) gen.Agent {
	t.Helper()
	var ag gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ag), "body=%s", w.Body.String())
	return ag
}

// Spec BDD "Fresh install shows the shipped global" / "Saved global" row 1.
func TestMaxToolIterations_GetPerformance_ReportsGlobal(t *testing.T) {
	api := newLimitAPI(t, 300)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var ps gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ps))
	require.NotNil(t, ps.MaxToolIterations)
	assert.Equal(t, 300, *ps.MaxToolIterations)
	require.NotNil(t, ps.MaxToolIterationsSavedState)
	assert.Equal(t, gen.MaxToolIterationsSavedStateOk, *ps.MaxToolIterationsSavedState)
	assert.Nil(t, ps.MaxToolIterationsSavedRaw, "saved_raw only for below_min/above_max")
}

// Spec BDD "Lowering preview lists only agents above the new value".
func TestMaxToolIterations_Preview_ListsOnlyAbove(t *testing.T) {
	api := newLimitAPI(t, 300,
		config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250},
		config.AgentConfig{ID: "agent-b", Name: "B", MaxToolIterations: 100},
		config.AgentConfig{ID: "agent-c", Name: "C", MaxToolIterations: 200},
		config.AgentConfig{ID: "agent-d", Name: "D"},
	)
	w := httptest.NewRecorder()
	api.HandleMaxToolIterationsPreview(w,
		httptest.NewRequest(http.MethodGet, "/api/v1/performance/max-tool-iterations/preview?value=200", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var p gen.MaxToolIterationsLoweringPreview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, 200, p.Value)
	assert.Equal(t, []gen.MaxToolIterationAgentChange{
		{AgentId: "agent-a", AgentName: "A", OldValue: 250, NewValue: 200},
	}, p.Agents)
	assert.Equal(t, 250, storedOwnLimit(t, api, "agent-a"), "preview writes nothing")

	for _, bad := range []string{"", "0", "1001", "abc", "2.5"} {
		w := httptest.NewRecorder()
		api.HandleMaxToolIterationsPreview(w,
			httptest.NewRequest(http.MethodGet, "/api/v1/performance/max-tool-iterations/preview?value="+bad, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, "value=%q", bad)
		assert.Contains(t, w.Body.String(), "value must be between 1 and 1000", "value=%q", bad)
	}
}

// Dataset "Global bounds" rows 3-5.
func TestMaxToolIterations_PutPerformance_OutOfRange(t *testing.T) {
	api := newLimitAPI(t, 200)
	for _, v := range []string{"0", "-1", "1001"} {
		w := putPerformanceJSON(t, api, `{"max_tool_iterations":`+v+`}`, true)
		assert.Equal(t, http.StatusBadRequest, w.Code, "value %s", v)
		assert.Contains(t, w.Body.String(), "max_tool_iterations must be between 1 and 1000")
	}
	assert.EqualValues(t, 200, savedGlobal(t, api))
}

// Spec BDD "Lowering without confirmation is refused" (dataset row 6): 409,
// nothing written, and the step-up token is NOT consumed (the same token then
// works for the confirmed PUT).
func TestMaxToolIterations_PutPerformance_NoConfirmation_409_TokenKept(t *testing.T) {
	api := newLimitAPI(t, 300, config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250})
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance", strings.NewReader(`{"max_tool_iterations":200}`))
	r = withReAuthAdmin(t, api, r)
	token := r.Header.Get(reAuthHeader)
	require.NotEmpty(t, token)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	var c gen.MaxToolIterationsLoweringConflict
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &c))
	assert.Equal(t, "max_tool_iterations_lowering_drift", c.Code)
	assert.Equal(t, "the agents affected by lowering the limit to 200 changed since the preview; "+
		"review the updated list and confirm again", c.Error)
	assert.Equal(t, []gen.MaxToolIterationAgentChange{
		{AgentId: "agent-a", AgentName: "A", OldValue: 250, NewValue: 200},
	}, c.Preview.Agents)
	assert.Equal(t, 250, storedOwnLimit(t, api, "agent-a"))
	assert.EqualValues(t, 300, savedGlobal(t, api))

	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`))
	r2 = withReAuthAdminNoToken(r2)
	r2.Header.Set(reAuthHeader, token)
	w2 := httptest.NewRecorder()
	api.HandlePerformance(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "token must survive the pre-check 409; body=%s", w2.Body.String())
}

// Dataset "Confirmed lowering" rows 2 (reordered → 200), 3/5 (drift → 409)
// and 7 (duplicate → 400).
func TestMaxToolIterations_PutPerformance_ConfirmedLowering(t *testing.T) {
	agents := func() []config.AgentConfig {
		return []config.AgentConfig{
			{ID: "agent-a", Name: "A", MaxToolIterations: 250},
			{ID: "agent-b", Name: "B", MaxToolIterations: 280},
		}
	}
	t.Run("missing agent is drift", func(t *testing.T) {
		api := newLimitAPI(t, 300, agents()...)
		w := putPerformanceJSON(t, api,
			`{"max_tool_iterations":200,"confirmed_lowering":[{"agent_id":"agent-a","old_value":250}]}`, true)
		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
		assert.Equal(t, 250, storedOwnLimit(t, api, "agent-a"))
		assert.Equal(t, 280, storedOwnLimit(t, api, "agent-b"))
		assert.EqualValues(t, 300, savedGlobal(t, api))
	})
	t.Run("changed old value is drift", func(t *testing.T) {
		api := newLimitAPI(t, 300, agents()...)
		w := putPerformanceJSON(t, api, `{"max_tool_iterations":200,"confirmed_lowering":`+
			`[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":270}]}`, true)
		require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	})
	t.Run("duplicate id is 400", func(t *testing.T) {
		api := newLimitAPI(t, 300, agents()...)
		w := putPerformanceJSON(t, api, `{"max_tool_iterations":200,"confirmed_lowering":`+
			`[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280}]}`, true)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "confirmed_lowering lists agent agent-a more than once")
	})
	t.Run("reordered confirmation lowers both", func(t *testing.T) {
		api := newLimitAPI(t, 300, agents()...)
		w := putPerformanceJSON(t, api, `{"max_tool_iterations":200,"confirmed_lowering":`+
			`[{"agent_id":"agent-b","old_value":280},{"agent_id":"agent-a","old_value":250}]}`, true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var ps gen.PerformanceSettings
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ps))
		require.NotNil(t, ps.MaxToolIterations)
		assert.Equal(t, 200, *ps.MaxToolIterations)
		require.NotNil(t, ps.MaxToolIterationsLoweredAgents)
		assert.ElementsMatch(t, []gen.MaxToolIterationAgentChange{
			{AgentId: "agent-a", AgentName: "A", OldValue: 250, NewValue: 200},
			{AgentId: "agent-b", AgentName: "B", OldValue: 280, NewValue: 200},
		}, *ps.MaxToolIterationsLoweredAgents)
		assert.Equal(t, 200, storedOwnLimit(t, api, "agent-a"))
		assert.Equal(t, 200, storedOwnLimit(t, api, "agent-b"))
		assert.EqualValues(t, 200, savedGlobal(t, api))
		assert.Equal(t, 200, api.agentLoop.GetConfig().Agents.Defaults.EffectiveGlobalMaxToolIterations().Value)
	})
}

// faultLimitStore wraps the real store and fails chosen MutateState calls
// (spec test row 13e seam).
type faultLimitStore struct {
	*agentstore.Store
	failOn map[string][]bool // id → per-call "fail this call?"
	calls  map[string]int
}

var errInjectedIO = errors.New("injected disk failure")

func (f *faultLimitStore) MutateState(id, rev string, mutate func(*config.AgentConfig) error, soul *string) (agentstore.MutationResult, error) {
	n := f.calls[id]
	f.calls[id] = n + 1
	if plan := f.failOn[id]; n < len(plan) && plan[n] {
		return agentstore.MutationResult{}, errInjectedIO
	}
	return f.Store.MutateState(id, rev, mutate, soul)
}

// Spec BDD "Mid-write failure rolls back" and "Failed rollback is reported".
func TestMaxToolIterations_PutPerformance_Rollback(t *testing.T) {
	body := `{"max_tool_iterations":200,"confirmed_lowering":` +
		`[{"agent_id":"agent-a","old_value":250},{"agent_id":"agent-b","old_value":280}]}`
	setup := func(t *testing.T, failOn map[string][]bool) *restAPI {
		api := newLimitAPI(t, 300,
			config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 250},
			config.AgentConfig{ID: "agent-b", Name: "B", MaxToolIterations: 280})
		api.limitAgentStore = &faultLimitStore{Store: agentstore.New(api.homePath), failOn: failOn, calls: map[string]int{}}
		return api
	}
	t.Run("B fails, A restored", func(t *testing.T) {
		api := setup(t, map[string][]bool{"agent-b": {true}})
		w := putPerformanceJSON(t, api, body, true)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		var e gen.ErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
		require.NotNil(t, e.Code)
		assert.Equal(t, "max_tool_iterations_lowering_failed", *e.Code)
		assert.Contains(t, e.Error, "nothing was changed")
		assert.Contains(t, e.Error, "B")
		assert.Equal(t, 250, storedOwnLimit(t, api, "agent-a"))
		assert.Equal(t, 280, storedOwnLimit(t, api, "agent-b"))
		assert.EqualValues(t, 300, savedGlobal(t, api))
	})
	t.Run("A's restore also fails", func(t *testing.T) {
		api := setup(t, map[string][]bool{"agent-b": {true}, "agent-a": {false, true}})
		w := putPerformanceJSON(t, api, body, true)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		var e gen.ErrorResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
		require.NotNil(t, e.Code)
		assert.Equal(t, "max_tool_iterations_rollback_incomplete", *e.Code)
		assert.Contains(t, e.Error, "limit not changed; could not restore A (now 200, was 250)")
		assert.Equal(t, 200, storedOwnLimit(t, api, "agent-a"))
		assert.EqualValues(t, 300, savedGlobal(t, api))
	})
}

// Dataset "Resolver" rows 2/4-5 through the Agent API; spec BDD "Operator
// lowers one agent", "Use global limit clears", "Per-agent value above the
// global is refused", "Omitted field leaves the own value unchanged".
func TestMaxToolIterations_AgentPut(t *testing.T) {
	api := newLimitAPI(t, 200, config.AgentConfig{ID: "agent-a", Name: "A"})

	w := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":50}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	ag := agentWire(t, w)
	assert.Equal(t, 50, ag.MaxToolIterations)
	assert.Equal(t, gen.MaxToolIterationsSourceAgent, ag.MaxToolIterationsSource)
	require.NotNil(t, ag.MaxToolIterationsOverride)
	assert.Equal(t, 50, *ag.MaxToolIterationsOverride)
	assert.False(t, ag.MaxToolIterationsOverrideIgnored)

	w = putAgentJSON(t, api, "agent-a", `{"description":"x"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, 50, storedOwnLimit(t, api, "agent-a"), "omitted field leaves the own value")

	w = putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":300}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "max_tool_iterations 300 is above the global limit (200); "+
		"lower it, or raise the global limit in Settings → Performance")
	assert.Equal(t, 50, storedOwnLimit(t, api, "agent-a"))

	for _, v := range []string{"0", "-3", "1001"} {
		w = putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":`+v+`}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "value %s: %s", v, w.Body.String())
		assert.Contains(t, w.Body.String(), "max_tool_iterations must be between 1 and 1000")
	}

	w = putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":null}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	ag = agentWire(t, w)
	assert.Equal(t, 200, ag.MaxToolIterations)
	assert.Equal(t, gen.MaxToolIterationsSourceGlobal, ag.MaxToolIterationsSource)
	assert.Nil(t, ag.MaxToolIterationsOverride)
	assert.Equal(t, 0, storedOwnLimit(t, api, "agent-a"))
	raw, err := os.ReadFile(filepath.Join(agentstore.New(api.homePath).Dir(), "agent-a.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "max_tool_iterations", "a cleared own value leaves no key")
}

// Spec BDD "Profile flags an ignored own value" (Resolver row 5) on GET.
func TestMaxToolIterations_AgentGet_CappedAndFlagged(t *testing.T) {
	api := newLimitAPI(t, 200, config.AgentConfig{ID: "agent-a", Name: "A", MaxToolIterations: 500})
	w := httptest.NewRecorder()
	api.HandleAgents(w, httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	ag := agentWire(t, w)
	assert.Equal(t, 200, ag.MaxToolIterations)
	assert.Equal(t, gen.MaxToolIterationsSourceGlobal, ag.MaxToolIterationsSource)
	require.NotNil(t, ag.MaxToolIterationsOverride)
	assert.Equal(t, 500, *ag.MaxToolIterationsOverride)
	assert.True(t, ag.MaxToolIterationsOverrideIgnored)
}

// Spec BDD "Create keeps the requested own value" (FR-014) and "Create above
// the global is refused".
func TestMaxToolIterations_AgentCreate(t *testing.T) {
	api := newLimitAPI(t, 200)
	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.createAgent(w, withAdminRole(r))
		return w
	}
	w := post(`{"type":"Main","name":"Forty","soul":"s","max_tool_iterations":40}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	ag := agentWire(t, w)
	assert.Equal(t, 40, ag.MaxToolIterations)
	assert.Equal(t, gen.MaxToolIterationsSourceAgent, ag.MaxToolIterationsSource)
	assert.Equal(t, 40, storedOwnLimit(t, api, ag.Id))

	before, _, err := agentstore.New(api.homePath).List()
	require.NoError(t, err)
	w = post(`{"type":"Main","name":"Five hundred","soul":"s","max_tool_iterations":500}`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "above the global limit (200)")
	after, _, err := agentstore.New(api.homePath).List()
	require.NoError(t, err)
	assert.Len(t, after, len(before), "a refused create persists no agent")
}

// Spec BDD "Worker PUT of the limit is accepted" (D14).
func TestMaxToolIterations_Subagent3pPutAccepted(t *testing.T) {
	api := newLimitAPI(t, 200, config.AgentConfig{
		ID: "worker-3p", Name: "W", Description: "d", Type: config.AgentTypeWorker,
		Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{
			Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIPath: "/usr/local/bin/codex"}},
	})
	w := putAgentJSON(t, api, "worker-3p", `{"max_tool_iterations":60}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	ag := agentWire(t, w)
	require.NotNil(t, ag.MaxToolIterationsOverride)
	assert.Equal(t, 60, *ag.MaxToolIterationsOverride)
	assert.Equal(t, 60, storedOwnLimit(t, api, "worker-3p"))
}

// Spec BDD "Worker preview equals runtime" (D4): the preview's turn cap is
// the resolver's effective value.
func TestMaxToolIterations_ExecutorPreview(t *testing.T) {
	api := newLimitAPI(t, 200)
	preview := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		api.postAgentsExecutorPreview(w,
			httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-preview", strings.NewReader(body)))
		return w
	}
	turnCap := func(w *httptest.ResponseRecorder) string {
		var resp gen.ExecutorCommandPreviewResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
		for i, a := range resp.Argv {
			if a == "--max-turns" && i+1 < len(resp.Argv) {
				return resp.Argv[i+1]
			}
		}
		t.Fatalf("no --max-turns in argv %v", resp.Argv)
		return ""
	}
	w := preview(`{"cli":"claude-code"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "200", turnCap(w))
	w = preview(`{"cli":"claude-code","max_tool_iterations":30}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "30", turnCap(w))
	w = preview(`{"cli":"claude-code","max_tool_iterations":500}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "200", turnCap(w), "an own value above the global runs at the global")
	w = preview(`{"cli":"claude-code","max_tool_iterations":0}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}
