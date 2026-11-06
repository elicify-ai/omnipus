// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/modelresolve"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// agentModelChain is get_agent's `model_chain` (r17 §7.5 AG-16, MAJ-008):
// the agent's EFFECTIVE model chain, primary first, then each fallback in
// order, derived through the runtime's own resolver (pkg/modelresolve: the
// agent's primary resolution, its fallback selection and the FR-040 pair
// rules). Nothing is guessed: a value that is not known is null.
func agentModelChain(a *config.AgentConfig, cfg *config.Config) []map[string]any {
	var defaults *config.AgentDefaults
	if cfg != nil {
		defaults = &cfg.Agents.Defaults
	}
	fallbacks := modelresolve.AgentFallbackModels(a)
	chain := make([]map[string]any, 0, 1+len(fallbacks))
	chain = append(chain, modelChainEntry(cfg, "primary", 0,
		modelresolve.AgentPrimaryProvider(a, defaults), modelresolve.AgentPrimaryModel(a, defaults), false))
	for i, fb := range fallbacks {
		chain = append(chain, modelChainEntry(cfg, fmt.Sprintf("fallback_%d", i+1), i+1,
			strings.TrimSpace(fb.Provider), strings.TrimSpace(fb.Model), true))
	}
	return chain
}

// modelChainEntry describes one AgentModelChainEntry. n is the fallback's
// 1-based number (0 for the primary).
func modelChainEntry(cfg *config.Config, position string, n int, provider, model string, fallback bool) map[string]any {
	label := "primary"
	if fallback {
		label = fmt.Sprintf("fallback %d", n)
	}
	entry := map[string]any{
		"position": position, "model": model, "provider": nil,
		"provider_configured": false, "model_offered": nil, "accepts_images": nil,
	}
	switch {
	case model == "":
		entry["warning"] = label + " has no model set — will never be used"
	case fallback && provider == "":
		// A legacy model-only fallback is never routed (founder rule): it is
		// reported, with the resolver's facts left unknown.
		entry["warning"] = label + " is not paired with a provider — reported, never routed"
	case provider == "":
		// A bare primary resolves through FR-040 rule 2.
		if mc, ok := modelresolve.Pair(cfg, "", model); ok {
			describeResolved(entry, strings.TrimSpace(mc.Provider), model)
		} else {
			entry["warning"] = fmt.Sprintf("%s model %q resolves to no single configured provider — will never be used", label, model)
		}
	default:
		entry["provider"] = provider
		if !modelresolve.HasConfiguredProvider(cfg, provider) {
			entry["warning"] = label + " has no configured provider — will never be used"
			break
		}
		entry["provider_configured"] = true
		if _, ok := modelresolve.Pair(cfg, provider, model); ok {
			describeResolved(entry, provider, model)
		} else {
			entry["model_offered"] = false
			entry["warning"] = fmt.Sprintf("%s model %q is not offered by provider %q", label, model, provider)
		}
	}
	return entry
}

// describeResolved fills a resolved pair's facts: the provider, configured
// and offered, and the catalog's image-input capability when the catalog
// knows the model (null otherwise).
func describeResolved(entry map[string]any, provider, model string) {
	entry["provider"] = provider
	entry["provider_configured"] = true
	entry["model_offered"] = true
	if h := providers.ProviderCatalog().Resolve(provider, model); h.Found() {
		entry["accepts_images"] = h.Supports(catalog.ModalityImage)
	}
}
