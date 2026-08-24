// Tests for GET/PUT /api/v1/settings/context (ADR-066 D9, FR-036 / US-11).
//
// The regression these guard: the contract, the generated types, the config
// struct and the whole Settings → Models panel shipped, but no handler was
// ever registered, so every control on that screen 404'd — including
// model_overrides, the ONLY documented escape from the context_window_unknown
// turn refusal. TestContextSettings_RouteIsRegisteredOnRealMux drives the real
// registerAdditionalEndpoints mux, so it fails with 404 if the registration is
// ever dropped again.

package gateway

import (
	"context"
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
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// newContextSettingsAPI builds a restAPI over a real $OMNIPUS_HOME with a v1
// config.json on disk, so the PUT's safeUpdateConfigJSON round-trip works.
func newContextSettingsAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	cfgJSON := `{"version":1,"agents":{"defaults":{"workspace":"` + tmpDir +
		`","model_name":"test-model","max_tokens":4096}},` +
		`"providers":[{"provider":"ollama","model":"llama3.1:8b","api_base":"http://127.0.0.1:11434"}]}`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(cfgJSON), 0o600))

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
		Providers: []*config.ModelConfig{
			{Provider: "ollama", Model: "llama3.1:8b", APIBase: "http://127.0.0.1:11434"},
		},
		Context: config.DefaultContextSettings(),
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{agentLoop: al, homePath: tmpDir}, tmpDir
}

// TestContextSettings_RouteIsRegisteredOnRealMux is the guard for the missing
// handler: it drives the production registerAdditionalEndpoints mux, so a
// missing registration answers 404 and fails here.
func TestContextSettings_RouteIsRegisteredOnRealMux(t *testing.T) {
	api, _ := newContextSettingsAPI(t)

	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	bypassCfg := &config.Config{}
	bypassCfg.Gateway.DevModeBypass = true

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/context", nil)
	req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusNotFound, w.Code,
		"GET /api/v1/settings/context must be registered on the real mux (the whole ADR-066 D9 operator surface hangs off it)")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp gen.ContextSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, config.DefaultMcpResultCap, resp.McpResultCap)
	assert.NotNil(t, resp.ModelOverrides, "model_overrides must be [] on the wire, never null")
}

// TestContextSettings_GetReturnsSeededDefaults verifies the GET projection.
func TestContextSettings_GetReturnsSeededDefaults(t *testing.T) {
	api, _ := newContextSettingsAPI(t)

	w := httptest.NewRecorder()
	api.HandleContextSettings(w, httptest.NewRequest(http.MethodGet, "/api/v1/settings/context", nil))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp gen.ContextSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, config.DefaultMcpResultCap, resp.McpResultCap)
	assert.Equal(t, config.DefaultBuiltinSuccessCap, resp.BuiltinSuccessCap)
	assert.Equal(t, config.DefaultBuiltinFailureCap, resp.BuiltinFailureCap)
	assert.Equal(t, config.DefaultAbsoluteTriggerChars, resp.AbsoluteTriggerChars)
	assert.Equal(t, config.DefaultIngestBoundBytes, resp.IngestBoundBytes)
	assert.Nil(t, resp.DefaultContextWindow, "default_context_window is unset on a fresh install")
	assert.Empty(t, resp.ModelOverrides)
}

