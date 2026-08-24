// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// rest_providers_entitlement.go — "Check with my account" (ADR-067 T067-11).
//
// POST /api/v1/providers/{id}/entitlement (FR-021) makes ONE live listing
// call with the provider's stored key, intersects the result with the served
// catalog, and answers every catalog model annotated `entitled` with
// `limits: "known"` plus every model the provider returned that the catalog
// lacks with `limits: "unknown"`.
//
// The answer is cached for the gateway process under
// SHA-256(providerID + ":" + credentialRefName) — the ref NAME, never the
// secret — and evicted in exactly three places:
//
//  1. provider DELETE            (rest_providers_delete.go, step 3b);
//  2. a PUT that changes the key (rest.go's PUT branch, `keyChanged`) —
//     a PUT that only bumps updated_at is deliberately NOT an eviction;
//  3. a catalog refresh          (FR-037, via Catalog.OnRefreshApplied).
//
// It is never called at boot and never on a turn.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

const (
	// entitlementUnsupportedProtocolMsg is the DS-5 row 11b 409 body, for a
	// `cli` row (no HTTP listing exists) and for an operator-named custom
	// row (nothing to intersect against — the catalogue IS the operator's
	// own slugs).
	entitlementUnsupportedProtocolMsg = "entitlement not supported for this protocol"

	// entitlementNoEndpointMsg is the 409 for a row whose protocol IS
	// listable but which has no endpoint to list from — neither a
	// configured api_base nor a catalog `api`. Refusing is the honest
	// answer; there is nothing to dial.
	entitlementNoEndpointMsg = "entitlement not available: this provider has no endpoint configured"

	// entitlementNoKeyMsg is the 422 for a row that carries no credential
	// reference at all, beside describeCredentialResolutionError's two
	// messages for a reference that exists but does not resolve.
	entitlementNoKeyMsg = "no API key configured for this provider"

	// entitlementUpstreamTimeout bounds the one live listing call.
	entitlementUpstreamTimeout = 15 * time.Second
)

// ── the process cache (FR-021) ──────────────────────────────────────────────

// entitlementCacheEntry is one cached answer. It holds the ANNOTATED rows
// and the time of the live call: a cache hit repeats the original
// checked_at, so an operator can tell how old the fact is.
type entitlementCacheEntry struct {
	// providerID is carried so an eviction can find every entry for a
	// provider without re-deriving the hash for a credential ref name that
	// may itself have changed.
	providerID string
	models     []gen.EntitlementModel
	checkedAt  time.Time
}

// entitlementCache is the process-lifetime cache. Its zero value is ready
// to use, so every restAPI (production and test) has one without wiring.
type entitlementCache struct {
	mu      sync.Mutex
	entries map[string]entitlementCacheEntry
}

// entitlementCacheKey is FR-021's key derivation: SHA-256 over the provider
// id and the credential REF NAME joined by a colon. The secret value is
// never an input — the ref name is a label, and the hash is what is kept in
// memory.
func entitlementCacheKey(providerID, credentialRefName string) string {
	sum := sha256.Sum256([]byte(providerID + ":" + credentialRefName))
	return hex.EncodeToString(sum[:])
}

func (c *entitlementCache) get(key string) (entitlementCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	return e, ok
}

func (c *entitlementCache) put(key string, e entitlementCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]entitlementCacheEntry, 4)
	}
	c.entries[key] = e
}

// evictProvider drops every entry belonging to one provider — the DELETE
// and key-changing-PUT eviction. Keyed on the stored provider id rather
// than a recomputed hash, because the ref name the entry was keyed on is
// not necessarily the one the caller can still see.
func (c *entitlementCache) evictProvider(providerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if e.providerID == providerID {
			delete(c.entries, k)
		}
	}
}

// clear drops every entry — the catalog-refresh eviction (FR-037): the
// intersection was computed against a document that is no longer served.
func (c *entitlementCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
}

// registerEntitlementCacheInvalidation wires FR-037's refresh half. The
// gateway calls this once at boot; the T37 test calls the same helper, so
// the wiring under test is the wiring that ships.
func registerEntitlementCacheInvalidation(cat *catalog.Catalog, api *restAPI) {
	if cat == nil || api == nil {
		return
	}
	cat.OnRefreshApplied(func() {
		api.entitlements.clear()
		slog.Debug("rest: entitlement cache invalidated by catalog refresh")
	})
}

// ── the handler ─────────────────────────────────────────────────────────────

