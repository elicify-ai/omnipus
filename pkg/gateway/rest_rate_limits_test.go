//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleRateLimits_* covers the PUT rate-limits semantics for the
// surviving SEC-26 sliding-window fields.
//
// ADR-053 D12 retired the SEC-26 daily USD cost cap (`daily_cost_cap_usd`)
// from this endpoint. The endpoint now handles ONLY per-agent sliding-window
// limits (LLM/hr, tool/min); the app-level spend brake is the token budget
// (PUT /api/v1/settings/token-budget). Retired-field rejection is tested in
// TestHandleRateLimits_RejectsRetiredUSDField in rest_security_wave4_test.go.

func putRateLimits(t *testing.T, api *restAPI, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/rate-limits", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleRateLimits(w, r)
	return w
}

// TestHandleRateLimits_PersistsBothFields verifies that both surviving fields
// are written to disk and readable via GET after a successful PUT.
func TestHandleRateLimits_PersistsBothFields(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := `{"max_agent_llm_calls_per_hour":100,"max_agent_tool_calls_per_minute":30}`
	w := putRateLimits(t, api, body)
	require.Equal(t, http.StatusOK, w.Code, "PUT must succeed: %s", w.Body)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["saved"])
	assert.Equal(t, false, resp["requires_restart"])

	applied, ok := resp["applied"].(map[string]any)
	require.True(t, ok, "applied must be an object")
	assert.Equal(t, float64(100), applied["max_agent_llm_calls_per_hour"])
	assert.Equal(t, float64(30), applied["max_agent_tool_calls_per_minute"])
	// ADR-053 D12: USD cap retired; must NOT be in applied.
	_, hasUSD := applied["daily_cost_cap_usd"]
	assert.False(t, hasUSD,
		"daily_cost_cap_usd must NOT appear in applied after D12 retirement")

	// Read back via GET to confirm persistence.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/security/rate-limits", nil)
	getW := httptest.NewRecorder()
	api.HandleRateLimits(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	assert.Equal(t, float64(100), getResp["max_agent_llm_calls_per_hour"])
	assert.Equal(t, float64(30), getResp["max_agent_tool_calls_per_minute"])
}

// TestHandleRateLimits_PartialUpdate verifies that a PUT with only one field
// leaves the other pre-seeded field unchanged.
func TestHandleRateLimits_PartialUpdate(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Pre-seed: set LLM cap to 50.
	preseed := `{"max_agent_llm_calls_per_hour":50}`
	w1 := putRateLimits(t, api, preseed)
	require.Equal(t, http.StatusOK, w1.Code, "preseed PUT must succeed: %s", w1.Body)

	// Partial update: only change tool cap.
	w2 := putRateLimits(t, api, `{"max_agent_tool_calls_per_minute":75}`)
	require.Equal(t, http.StatusOK, w2.Code, "partial PUT must succeed: %s", w2.Body)

	// Confirm LLM cap is preserved.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/security/rate-limits", nil)
	getW := httptest.NewRecorder()
	api.HandleRateLimits(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	assert.Equal(t, float64(75), getResp["max_agent_tool_calls_per_minute"], "tool cap must be updated")
	assert.Equal(t, float64(50), getResp["max_agent_llm_calls_per_hour"], "llm_calls cap must be preserved")
}

// TestHandleRateLimits_EmptyBodyNoOp verifies that {} returns 200 without
// changing any persisted values.
func TestHandleRateLimits_EmptyBodyNoOp(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Pre-seed a value.
	preseed := `{"max_agent_tool_calls_per_minute":20}`
	require.Equal(t, http.StatusOK, putRateLimits(t, api, preseed).Code)

	// Empty body no-op.
	w := putRateLimits(t, api, `{}`)
	require.Equal(t, http.StatusOK, w.Code, "empty body must succeed: %s", w.Body)

	// Confirm value unchanged.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/security/rate-limits", nil)
	getW := httptest.NewRecorder()
	api.HandleRateLimits(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &getResp))
	assert.Equal(t, float64(20), getResp["max_agent_tool_calls_per_minute"], "tool cap must be unchanged after no-op")
}

// TestHandleRateLimits_NegativeRejected verifies that negative values are
// rejected with 400.
func TestHandleRateLimits_NegativeRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := putRateLimits(t, api, `{"max_agent_llm_calls_per_hour":-5}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "negative llm cap must return 400: %s", w.Body)
}

// TestHandleRateLimits_StringInIntFieldRejected verifies that a JSON string
// value in an integer field is rejected with 400.
func TestHandleRateLimits_StringInIntFieldRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := putRateLimits(t, api, `{"max_agent_llm_calls_per_hour":"50"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, `"50" in int field must return 400: %s`, w.Body)
}

