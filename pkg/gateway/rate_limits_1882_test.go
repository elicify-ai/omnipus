// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED repro for GitHub issue #1882 — "Settings 'Per-Agent Rate Limits' fields
// save to a key the server does not enforce".
//
// Oracle (from the issue's Expected, never from the implementation):
//
//	A per-agent limit saved the way the Settings screen saves it is the limit
//	the server enforces, and reads back the same after a reload.
//
// The save the Settings screen makes:
//   - src/components/settings/SecuritySection.tsx:385-395 — the section calls
//     updateConfig({security: {..., rate_limits: {...config?.security.rate_limits,
//     max_agent_llm_calls_per_hour, max_agent_tool_calls_per_minute}}}).
//   - src/lib/api/config.ts:303-314 (frontendToRawConfig) — the wire body keeps
//     exactly security.rate_limits (:310-313); exec_timeout_seconds and
//     max_background_seconds are only sent when defined (:308-309). They
//     hydrate from config.security.exec_timeout_seconds/:max_background_seconds
//     (SecuritySection.tsx:370-371), which the server never serves (the
//     security config struct has no such fields), so the faithful wire body is
//     security.rate_limits alone.
//
// The read-back the issue requires is where the SPA re-hydrates the fields:
// src/components/settings/SecuritySection.tsx:368-369 —
// config.security.rate_limits.max_agent_llm_calls_per_hour / .max_agent_tool_calls_per_minute.
//
// The enforced limit the issue names is the one beginIteration applies:
// pkg/agent/loop_run_turn.go reads cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour
// (served on the wire by GET /api/v1/security/rate-limits,
// pkg/gateway/rest_rate_limits.go::getRateLimits).
//
// Test-side reconciliation note: these tests were written RED against release
// 7bed3c14c2. The positive control (Test1882_PositiveControl_...) is required
// to be green; the repro test (Test1882_SettingsSavedPerAgentLimit_...) is
// expected RED. Once the #1882 fix lands, the repro test's assertions become
// the fixed-behaviour contract and must go green unchanged.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// savedLLMLimitPerHour / savedToolLimitPerMinute are the values the operator
// types into Settings in this repro. They are ordinary distinctive values
// chosen on the spec side (the issue says "set a per-agent limit ... the limit
// is enforced"); they come from nowhere in the implementation, so any
// enforcement of a different value fails these assertions visibly.
const (
	savedLLMLimitPerHour  = 42
	savedToolLimitPerMin  = 7
	settings1882AgentID   = "settings-1882-agent"
	rateLimits1882PutBody = `{"security":{"rate_limits":{"max_agent_llm_calls_per_hour":42,"max_agent_tool_calls_per_minute":7}}}`
)

// new1882TestAPI builds a restAPI over a real temp-dir config.json the same
// way newMTIAPI does: minimal on-disk config, one custom agent in the entity
// store (so the strict roster population has a record), a real AgentLoop, and
// one successful production refresh BEFORE the repro asserts anything — the
// reload machinery itself is proven good, so a later failure belongs to the
// repro, not to the harness.
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

