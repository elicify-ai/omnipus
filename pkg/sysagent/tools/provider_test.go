// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// newProviderTestDeps builds a Deps wired to an in-memory config + an unlocked
// credential store, recording whether ReloadFunc was triggered.
func newProviderTestDeps(t *testing.T, cfg *config.Config) (*Deps, *credentials.Store, *bool) {
	t.Helper()
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("0", 64))
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, credentials.Unlock(store))
	reloaded := false
	deps := &Deps{
		GetCfg:           func() *config.Config { return cfg },
		MutateConfig:     func(fn func(*config.Config) error) error { return fn(cfg) },
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		CredStore:        store,
		ReloadFunc:       func() error { reloaded = true; return nil },
	}
	return deps, store, &reloaded
}

// unmarshalProviderResult is a package-internal helper to parse a tool result
// body as a generic map. Used only by the internal (package systools) provider tests.
func unmarshalProviderResult(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("result body is not valid JSON: %v\nbody: %s", err, body)
	}
	return m
}

// TestProviderConfigure_WiresAPIKeyRef is the core fix: storing a provider key
// must also wire the config entry's APIKeyRef (canonical <provider>_API_KEY) so
// the key is referenced, injected, and resolvable — not orphaned in the store.
func TestProviderConfigure_WiresAPIKeyRef(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openrouter", Model: "openrouter/auto"},            // seed, empty ref
		{Provider: "openrouter", Model: "z-ai/glm", Name: "z-ai/glm"}, // seed, empty ref
	}}
	deps, store, reloaded := newProviderTestDeps(t, cfg)

	res := NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name":     "openrouter",
		"api_key":  "sk-or-test",
		"api_base": "https://openrouter.ai/api/v1",
	})
	require.False(t, res.IsError, "configure should succeed: %s", res.ForLLM)

	v, err := store.Get("openrouter_API_KEY")
	require.NoError(t, err)
	require.Equal(t, "sk-or-test", v, "key stored under canonical ref")

	for _, p := range cfg.Providers {
		require.Equal(t, "openrouter_API_KEY", p.APIKeyRef, "every entry now references the key")
		require.Equal(t, "https://openrouter.ai/api/v1", p.APIBase, "api_base wired")
	}
	require.True(t, *reloaded, "reload triggered so the provider becomes live")
}

// TestProviderConfigure_ReusesExistingRef updates the key under the entry's
// existing APIKeyRef rather than stranding it behind a new ref.
func TestProviderConfigure_ReusesExistingRef(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "anthropic", Model: "anthropic/claude", APIKeyRef: "ANTHROPIC_API_KEY"},
	}}
	deps, store, _ := newProviderTestDeps(t, cfg)
	require.NoError(t, store.Set("ANTHROPIC_API_KEY", "old"))

	res := NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name": "anthropic", "api_key": "new-key",
	})
	require.False(t, res.IsError)

	v, _ := store.Get("ANTHROPIC_API_KEY")
	require.Equal(t, "new-key", v, "updated under the existing ref")
	require.Equal(t, "ANTHROPIC_API_KEY", cfg.Providers[0].APIKeyRef, "ref unchanged")
}

// TestProviderConfigure_AppendsEntryForNewProvider adds a referenced entry when
// the provider has no config entry yet.
func TestProviderConfigure_AppendsEntryForNewProvider(t *testing.T) {
	cfg := &config.Config{}
	deps, store, _ := newProviderTestDeps(t, cfg)

	res := NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name": "cohere", "api_key": "sk-co",
	})
	require.False(t, res.IsError)

	require.Len(t, cfg.Providers, 1)
	require.Equal(t, "cohere", cfg.Providers[0].Provider)
	require.Equal(t, "cohere_API_KEY", cfg.Providers[0].APIKeyRef)
	v, _ := store.Get("cohere_API_KEY")
	require.Equal(t, "sk-co", v)
}

// ---- test_provider ----

