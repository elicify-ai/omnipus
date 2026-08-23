// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_providers_delete_test.go — T068-09 (ADR-068 FR-010/FR-011/FR-013/
// FR-042, D14.2).
//
// DELETE /api/v1/providers/{id}: the five idempotent steps run under the
// config lock after RECOMPUTING dependents / backs_default (the GET values
// are advisory, MAJ-018), the guard ladder (404 unconfigured / 503 locked /
// 503 bypass / 401 unauthenticated / 409 backs-default without new_default /
// 400 invalid new_default), the audit entry carrying the credential REF NAME
// (never the value), and the partial-failure contract: 500 {deleted:false}
// leaving a retryable state, where a second identical DELETE succeeds and no
// orphaned secret survives a completed run (SC-003).
//
// TDD rows 10, 10a, 10c, 10d, 11, 12, 13, 14, 15, 16 of the spec's test
// plan; Dataset "DELETE /providers/{id} bodies" rows 1-14.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
)

// testMasterKeyHex is a fixed 256-bit key so credentials.Unlock is
// deterministic in tests (mode 1, OMNIPUS_MASTER_KEY) regardless of the
// developer machine's key files.
const testMasterKeyHex = "abababababababababababababababababababababababababababababababab"

// newProviderDeleteAPI builds a restAPI whose config.json is on disk (so the
// DELETE's updateConfigJSONLocked read-modify-write cycle works), with an
// audit logger, an UNLOCKED credential store, and per-agent entity records
// seeded for every cfg.Agents.List row (refreshConfigAndRewireServices
// repopulates the roster from the entity store after every config write).
func newProviderDeleteAPI(t *testing.T, cfg *config.Config) (*restAPI, string, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKeyHex)
	tmpDir := t.TempDir()
	cfg.Agents.Defaults.Home = tmpDir
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "config.json"), marshalConfigForDisk(t, cfg), 0o600))

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditLogger.Close() })

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	credStore := credentials.NewStore(filepath.Join(tmpDir, "credentials.json"))
	require.NoError(t, credentials.Unlock(credStore))

	api := &restAPI{agentLoop: al, homePath: tmpDir, auditor: auditLogger, credStore: credStore}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)
	return api, tmpDir, auditDir
}

// providerDeleteBaseConfig mirrors newDefaultModelAPI's config shape: a
// routable default agent ("mia") plus the given extra agents and provider
// rows, with agents.defaults.default_model set to pair.
func providerDeleteBaseConfig(
	pair config.DefaultModel,
	rows []*config.ModelConfig,
	extraAgents ...config.AgentConfig,
) *config.Config {
	agents := append([]config.AgentConfig{{ID: "mia", Name: "Mia"}}, extraAgents...)
	return &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      pair,
				MaxTokens:         4096,
				MaxToolIterations: 10,
				DefaultAgentID:    "mia",
			},
			List: agents,
		},
		Providers: rows,
	}
}

// openrouterRow is a configured openrouter provider row backed by the
// credential ref the DELETE must sweep.
func deleteTestOpenrouterRow() *config.ModelConfig {
	return &config.ModelConfig{
		ModelName: "openrouter", Provider: "openrouter",
		Model: "z-ai/glm-5", APIKeyRef: "openrouter_API_KEY",
	}
}

// anthropicRow is a second configured provider so openrouter is never the
// last one.
func deleteTestAnthropicRow() *config.ModelConfig {
	return &config.ModelConfig{
		ModelName: "anthropic", Provider: "anthropic",
		Model: "claude-sonnet-4.6", APIKeyRef: "anthropic_API_KEY",
	}
}

// doProviderDelete sends DELETE /api/v1/providers/{id} through the real
// HandleProviders dispatcher. The request context carries the config
// snapshot (RequireNotBypass reads it) and, unless withUser is false, an
// authenticated admin user (the dispatcher is registered withOptionalAuth in
// production; the DELETE verb's 401 is inline per FR-042).
func doProviderDelete(
	t *testing.T, api *restAPI, id, body string, cfg *config.Config, withUser bool,
) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(http.MethodDelete, "/api/v1/providers/"+id, nil)
	} else {
		r = httptest.NewRequest(http.MethodDelete, "/api/v1/providers/"+id, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if cfg == nil {
		cfg = api.agentLoop.GetConfig()
	}
	ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, cfg)
	if withUser {
		ctx = context.WithValue(ctx, UserContextKey{}, &config.UserConfig{Username: "admin"})
	}
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	api.HandleProviders(w, r)
	return w
}

