// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// wu3devLockOrderDeps wires a Deps whose config lock and agent-loop lock record the
// order they are taken in, so a test can prove the FR-085 lock order.
func wu3devLockOrderDeps(t *testing.T, cfg *config.Config) (*Deps, *[]string) {
	t.Helper()
	deps, _, _ := newProviderTestDeps(t, cfg)
	var mu sync.Mutex
	var configHeld bool
	events := &[]string{}
	deps.LockConfigWrites = func() func() {
		mu.Lock()
		configHeld = true
		*events = append(*events, "config-lock")
		mu.Unlock()
		return func() {
			mu.Lock()
			configHeld = false
			*events = append(*events, "config-unlock")
			mu.Unlock()
		}
	}
	deps.MutateConfig = func(fn func(*config.Config) error) error {
		mu.Lock()
		held := configHeld
		*events = append(*events, "loop-lock")
		mu.Unlock()
		if !held {
			t.Errorf("FR-085: the agent-loop lock was entered without the config lock held")
		}
		return fn(cfg)
	}
	return deps, events
}

func TestWU3Dev_SharedWrite_ConfigLockBeforeLoopLock(t *testing.T) {
	cfg := &config.Config{}
	deps, events := wu3devLockOrderDeps(t, cfg)
	res := NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name": "ollama", expectedRevisionParam: revisionAbsent,
	})
	require.False(t, res.IsError, "%s", res.ForLLM)
	require.Equal(t, []string{"config-lock", "loop-lock", "config-unlock"}, *events,
		"the write must hold the config lock across the agent-loop mutation and release it after")
}

func TestWU3Dev_SharedWrite_StaleRevisionRefusedNothingSaved(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{{Provider: "openai", Model: "gpt-4o"}}}
	deps, store, _ := newProviderTestDeps(t, cfg)
	planned := providerRevision(cfg, "openai")
	require.NotEqual(t, revisionAbsent, planned, "instrument: the provider exists")

	// Another writer (Settings) changes the endpoint after the plan was confirmed.
	cfg.Providers[0].APIBase = "https://proxy.example.com/v1"
	require.NotEqual(t, planned, providerRevision(cfg, "openai"), "the endpoint change must change the token")

	ref := IssueProviderReferenceForTest(t, deps, "openai", "", "sk-new-value")
	before, err := store.List()
	require.NoError(t, err)
	res := NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name": "openai", "api_key_ref": ref, expectedRevisionParam: planned,
	})
	require.True(t, res.IsError, "a stale revision must be refused: %s", res.ForLLM)
	m := unmarshalProviderResult(t, res.ForLLM)
	errBlock, ok := m["error"].(map[string]any)
	require.True(t, ok, res.ForLLM)
	require.Equal(t, "CONFLICT", errBlock["code"])
	require.Equal(t, "failed: nothing saved", m["outcome"])
	require.Contains(t, errBlock["message"], `provider "openai"`)
	after, err := store.List()
	require.NoError(t, err)
	require.Equal(t, before, after, "a refused selection must not add credential versions")
	value, err := store.Get(ref)
	require.NoError(t, err)
	require.Equal(t, "sk-new-value", value, "the prior card submission remains unused")
	require.Equal(t, "https://proxy.example.com/v1", cfg.Providers[0].APIBase, "the other writer's change stays")
	require.Empty(t, cfg.Providers[0].APIKeyRef)

	// Control: the current revision is accepted.
	res = NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{
		"name": "openai", "api_key_ref": ref, expectedRevisionParam: providerRevision(cfg, "openai"),
	})
	require.False(t, res.IsError, "%s", res.ForLLM)
}

func TestWU3Dev_SharedWrite_MissingExpectedRevisionRefused(t *testing.T) {
	cfg := &config.Config{}
	deps, _, _ := newProviderTestDeps(t, cfg)
	cases := map[string]func() string{
		"configure_provider": func() string {
			return NewProviderConfigureTool(deps).Execute(context.Background(), map[string]any{"name": "ollama"}).ForLLM
		},
		"add_mcp_server": func() string {
			return NewMCPAddTool(deps).Execute(context.Background(), map[string]any{"name": "s", "command": "x"}).ForLLM
		},
		"enable_channel": func() string {
			return NewChannelEnableTool(deps).Execute(context.Background(), map[string]any{"id": "telegram"}).ForLLM
		},
	}
	for name, run := range cases {
		out := run()
		require.Contains(t, out, "expected_revision is required", name)
		require.Contains(t, out, `"failed: nothing saved"`, name)
	}
	require.Empty(t, cfg.Providers)
	require.Empty(t, cfg.Tools.MCP.Servers)
	require.Empty(t, cfg.Channels)
}

func TestWU3Dev_SharedRevision_SecretFreeAndStable(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{{Provider: "openai", Model: "gpt-4o", APIKeyRef: "openai_API_KEY"}}}
	deps, store, _ := newProviderTestDeps(t, cfg)
	require.NoError(t, store.Set("openai_API_KEY", "sk-secret-value-123"))

	before := providerRevision(cfg, "openai")
	require.NoError(t, store.Set("openai_API_KEY", "sk-another-secret-456"))
	require.Equal(t, before, providerRevision(cfg, "openai"), "the token must not depend on the key value")

	res := NewProviderListTool(deps).Execute(context.Background(), nil)
	require.NotContains(t, res.ForLLM, "sk-another-secret-456")
	m := unmarshalProviderResult(t, res.ForLLM)
	rows, ok := m["providers"].([]any)
	require.True(t, ok, res.ForLLM)
	require.Len(t, rows, 1)
	row, ok := rows[0].(map[string]any)
	require.True(t, ok, res.ForLLM)
	require.Equal(t, before, row["revision"])
	require.Equal(t, "openai_API_KEY", row["api_key_ref"])
	endpoint, _ := row["api_base"].(string)
	require.True(t, strings.HasPrefix(endpoint, "https://"), "effective endpoint: %v", row["api_base"])
	require.NotEmpty(t, row["protocol"])

	cfg.Channels = map[string]config.ChannelInstanceConfig{"telegram": {Type: "telegram", TokenRef: "channel_telegram_token"}}
	ch := channelRevision(cfg, "telegram")
	cfg.Channels["telegram"] = config.ChannelInstanceConfig{Type: "telegram", TokenRef: "channel_telegram_token", Enabled: true}
	require.NotEqual(t, ch, channelRevision(cfg, "telegram"), "enabling the channel must change its token")
	require.Equal(t, revisionAbsent, channelRevision(cfg, "discord"))
	require.Equal(t, revisionAbsent, mcpServerRevision(cfg, "none"))
}
