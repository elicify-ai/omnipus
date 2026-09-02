// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

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
)

// TestGetPerformance_ZeroConfig_SchemaValid is a regression test for the UAT
// finding that GET /api/v1/performance returned max_parallel_agents:0 on a
// fresh install (unconfigured), violating the PerformanceSettings contract's
// `minimum: 1` and tripping the SPA's zod edge-validation. The handler must
// surface the resolved effective value when the on-disk value is the
// "unset" zero.
//
// RE-DERIVED for FR-069. The previous assertions were `>= 2` on both integers
// (and the comment claimed a contract minimum of 2, which was already stale —
// PerformanceSettings.yaml has always said `minimum: 1`). Those assertions
// would still pass today against the physical backstop, WHILE the field's
// meaning changed underneath them from "the memory-sized default" to "an
// OS-thread bound that is not a capacity claim". A test that cannot tell those
// apart is not testing this handler.
//
// The load-bearing new assertion is max_parallel_agents_configured=false: it is
// the only thing on the wire distinguishing an unconfigured host from one where
// an operator explicitly typed the same number, and the SPA renders a
// completely different panel on it.
func TestGetPerformance_ZeroConfig_SchemaValid(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/performance: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp gen.PerformanceSettings
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	// Contract: both integers carry `minimum: 1`, so the internal 0 sentinel
	// must never reach the wire.
	if resp.MaxParallelAgents == nil || *resp.MaxParallelAgents < 1 {
		t.Fatalf("max_parallel_agents must be >= 1 (contract minimum) even when unconfigured; got %v", resp.MaxParallelAgents)
	}
	if resp.EffectiveMaxParallelAgents == nil || *resp.EffectiveMaxParallelAgents < 1 {
		t.Fatalf("effective_max_parallel_agents must be >= 1; got %v", resp.EffectiveMaxParallelAgents)
	}

	// FR-069: nothing is configured on this fresh-install fixture, so the flag
	// must say so. Without it the SPA cannot tell the backstop from a real
	// setting, and renders 2000 to the operator as a recommendation.
	if resp.MaxParallelAgentsConfigured == nil {
		t.Fatal("max_parallel_agents_configured is absent from the response — it is required in responses; the SPA cannot distinguish an unconfigured host from a configured one without it")
	}
	if *resp.MaxParallelAgentsConfigured {
		t.Fatalf("max_parallel_agents_configured = true on a fresh install with nothing configured; want false (effective_max_parallel_agents = %v is the physical OS-thread backstop, not a capacity the system is recommending)", *resp.EffectiveMaxParallelAgents)
	}
}

// TestGetPerformance_ExplicitConfig_ReportsConfigured is the other direction,
// and it is what stops the assertion above from being satisfied by a handler
// that hardcodes false. An explicitly configured value must report
// configured=true and echo the operator's own number.
func TestGetPerformance_ExplicitConfig_ReportsConfigured(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cfgJSON := []byte(`{"version":1,"agents":{"defaults":{}},"providers":[],"performance":{"max_parallel_agents":7}}`)
	require.NoError(t, os.WriteFile(api.homePath+"/config.json", cfgJSON, 0o600))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/performance: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp gen.PerformanceSettings
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.MaxParallelAgentsConfigured == nil || !*resp.MaxParallelAgentsConfigured {
		t.Fatalf("max_parallel_agents_configured = %v on a host with performance.max_parallel_agents=7; want true", resp.MaxParallelAgentsConfigured)
	}
	if resp.EffectiveMaxParallelAgents == nil || *resp.EffectiveMaxParallelAgents != 7 {
		t.Fatalf("effective_max_parallel_agents = %v, want 7 (the operator's own value, honored as configured)", resp.EffectiveMaxParallelAgents)
	}
	if resp.MaxParallelAgents == nil || *resp.MaxParallelAgents != 7 {
		t.Fatalf("max_parallel_agents = %v, want 7", resp.MaxParallelAgents)
	}
}

// TestGetPerformance_ToolsOnDemand_AlwaysPresent verifies that GET /api/v1/performance
// always includes the tools_on_demand field and that it reflects
// cfg.Tools.Manifest.Compressed correctly. The test harness writes a config with
// tools.manifest.compressed=true so the assertion can exercise the true branch.
func TestGetPerformance_ToolsOnDemand_AlwaysPresent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// Overwrite the on-disk config with compressed=true so the in-memory config
	// is reloaded with that value before the GET.
	cfgJSON := []byte(`{"version":1,"agents":{"defaults":{}},"providers":[],"tools":{"manifest":{"compressed":true}}}`)
	require.NoError(t, os.WriteFile(api.homePath+"/config.json", cfgJSON, 0o600))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET must return 200; body=%s", w.Body.String())
	var resp gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.ToolsOnDemand, "tools_on_demand must always be present in GET response")
	assert.True(t, *resp.ToolsOnDemand,
		"tools_on_demand must be true when cfg.Tools.Manifest.Compressed=true")
}

