// loop_config_test.go: tests for live config, model and reload

package agent

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

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

// TestMutateConfig_ConcurrentUpsertAgentFast_NoDataRace is the mutation test
// for the MutateConfig data-race fix.
//
// THE RACE (pre-fix): MutateConfig mutated al.cfg's fields IN PLACE under
// al.mu.Lock — no pointer swap, no configGen bump. But GetConfig() hands out
// that same live *config.Config pointer, and fastAgentUpsert/UpsertAgentFast
// passes it straight into its wiring pass (registerSharedTools,
// wireTier13DepsLocked, NewAgentInstance(&cfg.Agents.Defaults, ...), the route
// resolver, …) which reads it WITHOUT any lock during the slow wiring pass. A
// concurrent MutateConfig writing the very object the wiring pass reads is a
// genuine data race — and it was INVISIBLE to the configGen CAS guard, which
// only detects pointer SWAPS by SwapConfig/ReloadProviderAndConfig.
//
// THE FIX: MutateConfig now deep-copies (config.Clone), runs fn on the private
// copy, and publishes via pointer-swap + configGen bump — mirroring SwapConfig
// and ReloadProviderAndConfig. The live pointer GetConfig handed out is never
// touched, so the wiring pass's unlocked reads race nothing.
//
// PROOF MODEL: a hammer — one goroutine hammers MutateConfig (writing a field
// the wiring pass reads: cfg.Agents.Defaults.MaxTokens), several goroutines
// hammer UpsertAgentFast with the LIVE al.cfg pointer (exactly what production
// does at pkg/gateway/gateway.go's UpsertAgentFastFunc:
// `agentLoop.UpsertAgentFast(agentLoop.GetConfig(), agentID)`). The race
// detector flags any unsynchronized write/read pair with no happens-before
// edge between them; under the pre-fix code MutateConfig's in-place write
// (under al.mu) and the wiring pass's field read (NOT under al.mu) are exactly
// such a pair. Run under `-race`.
//
// MUTATION-SENSITIVE: reverting MutateConfig to `return fn(al.cfg)` (in-place,
// no clone/swap) makes `go test -race` report a DATA RACE on
// cfg.Agents.Defaults between MutateConfig's write and UpsertAgentFast's
// wiring-pass read. With the clone-then-swap fix in place, no race is reported.
func TestMutateConfig_ConcurrentUpsertAgentFast_NoDataRace(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
		{ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom},
	})

	// Seed gamma's durable entity record so UpsertAgentFast's lost-race rebase
	// branch (which asks the entity store via agentstore.Store.Get) does not
	// error out for an unrelated reason — mirrors production ordering where
	// agentstore.Store.Create completes before UpsertAgentFast is ever called.
	require.NoError(t, agentstore.New(al.homePath).Create("gamma", &config.AgentConfig{
		ID: "gamma", Name: "Gamma", Type: config.AgentTypeCustom,
	}))

	// Workload is intentionally small: the race detector flags the FIRST
	// unsynchronized write/read pair with no happens-before edge — it does not
	// need a large iteration count to fire, only genuine concurrency between
	// MutateConfig's write and one wiring-pass read. Each UpsertAgentFast call
	// runs the full wiring pass (registerSharedTools et al.), which is heavy,
	// so a handful of overlapping calls is both sufficient and pod-friendly.
	const readers = 2
	const itersPerReader = 3

	stop := make(chan struct{})
	var mutateWG, upsertWG sync.WaitGroup

	// Writer: hammer MutateConfig, mutating a field the wiring pass reads.
	// Pre-fix, each call wrote the LIVE al.cfg in place — the racing write.
	mutateWG.Add(1)
	go func() {
		defer mutateWG.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = al.MutateConfig(func(cfg *config.Config) error {
				// Write a Defaults field; UpsertAgentFast's wiring pass reads
				// cfg.Agents.Defaults (NewAgentInstance takes its address) with
				// no lock — the racing read.
				cfg.Agents.Defaults.MaxTokens = 1000 + (i % 500)
				i++
				return nil
			})
		}
	}()

	// Readers: hammer UpsertAgentFast with the LIVE al.cfg pointer, exactly as
	// pkg/gateway/gateway.go's UpsertAgentFastFunc does in production.
	for r := 0; r < readers; r++ {
		upsertWG.Add(1)
		go func() {
			defer upsertWG.Done()
			for j := 0; j < itersPerReader; j++ {
				_, _ = al.UpsertAgentFast(al.GetConfig(), "gamma")
			}
		}()
	}
	upsertWG.Wait()
	close(stop)
	mutateWG.Wait()
}

