// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
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
	ll := newLiveLimitsForBoot(home, nil, src, nil)
	require.NotNil(t, ll)
	// FR-003: the 24 h cache lives at $OMNIPUS_HOME/cache/model_limits.json.
	// This used to be filepath.Rel over two strings the test built itself and
	// then discarded — it never touched ll or liveLimitsCacheFile, and
	// filepath.Rel of a path against its own prefix cannot fail, so the
	// assertion could not fail either. Ask the constructed rung instead.
	require.Equal(t, filepath.Join(home, "cache", "model_limits.json"), ll.CachePath(),
		"the cache must land under $OMNIPUS_HOME so a home reset and a backup cover it")
	// Installing the rung is not a fetch: a lookup for a provider with no
	// credential is skipped outright, and Wait returns at once.
	_, ok := ll.Lookup("groq", "https://api.groq.com/openai/v1", "llama-3.3-70b")
	assert.False(t, ok)
	ll.Wait()
}

// TestPrimeUnknownWindows_AsksTheRungOnlyForRefusedAgents pins FR-007 /
// US-2.AC2's boot half.
//
// Rung 4 is installed AFTER NewAgentLoop has built every instance, so boot's
// own resolutions never reach it. For a `locality: local` row the catalog
// cannot size that means WindowResolution{Unknown} — cached on the instance,
// so runTurn refuses every later turn with context_window_unknown and nothing
// ever asks the endpoint. The agent stays refused indefinitely on a fresh
// Ollama install, and the refusal points the operator at a settings control
// they should not have needed.
//
// The prime asks once for exactly the refused population — never for an agent
// whose window already resolved — so it starts no upstream request the ladder
// did not already need.
func TestPrimeUnknownWindows_AsksTheRungOnlyForRefusedAgents(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Provider: "ollama", Model: "llama3.1:8b"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				// Local row, no catalog entry, no override → Unknown.
				{ID: "local-agent", Name: "Local"},
				// A cloud row resolves to the conservative floor, never Unknown.
				{ID: "cloud-agent", Name: "Cloud", Model: &config.AgentModelConfig{
					Provider: "some-cloud", Primary: "some-model",
				}},
			},
		},
		Providers: []*config.ModelConfig{
			{Provider: "ollama", Model: "llama3.1:8b", APIBase: "http://127.0.0.1:11434"},
			{Provider: "some-cloud", Model: "some-model", APIBase: "https://api.example.com/v1"},
		},
		Context: config.DefaultContextSettings(),
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), marshalConfigForDisk(t, cfg), 0o600))
	seedAgentEntities(t, home, cfg.Agents.List)
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	registry := al.GetRegistry()
	require.NotNil(t, registry)
	localInst, ok := registry.GetAgent("local-agent")
	require.True(t, ok)
	require.True(t, localInst.WindowUnknown,
		"precondition: a local row nobody can size resolves UNKNOWN and every turn is refused")
	cloudInst, ok := registry.GetAgent("cloud-agent")
	require.True(t, ok)
	require.False(t, cloudInst.WindowUnknown, "precondition: a cloud row falls back to the floor")

	// Record which (provider, model) pairs reach rung 4.
	var mu sync.Mutex
	var asked []string
	agent.SetLiveWindowLookup(func(provider, _, model string) (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, provider+"/"+model)
		return 0, false
	})
	t.Cleanup(func() { agent.SetLiveWindowLookup(nil) })

	primeUnknownWindows(al)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, asked, "ollama/llama3.1:8b",
		"the refused local agent must reach the live rung so the endpoint's own window can be applied")
	assert.NotContains(t, asked, "some-cloud/some-model",
		"an agent whose window already resolved must not trigger an upstream request")
}
