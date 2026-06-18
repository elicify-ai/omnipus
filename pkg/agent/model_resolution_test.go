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

// TestResolveModelCandidatesFromList_PerEntryProvider locks in the FR-007
// contract: each fallback in the [{model, provider}] form must yield a
// candidate whose Provider equals the explicit provider, NOT the agent's
// default provider. Without this, a fallback that the operator pinned to a
// different provider (e.g. anthropic when primary is openrouter) silently
// re-routes through the primary's credentials at LLM-call time.
//
// The primary is resolved through the model_list lookup (it has no explicit
// Provider on the FallbackModel entry); the fallback bypasses the lookup
// because its Provider is pinned — the operator's exact intent.
func TestResolveModelCandidatesFromList_PerEntryProvider(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "gpt-5",
				Model:     "openai/gpt-5",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
			{
				ModelName: "claude-haiku",
				Model:     "anthropic/claude-haiku-4-5",
				Provider:  "anthropic",
				APIBase:   "https://api.anthropic.com/v1",
				APIKeyRef: "ANTHROPIC_KEY",
			},
		},
	}

	fallbacks := []config.FallbackModel{
		{Model: "claude-haiku", Provider: "anthropic"},
	}

	candidates := resolveModelCandidatesFromList(cfg, "openrouter", "gpt-5", fallbacks)

	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2 (primary + 1 explicit-provider fallback)", len(candidates))
	}
	// Primary is resolved via cfg.GetModelConfig(ModelName="gpt-5") which
	// returns the entry's Model verbatim ("openai/gpt-5") — parsed into
	// Provider=openai, Model=gpt-5.
	if candidates[0].Provider != "openai" || candidates[0].Model != "gpt-5" {
		t.Errorf("candidates[0] = %s/%s, want openai/gpt-5", candidates[0].Provider, candidates[0].Model)
	}
	// Fallback MUST carry its own Provider — anthropic, NOT openrouter.
	// This is the FR-007 invariant the bug was violating.
	if candidates[1].Provider != "anthropic" {
		t.Errorf("candidates[1].Provider = %q, want %q (FR-007: fallback must use its own provider)",
			candidates[1].Provider, "anthropic")
	}
	// Because the fallback has a pinned Provider, the resolver is bypassed:
	// the Model is taken verbatim from the entry. The operator wrote the
	// model slug the way their provider expects it — we don't second-guess.
	if candidates[1].Model != "claude-haiku" {
		t.Errorf("candidates[1].Model = %q, want %q (explicit Provider: model is verbatim)",
			candidates[1].Model, "claude-haiku")
	}
}

// TestResolveModelCandidatesFromList_PassthroughProviderHonored confirms that
// when the only configured provider is openrouter, a fallback declared as
// {model, provider:"anthropic"} STILL routes through anthropic — the
// provider is honored even though no passthrough match exists for the slug.
func TestResolveModelCandidatesFromList_PassthroughProviderHonored(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "gpt-5",
				Model:     "openai/gpt-5",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_KEY",
			},
		},
	}

	fallbacks := []config.FallbackModel{
		{Model: "claude-haiku-4-5", Provider: "anthropic"},
	}

	candidates := resolveModelCandidatesFromList(cfg, "openrouter", "gpt-5", fallbacks)

	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}
	// Primary: matched via cfg.GetModelConfig by ModelName "gpt-5"; the
	// entry's Model is "openai/gpt-5" → parsed into Provider=openai,
	// Model=gpt-5. The primary's "openrouter" provider field is a routing
	// hint for credential resolution, not what appears in the candidate's
	// Provider field (which comes from the model-ref prefix).
	if candidates[0].Provider != "openai" {
		t.Errorf("candidates[0].Provider = %q, want openai", candidates[0].Provider)
	}
	if candidates[0].Model != "gpt-5" {
		t.Errorf("candidates[0].Model = %q, want gpt-5", candidates[0].Model)
	}
	// Fallback via anthropic — explicit Provider overrides any passthrough
	// that might match the slug.
	if candidates[1].Provider != "anthropic" {
		t.Errorf("candidates[1].Provider = %q, want anthropic (explicit provider wins over passthrough)",
			candidates[1].Provider)
	}
	if candidates[1].Model != "claude-haiku-4-5" {
		t.Errorf("candidates[1].Model = %q, want claude-haiku-4-5", candidates[1].Model)
	}
}

