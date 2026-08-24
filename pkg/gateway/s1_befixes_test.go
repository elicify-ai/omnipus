// Package gateway — Spec-1 BE-fixes tests for issues #351 and #352.
//
// #351 (FR-104): HandleProviders GET reports Connected ONLY when the provider's
// API key resolves to a non-empty credential.
//
// #352 (FR-105/FR-106): gateway.users is removed from RestartGatedKeys (the
// fresh-install restart banner) and hot_reload defaults on.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- Issue #351: Provider status = Connected iff key resolves ---

// TestHandleProviders_NoKey_IsDisconnected verifies that a provider listed in
// config with no resolvable API key reports status "disconnected".
//
// BDD (US-2 / AC1):
//
//	Given a provider in config whose api_key_ref resolves empty (no cred store, no env var)
//	When GET /api/v1/providers is called
//	Then the provider is shown as "disconnected" (not "connected").
//
// Traces to: FR-104, US-2/AC1, SC-103, test dataset row: no-key → Disconnected.
func TestHandleProviders_NoKey_IsDisconnected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
	// Make sure env var for the provider's APIKeyRef is not set.
	t.Setenv("ANTHROPIC_API_KEY", "")

	tmpDir := t.TempDir()
	cfgData := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "claude-sonnet-4-6"},
			"list":     []any{},
		},
		"providers": []any{
			map[string]any{
				"model_name":  "claude-sonnet-4-6",
				"provider":    "anthropic",
				"model":       "claude-sonnet-4-6",
				"api_key_ref": "ANTHROPIC_API_KEY",
			},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{
				Name:      "claude-sonnet-4-6",
				Provider:  "anthropic",
				Model:     "claude-sonnet-4-6",
				APIKeyRef: "ANTHROPIC_API_KEY",
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotEmpty(t, providers)

	found := false
	for _, p := range providers {
		if id, _ := p["id"].(string); id == "anthropic" {
			found = true
			assert.Equal(t, "disconnected", p["status"],
				"provider with no resolvable key must be 'disconnected' (FR-104)")
			break
		}
	}
	assert.True(t, found, "anthropic provider must appear in the list even when disconnected")
}

// TestHandleProviders_EnvVarKey_IsConnected verifies that a provider with a key
// available via the env var (as set by InjectFromConfig at boot) reports Connected.
//
// BDD (US-2 / AC2):
//
//	Given a provider whose api_key_ref resolves to a non-empty credential via env var
//	When GET /api/v1/providers is called
//	Then the provider is shown as "connected".
//
// Traces to: FR-104, US-2/AC2, SC-103, test dataset row: inline-key → Connected.
func TestHandleProviders_EnvVarKey_IsConnected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
	// Simulate InjectFromConfig injecting the key into the env.
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test-key-abc123")

	tmpDir := t.TempDir()
	cfgData := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "gpt-4o"},
			"list":     []any{},
		},
		"providers": []any{
			map[string]any{
				"model_name":  "gpt-4o",
				"provider":    "openrouter",
				"model":       "openai/gpt-4o",
				"api_key_ref": "OPENROUTER_API_KEY",
			},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openai/gpt-4o"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{Name: "gpt-4o", Provider: "openrouter", Model: "openai/gpt-4o", APIKeyRef: "OPENROUTER_API_KEY"},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotEmpty(t, providers)

	found := false
	for _, p := range providers {
		if id, _ := p["id"].(string); id == "openrouter" {
			found = true
			assert.Equal(t, "connected", p["status"],
				"provider with env-var key must be 'connected' (FR-104)")
			break
		}
	}
	assert.True(t, found, "openrouter provider must appear in the list when key is set")
}

// TestHandleProviders_CredStoreRef_EmptyRef_IsDisconnected verifies that a
// provider with an api_key_ref that resolves to an empty string from the cred
// store reports Disconnected.
//
// BDD (US-2 / AC1 — _ref-empty variant):
//
//	Given a provider in config whose api_key_ref resolves empty from the cred store
//	When GET /api/v1/providers is called
//	Then the provider is shown as "disconnected".
//
// Traces to: FR-104, US-2/AC1, test dataset row: _ref resolves empty → Disconnected.
func TestHandleProviders_CredStoreRef_EmptyRef_IsDisconnected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
	// Use a fixed master key so the cred store opens but has no matching entry.
	masterKey := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	t.Setenv("OMNIPUS_MASTER_KEY", masterKey)
	// Ensure the env var for the ref is not set.
	t.Setenv("GEMINI_API_KEY", "")

	tmpDir := t.TempDir()
	cfgData := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "gemini-flash"},
			"list":     []any{},
		},
		"providers": []any{
			map[string]any{
				"model_name":  "gemini-flash",
				"provider":    "gemini",
				"model":       "gemini-2.0-flash",
				"api_key_ref": "GEMINI_API_KEY",
			},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Provider: "gemini", Model: "gemini-2.0-flash"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{Name: "gemini-flash", Provider: "gemini", Model: "gemini-2.0-flash", APIKeyRef: "GEMINI_API_KEY"},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	// Wire a real cred store (unlocked via OMNIPUS_MASTER_KEY) with no matching entry.
	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))
	// Do NOT set "GEMINI_API_KEY" in the store — simulates a ref that resolves empty.
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotEmpty(t, providers)

	found := false
	for _, p := range providers {
		if id, _ := p["id"].(string); id == "gemini" {
			found = true
			assert.Equal(t, "disconnected", p["status"],
				"provider with unresolvable api_key_ref must be 'disconnected' (FR-104)")
			break
		}
	}
	assert.True(t, found, "gemini provider must appear in the list even when disconnected")
}