// TestMutateConfig_PreservesRegisteredSensitiveValues locks in the
// config.Clone change that carries registeredSensitive across the swap.
//
// MutateConfig now publishes a Clone() of the live config as the new al.cfg.
// config.Clone JSON-round-trips, which drops the unexported registeredSensitive
// slice (the resolved credential plaintexts registered at boot/reload via
// RegisterSensitiveValues). If the clone lost them, the post-swap live config
// would scrub NOTHING from LLM output/audit logs until the next full reload — a
// security regression the race fix must NOT introduce. config.Clone therefore
// carries registeredSensitive onto the clone.
//
// MUTATION-SENSITIVE: reverting config.Clone's registeredSensitive carry-over
// (while keeping MutateConfig's clone+swap) makes this test fail — the
// registered secret's plaintext appears unfiltered after MutateConfig returns.
func TestMutateConfig_PreservesRegisteredSensitiveValues(t *testing.T) {
	al := buildFastUpsertTestLoop(t, []config.AgentConfig{
		{ID: "alpha", Name: "Alpha", Type: config.AgentTypeCustom},
	})
	const secret = "supersecret-api-key-value-12345" // >3 chars so it is filtered
	al.GetConfig().RegisterSensitiveValues([]string{secret})

	// Sanity: scrubbing works on the pre-mutation live config.
	pre := al.GetConfig().FilterSensitiveData("token=" + secret + " ok")
	require.Containsf(t, pre, "[FILTERED]",
		"registered secret must be scrubbed BEFORE MutateConfig (sanity)")
	require.NotContains(t, pre, secret)

	// MutateConfig publishes a clone as the new al.cfg. That clone MUST still
	// scrub the registered secret — otherwise clone+swap regresses credential
	// scrubbing until the next full reload.
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error {
		cfg.Agents.Defaults.MaxTokens = 7777
		return nil
	}))

	post := al.GetConfig().FilterSensitiveData("token=" + secret + " ok")
	require.Containsf(t, post, "[FILTERED]",
		"registered secret must still be scrubbed after MutateConfig's clone+swap — "+
			"if this fails, config.Clone dropped registeredSensitive and the live config no longer scrubs credentials")
	require.NotContains(t, post, secret,
		"registered secret plaintext must not appear after MutateConfig")
}

// TestApplyAgentModel_RebuildsProviderPool covers Crit 8: after switching
// primary to a model whose pinned provider is one of the agent's fallback
// providers, the pool must STILL contain BOTH providers (not just the new
// primary's). Otherwise a subsequent fallback that routes through the other
// provider would silently degrade to "use the primary's provider" per
// GetProviderForCandidate's legacy fallback path.
func TestApplyAgentModel_RebuildsProviderPool(t *testing.T) {
	t.Setenv("W4_17_OPENROUTER_KEY", "or-key")
	t.Setenv("W4_17_ANTHROPIC_KEY", "anth-key")

	cfg := newPoolTestConfig(t)
	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)

	before := al.GetRegistry().GetDefaultAgent()
	if before == nil {
		t.Fatal("no default agent")
	}

	// Sanity: the initial pool was built eagerly from the candidate chain at
	// NewAgentInstance. openrouter must be in it (the primary provider).
	// providerPool is an atomic.Pointer[map[…]]; Load() returns the current
	// map snapshot (or nil if the pointer was never set, which would be a bug).
	if pool := before.providerPool.Load(); pool == nil {
		t.Fatal("initial providerPool is nil — FR-007 buildProviderPool was skipped")
	} else if _, ok := (*pool)["openrouter"]; !ok {
		t.Error("initial pool missing openrouter entry — buildProviderPool did not pick up the primary provider")
	}

	// Switch primary to anthropic-pinned model. The fallback chain (if any)
	// references anthropic, so the post-switch pool must include both
	// providers.
	if _, err := al.ApplyAgentModel(before.ID, "claude-haiku-4-5-20251001"); err != nil {
		t.Fatalf("ApplyAgentModel(claude-haiku-4-5-20251001): %v", err)
	}

	after, ok := al.GetRegistry().GetAgent(before.ID)
	if !ok {
		t.Fatal("agent vanished after ApplyAgentModel")
	}
	if after.Provider == nil {
		t.Fatal("agent.Provider is nil after switch")
	}

	// Pool must contain anthropic (the new primary's provider).
	anthProv := after.GetProviderForCandidate(providers.FallbackCandidate{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5-20251001",
	})
	if anthProv == nil {
		t.Errorf("post-switch pool missing anthropic entry: GetProviderForCandidate returned nil for anthropic")
	}

	// Pool must STILL contain openrouter (the original primary / any fallback
	// that routes through openrouter). FR-007: a fallback that pins
	// openrouter must use openrouter credentials, not the new primary's.
	orProv := after.GetProviderForCandidate(providers.FallbackCandidate{
		Provider: "openrouter",
		Model:    "openrouter/anthropic/claude-sonnet-4.6",
	})
	if orProv == nil {
		t.Errorf("post-switch pool dropped openrouter entry: GetProviderForCandidate returned nil for openrouter")
	}
	// FR-007 invariant: the anthropic provider instance and the openrouter
	// provider instance MUST be different — they have different API keys,
	// different endpoints, and different model families. A fall-back
	// implementation that lazily points all pinned providers at the primary
	// would violate this.
	if anthProv == orProv {
		t.Error(
			"post-switch pool returned the same LLMProvider for anthropic and openrouter — FR-007 requires distinct provider instances",
		)
	}
}

