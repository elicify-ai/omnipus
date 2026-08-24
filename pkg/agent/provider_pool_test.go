// Provider pool tests — W4-17.
//
// These cover FR-007's invariant that an agent's provider pool is rebuilt
// every time the candidate chain changes (ApplyAgentModel / handleModelSwitch)
// and the build functions dedup + skip-with-warn consistently.
//
// Criticality 7-8: the previous wave shipped handleModelSwitch + pool refactor
// without any direct unit tests for buildProviderPool / findModelConfigForProvider;
// these regressions would have been caught earlier had they existed.

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// newPoolTestConfig builds a Config with two distinct providers — openrouter
// (passthrough, slug-form models) and anthropic (API-key direct, slug-form
// models). APIKeyRefs are env vars set by the calling test via t.Setenv.
func newPoolTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Provider: "openrouter", Model: "anthropic/claude-sonnet-4.6"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{
				// The model id is `anthropic/claude-sonnet-4.6` — one
				// OpenRouter model whose id contains a slash (FR-034), not a
				// request for a separate `anthropic` provider.
				Provider:  "openrouter",
				Model:     "anthropic/claude-sonnet-4.6",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "W4_17_OPENROUTER_KEY",
			},
			{
				Provider:  "anthropic",
				Model:     "claude-haiku-4-5-20251001",
				APIBase:   "https://api.anthropic.com",
				APIKeyRef: "W4_17_ANTHROPIC_KEY",
			},
		},
	}
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

// TestBuildProviderPool_DedupsDistinctProviders covers Crit 8: when the
// candidate chain references the same provider multiple times (e.g. several
// openrouter fallbacks), the pool must contain a single entry per distinct
// provider. Otherwise CreateProviderFromConfig runs N times for the same
// provider (wasted CPU + extra log lines).
func TestBuildProviderPool_DedupsDistinctProviders(t *testing.T) {
	t.Setenv("W4_17_OPENROUTER_KEY", "or-key")

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "openrouter-1",
				Model:     "openrouter/openai/gpt-4o",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "W4_17_OPENROUTER_KEY",
			},
		},
	}

	candidates := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: "openrouter/openai/gpt-4o"},
		{Provider: "openrouter", Model: "anthropic/claude-sonnet-4.6"},
		{Provider: "openrouter", Model: "openrouter/google/gemini-2.5-flash"},
	}

	pool := buildProviderPool(cfg, candidates)
	if pool == nil {
		t.Fatal("buildProviderPool returned nil despite 3 openrouter candidates")
	}
	if len(pool) != 1 {
		t.Errorf("pool size = %d, want 1 (deduped by provider name); entries = %v", len(pool), pool)
	}
	if _, ok := pool["openrouter"]; !ok {
		t.Error("pool missing the 'openrouter' entry — the only distinct provider")
	}
}

// TestBuildProviderPool_SkipsProvidersWithMissingModelConfig covers Crit 8:
// if a fallback pins a provider that's not in cfg.Providers, the build must
// skip the entry (not crash) and return a pool with the remaining valid
// providers. The skipped entry is non-fatal — GetProviderForCandidate's
// legacy fallback path will degrade to the primary's provider.
func TestBuildProviderPool_SkipsProvidersWithMissingModelConfig(t *testing.T) {
	t.Setenv("W4_17_OPENROUTER_KEY", "or-key")

	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "openrouter-1",
				Model:     "openrouter/openai/gpt-4o",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "W4_17_OPENROUTER_KEY",
			},
		},
	}

	candidates := []providers.FallbackCandidate{
		{Provider: "openrouter", Model: "openrouter/openai/gpt-4o"},
		{Provider: "nonexistent-provider", Model: "mystery/model"},
	}

	pool := buildProviderPool(cfg, candidates)
	if pool == nil {
		t.Fatal("buildProviderPool returned nil — valid openrouter entry should have produced a 1-entry pool")
	}
	if _, ok := pool["openrouter"]; !ok {
		t.Error("pool missing the valid 'openrouter' entry — the missing-provider skip should be non-fatal")
	}
	if _, ok := pool["nonexistent-provider"]; ok {
		t.Error(
			"pool contains a 'nonexistent-provider' entry — findModelConfigForProvider should have failed and the entry should have been skipped",
		)
	}
}