// TestHandleProviders_CredStoreRef_Resolved_IsConnected verifies that a
// provider with an api_key_ref that resolves to a non-empty credential from the
// cred store reports Connected.
//
// BDD (US-2 / AC2 — _ref resolves non-empty variant):
//
//	Given a provider in config whose api_key_ref resolves to a non-empty credential
//	When GET /api/v1/providers is called
//	Then the provider is shown as "connected".
//
// Traces to: FR-104, US-2/AC2, test dataset row: _ref resolves non-empty → Connected.
func TestHandleProviders_CredStoreRef_Resolved_IsConnected(t *testing.T) {
	// setupMasterKeyTempDir (no AgentLoop) — the loop below is the only one
	// created in this test, avoiding the wasted first loop that the old
	// newTestAPIWithMasterKey call would have left alive until cleanup. #351 #352
	tmpDir, _ := setupMasterKeyTempDir(t)

	// Store an API key in the credentials store.
	credRef := "ANTHROPIC_API_KEY_SPEC1"
	credStore := credentials.NewStore(tmpDir + "/credentials.json")
	require.NoError(t, credentials.Unlock(credStore))
	require.NoError(t, credStore.Set(credRef, "sk-ant-real-secret-key"))

	// Clear env var so the only resolution path is via the cred store.
	t.Setenv(credRef, "")

	// Write config.json with api_key_ref pointing to the credential.
	cfgData := map[string]any{
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "claude-haiku"},
			"list":     []any{},
		},
		"providers": []any{
			map[string]any{
				"model_name":  "claude-haiku",
				"provider":    "anthropic",
				"model":       "claude-haiku-4-5",
				"api_key_ref": credRef,
			},
		},
	}
	data, err := json.Marshal(cfgData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", data, 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Provider: "anthropic", Model: "claude-haiku-4-5"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{Name: "claude-haiku", Provider: "anthropic", Model: "claude-haiku-4-5", APIKeyRef: credRef},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		credStore:     credStore,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotEmpty(t, providers)

	found := false
	for _, p := range providers {
		if id, _ := p["id"].(string); id == "anthropic" {
			found = true
			assert.Equal(t, "connected", p["status"],
				"provider with resolved api_key_ref must be 'connected' (FR-104)")
			break
		}
	}
	assert.True(t, found, "anthropic provider must appear in the list when key resolves")
}

// --- Issue #352: No spurious restart banner on fresh install ---

// TestRestartGatedKeys_GatewayUsersNotGated verifies that gateway.users is NOT
// in RestartGatedKeys, so adding a user via onboarding does not trigger the
// restart banner.
//
// BDD (US-3 / AC1):
//
//	Given a just-completed onboarding config (wrote gateway.users + a provider)
//	When HandlePendingRestart is called
//	Then no pending-restart diff appears for gateway.users changes.
//
// Traces to: FR-105, US-3/AC1, SC-104, test dataset row: gateway.users change → no banner.
func TestRestartGatedKeys_GatewayUsersNotGated(t *testing.T) {
	// Verify the compile-time invariant: GatewayUsers must not be in the list.
	for _, key := range RestartGatedKeys {
		if key == config.GatewayUsers {
			t.Errorf("config.GatewayUsers must NOT be in RestartGatedKeys (FR-105/US-3): "+
				"gateway.users is hot (auth reads GetConfig() live); adding it caused the "+
				"fresh-install restart banner.\nFound at position in RestartGatedKeys: %v",
				RestartGatedKeys)
		}
	}
}

