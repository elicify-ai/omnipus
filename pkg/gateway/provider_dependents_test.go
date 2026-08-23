// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-068 T068-08 — TDD row 3: TestProviderDependents +
// TestProviderDependents_EnumeratesEveryModelField.
//
// Spec: adr-068-providers-ux-spec.md FR-012 (MAJ-010 dependents definition),
// Dataset "Dependents computation" rows 1-12; BDD "Dependents are listed and
// left without a model", "Fallback references are removed and listed",
// "Passthrough-resolved agents are dependents".

package gateway

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// depCfg builds a config with the given configured provider rows and agents.
func depCfg(providers []*config.ModelConfig, agents ...config.AgentConfig) *config.Config {
	return &config.Config{
		Providers: providers,
		Agents: config.AgentsConfig{
			List: agents,
		},
	}
}

func openrouterRow() *config.ModelConfig {
	return &config.ModelConfig{
		Provider: "openrouter", Model: "openrouter/auto", ModelName: "openrouter",
	}
}

func anthropicRow() *config.ModelConfig {
	return &config.ModelConfig{
		Provider: "anthropic", Model: "claude-sonnet-4.6", ModelName: "anthropic",
	}
}

func agentWith(id, name, model, provider string) config.AgentConfig {
	ac := config.AgentConfig{ID: id, Name: name}
	if model != "" || provider != "" {
		ac.Model = &config.AgentModelConfig{Primary: model, Provider: provider}
	}
	return ac
}