// deleteRespBody decodes a ProviderDeleteResponse-shaped body.
func deleteRespBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), "body=%s", w.Body.String())
	return m
}

// diskConfig reads config.json back as a raw map.
func diskConfig(t *testing.T, tmpDir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// diskProviderIDs lists the provider identities persisted in config.json.
func diskProviderIDs(t *testing.T, tmpDir string) []string {
	t.Helper()
	m := diskConfig(t, tmpDir)
	list, _ := m["providers"].([]any)
	ids := []string{}
	for _, it := range list {
		row, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if p, _ := row["provider"].(string); p != "" {
			ids = append(ids, p)
		}
	}
	return ids
}

// countAuditEvents counts records with the given event name across the audit dir.
func countAuditEvents(t *testing.T, auditDir, event string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
	require.NoError(t, err)
	n := 0
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
				n++
			}
		}
	}
	return n
}

// credentialExists reports whether the store still holds name.
func credentialExists(t *testing.T, api *restAPI, name string) bool {
	t.Helper()
	_, err := api.credStore.Get(name)
	if err == nil {
		return true
	}
	var nf *credentials.NotFoundError
	require.ErrorAs(t, err, &nf, "unexpected store error for %q", name)
	return false
}

// TestDeleteProvider_Unused200 is TDD row 10 / Dataset row 1: an unused,
// non-default-backing provider deletes with one request — 200
// {deleted:true, dependents:[], default_changed:false}, the row is gone from
// config.json, the credential is gone from the store (also when it was
// pre-deleted: NotFoundError is success), and one provider.deleted audit
// entry exists carrying the ref NAME and no key material (SC-003).
func TestDeleteProvider_Unused200(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, tmpDir, auditDir := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test-openrouter-secret"))

	w := doProviderDelete(t, api, "openrouter", "", nil, true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := deleteRespBody(t, w)
	assert.Equal(t, true, body["deleted"])
	assert.Equal(t, false, body["default_changed"])
	assert.Equal(t, []any{}, body["dependents"])

	assert.NotContains(t, diskProviderIDs(t, tmpDir), "openrouter", "row must be gone from config.json")
	assert.False(t, credentialExists(t, api, "openrouter_API_KEY"), "credential must be deleted")

	rec := findAuditEvent(t, auditDir, "provider.deleted")
	require.NotNil(t, rec, "audit entry provider.deleted must exist")
	details, _ := rec["details"].(map[string]any)
	require.NotNil(t, details)
	assert.Equal(t, "openrouter_API_KEY", details["credential_ref"])
	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "sk-test-openrouter-secret",
		"audit entry must never carry key material")
	assert.Equal(t, 1, countAuditEvents(t, auditDir, "provider.deleted"),
		"exactly one audit entry per completed run")

	// NotFoundError is success: deleting a provider whose key was already
	// swept (pre-deleted) still returns 200.
	cfg2 := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api2, _, _ := newProviderDeleteAPI(t, cfg2)
	w2 := doProviderDelete(t, api2, "openrouter", "", nil, true)
	require.Equal(t, http.StatusOK, w2.Code, "pre-deleted key must count as success; body=%s", w2.Body.String())
}

