// Tests for GET/PUT /api/v1/settings/token-budget (ADR-053 D12/R§8.3, FE-6).

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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestTokenBudget_GetReturnsLiveStatus verifies that GET
// /api/v1/settings/token-budget returns the live pool status (default fresh
// install = unbounded: budget 0, remaining -1, exhausted false, advisory set).
func TestTokenBudget_GetReturnsLiveStatus(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/settings/token-budget", nil)
	api.HandleTokenBudgetSettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp gen.TokenBudgetStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// Fresh install → unbounded sentinel (FR-175).
	assert.Equal(t, 0, resp.Budget, "fresh-install budget must be 0 (unbounded)")
	assert.Equal(t, -1, resp.Remaining, "unbounded remaining must be -1")
	assert.False(t, resp.Exhausted, "unbounded must never be exhausted")
	// by_scope is not yet tracked — all zeros.
	assert.Equal(t, 0, resp.ByScope.Owner)
	assert.Equal(t, 0, resp.ByScope.Member)
	assert.Equal(t, 0, resp.ByScope.Verifier)
	assert.Equal(t, 0, resp.ByScope.Judge)
	// Unbounded advisory must be present (R§8.3a).
	require.NotNil(t, resp.Advisory, "unbounded advisory must be present when budget==0")
	assert.NotEmpty(t, *resp.Advisory)
}

// TestTokenBudget_PutPersistsAndNotesRestart verifies that PUT persists the
// ceiling to config.json (planning.token_budget) and returns the persisted
// intent with the restart-required advisory. The live cap is NOT reloaded
// (restart-gated, R§8.3e) — budget in the response reflects the persisted value.
func TestTokenBudget_PutPersistsAndNotesRestart(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	// v1 config so LoadConfig treats it as CurrentVersion (no migration).
	// ADR-054: agents.list is json:"-" and can no longer be seeded via a raw
	// config.json splice (it is silently stripped on load) — omitted here
	// entirely; the roster now lives in the entity store (seedAgentEntities
	// below), keeping the in-memory cfg.Agents.List (still required for
	// AgentLoop construction).
	cfgJSON := `{"version":1,"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096}}}`
	cfgPath := filepath.Join(tmpDir, "config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom},
			},
		},
	}
	// ADR-054 D2/D6: this PUT reaches safeUpdateConfigJSON, which triggers
	// refreshConfigAndRewireServices and repopulates cfg.Agents.List wholesale
	// from the agent entity store — an in-memory-only roster would be
	// silently wiped to empty at that point. Persist a real entity record so
	// the roster survives the reload.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"budget":5000000}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/token-budget", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleTokenBudgetSettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body: %s", w.Body.String())

	var resp gen.TokenBudgetStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// Budget reflects the just-persisted intent (NOT the live cap, which is
	// restart-gated and still 0).
	assert.Equal(t, 5000000, resp.Budget, "PUT response budget must reflect persisted intent")
	// Restart-required advisory must be present and mention restart.
	require.NotNil(t, resp.Advisory, "set-time advisory must be present")
	assert.Contains(t, *resp.Advisory, "Restart required")

	// Verify it actually hit disk: planning.token_budget in config.json.
	raw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var persisted struct {
		Planning struct {
			TokenBudget int `json:"token_budget"`
		} `json:"planning"`
	}
	require.NoError(t, json.Unmarshal(raw, &persisted), "config.json: %s", string(raw))
	assert.Equal(t, 5000000, persisted.Planning.TokenBudget,
		"planning.token_budget must be persisted to config.json")
}

// TestTokenBudget_PutSetToZeroGivesUnbounded verifies that PUT with budget=0
// persists the unbounded sentinel and returns the R§8.3a unbounded advisory.
func TestTokenBudget_PutSetToZeroGivesUnbounded(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	// ADR-054: agents.list is json:"-" — see the sibling test above for why
	// the on-disk splice is omitted and seedAgentEntities is used instead.
	cfgJSON := `{"version":1,"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096}}}`
	cfgPath := filepath.Join(tmpDir, "config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom},
			},
		},
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/token-budget", strings.NewReader(`{"budget":0}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleTokenBudgetSettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body: %s", w.Body.String())

	var resp gen.TokenBudgetStatus
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Budget)
	assert.Equal(t, -1, resp.Remaining, "budget 0 → unbounded → remaining -1")
	require.NotNil(t, resp.Advisory)
	assert.Contains(t, *resp.Advisory, "unbounded", "budget=0 PUT must carry the unbounded advisory")
}

// TestTokenBudget_PutValidates verifies the PUT body validation.
func TestTokenBudget_PutValidates(t *testing.T) {
	api := buildExecutorTestAPI(t)

	cases := []struct {
		name string
		body string
	}{
		{"missing budget field", `{}`},
		{"negative budget", `{"budget":-1}`},
		{"invalid JSON", `{not-json`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/token-budget", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleTokenBudgetSettings(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code,
				"%s must return 400; body: %s", tc.name, w.Body.String())
		})
	}
}

// TestTokenBudget_MethodNotAllowed verifies that unsupported methods return 405.
func TestTokenBudget_MethodNotAllowed(t *testing.T) {
	api := buildExecutorTestAPI(t)

	for _, method := range []string{http.MethodDelete, http.MethodPost, http.MethodPatch} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/v1/settings/token-budget", nil)
		api.HandleTokenBudgetSettings(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code,
			"method %s must be rejected with 405", method)
	}
}
