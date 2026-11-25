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

// RETIRED with the "Load tools on demand" switch (skills-tools-redesign-spec.md,
// C-22 row 2: `tools_on_demand` leaves PerformanceSettings and
// PerformanceSettingsUpdate, and `rest_performance.go` "stops reading, writing and
// reporting it"; C-22 row 4: `TestSettingsAgentWriteParity` loses the same row;
// ST-Q4 B). Three cases here tested only that removed surface, so they are deleted
// rather than rewritten — each names where its invariant now lives:
//
//   - TestGetPerformance_ToolsOnDemand_AlwaysPresent (GET always carried the field
//     and mirrored cfg.Tools.Manifest.Compressed): the spec now says the opposite,
//     pinned by TestGetPerformance_DoesNotReportToolsOnDemand in
//     performance_tools_on_demand_red_test.go.
//   - TestPutPerformance_ToolsOnDemand_False_ThenGet (PUT wrote the field and the
//     GET echoed it): a request still carrying it now gets the schema's 400, pinned
//     by TestPerformancePut_RejectsToolsOnDemand in the same file.
//   - TestPutPerformance_PartialUpdate_ToolsOnDemandOnly_LeavesParallelAgentsUnchanged
//     (a tools_on_demand-only PUT left max_parallel_agents alone): such a PUT is now
//     a 400. The invariant it protected — a PUT of one field leaves
//     max_parallel_agents alone — is still pinned below by
//     TestPutPerformance_PartialUpdate_GoalMaxRoundsOnly_LeavesOtherFieldsUnchanged.

// TestGetPerformance_GoalMaxRounds_DefaultTwenty is GOAL-FR-024/FR-045's
// backend-half regression: a fresh install with nothing configured must
// still surface goal_max_rounds (schema: "Always present in responses"),
// resolved to config.DefaultGoalMaxRounds (20) via
// cfg.Planning.EffectiveGoalMaxRounds() — never a bare zero.
func TestGetPerformance_GoalMaxRounds_DefaultTwenty(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET must return 200; body=%s", w.Body.String())

	var resp gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.GoalMaxRounds, "goal_max_rounds must always be present in the response (GOAL-FR-045)")
	assert.EqualValues(t, 20, *resp.GoalMaxRounds,
		"goal_max_rounds must default to 20 (GOAL-FR-024) on a fresh install with nothing configured")
}

