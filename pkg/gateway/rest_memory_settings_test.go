//go:build !cgo

// Tests for GET/PUT /api/v1/settings/memory (FR-019 / US-6, ADR-027).

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

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestMemorySettings_GetReturnsDefaults verifies that GET /api/v1/settings/memory
// returns the current effective settings from config (using the test API's defaults).
func TestMemorySettings_GetReturnsDefaults(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/settings/memory", nil)
	api.HandleMemorySettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp gen.MemorySettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// The response must include all required fields (non-nil).
	assert.NotNil(t, resp.AutoRecapEnabled, "auto_recap_enabled must be present")
	assert.NotNil(t, resp.BootstrapRecapEnabled, "bootstrap_recap_enabled must be present")
	assert.NotNil(t, resp.SessionDays, "session_days must be present")
	assert.NotNil(t, resp.MemoryRetrosDays, "memory_retros_days must be present")
	assert.NotNil(t, resp.RecapModelAllowList, "recap_model_allow_list must be present (array, not null)")
}

// TestMemorySettings_PutUpdatesAndReturnsNewValues verifies that PUT
// /api/v1/settings/memory persists changes and returns the updated values.
// Uses a v1 config (version:1) to avoid v0→v1 migration stripping the
// agents.defaults fields that are added by the PUT.
func TestMemorySettings_PutUpdatesAndReturnsNewValues(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	// Write a v1 config so config.LoadConfig treats it as CurrentVersion (no
	// migration), preserving agents.defaults.auto_recap_enabled across the
	// safeUpdateConfigJSON → refreshConfigAndRewireServices round-trip.
	cfgJSON := `{"version":1,"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"test-agent","name":"Test Agent","type":"custom"}]}}`
	cfgPath := filepath.Join(tmpDir, "config.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"auto_recap_enabled":true,"idle_timeout_minutes":45,"session_days":60}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMemorySettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body: %s", w.Body.String())

	var resp gen.MemorySettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// auto_recap_enabled, idle_timeout_minutes, and session_days must have been updated.
	require.NotNil(t, resp.AutoRecapEnabled)
	assert.True(t, *resp.AutoRecapEnabled, "auto_recap_enabled must be updated to true")
	require.NotNil(t, resp.IdleTimeoutMinutes)
	assert.Equal(t, 45, *resp.IdleTimeoutMinutes, "idle_timeout_minutes must be updated to 45")
	require.NotNil(t, resp.SessionDays)
	assert.Equal(t, 60, *resp.SessionDays, "session_days must be updated to 60")
}

// TestMemorySettings_PutMethodNotAllowed verifies that unsupported methods return 405.
func TestMemorySettings_MethodNotAllowed(t *testing.T) {
	api := buildExecutorTestAPI(t)

	for _, method := range []string{http.MethodDelete, http.MethodPost, http.MethodPatch} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/v1/settings/memory", nil)
		api.HandleMemorySettings(w, r)
		assert.Equal(t, http.StatusMethodNotAllowed, w.Code,
			"method %s must be rejected with 405", method)
	}
}

// TestMemorySettings_PutValidatesNegativeValues verifies that negative values
// for bounded fields are rejected with 400.
func TestMemorySettings_PutValidatesNegativeValues(t *testing.T) {
	api := buildExecutorTestAPI(t)

	cases := []struct {
		name string
		body string
	}{
		{"negative session_days", `{"session_days":-1}`},
		{"negative memory_retros_days", `{"memory_retros_days":-5}`},
		{"negative idle_timeout_minutes", `{"idle_timeout_minutes":-1}`},
		{"negative bootstrap_recap_max_per_minute", `{"bootstrap_recap_max_per_minute":-1}`},
		{"negative bootstrap_recap_daily_budget_usd", `{"bootstrap_recap_daily_budget_usd":-0.01}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleMemorySettings(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code,
				"negative value in %s must return 400; body: %s", tc.name, w.Body.String())
		})
	}
}
