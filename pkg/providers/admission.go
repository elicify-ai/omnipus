// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ── Provider admission (ADR-067 FR-019, FR-035) ──────────────────────────────
//
// ONE rule, shared by every entry point that lets an operator name a
// provider: `PUT /providers/{id}`, the onboarding probe, `POST
// /onboarding/complete` and the CLI wizard (F-07/F-08/F-09). It used to live
// in the gateway alone, which is why the CLI could accept a `tier:
// unsupported` id the REST layer rejected — the same install, two answers.

// AdmitIn reports whether id may be configured or probed against the given
// catalog document, and whether it is admitted as an operator-named CUSTOM
// row (FR-035, X-13 — `custom: true` is what every later check reads, never
// the literal id "custom").
//
// The three outcomes:
//   - a catalog row of any tier but `unsupported` → admitted, custom=false;
//   - a `tier: unsupported` row → *UnsupportedProviderError carrying the
//     catalog's OWN `unsupported_reason` (FR-019), never a Go list;
//   - an id the catalog does not carry → admitted as a custom row iff it
//     supplies BOTH halves of what it takes to reach one, an api_base and
//     one of the two protocols a base URL fully describes; otherwise
//     *UnknownProviderError, which names the id and offers no alternative.
//
// With no catalog document loaded (E7) nothing is classified: admitting is
// the honest behaviour, since the process cannot tell a known id from an
// unknown one and refusing every configuration would make a bad snapshot
// unrecoverable through the UI or the wizard.
func AdmitIn(cat *catalog.Catalog, id, apiBase, protocol string) (custom bool, err error) {
	if cat == nil || cat.Document() == nil {
		return false, nil
	}
	row, known := cat.Provider(id)
	if known {
		if row.Tier == catalog.TierUnsupported {
			reason := row.UnsupportedReason
			if reason == "" {
				reason = "unsupported"
			}
			return false, &UnsupportedProviderError{ProviderID: id, Reason: reason}
		}
		return false, nil
	}
	if strings.TrimSpace(apiBase) == "" || !IsCustomRowProtocol(protocol) {
		return false, &UnknownProviderError{ProviderID: id}
	}
	return true, nil
}

// Admit applies AdmitIn to the process catalog — the document the gateway
// installed at boot, or the embedded snapshot for an entry point that never
// boots a gateway (the CLI wizard, A-21). It is the form every caller that
// does not already hold a *catalog.Catalog should use.
func Admit(id, apiBase, protocol string) (custom bool, err error) {
	return AdmitIn(ProviderCatalog(), id, apiBase, protocol)
}

// IsCustomRowProtocol reports whether p is one of the two protocols a custom
// row may declare (FR-014/FR-035). `google`, `ollama` and `cli` carry
// vendor-specific construction the operator cannot describe with a base URL
// alone, so they are catalog-only.
func IsCustomRowProtocol(p string) bool {
	switch catalog.Protocol(strings.TrimSpace(p)) {
	case catalog.ProtocolOpenAICompatible, catalog.ProtocolAnthropic:
		return true
	default:
		return false
	}
}
