// Tests for GET/PUT /api/v1/settings/memory (FR-019 / US-6, ADR-027),
// reconciled to the approved Settings redesign spec: FR-045 removes
// session_days and memory_retros_days from MemorySettings, so the retention
// fields and assertions that used to live here moved to
// /settings/retention (C-3, FR-041/FR-042/FR-043).

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

// TestMemorySettings_GetReturnsDefaults verifies that GET /api/v1/settings/memory
// returns the current effective settings from config (using the test API's defaults).
// The retention fields this test used to assert are removed from MemorySettings
// (FR-045); their checks live at /settings/retention (see
// TestMemorySettings_RetentionFieldsMovedToRetentionEndpoint below).
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
	// recap_model is present (may be empty string on a default config).
	assert.NotNil(t, resp.RecapModel, "recap_model must be present")
	// recap_fallback_models is present as an array (not null).
	assert.NotNil(t, resp.RecapFallbackModels, "recap_fallback_models must be present (array, not null)")
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
	// ADR-054: agents.list is json:"-" and can no longer be seeded via a raw
	// config.json splice (silently stripped on load) — omitted here; the
	// roster now lives in the entity store (seedAgentEntities below), which
	// also protects the in-memory cfg.Agents.List from being silently wiped
	// by the refreshConfigAndRewireServices reload this PUT triggers.
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
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// The session_days member is removed from the write shape (FR-045); the
	// body carries only fields the endpoint still owns.
	body := `{"auto_recap_enabled":true,"idle_timeout_minutes":45}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMemorySettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body: %s", w.Body.String())

	var resp gen.MemorySettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// auto_recap_enabled and idle_timeout_minutes must have been updated.
	require.NotNil(t, resp.AutoRecapEnabled)
	assert.True(t, *resp.AutoRecapEnabled, "auto_recap_enabled must be updated to true")
	require.NotNil(t, resp.IdleTimeoutMinutes)
	assert.Equal(t, 45, *resp.IdleTimeoutMinutes, "idle_timeout_minutes must be updated to 45")
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

// TestMemorySettings_RecapModelRoundTrip verifies that recap_model and
// recap_fallback_models can be written via PUT and read back via GET.
func TestMemorySettings_RecapModelRoundTrip(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	// ADR-054: agents.list is json:"-" — see TestMemorySettings_PutUpdatesAndReturnsNewValues
	// for why the on-disk splice is omitted and seedAgentEntities is used instead.
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
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"recap_model":"claude-haiku-3","recap_fallback_models":[{"model":"gpt-4o-mini","provider":"openai"}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleMemorySettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body: %s", w.Body.String())

	var resp gen.MemorySettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.RecapModel)
	assert.Equal(t, "claude-haiku-3", *resp.RecapModel, "recap_model must round-trip")
	require.NotNil(t, resp.RecapFallbackModels)
	require.Len(t, *resp.RecapFallbackModels, 1, "recap_fallback_models must have 1 entry")
	fb := (*resp.RecapFallbackModels)[0]
	assert.Equal(t, "gpt-4o-mini", fb.Model)
	require.NotNil(t, fb.Provider)
	assert.Equal(t, "openai", *fb.Provider)
}

// TestMemorySettings_RetentionFieldsMovedToRetentionEndpoint — replaces
// TestMemorySettings_RetentionRetrosDefault (which asserted the
// memory_retros_days default of 180 on /settings/memory). FR-045 removes the
// retention fields from MemorySettings, so the moved check now pins BOTH
// halves of the move: the memory read carries no retention field any more,
// and the 180 default it used to pin is answered by the replacing endpoint
// GET /settings/retention (FR-041).
func TestMemorySettings_RetentionFieldsMovedToRetentionEndpoint(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/settings/memory", nil)
	api.HandleMemorySettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var mem map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mem))
	assert.NotContains(t, mem, "session_days",
		"FR-045 removes session_days from MemorySettings — the memory read must not carry it")
	assert.NotContains(t, mem, "memory_retros_days",
		"FR-045 removes memory_retros_days from MemorySettings — the memory read must not carry it")

	// The replacing endpoint answers the default this test pinned (FR-041:
	// memory_retros_days defaults to 180).
	h := newRetentionSectionAPI(t, true)
	wRet := h.do(http.MethodGet, "/api/v1/settings/retention", "")
	require.Equal(t, http.StatusOK, wRet.Code,
		"BLOCKED RED: GET /api/v1/settings/retention is not implemented — required by FR-041; body=%s", wRet.Body.String())
	var ret map[string]any
	require.NoError(t, json.Unmarshal(wRet.Body.Bytes(), &ret))
	assert.Equal(t, float64(180), ret["memory_retros_days"],
		"the default memory_retros_days of 180 must be answered by /settings/retention (FR-041)")
}

// TestMemorySettings_PutValidatesNegativeValues verifies that negative values
// for bounded fields are rejected with 400. The two retention rows are
// REPLACED by the FR-045 refusal: PUT /settings/memory carrying session_days
// or memory_retros_days answers 400 naming the retired field (their range
// enforcement moved to PUT /settings/retention, FR-042). On pre-merge code
// those rows still answer 200 — the loud RED of the FR-045 rows is expected
// until the refusal is implemented.
func TestMemorySettings_PutValidatesNegativeValues(t *testing.T) {
	api := buildExecutorTestAPI(t)

	cases := []struct {
		name string
		body string
	}{
		{"negative idle_timeout_minutes", `{"idle_timeout_minutes":-1}`},
		{"negative bootstrap_recap_max_per_minute", `{"bootstrap_recap_max_per_minute":-1}`},
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

	// FR-045 rows: the retired retention fields are refused, naming the
	// field, whatever the value would have been.
	refused := []struct {
		name     string
		body     string
		wantName string
	}{
		{"retired session_days", `{"session_days":-1}`, "session_days"},
		{"retired memory_retros_days", `{"memory_retros_days":-5}`, "memory_retros_days"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/memory", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleMemorySettings(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code,
				"RED: PUT /settings/memory still accepts %s — FR-045 removes it from MemorySettings and requires a 400 naming it (got %d); body: %s",
				tc.wantName, w.Code, w.Body.String())
			var errBody map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errBody),
				"the refusal must be a JSON error body; got: %s", w.Body.String())
			errMsg, _ := errBody["error"].(string)
			require.Contains(t, errMsg, tc.wantName,
				"the FR-045 refusal must name the retired field; got: %s", errMsg)
		})
	}
}
