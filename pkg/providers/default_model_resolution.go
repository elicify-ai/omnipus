// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ResolveDefaultModelRow resolves the global default-model (provider, model)
// pair (agents.defaults.default_model, ADR-068 D14.1) to a providers[] row
// ready for CreateProviderFromConfig.
//
// This is the ONE place that decides whether a (provider, model) pair "the
// Providers card offers" can actually be turned into a working provider
// client. It takes cat as an explicit parameter (rather than reaching for
// the process-global providers.ProviderCatalog()) so its two callers can be
// proven to agree without relying on shared global state in tests — in
// production they always DO agree, because gateway.go installs the same
// booted *catalog.Catalog into both providers.SetCatalog (which
// CreateProvider's ProviderCatalog() call below reads) and restAPI.providerCatalog
// (which putDefaultModel passes here) from the same local variable:
//
//   - pkg/gateway/rest_default_model.go's putDefaultModel, which must 400
//     rather than persist a pair this function rejects (invariant: a PUT
//     that returns 200 has actually taken effect).
//   - CreateProvider below, the boot/reload path, which must never receive
//     an already-persisted pair this function would reject (invariant: a
//     config a successful PUT wrote always boots).
//
// Before this existed, boot resolution went through config.GetModelConfig,
// which requires the pair to equal a row's own legacy singular Model field
// exactly — a field the default-model PUT never writes — while validation
// separately accepted any pair the served catalog (or, for custom/local
// rows, nothing at all) confirmed. Every pair the Providers card actually
// offers (a row's Models[] catalogue picks, or any model the served catalog
// lists for a connected catalog row) passed validation but failed
// CreateProviderFromConfig's exact-only lookup, so a 200 PUT silently wrote
// a config.json that bricked the next restart.
//
// Four rungs, first match wins:
//
//  1. Exact pair — config.GetModelConfig(provider, model): a row whose own
//     Model field equals the request. Delegating to it here (rather than
//     re-implementing) preserves its existing round-robin / usable-preferred
//     behaviour across duplicate rows unchanged.
//  2. A row for this provider whose Models[] list carries the model — the
//     picker's own explicit catalogue for a row, whether or not the catalog
//     document independently agrees (a live provider probe can know models
//     newer than the committed catalog snapshot).
//  3. X-13/X-17/X-22: an operator-typed custom row (a provider id the
//     catalog does not know), or a LOCAL catalog row (a locally-run
//     runtime the catalog cannot enumerate models for) — either serves
//     whatever non-empty model the operator names, no live call.
//  4. A row for this provider that is a KNOWN, non-custom, non-local catalog
//     entry: the served catalog must confirm that provider offers the
//     model.
//
// The returned *config.ModelConfig is always a CLONE with Model set to the
// exact requested model — never the row's own stored Model/Models[0] — so
// CreateProviderFromConfig sends the model the caller actually asked for
// upstream, not a substitute. Returns (nil, false) when no row can serve the
// pair; callers must treat that as "this pair cannot be applied", not fall
// back to any other resolution.
func ResolveDefaultModelRow(
	cfg *config.Config, cat *catalog.Catalog, provider, model string,
) (*config.ModelConfig, bool) {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if cfg == nil || provider == "" || model == "" {
		return nil, false
	}

	// Rung 1 — exact pair, unchanged existing resolution (round-robin +
	// usable-preference across duplicate rows).
	if mc, err := cfg.GetModelConfig(provider, model); err == nil && mc != nil {
		return mc, true
	}

	// Rung 2 — a row's own Models[] list for this provider.
	if clone, ok := cloneUsableRowForModel(cfg, provider, func(m *config.ModelConfig) bool {
		for _, listed := range m.Models {
			if strings.TrimSpace(listed) == model {
				return true
			}
		}
		return false
	}); ok {
		clone.Model = model
		return clone, true
	}

	catRow, known := catalogProviderIn(cat, provider)

	// Rung 3 — X-13/X-17/X-22: an unrecognized (operator-custom) provider id,
	// or a LOCAL catalog row, serves any non-empty model with no live check.
	if !known || catRow.Custom || catRow.Locality == catalog.LocalityLocal {
		if clone, ok := cloneUsableRowForModel(cfg, provider, alwaysMatch); ok {
			clone.Model = model
			return clone, true
		}
		return nil, false
	}

	// Rung 4 — known, non-custom, non-local: the served catalog is
	// authoritative.
	if cat == nil || !cat.Resolve(provider, model).Found() {
		return nil, false
	}
	if clone, ok := cloneUsableRowForModel(cfg, provider, alwaysMatch); ok {
		clone.Model = model
		return clone, true
	}
	return nil, false
}

// catalogProviderIn looks up id in cat, tolerating a nil catalog (no
// document loaded at all — E7) by reporting "not known" rather than
// panicking.
func catalogProviderIn(cat *catalog.Catalog, id string) (catalog.Provider, bool) {
	if cat == nil {
		return catalog.Provider{}, false
	}
	return cat.Provider(id)
}

func alwaysMatch(*config.ModelConfig) bool { return true }

// cloneUsableRowForModel returns a CLONE of the first providers[] row whose
// Provider matches and which satisfies match, preferring a row whose
// credential actually resolves (modelConfigRowUsable) when at least one
// candidate does — mirroring config.findMatches's usable-preference so a row
// with a broken api_key_ref is never handed back over a working sibling.
func cloneUsableRowForModel(
	cfg *config.Config, provider string, match func(*config.ModelConfig) bool,
) (*config.ModelConfig, bool) {
	var first, firstUsable *config.ModelConfig
	for _, m := range cfg.Providers {
		if m == nil || strings.TrimSpace(m.Provider) != provider || !match(m) {
			continue
		}
		if first == nil {
			first = m
		}
		if firstUsable == nil && modelConfigRowUsable(m) {
			firstUsable = m
		}
	}
	src := firstUsable
	if src == nil {
		src = first
	}
	if src == nil {
		return nil, false
	}
	clone := *src
	return &clone, true
}

// modelConfigRowUsable mirrors config's private modelConfigCredentialUsable:
// a row with no api_key_ref never needed a vault credential (local model,
// CLI/OAuth, api_base-only), otherwise its ref must have actually resolved.
func modelConfigRowUsable(m *config.ModelConfig) bool {
	if m == nil {
		return false
	}
	if strings.TrimSpace(m.APIKeyRef) == "" {
		return true
	}
	return m.APIKey() != ""
}