// TestHandleRateLimits_FloatInIntFieldRejected verifies that a fractional float
// value in an integer field is rejected with 400.
func TestHandleRateLimits_FloatInIntFieldRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := putRateLimits(t, api, `{"max_agent_llm_calls_per_hour":10.5}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "10.5 in int field must return 400: %s", w.Body)
}

// TestHandleRateLimits_NullRejected verifies that JSON null in an integer field
// is rejected with 400.
func TestHandleRateLimits_NullRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := putRateLimits(t, api, `{"max_agent_tool_calls_per_minute":null}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "null in int field must return 400: %s", w.Body)
}

// TestHandleRateLimits_MaxInt64Accepted verifies that math.MaxInt64 is accepted.
func TestHandleRateLimits_MaxInt64Accepted(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := putRateLimits(t, api, `{"max_agent_llm_calls_per_hour":9223372036854775807}`)
	assert.Equal(t, http.StatusOK, w.Code, "MaxInt64 must be accepted: %s", w.Body)
}

// TestHandleRateLimits_OverflowRejected verifies that a value exceeding
// math.MaxInt64 is rejected with 400.
func TestHandleRateLimits_OverflowRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// 9223372036854775808 = MaxInt64 + 1.
	w := putRateLimits(t, api, `{"max_agent_llm_calls_per_hour":9223372036854775808}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "overflow must return 400: %s", w.Body)
}

// TestHandleRateLimits_HotReload verifies that the response includes
// requires_restart: false (rate limits are hot-reloaded via the 2s config poll).
func TestHandleRateLimits_HotReload(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	w := putRateLimits(t, api, `{"max_agent_llm_calls_per_hour":5}`)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["requires_restart"], "rate limits must not require restart")
}

// TestHandleRateLimits_MethodNotAllowed verifies that DELETE returns 405.
func TestHandleRateLimits_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/security/rate-limits", nil)
	api.HandleRateLimits(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestHandleRateLimits_EmitsAuditEntry verifies that a successful PUT to
// /api/v1/security/rate-limits emits a security_setting_change JSONL record
// with resource=="sandbox.rate_limits" and the correct actor field.
//
// Pattern mirrors TestHandleSandboxAuditLog_EmitsAuditEntry in
// rest_audit_log_test.go. The audit logger is wired by newTestRestAPIWithAuditLog
// (Sandbox.AuditLog=true). The JSONL is written to tmpDir/system/.
func TestHandleRateLimits_EmitsAuditEntry(t *testing.T) {
	api, tmpDir := newTestRestAPIWithAuditLog(t)

	body := strings.NewReader(`{"max_agent_llm_calls_per_hour":25}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/rate-limits", body)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleRateLimits(w, r)

	require.Equal(t, http.StatusOK, w.Code, "PUT must succeed: %s", w.Body)

	systemDir := filepath.Join(tmpDir, "system")
	entries, err := os.ReadDir(systemDir)
	require.NoError(t, err, "system dir must exist after audit emit")
	require.NotEmpty(t, entries, "at least one audit file must exist")

	var found bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(systemDir, entry.Name()))
		require.NoError(t, err)
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				continue
			}
			if record["event"] != "security_setting_change" {
				continue
			}
			if record["resource"] != "sandbox.rate_limits" {
				continue
			}
			assert.Equal(t, "sandbox.rate_limits", record["resource"], "resource must match")
			assert.Equal(t, "admin", record["actor"], "actor must be the admin username")
			// Verify the new_value reflects the change — catches a no-op audit emit.
			newVal, _ := record["new_value"].(map[string]any)
			assert.NotNil(t, newVal, "new_value must be an object")
			assert.Equal(t, float64(25), newVal["max_agent_llm_calls_per_hour"],
				"new_value.max_agent_llm_calls_per_hour must match the PUT body")
			// ADR-053 D12: USD cap retired; must NOT appear in the audit record.
			_, hasUSD := newVal["daily_cost_cap_usd"]
			assert.False(t, hasUSD,
				"audit new_value must not carry the retired daily_cost_cap_usd field")
			found = true
		}
		require.NoError(t, scanner.Err())
	}

	assert.True(t, found, "security_setting_change record with resource=sandbox.rate_limits must appear in audit JSONL")
}
