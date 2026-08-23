// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

type staticConfigSource struct{ cfg *config.Config }

func (s staticConfigSource) GetConfig() *config.Config { return s.cfg }

// TestLiveLimitsBoot_ProviderCredential — T066-10: the rung's credential
// comes from the provider's api_key_ref (env-injected at boot, else the
// store); no row / no ref → "" so the rung is skipped for that provider.
func TestLiveLimitsBoot_ProviderCredential(t *testing.T) {
	t.Setenv("T066_10_TEST_KEY", "sk-live-abc")
	cfg := &config.Config{Providers: []*config.ModelConfig{
		{Provider: "openrouter"}, // no ref at all
		{Provider: "anthropic", APIKeyRef: "T066_10_TEST_KEY"},
		{Provider: "groq", APIKeyRef: "T066_10_MISSING_REF"},
	}}
	src := staticConfigSource{cfg: cfg}

	assert.Equal(t, "sk-live-abc", providerCredential(src, nil, "anthropic"), "env-injected ref resolves")
	assert.Equal(t, "", providerCredential(src, nil, "openrouter"), "no ref → no credential")
	assert.Equal(t, "", providerCredential(src, nil, "groq"), "unresolvable ref with no store → no credential")
	assert.Equal(t, "", providerCredential(src, nil, "unknown-provider"))
	assert.Equal(t, "", providerCredential(nil, nil, "anthropic"))
	assert.Equal(t, "", providerCredential(staticConfigSource{}, nil, "anthropic"))

	home := t.TempDir()
	ll := newLiveLimitsForBoot(home, nil, src)
	require.NotNil(t, ll)
	_, err := filepath.Rel(home, filepath.Join(home, "cache", "model_limits.json"))
	require.NoError(t, err)
	// Installing the rung is not a fetch: a lookup for a provider with no
	// credential is skipped outright, and Wait returns at once.
	_, ok := ll.Lookup("groq", "https://api.groq.com/openai/v1", "llama-3.3-70b")
	assert.False(t, ok)
	ll.Wait()
}