// TestDeleteProvider_PartialFailureNoOrphanSecret is TDD row 10a / Dataset
// rows 10-11: an injected failure at step 2 (config write) or step 1 (entity
// update on agent 2 of 3) yields 500 {deleted:false}, leaves the provider
// row AND the credential in place (step 3 never ran — no orphaned secret),
// and a second identical DELETE succeeds; the audit entry exists exactly
// once per COMPLETED run.
func TestDeleteProvider_PartialFailureNoOrphanSecret(t *testing.T) {
	t.Run("config write fails", func(t *testing.T) {
		cfg := providerDeleteBaseConfig(
			config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
			[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
			config.AgentConfig{ID: "ava", Name: "Ava",
				Model: &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}},
			config.AgentConfig{ID: "scout", Name: "Scout",
				Model: &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}},
		)
		api, tmpDir, auditDir := newProviderDeleteAPI(t, cfg)
		require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

		testHookProviderDeleteConfigWrite = func() error { return errors.New("injected config write failure") }
		t.Cleanup(func() { testHookProviderDeleteConfigWrite = nil })

		w := doProviderDelete(t, api, "openrouter", "", nil, true)
		require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, false, deleteRespBody(t, w)["deleted"])
		assert.Contains(t, diskProviderIDs(t, tmpDir), "openrouter", "row must survive the failed step 2")
		assert.True(t, credentialExists(t, api, "openrouter_API_KEY"),
			"step 3 must never run after a step-2 failure — no orphaned deletion")
		assert.Equal(t, 0, countAuditEvents(t, auditDir, "provider.deleted"),
			"no audit entry for an uncompleted run")

		// Step 1 already ran and is idempotent: both dependents are cleared.
		store := agentstore.New(tmpDir)
		for _, id := range []string{"ava", "scout"} {
			rec, err := store.Get(id)
			require.NoError(t, err)
			require.NotNil(t, rec.Model)
			assert.Empty(t, rec.Model.Primary, "agent %s primary must be cleared by step 1", id)
			assert.Empty(t, rec.Model.Provider, "agent %s provider must be cleared by step 1", id)
		}

		// Retry with the injection removed: all steps re-run and succeed.
		testHookProviderDeleteConfigWrite = nil
		w2 := doProviderDelete(t, api, "openrouter", "", nil, true)
		require.Equal(t, http.StatusOK, w2.Code, "retry must succeed; body=%s", w2.Body.String())
		assert.NotContains(t, diskProviderIDs(t, tmpDir), "openrouter")
		assert.False(t, credentialExists(t, api, "openrouter_API_KEY"))
		assert.Equal(t, 1, countAuditEvents(t, auditDir, "provider.deleted"),
			"exactly one audit entry per completed run")
	})

	t.Run("entity update fails on agent 2 of 3", func(t *testing.T) {
		cfg := providerDeleteBaseConfig(
			config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
			[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
			config.AgentConfig{ID: "a1", Name: "A1",
				Model: &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}},
			config.AgentConfig{ID: "a2", Name: "A2",
				Model: &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}},
			config.AgentConfig{ID: "a3", Name: "A3",
				Model: &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}},
		)
		api, tmpDir, auditDir := newProviderDeleteAPI(t, cfg)
		require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

		calls := 0
		testHookProviderDeleteEntityUpdate = func(string) error {
			calls++
			if calls == 2 {
				return errors.New("injected entity update failure")
			}
			return nil
		}
		t.Cleanup(func() { testHookProviderDeleteEntityUpdate = nil })

		w := doProviderDelete(t, api, "openrouter", "", nil, true)
		require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, false, deleteRespBody(t, w)["deleted"])
		assert.Contains(t, diskProviderIDs(t, tmpDir), "openrouter")
		assert.True(t, credentialExists(t, api, "openrouter_API_KEY"))

		// Retry: every step re-runs; each agent ends cleared exactly once.
		testHookProviderDeleteEntityUpdate = nil
		w2 := doProviderDelete(t, api, "openrouter", "", nil, true)
		require.Equal(t, http.StatusOK, w2.Code, "retry must succeed; body=%s", w2.Body.String())
		store := agentstore.New(tmpDir)
		for _, id := range []string{"a1", "a2", "a3"} {
			rec, err := store.Get(id)
			require.NoError(t, err)
			require.NotNil(t, rec.Model)
			assert.Empty(t, rec.Model.Primary, "agent %s must end cleared", id)
			assert.Empty(t, rec.Model.Provider, "agent %s must end cleared", id)
		}
		assert.NotContains(t, diskProviderIDs(t, tmpDir), "openrouter")
		assert.False(t, credentialExists(t, api, "openrouter_API_KEY"))
		assert.Equal(t, 1, countAuditEvents(t, auditDir, "provider.deleted"))
	})
}

