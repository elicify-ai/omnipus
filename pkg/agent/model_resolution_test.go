package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ADR-067 FR-040 (X-24) replaced this file's subject wholesale. The old rules
// — a `model_name` display alias, a `<protocol>/<model>` prefix split, and a
// passthrough fallback that re-prefixed ANY unmatched slug onto the first
// aggregator it found — are gone. Three rules remain:
//
//  1. an exact (provider, model) pair;
//  1b. a configured provider that OFFERS the model;
//  2. a bare model id offered by exactly ONE configured provider;
//  3. otherwise unresolved.
//
// The passthrough rung is the one worth remembering: it meant a typo'd or
// retired model id NEVER failed to resolve — it silently became an OpenRouter
// request, billed to the operator's OpenRouter key, for a model nobody chose.

// row builds a configured provider row keyed by a real catalog provider id.
func row(provider, model string) *config.ModelConfig {
	return &config.ModelConfig{
		Provider:  provider,
		Model:     model,
		APIKeyRef: "RESOLVE_TEST_KEY",
	}
}

func TestResolveModelCfg_EmptyProviders_ReturnsNotFound(t *testing.T) {
	cfg := &config.Config{}
	got, err := ResolveModelCfg(cfg, "gpt-4.1", "")
	if err == nil {
		t.Fatalf("expected error for empty providers, got %v", got)
	}
	if got != nil {
		t.Errorf("expected nil ModelConfig on miss, got %+v", got)
	}
}

// T24c — TestModelListResolver_PairExactThenUnique is the FR-040 rule set,
// asserted rung by rung.
func TestModelListResolver_PairExactThenUnique(t *testing.T) {
	t.Run("rule 1: the exact pair wins over any other row", func(t *testing.T) {
		cfg := &config.Config{Providers: []*config.ModelConfig{
			row("openrouter", "z-ai/glm-5.2"),
			row("zai", "glm-5.2"),
		}}
		mc, err := ResolveModelPairCfg(cfg, "zai", "glm-5.2", "")
		if err != nil {
			t.Fatalf("ResolveModelPairCfg: %v", err)
		}
		if mc.Provider != "zai" || mc.Model != "glm-5.2" {
			t.Errorf("resolved (%s, %s), want (zai, glm-5.2)", mc.Provider, mc.Model)
		}
	})

	t.Run("rule 1b: a configured provider that offers the model", func(t *testing.T) {
		cfg := &config.Config{Providers: []*config.ModelConfig{row("anthropic", "claude-haiku-4-5")}}
		mc, err := ResolveModelPairCfg(cfg, "anthropic", "claude-opus-4-5", "")
		if err != nil {
			t.Fatalf("a catalog model on a configured provider must resolve: %v", err)
		}
		if mc.Provider != "anthropic" || mc.Model != "claude-opus-4-5" {
			t.Errorf("resolved (%s, %s), want (anthropic, claude-opus-4-5)", mc.Provider, mc.Model)
		}
	})

	t.Run("rule 2: a bare id offered by exactly one provider", func(t *testing.T) {
		cfg := &config.Config{Providers: []*config.ModelConfig{
			row("openrouter", "z-ai/glm-5.2"),
			row("anthropic", "claude-haiku-4-5"),
		}}
		mc, err := ResolveModelCfg(cfg, "z-ai/glm-5.2", "")
		if err != nil {
			t.Fatalf("ResolveModelCfg: %v", err)
		}
		if mc.Provider != "openrouter" {
			t.Errorf("provider = %q, want openrouter", mc.Provider)
		}
		if mc.Model != "z-ai/glm-5.2" {
			t.Errorf("model = %q, want the full id verbatim — no prefix split", mc.Model)
		}
	})

	t.Run("rule 3: an ambiguous bare id stays unresolved", func(t *testing.T) {
		cfg := &config.Config{Providers: []*config.ModelConfig{
			row("openrouter", "shared-model"),
			row("groq", "shared-model"),
		}}
		if mc, err := ResolveModelCfg(cfg, "shared-model", ""); err == nil {
			t.Errorf("two providers offer it; resolving to %q is a guess, not an answer", mc.Provider)
		}
	})

	t.Run("rule 3: an unknown id is unresolved, never re-routed", func(t *testing.T) {
		cfg := &config.Config{Providers: []*config.ModelConfig{row("openrouter", "z-ai/glm-5.2")}}
		if mc, err := ResolveModelCfg(cfg, "no-such-model", ""); err == nil {
			t.Errorf("an unmatched id resolved to (%s, %s); the passthrough fallback is gone",
				mc.Provider, mc.Model)
		}
	})

	t.Run("no ModelName path: a row's display Name resolves nothing", func(t *testing.T) {
		mc := row("openai", "gpt-4.1")
		mc.Name = "my-favourite-model"
		cfg := &config.Config{Providers: []*config.ModelConfig{mc}}
		if got, err := ResolveModelCfg(cfg, "my-favourite-model", ""); err == nil {
			t.Errorf("the display alias resolved to (%s, %s); X-25 deleted that rung",
				got.Provider, got.Model)
		}
	})

	t.Run("a row on an unknown provider resolves nothing", func(t *testing.T) {
		cfg := &config.Config{Providers: []*config.ModelConfig{row("z-ai", "glm-5.2")}}
		if got, err := ResolveModelCfg(cfg, "glm-5.2", ""); err == nil {
			t.Errorf("a degraded row served the model as (%s, %s); it needs a provider first",
				got.Provider, got.Model)
		}
	})

	t.Run("a custom row is configured and resolves", func(t *testing.T) {
		mc := row("my-proxy", "house-model")
		mc.Custom = true
		mc.Protocol = "openai-compatible"
		mc.APIBase = "https://llm.example/v1"
		cfg := &config.Config{Providers: []*config.ModelConfig{mc}}
		got, err := ResolveModelCfg(cfg, "house-model", "")
		if err != nil {
			t.Fatalf("a custom row must resolve its own model: %v", err)
		}
		if got.Provider != "my-proxy" {
			t.Errorf("provider = %q, want my-proxy", got.Provider)
		}
	})
}

