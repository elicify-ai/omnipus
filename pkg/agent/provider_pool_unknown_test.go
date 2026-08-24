// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-067 T067-09 — the provider-pool half of the per-agent degrade.
//
// Spec: docs/internal/specs/adr-067-registry-catalog-spec.md FR-016 (an agent
// is `needs_provider` iff its PRIMARY provider is unknown; an unknown
// fallback is dropped with one WARN and the agent runs on the rest), FR-036
// (exact id comparison). Scenarios US-6.AC5 / US-6.AC6; dataset DS-8, all six
// rows. Tests T32 and T33b.

package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// newUnknownProviderTestConfig builds a config with three provider rows:
//
//   - openai — a catalog id, configured with a key: constructible.
//   - zai    — a catalog id, configured with a key: constructible.
//   - nope   — NOT a catalog id and not a custom row (no api_base, no
//     protocol): the unknown provider every DS-8 row keys on.
//
// The catalog consulted is the process catalog, i.e. the committed embedded
// snapshot — the same document a freshly installed gateway resolves against
// before its first pull.
func newUnknownProviderTestConfig(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("T067_09_OPENAI_KEY", "sk-openai")
	t.Setenv("T067_09_ZAI_KEY", "sk-zai")
	return &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "openai-1",
				Provider:  "openai",
				Model:     "gpt-5.2",
				APIBase:   "https://api.openai.com/v1",
				APIKeyRef: "T067_09_OPENAI_KEY",
			},
			{
				Name:      "zai-1",
				Provider:  "zai",
				Model:     "glm-5.2",
				APIBase:   "https://api.z.ai/api/paas/v4",
				APIKeyRef: "T067_09_ZAI_KEY",
			},
			{
				// An operator-typed id the catalog has never heard of, with
				// neither half of a custom-endpoint definition.
				Name:     "nope",
				Provider: "nope",
				Model:    "nope/x",
			},
		},
	}
}

// TestBuildProviderPool_UnknownProvider_Skips — T32 (FR-016; DS-8 rows 2 and
// 5). An UNKNOWN primary never reaches construction, produces no pool entry,
// and is reported as the agent's `needs_provider` degrade. Row 5 pins the
// "primary rules" clause: a healthy FALLBACK does not rescue an unknown
// primary.
func TestBuildProviderPool_UnknownProvider_Skips(t *testing.T) {
	cfg := newUnknownProviderTestConfig(t)

	t.Run("DS-8 row 2: unknown primary, no fallbacks", func(t *testing.T) {
		build := buildProviderPool(cfg, []providers.FallbackCandidate{
			{Provider: "nope", Model: "nope/x"},
		}, "agent-b")
		if !build.primaryUnknown {
			t.Error("primaryUnknown = false, want true — an unknown primary is the needs_provider degrade")
		}
		if build.primaryProvider != "nope" {
			t.Errorf("primaryProvider = %q, want %q (the operator's own spelling)", build.primaryProvider, "nope")
		}
		if _, ok := build.pool["nope"]; ok {
			t.Errorf("pool contains a 'nope' entry — an unknown id must never be constructed; keys=%v", poolKeys(build.pool))
		}
	})

	t.Run("DS-8 row 5: unknown primary with a healthy fallback still degrades", func(t *testing.T) {
		build := buildProviderPool(cfg, []providers.FallbackCandidate{
			{Provider: "nope", Model: "nope/x"},
			{Provider: "openai", Model: "gpt-5.2"},
		}, "agent-b")
		if !build.primaryUnknown {
			t.Error("primaryUnknown = false, want true — the PRIMARY rules, a healthy fallback does not rescue it")
		}
		if _, ok := build.pool["openai"]; !ok {
			t.Errorf("pool missing 'openai' — the healthy fallback must still be built; keys=%v", poolKeys(build.pool))
		}
	})

	t.Run("DS-8 row 1: a catalog primary is not degraded", func(t *testing.T) {
		build := buildProviderPool(cfg, []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-5.2"},
		}, "agent-a")
		if build.primaryUnknown {
			t.Error("primaryUnknown = true for a configured catalog provider — nothing is degraded here")
		}
		if _, ok := build.pool["openai"]; !ok {
			t.Errorf("pool missing 'openai'; keys=%v", poolKeys(build.pool))
		}
	})

	t.Run("DS-8 row 4: a case-different id is unknown (FR-036)", func(t *testing.T) {
		build := buildProviderPool(cfg, []providers.FallbackCandidate{
			{Provider: "ZAI", Model: "glm-5.2"},
		}, "agent-d")
		if !build.primaryUnknown {
			t.Error(
				"primaryUnknown = false for entity provider \"ZAI\" against config \"zai\" — " +
					"FR-036 requires an exact comparison, so the agent must degrade",
			)
		}
		if _, ok := build.pool["ZAI"]; ok {
			t.Errorf("pool contains a 'ZAI' entry — a case-different id must not resolve the 'zai' row; keys=%v", poolKeys(build.pool))
		}
	})
}