// Test1882_SettingsSavedPerAgentLimit_ReadBackAndEnforced is the R1 repro:
// save a per-agent limit the way Settings saves it (PUT /api/v1/config with
// security.rate_limits.*), reload, then require BOTH halves of the issue's
// Expected:
//
//	(a) the values read back where the SPA reads them
//	    (GET /api/v1/config → security.rate_limits.*, SecuritySection.tsx:368-369);
//	(b) the enforced limit equals the saved value — the cfg.Sandbox.RateLimits
//	    value beginIteration reads (read on the wire via
//	    GET /api/v1/security/rate-limits), after a reload.
//
// Each half reports its own failure naming #1882 with expected vs seen, so the
// log shows honestly which half fails.
func Test1882_SettingsSavedPerAgentLimit_ReadBackAndEnforced(t *testing.T) {
	api := new1882TestAPI(t)

	// — Act: save exactly the way the Settings screen saves ————————————————
	putW := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/config", strings.NewReader(rateLimits1882PutBody))
	putReq.Header.Set("Content-Type", "application/json")
	api.HandleConfig(putW, putReq)
	putStatus := putW.Code
	putBody := putW.Body.String()

	// Reload (the hot-reload poll / SPA refetch path) before judging.
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()),
		"SETUP: the post-save reload must not fail the harness — a reload failure here is an instrument problem, not the #1882 repro")

	// — Assert (a): read-back where the SPA reads (SecuritySection.tsx:368-369)
	getW := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	api.HandleConfig(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code,
		"SETUP: GET /api/v1/config must answer 200 — without it neither half of the repro can be judged: %s", getW.Body.String())
	var wire map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &wire),
		"SETUP: GET /api/v1/config must return a JSON object")

	sec, _ := wire["security"].(map[string]any)
	rl, _ := sec["rate_limits"].(map[string]any)
	llmSeen, llmFound := rl["max_agent_llm_calls_per_hour"]
	toolSeen, toolFound := rl["max_agent_tool_calls_per_minute"]
	assert.True(t, llmFound,
		"#1882 (read-back half): expected config.security.rate_limits.max_agent_llm_calls_per_hour = %d where the SPA re-hydrates the field "+
			"(SecuritySection.tsx:368); the key is ABSENT from GET /api/v1/config — the Settings save never reads back", savedLLMLimitPerHour)
	assert.True(t, toolFound,
		"#1882 (read-back half): expected config.security.rate_limits.max_agent_tool_calls_per_minute = %d where the SPA re-hydrates the field "+
			"(SecuritySection.tsx:369); the key is ABSENT from GET /api/v1/config — the Settings save never reads back", savedToolLimitPerMin)
	if llmFound {
		assert.EqualValues(t, savedLLMLimitPerHour, llmSeen,
			"#1882 (read-back half): expected %d, seen %v", savedLLMLimitPerHour, llmSeen)
	}
	if toolFound {
		assert.EqualValues(t, savedToolLimitPerMin, toolSeen,
			"#1882 (read-back half): expected %d, seen %v", savedToolLimitPerMin, toolSeen)
	}

	// — Assert (b): the enforced limit equals the saved value ——————————————
	// beginIteration (pkg/agent/loop_run_turn.go) applies
	// cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour / MaxAgentToolCallsPerMinute.
	enforced := api.agentLoop.GetConfig().Sandbox.RateLimits
	assert.Equal(t, savedLLMLimitPerHour, enforced.MaxAgentLLMCallsPerHour,
		"#1882 (enforcement half): the limit the server enforces (cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour, read by beginIteration in "+
			"pkg/agent/loop_run_turn.go) must equal the value saved the way Settings saves it; expected %d, seen %d",
		savedLLMLimitPerHour, enforced.MaxAgentLLMCallsPerHour)
	assert.Equal(t, savedToolLimitPerMin, enforced.MaxAgentToolCallsPerMinute,
		"#1882 (enforcement half): the limit the server enforces (cfg.Sandbox.RateLimits.MaxAgentToolCallsPerMinute, read by beginIteration in "+
			"pkg/agent/loop_run_turn.go) must equal the value saved the way Settings saves it; expected %d, seen %d",
		savedToolLimitPerMin, enforced.MaxAgentToolCallsPerMinute)

	// The same enforced value on the wire (GET /api/v1/security/rate-limits).
	rlW := httptest.NewRecorder()
	rlReq := httptest.NewRequest(http.MethodGet, "/api/v1/security/rate-limits", nil)
	api.HandleRateLimits(rlW, rlReq)
	require.Equal(t, http.StatusOK, rlW.Code,
		"SETUP: GET /api/v1/security/rate-limits must answer 200: %s", rlW.Body.String())
	var rlResp map[string]any
	require.NoError(t, json.Unmarshal(rlW.Body.Bytes(), &rlResp))
	assert.EqualValues(t, savedLLMLimitPerHour, rlResp["max_agent_llm_calls_per_hour"],
		"#1882 (enforcement half): GET /api/v1/security/rate-limits (the wire form of the limit the server enforces) must report the Settings-saved value; "+
			"expected %d, seen %v", savedLLMLimitPerHour, rlResp["max_agent_llm_calls_per_hour"])
	assert.EqualValues(t, savedToolLimitPerMin, rlResp["max_agent_tool_calls_per_minute"],
		"#1882 (enforcement half): GET /api/v1/security/rate-limits (the wire form of the limit the server enforces) must report the Settings-saved value; "+
			"expected %d, seen %v", savedToolLimitPerMin, rlResp["max_agent_tool_calls_per_minute"])

	// — The save half itself: the save must succeed for either half above —
	// to be reachable at all.
	assert.Equal(t, http.StatusOK, putStatus,
		"#1882 (save half): PUT /api/v1/config with exactly the body the Settings screen sends "+
			"(SecuritySection.tsx:385-395 → src/lib/api/config.ts:303-314) must succeed for the limit to exist anywhere; "+
			"seen HTTP %d: %s", putStatus, putBody)
}

// Test1882_PositiveControl_DedicatedEndpointSetsTheEnforcedLimit is the R3
// positive control: the SAME enforcement reads that fail in the repro above
// must PASS when the limit is set via PUT /api/v1/security/rate-limits — the
// endpoint the issue names as the one that writes the enforced location
// (pkg/gateway/rest_rate_limits.go::putRateLimits). Green here proves the
// repro's enforcement reads are wired correctly, so the repro's failure is
// attributable to the Settings save path, not to the instrument.
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
