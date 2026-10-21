package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

type mockRegistryProvider struct{}

func (m *mockRegistryProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "mock", FinishReason: "stop"}, nil
}

func (m *mockRegistryProvider) GetDefaultModel() string {
	return "mock-model"
}

func testCfg(agents []config.AgentConfig) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              "/tmp/omnipus-test-registry",
				DefaultModel:      config.DefaultModel{Model: "gpt-4"},
				MaxTokens:         8192,
				MaxToolIterations: 10,
			},
			List: agents,
		},
	}
}

// TestNewAgentRegistry_EmptyList_NoImplicitAgent pins the removal of the
// "main" sentinel: NewAgentRegistry used to register an implicit "main"
// fallback agent regardless of cfg. That is gone — the retired sentinel was
// a shadow entity (no schema anywhere, absent from cfg.Agents.List, a 404
// from GET /api/v1/agents/main) yet it was registered at runtime and
// stamped as owner on real user data. An empty cfg.Agents.List now produces
// a registry with ZERO agents; there is nothing implicit to fall back to.
func TestNewAgentRegistry_EmptyList_NoImplicitAgent(t *testing.T) {
	cfg := testCfg(nil)
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	ids := registry.ListAgentIDs()
	if len(ids) != 0 {
		t.Errorf("expected zero agents for an empty cfg.Agents.List, got %v", ids)
	}

	if agent, ok := registry.GetAgent("main"); ok || agent != nil {
		t.Fatalf("expected 'main' to be absent (no implicit sentinel), got %+v", agent)
	}
}

func TestNewAgentRegistry_ExplicitAgents(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "sales", Name: "Sales Bot"},
		{ID: "support", Name: "Support Bot"},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	ids := registry.ListAgentIDs()
	// No "main" sentinel added anymore — exactly the 2 configured agents.
	if len(ids) != 2 {
		t.Fatalf("expected 2 agents (sales + support, no implicit sentinel), got %d: %v", len(ids), ids)
	}

	if _, hasMain := registry.GetAgent("main"); hasMain {
		t.Error("expected 'main' to be absent — there is no implicit sentinel agent anymore")
	}

	sales, ok := registry.GetAgent("sales")
	if !ok || sales == nil {
		t.Fatal("expected to find 'sales' agent")
	}
	if sales.Name != "Sales Bot" {
		t.Errorf("sales.Name = %q, want 'Sales Bot'", sales.Name)
	}

	support, ok := registry.GetAgent("support")
	if !ok || support == nil {
		t.Fatal("expected to find 'support' agent")
	}
}

// TestAgentRegistry_IsExternalCLI is the regression proof for W2's
// native-vs-3p classification (DelegateTaskState.Is3P): AgentRegistry.
// IsExternalCLI must resolve true ONLY for an agent whose own
// Subagents.Executor config resolves to runner.DispatchKindExternalCLI —
// the exact same resolution pkg/agent/subturn.go's spawnSubTurn performs via
// executorConfigOf/runner.ResolveDispatch — and false for every other case
// (native agent, unset executor, unknown agent, empty agentID).
func TestAgentRegistry_IsExternalCLI(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "native-worker", Name: "Native Worker"},
		{
			ID:   "external-worker",
			Name: "External Worker",
			Subagents: &config.SubagentsConfig{
				Executor: &config.ExecutorConfig{
					Kind:    config.ExecutorKindExternalCLI,
					CLI:     "codex",
					CLIPath: "/usr/bin/codex",
				},
			},
		},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	if registry.IsExternalCLI("native-worker") {
		t.Error("a native agent (no executor override) must resolve IsExternalCLI=false")
	}
	if !registry.IsExternalCLI("external-worker") {
		t.Error("an agent with Executor.Kind=external-cli must resolve IsExternalCLI=true")
	}
	if registry.IsExternalCLI("does-not-exist") {
		t.Error("an unknown agent ID must resolve IsExternalCLI=false, not panic or default true")
	}
	if registry.IsExternalCLI("") {
		t.Error("an empty agent ID must resolve IsExternalCLI=false — this is IsExternalCLI's own " +
			"documented limitation, NOT a real dispatch guarantee: self-delegation (TargetAgentID==\"\") " +
			"actually dispatches with the PARENT's own executor in spawnSubTurn, so a self-delegating " +
			"external-CLI parent is misclassified native here (see IsExternalCLI's doc comment)")
	}
}

func TestAgentRegistry_GetAgent_Normalize(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "my-agent"},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent, ok := registry.GetAgent("My-Agent")
	if !ok || agent == nil {
		t.Fatal("expected to find agent with normalized ID")
	}
	if agent.ID != "my-agent" {
		t.Errorf("agent.ID = %q, want 'my-agent'", agent.ID)
	}
}

