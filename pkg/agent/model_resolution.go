package agent

import (
	"fmt"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// ResolveModelCfg resolves a model name to a *config.ModelConfig that is ready
// to be handed to providers.CreateProviderFromConfig. It is the single source
// of truth for "which ModelConfig does this name refer to?" used by both the
// chat runtime (ApplyAgentModel → CreateProviderFromConfig) and the UI
// selector helper (buildModelListResolver).
//
// Resolution order (per phase-1 spec §11 Dataset 1 + FR-003):
//  1. Exact match against cfg.GetModelConfig (looks up cfg.Providers by
//     ModelName). This is the historical path.
//  2. Exact match against cfg.Providers[i].Model (the protocol-prefixed form).
//  3. Match against the model ID extracted from cfg.Providers[i].Model via
//     providers.ExtractProtocol — catches slugs like "gpt-4o" when the entry
//     has Model="openai/gpt-4o".
//  4. Passthrough fallback: if NO match found AND the input has no provider
//     prefix (no "/"), and a passthrough provider (openrouter / vivgrid) is
//     configured, prepend the passthrough provider name to the input and
//     return a clone of that provider's ModelConfig (so credentials and
//     APIBase are inherited).
//
// The "no slash" guard on step 4 prevents the original bug from Dataset 1
// row 4: a deliberate "openai/gpt-4o" input MUST NOT be silently re-prefixed
// to "openrouter/openai/gpt-4o" when no openai provider is configured — the
// caller is explicitly asking for a provider that doesn't exist.
//
// Returns an error when no match is found; on error the returned
// *config.ModelConfig is nil. On success the returned config is a CLONE —
// callers may mutate it (e.g. setting Workspace) without affecting the
// provider entry in cfg.
func ResolveModelCfg(cfg *config.Config, modelName, workspace string) (*config.ModelConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	raw := strings.TrimSpace(modelName)
	if raw == "" {
		return nil, fmt.Errorf("model name is required")
	}

	// 1. Direct match via cfg.GetModelConfig (matches by ModelName).
	if mc, err := cfg.GetModelConfig(raw); err == nil && mc != nil {
		return cloneWithWorkspace(mc, workspace), nil
	}

	// 2 & 3. Direct model-field or unprefixed-modelID match against each
	// provider entry.
	for i := range cfg.Providers {
		full := strings.TrimSpace(cfg.Providers[i].Model)
		if full == "" {
			continue
		}
		if full == raw {
			return cloneWithWorkspace(cfg.Providers[i], workspace), nil
		}
		if _, modelID := providers.ExtractProtocol(full); modelID == raw {
			return cloneWithWorkspace(cfg.Providers[i], workspace), nil
		}
	}

	// 4. Passthrough fallback. A passthrough provider (openrouter, vivgrid)
	// routes arbitrary slugs through its own API, so when the input looks
	// like a model slug (no slash, OR a slash whose prefix isn't a known
	// provider name) we route it through the first matching passthrough
	// provider. We MUST NOT hijack an input that explicitly names a provider
	// that doesn't exist — e.g. "openai/gpt-4o" when no openai provider is
	// configured (Dataset 1 row 4 / BDD-23).
	if looksLikeBareModelSlug(raw) {
		for i := range cfg.Providers {
			provName := strings.TrimSpace(cfg.Providers[i].Provider)
			if provName == "" {
				continue
			}
			if providers.IsPassthroughProvider(provName, cfg.Providers[i].APIBase) {
				clone := cloneWithWorkspace(cfg.Providers[i], workspace)
				clone.Model = provName + "/" + raw
				// Clone already carries the provider's own Provider field; keep
				// it so CreateProviderFromConfig routes via the right backend.
				return clone, nil
			}
		}
	}

	return nil, fmt.Errorf("model %q not found in model_list or providers", raw)
}

// cloneWithWorkspace returns a pointer to a fresh copy of src with Workspace
// filled in when src left it empty. The dereference-and-take-address pattern
// (vs. a deep copy) is intentional: ModelConfig holds scalar fields and
// pointers; the inner pointer targets (e.g. Subagents) are deliberately shared
// with the underlying cfg.Providers entry so a model change does NOT silently
// rewrite the agent's subagent wiring.
func cloneWithWorkspace(src *config.ModelConfig, workspace string) *config.ModelConfig {
	if src == nil {
		return nil
	}
	clone := *src
	if clone.Workspace == "" {
		clone.Workspace = workspace
	}
	return &clone
}

// resolveModel is the thin boolean wrapper around ResolveModelCfg used by the
// UI selector. It returns the canonical protocol-prefixed model name when
// found, or ("", false) on miss.
//
// For legacy entries that do NOT set the Provider field (model_name == model
// == bare slug), apply the historical ensureProtocol heuristic: bare slugs
// (no "/") get an implicit "openai/" prefix so downstream consumers that
// rely on ParseModelRef's slash-based provider inference see a non-empty
// provider. When the Provider field IS set (the modern, contract-first
// shape), return mc.Model verbatim — the spec (Dataset 1 row 6) requires
// the canonical name without an implicit prefix in that case.
func resolveModel(cfg *config.Config, modelName string) (string, bool) {
	mc, err := ResolveModelCfg(cfg, modelName, "")
	if err != nil || mc == nil {
		return "", false
	}
	if strings.TrimSpace(mc.Provider) == "" {
		mc.Model = ensureProtocol(mc.Model)
	}
	return mc.Model, true
}

// ensureProtocol is the historical helper that gave bare slugs an implicit
// "openai/" prefix. Preserved for backward compatibility with legacy
// provider entries that don't set the Provider field. New entries should
// set Provider explicitly so this heuristic is not needed.
func ensureProtocol(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.Contains(model, "/") {
		return model
	}
	return "openai/" + model
}

// buildModelListResolver returns a closure the UI selector uses to ask
// "can this slug be used as a model?". After the phase-1 refactor (FR-003),
// it delegates to resolveModel so the chat runtime and the UI selector agree
// on every input — the historical divergence (passthrough fallback only on
// the UI side) is closed.
func buildModelListResolver(cfg *config.Config) func(raw string) (string, bool) {
	return func(raw string) (string, bool) {
		return resolveModel(cfg, raw)
	}
}

// isPassthroughProvider reports whether the given provider type forwards model
// slugs to its backend without per-slug registration. OpenRouter is the
// canonical example.
func isPassthroughProvider(provider, apiBase string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openrouter", "vivgrid":
		return true
	}
	return strings.Contains(strings.ToLower(apiBase), "openrouter.ai")
}

