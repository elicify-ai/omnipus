// Omnipus — C1 fix regression test: the O3 primary-model "provider/model"
// prefix-splitting migration is DELETED (ADR-067 FR-034). A slash inside an
// agent's Model.Primary is DATA, never a routing-protocol prefix — splitting
// on it silently rerouted models whose bare id collided with a live provider
// id to the wrong vendor (measured: 88/360 shipped OpenRouter model ids split
// into a valid pair at a DIFFERENT vendor with no error). This file used to
// assert the split; it now asserts the opposite — a slash-bearing model id
// loaded through the config's normal JSON path survives completely verbatim,
// and Provider stays empty so the pre-turn needs_provider gate (ADR-067
// T067-09) catches it instead of a silent wrong-vendor route.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentModelConfig_BareStringSlashSurvivesLoadVerbatim proves the C1 fix:
// loading a config whose agent carries the bare-string legacy form
// "anthropic/claude-sonnet-4.5" (a real, live provider id as the leading
// segment — exactly the shape that used to get silently split) leaves the
// resolved Provider EMPTY (never "anthropic") and the Primary model id
// completely intact, exercising the real read path
// (AgentModelConfig.UnmarshalJSON) rather than calling any migration
// function directly — there is no longer one to call.
//
// Mutation check: reverting the C1 fix (restoring knownProviderProtocols +
// migrateAgentPrimaryProvider and its call site in NormalizeAgentRoster)
// makes this test fail: Provider would come back "anthropic" and Primary
// would be truncated to "claude-sonnet-4.5".
func TestAgentModelConfig_BareStringSlashSurvivesLoadVerbatim(t *testing.T) {
	const wireForm = `"anthropic/claude-sonnet-4.5"`

	var mc AgentModelConfig
	require.NoError(t, json.Unmarshal([]byte(wireForm), &mc))

	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{{ID: "a", Model: &mc}},
		},
	}

	// NormalizeAgentRoster is the real second normalization point every
	// entity-loaded agent roster passes through (pkg/gateway's
	// populateAgentsListFromEntityStore bridge, cmd/omnipus's equivalent).
	NormalizeAgentRoster(cfg)

	got := cfg.Agents.List[0].Model
	require.NotNil(t, got)
	assert.NotEqual(t, "anthropic", got.Provider,
		"a bare-string model must never have its leading segment silently promoted to Provider")
	assert.Equal(t, "", got.Provider,
		"Provider must stay empty for a bare-string primary so the needs_provider gate catches it")
	assert.Equal(t, "anthropic/claude-sonnet-4.5", got.Primary,
		"the model id is DATA and must survive completely intact, slash included")
}

// TestAgentModelConfig_BareStringNoProviderCollisionSurvives proves the same
// property for a model id whose leading segment collides with a RETIRED
// protocol spelling that used to live in the deleted knownProviderProtocols
// table ("qwen-intl") — it must not be recognized as a provider at all
// anymore, retired or otherwise, because there is no table left to recognize
// it against.
func TestAgentModelConfig_BareStringNoProviderCollisionSurvives(t *testing.T) {
	var mc AgentModelConfig
	require.NoError(t, json.Unmarshal([]byte(`"qwen-intl/qwen3-max"`), &mc))

	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{{ID: "b", Model: &mc}},
		},
	}
	NormalizeAgentRoster(cfg)

	got := cfg.Agents.List[0].Model
	require.NotNil(t, got)
	assert.Equal(t, "", got.Provider)
	assert.Equal(t, "qwen-intl/qwen3-max", got.Primary)
}

// TestNormalizeAgentRoster_PrimaryProviderSplit_Idempotent proves calling
// NormalizeAgentRoster repeatedly never mutates an already-resolved primary
// model (Provider explicitly set by the operator or by NormalizeFallbacks'
// sibling logic) and never invents a split on a second pass either.
func TestNormalizeAgentRoster_PrimaryProviderSplit_Idempotent(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{ID: "a", Model: &AgentModelConfig{Primary: "openrouter/google/gemini-2.5-flash"}},
			},
		},
	}
	NormalizeAgentRoster(cfg)
	NormalizeAgentRoster(cfg)

	mc := cfg.Agents.List[0].Model
	require.NotNil(t, mc)
	assert.Equal(t, "", mc.Provider, "no migration exists to populate Provider from the slug")
	assert.Equal(t, "openrouter/google/gemini-2.5-flash", mc.Primary, "the slug must never be truncated")
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
