// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// canonicalGatewayOrigin / CanonicalGatewayOrigin — the single derivation of
// the browser-facing origin for the main gateway listener.
//
// This function is pure (config + stdlib only) and compiles under both
// CGO_ENABLED=0 and CGO_ENABLED=1.
//
// It was originally split into its own file to escape the blanket
// `//go:build !cgo` constraint that used to cover pkg/gateway and
// pkg/gateway/middleware, which made those packages compile to nothing under
// CGO_ENABLED=1 and so broke the `go test -race` gate (race forces cgo) for
// pkg/tools/web_serve.go, which depends on CanonicalGatewayOrigin for the
// ADR-044 single-canonical-origin preview URLs. That constraint has since been
// removed package-wide: the pure-Go guarantee (CLAUDE.md constraint #2) is
// enforced by building with CGO_ENABLED=0, not by a build tag. The split is
// therefore no longer load-bearing; the file is kept as-is to avoid churn.

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
