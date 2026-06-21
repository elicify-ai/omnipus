// Omnipus — O3 two-field model (primary {model, provider}) migration + round-trip.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrateAgentPrimaryProvider_SplitsCombinedSlug proves the O3 config-load
// migration splits an existing combined primary slug into {primary, provider}.
func TestMigrateAgentPrimaryProvider_SplitsCombinedSlug(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "a", Model: &AgentModelConfig{Primary: "openrouter/google/gemini-2.5-flash"}},
			},
		},
	}
	migrateAgentPrimaryProvider(cfg)

	mc := cfg.Agents.List[0].Model
	require.NotNil(t, mc)
	assert.Equal(t, "openrouter", mc.Provider, "leading provider protocol must move to Provider")
	assert.Equal(t, "google/gemini-2.5-flash", mc.Primary, "remaining slug must stay in Primary")
}

// TestMigrateAgentPrimaryProvider_Idempotent proves a second pass is a no-op and
// that an already-split entry (Provider set) is left untouched.
func TestMigrateAgentPrimaryProvider_Idempotent(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "a", Model: &AgentModelConfig{Primary: "google/gemini-2.5-flash", Provider: "openrouter"}},
			},
		},
	}
	migrateAgentPrimaryProvider(cfg)
	migrateAgentPrimaryProvider(cfg)
	mc := cfg.Agents.List[0].Model
	assert.Equal(t, "openrouter", mc.Provider)
	assert.Equal(t, "google/gemini-2.5-flash", mc.Primary)
}

// TestMigrateAgentPrimaryProvider_LeavesUnknownPrefix proves a non-provider
// leading segment (a model vendor like "google" used without a configured
// provider protocol of that exact name is still in the protocol set, so use a
// genuinely-unknown prefix) is NOT split.
func TestMigrateAgentPrimaryProvider_LeavesUnknownPrefix(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "a", Model: &AgentModelConfig{Primary: "acme-vendor/some-model"}},
				{ID: "b", Model: &AgentModelConfig{Primary: "gpt-4o"}}, // no slash
				{ID: "c"}, // nil model
			},
		},
	}
	migrateAgentPrimaryProvider(cfg)
	assert.Equal(t, "", cfg.Agents.List[0].Model.Provider, "unknown vendor prefix must not be split")
	assert.Equal(t, "acme-vendor/some-model", cfg.Agents.List[0].Model.Primary)
	assert.Equal(t, "", cfg.Agents.List[1].Model.Provider, "bare slug must not gain a provider")
	assert.Nil(t, cfg.Agents.List[2].Model)
}

// TestAgentModelConfig_MarshalRoundTrip proves the provider field round-trips
// through JSON and forces the object form once a provider is present.
func TestAgentModelConfig_MarshalRoundTrip(t *testing.T) {
	// Bare primary, no provider → emits a bare string (legacy shape preserved).
	bare := AgentModelConfig{Primary: "gpt-4o"}
	b, err := json.Marshal(bare)
	require.NoError(t, err)
	assert.JSONEq(t, `"gpt-4o"`, string(b))

	// With provider → object form including provider.
	withProv := AgentModelConfig{Primary: "google/gemini-2.5-flash", Provider: "openrouter"}
	b, err = json.Marshal(withProv)
	require.NoError(t, err)
	assert.JSONEq(t, `{"primary":"google/gemini-2.5-flash","provider":"openrouter"}`, string(b))

	// Round-trip back.
	var got AgentModelConfig
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, "google/gemini-2.5-flash", got.Primary)
	assert.Equal(t, "openrouter", got.Provider)

	// Legacy bare-string unmarshal still works and leaves provider empty.
	var legacy AgentModelConfig
	require.NoError(t, json.Unmarshal([]byte(`"claude-sonnet-4.6"`), &legacy))
	assert.Equal(t, "claude-sonnet-4.6", legacy.Primary)
	assert.Equal(t, "", legacy.Provider)
}

// TestMigrateGlobalHeartbeatToAgent_AppliesToDefault proves the O6 config-load
// migration moves the legacy global heartbeat onto the DEFAULT Main agent and
// leaves workers heartbeat-less.
func TestMigrateGlobalHeartbeatToAgent_AppliesToDefault(t *testing.T) {
	cfg := &Config{
		Heartbeat: HeartbeatConfig{Enabled: true, Interval: 20},
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "mia", Type: AgentTypeCore, Default: true},
				{ID: "worker", Type: AgentTypeWorker, Default: false},
			},
		},
	}
	migrateGlobalHeartbeatToAgent(cfg)

	mia := cfg.Agents.List[0]
	require.NotNil(t, mia.HeartbeatEnabled)
	assert.True(t, *mia.HeartbeatEnabled, "global enabled must migrate to default agent")
	assert.Equal(t, 20, mia.HeartbeatInterval)
	assert.Nil(t, cfg.Agents.List[1].HeartbeatEnabled, "worker must remain heartbeat-less")
}

// TestMigrateGlobalHeartbeatToAgent_NoopWhenGlobalOff proves the migration is a
// no-op when the global heartbeat is disabled with no interval (fresh install).
func TestMigrateGlobalHeartbeatToAgent_NoopWhenGlobalOff(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{{ID: "mia", Type: AgentTypeCore, Default: true}},
		},
	}
	migrateGlobalHeartbeatToAgent(cfg)
	assert.Nil(t, cfg.Agents.List[0].HeartbeatEnabled, "no migration when global heartbeat is off")
}

// TestMigrateGlobalHeartbeatToAgent_DoesNotClobberPerAgent proves an agent that
// already set its own heartbeat is left untouched (idempotent / operator wins).
func TestMigrateGlobalHeartbeatToAgent_DoesNotClobberPerAgent(t *testing.T) {
	off := false
	cfg := &Config{
		Heartbeat: HeartbeatConfig{Enabled: true, Interval: 20},
		Agents: AgentsConfig{
			List: []AgentConfig{{ID: "mia", Type: AgentTypeCore, Default: true, HeartbeatEnabled: &off}},
		},
	}
	migrateGlobalHeartbeatToAgent(cfg)
	require.NotNil(t, cfg.Agents.List[0].HeartbeatEnabled)
	assert.False(t, *cfg.Agents.List[0].HeartbeatEnabled, "operator's explicit per-agent value must survive")
}