// TestBuildProviderPool_UnknownFallback_DroppedWithWarn — T33b (FR-016,
// US-6.AC5; DS-8 rows 3 and 6). An unknown provider named ONLY by a fallback
// is dropped from the pool with exactly ONE WARN naming the agent and the
// provider, and the agent is NOT degraded: it runs on the remaining pool.
func TestBuildProviderPool_UnknownFallback_DroppedWithWarn(t *testing.T) {
	cfg := newUnknownProviderTestConfig(t)

	t.Run("DS-8 row 3: openai + [nope]", func(t *testing.T) {
		readLog := captureLogFile(t, logger.WARN)
		build := buildProviderPool(cfg, []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-5.2"},
			{Provider: "nope", Model: "nope/x"},
		}, "agent-c")

		if build.primaryUnknown {
			t.Error("primaryUnknown = true — an unknown FALLBACK must not degrade the agent (US-6.AC5)")
		}
		if len(build.pool) != 1 {
			t.Errorf("pool size = %d, want 1 (openai only); keys=%v", len(build.pool), poolKeys(build.pool))
		}
		if _, ok := build.pool["openai"]; !ok {
			t.Errorf("pool missing 'openai'; keys=%v", poolKeys(build.pool))
		}
		if _, ok := build.pool["nope"]; ok {
			t.Errorf("pool contains 'nope' — the unknown fallback must be dropped; keys=%v", poolKeys(build.pool))
		}

		log := readLog()
		if n := strings.Count(log, "Unknown provider named by a fallback"); n != 1 {
			t.Errorf("dropped-fallback WARN count = %d, want exactly 1; log=%s", n, log)
		}
		if !strings.Contains(log, "agent-c") {
			t.Errorf("the WARN must name the agent; log=%s", log)
		}
		if !strings.Contains(log, "nope") {
			t.Errorf("the WARN must name the provider the operator typed; log=%s", log)
		}
	})

	t.Run("DS-8 row 6: openai + [nope, zai] keeps zai", func(t *testing.T) {
		readLog := captureLogFile(t, logger.WARN)
		build := buildProviderPool(cfg, []providers.FallbackCandidate{
			{Provider: "openai", Model: "gpt-5.2"},
			{Provider: "nope", Model: "nope/x"},
			{Provider: "zai", Model: "glm-5.2"},
		}, "agent-c")

		if build.primaryUnknown {
			t.Error("primaryUnknown = true — only the unknown FALLBACK is affected")
		}
		if len(build.pool) != 2 {
			t.Errorf("pool size = %d, want 2 (openai + zai); keys=%v", len(build.pool), poolKeys(build.pool))
		}
		for _, want := range []string{"openai", "zai"} {
			if _, ok := build.pool[want]; !ok {
				t.Errorf("pool missing %q; keys=%v", want, poolKeys(build.pool))
			}
		}
		if n := strings.Count(readLog(), "Unknown provider named by a fallback"); n != 1 {
			t.Errorf("dropped-fallback WARN count = %d, want exactly 1 (only 'nope' is unknown)", n)
		}
	})
}

