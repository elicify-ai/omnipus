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

	pool := buildProviderPool(cfg, candidates, "mia").pool
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

	pool := buildProviderPool(cfg, candidates, "mia").pool
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
	if got := buildProviderPool(nil, []providers.FallbackCandidate{{Provider: "openrouter", Model: "x"}}, "mia"); got.pool != nil {
		t.Errorf("buildProviderPool(nil, ...) = %v, want nil", got.pool)
	}
	if got := buildProviderPool(&config.Config{}, nil, "mia"); got.pool != nil {
		t.Errorf("buildProviderPool(cfg, nil) = %v, want nil", got.pool)
	}
}

// TestFindModelConfigForProvider_ExactMatchOnly — ADR-067 T067-09 test T28
// (FR-036, DS-8 row 4). REPLACES TestFindModelConfigForProvider_CaseInsensitiveMatch,
// whose subject (strings.EqualFold on the provider id) FR-036 deletes: a
// case-folded match is the one shape through which a retired spelling could
// still resolve a canonical row after the greenfield rename paths were
// removed. The clone half of that test's coverage is preserved verbatim
// below — it was, and remains, a real invariant.
//
// The lookup is EXACT after TrimSpace: surrounding whitespace is tolerated
// (a config field the operator typed), a different case is not.
func TestFindModelConfigForProvider_ExactMatchOnly(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "zai-1",
				Model:     "glm-5.2",
				Provider:  "zai",
				APIBase:   "https://api.z.ai/api/paas/v4",
				APIKeyRef: "k",
			},
		},
	}

	// The FR-036 case: an entity that says "ZAI" must NOT resolve "zai".
	if mc, err := findModelConfigForProvider(cfg, "ZAI"); err == nil {
		t.Fatalf(
			"findModelConfigForProvider(%q) resolved %q — FR-036 requires an EXACT comparison; a case-folded match resurrects a retired spelling",
			"ZAI", mc.Provider,
		)
	}

	// The exact id still resolves, and whitespace around it is trimmed.
	for _, id := range []string{"zai", "  zai  "} {
		mc, err := findModelConfigForProvider(cfg, id)
		if err != nil {
			t.Fatalf("findModelConfigForProvider(%q) error = %v — the exact id (after TrimSpace) must resolve", id, err)
		}
		if mc == nil || mc.Provider != "zai" {
			t.Fatalf("findModelConfigForProvider(%q) = %+v, want the 'zai' row", id, mc)
		}
	}

	// And the clone must be a distinct value (modifying it must not mutate
	// cfg.Providers[0]) — preserved from the replaced test.
	mc, err := findModelConfigForProvider(cfg, "zai")
	if err != nil {
		t.Fatalf("findModelConfigForProvider(zai) error = %v", err)
	}
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
	pool := buildProviderPool(cfg, candidates, "mia").pool
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
