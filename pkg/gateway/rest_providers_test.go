// rest_providers_test.go: tests for lLM provider catalog, keys, and dependents

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// TestHandleProviders_CorruptedCredential_IsErrorStatus pins Task 3: when a
// provider's api_key_ref is present in config but the credential store
// cannot authenticate it (corrupted entry / wrong master key — worse than "not
// configured"), GET /api/v1/providers must report status=error with a
// remediation message naming that credential and distinguishing it from a
// provider that was simply never configured (which remains
// status=disconnected — see
// TestHandleProviders_CredStoreRef_EmptyRef_IsDisconnected in
// s1_befixes_test.go).
func TestHandleProviders_CorruptedCredential_IsErrorStatus(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_KEY_FILE", "")
	t.Setenv("OMNIPUS_MASTER_KEY", fixedHexKey)
	// Ensure the env-var resolution path (ModelConfig.APIKey) is empty so the
	// only resolution path exercised is the credential store.
	t.Setenv("CORRUPT_PROVIDER_KEY", "")

	tmpDir := t.TempDir()
	credsPath := filepath.Join(tmpDir, "credentials.json")
	writeCorruptedCredentialsFile(t, credsPath, "CORRUPT_PROVIDER_KEY")
	credStore := credentials.NewStore(credsPath)
	require.NoError(t, credentials.Unlock(credStore))

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
				"api_key_ref": "CORRUPT_PROVIDER_KEY",
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
				Home: tmpDir, DefaultModel: config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4-6"}, MaxTokens: 4096},
		},
		Providers: []*config.ModelConfig{
			{
				Name:      "claude-sonnet-4-6",
				Provider:  "anthropic",
				Model:     "claude-sonnet-4-6",
				APIKeyRef: "CORRUPT_PROVIDER_KEY",
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
		credStore:     credStore,
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, isolateRateLimit(t, r))

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotEmpty(t, providers)

	found := false
	for _, p := range providers {
		if id, _ := p["id"].(string); id == "anthropic" {
			found = true
			assert.Equal(t, "error", p["status"],
				"a locked/undecryptable vault must report status=error, distinguishable from never-configured (Task 3)")
			errMsg, _ := p["error"].(string)
			// A present-but-unauthenticating entry now gets the entry-naming
			// remediation rather than the generic "vault could not be read":
			// the vault IS readable, the bytes under that name are the problem,
			// so "unlock and retry" would be the wrong advice.
			assert.Contains(t, errMsg, "CORRUPT_PROVIDER_KEY",
				"the remediation must name the credential at fault")
			assert.Contains(t, errMsg, "re-enter")
			break
		}
	}
	assert.True(t, found, "anthropic provider must appear in the list even when its credential is corrupted")
}

// TestHandleProvidersGET verifies GET /api/v1/providers returns 200 with an array.
// BDD: Given the gateway is running with no configured provider,
// When GET /api/v1/providers is called,
// Then 200 with an EMPTY array — ADR-068 FR-011a (T068-04) removed the
// synthetic "default" filler row along with the template rows; the body is
// `[]`, never `null` (Provider.yaml requires an array).
// Traces to: wave5b-system-agent-spec.md — E4: providers endpoint
func TestHandleProvidersGET(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil)
	r.URL.Path = "/api/v1/providers"
	api.HandleProviders(w, isolateRateLimit(t, r))

	require.Equal(t, http.StatusOK, w.Code)
	var providers []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &providers))
	require.NotNil(t, providers, "body must be a JSON array, not null")
	assert.Empty(t, providers, "no configured provider → no rows (no synthetic default filler)")
}

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
	api.HandleProviders(w, isolateRateLimit(t, r))

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
	api.HandleProviders(w, isolateRateLimit(t, r))

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
	api.HandleProviders(w, isolateRateLimit(t, r))

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
	api.HandleProviders(w, isolateRateLimit(t, r))

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
