//go:build !cgo

// BDD: token stats REST API tests.
// Traces to: FR-013 (token usage stats, period validation, method enforcement).

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
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
// BDD: Given GET /api/v1/stats/tokens?period=<day|week|month|all>, Then 200.
// Given GET /api/v1/stats/tokens (no period), Then 200 (defaults to month).
// Given GET /api/v1/stats/tokens?period=garbage, Then 400.
// Traces to: token-usage-tracking-2026-06.md (period enum day/week/month/all).
func TestHandleTokenStats_PeriodValidation(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Each valid period (incl. day/week/all, previously rejected) → 200.
	for _, period := range []string{"day", "week", "month", "all"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
		r.URL.RawQuery = "period=" + period
		api.HandleTokenStats(w, r)
		assert.Equalf(t, http.StatusOK, w.Code,
			"GET /stats/tokens?period=%s must return 200; body=%s", period, w.Body.String())
	}

	// no period → 200 (defaults to month).
	wDefault := httptest.NewRecorder()
	rDefault := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	api.HandleTokenStats(wDefault, rDefault)
	assert.Equal(t, http.StatusOK, wDefault.Code,
		"GET /stats/tokens without period must return 200 (defaults to month); body=%s", wDefault.Body.String())

	// unrecognized period → 400.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	rBad.URL.RawQuery = "period=garbage"
	api.HandleTokenStats(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code,
		"GET /stats/tokens?period=garbage must return 400; body=%s", wBad.Body.String())
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

// TestHandleTokenStats_ExcludesOutOfPeriodSessions verifies that sessions outside the
// current month are excluded from the token stats response.
// BDD: Given a session from the current month (agent-1, tokens_in=100, tokens_out=50)
// and a session from the prior month (agent-2, tokens_in=999, tokens_out=999),
// When GET /api/v1/stats/tokens?period=month is called,
// Then 200 with agents array of length 1 (only agent-1), tokens_in=100, tokens_out=50.
// Traces to: project-task-management-level1-spec.md — TST-003 (period filter exclusion)
func TestHandleTokenStats_ExcludesOutOfPeriodSessions(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Step 1: Write a session meta file with updated_at in the current month (in-period).
	inPeriod := time.Now().UTC()
	writeTestSessionMeta(t, api.homePath, "sess-inperiod-001", "agent-1", 100, 50, inPeriod)

	// Step 2: Write another session meta file with updated_at in the prior month (out-of-period).
	outOfPeriod := time.Now().AddDate(0, -1, 0).UTC()
	writeTestSessionMeta(t, api.homePath, "sess-outofperiod-002", "agent-2", 999, 999, outOfPeriod)

	// Step 3: Call GET /api/v1/stats/tokens?period=month.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=month", nil)
	r.URL.RawQuery = "period=month"
	api.HandleTokenStats(w, r)

	// Step 4: Assert response status 200.
	require.Equal(t, http.StatusOK, w.Code,
		"GET /stats/tokens?period=month must return 200; body=%s", w.Body.String())

	var resp struct {
		Agents []struct {
			AgentID   string `json:"agent_id"`
			TokensIn  int    `json:"tokens_in"`
			TokensOut int    `json:"tokens_out"`
		} `json:"agents"`
		TotalTokensIn  int `json:"total_tokens_in"`
		TotalTokensOut int `json:"total_tokens_out"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Step 5: Assert the agents array has exactly 1 entry (for agent-1, not agent-2).
	require.Len(t, resp.Agents, 1,
		"agents must contain exactly 1 entry (out-of-period session must be excluded); body=%s", w.Body.String())
	assert.Equal(t, "agent-1", resp.Agents[0].AgentID,
		"only the in-period session's agent must appear; body=%s", w.Body.String())

	// Step 6: Assert tokens_in=100, tokens_out=50 (only in-period values).
	assert.Equal(t, 100, resp.Agents[0].TokensIn,
		"tokens_in must be 100 (only in-period session counted); body=%s", w.Body.String())
	assert.Equal(t, 50, resp.Agents[0].TokensOut,
		"tokens_out must be 50 (only in-period session counted); body=%s", w.Body.String())

	// Differentiation test: assert agent-2 is NOT present (not just empty array).
	for _, a := range resp.Agents {
		assert.NotEqual(t, "agent-2", a.AgentID,
			"agent-2 (out-of-period) must not appear in stats response")
	}
}

// TestHandleTokenStats_StatusValidation verifies period parameter validation:
// no period defaults to month (200); the four valid periods (day/week/month/all)
// each return 200; an unrecognized period returns 400.
// BDD: Given GET /api/v1/stats/tokens with no period, Then 200 (defaults to month).
// Given GET /api/v1/stats/tokens?period=<day|week|month|all>, Then 200.
// Given GET /api/v1/stats/tokens?period=garbage, Then 400.
// Traces to: token-usage-tracking-2026-06.md (period enum day/week/month/all).
func TestHandleTokenStats_StatusValidation(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// No period → 200 (defaults to month).
	wDefault := httptest.NewRecorder()
	rDefault := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	api.HandleTokenStats(wDefault, rDefault)
	assert.Equal(t, http.StatusOK, wDefault.Code,
		"GET /stats/tokens without period must return 200; body=%s", wDefault.Body.String())

	// Each valid period → 200.
	for _, period := range []string{"day", "week", "month", "all"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
		r.URL.RawQuery = "period=" + period
		api.HandleTokenStats(w, r)
		assert.Equalf(t, http.StatusOK, w.Code,
			"GET /stats/tokens?period=%s must return 200; body=%s", period, w.Body.String())
	}

	// Unrecognized period → 400.
	wBad := httptest.NewRecorder()
	rBad := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	rBad.URL.RawQuery = "period=garbage"
	api.HandleTokenStats(wBad, rBad)
	assert.Equal(t, http.StatusBadRequest, wBad.Code,
		"GET /stats/tokens?period=garbage must return 400; body=%s", wBad.Body.String())
}

// writeTestSessionMetaWithByModel writes a meta.json for a session that includes
// per-model token breakdown in stats.by_model. The by_model map is a JSON object
// whose keys are model names and whose values follow the ModelTokens shape
// (cache_read, cache_write, in, out, total). This is used by the contract-drift
// guard test below to exercise the by_model path in HandleTokenStats.
func writeTestSessionMetaWithByModel(
	t *testing.T,
	homePath, sessionID, agentID string,
	tokensIn, tokensOut, cacheRead, cacheWrite int,
	byModel map[string]struct{ In, Out, CacheRead, CacheWrite, Total int },
	updatedAt time.Time,
) {
	t.Helper()
	agentLoopHome := filepath.Dir(homePath)
	sessDir := filepath.Join(agentLoopHome, "sessions", sessionID)
	require.NoError(t, os.MkdirAll(sessDir, 0o700))

	// Encode by_model as inline JSON.
	byModelParts := make([]string, 0, len(byModel))
	for model, mt := range byModel {
		byModelParts = append(byModelParts, fmt.Sprintf(
			"%q:{\"in\":%d,\"out\":%d,\"cache_read\":%d,\"cache_write\":%d,\"total\":%d}",
			model, mt.In, mt.Out, mt.CacheRead, mt.CacheWrite, mt.Total,
		))
	}
	byModelJSON := "{" + strings.Join(byModelParts, ",") + "}"

	meta := fmt.Sprintf(
		`{"id":%q,"agent_id":%q,"active_agent_id":%q,"status":"active","channel":"webchat","type":"chat",`+
			`"created_at":%q,"updated_at":%q,"partitions":[],`+
			`"stats":{"tokens_in":%d,"tokens_out":%d,"tokens_total":%d,"cost":0,"tool_calls":0,"message_count":0,`+
			`"tokens_cache_read":%d,"tokens_cache_write":%d,"by_model":%s}}`,
		sessionID, agentID, agentID,
		updatedAt.UTC().Format(time.RFC3339),
		updatedAt.UTC().Format(time.RFC3339),
		tokensIn, tokensOut, tokensIn+tokensOut,
		cacheRead, cacheWrite,
		byModelJSON,
	)
	require.NoError(t, os.WriteFile(filepath.Join(sessDir, "meta.json"), []byte(meta), 0o600))
}

// TestHandleTokenStats_WireShapeMatchesContract is the contract-drift guard for
// HandleTokenStats. It verifies that the JSON body emitted by the handler can be
// decoded into the oapi-codegen-generated gen.TokenUsageSummary type using
// DisallowUnknownFields — meaning every field the handler emits is recognised by
// the contract type, and the field names/JSON tags are byte-identical.
//
// The risk this guards against: rest_stats.go uses three hand-written local types
// (byModelCell, agentEntryWithByModel, tokenUsageResp) that mirror the inline
// anonymous struct shapes generated by oapi-codegen inside gen.TokenUsageSummary.
// If a future contract regen changes the generated inline shape (e.g., renames
// "cache_read" to "cache_read_tokens"), these hand-mirrors become stale. The
// verify-contracts gate only checks committed generated files; it cannot see the
// hand-written types in pkg/gateway. This test closes that gap.
//
// Drift that this test catches:
//  1. byModelCell JSON tag rename (e.g., cache_read → something else) →
//     DisallowUnknownFields rejects the unknown key when decoding into
//     gen.TokenUsageSummary's anonymous by_model element struct.
//  2. Extra field added to byModelCell with no matching generated field →
//     DisallowUnknownFields rejects it.
//  3. tokenUsageResp top-level field rename (e.g., period_start → start) →
//     the assertions on gen.TokenUsageSummary.PeriodStart / PeriodEnd fail
//     (they'd be zero values).
//  4. agentEntryWithByModel dropping a required field →
//     gen.TokenUsageSummary.Agents[i] assertions fail (zero value on the field).
//
// BDD:
//
//	Given one session attributed to "agent-contract" with ByModel data for
//	model "test-model-a" (total=300, cache_read=50, cache_write=20) and
//	top-level cache totals (tokens_cache_read=50, tokens_cache_write=20),
//	When GET /api/v1/stats/tokens?period=all is called,
//	Then the JSON body decodes into gen.TokenUsageSummary with DisallowUnknownFields,
//	     agents[0].by_model["test-model-a"].total == 300,
//	     agents[0].by_model["test-model-a"].cache_read == 50,
//	     top-level by_model["test-model-a"].total == 300,
//	     tokens_cache_read == 50, tokens_cache_write == 20,
//	     period_end is non-zero.
//
// Traces to: rest_stats.go — byModelCell, agentEntryWithByModel, tokenUsageResp
// (hand-written types that mirror gen.TokenUsageSummary's inline shapes)
func TestHandleTokenStats_WireShapeMatchesContract(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// BDD: Given a session with ByModel data.
	// Use period=all so we don't need to hit a specific month boundary.
	now := time.Now().UTC()
	writeTestSessionMetaWithByModel(
		t,
		api.homePath,
		"sess-contract-drift-guard",
		"agent-contract",
		200, 100, // tokensIn, tokensOut
		50, 20, // cacheRead, cacheWrite (top-level cache totals)
		map[string]struct{ In, Out, CacheRead, CacheWrite, Total int }{
			"test-model-a": {In: 0, Out: 0, CacheRead: 50, CacheWrite: 20, Total: 300},
		},
		now,
	)

	// BDD: When GET /api/v1/stats/tokens?period=all is called.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
	r.URL.RawQuery = "period=all"
	api.HandleTokenStats(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"GET /stats/tokens?period=all must return 200; body=%s", w.Body.String())

	rawBody := w.Body.Bytes()

	// BDD: Then the JSON body decodes into gen.TokenUsageSummary with
	// DisallowUnknownFields — any field the handler emits that the contract
	// type does not recognise causes a decode error here, catching drift.
	var decoded gen.TokenUsageSummary
	dec := json.NewDecoder(bytes.NewReader(rawBody))
	dec.DisallowUnknownFields()
	require.NoError(t, dec.Decode(&decoded),
		"handler output must decode into gen.TokenUsageSummary with DisallowUnknownFields; "+
			"a decode error means byModelCell / tokenUsageResp JSON tags drifted from the contract. body=%s",
		string(rawBody),
	)

	// BDD: Then agents is non-nil and contains exactly one entry.
	require.NotNil(t, decoded.Agents,
		"gen.TokenUsageSummary.Agents must be non-nil after decode")
	require.Len(t, decoded.Agents, 1,
		"must have exactly 1 agent entry; body=%s", string(rawBody))

	agent := decoded.Agents[0]
	assert.Equal(t, "agent-contract", agent.AgentId,
		"agent_id must be agent-contract")

	// BDD: Then agents[0].by_model["test-model-a"].total == 300.
	// by_model is a *map[string]struct{...} — must be non-nil.
	require.NotNil(t, agent.ByModel,
		"agents[0].by_model must be populated for a session with ByModel data")
	cell, hasModel := (*agent.ByModel)["test-model-a"]
	require.True(t, hasModel,
		"agents[0].by_model must contain key 'test-model-a'; by_model=%v", *agent.ByModel)

	assert.Equal(t, 300, cell.Total,
		"agents[0].by_model['test-model-a'].total must be 300")
	require.NotNil(t, cell.CacheRead,
		"agents[0].by_model['test-model-a'].cache_read must be non-nil (non-zero)")
	assert.Equal(t, 50, *cell.CacheRead,
		"agents[0].by_model['test-model-a'].cache_read must be 50")
	require.NotNil(t, cell.CacheWrite,
		"agents[0].by_model['test-model-a'].cache_write must be non-nil (non-zero)")
	assert.Equal(t, 20, *cell.CacheWrite,
		"agents[0].by_model['test-model-a'].cache_write must be 20")

	// BDD: Then top-level by_model["test-model-a"].total == 300.
	require.NotNil(t, decoded.ByModel,
		"top-level by_model must be populated")
	topCell, hasTopModel := (*decoded.ByModel)["test-model-a"]
	require.True(t, hasTopModel,
		"top-level by_model must contain key 'test-model-a'")
	assert.Equal(t, 300, topCell.Total,
		"top-level by_model['test-model-a'].total must be 300")

	// BDD: Then tokens_cache_read == 50, tokens_cache_write == 20.
	require.NotNil(t, decoded.TokensCacheRead,
		"top-level tokens_cache_read must be non-nil")
	assert.Equal(t, 50, *decoded.TokensCacheRead,
		"top-level tokens_cache_read must be 50")
	require.NotNil(t, decoded.TokensCacheWrite,
		"top-level tokens_cache_write must be non-nil")
	assert.Equal(t, 20, *decoded.TokensCacheWrite,
		"top-level tokens_cache_write must be 20")

	// BDD: Then period_end is non-zero (period=all still sets PeriodEnd).
	assert.False(t, decoded.PeriodEnd.IsZero(),
		"period_end must be non-zero; got %v", decoded.PeriodEnd)

	// Belt-and-suspenders: re-marshal the decoded gen.TokenUsageSummary and
	// verify the key fields survive the round-trip.
	reMarshaled, err := json.Marshal(decoded)
	require.NoError(t, err, "re-marshal of decoded gen.TokenUsageSummary must not error")
	reMarshaledStr := string(reMarshaled)
	assert.Contains(t, reMarshaledStr, `"test-model-a"`,
		"re-marshaled output must contain the model key")
	assert.Contains(t, reMarshaledStr, `"total":300`,
		"re-marshaled output must contain total:300")
	assert.Contains(t, reMarshaledStr, `"cache_read":50`,
		"re-marshaled output must contain cache_read:50")
}

// TestHandleTokenStats_PartialFlag exercises the partial / partial_error_count
// fields added to the /api/v1/stats/tokens response in rest_stats.go.
//
// The partial flag is set when agentLoop.ListAllSessions() returns a non-empty
// errs slice — indicating that at least one session store failed to enumerate
// sessions (ListSessions returned an error rather than just skipping a bad file).
//
// # Background: what actually surfaces in errs
//
// UnifiedStore.ListSessions() swallows individual bad meta.json entries (it logs
// a warning and continues). An error is only returned when os.ReadDir on the
// store's base directory fails (e.g., EACCES, ENOTDIR, ENOENT on a pre-existing
// dir). AgentLoop.ListAllSessions() then appends that error to errs.
//
// Concretely: making the shared sessions directory unreadable (chmod 000) causes
// sharedSessionStore.ListSessions() to return an error, which lands in errs.
//
// # Sub-tests
//
//   - Clean: normal sessions, no errs → response has NO partial field (or
//     partial:false) and NO partial_error_count.
//   - Partial: shared sessions directory made unreadable → ListAllSessions
//     returns errs with ≥1 entry → response has partial:true and
//     partial_error_count:1; status is still 200.
//
// Traces to: rest_stats.go — tokenUsageResp.Partial / tokenUsageResp.PartialErrorCount
func TestHandleTokenStats_PartialFlag(t *testing.T) {
	// ── Sub-test 1: clean case ───────────────────────────────────────────────
	// BDD: Given normal sessions with no store errors,
	// When GET /api/v1/stats/tokens?period=all is called,
	// Then 200 with partial absent or false, and partial_error_count absent.
	// Traces to: rest_stats.go:231-232 (Partial bool `json:"partial,omitempty"`)
	t.Run("clean_no_partial", func(t *testing.T) {
		api := newTestRestAPIWithHome(t)
		now := time.Now().UTC()
		writeTestSessionMeta(t, api.homePath, "sess-clean-001", "agent-clean", 100, 50, now)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
		r.URL.RawQuery = "period=all"
		api.HandleTokenStats(w, r)

		require.Equal(t, http.StatusOK, w.Code,
			"GET /stats/tokens?period=all must return 200 in clean case; body=%s", w.Body.String())

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

		// partial must be absent OR false (omitempty on bool means absent when false).
		if partialVal, hasPartial := resp["partial"]; hasPartial {
			assert.Equal(t, false, partialVal,
				"partial must be false in the clean case; body=%s", w.Body.String())
		}
		// partial_error_count must be absent (omitempty on *int means absent when nil).
		_, hasPartialCount := resp["partial_error_count"]
		assert.False(t, hasPartialCount,
			"partial_error_count must be absent in the clean case; body=%s", w.Body.String())

		// Differentiation: the valid session's tokens must appear — proving the
		// handler is not just returning a trivial empty response.
		agents, _ := resp["agents"].([]any)
		require.NotEmpty(t, agents,
			"agents must be non-empty in the clean case (session was written); body=%s", w.Body.String())
	})

	// ── Sub-test 2: partial case ─────────────────────────────────────────────
	// BDD: Given the shared sessions directory is unreadable (chmod 000),
	// so that sharedSessionStore.ListSessions() returns an EACCES error,
	// When GET /api/v1/stats/tokens?period=all is called,
	// Then 200 (partial failure does not abort the request),
	//      partial:true, partial_error_count >= 1.
	// Traces to: rest_stats.go:239-242, 251 — errs slice drives both fields.
	t.Run("partial_unreadable_sessions_dir", func(t *testing.T) {
		// chmod 000 does not block root (root bypasses DAC permission checks), so
		// os.ReadDir would still succeed and no partial error would surface. Skip
		// when running as root (e.g. some CI runners) rather than false-fail.
		if os.Geteuid() == 0 {
			t.Skip("chmod-based permission test is a no-op as root")
		}

		api := newTestRestAPIWithHome(t)

		// Locate the shared sessions directory.
		// AgentLoop uses homePath = filepath.Dir(cfg.WorkspacePath()) = filepath.Dir(api.homePath).
		// sharedSessionStore baseDir = homePath/sessions = filepath.Dir(api.homePath)/sessions.
		agentLoopHome := filepath.Dir(api.homePath)
		sessionsDir := filepath.Join(agentLoopHome, "sessions")

		// Ensure the sessions directory exists so chmod has a target.
		require.NoError(t, os.MkdirAll(sessionsDir, 0o700))

		// Register cleanup to restore permissions BEFORE t.TempDir() removes the
		// parent tree — otherwise RemoveAll would fail on a mode-000 directory.
		t.Cleanup(func() {
			// Restore read+execute so the cleanup can descend into the tree.
			_ = os.Chmod(sessionsDir, 0o700)
		})

		// Remove read and execute bits → os.ReadDir(sessionsDir) returns EACCES.
		require.NoError(t, os.Chmod(sessionsDir, 0o000),
			"chmod 000 on sessions dir must succeed (running as non-root)")

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens", nil)
		r.URL.RawQuery = "period=all"
		api.HandleTokenStats(w, r)

		// Handler must still return 200 — partial is a warning, not a fatal error.
		require.Equal(t, http.StatusOK, w.Code,
			"GET /stats/tokens must return 200 even when sessions dir is unreadable; body=%s", w.Body.String())

		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

		// partial must be present and true.
		partialVal, hasPartial := resp["partial"]
		require.True(t, hasPartial,
			"partial field must be present when session store errors occurred; body=%s", w.Body.String())
		assert.Equal(t, true, partialVal,
			"partial must be true when sharedSessionStore.ListSessions() failed; body=%s", w.Body.String())

		// partial_error_count must be present and >= 1.
		partialCountVal, hasPartialCount := resp["partial_error_count"]
		require.True(t, hasPartialCount,
			"partial_error_count must be present when partial:true; body=%s", w.Body.String())
		partialCount, ok := partialCountVal.(float64) // JSON numbers decode as float64.
		require.True(t, ok,
			"partial_error_count must be a number; got %T; body=%s", partialCountVal, w.Body.String())
		assert.GreaterOrEqual(t, int(partialCount), 1,
			"partial_error_count must be >= 1; got %v; body=%s", partialCount, w.Body.String())

		// Restore permissions so the cleanup goroutine can remove the dir.
		require.NoError(t, os.Chmod(sessionsDir, 0o700))
	})
}