func TestProviderDependents(t *testing.T) {
	t.Run("row 1: no reference → empty, non-nil", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow()},
			agentWith("mia", "Mia", "claude-sonnet-4.6", "anthropic"))
		deps := computeProviderDependents(cfg, "openrouter")
		require.NotNil(t, deps, "Provider.yaml requires dependents: array — [] not null")
		assert.Empty(t, deps)
	})

	t.Run("row 2: one explicit primary → role primary", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow()},
			agentWith("ava", "Ava", "google/gemini-2.5-flash", "openrouter"))
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependent{Id: "ava", Name: "Ava",
			Role: gen.ProviderDependentRolePrimary}, deps[0])
	})

	t.Run("row 3: primary + same-agent fallback → one entry, role primary", func(t *testing.T) {
		ac := agentWith("ava", "Ava", "google/gemini-2.5-flash", "openrouter")
		ac.FallbackModels = config.FallbackModelSlice{
			{Model: "z-ai/glm-5.2", Provider: "openrouter"},
		}
		cfg := depCfg([]*config.ModelConfig{openrouterRow()}, ac)
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1, "same agent must be de-duplicated")
		assert.Equal(t, gen.ProviderDependentRolePrimary, deps[0].Role)
	})

	t.Run("row 4: fallback only → role fallback", func(t *testing.T) {
		ac := agentWith("jim", "Jim", "claude-sonnet-4.6", "anthropic")
		ac.FallbackModels = config.FallbackModelSlice{
			{Model: "z-ai/glm-5.2", Provider: "openrouter"},
		}
		cfg := depCfg([]*config.ModelConfig{openrouterRow(), anthropicRow()}, ac)
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependent{Id: "jim", Name: "Jim",
			Role: gen.ProviderDependentRoleFallback}, deps[0])
	})

	t.Run("row 5: locked core agent primary → listed, no exemption", func(t *testing.T) {
		ac := agentWith("mia", "Mia", "z-ai/glm-5.2", "openrouter")
		ac.Locked = true
		ac.Type = config.AgentTypeCore
		cfg := depCfg([]*config.ModelConfig{openrouterRow()}, ac)
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, "mia", deps[0].Id)
	})

	t.Run("row 6: 50 agents → 50 entries, names sorted", func(t *testing.T) {
		agents := make([]config.AgentConfig, 0, 50)
		for i := 0; i < 50; i++ {
			// Names deliberately inserted in reverse order.
			agents = append(agents, agentWith(
				fmt.Sprintf("a%02d", i), fmt.Sprintf("Agent %02d", 49-i),
				"z-ai/glm-5.2", "openrouter"))
		}
		cfg := depCfg([]*config.ModelConfig{openrouterRow()}, agents...)
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 50)
		for i := 1; i < len(deps); i++ {
			assert.LessOrEqual(t, deps[i-1].Name, deps[i].Name,
				"dependents must be sorted by name")
		}
	})

	t.Run("row 7: provider empty, slug exact-matches the provider's row Model → primary", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow()},
			agentWith("ray", "Ray", "openrouter/auto", ""))
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependentRolePrimary, deps[0].Role,
			"exact row-slug match is a primary dependence (CRIT-001 exact lookup)")
	})

	t.Run("row 8: provider empty, slug unmatched, openrouter configured → passthrough", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow()},
			agentWith("ray", "Ray", "google/gemini-2.5-flash", ""))
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependent{Id: "ray", Name: "Ray",
			Role: gen.ProviderDependentRolePassthrough}, deps[0],
			"resolveFallbackProvider rule 3 resolution is a passthrough dependence")
	})

	t.Run("row 9: recap_fallback_models names the provider → role recap", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow()})
		cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{
			{Model: "z-ai/glm-5.2", Provider: "openrouter"},
		}
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependent{
			Id: "agents.defaults.recap_fallback_models", Name: "Recap fallback models",
			Role: gen.ProviderDependentRoleRecap}, deps[0])
	})

	t.Run("row 10: image_model on the provider → role image", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow()})
		cfg.Agents.Defaults.ImageModel = "openrouter/auto" // exact row match
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependent{
			Id: "agents.defaults.image_model", Name: "Image model",
			Role: gen.ProviderDependentRoleImage}, deps[0])
	})

	t.Run("row 11: voice.model_name on the provider → role voice", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow()})
		cfg.Voice.ModelName = "openrouter/auto"
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependent{
			Id: "voice.model_name", Name: "Voice model",
			Role: gen.ProviderDependentRoleVoice}, deps[0])
	})

	t.Run("row 12: provider is the default_model provider → backs_default", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{openrouterRow(), anthropicRow()})
		cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
			Provider: "openrouter", Model: "z-ai/glm-5.2",
		}
		assert.True(t, providerBacksDefault(cfg, "openrouter"))
		assert.False(t, providerBacksDefault(cfg, "anthropic"))
		// The default pair is carried by backs_default, not a dependents row
		// (the role enum has no value for it).
		assert.Empty(t, computeProviderDependents(cfg, "openrouter"))
	})

	t.Run("recap_model bare slug resolves via passthrough → still role recap", func(t *testing.T) {
		// Settings references keep their semantic role regardless of how the
		// slug resolved (BDD "Passthrough-resolved agents are dependents").
		cfg := depCfg([]*config.ModelConfig{openrouterRow()})
		cfg.Agents.Defaults.RecapModel = "google/gemini-2.5-flash"
		deps := computeProviderDependents(cfg, "openrouter")
		require.Len(t, deps, 1)
		assert.Equal(t, gen.ProviderDependentRoleRecap, deps[0].Role)
		assert.Equal(t, "agents.defaults.recap_model", deps[0].Id)
	})

	t.Run("explicit foreign provider pins routing — no dependence on the slug's row", func(t *testing.T) {
		// An agent explicitly pinned to anthropic is NOT an openrouter
		// dependent even when its slug happens to match an openrouter row.
		cfg := depCfg([]*config.ModelConfig{openrouterRow(), anthropicRow()},
			agentWith("ava", "Ava", "openrouter/auto", "anthropic"))
		assert.Empty(t, computeProviderDependents(cfg, "openrouter"))
	})
}

// TestProviderDependents_WireEmission proves the GET /providers rows carry
// the T068-08 fields end-to-end: dependents[], backs_default, updated_at
// (MAJ-015), auth_method (api_key until T068-14), and that account_label
// stays absent.
func TestProviderDependents_WireEmission(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	stamp := time.Date(2026, 8, 22, 10, 15, 0, 0, time.UTC)
	seedTemplateProviders(t, api, &config.ModelConfig{
		ModelName: "openrouter", Provider: "openrouter", Model: "openrouter/auto",
		Models: []string{"z-ai/glm-5.2"}, UpdatedAt: &stamp,
	})
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List,
		config.AgentConfig{ID: "ava", Name: "Ava", Model: &config.AgentModelConfig{
			Primary: "z-ai/glm-5.2", Provider: "openrouter"}})
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
		Provider: "openrouter", Model: "z-ai/glm-5.2",
	}

	provs := getProviders(t, api)
	require.Len(t, provs, 1, "got %+v", provs)
	row := provs[0]
	assert.Equal(t, "openrouter", row.Id)
	require.Len(t, row.Dependents, 1)
	assert.Equal(t, gen.ProviderDependent{Id: "ava", Name: "Ava",
		Role: gen.ProviderDependentRolePrimary}, row.Dependents[0])
	assert.True(t, row.BacksDefault)
	require.NotNil(t, row.UpdatedAt, "updated_at must echo the row's PUT stamp (MAJ-015)")
	assert.True(t, stamp.Equal(*row.UpdatedAt))
	assert.Equal(t, gen.ProviderAuthMethodApiKey, row.AuthMethod)
	assert.Nil(t, row.AccountLabel, "account_label stays absent until T068-14")
}