// handleProviderEntitlement answers POST /api/v1/providers/{id}/entitlement.
// The caller (HandleProviders) has already split the id off the path.
//
// Rate limiting: the route rides the SAME dispatcher, and therefore the same
// middleware chain, as POST /providers/{id}/test — which is what FR-021's
// "rate-limited like /test" asks for. Giving this one route a limiter of its
// own would make it stricter than the endpoint it is specified to match.
func (a *restAPI) handleProviderEntitlement(w http.ResponseWriter, r *http.Request, providerID string) {
	// Same auth posture as /test: an authenticated user once onboarding is
	// complete. Pre-onboarding there is no user to be, and nothing
	// configured to check either — the 404 below answers that case.
	onboardingDone := a.onboardingMgr != nil && a.onboardingMgr.IsComplete()
	if onboardingDone && r.Context().Value(UserContextKey{}) == nil {
		jsonErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if providerID == "" || isReservedProviderPathSegment(providerID) {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("unknown provider %q", providerID))
		return
	}

	row := a.configuredProviderRow(providerID)
	if row == nil {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("provider %q not configured", providerID))
		return
	}
	src := a.resolveProviderRow(providerID, row)

	// 409 — nothing to ask. A `cli` row has no HTTP listing; a custom row
	// has no catalog list to intersect with (FR-035: its catalogue is the
	// operator's own slugs).
	if src.custom || !entitlementListable(src.protocol) {
		jsonErr(w, http.StatusConflict, entitlementUnsupportedProtocolMsg)
		return
	}
	if strings.TrimSpace(src.apiBase) == "" {
		jsonErr(w, http.StatusConflict, entitlementNoEndpointMsg)
		return
	}

	// 422 — no resolvable key. ollama's /api/tags is unauthenticated, so a
	// missing key is not an error there; every other protocol needs one.
	apiKey := row.APIKey()
	if apiKey == "" && row.APIKeyRef != "" {
		resolved, err := a.resolveCredentialRef(row.APIKeyRef)
		if err != nil {
			slog.Warn("rest: entitlement: could not resolve provider credential",
				"provider", providerID, "ref", row.APIKeyRef, "error", err)
			jsonErr(w, http.StatusUnprocessableEntity, describeCredentialResolutionError(err))
			return
		}
		apiKey = resolved
	}
	if apiKey == "" && src.protocol != catalog.ProtocolOllama {
		jsonErr(w, http.StatusUnprocessableEntity, entitlementNoKeyMsg)
		return
	}

	key := entitlementCacheKey(providerID, row.APIKeyRef)
	if cached, ok := a.entitlements.get(key); ok {
		jsonOK(w, gen.EntitlementResponse{
			Models:    cached.models,
			CheckedAt: cached.checkedAt,
			Cached:    true,
		})
		return
	}

	// SEC-24: the api_base is operator-supplied through PUT /providers, so
	// it is SSRF-checked before any outbound call — except on a `locality =
	// local` row, which MEANS loopback/private and would fail the guard by
	// definition (the same reasoning as fetchLocalModels).
	local := src.locality == catalog.LocalityLocal
	checker := a.ssrfChk()
	if local {
		checker = providers_pkg.NoopChecker{}
	} else if a.ssrfChecker != nil {
		if err := a.ssrfChecker.CheckURL(r.Context(), src.apiBase); err != nil {
			slog.Warn("rest: entitlement: SSRF blocked api_base",
				"provider", providerID, "error", err)
			jsonErr(w, http.StatusUnprocessableEntity, "provider endpoint not allowed (SSRF guard)")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), entitlementUpstreamTimeout)
	defer cancel()
	upstream, err := providers_pkg.ListModels(ctx, src.protocol, src.apiBase, apiKey, checker)
	if err != nil {
		// X-12: nothing is cached on a failure, and the status the upstream
		// actually returned is the operator's most useful fact.
		var statusErr *providers_pkg.UpstreamStatusError
		if errors.As(err, &statusErr) {
			slog.Warn("rest: entitlement: upstream listing failed",
				"provider", providerID, "status", statusErr.Status)
			jsonErr(w, http.StatusBadGateway,
				fmt.Sprintf("could not fetch upstream model list: status %d", statusErr.Status))
			return
		}
		slog.Warn("rest: entitlement: upstream listing failed",
			"provider", providerID, "error", err)
		jsonErr(w, http.StatusBadGateway,
			fmt.Sprintf("could not fetch upstream model list: %v", err))
		return
	}

	checkedAt := time.Now().UTC()
	models := annotateEntitlement(src, upstream)
	a.entitlements.put(key, entitlementCacheEntry{
		providerID: providerID,
		models:     models,
		checkedAt:  checkedAt,
	})
	jsonOK(w, gen.EntitlementResponse{Models: models, CheckedAt: checkedAt, Cached: false})
}

// configuredProviderRow returns the representative CONFIGURED row for an id
// (FR-029: a seed template the operator never created is not a row), or nil.
func (a *restAPI) configuredProviderRow(providerID string) *config.ModelConfig {
	for _, m := range a.agentLoop.GetConfig().Providers {
		if m.IsVirtual() || isSeedTemplateRow(m) {
			continue
		}
		if strings.TrimSpace(m.Provider) == providerID {
			return m
		}
	}
	return nil
}

// entitlementListable reports whether a protocol has a live listing call at
// all. `cli` (and an unknown/empty protocol) does not.
func entitlementListable(p catalog.Protocol) bool {
	switch p {
	case catalog.ProtocolOpenAICompatible, catalog.ProtocolGoogle,
		catalog.ProtocolAnthropic, catalog.ProtocolOllama:
		return true
	default:
		return false
	}
}

// annotateEntitlement is FR-021's intersection: every catalog model for the
// provider first, in document order, annotated entitled/not with
// `limits: "known"`; then every model the listing returned that the catalog
// does not carry, in listing order, entitled with `limits: "unknown"`.
func annotateEntitlement(src providerRowSource, upstream []string) []gen.EntitlementModel {
	live := make(map[string]struct{}, len(upstream))
	for _, id := range upstream {
		live[id] = struct{}{}
	}
	known := make(map[string]struct{}, len(src.row.Models))
	out := make([]gen.EntitlementModel, 0, len(upstream)+len(src.row.Models))
	if src.known {
		for i := range src.row.Models {
			id := src.row.Models[i].ID
			if _, dup := known[id]; dup {
				continue
			}
			known[id] = struct{}{}
			_, entitled := live[id]
			out = append(out, gen.EntitlementModel{
				Id:       id,
				Entitled: entitled,
				Limits:   gen.EntitlementModelLimitsKnown,
			})
		}
	}
	seen := make(map[string]struct{}, len(upstream))
	for _, id := range upstream {
		if _, inCatalog := known[id]; inCatalog {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, gen.EntitlementModel{
			Id:       id,
			Entitled: true,
			Limits:   gen.EntitlementModelLimitsUnknown,
		})
	}
	return out
}