// TestResolveModelCfg_LocalRowUsesItsManualList — FR-040 rule 2's local half:
// the catalog carries no models for a local runtime, so the row's own
// `models[]` is the authority.
func TestResolveModelCfg_LocalRowUsesItsManualList(t *testing.T) {
	local := row("ollama", "llama3")
	local.Models = []string{"llama3", "mistral-7b"}
	cfg := &config.Config{Providers: []*config.ModelConfig{local}}

	for _, model := range []string{"llama3", "mistral-7b"} {
		mc, err := ResolveModelCfg(cfg, model, "")
		if err != nil {
			t.Fatalf("ResolveModelCfg(%q): %v", model, err)
		}
		if mc.Provider != "ollama" || mc.Model != model {
			t.Errorf("resolved (%s, %s), want (ollama, %s)", mc.Provider, mc.Model, model)
		}
	}
	if _, err := ResolveModelCfg(cfg, "not-pulled", ""); err == nil {
		t.Error("a model the local row does not list must not resolve")
	}
}

// TestResolveModelCfg_ClonesAndFillsWorkspace keeps the clone contract: the
// caller may mutate what it gets back.
func TestResolveModelCfg_ClonesAndFillsWorkspace(t *testing.T) {
	original := row("openai", "gpt-4.1")
	cfg := &config.Config{Providers: []*config.ModelConfig{original}}

	mc, err := ResolveModelCfg(cfg, "gpt-4.1", "/tmp/agent-home")
	if err != nil {
		t.Fatalf("ResolveModelCfg: %v", err)
	}
	if mc == original {
		t.Fatal("ResolveModelCfg returned the config's own row; callers mutate the result")
	}
	if mc.Home != "/tmp/agent-home" {
		t.Errorf("Home = %q, want the workspace filled in", mc.Home)
	}
	mc.Model = "MUTATED"
	if original.Model != "gpt-4.1" {
		t.Errorf("mutating the clone reached the config row (Model = %q)", original.Model)
	}
}

// TestBuildModelListResolver_MatchesResolveModelCfg — the UI selector and the
// chat runtime must answer the same question the same way, or the composer
// offers a model the runtime then refuses.
func TestBuildModelListResolver_MatchesResolveModelCfg(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{
		row("openrouter", "z-ai/glm-5.2"),
		row("anthropic", "claude-haiku-4-5"),
	}}
	resolve := buildModelListResolver(cfg)

	for _, model := range []string{"z-ai/glm-5.2", "claude-haiku-4-5"} {
		ref, ok := resolve(model)
		if !ok {
			t.Fatalf("buildModelListResolver(%q) returned false", model)
		}
		mc, err := ResolveModelCfg(cfg, model, "")
		if err != nil {
			t.Fatalf("ResolveModelCfg(%q): %v", model, err)
		}
		if ref.Provider != mc.Provider || ref.Model != mc.Model {
			t.Errorf("selector says (%s, %s), runtime says (%s, %s)",
				ref.Provider, ref.Model, mc.Provider, mc.Model)
		}
	}

	if ref, ok := resolve("no-such-model"); ok {
		t.Errorf("selector resolved an unknown id to (%s, %s)", ref.Provider, ref.Model)
	}
}

// TestResolveModelCandidatesFromList_PerEntryProvider — a fallback entry that
// pins its own provider is taken VERBATIM: no prefix split (FR-034), no
// re-resolution through the list.
func TestResolveModelCandidatesFromList_PerEntryProvider(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{row("openrouter", "z-ai/glm-5.2")}}
	fallbacks := []config.FallbackModel{
		{Provider: "anthropic", Model: "claude-haiku-4-5"},
		{Provider: "openrouter", Model: "z-ai/glm-5.2"},
	}

	cands := resolveModelCandidatesFromList(cfg, "openrouter", "z-ai/glm-5.2", fallbacks)
	if len(cands) != 2 {
		t.Fatalf("len(cands) = %d, want 2 (the primary and the anthropic fallback; the "+
			"second fallback duplicates the primary): %+v", len(cands), cands)
	}
	if cands[0].Provider != "openrouter" || cands[0].Model != "z-ai/glm-5.2" {
		t.Errorf("primary = (%s, %s), want (openrouter, z-ai/glm-5.2)", cands[0].Provider, cands[0].Model)
	}
	if cands[1].Provider != "anthropic" || cands[1].Model != "claude-haiku-4-5" {
		t.Errorf("fallback = (%s, %s), want (anthropic, claude-haiku-4-5)",
			cands[1].Provider, cands[1].Model)
	}
}

