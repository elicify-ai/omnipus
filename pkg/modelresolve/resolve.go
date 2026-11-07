// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package modelresolve holds the FR-040 model-reference resolution rules and
// an agent's primary/fallback selection: the one implementation the chat
// runtime (pkg/agent) and the agent reader (get_agent's model_chain, r17
// §7.5 AG-16) both use. It depends only on configuration and the provider
// catalog, so a package pkg/agent itself imports can call it.
package modelresolve

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ── FR-040: how a model reference resolves to a configured row ───────────────
//
// A reference is the PAIR (provider, model). There are exactly three rules
// (ADR-067 FR-040, X-24), and nothing else:
//
//  1. An exact pair — a configured row whose Provider and Model both match —
//     wins outright.
//  1b. A pair whose provider IS configured and whose model that provider
//     OFFERS (the catalog's models for a cloud row, the row's own manual
//     `models[]` for a local one) resolves to that row carrying the
//     requested model. This is the ordinary case: a provider row is a
//     credential plus an endpoint, and the agent picks the model.
//  2. A BARE model id with no provider resolves iff exactly one configured,
//     non-degraded provider offers it. Two providers offering the same model
//     is genuinely ambiguous and stays unresolved — guessing between them is
//     how a turn silently ran on the wrong account.
//  3. Anything else is unresolved (ADR-068 turns that into `needs_model`).
//
// What is GONE, deliberately: the `model_name` display alias (X-25), the
// `<protocol>/<model>` prefix split (FR-034), and the passthrough fallback
// that re-prefixed any unmatched slug onto the first aggregator it found.
// That last one is the reason this rewrite matters: it meant a typo'd or
// retired model id never failed to resolve — it silently became an
// OpenRouter request, billed to the operator's OpenRouter key, for a model
// nobody had chosen.

// ProviderOffers reports whether a configured row can serve a model id:
// its own pinned Model, an entry in its manual `models[]` (the local-row
// list), or — for a cloud row — a model the catalog lists under that
// provider. Comparison is exact after TrimSpace (FR-036).
func ProviderOffers(mc *config.ModelConfig, model string) bool {
	if mc == nil {
		return false
	}
	if strings.TrimSpace(mc.Model) == model {
		return true
	}
	for _, m := range mc.Models {
		if strings.TrimSpace(m) == model {
			return true
		}
	}
	row, known := providers.CatalogProvider(mc.Provider)
	if !known || row.Locality == catalog.LocalityLocal {
		// A local runtime serves whatever the operator pulled; the catalog
		// carries no model rows for it, so the manual list above is the only
		// authority. An unknown provider offers nothing.
		return false
	}
	return providers.ProviderCatalog().Resolve(strings.TrimSpace(mc.Provider), model).Found()
}

// ProviderConfigured reports whether a row's provider id is one the runtime
// can actually construct: a catalog id, or an operator-typed custom row.
// A row naming an unknown provider is DEGRADED and never resolves anything
// (FR-016) — that agent needs a provider, not a silently different one.
func ProviderConfigured(mc *config.ModelConfig) bool {
	if mc == nil {
		return false
	}
	return mc.Custom || providers.IsCatalogProvider(mc.Provider)
}

// HasConfiguredProvider reports whether cfg holds a configured,
// non-degraded row for the provider id.
func HasConfiguredProvider(cfg *config.Config, provider string) bool {
	if cfg == nil {
		return false
	}
	provider = strings.TrimSpace(provider)
	for _, mc := range cfg.Providers {
		if mc != nil && ProviderConfigured(mc) && strings.TrimSpace(mc.Provider) == provider {
			return true
		}
	}
	return false
}