// TestPendingRestart_FreshOnboardingNoBanner verifies that a config produced by
// onboarding (which writes gateway.users + a provider entry) results in zero
// pending-restart entries when applied and persisted configs start equal.
//
// BDD (US-3 / AC1):
//
//	Given the applied config equals the persisted config for all restart-gated keys
//	AND onboarding writes only gateway.users and providers (neither in RestartGatedKeys)
//	When HandlePendingRestart is called
//	Then the response is an empty array (no banner).
//
// Traces to: FR-105, US-3/AC1, SC-104.
func TestPendingRestart_FreshOnboardingNoBanner(t *testing.T) {
	// Simulate the state after onboarding: config has a provider and gateway.users.
	// Neither is in RestartGatedKeys, so the diff must be empty.
	onboardedCfg := map[string]any{
		"sandbox": map[string]any{"mode": "off"},
		"gateway": map[string]any{
			"port": float64(5000),
			"users": []any{
				map[string]any{
					"username":      "admin",
					"password_hash": "$2a$10$placeholder",
					"token_hash":    "$2a$10$placeholder",
					"role":          "admin",
				},
			},
		},
		"providers": []any{
			map[string]any{
				"model_name":  "claude-sonnet-4-6",
				"provider":    "anthropic",
				"model":       "claude-sonnet-4-6",
				"api_key_ref": "ANTHROPIC_API_KEY",
			},
		},
	}

	// applied = persisted (both are the onboarded config) — no diff expected.
	api := newPendingRestartAPI(t, onboardedCfg, onboardedCfg)

	w := httptest.NewRecorder()
	r := withAdminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/config/pending-restart", nil))
	api.HandlePendingRestart(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	assert.Empty(t, diffs,
		"fresh onboarded config must produce zero pending-restart entries (FR-105/US-3/SC-104)")
}

// TestPendingRestart_GatewayPortStillGated verifies that a genuinely restart-required
// key (GatewayPort) still triggers the banner after removing GatewayUsers.
//
// BDD (US-3 / AC3):
//
//	Given hot-reload is on
//	When the gateway port (a key actually in RestartGatedKeys) is changed
//	Then the restart banner shows for that key only.
//
// Traces to: FR-106, US-3/AC3.
func TestPendingRestart_GatewayPortStillGated(t *testing.T) {
	// ADR-044 removed gateway.preview_port entirely (no more auto-derivation
	// from gateway.port to pin against) — the diff is naturally isolated to
	// gateway.port on its own now.
	applied := map[string]any{
		"gateway": map[string]any{"port": float64(5000)},
	}
	persisted := map[string]any{
		"gateway": map[string]any{"port": float64(8080)}, // port changed
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := httptest.NewRecorder()
	r := withAdminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/config/pending-restart", nil))
	api.HandlePendingRestart(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	require.Len(t, diffs, 1, "exactly one gated key changed (gateway.port)")
	assert.Equal(t, "gateway.port", diffs[0].Key,
		"gateway.port must still appear in the diff (genuinely restart-gated)")
}

// TestPendingRestart_SandboxModeStillGated verifies that sandbox.mode (a
// genuinely restart-required key) still triggers the banner.
//
// Traces to: FR-106, US-3/AC3.
func TestPendingRestart_SandboxModeStillGated(t *testing.T) {
	// gateway.port is pinned to the same value on both sides: it is a gated key
	// that lacks omitempty, so the applied config (a full struct) always
	// materializes it. Real config.json is a full serialization too, but the test's
	// persisted map is sparse — without this pin, port 0-vs-absent reads as a second
	// spurious diff. Keeping it equal isolates the assertion to sandbox.mode.
	applied := map[string]any{
		"sandbox": map[string]any{"mode": "off"},
		"gateway": map[string]any{"port": float64(5000)},
	}
	persisted := map[string]any{
		"sandbox": map[string]any{"mode": "enforce"}, // changed
		"gateway": map[string]any{"port": float64(5000)},
	}
	api := newPendingRestartAPI(t, applied, persisted)

	w := httptest.NewRecorder()
	r := withAdminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/config/pending-restart", nil))
	api.HandlePendingRestart(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	diffs := decodeDiffs(t, w.Body.Bytes())
	require.Len(t, diffs, 1, "exactly one gated key changed (sandbox.mode)")
	assert.Equal(t, "sandbox.mode", diffs[0].Key,
		"sandbox.mode must still appear in the diff (genuinely restart-gated)")
}

// TestHotReloadDefault_IsTrue verifies that the HotReload field in the default
// GatewayConfig is true, so fresh installs have hot-reload always on.
//
// BDD (US-3 / AC2):
//
//	Given hot-reload defaults on
//	When a hot-reloadable key changes
//	Then it applies without a restart banner.
//
// Traces to: FR-106, US-3/AC2.
func TestHotReloadDefault_IsTrue(t *testing.T) {
	defaults := config.DefaultConfig()
	assert.True(t, defaults.Gateway.HotReload,
		"gateway.hot_reload must default to true (FR-106) so fresh installs always have hot-reload on")
}
