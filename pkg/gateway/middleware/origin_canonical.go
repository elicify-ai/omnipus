// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// canonicalGatewayOrigin / CanonicalGatewayOrigin — the single derivation of
// the browser-facing origin for the main gateway listener.
//
// This lives in its OWN file, deliberately WITHOUT the rest of
// pkg/gateway/middleware's `//go:build !cgo` constraint, so it stays importable
// under CGO_ENABLED=1 builds — specifically the `go test -race` gate (race
// forces cgo). pkg/tools/web_serve.go depends on CanonicalGatewayOrigin for the
// ADR-044 single-canonical-origin preview URLs; without this split that import
// makes pkg/tools — and every race-tested package that transitively imports it,
// e.g. pkg/sysagent/tools — fail to build under -race, because the otherwise
// entirely-!cgo middleware package has zero files under cgo ("build constraints
// exclude all Go files in .../pkg/gateway/middleware"). This function is pure
// (config + stdlib only), so it is safe to compile under both cgo and !cgo, and
// has no runtime effect (production always builds CGO_ENABLED=0). The genuinely
// !cgo helpers (Origin/CSRF fence, session cookie, bypass gate) stay in their
// own !cgo files.

package middleware

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// canonicalGatewayOrigin returns the browser-facing origin for the main gateway
// listener. Used as the authoritative value for CORS Access-Control-Allow-Origin
// and CSP frame-ancestors directives.
//
// Resolution order (FR-022 / MR-03):
//  1. cfg.Gateway.PublicURL set → return it verbatim. Reverse-proxy case: the
//     operator tells us the public-facing origin.
//  2. host is a wildcard bind ("0.0.0.0", "::", "[::]") and PublicURL unset →
//     return empty string. The CALLER interprets empty as "fall back to
//     frame-ancestors '*'" and emits a boot WARN per FR-007e.
//  3. host already looks like a URL (contains "://") → parse and return scheme+host.
//  4. Otherwise → derive from host:port (http or https heuristic).
//
// Returns "" when the config is empty (caller should reject all state-changing
// requests when the expected origin cannot be derived — fail-closed).
func canonicalGatewayOrigin(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}

	// 1. PublicURL override (FR-022).
	if pu := strings.TrimSpace(cfg.Gateway.PublicURL); pu != "" {
		return pu
	}

	host := strings.TrimSpace(cfg.Gateway.Host)
	if host == "" {
		return ""
	}

	// 2. Wildcard-bind hosts: 0.0.0.0, ::, [::] (MR-03 / FR-007e).
	normHost := strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	switch normHost {
	case "0.0.0.0", "::", "::0":
		return ""
	}

	// 3. If host already looks like a URL, parse it.
	if strings.Contains(host, "://") {
		u, err := url.Parse(host)
		if err != nil || u.Host == "" {
			return ""
		}
		return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	}

	// 4. Bare host — derive scheme from port heuristic.
	port := cfg.Gateway.Port
	scheme := "http"
	if port == 443 {
		scheme = "https"
	}
	if port > 0 && port != 80 && port != 443 {
		return fmt.Sprintf("%s://%s:%d", scheme, host, port)
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

// CanonicalGatewayOrigin is the exported form, used by Track B's CSP builder
// and by gateway.Run for the allowedOrigin computation.
func CanonicalGatewayOrigin(cfg *config.Config) string {
	return canonicalGatewayOrigin(cfg)
}