// knownProviderPrefixes is the conservative list of slash-prefixes that mean
// "explicit provider request" (not a model slug). When the input is
// "<prefix>/<model>" and the prefix matches one of these (case-insensitive),
// the resolver MUST NOT passthrough-rewrite it; the caller is asking for a
// specific provider. This catches the Dataset 1 row 4 case ("openai/gpt-4o"
// when no openai provider is configured) even when the only configured
// provider is a passthrough that would otherwise blindly re-prefix the input.
var knownProviderPrefixes = map[string]struct{}{
	"openai":            {},
	"openai-compatible": {},
	"anthropic":         {},
	"azure":             {},
	"azure-openai":      {},
	"openrouter":        {},
	"vivgrid":           {},
	"google":            {},
	"gemini":            {},
	"bedrock":           {},
	"cohere":            {},
	"mistral":           {},
	"groq":              {},
	"deepseek":          {},
	"xai":               {},
	"perplexity":        {},
}

// looksLikeBareModelSlug decides whether the input is safe to pass through a
// passthrough provider. It returns false when the input names a provider
// explicitly (e.g. "openai/gpt-4o" — the caller is asking for openai); it
// returns true when the input is unprefixed ("glm-5-turbo") or has a slash
// whose prefix is a model vendor, not a provider (e.g. "z-ai/glm-5-turbo"
// — "z-ai" is the model vendor, not a provider).
func looksLikeBareModelSlug(input string) bool {
	if !strings.Contains(input, "/") {
		return true
	}
	prefix, _, _ := strings.Cut(input, "/")
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return true
	}
	if _, ok := knownProviderPrefixes[prefix]; ok {
		return false
	}
	return true
}

func resolveModelCandidates(
	cfg *config.Config,
	defaultProvider string,
	primary string,
	fallbacks []string,
) []providers.FallbackCandidate {
	return providers.ResolveCandidatesWithLookup(
		providers.ModelConfig{
			Primary:   primary,
			Fallbacks: fallbacks,
		},
		defaultProvider,
		buildModelListResolver(cfg),
	)
}

func resolvedCandidateModel(candidates []providers.FallbackCandidate, fallback string) string {
	if len(candidates) > 0 && strings.TrimSpace(candidates[0].Model) != "" {
		return candidates[0].Model
	}
	return fallback
}

func resolvedCandidateProvider(candidates []providers.FallbackCandidate, fallback string) string {
	if len(candidates) > 0 && strings.TrimSpace(candidates[0].Provider) != "" {
		return candidates[0].Provider
	}
	return fallback
}

// resolvedModelConfig resolves a model name to a *config.ModelConfig usable by
// the chat runtime. After the phase-1 refactor (FR-003) it delegates to the
// shared ResolveModelCfg so the chat runtime and the UI selector agree on
// passthrough fallback and prefix-handling rules.
func resolvedModelConfig(cfg *config.Config, modelName, workspace string) (*config.ModelConfig, error) {
	return ResolveModelCfg(cfg, modelName, workspace)
}