// Pair applies the three rules above and returns the matched row (not a
// clone; callers must not mutate it), or (nil, false).
func Pair(cfg *config.Config, provider, model string) (*config.ModelConfig, bool) {
	if cfg == nil {
		return nil, false
	}
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, false
	}

	// Rule 1 — exact pair.
	for i := range cfg.Providers {
		mc := cfg.Providers[i]
		if mc == nil || !ProviderConfigured(mc) {
			continue
		}
		if strings.TrimSpace(mc.Provider) == provider && strings.TrimSpace(mc.Model) == model {
			return mc, true
		}
	}

	if provider != "" {
		// Rule 1b — the named provider is configured and offers the model.
		for i := range cfg.Providers {
			mc := cfg.Providers[i]
			if mc == nil || !ProviderConfigured(mc) {
				continue
			}
			if strings.TrimSpace(mc.Provider) != provider {
				continue
			}
			if ProviderOffers(mc, model) {
				return mc, true
			}
		}
		return nil, false
	}

	// Rule 2 — a bare model id, offered by exactly one provider. Two
	// different providers offering it is ambiguous — rule 3.
	match, offering := BareModelOffers(cfg, model)
	if match == nil || len(offering) != 1 {
		return nil, false
	}
	return match, true
}

// BareModelOffers walks rule 2 for a bare model id: the FIRST configured,
// non-degraded row that offers it, and the DISTINCT provider ids (config
// order) of every row that does. Several rows of one provider (multi-key
// load balancing) count once.
func BareModelOffers(cfg *config.Config, model string) (*config.ModelConfig, []string) {
	var match *config.ModelConfig
	var offering []string
	for i := range cfg.Providers {
		mc := cfg.Providers[i]
		if mc == nil || !ProviderConfigured(mc) || !ProviderOffers(mc, model) {
			continue
		}
		if match == nil {
			match = mc
		}
		id := strings.TrimSpace(mc.Provider)
		seen := false
		for _, o := range offering {
			if o == id {
				seen = true
				break
			}
		}
		if !seen {
			offering = append(offering, id)
		}
	}
	return match, offering
}

// AgentPrimaryModel resolves the primary model for an agent: its own
// model.primary, else the model half of agents.defaults.default_model
// (ADR-068 D14.1). Empty when neither is set.
func AgentPrimaryModel(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Primary)
	}
	if defaults == nil || defaults.DefaultModel.IsZero() {
		return ""
	}
	return strings.TrimSpace(defaults.DefaultModel.Model)
}

// AgentPrimaryProvider returns the provider the agent's primary model is
// PINNED to (O3 two-field model). When the agent sets its own primary model,
// this is its explicit model.provider — empty means "no explicit provider,
// resolve via the passthrough path". When the agent runs on the default model
// it is the provider half of agents.defaults.default_model: the default is an
// exact pair (ADR-068 D14.1), so it is pinned exactly like an explicit
// per-agent provider and never inferred from a configured passthrough.
func AgentPrimaryProvider(agentCfg *config.AgentConfig, defaults *config.AgentDefaults) string {
	if agentCfg != nil && agentCfg.Model != nil && strings.TrimSpace(agentCfg.Model.Primary) != "" {
		return strings.TrimSpace(agentCfg.Model.Provider)
	}
	if defaults == nil || defaults.DefaultModel.IsZero() {
		return ""
	}
	return strings.TrimSpace(defaults.DefaultModel.Provider)
}

// AgentFallbackModels returns the agent's fallback entries: the
// provider-aware FallbackModels when set, else the legacy model-only
// Model.Fallbacks as entries with no provider.
func AgentFallbackModels(agentCfg *config.AgentConfig) []config.FallbackModel {
	if agentCfg == nil {
		return nil
	}
	if len(agentCfg.FallbackModels) > 0 {
		return agentCfg.FallbackModels
	}
	if agentCfg.Model == nil || len(agentCfg.Model.Fallbacks) == 0 {
		return nil
	}
	legacy := agentCfg.Model.Fallbacks
	out := make([]config.FallbackModel, len(legacy))
	for i, s := range legacy {
		out[i] = config.FallbackModel{Model: s}
	}
	return out
}