// TestResolveModelCandidatesFromList_SeededAgentStyle pins the bug this
// resolution path exists for: a primary whose model id is `z-ai/glm-5.2` — a
// VENDOR NAMESPACE inside an OpenRouter model id, not a provider — must
// produce a candidate whose Provider is `openrouter`. Splitting it produced
// `zai`, a provider nothing was configured for, and the agent loop then
// refused to start a turn.
func TestResolveModelCandidatesFromList_SeededAgentStyle(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{row("openrouter", "z-ai/glm-5.2")}}

	cands := resolveModelCandidatesFromList(cfg, "", "z-ai/glm-5.2", nil)
	if len(cands) != 1 {
		t.Fatalf("len(cands) = %d, want 1: %+v", len(cands), cands)
	}
	if cands[0].Provider != "openrouter" {
		t.Errorf("Provider = %q, want openrouter — the vendor namespace must not leak into it",
			cands[0].Provider)
	}
	if cands[0].Model != "z-ai/glm-5.2" {
		t.Errorf("Model = %q, want the full id verbatim", cands[0].Model)
	}
}

// TestResolveAgentCandidatesWithPrimaryProvider_PinsPrimary — an explicit
// primary provider pins the candidate directly, never inferred from the slug
// and never split on a slash.
func TestResolveAgentCandidatesWithPrimaryProvider_PinsPrimary(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{row("openrouter", "z-ai/glm-5.2")}}

	cands := resolveAgentCandidatesWithPrimaryProvider(
		cfg, "openrouter", "claude-sonnet-4-5", "anthropic", nil, nil)

	if len(cands) == 0 {
		t.Fatal("expected at least the pinned primary candidate")
	}
	if cands[0].Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic (the explicit provider is never inferred away)",
			cands[0].Provider)
	}
	if cands[0].Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q, want it verbatim under the pinned provider", cands[0].Model)
	}
}

// TestResolveAgentCandidatesWithPrimaryProvider_PinnedPrefixedModelNotSplit —
// the pinned path must not split a `/` out of the model id (FR-034).
func TestResolveAgentCandidatesWithPrimaryProvider_PinnedPrefixedModelNotSplit(t *testing.T) {
	cfg := &config.Config{Providers: []*config.ModelConfig{row("openrouter", "z-ai/glm-5.2")}}

	cands := resolveAgentCandidatesWithPrimaryProvider(
		cfg, "", "z-ai/glm-5.2", "openrouter", nil, nil)

	if len(cands) == 0 {
		t.Fatal("expected the pinned primary candidate")
	}
	if cands[0].Provider != "openrouter" || cands[0].Model != "z-ai/glm-5.2" {
		t.Errorf("candidate = (%s, %s), want (openrouter, z-ai/glm-5.2)",
			cands[0].Provider, cands[0].Model)
	}
}

// TestIsKnownModel — the SPA's "unresolved" chip asks the same question the
// resolver does, minus the uniqueness requirement.
func TestIsKnownModel(t *testing.T) {
	models := make([]*config.ModelConfig, 0, 3)
	models = append(models,
		row("openrouter", "z-ai/glm-5.2"),
		row("anthropic", "claude-haiku-4-5"),
	)
	local := row("ollama", "llama3")
	local.Models = []string{"llama3"}
	models = append(models, local)

	tests := []struct {
		name string
		slug string
		want bool
	}{
		{"row's own model", "z-ai/glm-5.2", true},
		{"another catalog model on a configured provider", "claude-opus-4-5", true},
		{"a local row's manual entry", "llama3", true},
		{"a model no configured provider offers", "no-such-model", false},
		// A-19: ids are compared exactly. Case is significant.
		{"different case is a different id", "Z-AI/GLM-5.2", false},
		{"empty", "", false},
		{"whitespace only", "   ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsKnownModel(tt.slug, models); got != tt.want {
				t.Errorf("IsKnownModel(%q) = %v, want %v", tt.slug, got, tt.want)
			}
		})
	}
}

func TestIsKnownModel_NilAndEmpty(t *testing.T) {
	if IsKnownModel("gpt-4.1", nil) {
		t.Error("IsKnownModel with no providers = true, want false")
	}
	ps := []*config.ModelConfig{nil, row("openai", "gpt-4.1")}
	if !IsKnownModel("gpt-4.1", ps) {
		t.Error("IsKnownModel(slice with a nil entry) = false, want true")
	}
}