// TestBuildProviderPool_UnknownFallback_NotRescuedByPassthrough pins the
// deliberate narrowing of the passthrough safety net (FR-016 + the greenfield
// rule): the net exists for a vendor NAMESPACE that leaked out of a
// slash-separated model id — a real catalog id that simply has no configured
// row — and must never rescue an id the catalog has never heard of. Routing
// an unknown id through someone else's credentials would be an alias, which
// is exactly what ADR-067 deleted.
//
// The rescue itself is still exercised for a catalog id by
// TestBuildProviderPool_FallsBackToPassthrough.
func TestBuildProviderPool_UnknownFallback_NotRescuedByPassthrough(t *testing.T) {
	t.Setenv("T067_09_OR_KEY", "or-key")
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				Name:      "openrouter-1",
				Provider:  "openrouter",
				Model:     "z-ai/glm-5.2",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "T067_09_OR_KEY",
			},
		},
	}
	// "z-ai" is a non-canonical spelling: not a catalog id, and no config row
	// defines it as a custom endpoint. The openrouter row carries the exact
	// model, so the passthrough net WOULD match on model alone.
	build := buildProviderPool(cfg, []providers.FallbackCandidate{
		{Provider: "openrouter", Model: "z-ai/glm-5.2"},
		{Provider: "z-ai", Model: "z-ai/glm-5.2"},
	}, "agent-c")

	if _, ok := build.pool["z-ai"]; ok {
		t.Errorf(
			"pool contains a 'z-ai' entry — an UNKNOWN id must not be rescued through the passthrough net; keys=%v",
			poolKeys(build.pool),
		)
	}
	if _, ok := build.pool["openrouter"]; !ok {
		t.Errorf("pool missing 'openrouter' — the real row must still be built; keys=%v", poolKeys(build.pool))
	}
}

// TestIsUnknownProviderID_SharedPredicate pins the contract the gateway's
// Agent.degraded_reason and the agent's pre-turn gate BOTH read, so the list
// can never report an agent healthy while its next turn is refused.
func TestIsUnknownProviderID_SharedPredicate(t *testing.T) {
	cfg := newUnknownProviderTestConfig(t)
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		// A custom row: both halves present, so its id is NOT unknown even
		// though the catalog has never heard of it.
		Name:     "my-proxy",
		Provider: "my-proxy",
		Model:    "local-model",
		APIBase:  "http://127.0.0.1:8000/v1",
		Protocol: "openai-compatible",
	}, &config.ModelConfig{
		// A HALF-defined custom row: an api_base but no protocol. FR-035
		// requires both, so this id stays unknown.
		Name:     "half-proxy",
		Provider: "half-proxy",
		Model:    "local-model",
		APIBase:  "http://127.0.0.1:8001/v1",
	})

	cases := []struct {
		id   string
		want bool
	}{
		{"openai", false},   // catalog id
		{"zai", false},      // catalog id
		{"ZAI", true},       // FR-036: exact, never case-folded
		{"nope", true},      // configured, but neither catalog nor custom
		{"z-ai", true},      // a non-canonical spelling is simply unknown
		{"my-proxy", false}, // a complete custom row
		{"half-proxy", true},
		{"", false}, // "no provider pinned" is needs_model, not needs_provider
	}
	for _, tc := range cases {
		if got := providers.IsUnknownProviderID(cfg, tc.id); got != tc.want {
			t.Errorf("IsUnknownProviderID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// TestNewAgentInstance_DefaultsPathUnknownProvider_Degrades closes the one gap
// the candidate-level table above cannot: an agent that pins NOTHING of its
// own still inherits `agents.defaults.default_model`'s provider as its pinned
// primary (ADR-068 D14.1 — the default is an exact pair, never inferred). When
// THAT id is unknown, the agent degrades exactly as an explicitly-pinned one
// does, and a healthy default leaves it alone.
func TestNewAgentInstance_DefaultsPathUnknownProvider_Degrades(t *testing.T) {
	base := newUnknownProviderTestConfig(t)

	newInstance := func(t *testing.T, defaultProvider, defaultModel string) *AgentInstance {
		t.Helper()
		// config.Config carries a sync.RWMutex, so it is never copied by
		// value (govet copylocks). Build a fresh one sharing the same
		// provider rows instead.
		cfg := &config.Config{Providers: base.Providers}
		cfg.Agents = config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Provider: defaultProvider, Model: defaultModel},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		}
		cfg.Context = config.DefaultContextSettings()
		return NewAgentInstance(&cfg.Agents.List[0], &cfg.Agents.Defaults, cfg, nil)
	}

	t.Run("unknown default provider degrades the agent", func(t *testing.T) {
		needs, id := newInstance(t, "nope", "nope/x").needsProviderSnapshot()
		if !needs {
			t.Error("needsProvider = false — an unknown agents.defaults.default_model provider must degrade the agent")
		}
		if id != "nope" {
			t.Errorf("needsProviderID = %q, want %q", id, "nope")
		}
	})

	t.Run("healthy default provider leaves the agent alone", func(t *testing.T) {
		if needs, _ := newInstance(t, "openai", "gpt-5.2").needsProviderSnapshot(); needs {
			t.Error("needsProvider = true for a configured catalog default — nothing is degraded here")
		}
	})
}
