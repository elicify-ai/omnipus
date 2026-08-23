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
				DefaultModel:      config.DefaultModel{Model: "openai/qwen"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test needs
			// a REAL registered agent for GetDefaultAgent() to resolve.
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{
				ModelName: "local",
				Model:     "openai/qwen",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY_LOCAL_KEY",
			},
			{
				ModelName: "deepseek",
				Model:     "openrouter/deepseek",
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
	if before.Model != "openai/qwen" {
		t.Fatalf("initial model = %q, want the default pair's model openai/qwen", before.Model)
	}
	beforeProvider := before.Provider

	old, err := al.ApplyAgentModel(id, "deepseek")
	if err != nil {
		t.Fatalf("ApplyAgentModel: %v", err)
	}
	if old != "openai/qwen" {
		t.Errorf("returned previous model = %q, want openai/qwen", old)
	}

	after, ok := al.GetRegistry().GetAgent(id)
	if !ok {
		t.Fatal("agent vanished after ApplyAgentModel")
	}
	if after != before {
		t.Error("agent instance was replaced — in-memory conversation context would be lost (#73)")
	}
	if after.Model != "deepseek" {
		t.Errorf("model after switch = %q, want deepseek", after.Model)
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
				DefaultModel:      config.DefaultModel{Model: "openai/qwen"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test needs
			// a REAL registered agent for GetDefaultAgent() to resolve.
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{ModelName: "local", Model: "openai/qwen", APIBase: "http://127.0.0.1:1", APIKeyRef: "LOOP_APPLY2_KEY"},
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
	if after.Model != "openai/qwen" {
		t.Errorf("model = %q after failed switch; want unchanged 'openai/qwen'", after.Model)
	}

	if _, err := al.ApplyAgentModel(id, "   "); err == nil {
		t.Error("expected error for empty model")
	}
	if _, err := al.ApplyAgentModel("no-such-agent", "local"); err == nil {
		t.Error("expected error for unknown agent id")
	}
}

// TestApplyAgentModel_PassthroughModel_UpdatesInMemory covers Dataset 1 row 6
// / TDD row 6 (BDD-4): the runtime MUST accept a model whose slug is not
// registered as its own provider entry, when a passthrough provider (e.g.
// openrouter) is configured. The bug being fixed (per the phase-1 spec §1.1
// item 2): the UI shows passthrough-only slugs as available, but
// ApplyAgentModel's old resolvedModelConfig rejected them, leaving the
// in-memory agent stuck on the previous model.
func TestApplyAgentModel_PassthroughModel_UpdatesInMemory(t *testing.T) {
	t.Setenv("LOOP_APPLY_PASSTHROUGH_KEY", "passthrough-key")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: "openai/qwen"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			// No "main" sentinel to fall back to anymore — this test needs
			// a REAL registered agent for GetDefaultAgent() to resolve.
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{
				ModelName: "local",
				Model:     "openai/qwen",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY_PASSTHROUGH_KEY",
			},
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "LOOP_APPLY_PASSTHROUGH_KEY",
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
	if before.Model != "openai/qwen" {
		t.Fatalf("initial model = %q, want the default pair's model openai/qwen", before.Model)
	}

	// Apply a slug that is NOT registered as its own provider entry; the
	// resolver must passthrough-route it via openrouter.
	old, err := al.ApplyAgentModel(id, "z-ai/glm-5-turbo")
	if err != nil {
		t.Fatalf("ApplyAgentModel(passthrough) returned error: %v — FR-004 violated (FIX-2)", err)
	}
	if old != "openai/qwen" {
		t.Errorf("returned previous model = %q, want openai/qwen", old)
	}

	after, ok := al.GetRegistry().GetAgent(id)
	if !ok {
		t.Fatal("agent vanished after ApplyAgentModel")
	}
	if after.Model != "z-ai/glm-5-turbo" {
		t.Errorf("agent.Model after switch = %q, want z-ai/glm-5-turbo (FR-004 in-memory update)", after.Model)
	}
	if after.Provider == nil {
		t.Fatal("agent.Provider is nil after successful switch (FR-004)")
	}
	if len(after.Candidates) == 0 {
		t.Error("agent.Candidates is empty after successful switch (FR-004)")
	}
}
