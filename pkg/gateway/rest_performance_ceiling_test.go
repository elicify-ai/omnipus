// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
)

// TestPerformancePUT_AboveOldCeiling_SurvivesEndToEnd is the REQUIRED
// "no ceiling: an explicit value above 16 survives end to end (config ->
// resolver -> the semaphore that actually gates)" test, exercised through
// the REAL REST handler (concurrency-gate consolidation, 2026-08-04). Prior
// to this change, PUT /api/v1/performance silently clamped any value above
// 16 down to 16 via clampParallelExplicit — this proves that ceiling is
// gone at every layer this request passes through: request validation
// (contracts/components/schemas/PerformanceSettingsUpdate.yaml, no more
// maximum:16), config persistence, config.PerformanceConfig.
// EffectiveMaxParallelAgents(), and the live TaskExecutor.dispatchSema
// capacity the PUT handler explicitly resizes.
func TestPerformancePUT_AboveOldCeiling_SurvivesEndToEnd(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	const wantCap = 24 // comfortably above the retired ceiling of 16

	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":24}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT with max_parallel_agents=24 must succeed; body=%s", w.Body.String())

	var putResp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	assert.EqualValues(t, wantCap, putResp["max_parallel_agents"],
		"PUT response must echo the configured value unchanged, not clamped to 16")
	assert.EqualValues(t, wantCap, putResp["effective_max_parallel_agents"],
		"PUT response's effective value must also be unchanged, not clamped to 16")

	// Read back via GET — proves the value was actually PERSISTED (config
	// round-trip), not just echoed back from the request body.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	getReq = withReAuthAdmin(t, api, getReq)
	getW := httptest.NewRecorder()
	api.HandlePerformance(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code, "GET must succeed; body=%s", getW.Body.String())
	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	assert.EqualValues(t, wantCap, getResp["max_parallel_agents"],
		"GET after PUT must reflect the persisted, un-clamped value")
	assert.EqualValues(t, wantCap, getResp["effective_max_parallel_agents"],
		"GET's effective value must also reflect 24, not 16")

	// Finally, prove the LIVE semaphore that actually gates task/subagent
	// dispatch was resized to the same un-clamped value — closing the loop
	// from "config → resolver" (already proven in pkg/config and pkg/agent)
	// through to "the semaphore that actually gates" at the REST layer.
	te := agent.GetTaskExecutor(api.agentLoop)
	require.NotNil(t, te, "TaskExecutor must be wired on the test API's agent loop")
	assert.Equal(t, wantCap, te.DispatchSemaCap(),
		"the live dispatch semaphore capacity must reflect the un-clamped configured value")
}

// TestPerformancePUT_NegativeValue_Rejected is the required "error proof":
// invalid input (a negative max_parallel_agents) must be rejected with 400,
// not silently coerced or accepted.
func TestPerformancePUT_NegativeValue_Rejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":-5}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code,
		"PUT with a negative max_parallel_agents must be 400; body=%s", w.Body.String())
}

// TestPerformancePUT_ZeroResetsToAutoDetectedDefault verifies the documented
// "set to 0 to restore the auto-detected default" contract behavior actually
// works end-to-end — this exercises the OTHER required direction of "an
// explicit value overrides the auto default, both directions": explicit ->
// explicit is covered above; this is explicit -> back to auto.
func TestPerformancePUT_ZeroResetsToAutoDetectedDefault(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// First set an explicit small value.
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":3}`))
	r1.Header.Set("Content-Type", "application/json")
	r1 = withReAuthAdmin(t, api, r1)
	w1 := httptest.NewRecorder()
	api.HandlePerformance(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "initial PUT must succeed; body=%s", w1.Body.String())

	// Now reset to auto (0).
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":0}`))
	r2.Header.Set("Content-Type", "application/json")
	r2 = withReAuthAdmin(t, api, r2)
	w2 := httptest.NewRecorder()
	api.HandlePerformance(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "reset-to-auto PUT (0) must succeed; body=%s", w2.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	effective, ok := resp["effective_max_parallel_agents"].(float64)
	require.True(t, ok, "effective_max_parallel_agents must be present and numeric")
	assert.GreaterOrEqual(t, effective, float64(2), "auto-detected default must respect its floor of 2")
	assert.NotEqual(t, float64(3), effective,
		"after resetting to auto, the effective value must no longer be the previously-explicit 3 (unless auto genuinely also resolves to 3, which is astronomically unlikely on a real test machine)")
}

// TestPerformancePUT_Zero_ResponseBodySchemaValid is the MAJOR-3 regression
// test from the 2026-08-04 code review: a PUT of {"max_parallel_agents":0}
// (the documented "reset to auto-detect" contract) must return a response
// body that satisfies PerformanceSettings.yaml's `max_parallel_agents`
// `minimum: 1` — the same schema the SPA's zod PerformanceSettingsSchema
// (src/lib/api.ts) enforces client-side. Before the fix, putPerformance
// echoed the raw on-disk value (0) straight into the response instead of
// substituting the resolved effective value the way getPerformance already
// did, so this exact save-succeeded-but-body-was-schema-invalid case
// produced a 200 with a payload the SPA then rejected as
// ApiSchemaError, surfacing a false "failed to save" toast on a write that
// had, in fact, correctly persisted.
func TestPerformancePUT_Zero_ResponseBodySchemaValid(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	r := httptest.NewRequest(http.MethodPut, "/api/v1/performance",
		strings.NewReader(`{"max_parallel_agents":0}`))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT max_parallel_agents=0 must return 200; body=%s", w.Body.String())

	raw := w.Body.Bytes()

	// The literal assertion the review named: the wire body must not contain
	// max_parallel_agents:0, which fails the schema's minimum:1.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(raw, &resp))
	configured, ok := resp["max_parallel_agents"].(float64)
	require.True(t, ok, "max_parallel_agents must be present and numeric in the PUT response")
	assert.NotEqual(t, float64(0), configured,
		"PUT response must surface the resolved effective value, not the raw on-disk auto-detect sentinel 0 (violates PerformanceSettings.yaml minimum:1)")
	assert.GreaterOrEqual(t, configured, float64(1), "max_parallel_agents must satisfy the contract minimum of 1")

	// The general proof the review asked for: validate the RAW response body
	// against the actual shipped PerformanceSettings schema (same embedded
	// copy — byte-identical to contracts/components/schemas/
	// PerformanceSettings.yaml — that the SPA's zod schema is generated
	// from), not just the two fields above.
	if errMsg, serverErr := validateBodyAgainstSchema("PerformanceSettings", raw); errMsg != "" {
		t.Fatalf("PUT response body fails PerformanceSettings schema validation (serverErr=%v): %s\nbody=%s", serverErr, errMsg, raw)
	}
}
