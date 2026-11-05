package agent

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/modelresolve"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ── FR-040: how a model reference resolves to a configured row ───────────────
//
// The rules live in pkg/modelresolve (shared with get_agent's model_chain,
// r17 §7.5 AG-16); these are this package's names for them.

// providerOffers: modelresolve.ProviderOffers.
func providerOffers(mc *config.ModelConfig, model string) bool {
	return modelresolve.ProviderOffers(mc, model)
}

// resolveModelPair applies the FR-040 rules (modelresolve.Pair) and returns
// the matched row carrying the requested model, or (nil, false).
func resolveModelPair(cfg *config.Config, provider, model string) (*config.ModelConfig, bool) {
	return modelresolve.Pair(cfg, provider, model)
}

// bareModelOffers: modelresolve.BareModelOffers.
func bareModelOffers(cfg *config.Config, model string) (*config.ModelConfig, []string) {
	return modelresolve.BareModelOffers(cfg, model)
}

// AmbiguousModelError is the FR-040 rule-2 miss for a bare model id that
// several configured providers offer (provider hotfix H2). It names the
// providers so the operator can pick one, instead of the old
// `not found in model_list or providers`, which claimed the model existed
// nowhere when it actually existed in two places.
type AmbiguousModelError struct {
	Model     string
	Providers []string
}

func (e *AmbiguousModelError) Error() string {
	return fmt.Sprintf("model %q is offered by several providers (%s); choose the provider",
		e.Model, strings.Join(e.Providers, ", "))
}

// ResolveModelCfg resolves a model reference to a *config.ModelConfig ready
// for providers.CreateProviderFromConfig. It is the single source of truth
// for "which row does this reference name?" used by both the chat runtime
// (ApplyAgentModel → CreateProviderFromConfig) and the UI selector helper
// (buildModelListResolver), under the FR-040 rules above.
//
// On success the returned config is a CLONE — callers may mutate it (e.g.
// setting Home) without touching the entry in cfg.
func ResolveModelCfg(cfg *config.Config, modelName, workspace string) (*config.ModelConfig, error) {
	return ResolveModelPairCfg(cfg, "", modelName, workspace)
}

// ResolveModelPairCfg is ResolveModelCfg with the provider half supplied —
// the form every caller that already knows the pair should use (a pinned
// agent primary, a fallback entry with its own provider).
func ResolveModelPairCfg(
	cfg *config.Config,
	provider, modelName, workspace string,
) (*config.ModelConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	raw := strings.TrimSpace(modelName)
	if raw == "" {
		return nil, fmt.Errorf("model name is required")
	}
	mc, ok := resolveModelPair(cfg, provider, raw)
	if !ok {
		if strings.TrimSpace(provider) == "" {
			if _, offering := bareModelOffers(cfg, raw); len(offering) > 1 {
				return nil, &AmbiguousModelError{Model: raw, Providers: offering}
			}
		}
		return nil, fmt.Errorf("model %q not found in model_list or providers", raw)
	}
	clone := cloneWithWorkspace(mc, workspace)
	clone.Model = raw
	return clone, nil
}

// cloneWithWorkspace returns a pointer to a fresh copy of src with Home
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
	if clone.Home == "" {
		clone.Home = workspace
	}
	return &clone
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

// resolveModelCandidates resolves the PRIMARY only. Legacy plain-string
// fallbacks name a model without its provider, so they are never routed
// (founder rule 2026-10-18, issue #1340: provider and model always come in
// pairs) — the providers-level string-fallback resolution, which would send
// them through the default provider, is deliberately never fed.
func resolveModelCandidates(
	cfg *config.Config,
	defaultProvider string,
	primary string,
) []providers.FallbackCandidate {
	return providers.ResolveCandidatesWithLookup(
		providers.ModelConfig{Primary: primary},
		defaultProvider,
		buildModelListResolver(cfg),
	)
}

// resolveModelCandidatesForAgent resolves the primary followed by the agent's
// paired fallback chain (FR-005 / FR-007): each fallback routes through its
// own provider via resolveModelCandidatesFromList. Unpaired entries — and the
// legacy plain-string field — are never routed (issue #1340).
func resolveModelCandidatesForAgent(
	cfg *config.Config,
	defaultProvider string,
	primary string,
	agent *AgentInstance,
) []providers.FallbackCandidate {
	if agent == nil || len(agent.FallbackModels) == 0 {
		return resolveModelCandidates(cfg, defaultProvider, primary)
	}
	return resolveModelCandidatesFromList(cfg, defaultProvider, primary, agent.FallbackModels)
}