// TestProviderTest_ConfiguredKeyResolves verifies that test_provider returns "ok"
// when the provider's API key is stored in the credential store.
// This checks key resolution, NOT a live network call.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderTest_ConfiguredKeyResolves(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "anthropic", Model: "anthropic/claude-3-5-haiku", APIKeyRef: "anthropic_API_KEY"},
	}}
	deps, store, _ := newProviderTestDeps(t, cfg)
	require.NoError(t, store.Set("anthropic_API_KEY", "sk-ant-test"))

	res := NewProviderTestTool(deps).Execute(context.Background(), map[string]any{
		"name": "anthropic",
	})
	require.False(t, res.IsError, "test_provider should succeed: %s", res.ForLLM)

	m := unmarshalProviderResult(t, res.ForLLM)
	require.Equal(t, "anthropic", m["name"])
	require.Equal(t, "ok", m["status"], "expected status=ok when key resolves")
}

// TestProviderTest_NotConfigured verifies that test_provider returns status=error
// (not an IsError result) when no API key is stored.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderTest_NotConfigured(t *testing.T) {
	cfg := &config.Config{} // no providers, no keys
	deps, _, _ := newProviderTestDeps(t, cfg)

	res := NewProviderTestTool(deps).Execute(context.Background(), map[string]any{
		"name": "openai",
	})
	// test_provider returns a structured status body in both ok/error cases.
	// It must NOT set IsError — that would indicate a tool-level failure, not a
	// provider connectivity issue.
	require.False(
		t,
		res.IsError,
		"test_provider should return success envelope even when key is missing: %s",
		res.ForLLM,
	)

	m := unmarshalProviderResult(t, res.ForLLM)
	require.Equal(t, "openai", m["name"])
	require.Equal(t, "error", m["status"], "expected status=error when no key configured")
}

// TestProviderTest_LocalProviderNoKeyRequired verifies that test_provider reports
// "ok" for a local provider (e.g. ollama) without an API key.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderTest_LocalProviderNoKeyRequired(t *testing.T) {
	cfg := &config.Config{}
	deps, _, _ := newProviderTestDeps(t, cfg)

	res := NewProviderTestTool(deps).Execute(context.Background(), map[string]any{
		"name": "ollama",
	})
	require.False(t, res.IsError, "test_provider for local provider should not fail: %s", res.ForLLM)

	m := unmarshalProviderResult(t, res.ForLLM)
	// Local providers (ollama, llamacpp, lmstudio) do not require API keys —
	// the tool skips the key-resolution check for them.
	require.Equal(t, "ok", m["status"], "local provider should be ok without API key")
}

// TestProviderTest_MissingName verifies that test_provider rejects a missing name
// with an INVALID_INPUT error.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderTest_MissingName(t *testing.T) {
	cfg := &config.Config{}
	deps, _, _ := newProviderTestDeps(t, cfg)

	res := NewProviderTestTool(deps).Execute(context.Background(), map[string]any{})
	require.True(t, res.IsError, "expected IsError for missing name")

	m := unmarshalProviderResult(t, res.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	require.Equal(t, "INVALID_INPUT", errBlock["code"])
}

// TestProviderTest_Differentiation verifies that a configured provider and an
// unconfigured one return different statuses — proves the tool is not hardcoded.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderTest_Differentiation(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "groq", Model: "groq/mixtral-8x7b", APIKeyRef: "groq_API_KEY"},
	}}
	deps, store, _ := newProviderTestDeps(t, cfg)
	require.NoError(t, store.Set("groq_API_KEY", "sk-groq-test"))

	configured := NewProviderTestTool(deps).Execute(context.Background(), map[string]any{"name": "groq"})
	unconfigured := NewProviderTestTool(deps).Execute(context.Background(), map[string]any{"name": "mistral"})

	require.False(t, configured.IsError)
	require.False(t, unconfigured.IsError)

	mc := unmarshalProviderResult(t, configured.ForLLM)
	mu := unmarshalProviderResult(t, unconfigured.ForLLM)

	// The two providers must return different statuses.
	if mc["status"] == mu["status"] {
		t.Errorf("configured and unconfigured provider returned same status=%q — differentiation failed",
			mc["status"])
	}
}

// ---- list_providers ----

// TestProviderList_Empty verifies list_providers returns an empty list when no providers are configured.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderList_Empty(t *testing.T) {
	cfg := &config.Config{}
	deps, _, _ := newProviderTestDeps(t, cfg)

	res := NewProviderListTool(deps).Execute(context.Background(), nil)
	require.False(t, res.IsError, "list_providers should succeed: %s", res.ForLLM)

	m := unmarshalProviderResult(t, res.ForLLM)
	providers, _ := m["providers"].([]any)
	require.Empty(t, providers, "expected empty providers list")
}

