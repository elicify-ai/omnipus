//go:build !cgo

// BDD: token stats REST API tests.
// Traces to: FR-013 (token usage stats, period validation, method enforcement).

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

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

// writeTestSessionMeta writes a meta.json for a session into the shared session store directory
// (filepath.Dir(homePath)/sessions/{sessionID}/). This matches where NewAgentLoop initializes
// sharedSessionStore when cfg.Agents.Defaults.Workspace == homePath.
func writeTestSessionMeta(
	t *testing.T,
	homePath, sessionID, agentID string,
	tokensIn, tokensOut int,
	updatedAt time.Time,
) {
	t.Helper()
	// The agent loop sets homePath = filepath.Dir(cfg.WorkspacePath()) = filepath.Dir(homePath).
	// sharedSessionStore is at agentLoopHome/sessions/ = filepath.Dir(homePath)/sessions/.
	agentLoopHome := filepath.Dir(homePath)
	sessDir := filepath.Join(agentLoopHome, "sessions", sessionID)
	require.NoError(t, os.MkdirAll(sessDir, 0o700))
	meta := fmt.Sprintf(
		`{"id":%q,"agent_id":%q,"active_agent_id":%q,"status":"active","channel":"webchat","type":"chat",`+
			`"created_at":%q,"updated_at":%q,"partitions":[],"stats":{"tokens_in":%d,"tokens_out":%d,"tokens_total":%d,"cost":0,"tool_calls":0,"message_count":0}}`,
		sessionID, agentID, agentID,
		updatedAt.UTC().Format(time.RFC3339),
		updatedAt.UTC().Format(time.RFC3339),
		tokensIn, tokensOut, tokensIn+tokensOut,
	)
	require.NoError(t, os.WriteFile(filepath.Join(sessDir, "meta.json"), []byte(meta), 0o600))
}

// TestHandleTokenStats_SingleAgent verifies GET /api/v1/stats/tokens aggregates tokens
// from a single-agent session correctly.
// BDD: Given one session with active_agent_id="agent-alpha", tokens_in=100, tokens_out=50,
// updated_at within the current month,
// When GET /api/v1/stats/tokens?period=month is called,
// Then 200 with agents array of length 1, agent_id="agent-alpha", tokens_in=100, tokens_out=50.
// Traces to: project-task-management-level1-spec.md FG-H5 (token attribution positive-data test)
func TestHandleTokenStats_SingleAgent(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Write one session with tokens attributed to "agent-alpha".
	now := time.Now().UTC()
	writeTestSessionMeta(t, api.homePath, "sess-single-abc", "agent-alpha", 100, 50, now)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=month", nil)
	r.URL.RawQuery = "period=month"
	api.HandleTokenStats(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /stats/tokens?period=month must return 200; body=%s", w.Body.String())
	var resp struct {
		Agents []struct {
			AgentID   string `json:"agent_id"`
			TokensIn  int    `json:"tokens_in"`
			TokensOut int    `json:"tokens_out"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Agents, 1, "must return exactly 1 agent entry; body=%s", w.Body.String())
	assert.Equal(t, "agent-alpha", resp.Agents[0].AgentID, "agent_id must be agent-alpha")
	assert.Equal(t, 100, resp.Agents[0].TokensIn, "tokens_in must be 100")
	assert.Equal(t, 50, resp.Agents[0].TokensOut, "tokens_out must be 50")
}

// TestHandleTokenStats_MultiAgent verifies GET /api/v1/stats/tokens aggregates tokens
// from multiple agents and returns results sorted by agent_id.
// BDD: Given two sessions — sess-1 with active_agent_id="agent-beta" (tokens_in=200, tokens_out=80)
// and sess-2 with active_agent_id="agent-alpha" (tokens_in=60, tokens_out=30) — both within the
// current month,
// When GET /api/v1/stats/tokens?period=month is called,
// Then 200 with agents array of length 2, sorted by agent_id (alpha before beta), correct totals.
// Traces to: project-task-management-level1-spec.md FG-H5 (multi-agent differentiation test)
func TestHandleTokenStats_MultiAgent(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	now := time.Now().UTC()
	writeTestSessionMeta(t, api.homePath, "sess-multi-1", "agent-beta", 200, 80, now)
	writeTestSessionMeta(t, api.homePath, "sess-multi-2", "agent-alpha", 60, 30, now)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=month", nil)
	r.URL.RawQuery = "period=month"
	api.HandleTokenStats(w, r)

	require.Equal(t, http.StatusOK, w.Code, "GET /stats/tokens?period=month must return 200; body=%s", w.Body.String())
	var resp struct {
		Agents []struct {
			AgentID   string `json:"agent_id"`
			TokensIn  int    `json:"tokens_in"`
			TokensOut int    `json:"tokens_out"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Agents, 2, "must return exactly 2 agent entries; body=%s", w.Body.String())

	// Response must be sorted by agent_id ascending (alpha < beta).
	agentIDs := []string{resp.Agents[0].AgentID, resp.Agents[1].AgentID}
	sorted := sort.StringsAreSorted(agentIDs)
	assert.True(t, sorted, "agents must be sorted by agent_id ascending; got %v", agentIDs)

	// Find each agent's entry and assert correct totals.
	byAgentID := make(map[string]struct {
		TokensIn  int
		TokensOut int
	})
	for _, a := range resp.Agents {
		byAgentID[a.AgentID] = struct {
			TokensIn  int
			TokensOut int
		}{a.TokensIn, a.TokensOut}
	}

	alpha, hasAlpha := byAgentID["agent-alpha"]
	require.True(t, hasAlpha, "agent-alpha must appear in response")
	assert.Equal(t, 60, alpha.TokensIn, "agent-alpha tokens_in must be 60")
	assert.Equal(t, 30, alpha.TokensOut, "agent-alpha tokens_out must be 30")

	beta, hasBeta := byAgentID["agent-beta"]
	require.True(t, hasBeta, "agent-beta must appear in response")
	assert.Equal(t, 200, beta.TokensIn, "agent-beta tokens_in must be 200")
	assert.Equal(t, 80, beta.TokensOut, "agent-beta tokens_out must be 80")
}

// TestHandleTokenStats_StatusValidation verifies period parameter validation:
// no period defaults to month (200), and period=week returns 400.
// BDD: Given GET /api/v1/stats/tokens with no period,
// When the request is handled,
// Then 200 (defaults to month).
// Given GET /api/v1/stats/tokens?period=week,
// When the request is handled,
// Then 400.
// Traces to: project-task-management-level1-spec.md FG-H5
func TestHandleTokenStats_StatusValidation(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// No period → 200 (defaults to month).
	wDefault := httptest.NewRecorder()
	rDefault := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	api.HandleTokenStats(wDefault, rDefault)
	assert.Equal(t, http.StatusOK, wDefault.Code,
		"GET /stats/tokens without period must return 200; body=%s", wDefault.Body.String())

	// period=week → 400.
	wWeek := httptest.NewRecorder()
	rWeek := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=week", nil)
	rWeek.URL.RawQuery = "period=week"
	api.HandleTokenStats(wWeek, rWeek)
	assert.Equal(t, http.StatusBadRequest, wWeek.Code,
		"GET /stats/tokens?period=week must return 400; body=%s", wWeek.Body.String())
}