// TestDeleteProvider_RecomputesUnderLock is TDD row 10c / Dataset row 9: the
// server recomputes dependents under configMu — a dialog opened from a GET
// showing dependents:[] is stale once an agent PUT re-points an agent at the
// provider; the DELETE response lists that agent and clears it. A second
// identical DELETE finds no row and returns 404.
func TestDeleteProvider_RecomputesUnderLock(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
		config.AgentConfig{ID: "ava", Name: "Ava"},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	// The "dialog GET": no dependents yet.
	require.Empty(t, computeProviderDependents(api.agentLoop.GetConfig(), "openrouter"))

	// Meanwhile an agent PUT re-points Ava at openrouter (entity store +
	// in-memory config, the two things the real handler writes).
	_, err := agentstore.New(tmpDir).Update("ava", func(rec *config.AgentConfig) error {
		rec.Model = &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, api.agentLoop.MutateConfig(func(c *config.Config) error {
		for i := range c.Agents.List {
			if c.Agents.List[i].ID == "ava" {
				c.Agents.List[i].Model = &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}
			}
		}
		return nil
	}))

	w := doProviderDelete(t, api, "openrouter", "", nil, true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := deleteRespBody(t, w)
	deps, _ := body["dependents"].([]any)
	require.Len(t, deps, 1, "recomputed dependents must list Ava, not the stale GET's []")
	dep, _ := deps[0].(map[string]any)
	assert.Equal(t, "ava", dep["id"])

	rec, err := agentstore.New(tmpDir).Get("ava")
	require.NoError(t, err)
	require.NotNil(t, rec.Model)
	assert.Empty(t, rec.Model.Primary, "recomputed dependent must be cleared")
	assert.Empty(t, rec.Model.Provider)

	// Concurrency row 9: the second identical DELETE recomputes under the
	// same lock and finds nothing.
	w2 := doProviderDelete(t, api, "openrouter", "", nil, true)
	require.Equal(t, http.StatusNotFound, w2.Code, "second DELETE must 404; body=%s", w2.Body.String())
}

// TestDeleteProvider_AuthPosture is TDD row 10d (DELETE half) / Dataset rows
// 12-13 / FR-042: 401 with no authenticated user (no pre-onboarding
// exception), 503 under dev_mode_bypass — and nothing is changed by either.
func TestDeleteProvider_AuthPosture(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	// No user in context, bypass off → 401.
	w := doProviderDelete(t, api, "openrouter", "", nil, false)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "body=%s", w.Body.String())

	// dev_mode_bypass on → 503 even for an authenticated caller.
	bypassCfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	bypassCfg.Gateway.DevModeBypass = true
	w2 := doProviderDelete(t, api, "openrouter", "", bypassCfg, true)
	assert.Equal(t, http.StatusServiceUnavailable, w2.Code, "body=%s", w2.Body.String())

	// Neither refusal changed anything.
	assert.Contains(t, diskProviderIDs(t, tmpDir), "openrouter")
	assert.True(t, credentialExists(t, api, "openrouter_API_KEY"))
}

// TestDeleteProvider_DependentsLeftWithoutModel is TDD row 11 / FR-013:
// dependent agents' primaries are CLEARED, never re-pointed; the response
// lists them (names sorted); the cleared records persist in the entity store.
func TestDeleteProvider_DependentsLeftWithoutModel(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		// No global default: after clearing, both agents derive needs_model.
		config.DefaultModel{},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
		config.AgentConfig{ID: "scout", Name: "Scout",
			Model: &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}},
		config.AgentConfig{ID: "ava", Name: "Ava",
			Model: &config.AgentModelConfig{Primary: "z-ai/glm-5", Provider: "openrouter"}},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	w := doProviderDelete(t, api, "openrouter", "", nil, true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := deleteRespBody(t, w)
	deps, _ := body["dependents"].([]any)
	require.Len(t, deps, 2)
	first, _ := deps[0].(map[string]any)
	second, _ := deps[1].(map[string]any)
	assert.Equal(t, "Ava", first["name"], "dependents must be name-sorted")
	assert.Equal(t, "Scout", second["name"])
	assert.Equal(t, "primary", first["role"])

	store := agentstore.New(tmpDir)
	freshCfg := api.agentLoop.GetConfig()
	for _, id := range []string{"ava", "scout"} {
		rec, err := store.Get(id)
		require.NoError(t, err)
		require.NotNil(t, rec.Model)
		assert.Empty(t, rec.Model.Primary, "agent %s must be left WITHOUT a model, never re-pointed", id)
		assert.Empty(t, rec.Model.Provider, id)
		assert.True(t, agentNeedsModel(freshCfg, rec), "agent %s must derive needs_model=true", id)
	}
}