// resolveModelCandidatesFromList is the provider-aware variant of
// resolveModelCandidates. Each fallback entry may carry its own Provider
// (FR-005 / FR-007) so a fallback can route through a different provider than
// the primary. The primary is still resolved through the model_list lookup so
// a bare alias (e.g. "step-3.5-flash") is expanded to its full protocol-prefixed
// form; fallback entries bypass the lookup when Provider is set because the
// agent has explicitly pinned the route.
//
// An entry without BOTH a provider and a model is never routed: a fallback
// is a (provider, model) pair (founder rule 2026-10-18, issue #1340), so no
// model_list lookup or defaultProvider passthrough fills in a provider.
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
		model := strings.TrimSpace(fb.Model)
		provider := strings.TrimSpace(fb.Provider)
		// A fallback is a (provider, model) PAIR (founder rule 2026-10-18,
		// issue #1340): an entry missing either half is never routed — no
		// configured-row lookup, no default-provider passthrough. The agent
		// constructor reports such entries (warnUnpairedFallbacks).
		if model == "" || provider == "" {
			return
		}
		// The pair is pinned. Take it verbatim — no prefix split (FR-034: a
		// `/` inside a model id is data, e.g. `z-ai/glm-5.2` under
		// `openrouter`).
		key := providers.ModelKey(provider, model)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, providers.FallbackCandidate{Provider: provider, Model: model})
	}

	addPrimary(primary)
	for _, fb := range fallbacks {
		addFallback(fb)
	}
	return out
}

// resolveAgentCandidatesWithPrimaryProvider is the O3 two-field entry point used
// at agent construction. When the agent has an EXPLICIT primary provider
// (AgentConfig.Model.Provider), the primary candidate is pinned to that provider
// directly — never inferred from the slug or the default provider — exactly like
// a fallback entry with a pinned Provider. The remaining fallback chain is
// resolved by the existing provider-aware resolver and appended (de-duplicated
// against the pinned primary).
//
// When primaryProvider is empty the behavior is identical to the pre-O3 path
// (resolveModelCandidatesFromList for the modern chain, resolveModelCandidates
// for the legacy []string chain), so unmigrated agents are unaffected.
//
// This lives here (not in loop.go) per the O3 task split: loop.go's
// ApplyAgentModel resolves the provider from the already-resolved ModelConfig on
// a live switch; this construction-time helper honors the persisted explicit
// provider.
func resolveAgentCandidatesWithPrimaryProvider(
	cfg *config.Config,
	defaultProvider string,
	primary string,
	primaryProvider string,
	fallbackModels []config.FallbackModel,
	_ []string, // legacy plain-string fallbacks: unpaired, never routed (#1340)
) []providers.FallbackCandidate {
	pinned := strings.TrimSpace(primaryProvider)
	if pinned == "" {
		// No explicit provider — preserve the exact pre-O3 selection.
		if len(fallbackModels) > 0 {
			return resolveModelCandidatesFromList(cfg, defaultProvider, primary, fallbackModels)
		}
		return resolveModelCandidates(cfg, defaultProvider, primary)
	}

	// Explicit provider: pin the primary candidate to it, verbatim.
	seen := make(map[string]bool)
	var out []providers.FallbackCandidate
	// The pair is pinned by the agent's own config — take it verbatim, never
	// split a `/` out of the model id (FR-034).
	if model := strings.TrimSpace(primary); model != "" {
		key := providers.ModelKey(pinned, model)
		seen[key] = true
		out = append(out, providers.FallbackCandidate{Provider: pinned, Model: model})
	}

	// Append ONLY the configured, paired fallback chain, in configured
	// order. The primary is not handed to the fallback resolver:
	// re-resolving a vendor-prefixed pinned id
	// ("deepseek/m" under openrouter) would yield a different pair
	// ("deepseek", "m") — a phantom fallback the agent never configured.
	// The only de-duplication is against the actual pinned primary pair (and
	// earlier fallbacks), so a configured fallback is never dropped.
	rest := resolveModelCandidatesFromList(cfg, defaultProvider, "", fallbackModels)
	for _, c := range rest {
		key := providers.ModelKey(c.Provider, c.Model)
		if seen[key] {
			// Already covered (the pinned primary, or an earlier fallback).
			continue
		}
		seen[key] = true
		out = append(out, c)
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
// the FR-040 rules.
func resolvedModelConfig(cfg *config.Config, modelName, workspace string) (*config.ModelConfig, error) {
	return ResolveModelCfg(cfg, modelName, workspace)
}

// IsKnownModel reports whether `slug` names a model at least one configured
// provider offers. It is the validation half of W6-C4 / G12: the SPA lets
// users type arbitrary model ids into the AgentProfile picker, and this is
// the authoritative answer to "is this resolvable?" behind the persistent
// "unresolved" chip (the TS twin lives in
// `src/lib/agents/model-validation.ts` and MUST stay in sync).
//
// Matching is FR-040 rule 2's "offers it" test, applied per row and compared
// EXACTLY after TrimSpace — no case folding (A-19), no prefix split
// (FR-034), no display alias (X-25). Unlike ResolveModelCfg this does not
// require the match to be unique: the chip answers "does anything serve
// this?", and ambiguity is the resolver's problem, not the chip's.
func IsKnownModel(slug string, models []*config.ModelConfig) bool {
	needle := strings.TrimSpace(slug)
	if needle == "" {
		return false
	}
	for _, mc := range models {
		if mc == nil {
			continue
		}
		if providerOffers(mc, needle) {
			return true
		}
	}
	return false
}