// TestContextSettings_PutPersistsModelOverride is the US-2.AC4 path: the
// operator's escape from context_window_unknown must actually persist.
func TestContextSettings_PutPersistsModelOverride(t *testing.T) {
	api, home := newContextSettingsAPI(t)

	body := `{"mcp_result_cap":40000,"default_context_window":32768,` +
		`"model_overrides":[{"provider":"ollama","model":"llama3.1:8b","context_window":8192}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleContextSettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body: %s", w.Body.String())

	var resp gen.ContextSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 40000, resp.McpResultCap)
	require.NotNil(t, resp.DefaultContextWindow)
	assert.Equal(t, 32768, *resp.DefaultContextWindow)
	require.Len(t, resp.ModelOverrides, 1)
	assert.Equal(t, "ollama", resp.ModelOverrides[0].Provider)
	assert.Equal(t, "llama3.1:8b", resp.ModelOverrides[0].Model)
	assert.Equal(t, 8192, resp.ModelOverrides[0].ContextWindow)
	// Untouched fields keep their seeded values (partial update).
	assert.Equal(t, config.DefaultBuiltinSuccessCap, resp.BuiltinSuccessCap)

	// It reached disk, under the single `context` key.
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	ctxSection, ok := onDisk["context"].(map[string]any)
	require.True(t, ok, "config.json must carry a context section: %s", string(raw))
	assert.EqualValues(t, 40000, ctxSection["mcp_result_cap"])
	rows, ok := ctxSection["model_overrides"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
}

// TestContextSettings_PutPrunesOverrideForUnknownProvider covers the
// contract's "overrides whose provider no longer exists are pruned on write".
func TestContextSettings_PutPrunesOverrideForUnknownProvider(t *testing.T) {
	api, _ := newContextSettingsAPI(t)

	body := `{"model_overrides":[` +
		`{"provider":"ollama","model":"llama3.1:8b","context_window":8192},` +
		`{"provider":"deleted-provider","model":"ghost","context_window":4096}]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleContextSettings(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT body: %s", w.Body.String())

	var resp gen.ContextSettings
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.ModelOverrides, 1, "the row naming a provider that does not exist must be pruned")
	assert.Equal(t, "ollama", resp.ModelOverrides[0].Provider)
}

// TestContextSettings_PutNullClearsDefaultWindow — explicit null clears,
// absence leaves alone.
func TestContextSettings_PutNullClearsDefaultWindow(t *testing.T) {
	api, _ := newContextSettingsAPI(t)

	set := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context",
		strings.NewReader(`{"default_context_window":64000}`))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleContextSettings(set, r1)
	require.Equal(t, http.StatusOK, set.Code, "body: %s", set.Body.String())

	// An unrelated partial write must NOT clear it.
	keep := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context",
		strings.NewReader(`{"builtin_failure_cap":9000}`))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleContextSettings(keep, r2)
	require.Equal(t, http.StatusOK, keep.Code, "body: %s", keep.Body.String())
	var kept gen.ContextSettings
	require.NoError(t, json.Unmarshal(keep.Body.Bytes(), &kept))
	require.NotNil(t, kept.DefaultContextWindow, "an omitted field must be unchanged")
	assert.Equal(t, 64000, *kept.DefaultContextWindow)

	// Explicit null clears it.
	clear := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context",
		strings.NewReader(`{"default_context_window":null}`))
	r3.Header.Set("Content-Type", "application/json")
	api.HandleContextSettings(clear, r3)
	require.Equal(t, http.StatusOK, clear.Code, "body: %s", clear.Body.String())
	var cleared gen.ContextSettings
	require.NoError(t, json.Unmarshal(clear.Body.Bytes(), &cleared))
	assert.Nil(t, cleared.DefaultContextWindow, "an explicit null must clear default_context_window")
}

// TestContextSettings_PutRejectsOutOfRange covers every 400 rule the contract
// spells out, each naming the field.
func TestContextSettings_PutRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"cap over ceiling", `{"mcp_result_cap":150001}`, "mcp_result_cap"},
		{"cap under one", `{"builtin_success_cap":0}`, "builtin_success_cap"},
		{"failure cap under one", `{"builtin_failure_cap":-1}`, "builtin_failure_cap"},
		{"trigger under one", `{"absolute_trigger_chars":0}`, "absolute_trigger_chars"},
		{"ingest bound at ceiling", `{"ingest_bound_bytes":8388608}`, "ingest_bound_bytes"},
		{"ingest bound under one", `{"ingest_bound_bytes":0}`, "ingest_bound_bytes"},
		{"override window under one", `{"model_overrides":[{"provider":"ollama","model":"m","context_window":0}]}`, "model_overrides[0].context_window"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, _ := newContextSettingsAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/context", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleContextSettings(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), tc.want, "the 400 must name the offending field")
		})
	}
}
