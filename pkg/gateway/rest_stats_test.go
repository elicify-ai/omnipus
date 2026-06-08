//go:build !cgo

// BDD: token stats REST API tests.
// Traces to: FR-013 (token usage stats, period validation, method enforcement).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleTokenStats_EmptyReturns200 verifies GET /api/v1/stats/tokens returns 200
// with zeroed totals when no sessions exist.
// BDD: Given no sessions with token data,
// When GET /api/v1/stats/tokens?period=month is called,
// Then 200 with agents=[], total_tokens_in=0, total_tokens_out=0, total_tokens=0.
// Traces to: FR-013
func TestHandleTokenStats_EmptyReturns200(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=month", nil)
	r.URL.RawQuery = "period=month"
	api.HandleTokenStats(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /stats/tokens?period=month must return 200; body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// agents must be a non-null array (may be empty).
	agents, hasAgents := resp["agents"]
	assert.True(t, hasAgents, "response must contain 'agents' field")
	agentsSlice, ok := agents.([]any)
	assert.True(t, ok, "agents must be a JSON array")
	assert.Empty(t, agentsSlice, "agents must be empty when no sessions exist")

	// period_start and period_end must be present.
	_, hasPeriodStart := resp["period_start"]
	assert.True(t, hasPeriodStart, "response must contain 'period_start'")
	_, hasPeriodEnd := resp["period_end"]
	assert.True(t, hasPeriodEnd, "response must contain 'period_end'")
}

// TestHandleTokenStats_PeriodValidation verifies period query parameter validation.
// BDD: Given GET /api/v1/stats/tokens?period=week,
// When the request is handled,
// Then 400.
// Given GET /api/v1/stats/tokens (no period),
// When the request is handled,
// Then 200 (defaults to month).
// Traces to: FR-013
func TestHandleTokenStats_PeriodValidation(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// period=week → 400.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=week", nil)
	rBad.URL.RawQuery = "period=week"
	api.HandleTokenStats(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code,
		"GET /stats/tokens?period=week must return 400; body=%s", wBad.Body.String())

	// no period → 200 (defaults to month).
	wDefault := httptest.NewRecorder()
	rDefault := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	api.HandleTokenStats(wDefault, rDefault)
	assert.Equal(t, http.StatusOK, wDefault.Code,
		"GET /stats/tokens without period must return 200 (defaults to month); body=%s", wDefault.Body.String())

	// period=day → 400 (only "month" is supported per implementation).
	wDay := httptest.NewRecorder()
	rDay := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=day", nil)
	rDay.URL.RawQuery = "period=day"
	api.HandleTokenStats(wDay, rDay)
	assert.Equal(t, http.StatusBadRequest, wDay.Code,
		"GET /stats/tokens?period=day must return 400")
}

// TestHandleTokenStats_MethodNotAllowed verifies POST /api/v1/stats/tokens returns 405.
// BDD: Given POST /api/v1/stats/tokens,
// When the request is handled,
// Then 405.
// Traces to: FR-013
func TestHandleTokenStats_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/stats/tokens", nil)
	api.HandleTokenStats(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "POST /stats/tokens must return 405")
}
