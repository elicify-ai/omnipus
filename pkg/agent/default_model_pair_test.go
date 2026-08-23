package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ADR-068 D14.1 (T068-07): an agent with no primary model of its own runs on
// agents.defaults.default_model — and routes through that pair's PROVIDER
// exactly, pinned the same way an explicit per-agent provider is pinned.
// Before this change the default was a bare alias resolved by the passthrough
// ladder, so a configured OpenRouter row could silently capture a default that
// named a different provider.

func TestResolveAgentModel_DefaultPairIsPinned(t *testing.T) {
	defaults := &config.AgentDefaults{
		DefaultModel: config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
	}

	// No per-agent model → the default pair, both halves.
	if got := resolveAgentModel(&config.AgentConfig{ID: "a"}, defaults); got != "claude-sonnet-4.6" {
		t.Fatalf("resolveAgentModel = %q, want the default pair's model", got)
	}
	if got := resolveAgentPrimaryProvider(&config.AgentConfig{ID: "a"}, defaults); got != "anthropic" {
		t.Fatalf("resolveAgentPrimaryProvider = %q, want the default pair's provider", got)
	}

	// A per-agent primary model wins on both halves (an explicit provider when
	// set, otherwise the legacy "infer it" empty string — unchanged).
	own := &config.AgentConfig{ID: "b", Model: &config.AgentModelConfig{Primary: "gpt-4o", Provider: "openai"}}
	if got := resolveAgentModel(own, defaults); got != "gpt-4o" {
		t.Fatalf("resolveAgentModel = %q, want the agent's own model", got)
	}
	if got := resolveAgentPrimaryProvider(own, defaults); got != "openai" {
		t.Fatalf("resolveAgentPrimaryProvider = %q, want the agent's own provider", got)
	}
	ownNoProvider := &config.AgentConfig{ID: "c", Model: &config.AgentModelConfig{Primary: "gpt-4o"}}
	if got := resolveAgentPrimaryProvider(ownNoProvider, defaults); got != "" {
		t.Fatalf("resolveAgentPrimaryProvider = %q, want empty (agent model without provider keeps the inferred path)", got)
	}

	// Zero pair → nothing to run on; both halves empty.
	if got := resolveAgentModel(&config.AgentConfig{ID: "d"}, &config.AgentDefaults{}); got != "" {
		t.Fatalf("resolveAgentModel with a zero default pair = %q, want empty", got)
	}
}

func TestDefaultPair_RoutesThroughItsOwnProviderNotPassthrough(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			DefaultModel: config.DefaultModel{Provider: "anthropic", Model: "claude-sonnet-4.6"},
		}},
		Providers: []*config.ModelConfig{
			{Provider: "openrouter", Model: "openrouter/auto", APIBase: "https://openrouter.ai/api/v1", APIKeyRef: "OR_KEY"},
			{Provider: "anthropic", Model: "claude-sonnet-4.6", APIBase: "https://api.anthropic.com/v1", APIKeyRef: "ANTHROPIC_KEY"},
		},
	}
	agentCfg := &config.AgentConfig{ID: "mia"}
	defaults := &cfg.Agents.Defaults

	candidates := resolveAgentCandidatesWithPrimaryProvider(
		cfg,
		defaults.DefaultModel.Provider,
		resolveAgentModel(agentCfg, defaults),
		resolveAgentPrimaryProvider(agentCfg, defaults),
		nil, nil,
	)
	if len(candidates) == 0 {
		t.Fatal("expected the default pair as the primary candidate")
	}
	if candidates[0].Provider != "anthropic" || candidates[0].Model != "claude-sonnet-4.6" {
		t.Fatalf("candidates[0] = %+v, want {anthropic claude-sonnet-4.6}: the default pair must route through its own provider, never the configured passthrough", candidates[0])
	}
}
