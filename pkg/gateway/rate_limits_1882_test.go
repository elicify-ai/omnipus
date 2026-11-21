// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rate_limits_1882_test.go — #1882 rider, R3: the positive control carried
// over from the repro commit 5f7d429edd (branch local/1882-repro). It proves
// that the dedicated route PUT /api/v1/security/rate-limits sets the limit the
// server enforces and reads back the same. It passes today and must keep
// passing: the fix routes the Settings save through this endpoint.
//
// The PUT /config save-path repro test of that commit is deliberately NOT
// carried over: under the chief ruling of 2026-11-18 that path stays blocked
// by design (see security_keys_blocked_1882_test.go).
//
// Oracle (from the issue Expected, never from the implementation): a
// per-agent limit set through the dedicated endpoint is the limit the server
// enforces (cfg.Sandbox.RateLimits, read by beginIteration), served on the
// wire by GET /api/v1/security/rate-limits and GET /api/v1/config.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

const (
	savedLLMLimitPerHour = 42
	savedToolLimitPerMin = 7
	settings1882AgentID  = "settings-1882-agent"
)

// new1882TestAPI builds a restAPI over a real temp-dir config.json: minimal
// on-disk config, one custom agent in the entity store (so the strict roster
// population has a record), a real AgentLoop, and one successful production
// refresh BEFORE any assertion, so a later failure belongs to the test and not
// to the harness.
func new1882TestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	cfgJSON := `{"version":` + jsonNumber(config.CurrentVersion) +
		`,"agents":{"defaults":{"workspace":` + quoteJSON(tmpDir) +
		`,"model_name":"test-model","max_tokens":4096}},"providers":[]}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(cfgJSON), 0o600))

	store := agentstore.New(tmpDir)
	ac := config.AgentConfig{
		ID:    settings1882AgentID,
		Name:  "Settings 1882 Agent",
		Type:  config.AgentTypeCustom,
		Tools: coreagent.NewCustomAgentToolsCfg(),
	}
	require.NoError(t, store.Create(settings1882AgentID, &ac), "SETUP: seeding the agent store must succeed")

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{ac},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()),
		"SETUP: the production refresh must succeed on this fixture before the repro runs")
	return api
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Test1882_PositiveControl_DedicatedEndpointSetsTheEnforcedLimit is the R3
// positive control: the enforcement reads must PASS when the limit is set via
// PUT /api/v1/security/rate-limits, the endpoint that writes the enforced
// location (pkg/gateway/rest_rate_limits.go::putRateLimits). Green here proves
// the Settings fix has a working target to route to.
func Test1882_PositiveControl_DedicatedEndpointSetsTheEnforcedLimit(t *testing.T) {
	api := new1882TestAPI(t)

	w := putRateLimits(t, api,
		`{"max_agent_llm_calls_per_hour":42,"max_agent_tool_calls_per_minute":7}`)
	require.Equal(t, http.StatusOK, w.Code,
		"POSITIVE CONTROL: PUT /api/v1/security/rate-limits must succeed: %s", w.Body.String())
	var putResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	require.Equal(t, true, putResp["saved"], "POSITIVE CONTROL: the PUT must report saved=true")

	// In-memory enforcement value (what beginIteration reads).
	enforced := api.agentLoop.GetConfig().Sandbox.RateLimits
	require.Equal(t, savedLLMLimitPerHour, enforced.MaxAgentLLMCallsPerHour,
		"POSITIVE CONTROL: cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour must equal the value set via the dedicated endpoint; expected %d, seen %d",
		savedLLMLimitPerHour, enforced.MaxAgentLLMCallsPerHour)
	require.Equal(t, savedToolLimitPerMin, enforced.MaxAgentToolCallsPerMinute,
		"POSITIVE CONTROL: cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute must equal the value set via the dedicated endpoint; expected %d, seen %d",
		savedToolLimitPerMin, enforced.MaxAgentToolCallsPerMinute)

	// Wire form of the enforced value.
	rlW := httptest.NewRecorder()
	rlReq := httptest.NewRequest(http.MethodGet, "/api/v1/security/rate-limits", nil)
	api.HandleRateLimits(rlW, rlReq)
	require.Equal(t, http.StatusOK, rlW.Code,
		"POSITIVE CONTROL: GET /api/v1/security/rate-limits must answer 200: %s", rlW.Body.String())
	var rlResp map[string]any
	require.NoError(t, json.Unmarshal(rlW.Body.Bytes(), &rlResp))
	require.EqualValues(t, savedLLMLimitPerHour, rlResp["max_agent_llm_calls_per_hour"],
		"POSITIVE CONTROL: the wire form of the enforced hourly limit must equal the saved value; expected %d, seen %v",
		savedLLMLimitPerHour, rlResp["max_agent_llm_calls_per_hour"])
	require.EqualValues(t, savedToolLimitPerMin, rlResp["max_agent_tool_calls_per_minute"],
		"POSITIVE CONTROL: the wire form of the enforced per-minute limit must equal the saved value; expected %d, seen %v",
		savedToolLimitPerMin, rlResp["max_agent_tool_calls_per_minute"])

	// The enforced location as GET /api/v1/config serves it (sandbox.rate_limits).
	getW := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code,
		"POSITIVE CONTROL: GET /api/v1/config must answer 200: %s", getW.Body.String())
	var wire map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &wire))
	sb, ok := wire["sandbox"].(map[string]any)
	require.True(t, ok, "POSITIVE CONTROL: GET /api/v1/config must serve the sandbox section: %s", getW.Body.String())
	rl, ok := sb["rate_limits"].(map[string]any)
	require.True(t, ok, "POSITIVE CONTROL: GET /api/v1/config must serve sandbox.rate_limits once set: %s", getW.Body.String())
	require.EqualValues(t, savedLLMLimitPerHour, rl["max_agent_llm_calls_per_hour"],
		"POSITIVE CONTROL: sandbox.rate_limits.max_agent_llm_calls_per_hour on GET /api/v1/config must equal the saved value; expected %d, seen %v",
		savedLLMLimitPerHour, rl["max_agent_llm_calls_per_hour"])
	require.EqualValues(t, savedToolLimitPerMin, rl["max_agent_tool_calls_per_minute"],
		"POSITIVE CONTROL: sandbox.rate_limits.max_agent_tool_calls_per_minute on GET /api/v1/config must equal the saved value; expected %d, seen %v",
		savedToolLimitPerMin, rl["max_agent_tool_calls_per_minute"])
}