// TestProviderDependents_EnumeratesEveryModelField — TDD row 3, second test.
//
// The dependents definition (MAJ-010) is a closed enumeration over the
// model-reference sites in config.AgentDefaults and config.VoiceConfig. This
// test reflects over both structs and fails when a field whose name contains
// "Model" appears that the computation does not knowingly cover — forcing
// whoever adds a new model-reference site to extend
// computeProviderDependents (or consciously exclude the field here).
func TestProviderDependents_EnumeratesEveryModelField(t *testing.T) {
	// Every *Model* field, mapped to how the dependents computation covers it.
	covered := map[string]string{
		// AgentDefaults
		"DefaultModel":        "providerBacksDefault (backs_default; DELETE requires new_default)",
		"ModelFallbacks":      "settings row agents.defaults.model_fallbacks, role fallback",
		"ImageModel":          "settings row agents.defaults.image_model, role image",
		"ImageModelFallbacks": "settings row agents.defaults.image_model_fallbacks, role image",
		"RecapModel":          "settings row agents.defaults.recap_model, role recap",
		"RecapFallbackModels": "settings row agents.defaults.recap_fallback_models, role recap",
		// VoiceConfig
		"ModelName": "settings row voice.model_name, role voice",
	}
	seen := map[string]bool{}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(config.AgentDefaults{}),
		reflect.TypeOf(config.VoiceConfig{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if !strings.Contains(name, "Model") {
				continue
			}
			seen[name] = true
			if _, ok := covered[name]; !ok {
				t.Errorf("%s.%s is a *Model* config field with no dependents-computation row "+
					"(ADR-068 MAJ-010): extend computeProviderDependents in "+
					"provider_dependents.go, then list the field here", typ.Name(), name)
			}
		}
	}
	for name := range covered {
		assert.True(t, seen[name],
			"covered map lists %s but no such *Model* field exists any more — prune both the map entry and (if present) its computation row", name)
	}
}

// TestProviderDependents_NeedsModelDerivation — unit coverage of the FR-014
// rule the agent handlers apply (the HTTP round-trip lives in
// rest_agent_provider_test.go::TestAgentProvider_NeedsModelDerived).
func TestProviderDependents_NeedsModelDerivation(t *testing.T) {
	or := openrouterRow()

	t.Run("no model anywhere → true", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{or})
		ac := agentWith("a", "A", "", "")
		assert.True(t, agentNeedsModel(cfg, &ac))
	})

	t.Run("own pair on a configured provider → false", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{or})
		ac := agentWith("a", "A", "z-ai/glm-5.2", "openrouter")
		assert.False(t, agentNeedsModel(cfg, &ac))
	})

	t.Run("own model, explicit provider without a configured row → true", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{or})
		ac := agentWith("a", "A", "claude-sonnet-4.6", "anthropic")
		assert.True(t, agentNeedsModel(cfg, &ac))
	})

	t.Run("own model, empty provider, passthrough configured → false", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{or})
		ac := agentWith("a", "A", "google/gemini-2.5-flash", "")
		assert.False(t, agentNeedsModel(cfg, &ac))
	})

	t.Run("no own model, default pair on a configured provider → false", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{or})
		cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
			Provider: "openrouter", Model: "z-ai/glm-5.2"}
		ac := agentWith("a", "A", "", "")
		assert.False(t, agentNeedsModel(cfg, &ac))
	})

	t.Run("no own model, default pair on a vanished provider → true", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{or})
		cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
			Provider: "groq", Model: "llama-3.3-70b"}
		ac := agentWith("a", "A", "", "")
		assert.True(t, agentNeedsModel(cfg, &ac))
	})

	t.Run("own model, empty provider, nothing can serve the slug → true", func(t *testing.T) {
		cfg := depCfg([]*config.ModelConfig{anthropicRow()}) // no passthrough row
		ac := agentWith("a", "A", "google/gemini-2.5-flash", "")
		assert.True(t, agentNeedsModel(cfg, &ac))
	})
}