func TestAgentInstance_Model(t *testing.T) {
	model := &config.AgentModelConfig{Primary: "claude-opus"}
	cfg := testCfg([]config.AgentConfig{
		{ID: "custom", Model: model},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent, _ := registry.GetAgent("custom")
	if agent.Model != "claude-opus" {
		t.Errorf("agent.Model = %q, want 'claude-opus'", agent.Model)
	}
}

func TestAgentInstance_FallbackExplicitEmpty(t *testing.T) {
	model := &config.AgentModelConfig{
		Primary:   "gpt-4",
		Fallbacks: []string{}, // explicitly empty = disable
	}
	cfg := testCfg([]config.AgentConfig{
		{ID: "no-fallback", Model: model},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent, _ := registry.GetAgent("no-fallback")
	if len(agent.Fallbacks) != 0 {
		t.Errorf("expected 0 fallbacks (explicit empty), got %d: %v", len(agent.Fallbacks), agent.Fallbacks)
	}
}

// --- ADR-054 §5: registry read-path / write-invalidation ---

// TestAgentRegistry_UpsertAgent verifies a freshly-constructed instance
// becomes visible via GetAgent/ListAgentIDs immediately, without rebuilding
// the rest of the registry (ADR-054 §5's originally-envisioned zero-rebuild
// write path). No production caller exists as of this commit — see
// UpsertAgent's own doc comment for the honest status and why full
// AgentLoop.ReloadProviderAndConfig is what production actually uses; this
// test exists to keep the primitive itself correct for whenever a future
// caller needs it.
func TestAgentRegistry_UpsertAgent(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{{ID: "alpha"}})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	if _, ok := registry.GetAgent("newagent"); ok {
		t.Fatal("newagent should not exist yet")
	}

	newCfg := &config.AgentConfig{ID: "newagent", Name: "New Agent"}
	instance := NewAgentInstance(newCfg, &cfg.Agents.Defaults, cfg, &mockRegistryProvider{})
	registry.UpsertAgent(instance)

	got, ok := registry.GetAgent("newagent")
	if !ok || got == nil {
		t.Fatal("expected newagent to be present after UpsertAgent")
	}
	if got.Name != "New Agent" {
		t.Errorf("got.Name = %q, want %q", got.Name, "New Agent")
	}
	// The pre-existing agent must be untouched (no wholesale rebuild).
	if _, ok := registry.GetAgent("alpha"); !ok {
		t.Fatal("expected pre-existing agent 'alpha' to remain registered")
	}
}

// TestAgentRegistry_UpsertAgent_ReplacesExisting verifies UpsertAgent
// replaces (not duplicates) an existing entry with the same ID.
func TestAgentRegistry_UpsertAgent_ReplacesExisting(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{{ID: "alpha", Name: "Old Name"}})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	updatedCfg := &config.AgentConfig{ID: "alpha", Name: "New Name"}
	instance := NewAgentInstance(updatedCfg, &cfg.Agents.Defaults, cfg, &mockRegistryProvider{})
	registry.UpsertAgent(instance)

	got, ok := registry.GetAgent("alpha")
	if !ok {
		t.Fatal("expected alpha to still be present")
	}
	if got.Name != "New Name" {
		t.Errorf("got.Name = %q, want %q (UpsertAgent must replace, not duplicate)", got.Name, "New Name")
	}
	// No "main" sentinel added anymore — just the one "alpha" entry.
	if len(registry.ListAgentIDs()) != 1 {
		t.Errorf("ListAgentIDs = %v, want exactly 1 (alpha, no duplicate)", registry.ListAgentIDs())
	}
}

// TestAgentRegistry_RemoveAgent verifies a removed agent disappears from
// GetAgent/ListAgentIDs, including an agent literally named "main" — the
// reserved-ID protection that used to block removing the "main" sentinel is
// gone along with the sentinel itself (registry.go's reserved-ID map and the
// RemoveAgent guard were both deleted). "main" is an ordinary agent id now,
// with no special casing anywhere in this path.
func TestAgentRegistry_RemoveAgent(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{{ID: "alpha"}, {ID: "main"}})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	if ok := registry.RemoveAgent("alpha"); !ok {
		t.Fatal("expected RemoveAgent(alpha) to report true")
	}
	if _, ok := registry.GetAgent("alpha"); ok {
		t.Fatal("expected alpha to be gone after RemoveAgent")
	}

	if ok := registry.RemoveAgent("alpha"); ok {
		t.Error("expected a second RemoveAgent(alpha) to report false (already gone)")
	}

	if ok := registry.RemoveAgent("main"); !ok {
		t.Error("expected RemoveAgent(main) to succeed — 'main' is not reserved anymore, it is an ordinary agent id")
	}
	if _, ok := registry.GetAgent("main"); ok {
		t.Fatal("'main' must be gone after RemoveAgent — no special protection survives it")
	}
}

// --- ADR-054 R3: default-agent degradation, availability not black-holing ---

// --- Concurrency: two writers cannot both become "the" default ---

// TestNormalizeAgentID_EmptyReturnsEmpty pins the removal-adjacent change to
// routing.NormalizeAgentID: it used to be paired with the "main" sentinel
// (an empty agent id would eventually resolve to "main" somewhere downstream
// in the old routing/registry code). Now NormalizeAgentID("") returns "" —
// no hardcoded agent name is ever synthesized from an absent id. Downstream,
// AgentRegistry.GetAgent("") normalizes to "" and correctly misses (there is
// no agent keyed under the empty string), which is what
// TestAgentRegistry_GetAgent_EmptyIDMisses below exercises end-to-end.
func TestNormalizeAgentID_EmptyReturnsEmpty(t *testing.T) {
	if got := routing.NormalizeAgentID(""); got != "" {
		t.Errorf("routing.NormalizeAgentID(\"\") = %q, want \"\" (must not synthesize any hardcoded agent id)", got)
	}
	if got := routing.NormalizeAgentID("   "); got != "" {
		t.Errorf("routing.NormalizeAgentID(\"   \") = %q, want \"\" (whitespace-only input)", got)
	}
}

// TestAgentRegistry_GetAgent_EmptyIDMisses proves the empty-id case actually
// misses in the registry rather than accidentally resolving to some default
// agent — the practical consequence of NormalizeAgentID("") returning "".
func TestAgentRegistry_GetAgent_EmptyIDMisses(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{{ID: "alpha"}})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	if agent, ok := registry.GetAgent(""); ok || agent != nil {
		t.Fatalf("GetAgent(\"\") = (%+v, %v), want (nil, false) — an empty id must never resolve to any agent", agent, ok)
	}
}
