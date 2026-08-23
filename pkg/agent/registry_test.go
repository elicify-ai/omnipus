package agent

import (
	"context"
	"sync"
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

	if def := registry.GetDefaultAgent(); def != nil {
		t.Fatalf("GetDefaultAgent must return nil on an empty registry, got %+v", def)
	}
}

func TestNewAgentRegistry_ExplicitAgents(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "sales", Default: true, Name: "Sales Bot"},
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
		{ID: "my-agent", Default: true},
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

// TestAgentRegistry_GetDefaultAgent_EntityDefaultFlagIsInert is the ADR-054
// D6.4 regression guard: AgentConfig.Default=true alone (no
// SetDefaultAgentOverride, i.e. no config.Agents.Defaults.DefaultAgentID)
// must NOT select "mia" — resolution falls through to the lexicographically-
// first non-worker registered agent instead (there is no "main" sentinel to
// fall back to anymore — see GetDefaultAgent's Priority 2). The per-entity
// Default field was demoted from the resolution ladder precisely because
// splitting agents into independent per-entity files means two concurrent
// writes could each set it true with no shared lock to serialize them; only
// the settings singleton (SetDefaultAgentOverride /
// config.Agents.Defaults.DefaultAgentID) is consulted now. See
// TestAgentRegistry_GetDefaultAgent_DefaultAgentIDOverride for the positive
// case.
func TestAgentRegistry_GetDefaultAgent_EntityDefaultFlagIsInert(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "alpha"},
		{ID: "mia", Default: true},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}
	// "alpha" < "mia" lexicographically — Priority 2's deterministic fallback.
	if agent.ID != "alpha" {
		t.Errorf("GetDefaultAgent = %q, want %q (AgentConfig.Default must be inert without SetDefaultAgentOverride)", agent.ID, "alpha")
	}
}

// TestAgentRegistry_GetDefaultAgent_FallsBackToLexicographicallyFirst
// verifies that when no agent is marked Default=true and no override is
// configured, GetDefaultAgent falls back to the lexicographically-first
// non-worker registered agent (Priority 2). There is no "main" sentinel to
// fall back to anymore.
func TestAgentRegistry_GetDefaultAgent_FallsBackToLexicographicallyFirst(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta"},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}
	if agent.ID != "alpha" {
		t.Errorf("GetDefaultAgent fallback = %q, want %q (must fall back to the lexicographically-first non-worker agent)",
			agent.ID, "alpha")
	}
}

// TestAgentRegistry_GetDefaultAgent_DefaultAgentIDOverride verifies that when
// no agent is marked Default=true but DefaultAgentID is set, the override is
// respected (F3 second-priority fallback).
func TestAgentRegistry_GetDefaultAgent_DefaultAgentIDOverride(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta"},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})
	// Simulate config.Agents.Defaults.DefaultAgentID being set.
	registry.SetDefaultAgentOverride("beta")

	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}
	if agent.ID != "beta" {
		t.Errorf("GetDefaultAgent with override = %q, want %q", agent.ID, "beta")
	}
}

// TestAgentRegistry_GetDefaultAgent_OverrideBeatsEntityDefaultFlag verifies
// the ADR-054 D6.4 ladder: the SetDefaultAgentOverride setting (mirroring
// config.Agents.Defaults.DefaultAgentID) wins over another agent's
// AgentConfig.Default=true flag — the inverse of the pre-ADR-054 behavior,
// where the per-entity flag was Priority 1. That priority was removed
// entirely (not merely reordered) since a per-entity bool cannot be an
// at-most-one singleton once agents split into independent files.
func TestAgentRegistry_GetDefaultAgent_OverrideBeatsEntityDefaultFlag(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "beta"},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})
	// Explicit override pointing to "beta" now wins, despite mia's inert
	// Default=true flag.
	registry.SetDefaultAgentOverride("beta")

	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}
	if agent.ID != "beta" {
		t.Errorf("GetDefaultAgent = %q, want %q (SetDefaultAgentOverride must beat an inert AgentConfig.Default flag)", agent.ID, "beta")
	}
}

