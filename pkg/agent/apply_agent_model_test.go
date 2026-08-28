package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestApplyAgentModel_SwitchesInPlacePreservingInstance guards #73: a model
// change must update the LIVE agent instance (model + provider + candidates)
// without replacing the instance, so the in-memory conversation context is
// preserved and the change takes effect on the next turn — no hot-reload, no
// WebSocket drop.
func TestApplyAgentModel_SwitchesInPlacePreservingInstance(t *testing.T) {
	t.Setenv("LOOP_APPLY_LOCAL_KEY", "local-key")
	t.Setenv("LOOP_APPLY_REMOTE_KEY", "remote-key")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Provider: "openai", Model: "gpt-4.1"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test needs
			// a REAL registered agent for GetDefaultAgent() to resolve.
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{
				Provider:  "openai",
				Model:     "gpt-4.1",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY_LOCAL_KEY",
			},
			{
				Provider:  "deepseek",
				Model:     "deepseek-chat",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY_REMOTE_KEY",
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)

	before := al.GetRegistry().GetDefaultAgent()
	if before == nil {
		t.Fatal("no default agent")
	}
	id := before.ID
	if before.Model != "gpt-4.1" {
		t.Fatalf("initial model = %q, want the default pair's model gpt-4.1", before.Model)
	}
	beforeProvider := before.Provider

	old, err := al.ApplyAgentModel(id, "deepseek-chat")
	if err != nil {
		t.Fatalf("ApplyAgentModel: %v", err)
	}
	if old != "gpt-4.1" {
		t.Errorf("returned previous model = %q, want gpt-4.1", old)
	}

	after, ok := al.GetRegistry().GetAgent(id)
	if !ok {
		t.Fatal("agent vanished after ApplyAgentModel")
	}
	if after != before {
		t.Error("agent instance was replaced — in-memory conversation context would be lost (#73)")
	}
	if after.Model != "deepseek-chat" {
		t.Errorf("model after switch = %q, want deepseek-chat", after.Model)
	}
	if after.Provider == beforeProvider {
		t.Error("provider was not switched to the new model's provider")
	}
}

// TestApplyAgentModel_UnknownModelRejectedNoMutation confirms an invalid model
// is rejected and leaves the instance untouched (no half-applied state).
func TestApplyAgentModel_UnknownModelRejectedNoMutation(t *testing.T) {
	t.Setenv("LOOP_APPLY2_KEY", "k")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Provider: "openai", Model: "gpt-4.1"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test needs
			// a REAL registered agent for GetDefaultAgent() to resolve.
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{Provider: "openai", Model: "gpt-4.1", APIBase: "http://127.0.0.1:1", APIKeyRef: "LOOP_APPLY2_KEY"},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	defAgent := al.GetRegistry().GetDefaultAgent()
	if defAgent == nil {
		t.Fatal("no default agent")
	}
	id := defAgent.ID

	if _, err := al.ApplyAgentModel(id, "does-not-exist"); err == nil {
		t.Fatal("expected error for unknown model, got nil")
	}
	after, _ := al.GetRegistry().GetAgent(id)
	if after.Model != "gpt-4.1" {
		t.Errorf("model = %q after failed switch; want it unchanged at gpt-4.1", after.Model)
	}

	if _, err := al.ApplyAgentModel(id, "   "); err == nil {
		t.Error("expected error for empty model")
	}
	if _, err := al.ApplyAgentModel("no-such-agent", "gpt-4.1"); err == nil {
		t.Error("expected error for unknown agent id")
	}
}

// TestApplyAgentModel_ModelOfferedByAnotherConfiguredProvider — the
// successor to TestApplyAgentModel_PassthroughModel_UpdatesInMemory.
//
// That test covered "a slug that is not its own provider row still applies,
// because a passthrough aggregator accepts anything". ADR-067 FR-040 deleted
// the passthrough rung: an unmatched id no longer becomes an OpenRouter
// request by default. The legitimate half of the behaviour survives and is
// what this asserts — a model a CONFIGURED provider actually OFFERS applies
// even when no dedicated row names it, so the composer's picks still work.
func TestApplyAgentModel_ModelOfferedByAnotherConfiguredProvider(t *testing.T) {
	t.Setenv("LOOP_APPLY_OFFERED_KEY", "offered-key")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Provider: "openai", Model: "gpt-4.1"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{
				Provider:  "openai",
				Model:     "gpt-4.1",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY_OFFERED_KEY",
			},
			{
				Provider:  "anthropic",
				Model:     "claude-haiku-4-5",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY_OFFERED_KEY",
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)

	before := al.GetRegistry().GetDefaultAgent()
	if before == nil {
		t.Fatal("no default agent")
	}
	id := before.ID
	if before.Model != "gpt-4.1" {
		t.Fatalf("initial model = %q, want the default pair's model gpt-4.1", before.Model)
	}

	// `claude-opus-4-5` has no row of its own, but the configured anthropic
	// provider offers it — FR-040 rule 1b/2.
	old, err := al.ApplyAgentModel(id, "claude-opus-4-5")
	if err != nil {
		t.Fatalf("ApplyAgentModel(offered model) returned error: %v", err)
	}
	if old != "gpt-4.1" {
		t.Errorf("returned previous model = %q, want gpt-4.1", old)
	}

	after, ok := al.GetRegistry().GetAgent(id)
	if !ok {
		t.Fatal("agent vanished after ApplyAgentModel")
	}
	if after.Model != "claude-opus-4-5" {
		t.Errorf("agent.Model after switch = %q, want claude-opus-4-5", after.Model)
	}
	if after.Provider == nil {
		t.Fatal("agent.Provider is nil after a successful switch")
	}
	if len(after.Candidates) == 0 {
		t.Error("agent.Candidates is empty after a successful switch")
	}

	// And a model NOTHING offers must still be refused — the passthrough
	// fallback that used to accept it is gone.
	if _, err := al.ApplyAgentModel(id, "z-ai/glm-5-turbo"); err == nil {
		t.Error("a model no configured provider offers must not apply")
	}
}
