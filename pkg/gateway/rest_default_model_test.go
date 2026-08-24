// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_default_model_test.go — T068-11 (ADR-068 FR-018/FR-042, MAJ-002).
//
// GET/PUT /api/v1/providers/default-model as its own adminWrap route, PUT
// validation over the served catalog with the custom/local bypass (X-13/X-17,
// X-22), audit provider.default_model.changed, and the TURN-TIME oracle:
// after the PUT, one real turn against a stub provider must carry the new
// pair in the session transcript — never a config read-back alone (the
// ADR-037 "saved but changed nothing" anti-pattern this file's sibling,
// rest_default_agent_singleton_test.go, exists to ban).
//
// Reserved-literal coverage (MAJ-002): "catalog", "default-model" (and
// "model-capabilities" until ADR-067 removes it) are never provider ids —
// PUT /providers/{reserved} → 400 field=id, DELETE /providers/{reserved} →
// 404, probe/onboarding-complete with a reserved id → 400 field=id, and the
// default-model route itself answers 405 on any verb other than GET/PUT.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// recordingLLMProvider is a stub provider that records every model string it
// is asked to serve, so the turn-time oracle can assert the LLM call itself
// was routed to the new default — not just the transcript stamp.
type recordingLLMProvider struct {
	models []string
}

func (m *recordingLLMProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	m.models = append(m.models, model)
	return &providers.LLMResponse{Content: "stubbed reply"}, nil
}

func (m *recordingLLMProvider) GetDefaultModel() string { return "model-before" }

// defaultModelTestCatalog builds a served catalog with one cloud provider
// ("cloudprov", models model-in-catalog and a 256-char slug) and one local
// provider ("ollama", locality local via loopback api). Layout mirrors
// pkg/providers/catalog/testdata/providers_catalog_2.0.0_fixture.json.
func defaultModelTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	max256 := strings.Repeat("m", 256)
	doc := fmt.Sprintf(`{
		"schema_version": "2.0.0",
		"version": "v2026.8.23",
		"updated_at": "2026-08-23T06:00:00Z",
		"source": "models.dev@0123456789abcdef0123456789abcdef01234567",
		"default_resize_limits": { "long_edge_px": 7680, "max_bytes": 10485760 },
		"providers": [
			{
				"id": "cloudprov",
				"name": "Cloud Prov",
				"company": "Cloud Prov",
				"api": "https://api.cloudprov.example/v1",
				"protocol": "openai-compatible",
				"env": ["CLOUDPROV_API_KEY"],
				"tier": "standard",
				"auth_methods": ["api_key"],
				"models": [
					{"id": "model-in-catalog", "name": "In Catalog", "tool_call": true,
					 "context_window": 200000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"},
					{"id": %q, "name": "Max Slug", "tool_call": true,
					 "context_window": 100000, "max_output_tokens": 8192,
					 "input_modalities": ["text"], "status": "active"}
				]
			},
			{
				"id": "ollama",
				"name": "Ollama",
				"company": "Ollama",
				"api": "http://localhost:11434/v1",
				"protocol": "openai-compatible",
				"env": [],
				"tier": "standard",
				"auth_methods": ["api_key"],
				"models": []
			}
		]
	}`, max256)
	c, err := catalog.NewCatalog([]byte(doc))
	require.NoError(t, err, "test catalog document must parse")
	return c
}

// newDefaultModelAPI builds a restAPI whose config.json is on disk (so the
// PUT's safeUpdateConfigJSON read-modify-write cycle works), with an audit
// logger and the given providers[] rows.
func newDefaultModelAPI(
	t *testing.T,
	pair config.DefaultModel,
	rows []*config.ModelConfig,
	provider providers.LLMProvider,
) (*restAPI, string, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      pair,
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// The routing singleton (ADR-054 D6.4) so the turn-time
				// oracle's ProcessDirectWithChannel resolves a default agent.
				DefaultAgentID: "mia",
			},
			// A real chat-target agent so default-agent resolution (and the
			// turn-time oracle's ProcessDirect) has something to resolve to.
			List: []config.AgentConfig{{ID: "mia", Name: "Mia"}},
		},
		Providers: rows,
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "config.json"), marshalConfigForDisk(t, cfg), 0o600))

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditLogger.Close() })

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)
	api := &restAPI{agentLoop: al, homePath: tmpDir, auditor: auditLogger}
	// Seed the per-agent entity records: refreshConfigAndRewireServices
	// (run by the PUT's safeUpdateConfigJSON) repopulates cfg.Agents.List
	// from the entity store, so without these the roster empties out after
	// the first config write and routing loses every agent (same seed the
	// sibling rest_default_agent_singleton_test.go performs).
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)
	return api, tmpDir, auditDir
}