// TestBuildProviderPool_NilCfgReturnsNil covers Crit 8: nil config is a
// valid input (NewAgentInstance may be called during test teardown / partial
// boot) — the function must not panic and must return nil.
func TestBuildProviderPool_NilCfgReturnsNil(t *testing.T) {
	if got := buildProviderPool(nil, []providers.FallbackCandidate{{Provider: "openrouter", Model: "x"}}); got != nil {
		t.Errorf("buildProviderPool(nil, ...) = %v, want nil", got)
	}
	if got := buildProviderPool(&config.Config{}, nil); got != nil {
		t.Errorf("buildProviderPool(cfg, nil) = %v, want nil", got)
	}
}

// TestFindModelConfigForProvider_CaseInsensitiveMatch covers Crit 8: a
// candidate chain that pins "OpenRouter" (mixed case) must still resolve to
// the "openrouter" ModelConfig. The spec for the candidate string is
// case-insensitive on the provider name — otherwise a config that defines
// "openrouter" but a fallback that says "OpenRouter" silently skips the
// pool entry.
func TestFindModelConfigForProvider_CaseInsensitiveMatch(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "openrouter-1",
				Model:     "openrouter/openai/gpt-4o",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "k",
			},
		},
	}

	mc, err := findModelConfigForProvider(cfg, "OpenRouter")
	if err != nil {
		t.Fatalf("findModelConfigForProvider(OpenRouter) error = %v — want case-insensitive match", err)
	}
	if mc == nil {
		t.Fatal("findModelConfigForProvider returned nil mc — expected clone of openrouter entry")
	}
	if mc.Provider != "openrouter" {
		t.Errorf("returned mc.Provider = %q, want 'openrouter' (the canonical-cased match)", mc.Provider)
	}

	// And the clone must be a distinct value (modifying it must not mutate
	// cfg.Providers[0]).
	mc.Name = "MUTATED"
	if cfg.Providers[0].Name == "MUTATED" {
		t.Error("findModelConfigForProvider did not return a clone — mutation leaked into cfg.Providers[0]")
	}
}

// TestBuildProviderPool_FallsBackToPassthrough is the defensive-layer
// safety net behind the resolver fix: when a candidate's Provider is a
// vendor namespace that doesn't match any configured provider entry, the
// pool builder scans cfg.Providers for a passthrough entry (openrouter /
// vivgrid) whose Model equals the candidate's Model and uses its
// credentials. The original candidate's name is preserved as the pool key
// so the agent's GetProviderForCandidate lookup still works.
func TestBuildProviderPool_FallsBackToPassthrough(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "k",
			},
		},
	}
	// A candidate whose Provider is "zai" (a vendor namespace, not a
	// configured provider) but whose Model is "z-ai/glm-5.2" — matches the
	// openrouter entry's Model exactly.
	candidates := []providers.FallbackCandidate{
		{Provider: "zai", Model: "z-ai/glm-5.2"},
	}
	pool := buildProviderPool(cfg, candidates)
	if len(pool) == 0 {
		t.Fatal("buildProviderPool returned empty pool — defensive fallback failed to route through openrouter")
	}
	// The candidate's name ("zai") is the pool key — that's what
	// GetProviderForCandidate looks up against.
	if _, ok := pool["zai"]; !ok {
		t.Errorf(
			"pool missing key 'zai' — defensive fallback should preserve the candidate's name as the pool key; got keys: %v",
			poolKeys(pool),
		)
	}
}

// poolKeys returns the sorted keys of a provider pool for stable error
// messages in the test above.
func poolKeys(pool map[string]providers.LLMProvider) []string {
	keys := make([]string, 0, len(pool))
	for k := range pool {
		keys = append(keys, k)
	}
	return keys
}
