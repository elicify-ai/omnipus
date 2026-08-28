// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ── The process catalog (ADR-067 D11) ────────────────────────────────────────
//
// The factory dispatches on the protocol the CATALOG carries for a provider
// id, so every construction path needs one document to read. The gateway
// installs the document it booted (SetCatalog, beside agent.SetWindowCatalog);
// every other entry point — the CLI onboarding wizard (A-21), the migration
// tool, a unit test that never boots a gateway — falls back to the committed
// embedded snapshot, parsed once, lazily.
//
// The fallback is not a second source of truth: it is the SAME bytes the
// gateway's own Boot starts from (catalog.EmbeddedSnapshot), so a process
// that never installs a catalog resolves exactly what a freshly-installed
// gateway would before its first pull. When the embedded snapshot itself
// fails validation (E7) the fallback is an empty catalog: every id is
// unknown, and every construction returns ErrUnknownProvider rather than
// silently inventing a URL.

var (
	installedCatalog atomic.Pointer[catalog.Catalog]

	embeddedOnce     sync.Once
	embeddedFallback *catalog.Catalog
)

// SetCatalog installs the catalog every provider construction resolves
// against. The gateway calls it once at boot with the document it booted;
// nil restores the embedded-snapshot fallback.
func SetCatalog(c *catalog.Catalog) {
	if c == nil {
		installedCatalog.Store(nil)
		return
	}
	installedCatalog.Store(c)
}

// ProviderCatalog returns the catalog the factory reads: the installed
// document when the gateway has booted one, otherwise the embedded snapshot.
// It never returns nil.
func ProviderCatalog() *catalog.Catalog {
	if c := installedCatalog.Load(); c != nil {
		return c
	}
	embeddedOnce.Do(func() {
		c, err := catalog.NewCatalog(catalog.EmbeddedSnapshot)
		if err != nil || c == nil {
			// E7: a corrupt embedded snapshot degrades to "no catalog".
			// The gateway logs the boot ERROR; this package stays silent so
			// a CLI or a test does not emit a second copy of it.
			embeddedFallback = catalog.New()
			return
		}
		embeddedFallback = c
	})
	return embeddedFallback
}

// CatalogProvider returns the catalog row for an exactly-matching provider id
// (trimmed, never case-folded — A-19) and whether it exists.
func CatalogProvider(providerID string) (catalog.Provider, bool) {
	id := strings.TrimSpace(providerID)
	if id == "" {
		return catalog.Provider{}, false
	}
	return ProviderCatalog().Provider(id)
}

// IsCatalogProvider reports whether the served document contains the id. It
// is the ONE membership test every gate uses (REST PUT, the onboarding probe,
// the CLI wizard) — there is no protocol allow-list to consult any more.
func IsCatalogProvider(providerID string) bool {
	_, ok := CatalogProvider(providerID)
	return ok
}

// APIBaseFor returns the catalog's primary base URL for a provider id, or ""
// when the id is unknown or the row carries no URL (an unsupported row).
// Callers that hold an explicit `api_base` MUST prefer it — the catalog URL
// is the default, never an override (FR-012).
func APIBaseFor(providerID string) string {
	p, ok := CatalogProvider(providerID)
	if !ok {
		return ""
	}
	return p.API
}

// endpointFor resolves the (protocol, base URL) pair a config selects on a
// catalog row: the row's primary when the config names no protocol, else the
// matching entry of the row's `protocols[]` (FR-013, A-8).
//
// A protocol the row does not offer is an error, never a silent fallback to
// the primary — an operator who asked for Anthropic on a provider that only
// speaks OpenAI must find out before the first turn, not after it.
func endpointFor(row catalog.Provider, want catalog.Protocol) (catalog.Endpoint, error) {
	if want == "" {
		if row.Protocol == "" {
			return catalog.Endpoint{}, wrapUnsupported(row)
		}
		return catalog.Endpoint{Protocol: row.Protocol, API: row.API}, nil
	}
	if want == row.Protocol {
		return catalog.Endpoint{Protocol: row.Protocol, API: row.API}, nil
	}
	for _, ep := range row.Protocols {
		if ep.Protocol == want {
			return ep, nil
		}
	}
	return catalog.Endpoint{}, &UnofferedProtocolError{ProviderID: row.ID, Protocol: string(want)}
}

// UnofferedProtocolError reports a config that selected a protocol the
// catalog row does not offer (DS-3 row 4).
type UnofferedProtocolError struct {
	ProviderID string
	Protocol   string
}

func (e *UnofferedProtocolError) Error() string {
	return "provider " + strconv.Quote(e.ProviderID) +
		" does not offer protocol " + strconv.Quote(e.Protocol)
}

// wrapUnsupported turns a tier-unsupported row into the typed error that
// names the catalog's own reason (FR-019).
func wrapUnsupported(row catalog.Provider) error {
	reason := row.UnsupportedReason
	if reason == "" {
		reason = "unsupported"
	}
	return &UnsupportedProviderError{ProviderID: row.ID, Reason: reason}
}

// UnsupportedProviderError reports a `tier: unsupported` catalog row.
type UnsupportedProviderError struct {
	ProviderID string
	Reason     string
}

func (e *UnsupportedProviderError) Error() string {
	return "provider " + strconv.Quote(e.ProviderID) + " is unsupported: " + e.Reason
}

// Unwrap lets errors.Is(err, ErrUnsupportedProvider) classify it.
func (e *UnsupportedProviderError) Unwrap() error { return ErrUnsupportedProvider }

// UnknownProviderError names the id the operator typed and nothing else.
type UnknownProviderError struct {
	ProviderID string
}

func (e *UnknownProviderError) Error() string {
	return "unknown provider " + strconv.Quote(e.ProviderID)
}

// Unwrap lets errors.Is(err, ErrUnknownProvider) classify it.
func (e *UnknownProviderError) Unwrap() error { return ErrUnknownProvider }