// TestReloadProviderAndConfig_ReassertsSkillsWriteAuditLogger proves the
// reload path (pkg/agent/loop.go's al.auditLogger != nil block that also
// calls al.wireMemoryAuditLoggerOn) re-asserts
// tools.SetSkillsWriteAuditLogger too, so a hot config reload can never leave
// the process-wide skills audit hook silently unset even though it started
// wired at boot.
func TestReloadProviderAndConfig_ReassertsSkillsWriteAuditLogger(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := config.DefaultConfig()
	coreagent.SeedConfig(cfg)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Sandbox.AuditLog = true

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })

	reloadWithSameAgents(t, al)

	tools.EmitSkillWriteAudit("registry", "write_file", "jim", "sess-2", "ws-2", "/home/.omnipus/skills/some-skill/SKILL.md")

	auditPath := filepath.Join(home, "system", "audit.jsonl")
	events := readAuditEvents(t, auditPath)
	found := false
	for _, e := range events {
		if e["event"] == "skill.write" {
			details, _ := e["details"].(map[string]any)
			if details != nil && details["workspace_id"] == "ws-2" {
				found = true
			}
		}
	}
	assert.True(t, found, "the skills write-audit logger must still be wired after a config reload; got events: %v", events)
}

// =============================================================================
// W2-27 — apply_agent_model_test.go passthrough case
// (instance-preservation re-assertion `id == after.ID`)
// =============================================================================
//
// W2-27 (test-analyzer-A #6) flagged that the existing
// TestApplyAgentModel_SwitchesInPlacePreservingInstance test asserts
// `after != before` (pointer equality) but not the ID field directly. A
// regression that replaced the instance with one having a different ID
// (e.g. via hot-reload) would slip past the pointer check if the
// implementation also re-assigned the same pointer.
//
// This test adds an ID-level re-assertion to lock the contract.
func TestApplyAgentModel_SwitchesInPlace_PreservesID(t *testing.T) {
	t.Setenv("LOOP_APPLY3_KEY", "k")

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
				APIKeyRef: "LOOP_APPLY3_KEY",
			},
			{
				Provider:  "deepseek",
				Model:     "deepseek-chat",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY3_KEY",
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	require.NoError(t, err)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	before := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, before)
	id := before.ID
	require.NotEmpty(t, id)

	_, err = al.ApplyAgentModel(id, "deepseek-chat")
	require.NoError(t, err)

	after, ok := al.GetRegistry().GetAgent(id)
	require.True(t, ok, "agent must remain in the registry after ApplyAgentModel")
	require.Equal(t, id, after.ID,
		"W2-27 (passthrough case): agent ID must be preserved across ApplyAgentModel — "+
			"a regression that hot-replaces the instance would change the ID and break "+
			"downstream session/agent binding")
}
