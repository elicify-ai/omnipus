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

// resolveModel is the thin boolean wrapper around ResolveModelCfg used by
// callers that only need the canonical wire form (not the matched Provider).
// It returns the canonical protocol-prefixed model name when found, or
// ("", false) on miss.
//
// For legacy entries that do NOT set the Provider field (model_name == model
// == bare slug), apply the historical ensureProtocol heuristic: bare slugs
// (no "/") get an implicit "openai/" prefix so downstream consumers that
// rely on ParseModelRef's slash-based provider inference see a non-empty
// provider. When the Provider field IS set (the modern, contract-first
// shape), return mc.Model verbatim — the spec (Dataset 1 row 6) requires
// the canonical name without an implicit prefix in that case.
func resolveModel(cfg *config.Config, modelName string) (string, bool) {
	r, ok := resolveModelRef(cfg, modelName)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(r.Provider) == "" {
		r.Model = ensureProtocol(r.Model)
	}
	return r.Model, true
}

// resolveModelRef returns the matched ModelConfig's canonical Model form
// AND its Provider. The Provider field of the returned ref is the matched
// entry's Provider (e.g. "openrouter"), not the slash-prefix from mc.Model
// (which may be a vendor namespace like "z-ai" and not a configured
// provider at all). This is the unified lookup used by both the candidate
// builder (which needs Provider) and the UI selector / wire-shape
// canonicalization paths (which need Model).
func resolveModelRef(cfg *config.Config, modelName string) (providers.ResolvedRef, bool) {
	mc, err := ResolveModelCfg(cfg, modelName, "")
	if err != nil || mc == nil {
		return providers.ResolvedRef{}, false
	}
	return providers.ResolvedRef{Model: mc.Model, Provider: mc.Provider}, true
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

// buildModelListResolver is the SINGLE source of truth for "what is the
// canonical (model, provider) pair for this slug?". It is used by the
// candidate builder (which needs the matched Provider) and the UI selector
// (which needs the canonical Model form). Both paths use the same lookup
// so a chat runtime mismatch (passthrough fallback only on the UI side)
// cannot happen.
//
// The returned closure yields a providers.ResolvedRef so the candidate
// builder can set the candidate's Provider to the configured provider that
// owns the credentials — not the slash-prefix from mc.Model, which may be
// a vendor namespace like "z-ai" with no matching provider entry.
func buildModelListResolver(cfg *config.Config) func(raw string) (providers.ResolvedRef, bool) {
	return func(raw string) (providers.ResolvedRef, bool) {
		return resolveModelRef(cfg, raw)
	}
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

// resolveModelCandidatesForAgent picks the right candidate resolver based on
// what the agent has populated. When the agent carries FallbackModels (the
// modern provider-aware chain — FR-005 / FR-007), it routes each fallback
// through its own provider via resolveModelCandidatesFromList. Otherwise it
// falls back to the legacy string-based resolver so configs that predate the
// new wire shape keep their original behavior.
func resolveModelCandidatesForAgent(
	cfg *config.Config,
	defaultProvider string,
	primary string,
	agent *AgentInstance,
) []providers.FallbackCandidate {
	if agent == nil {
		return resolveModelCandidates(cfg, defaultProvider, primary, nil)
	}
	if len(agent.FallbackModels) > 0 {
		return resolveModelCandidatesFromList(cfg, defaultProvider, primary, agent.FallbackModels)
	}
	return resolveModelCandidates(cfg, defaultProvider, primary, agent.Fallbacks)
}

// resolveModelCandidatesFromList is the provider-aware variant of
// resolveModelCandidates. Each fallback entry may carry its own Provider
// (FR-005 / FR-007) so a fallback can route through a different provider than
// the primary. The primary is still resolved through the model_list lookup so
// a bare alias (e.g. "step-3.5-flash") is expanded to its full protocol-prefixed
// form; fallback entries bypass the lookup when Provider is set because the
// agent has explicitly pinned the route.
//
// An entry with Provider == "" falls back to the model_list lookup so legacy
// [string] entries normalized via config.NormalizeFallbacks continue to work
// at the agent layer. After lookup, the entry is treated as a bare slug routed
// through defaultProvider — same behavior as resolveModelCandidates.
//
// Order is preserved: primary first, then fallbacks in declared order.
// Duplicates (same provider+model) are collapsed.
func resolveModelCandidatesFromList(
	cfg *config.Config,
	defaultProvider string,
	primary string,
	fallbacks []config.FallbackModel,
) []providers.FallbackCandidate {
	resolver := buildModelListResolver(cfg)
	seen := make(map[string]bool)
	var out []providers.FallbackCandidate

	addPrimary := func(raw string) {
		candidateRaw := strings.TrimSpace(raw)
		if candidateRaw == "" {
			return
		}
		// When the resolver matches, use the matched entry's Provider as the
		// candidate's Provider — this is the fix for the seeded-agent bug
		// where mc.Model == "z-ai/glm-5.2" used to leak as Provider: "zai"
		// into the candidate. The matched entry's Provider is the
		// configured provider (e.g. "openrouter") that owns the credentials.
		if resolver != nil {
			if resolved, ok := resolver(candidateRaw); ok {
				key := providers.ModelKey(resolved.Provider, resolved.Model)
				if seen[key] {
					return
				}
				seen[key] = true
				out = append(out, providers.FallbackCandidate{Provider: resolved.Provider, Model: resolved.Model})
				return
			}
		}
		// No match in model_list — fall back to ParseModelRef with
		// defaultProvider (legacy path for entries not in cfg.Providers).
		ref := providers.ParseModelRef(candidateRaw, defaultProvider)
		if ref == nil {
			return
		}
		key := providers.ModelKey(ref.Provider, ref.Model)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, providers.FallbackCandidate{Provider: ref.Provider, Model: ref.Model})
	}

	addFallback := func(fb config.FallbackModel) {
		candidateRaw := strings.TrimSpace(fb.Model)
		if candidateRaw == "" {
			return
		}
		// When the entry has a pinned Provider, use it directly — bypass the
		// model_list lookup so a non-passthrough provider is honored even when
		// the configured providers only include a passthrough (FR-007).
		// NormalizeFallbacks has already populated Provider for legacy string
		// entries against the configured providers, so an empty Provider here
		// means "no configured provider matched" — fall back to the chat-side
		// resolver so the candidate at least surfaces a sensible default.
		if strings.TrimSpace(fb.Provider) != "" {
			ref := providers.ParseModelRef(candidateRaw, fb.Provider)
			if ref == nil {
				return
			}
			key := providers.ModelKey(ref.Provider, ref.Model)
			if seen[key] {
				return
			}
			seen[key] = true
			out = append(out, providers.FallbackCandidate{Provider: ref.Provider, Model: ref.Model})
			return
		}
		// No pinned provider — use the resolver like the primary does.
		if resolver != nil {
			if resolved, ok := resolver(candidateRaw); ok {
				key := providers.ModelKey(resolved.Provider, resolved.Model)
				if seen[key] {
					return
				}
				seen[key] = true
				out = append(out, providers.FallbackCandidate{Provider: resolved.Provider, Model: resolved.Model})
				return
			}
		}
		ref := providers.ParseModelRef(candidateRaw, defaultProvider)
		if ref == nil {
			return
		}
		key := providers.ModelKey(ref.Provider, ref.Model)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, providers.FallbackCandidate{Provider: ref.Provider, Model: ref.Model})
	}

	addPrimary(primary)
	for _, fb := range fallbacks {
		addFallback(fb)
	}
	return out
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

// IsKnownModel reports whether `slug` corresponds to a model that at least
// one configured provider exposes. It is the validation half of W6-C4 / G12:
// the SPA lets users type arbitrary model slugs (free-text) into the
// AgentProfile model picker, but the runtime will fail any chat call against
// a slug no provider actually supports. This helper is the authoritative
// answer for "is this slug resolvable?" so the SPA can show a persistent
// "unresolved" indicator (the TS twin lives in
// `src/lib/agents/model-validation.ts` and MUST stay in sync).
//
// Matching rules (mirrors `resolveModel` so the UI's unresolved chip and the
// chat runtime agree on what "known" means):
//
//  1. Empty / whitespace slug → false (no model to validate).
//  2. Exact match against `mc.Model` (the protocol-prefixed form, e.g.
//     "openai/gpt-4o"). Case-insensitive on both sides.
//  3. Match against the model ID extracted from `mc.Model` via
//     `providers.ExtractProtocol` — catches bare slugs ("gpt-4o") when the
//     entry stores the protocol-prefixed form ("openai/gpt-4o"). Same case
//     insensitivity.
//  4. Exact match against `mc.ModelName` (the user-facing alias). Same
//     case insensitivity.
//
// A passthrough provider (openrouter / vivgrid) does NOT auto-validate
// arbitrary slugs — `IsKnownModel` is the conservative "explicitly
// configured" check; the SPA's free-text "Use <slug>" path still works, but
// the unresolved chip is the user-facing signal that the runtime may not
// have a route. That matches G12's product intent ("show a warning; don't
// block the save").
func IsKnownModel(slug string, models []*config.ModelConfig) bool {
	needle := strings.ToLower(strings.TrimSpace(slug))
	if needle == "" {
		return false
	}
	for _, mc := range models {
		if mc == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(mc.Model)) == needle {
			return true
		}
		if _, modelID := providers.ExtractProtocol(
			strings.TrimSpace(mc.Model),
		); strings.ToLower(
			strings.TrimSpace(modelID),
		) == needle {
			return true
		}
		if strings.ToLower(strings.TrimSpace(mc.ModelName)) == needle {
			return true
		}
	}
	return false
}