// TestAgentRegistry_GetDefaultAgent_OverrideToWorkerSkipped verifies the
// Priority-1 hardening: a legacy DefaultAgentID override pointing at a worker
// (hand-edited config) is skipped — a worker is never a chat target — and
// resolution falls through to a non-worker agent, never the worker.
func TestAgentRegistry_GetDefaultAgent_OverrideToWorkerSkipped(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "base"},
		{ID: "worker", Type: config.AgentTypeWorker},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})
	// Hand-edited override pointing at the worker.
	registry.SetDefaultAgentOverride("worker")

	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}
	if agent.ID == "worker" || agent.IsWorker() {
		t.Fatalf(
			"GetDefaultAgent returned a worker (%q) via the Priority-1 override — workers are not chat targets",
			agent.ID,
		)
	}
}

// TestAgentRegistry_GetDefaultAgent_OnlyWorkerAndBaseSkipsWorker verifies that
// with only a worker and a base agent registered (no override), GetDefaultAgent
// returns the base agent, never the worker. This exercises the Priority-3
// first-non-worker fallback.
func TestAgentRegistry_GetDefaultAgent_OnlyWorkerAndBaseSkipsWorker(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "abase"},
		{ID: "zworker", Type: config.AgentTypeWorker},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}
	if agent.IsWorker() {
		t.Fatalf("GetDefaultAgent returned a worker (%q) — workers must be skipped", agent.ID)
	}
}

func TestAgentInstance_Model(t *testing.T) {
	model := &config.AgentModelConfig{Primary: "claude-opus"}
	cfg := testCfg([]config.AgentConfig{
		{ID: "custom", Default: true, Model: model},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent, _ := registry.GetAgent("custom")
	if agent.Model != "claude-opus" {
		t.Errorf("agent.Model = %q, want 'claude-opus'", agent.Model)
	}
}

func TestAgentInstance_FallbackInheritance(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "inherit", Default: true},
	})
	cfg.Agents.Defaults.ModelFallbacks = []string{"openai/gpt-4o-mini", "anthropic/haiku"}
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	agent, _ := registry.GetAgent("inherit")
	if len(agent.Fallbacks) != 2 {
		t.Errorf("expected 2 fallbacks inherited from defaults, got %d", len(agent.Fallbacks))
	}
}

func TestAgentInstance_FallbackExplicitEmpty(t *testing.T) {
	model := &config.AgentModelConfig{
		Primary:   "gpt-4",
		Fallbacks: []string{}, // explicitly empty = disable
	}
	cfg := testCfg([]config.AgentConfig{
		{ID: "no-fallback", Default: true, Model: model},
	})
	cfg.Agents.Defaults.ModelFallbacks = []string{"should-not-inherit"}
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

// TestAgentRegistry_EvaluateDefaultAgentHealth_DegradesOnSkippedRecord
// verifies that when the configured default agent's entity record was
// skipped at load time (exists but failed to parse), the registry is marked
// degraded with a reason — this is a pure health-surface signal and must NOT
// affect GetDefaultAgent's own resolution (which never consults it).
func TestAgentRegistry_EvaluateDefaultAgentHealth_DegradesOnSkippedRecord(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{{ID: "alpha"}})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	if degraded, _ := registry.DefaultAgentDegraded(); degraded {
		t.Fatal("registry must not start out degraded")
	}

	registry.EvaluateDefaultAgentHealth("mia", []string{"mia", "other-skipped"})

	degraded, reason := registry.DefaultAgentDegraded()
	if !degraded {
		t.Fatal("expected DefaultAgentDegraded=true when the configured default is in the skipped set")
	}
	if reason == "" {
		t.Error("expected a non-empty degraded reason")
	}

	// Resolution must still return a working agent — never black-holed.
	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("GetDefaultAgent must still return a working agent while degraded (R3: availability, not fail-closed)")
	}

	registry.ClearDefaultAgentDegraded()
	if degraded, _ := registry.DefaultAgentDegraded(); degraded {
		t.Fatal("expected ClearDefaultAgentDegraded to reset the flag")
	}
}