// TestPutPerformance_GoalMaxRounds_PersistsThenGet verifies the write-then-
// read round trip end to end through the real REST handler: PUT a new
// goal_max_rounds, confirm the PUT response reflects it, then confirm a
// SEPARATE GET request reflects the persisted value — proving it actually
// reached pkg/config's planning.goal_max_rounds on disk rather than being
// echoed back from the request body alone.
func TestPutPerformance_GoalMaxRounds_PersistsThenGet(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"goal_max_rounds":35}`))
	putReq.Header.Set("Content-Type", "application/json")
	putReq = withReAuthAdmin(t, api, putReq)
	putW := httptest.NewRecorder()
	api.HandlePerformance(putW, putReq)
	require.Equal(t, http.StatusOK, putW.Code, "PUT must return 200; body=%s", putW.Body.String())

	var putResp gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(putW.Body.Bytes(), &putResp))
	require.NotNil(t, putResp.GoalMaxRounds, "goal_max_rounds must be in the PUT response")
	assert.EqualValues(t, 35, *putResp.GoalMaxRounds, "PUT response must reflect the written value (35)")

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	getW := httptest.NewRecorder()
	api.HandlePerformance(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code, "GET must return 200; body=%s", getW.Body.String())

	var getResp gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	require.NotNil(t, getResp.GoalMaxRounds, "goal_max_rounds must be in the GET response")
	assert.EqualValues(t, 35, *getResp.GoalMaxRounds,
		"GET after PUT must reflect the persisted value (35), not the pre-PUT default of 20")

	// The single global value must also drive EffectiveGoalMaxRounds() for
	// any in-memory caller (GOAL-FR-024/D-D: one budget, governs task and
	// chat goals identically) — not just the wire echo.
	liveCfg := api.agentLoop.GetConfig()
	assert.Equal(t, 35, liveCfg.Planning.EffectiveGoalMaxRounds(),
		"cfg.Planning.EffectiveGoalMaxRounds() must be 35 after PUT")
}

// TestPutPerformance_PartialUpdate_GoalMaxRoundsOnly_LeavesOtherFieldsUnchanged
// pins that a PUT touching only goal_max_rounds does not clobber
// max_parallel_agents. (Its first PUT used to set tools_on_demand as well and its
// last assertion pinned that switch; both left with the switch — C-22 row 2. The
// "a Limits PUT never touches tools.manifest.compressed" invariant lives in
// TestPutPerformance_PartialUpdate_MaxParallelAgentsOnly_LeavesStoredManifestKeyInPlace.)
func TestPutPerformance_PartialUpdate_GoalMaxRoundsOnly_LeavesOtherFieldsUnchanged(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	req1 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":6}`))
	req1.Header.Set("Content-Type", "application/json")
	req1 = withReAuthAdmin(t, api, req1)
	w1 := httptest.NewRecorder()
	api.HandlePerformance(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code, "first PUT must return 200; body=%s", w1.Body.String())

	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"goal_max_rounds":12}`))
	req2.Header.Set("Content-Type", "application/json")
	req2 = withReAuthAdmin(t, api, req2)
	w2 := httptest.NewRecorder()
	api.HandlePerformance(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code, "second PUT must return 200; body=%s", w2.Body.String())

	var resp2 gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	require.NotNil(t, resp2.GoalMaxRounds, "goal_max_rounds must be in the PUT response")
	assert.EqualValues(t, 12, *resp2.GoalMaxRounds, "goal_max_rounds must be 12 after the goal_max_rounds-only PUT")

	liveCfg := api.agentLoop.GetConfig()
	assert.Equal(t, 6, liveCfg.Performance.MaxParallelAgents,
		"max_parallel_agents must be unchanged after a goal_max_rounds-only PUT")
}

// TestPutPerformance_PartialUpdate_MaxParallelAgentsOnly_LeavesStoredManifestKeyInPlace
// replaces ..._LeavesToolsOnDemandUnchanged. The switch left the Performance
// contract (C-22 row 2), but the stored `tools.manifest.compressed` key does not
// leave the file: "stored values stay in place, ignored" (spec C-6 / D7, the
// tool-loading conversion). So the invariant the old test protected survives in a
// new form — saving a Limits value must never write that key, in either direction.
//
// The seed is the NON-default value: the shipped default is compressed=true
// (pkg/config/defaults.go), so a handler that rewrote the key to its default, or
// dropped it from config.json, is visible here. The key is read from the file, not
// from the in-memory struct, because the spec does not say when the Go field goes.
// This test passes on the pre-change code (the old handler also left the key alone
// when the PUT omitted tools_on_demand); it guards the removal.
func TestPutPerformance_PartialUpdate_MaxParallelAgentsOnly_LeavesStoredManifestKeyInPlace(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	cfgJSON := []byte(`{"version":1,"agents":{"defaults":{}},"providers":[],"tools":{"manifest":{"compressed":false}}}`)
	require.NoError(t, os.WriteFile(api.configPath(), cfgJSON, 0o600))
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":8}`))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, req)
	require.Equal(t, http.StatusOK, w.Code, "PUT must return 200; body=%s", w.Body.String())

	var resp gen.PerformanceSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.MaxParallelAgents, "max_parallel_agents must be in PUT response")
	assert.EqualValues(t, 8, *resp.MaxParallelAgents, "max_parallel_agents must be 8 after the PUT")

	data, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var saved map[string]any
	require.NoError(t, json.Unmarshal(data, &saved))
	tools, _ := saved["tools"].(map[string]any)
	manifest, _ := tools["manifest"].(map[string]any)
	assert.Equal(t, false, manifest["compressed"],
		"tools.manifest.compressed must stay in config.json, untouched, after a Limits PUT (C-6: stored values stay in place); saved tools section: %v", saved["tools"])
}