// TestProviderList_Populated verifies list_providers returns all configured providers
// and never leaks API keys.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderList_Populated(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openrouter", Model: "openrouter/auto", APIKeyRef: "openrouter_API_KEY"},
		{Provider: "groq", Model: "groq/mixtral-8x7b", APIKeyRef: "groq_API_KEY"},
	}}
	deps, store, _ := newProviderTestDeps(t, cfg)
	require.NoError(t, store.Set("openrouter_API_KEY", "sk-or-secret"))
	require.NoError(t, store.Set("groq_API_KEY", "sk-groq-secret"))

	res := NewProviderListTool(deps).Execute(context.Background(), nil)
	require.False(t, res.IsError, "list_providers should succeed: %s", res.ForLLM)

	m := unmarshalProviderResult(t, res.ForLLM)
	providers, _ := m["providers"].([]any)
	require.Len(t, providers, 2, "expected 2 providers")

	// Build name set for assertions.
	names := map[string]bool{}
	for _, p := range providers {
		pm, _ := p.(map[string]any)
		name, _ := pm["name"].(string)
		names[name] = true
	}
	require.True(t, names["openrouter"], "openrouter not in list")
	require.True(t, names["groq"], "groq not in list")

	// Critical security check: API keys must NEVER appear in the response body.
	if strings.Contains(res.ForLLM, "sk-or-secret") || strings.Contains(res.ForLLM, "sk-groq-secret") {
		t.Error("list_providers response contains a plaintext API key — credential leak!")
	}
}

// TestProviderList_Differentiation verifies that two different provider configurations
// produce different list outputs — proves the tool is not hardcoded.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderList_Differentiation(t *testing.T) {
	cfg1 := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "anthropic", Model: "anthropic/claude-3-5-haiku"},
	}}
	deps1, _, _ := newProviderTestDeps(t, cfg1)
	res1 := NewProviderListTool(deps1).Execute(context.Background(), nil)

	cfg2 := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "anthropic", Model: "anthropic/claude-3-5-haiku"},
		{Provider: "openrouter", Model: "openrouter/auto"},
	}}
	deps2, _, _ := newProviderTestDeps(t, cfg2)
	res2 := NewProviderListTool(deps2).Execute(context.Background(), nil)

	m1 := unmarshalProviderResult(t, res1.ForLLM)
	m2 := unmarshalProviderResult(t, res2.ForLLM)
	p1, _ := m1["providers"].([]any)
	p2, _ := m2["providers"].([]any)

	if len(p1) == len(p2) {
		t.Errorf("both configs returned %d providers — differentiation test failed (expected 1 vs 2)", len(p1))
	}
}

// ---- list_models ----

// TestListModels_NoProviderConfigured verifies list_models reports a warning
// when no provider has an API key, and returns an empty models slice.
// This test avoids any live network call.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestListModels_NoProviderConfigured(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openai", Model: "openai/gpt-4o"}, // no APIKeyRef, no key
	}}
	deps, _, _ := newProviderTestDeps(t, cfg)

	res := NewModelsListTool(deps).Execute(context.Background(), nil)
	require.False(t, res.IsError, "list_models with no keys should not IsError: %s", res.ForLLM)

	m := unmarshalProviderResult(t, res.ForLLM)
	models, _ := m["models"].([]any)
	require.Empty(t, models, "expected empty models list when no keys configured")

	// A warning explaining why must be present.
	warnings, _ := m["warnings"].([]any)
	require.NotEmpty(t, warnings, "expected a warning when provider has no API key")
}

// TestListModels_ResponseShape verifies list_models response always includes the
// required fields: models, default_model, and total.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestListModels_ResponseShape(t *testing.T) {
	cfg := &config.Config{} // no providers at all
	deps, _, _ := newProviderTestDeps(t, cfg)

	res := NewModelsListTool(deps).Execute(context.Background(), nil)
	require.False(t, res.IsError)

	m := unmarshalProviderResult(t, res.ForLLM)

	// These fields must always be present in the response shape.
	if _, ok := m["models"]; !ok {
		t.Error("response missing 'models' field")
	}
	if _, ok := m["total"]; !ok {
		t.Error("response missing 'total' field")
	}
	if _, ok := m["default_model"]; !ok {
		t.Error("response missing 'default_model' field")
	}
	total, _ := m["total"].(float64)
	models, _ := m["models"].([]any)
	if int(total) != len(models) {
		t.Errorf("total=%v does not match len(models)=%d", total, len(models))
	}
}