func doDefaultModel(api *restAPI, method, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/api/v1/providers/default-model", nil)
	} else {
		r = httptest.NewRequest(method, "/api/v1/providers/default-model", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	api.HandleDefaultModel(w, r)
	return w
}

// errBody decodes a JSON error body into a map for error/field assertions.
func errBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), "body=%s", w.Body.String())
	return m
}

// TestDefaultModel_PutResolvesAtTurnTime is TDD row 17: the oracle is
// turn-time resolution, never a config read-back. PUT the new pair, run one
// real turn against the stub provider, and assert the session transcript's
// assistant entry carries the new model, GetModelConfig resolves the pair to
// the exact row, config.json has no model_name key, and the audit entry
// provider.default_model.changed exists with the old and new pairs.
func TestDefaultModel_PutResolvesAtTurnTime(t *testing.T) {
	rec := &recordingLLMProvider{}
	// Two rows serving DIFFERENT providers; the second is the PUT target.
	// Neither names a vault ref, so both are credential-usable custom rows
	// (no served catalog is installed → the membership check is bypassed the
	// same way a custom row bypasses it, X-13).
	rows := []*config.ModelConfig{
		{Name: "before", Provider: "provider-a", Model: "model-before", APIBase: "https://a.example/v1"},
		{Name: "after", Provider: "provider-b", Model: "model-after", APIBase: "https://b.example/v1"},
	}
	api, tmpDir, auditDir := newDefaultModelAPI(
		t, config.DefaultModel{Provider: "provider-a", Model: "model-before"}, rows, rec)

	// The PUT.
	w := doDefaultModel(api, http.MethodPut, `{"provider":"provider-b","model":"model-after"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "provider-b", resp["provider"])
	assert.Equal(t, "model-after", resp["model"])

	// config.json holds the pair and carries NO model_name key under
	// agents.defaults (CRIT-001).
	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var cfgRaw map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfgRaw))
	defaults := cfgRaw["agents"].(map[string]any)["defaults"].(map[string]any)
	_, hasAlias := defaults["model_name"]
	assert.False(t, hasAlias, "config.json must carry no agents.defaults.model_name key: %s", raw)
	assert.Equal(t, map[string]any{"provider": "provider-b", "model": "model-after"},
		defaults["default_model"])

	// GetModelConfig resolves the pair EXACTLY to the row that serves it.
	liveCfg := api.agentLoop.GetConfig()
	require.Equal(t, config.DefaultModel{Provider: "provider-b", Model: "model-after"},
		liveCfg.Agents.Defaults.DefaultModel,
		"the in-memory config must reflect the PUT without a restart")
	mc, err := liveCfg.GetModelConfig("provider-b", "model-after")
	require.NoError(t, err)
	assert.Equal(t, "provider-b", mc.Provider)

	// GET reads the same pair back.
	g := doDefaultModel(api, http.MethodGet, "")
	require.Equal(t, http.StatusOK, g.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &got))
	assert.Equal(t, "provider-b", got["provider"])
	assert.Equal(t, "model-after", got["model"])

	// TURN-TIME ORACLE. The lightweight test harness has no reload pipeline
	// wired (TriggerReload → ErrReloadNotConfigured, a confirmed no-op), so
	// apply the exact rebuild a production reload performs — the same call
	// default_model_pair_test.go uses — and then run ONE real turn against a
	// concrete session so the transcript write path is bound
	// (ProcessScheduled wires TranscriptSessionID+TranscriptStore for the
	// exact session id it is given).
	ctx := context.Background()
	require.NoError(t, api.agentLoop.ReloadProviderAndConfig(ctx, rec, api.agentLoop.GetConfig()))
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "the shared session store must be available")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	reply, err := api.agentLoop.ProcessScheduled(ctx, "mia", meta.ID, "hello there", "web", "chat-t068-11")
	require.NoError(t, err)
	require.NotEmpty(t, reply)

	// The stub provider's own record: the LLM call was routed to the NEW model.
	require.NotEmpty(t, rec.models, "the turn must reach the stub provider")
	assert.Equal(t, "model-after", rec.models[len(rec.models)-1],
		"the turn-time LLM call must carry the new default model")

	// The session transcript's assistant entry carries the new model
	// (TranscriptEntry.Model — FR-013 is the per-entry stamp; the provider
	// half of the pair is proven by GetModelConfig above and by the stub's
	// own routing record, since TranscriptEntry has no provider field).
	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var lastAssistantModel string
	for _, e := range entries {
		if e.Role == "assistant" && e.Model != "" {
			lastAssistantModel = e.Model
		}
	}
	assert.Equal(t, "model-after", lastAssistantModel,
		"the session transcript's assistant entry must carry the new model — "+
			"a 200 that does not change the next turn is the ADR-037 anti-pattern")

	// Audit: provider.default_model.changed with old and new pairs.
	found := findAuditEvent(t, auditDir, "provider.default_model.changed")
	require.NotNil(t, found, "audit entry provider.default_model.changed must exist")
	details, _ := found["details"].(map[string]any)
	require.NotNil(t, details, "audit entry must carry details")
	assert.Equal(t, "provider-a", details["old_provider"])
	assert.Equal(t, "model-before", details["old_model"])
	assert.Equal(t, "provider-b", details["new_provider"])
	assert.Equal(t, "model-after", details["new_model"])
}

// findAuditEvent scans the audit dir for the first record with the event name.
func findAuditEvent(t *testing.T, auditDir, event string) map[string]any {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
	require.NoError(t, err)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) != nil {
				continue
			}
			if rec["event"] == event {
				return rec
			}
		}
	}
	return nil
}

// TestDefaultModel_PutValidation is TDD row 18 — the "Default-model PUT"
// dataset rows over one harness: a served catalog with a cloud provider and
// a local provider, plus a configured custom row not in the catalog.
func TestDefaultModel_PutValidation(t *testing.T) {
	max256 := strings.Repeat("m", 256)
	rows := []*config.ModelConfig{
		{Name: "cloud", Provider: "cloudprov", Model: "model-in-catalog", APIBase: "https://api.cloudprov.example/v1"},
		{Name: "local", Provider: "ollama", Model: "llama3", APIBase: "http://localhost:11434/v1"},
		{Name: "custom", Provider: "my-proxy", Model: "user/slug", APIBase: "https://proxy.example/v1"},
	}
	api, _, _ := newDefaultModelAPI(
		t, config.DefaultModel{}, rows, &restMockProvider{})
	api.providerCatalog = defaultModelTestCatalog(t)

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantField  string
		wantErr    string
	}{
		{"row 1: connected + model in catalog", `{"provider":"cloudprov","model":"model-in-catalog"}`, 200, "", ""},
		{"row 2: unconfigured provider", `{"provider":"groq","model":"x"}`, 400, "provider", "provider not configured"},
		{"row 3: connected + unknown model", `{"provider":"cloudprov","model":"not-in-catalog"}`, 400, "model", "model not in catalog for provider"},
		{"row 4: custom row + user slug bypasses the catalog", `{"provider":"my-proxy","model":"any/user-slug"}`, 200, "", ""},
		{"row 5: local row + any non-empty model", `{"provider":"ollama","model":"whatever:latest"}`, 200, "", ""},
		{"row 6: empty provider", `{"provider":"","model":"x"}`, 400, "provider", ""},
		{"row 7: model exactly 256 chars, listed", `{"provider":"cloudprov","model":"` + max256 + `"}`, 200, "", ""},
		{"row 8: model 257 chars", `{"provider":"cloudprov","model":"` + max256 + `m"}`, 400, "model", ""},
		{"row 9: extra property", `{"provider":"cloudprov","model":"model-in-catalog","nope":true}`, 400, "", ""},
		{"row: empty object", `{}`, 400, "", ""},
		{"row: malformed JSON", `{"provider":`, 400, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doDefaultModel(api, http.MethodPut, tc.body)
			require.Equal(t, tc.wantStatus, w.Code, "body=%s", w.Body.String())
			if tc.wantStatus != 200 {
				m := errBody(t, w)
				if tc.wantField != "" {
					assert.Equal(t, tc.wantField, m["field"], "field must name the rejected input")
				}
				if tc.wantErr != "" {
					assert.Equal(t, tc.wantErr, m["error"])
				}
			}
		})
	}
}

// TestDefaultModel_GetNoDefault404: a zero pair answers 404
// {"error":"no default model"} (fresh install / after onboarding step 2).
func TestDefaultModel_GetNoDefault404(t *testing.T) {
	api, _, _ := newDefaultModelAPI(t, config.DefaultModel{}, nil, &restMockProvider{})
	w := doDefaultModel(api, http.MethodGet, "")
	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, "no default model", errBody(t, w)["error"])
}

// TestDefaultModel_AuthPosture is TDD row 10d's default-model half: the route
// is registered with adminWrap (withAuth → RequireNotBypass) — unauthenticated
// → 401, dev-mode bypass → 503. Exercised through the composed chain exactly
// as registered, not the bare handler.
func TestDefaultModel_AuthPosture(t *testing.T) {
	api, _, _ := newDefaultModelAPI(
		t, config.DefaultModel{Provider: "p", Model: "m"}, nil, &restMockProvider{})
	wrapped := api.adminWrap(api.HandleDefaultModel)

	t.Run("401 without a Bearer token", func(t *testing.T) {
		// A configured user forces real auth; no header → 401.
		cfg := *api.agentLoop.GetConfig()
		cfg.Gateway.Users = []config.UserConfig{{Username: "admin", PasswordHash: "x"}}
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/default-model", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, &cfg))
		w := httptest.NewRecorder()
		wrapped(w, r)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())
	})

	t.Run("503 under dev-mode bypass", func(t *testing.T) {
		cfg := *api.agentLoop.GetConfig()
		cfg.Gateway.DevModeBypass = true
		r := httptest.NewRequest(http.MethodGet, "/api/v1/providers/default-model", nil)
		r = r.WithContext(context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, &cfg))
		w := httptest.NewRecorder()
		wrapped(w, r)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body=%s", w.Body.String())
	})
}

// TestDefaultModel_ReservedLiterals covers the MAJ-002 scenario rows:
// reserved path segments are never provider ids anywhere an id is accepted.
func TestDefaultModel_ReservedLiterals(t *testing.T) {
	api, tmpDir, _ := newDefaultModelAPI(t, config.DefaultModel{}, nil, &restMockProvider{})

	t.Run("405 on verbs other than GET/PUT at the default-model route", func(t *testing.T) {
		for _, method := range []string{http.MethodDelete, http.MethodPost, http.MethodPatch} {
			w := doDefaultModel(api, method, "")
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "method=%s", method)
		}
	})

	t.Run("PUT default-model with a ProviderUpdateRequest body is a 400 on shape, nothing created", func(t *testing.T) {
		w := doDefaultModel(api, http.MethodPut, `{"api_key":"sk-test","model":"gpt-4o"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
		raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"default-model"`,
			"a provider named default-model must never be created")
	})

	t.Run("PUT /providers/{reserved} → 400 field=id", func(t *testing.T) {
		for _, id := range []string{"catalog", "default-model", "model-capabilities"} {
			r := httptest.NewRequest(http.MethodPut, "/api/v1/providers/"+id,
				strings.NewReader(`{"api_key":"sk-test"}`))
			w := httptest.NewRecorder()
			api.HandleProviders(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code, "id=%s body=%s", id, w.Body.String())
			m := errBody(t, w)
			assert.Equal(t, "id", m["field"], "id=%s", id)
			assert.Equal(t, fmt.Sprintf("unknown provider %q", id), m["error"], "id=%s", id)
		}
	})

	t.Run("DELETE /providers/catalog → 404", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/providers/catalog", nil)
		w := httptest.NewRecorder()
		api.HandleProviders(w, r)
		assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
	})
}

// TestProbeProviderID_ReservedLiterals — the probe half of MAJ-002 (row 11b
// of the Provider-id dataset; T068-13 owns the full outline).
func TestProbeProviderID_ReservedLiterals(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	for _, id := range []string{"catalog", "default-model", "model-capabilities"} {
		body := fmt.Sprintf(`{"id":%q,"auth":"api_key","api_key":"sk-test"}`, id)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/probe-provider",
			strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.HandleOnboardingProbeProvider(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, "id=%s body=%s", id, w.Body.String())
		var m map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
		assert.Equal(t, "id", m["field"], "id=%s", id)
		assert.Equal(t, fmt.Sprintf("unknown provider %q", id), m["error"],
			"the 400 body is the generic echo with no id list (CRIT-003)")
	}
}

// TestOnboardingComplete_ReservedLiteralID — the completion half of MAJ-002.
func TestOnboardingComplete_ReservedLiteralID(t *testing.T) {
	api, _ := newAuthMethodOnboardingAPI(t)
	body := `{"provider":{"auth_method":"api_key","id":"default-model","api_key":"sk-test","model":"x"},"admin":{"username":"admin","password":"secret123"}}`
	w := postOnboardingComplete(api, body)
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	assert.Equal(t, "id", m["field"])
	assert.False(t, api.onboardingMgr.IsComplete(), "nothing may be persisted on a reserved id")
}
