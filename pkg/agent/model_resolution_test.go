package agent

import (
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// Dataset 1 from phase-1 spec §11 / TDD rows 1–5. The single underlying
// resolver MUST return the same canonical model name from both the UI
// selector path (buildModelListResolver) and the chat runtime path
// (resolvedModelConfig → CreateProviderFromConfig). Traces to FR-003.

// TestResolveModelCfg_EmptyProviders_ReturnsNotFound covers Dataset 1 row 1
// (BDD-21): no providers, any model resolves to false.
func TestResolveModelCfg_EmptyProviders_ReturnsNotFound(t *testing.T) {
	cfg := &config.Config{}
	got, err := ResolveModelCfg(cfg, "gpt-4o", "")
	if err == nil {
		t.Fatalf("expected error for empty providers, got %v", got)
	}
	if got != nil {
		t.Errorf("expected nil ModelConfig on miss, got %+v", got)
	}
}

// TestResolveModelCfg_ExactMatchInProviders covers Dataset 1 row 2 (BDD-5):
// model_name="gpt-4o" with Model="openai/gpt-4o" resolves to the canonical
// protocol-prefixed form.
func TestResolveModelCfg_ExactMatchInProviders(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "gpt-4o",
				Model:     "openai/gpt-4o",
				Provider:  "openai",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "RESOLVE_KEY",
			},
		},
	}
	mc, err := ResolveModelCfg(cfg, "gpt-4o", "")
	if err != nil {
		t.Fatalf("ResolveModelCfg: %v", err)
	}
	if mc.Model != "openai/gpt-4o" {
		t.Errorf("mc.Model = %q, want %q", mc.Model, "openai/gpt-4o")
	}
}

// TestResolveModelCfg_PassthroughProviderFallback covers Dataset 1 row 3
// (BDD-4): user picks "z-ai/glm-5-turbo" from the live list. The slug isn't
// its own provider entry but openrouter accepts arbitrary slugs; the resolver
// MUST prefix the input with the passthrough provider so the runtime can
// route it via the openrouter credentials.
func TestResolveModelCfg_PassthroughProviderFallback(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
		},
	}
	mc, err := ResolveModelCfg(cfg, "z-ai/glm-5-turbo", "")
	if err != nil {
		t.Fatalf("ResolveModelCfg: %v", err)
	}
	if mc.Model != "openrouter/z-ai/glm-5-turbo" {
		t.Errorf("mc.Model = %q, want %q", mc.Model, "openrouter/z-ai/glm-5-turbo")
	}
	if mc.Provider != "openrouter" {
		t.Errorf("mc.Provider = %q, want openrouter (clone must preserve passthrough provider)", mc.Provider)
	}
	// The passthrough clone MUST inherit credentials from the provider entry —
	// otherwise CreateProviderFromConfig has no API base / key to talk to.
	if mc.APIBase != "https://openrouter.ai/api/v1" {
		t.Errorf("mc.APIBase = %q, want passthrough provider's APIBase", mc.APIBase)
	}
	if mc.APIKeyRef != "OPENROUTER_KEY" {
		t.Errorf("mc.APIKeyRef = %q, want passthrough provider's APIKeyRef", mc.APIKeyRef)
	}
}

// TestResolveModelCfg_OpenaiPrefixWithoutOpenaiProvider_ReturnsFalse covers
// Dataset 1 row 4 (BDD-23): an input with an explicit "openai/" prefix MUST
// NOT be re-prefixed by the passthrough fallback when no openai provider is
// configured. The original buildModelListResolver silently re-prefixed it as
// "openrouter/openai/gpt-4o", which was the bug the spec wants fixed.
func TestResolveModelCfg_OpenaiPrefixWithoutOpenaiProvider_ReturnsFalse(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
		},
	}
	mc, err := ResolveModelCfg(cfg, "openai/gpt-4o", "")
	if err == nil {
		t.Fatalf("expected error for explicit openai/ prefix without openai provider, got %+v", mc)
	}
	if mc != nil {
		t.Errorf("expected nil ModelConfig on miss, got %+v", mc)
	}
}