// ---- configure_provider: api_base validation ----

// TestProviderConfigure_APIBaseIsStored verifies that configure_provider stores
// the api_base value in the config entry.
//
// INFERRED: spec §3.14 notes "api_base URL-format validation if missing" —
// this test confirms the field is persisted (format validation is a production gap).
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderConfigure_APIBaseIsStored(t *testing.T) {
	cfg := &config.Config{}
	deps, _, _ := newProviderTestDeps(t, cfg)

	res := NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name":     "openai",
		"api_key":  "sk-test",
		"api_base": "https://api.openai.com/v1",
	})
	require.False(t, res.IsError, "configure with valid api_base should succeed: %s", res.ForLLM)

	// Verify api_base was wired into the config entry.
	require.Len(t, cfg.Providers, 1)
	require.Equal(t, "https://api.openai.com/v1", cfg.Providers[0].APIBase,
		"api_base should be persisted in config entry")
}

// TestProviderConfigure_CloudProviderRequiresAPIKey verifies that configure_provider
// rejects a cloud provider call with no api_key.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.14
func TestProviderConfigure_CloudProviderRequiresAPIKey(t *testing.T) {
	cfg := &config.Config{}
	deps, _, _ := newProviderTestDeps(t, cfg)

	// anthropic is a cloud provider — api_key is required.
	res := NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name": "anthropic",
		// no api_key
	})
	require.True(t, res.IsError, "cloud provider without api_key should fail: %s", res.ForLLM)

	m := unmarshalProviderResult(t, res.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	require.Equal(t, "INVALID_INPUT", errBlock["code"])
}

// ---- configure_provider: api_base URL validation (BUG FIX) ----

// TestProviderConfigure_APIBaseValidation verifies that configure_provider validates
// the api_base parameter when non-empty: scheme-less URLs and non-http(s) schemes
// must be rejected with INVALID_INPUT; valid https URLs must be accepted; empty
// api_base must pass through (meaning "use provider default").
//
// Traces to: bug report "configure_provider stores api_base with no validation"
func TestProviderConfigure_APIBaseValidation(t *testing.T) {
	cases := []struct {
		name      string
		apiBase   string
		wantError bool
		wantCode  string
	}{
		{
			name:      "valid https URL accepted",
			apiBase:   "https://openrouter.ai/api/v1",
			wantError: false,
		},
		{
			name:      "valid http URL accepted",
			apiBase:   "http://localhost:11434/v1",
			wantError: false,
		},
		{
			name:      "empty api_base allowed (use provider default)",
			apiBase:   "",
			wantError: false,
		},
		{
			name:      "scheme-less host rejected",
			apiBase:   "api.openai.com",
			wantError: true,
			wantCode:  "INVALID_INPUT",
		},
		{
			name:      "scheme-less host with path rejected",
			apiBase:   "api.openai.com/v1",
			wantError: true,
			wantCode:  "INVALID_INPUT",
		},
		{
			name:      "ftp scheme rejected",
			apiBase:   "ftp://api.example.com/v1",
			wantError: true,
			wantCode:  "INVALID_INPUT",
		},
		{
			name:      "bare hostname without scheme rejected",
			apiBase:   "localhost:8080",
			wantError: true,
			wantCode:  "INVALID_INPUT",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			deps, _, _ := newProviderTestDeps(t, cfg)

			args := map[string]any{
				"name":    "openai",
				"api_key": "sk-test",
			}
			if tc.apiBase != "" {
				args["api_base"] = tc.apiBase
			}

			res := NewProviderConfigureTool(deps).Execute(context.Background(), args)

			if tc.wantError {
				require.True(t, res.IsError,
					"api_base=%q should be rejected; got: %s", tc.apiBase, res.ForLLM)
				m := unmarshalProviderResult(t, res.ForLLM)
				errBlock, _ := m["error"].(map[string]any)
				require.Equal(t, tc.wantCode, errBlock["code"],
					"error code must be %s for api_base=%q", tc.wantCode, tc.apiBase)
			} else {
				require.False(t, res.IsError,
					"api_base=%q should be accepted; got: %s", tc.apiBase, res.ForLLM)
			}
		})
	}
}