// TestResolveModelCandidatesForAgent_PicksFallbackModelsWhenSet asserts the
// runtime helper used by ApplyAgentModel: when the agent instance carries
// FallbackModels, the per-entry-provider resolver is selected; when only the
// legacy Fallbacks []string is set, the historical resolver is selected.
func TestResolveModelCandidatesForAgent_PicksFallbackModelsWhenSet(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{ModelName: "gpt-5", Model: "openai/gpt-5", Provider: "openrouter", APIBase: "https://openrouter.ai/api/v1"},
			{ModelName: "haiku", Model: "anthropic/claude-haiku-4-5", Provider: "anthropic", APIBase: "https://api.anthropic.com/v1"},
		},
	}

	// Modern wire shape: fallback carries its own Provider.
	modern := &AgentInstance{
		Model: "gpt-5",
		FallbackModels: []config.FallbackModel{
			{Model: "haiku", Provider: "anthropic"},
		},
	}
	cands := resolveModelCandidatesForAgent(cfg, "openrouter", "gpt-5", modern)
	if len(cands) != 2 || cands[1].Provider != "anthropic" {
		t.Fatalf("modern agent: cands = %+v, want 2 entries with [1].Provider=anthropic", cands)
	}

	// Legacy wire shape: agent carries only Fallbacks []string.
	// The legacy resolver runs the bare slug "haiku" through the model_list
	// lookup first. "haiku" is registered as a ModelName → entry's Model
	// "anthropic/claude-haiku-4-5". ParseModelRef parses that into
	// {Provider:anthropic, Model:claude-haiku-4-5}. So the legacy path ALSO
	// ends up with the anthropic provider in this case — that's a happy
	// coincidence of how the entry was authored, not a guarantee. The
	// contract is "legacy preserves old behavior"; modern is "fallback uses
	// its own provider" which the explicit-Provider path guarantees
	// regardless of how the entry is authored.
	legacy := &AgentInstance{
		Model:     "gpt-5",
		Fallbacks: []string{"haiku"},
	}
	cands = resolveModelCandidatesForAgent(cfg, "openrouter", "gpt-5", legacy)
	if len(cands) != 2 {
		t.Fatalf("legacy agent: cands = %d, want 2", len(cands))
	}

	// Nil agent → legacy resolver with no fallbacks → only the primary.
	cands = resolveModelCandidatesForAgent(cfg, "openrouter", "gpt-5", nil)
	if len(cands) != 1 || cands[0].Model != "gpt-5" {
		t.Fatalf("nil agent: cands = %+v, want 1 entry {*,gpt-5}", cands)
	}
}

// TestIsKnownModel covers W6-C4 / G12 — the catalog helper that lets the SPA
// show a persistent "unresolved" indicator when a user types a free-text
// model slug that no configured provider exposes. The TS twin in
// src/lib/agents/model-validation.ts MUST mirror these cases.
func TestIsKnownModel(t *testing.T) {
	cfg := &config.Config{
		Providers: []*config.ModelConfig{
			{
				ModelName: "claude-haiku",
				Model:     "anthropic/claude-haiku-4-5",
				Provider:  "anthropic",
			},
			{
				ModelName: "gpt-4o",
				Model:     "openai/gpt-4o",
				Provider:  "openai",
			},
			{
				ModelName: "z-ai/glm-5.2",
				Model:     "z-ai/glm-5.2",
				Provider:  "openrouter",
			},
		},
	}

	cases := []struct {
		name string
		slug string
		want bool
	}{
		// Empty / whitespace → false (nothing to validate).
		{"empty", "", false},
		{"whitespace", "   ", false},

		// Exact match against mc.Model (protocol-prefixed form).
		{"exact-protocol-prefixed", "anthropic/claude-haiku-4-5", true},
		// Case-insensitive.
		{"case-insensitive-protocol", "Anthropic/Claude-Haiku-4-5", true},

		// Bare slug extracted from mc.Model via providers.ExtractProtocol.
		{"bare-slug-from-protocol", "claude-haiku-4-5", true},
		{"bare-slug-from-protocol-case", "GPT-4O", true},

		// Match against mc.ModelName (user-facing alias).
		{"model-name", "claude-haiku", true},

		// None of the providers expose this slug → false.
		{"unknown-bare", "gpt-9000-ultra", false},
		{"unknown-prefix", "fake-provider/some-model", false},

		// Passthrough provider's ModelName happens to contain a slash; that
		// shouldn't fool the validator when the lookup is against an
		// entirely different slug.
		{"passthrough-non-match", "z-ai/glm-5-turbo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsKnownModel(tc.slug, cfg.Providers)
			if got != tc.want {
				t.Errorf("IsKnownModel(%q) = %v, want %v", tc.slug, got, tc.want)
			}
		})
	}
}

// TestIsKnownModel_NilAndEmpty makes sure the helper doesn't panic on the
// edge cases the gateway encounters when /providers has never been seeded
// (fresh install, prior to onboarding).
func TestIsKnownModel_NilAndEmpty(t *testing.T) {
	if IsKnownModel("gpt-4o", nil) {
		t.Errorf("IsKnownModel(nil providers) = true, want false")
	}
	if IsKnownModel("gpt-4o", []*config.ModelConfig{}) {
		t.Errorf("IsKnownModel(empty providers) = true, want false")
	}
	// Nil entry inside the slice is skipped, not panicked on.
	ps := []*config.ModelConfig{nil, {Model: "openai/gpt-4o", Provider: "openai"}}
	if !IsKnownModel("openai/gpt-4o", ps) {
		t.Errorf("IsKnownModel(slice with nil entry) = false, want true")
	}
}