// TestDeleteProvider_DefaultRequiresReplacement409 is TDD row 12 / Dataset
// rows 2, 4, 5, 6, 7: a default-backing provider refuses DELETE without a
// valid new_default — 409 with no body, 400 for a self-referencing /
// unconfigured / empty new_default and malformed JSON — and NOTHING changes.
func TestDeleteProvider_DefaultRequiresReplacement409(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	cases := []struct {
		name string
		body string
		want int
	}{
		{"no body backs default", "", http.StatusConflict},
		{"same id", `{"new_default":{"provider":"openrouter","model":"z-ai/glm-5"}}`, http.StatusBadRequest},
		{"unconfigured provider", `{"new_default":{"provider":"groq","model":"x"}}`, http.StatusBadRequest},
		{"empty object", `{"new_default":{}}`, http.StatusBadRequest},
		{"malformed json", `{"new_default":`, http.StatusBadRequest},
		{"unknown key", `{"nope":true}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doProviderDelete(t, api, "openrouter", tc.body, nil, true)
			assert.Equal(t, tc.want, w.Code, "body=%s", w.Body.String())
		})
	}

	// Provider, key and default unchanged after every refusal.
	assert.Contains(t, diskProviderIDs(t, tmpDir), "openrouter")
	assert.True(t, credentialExists(t, api, "openrouter_API_KEY"))
	fresh := api.agentLoop.GetConfig()
	assert.Equal(t, "openrouter", fresh.Agents.Defaults.DefaultModel.Provider)
}

// TestDeleteProvider_WithNewDefault is TDD row 13 / Dataset rows 3, 14: a
// valid new_default is applied BEFORE the row is removed (default_changed:
// true), and a replacement provider in a degraded state (its credential no
// longer resolves — the error/expired class, MAJ-011) is still accepted:
// the operator's risk, the dialog showed the state.
func TestDeleteProvider_WithNewDefault(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, tmpDir, auditDir := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))
	// anthropic's key is deliberately NOT in the store: the row's credential
	// does not resolve (degraded / error-class state) — still acceptable as
	// new_default per MAJ-011.

	body := `{"new_default":{"provider":"anthropic","model":"claude-sonnet-4.6"}}`
	w := doProviderDelete(t, api, "openrouter", body, nil, true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	resp := deleteRespBody(t, w)
	assert.Equal(t, true, resp["deleted"])
	assert.Equal(t, true, resp["default_changed"])
	nd, _ := resp["new_default"].(map[string]any)
	require.NotNil(t, nd)
	assert.Equal(t, "anthropic", nd["provider"])
	assert.Equal(t, "claude-sonnet-4.6", nd["model"])

	// The default pair changed (persisted + live) and the row is gone.
	fresh := api.agentLoop.GetConfig()
	assert.Equal(t, "anthropic", fresh.Agents.Defaults.DefaultModel.Provider)
	assert.Equal(t, "claude-sonnet-4.6", fresh.Agents.Defaults.DefaultModel.Model)
	assert.NotContains(t, diskProviderIDs(t, tmpDir), "openrouter")

	rec := findAuditEvent(t, auditDir, "provider.deleted")
	require.NotNil(t, rec)
	details, _ := rec["details"].(map[string]any)
	require.NotNil(t, details)
	assert.Equal(t, true, details["default_changed"])
	assert.Equal(t, "anthropic", details["new_default_provider"])
}

// TestDeleteProvider_404_503_Bypass503 is TDD row 14 / FR-011: 404 for an
// unconfigured id (and the reserved literals, which are never provider ids),
// 503 while the credential store is locked (before ANY change — config.json
// byte-identical), 503 under dev-mode bypass.
func TestDeleteProvider_404_503_Bypass503(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	// 404 unconfigured.
	w := doProviderDelete(t, api, "groq", "", nil, true)
	assert.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())

	// 404 reserved literals (never provider ids).
	for _, reserved := range []string{"catalog", "model-capabilities"} {
		wr := doProviderDelete(t, api, reserved, "", nil, true)
		assert.Equal(t, http.StatusNotFound, wr.Code, "reserved %q; body=%s", reserved, wr.Body.String())
	}

	// 503 locked, before any change: swap in a LOCKED store.
	before, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	unlocked := api.credStore
	api.credStore = credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	w2 := doProviderDelete(t, api, "openrouter", "", nil, true)
	assert.Equal(t, http.StatusServiceUnavailable, w2.Code, "body=%s", w2.Body.String())
	after, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "config.json must be byte-identical after a locked-store refusal")
	api.credStore = unlocked

	// 503 bypass.
	bypassCfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
	)
	bypassCfg.Gateway.DevModeBypass = true
	w3 := doProviderDelete(t, api, "openrouter", "", bypassCfg, true)
	assert.Equal(t, http.StatusServiceUnavailable, w3.Code, "body=%s", w3.Body.String())
}

// TestDeleteProvider_OnlyProviderRefused409 is TDD row 15 / Dataset row 8 /
// resolution #4: the last connected provider backs the default, so a direct
// DELETE with no body is 409, a self-referencing new_default is 400, and
// the provider, its key and the default are unchanged — the last connected
// provider is never deletable.
func TestDeleteProvider_OnlyProviderRefused409(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5"},
		[]*config.ModelConfig{deleteTestOpenrouterRow()},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	w := doProviderDelete(t, api, "openrouter", "", nil, true)
	assert.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())

	// There is no other provider to name: any new_default the client could
	// send is either the same id (400) or unconfigured (400).
	w2 := doProviderDelete(t, api, "openrouter",
		`{"new_default":{"provider":"openrouter","model":"z-ai/glm-5"}}`, nil, true)
	assert.Equal(t, http.StatusBadRequest, w2.Code, "body=%s", w2.Body.String())

	assert.Contains(t, diskProviderIDs(t, tmpDir), "openrouter")
	assert.True(t, credentialExists(t, api, "openrouter_API_KEY"))
	fresh := api.agentLoop.GetConfig()
	assert.Equal(t, "openrouter", fresh.Agents.Defaults.DefaultModel.Provider)
}

// TestDeleteProvider_FallbackRemoved is TDD row 16 / FR-013 / BDD "Fallback
// references are removed and listed": an agent referencing the provider only
// in fallback_models is listed with role fallback; after the DELETE the
// matching fallback entries are gone, other entries survive, and the agent
// keeps its primary (needs_model false).
func TestDeleteProvider_FallbackRemoved(t *testing.T) {
	cfg := providerDeleteBaseConfig(
		config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		[]*config.ModelConfig{deleteTestOpenrouterRow(), deleteTestAnthropicRow()},
		config.AgentConfig{ID: "jim", Name: "Jim",
			Model: &config.AgentModelConfig{Primary: "claude-sonnet-4.6", Provider: "anthropic"},
			FallbackModels: config.FallbackModelSlice{
				{Model: "z-ai/glm-5", Provider: "openrouter"},
				{Model: "claude-3.5-haiku", Provider: "anthropic"},
			}},
	)
	api, tmpDir, _ := newProviderDeleteAPI(t, cfg)
	require.NoError(t, api.credStore.Set("openrouter_API_KEY", "sk-test"))

	w := doProviderDelete(t, api, "openrouter", "", nil, true)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	body := deleteRespBody(t, w)
	deps, _ := body["dependents"].([]any)
	require.Len(t, deps, 1)
	dep, _ := deps[0].(map[string]any)
	assert.Equal(t, "jim", dep["id"])
	assert.Equal(t, "fallback", dep["role"])

	rec, err := agentstore.New(tmpDir).Get("jim")
	require.NoError(t, err)
	require.NotNil(t, rec.Model)
	assert.Equal(t, "claude-sonnet-4.6", rec.Model.Primary, "primary must be untouched")
	require.Len(t, rec.FallbackModels, 1, "the openrouter fallback entry must be removed")
	assert.Equal(t, "anthropic", rec.FallbackModels[0].Provider)
	assert.False(t, agentNeedsModel(api.agentLoop.GetConfig(), rec),
		"an agent that only lost a fallback keeps needs_model=false")
}
