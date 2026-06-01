// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
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
		GetCfg:       func() *config.Config { return cfg },
		MutateConfig: func(fn func(*config.Config) error) error { return fn(cfg) },
		SaveConfig:   func() error { return nil },
		CredStore:    store,
		ReloadFunc:   func() error { reloaded = true; return nil },
	}
	return deps, store, &reloaded
}

// TestProviderConfigure_WiresAPIKeyRef is the core fix: storing a provider key
// must also wire the config entry's APIKeyRef (canonical <provider>_API_KEY) so
// the key is referenced, injected, and resolvable — not orphaned in the store.
func TestProviderConfigure_WiresAPIKeyRef(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openrouter", Model: "openrouter/auto"},                 // seed, empty ref
		{Provider: "openrouter", Model: "z-ai/glm", ModelName: "z-ai/glm"}, // seed, empty ref
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