// TestResolveModelCfg_MultipleProviders_PassthroughSelectsCorrect covers
// Dataset 1 row 5 (BDD-4 / BDD-8): with both openai and openrouter configured,
// passthrough fallback MUST select openrouter (not openai), and the openai
// provider's credentials MUST NOT leak into the resolved config.
func TestResolveModelCfg_MultipleProviders_PassthroughSelectsCorrect(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "gpt-4o",
				Model:     "gpt-4o",
				Provider:  "openai",
				APIBase:   "https://api.openai.com/v1",
				APIKeyRef: "OPENAI_KEY",
			},
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
		},
	}
	mc, err := ResolveModelCfg(cfg, "z-ai/glm-5-turbo", "")
	if err != nil {
		t.Fatalf("ResolveModelCfg: %v", err)
	}
	if mc.Model != "openrouter/z-ai/glm-5-turbo" {
		t.Errorf("mc.Model = %q, want %q", mc.Model, "openrouter/z-ai/glm-5-turbo")
	}
	if mc.APIKeyRef != "OPENROUTER_KEY" {
		t.Errorf("mc.APIKeyRef = %q, want OPENROUTER_KEY (passthrough must not steal openai creds)", mc.APIKeyRef)
	}
}

// TestResolveModelCfg_MultipleProviders_ExactMatchInOne covers Dataset 1
// row 6 (BDD-5): with multiple providers configured, an exact model_name
// match in one provider resolves to that provider's entry — passthrough
// fallback MUST NOT shadow it.
func TestResolveModelCfg_MultipleProviders_ExactMatchInOne(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "gpt-4o",
				Model:     "gpt-4o",
				Provider:  "openai",
				APIBase:   "https://api.openai.com/v1",
				APIKeyRef: "OPENAI_KEY",
			},
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
		},
	}
	mc, err := ResolveModelCfg(cfg, "gpt-4o", "")
	if err != nil {
		t.Fatalf("ResolveModelCfg: %v", err)
	}
	if mc.Model != "gpt-4o" {
		t.Errorf("mc.Model = %q, want %q (entry's own Model field, unprefixed)", mc.Model, "gpt-4o")
	}
	if mc.Provider != "openai" {
		t.Errorf("mc.Provider = %q, want openai (must NOT be hijacked by openrouter passthrough)", mc.Provider)
	}
	if mc.APIKeyRef != "OPENAI_KEY" {
		t.Errorf("mc.APIKeyRef = %q, want OPENAI_KEY", mc.APIKeyRef)
	}
}

// TestBuildModelListResolver_MatchesResolveModelCfg is the regression guard
// for FR-003: the UI selector helper and the chat runtime helper MUST agree
// on every input. Pre-refactor, the two paths diverged — `buildModelListResolver`
// had the passthrough fallback, `resolvedModelConfig` did not. After the
// refactor, both MUST resolve a model name to the same canonical string.
func TestBuildModelListResolver_MatchesResolveModelCfg(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "gpt-4o",
				Model:     "openai/gpt-4o",
				Provider:  "openai",
				APIBase:   "https://api.openai.com/v1",
				APIKeyRef: "OPENAI_KEY",
			},
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
		},
	}

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"exact_match_openai", "gpt-4o", "openai/gpt-4o"},
		{"passthrough_openrouter", "z-ai/glm-5-turbo", "openrouter/z-ai/glm-5-turbo"},
		{"unprefixed_openai_entry", "openai/gpt-4o", "openai/gpt-4o"},
	}

	resolver := buildModelListResolver(cfg)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui, ok := resolver(tc.input)
			if !ok {
				t.Fatalf("buildModelListResolver(%q) returned false", tc.input)
			}
			mc, err := ResolveModelCfg(cfg, tc.input, "")
			if err != nil {
				t.Fatalf("ResolveModelCfg(%q): %v", tc.input, err)
			}
			if ui != mc.Model {
				t.Errorf("path divergence on %q: ui=%q, chat=%q — FR-003 violated", tc.input, ui, mc.Model)
			}
			if ui != tc.want {
				t.Errorf("got %q, want %q", ui, tc.want)
			}
		})
	}
}

// TestBuildModelListResolver_RejectsPrefixedPassthrough confirms the UI
// selector path also enforces the same "no blind re-prefixing" rule that
// ResolveModelCfg enforces (Dataset 1 row 4 — the bug being fixed).
func TestBuildModelListResolver_RejectsPrefixedPassthrough(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
		},
	}
	resolver := buildModelListResolver(cfg)
	got, ok := resolver("openai/gpt-4o")
	if ok {
		t.Errorf("expected false for openai/gpt-4o without openai provider, got (%q, true)", got)
	}
	// Sanity: the helper must NOT have invented "openrouter/openai/gpt-4o".
	if got != "" && strings.Contains(got, "openai/gpt-4o") && !strings.HasPrefix(got, "openai/") {
		t.Errorf("passthrough should not have re-prefixed openai/gpt-4o, got %q", got)
	}
}