// TestAgentRegistry_EvaluateDefaultAgentHealth_NotDegradedWhenNeverConfigured
// verifies the negative case: an empty default-agent-id (never configured)
// must never raise the degraded flag — this is ordinary operation, not R3's
// concern.
func TestAgentRegistry_EvaluateDefaultAgentHealth_NotDegradedWhenNeverConfigured(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{{ID: "alpha"}})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	registry.EvaluateDefaultAgentHealth("", []string{"some-other-skipped-id"})
	if degraded, _ := registry.DefaultAgentDegraded(); degraded {
		t.Fatal("an empty/unconfigured default agent id must never raise degraded")
	}
}

// TestAgentRegistry_EvaluateDefaultAgentHealth_NotDegradedWhenNeverExisted
// verifies that a configured default agent id that simply was never created
// (absent from the skipped set entirely — a config error, not a load
// failure) does not raise degraded. Only "exists but failed to parse" does.
func TestAgentRegistry_EvaluateDefaultAgentHealth_NotDegradedWhenNeverExisted(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{{ID: "alpha"}})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	registry.EvaluateDefaultAgentHealth("ghost-agent-never-created", nil)
	if degraded, _ := registry.DefaultAgentDegraded(); degraded {
		t.Fatal("a default agent id absent from the skipped set (never existed) must not raise degraded")
	}
}

// --- Concurrency: two writers cannot both become "the" default ---

// TestAgentRegistry_ConcurrentSetDefaultAgentOverride_NoSplitBrain is the
// registry-level counterpart to the settings-scalar argument in
// GetDefaultAgent's doc comment: config.Agents.Defaults.DefaultAgentID (and
// its in-memory mirror, defaultAgentOverride) is a SINGLE string field, so
// unlike the old per-entity AgentConfig.Default bool, there is no way for two
// concurrent writers to leave the registry believing two different agents
// are simultaneously "the" default — the last write under the mutex simply
// wins, and every GetDefaultAgent call in between and after observes exactly
// one well-defined answer. Run with -race: the assertion here is "no data
// race and no panic", not which of the two concurrently-set IDs wins (that
// is intentionally non-deterministic).
func TestAgentRegistry_ConcurrentSetDefaultAgentOverride_NoSplitBrain(t *testing.T) {
	cfg := testCfg([]config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta"},
	})
	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				registry.SetDefaultAgentOverride("alpha")
			} else {
				registry.SetDefaultAgentOverride("beta")
			}
		}(i)
	}
	// Concurrent reads racing the writes above — must never observe a torn
	// state (e.g. two agents both returned "the" default) or panic.
	seen := make(map[string]bool)
	var mu sync.Mutex
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent := registry.GetDefaultAgent()
			if agent == nil {
				t.Error("GetDefaultAgent returned nil during concurrent override writes")
				return
			}
			mu.Lock()
			seen[agent.ID] = true
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Exactly one of alpha/beta may have been observed — never more than the
	// known candidate set, and never a corrupted/empty ID. No "main"
	// sentinel to allow for anymore — the registry only ever holds alpha and
	// beta.
	for id := range seen {
		if id != "alpha" && id != "beta" {
			t.Errorf("GetDefaultAgent returned unexpected id %q during concurrent writes", id)
		}
	}

	// Final state is deterministic and single-valued.
	final := registry.GetDefaultAgent()
	if final == nil || (final.ID != "alpha" && final.ID != "beta") {
		t.Fatalf("expected a definite final default of alpha or beta, got %v", final)
	}
}

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
