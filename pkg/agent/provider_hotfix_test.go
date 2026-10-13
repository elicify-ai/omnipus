// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// Regression tests for the 2026-10-13 provider hotfix (H2, H4).

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// H2 — a bare model id that two configured providers offer is deliberately
// unresolved (FR-040 rule 2). The error must say WHY — ambiguity, naming the
// providers — not claim the model does not exist anywhere.
func TestResolveModelPairCfg_AmbiguousBareModelNamesProviders(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Provider: "openai", Model: "gpt-5.4", AuthMethod: config.AuthMethodAPIKey},
			{Provider: "openai-chatgpt", Model: "gpt-5.4", AuthMethod: config.AuthMethodSignIn},
		},
	}

	_, err := ResolveModelPairCfg(cfg, "", "gpt-5.4", "")
	require.Error(t, err, "two providers offering a bare model id is ambiguous (FR-040 rule 2)")
	assert.Equal(t,
		`model "gpt-5.4" is offered by several providers (openai, openai-chatgpt); choose the provider`,
		err.Error())

	// Control: the same model WITH its provider half resolves (rule 1), so
	// the ambiguity message is reserved for the bare form.
	mc, err := ResolveModelPairCfg(cfg, "openai-chatgpt", "gpt-5.4", "")
	require.NoError(t, err)
	assert.Equal(t, "openai-chatgpt", mc.Provider)

	// Control: a model nobody offers still says "not found".
	_, err = ResolveModelPairCfg(cfg, "", "no-such-model", "")
	require.Error(t, err)
	assert.Equal(t, `model "no-such-model" not found in model_list or providers`, err.Error())
}

// H2 — several rows of the SAME provider (multi-key load balancing) are not
// ambiguous: rule 2 counts distinct providers, and the message must never
// name one provider twice.
func TestResolveModelPairCfg_SameProviderTwiceIsNotAmbiguous(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Provider: "openai", Model: "gpt-5.4"},
			{Provider: "openai", Model: "gpt-5.4"},
		},
	}
	mc, err := ResolveModelPairCfg(cfg, "", "gpt-5.4", "")
	require.NoError(t, err)
	assert.Equal(t, "openai", mc.Provider)
}

// H4 — an agent whose executor is an external CLI (subagent_3p) runs on its
// own engine: its CLI manages its own context, so the window ladder must not
// floor-and-clamp it (the live WARN "override exceeds the model's
// capability; clamped" with capability 128000).
func TestResolveWindow_ExternalCLIWorkerIsExempt(t *testing.T) {
	installWindowTestCatalog(t, 1048576)
	override := 500000
	cfg := windowTestConfig()
	cfg.Agents.List = []config.AgentConfig{{
		ID:                    "w3p",
		Type:                  config.AgentTypeWorker,
		ContextWindowOverride: &override,
		Model:                 &config.AgentModelConfig{Primary: "some-cli-model"},
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"},
		},
	}}

	res := ResolveWindow(cfg, "", "some-cli-model", "w3p")
	assert.True(t, res.Exempt, "a subagent_3p agent's CLI manages its own context")
	assert.Equal(t, 0, res.Window)
	assert.False(t, res.Clamped)

	// Control: the same override on a NATIVE worker is still resolved (and,
	// with no catalog row for the model, floored) — the exemption is keyed
	// on the executor, not on being a worker.
	cfg.Agents.List[0].Subagents.Executor.Kind = config.ExecutorKindNative
	native := ResolveWindow(cfg, "", "some-cli-model", "w3p")
	assert.False(t, native.Exempt)
	assert.Equal(t, cloudWindowFloor, native.Window)
	assert.True(t, native.Clamped)
}

// H4 (test-analyzer I4) — RuntimeWindowPair keys the window ladder by the
// pair the agent's turns actually run on: a pinned provider is kept
// verbatim; an empty provider resolves exactly as the candidate builder
// resolves it (oracle: resolveAgentCandidatesWithPrimaryProvider, the
// function NewAgentInstance uses), including the existing runtime fallback
// to the default provider for an ambiguous bare model (issue #1292 tracks
// changing that startup behaviour).
func TestRuntimeWindowPair_MatchesTheRuntimeCandidate(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{Provider: "openrouter", Model: "z-ai/glm-5.3"},
			{Provider: "openai", Model: "gpt-5.4"},
			{Provider: "openai-chatgpt", Model: "gpt-5.4"},
		},
	}
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "openai", Model: "gpt-5.4"}

	cases := []struct {
		name, provider, model, wantProvider, wantModel string
	}{
		{"pinned provider is verbatim", "openai-chatgpt", "gpt-5.4", "openai-chatgpt", "gpt-5.4"},
		{"pinned provider never re-resolved", "zai", "z-ai/glm-5.3", "zai", "z-ai/glm-5.3"},
		{"bare model offered once resolves to its provider", "", "z-ai/glm-5.3", "openrouter", "z-ai/glm-5.3"},
		{"ambiguous bare model keeps the runtime's default-provider fallback", "", "gpt-5.4", "openai", "gpt-5.4"},
		{"empty model stays empty", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, m := RuntimeWindowPair(cfg, tc.provider, tc.model)
			assert.Equal(t, tc.wantProvider, p)
			assert.Equal(t, tc.wantModel, m)
			if tc.provider == "" && tc.model != "" {
				c := resolveAgentCandidatesWithPrimaryProvider(cfg, "openai", tc.model, "", nil, nil)
				require.NotEmpty(t, c)
				assert.Equal(t, c[0].Provider, p, "must equal the runtime's own primary candidate")
			}
		})
	}
}
