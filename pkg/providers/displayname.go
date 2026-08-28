// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "strings"

// DisplayName returns the catalog's own `name` for a provider id — the single
// source of truth for the FR-7 user-facing validation messages in the gateway
// and for any other label built in Go (ADR-067 A-14, FR-030).
//
// An id the catalog does not carry is returned VERBATIM. The old
// `knownDisplayNames` map and its title-case fallback are gone: a hand-typed
// branded name is exactly the kind of Go-side provider knowledge ADR-067
// removed, and title-casing an unknown id ("Z-ai") invented a brand for a
// provider Omnipus does not know. Echoing the id back is honest and, for an
// unknown provider, is the only string that cannot mislead.
func DisplayName(providerID string) string {
	id := strings.TrimSpace(providerID)
	if id == "" {
		return providerID
	}
	if p, ok := CatalogProvider(id); ok && strings.TrimSpace(p.Name) != "" {
		return p.Name
	}
	return id
}
