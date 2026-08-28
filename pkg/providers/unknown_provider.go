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

// IsUnknownProviderIDIn reports whether a configured provider id is UNKNOWN in
// the ADR-067 FR-015/FR-016 sense against an explicit catalog: the id is
// neither a row of that document nor an operator-defined CUSTOM row in cfg.
//
// It is the one predicate behind two user-visible states that must never
// disagree: the `unknown-provider` row on `GET /providers` and the
// `needs_provider` degrade an agent bound to such a row carries (FR-016). The
// agent side (pkg/agent's provider pool + the pre-turn gate) and the gateway
// side (Agent.degraded_reason) both call it, so an operator can never see a
// provider reported healthy while the agent bound to it refuses turns.
//
// Three rules, in order:
//
//   - EXACT membership (FR-036). The id is compared after TrimSpace and NEVER
//     case-folded, so an entity that says "ZAI" does not silently resolve the
//     catalog's "zai" — it is unknown, and the agent degrades (DS-8 row 4).
//   - A CUSTOM row rescues an id the catalog does not carry, but only when it
//     supplies BOTH halves of its own endpoint definition: a non-empty
//     `api_base` and a `protocol` of `openai-compatible` or `anthropic`. This
//     mirrors resolveRow's custom branch exactly (an id flagged `custom: true`
//     and an unflagged id follow the same rule) without constructing a client
//     or touching a credential.
//   - Everything else is unknown, and the caller names the operator's own id
//     and NOTHING else — no canonical alternative, no "did you mean"
//     (ErrUnknownProvider's contract, SC-010).
//
// E7 posture: when NO catalog document is loaded at all (a corrupt embedded
// snapshot), every id would otherwise classify as unknown and every agent in
// the install would degrade at once. The absent-catalog case therefore answers
// false — the same guard `GET /providers` applies before it stamps a row
// `unknown-provider`.
//
// An empty id is NOT unknown: "no provider pinned" is ADR-068's needs_model
// state, resolved through the slug rungs, not this one.
func IsUnknownProviderIDIn(cat *catalog.Catalog, cfg *config.Config, providerID string) bool {
	id := strings.TrimSpace(providerID)
	if id == "" {
		return false
	}
	if cat == nil || cat.Document() == nil {
		return false
	}
	if _, known := cat.Provider(id); known {
		return false
	}
	if cfg == nil {
		return true
	}
	for _, mc := range cfg.Providers {
		if mc == nil || strings.TrimSpace(mc.Provider) != id {
			continue
		}
		if isValidCustomRow(mc) {
			return false
		}
	}
	return true
}

// IsUnknownProviderID is IsUnknownProviderIDIn against the process catalog —
// the document the gateway installed at boot (SetCatalog), or the embedded
// snapshot in a process that never booted one.
func IsUnknownProviderID(cfg *config.Config, providerID string) bool {
	return IsUnknownProviderIDIn(ProviderCatalog(), cfg, providerID)
}

// isValidCustomRow reports whether a config row defines an operator's own
// endpoint: both `api_base` and a custom-eligible `protocol` (FR-035). It is
// the credential-free, catalog-free half of resolveRow's custom branch.
func isValidCustomRow(mc *config.ModelConfig) bool {
	if mc == nil {
		return false
	}
	if strings.TrimSpace(mc.APIBase) == "" {
		return false
	}
	return isCustomProtocol(catalog.Protocol(strings.TrimSpace(mc.Protocol)))
}