// TestPutPerformance_ToolsOnDemand_False_ThenGet verifies that:
//  1. PUT tools_on_demand=false persists and is reflected in the response.
//  2. A subsequent GET returns tools_on_demand=false.
//  3. cfg.Tools.Manifest.Compressed is false after the write.
func TestPutPerformance_ToolsOnDemand_False_ThenGet(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// PUT: disable tools-on-demand (set Compressed=false).
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"tools_on_demand":false}`))
	putReq.Header.Set("Content-Type", "application/json")
	putReq = withReAuthAdmin(t, api, putReq)
	putW := httptest.NewRecorder()
	api.HandlePerformance(putW, putReq)
	require.Equal(t, http.StatusOK, putW.Code, "PUT must return 200; body=%s", putW.Body.String())

	var putResp gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(putW.Body.Bytes(), &putResp))
	require.NotNil(t, putResp.ToolsOnDemand, "tools_on_demand must be in PUT response")
	assert.False(t, *putResp.ToolsOnDemand, "PUT response must reflect the written value (false)")

	// GET: confirm the persisted value is returned.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	getW := httptest.NewRecorder()
	api.HandlePerformance(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code, "GET must return 200; body=%s", getW.Body.String())

	var getResp gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	require.NotNil(t, getResp.ToolsOnDemand, "tools_on_demand must be in GET response")
	assert.False(t, *getResp.ToolsOnDemand,
		"GET must return tools_on_demand=false after PUT persisted false")

	// Verify in-memory config is updated.
	liveCfg := api.agentLoop.GetConfig()
	assert.False(t, liveCfg.Tools.Manifest.Compressed,
		"cfg.Tools.Manifest.Compressed must be false after PUT")
}

// TestPutPerformance_PartialUpdate_ToolsOnDemandOnly_LeavesParallelAgentsUnchanged
// verifies that a PUT with only tools_on_demand does not clobber max_parallel_agents.
func TestPutPerformance_PartialUpdate_ToolsOnDemandOnly_LeavesParallelAgentsUnchanged(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First set max_parallel_agents to a known value.
	req1 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":6}`))
	req1.Header.Set("Content-Type", "application/json")
	req1 = withReAuthAdmin(t, api, req1)
	w1 := httptest.NewRecorder()
	api.HandlePerformance(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code, "first PUT must return 200; body=%s", w1.Body.String())

	// Then update only tools_on_demand without providing max_parallel_agents.
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"tools_on_demand":false}`))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withReAuthAdmin(t, api, req2)
	w2 := httptest.NewRecorder()
	api.HandlePerformance(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code, "second PUT must return 200; body=%s", w2.Body.String())

	var resp2 gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.NotNil(t, resp2.ToolsOnDemand, "tools_on_demand must be in PUT response")
	assert.False(t, *resp2.ToolsOnDemand, "tools_on_demand must be false after second PUT")

	// max_parallel_agents must still be 6 (not clobbered).
	liveCfg := api.agentLoop.GetConfig()
	assert.Equal(t, 6, liveCfg.Performance.MaxParallelAgents,
		"max_parallel_agents must be unchanged after a tools_on_demand-only PUT")
}

// TestPutPerformance_PartialUpdate_MaxParallelAgentsOnly_LeavesToolsOnDemandUnchanged
// verifies that a PUT with only max_parallel_agents does not clobber tools_on_demand.
func TestPutPerformance_PartialUpdate_MaxParallelAgentsOnly_LeavesToolsOnDemandUnchanged(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First set tools_on_demand=false.
	req1 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"tools_on_demand":false}`))
	req1.Header.Set("Content-Type", "application/json")
	req1 = withReAuthAdmin(t, api, req1)
	w1 := httptest.NewRecorder()
	api.HandlePerformance(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code, "first PUT must return 200; body=%s", w1.Body.String())

	// Then update only max_parallel_agents without providing tools_on_demand.
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":8}`))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withReAuthAdmin(t, api, req2)
	w2 := httptest.NewRecorder()
	api.HandlePerformance(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code, "second PUT must return 200; body=%s", w2.Body.String())

	var resp2 gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.NotNil(t, resp2.MaxParallelAgents, "max_parallel_agents must be in PUT response")
	assert.EqualValues(t, 8, *resp2.MaxParallelAgents, "max_parallel_agents must be 8 after second PUT")

	// tools_on_demand must still be false (not clobbered back to default).
	liveCfg := api.agentLoop.GetConfig()
	assert.False(t, liveCfg.Tools.Manifest.Compressed,
		"cfg.Tools.Manifest.Compressed must remain false after a max_parallel_agents-only PUT")
}
