//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package middleware — canonical-origin computation and Origin enforcement
// middleware for the Omnipus gateway.
//
// canonicalGatewayOrigin (and its exported alias CanonicalGatewayOrigin)
// derives the browser-facing origin for the main listener from the current
// config. It is used by the CSP builder (buildWorkspaceCSP via
// resolveMainOrigin) and by the CORS preflight handler to decide which origin
// receives Access-Control-Allow-Origin. Resolution order: explicit
// cfg.Gateway.PublicURL, then host:port heuristic, then empty-string fallback
// for wildcard binds (see FR-022 / MR-03 / FR-007e).
//
// RequireMatchingOriginOnStateChanging is a general-purpose CSRF fence for the
// SPA's own /api/v1/* routes. It is NOT applied to the /preview/<agent>/<token>/
// reverse-proxy path (ADR-044 / FR-012 / FR-023a): /preview/ uses
// path-token-only authentication, and a state-changing request originating
// from inside a previewed app (or a form POST proxied through it) carries no
// Origin header the SPA's main origin would recognise — by design, since the
// previewed app is not the SPA. Applying the Origin check to /preview/ would
// block legitimate previewed-app POSTs with 403. /preview/ is protected
// against foreign embeds by the CSP frame-ancestors directive instead
// (Threat Model T-04), and FR-013 additionally strips the operator's cookies
// before the request is proxied to the dev server.

package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// DevOriginDeniedEvent is the audit event name emitted when the Origin check
// rejects a state-changing request. Consumers (B6 dev-proxy wiring, tests)
// reference this constant rather than the literal string.
const DevOriginDeniedEvent = "dev.origin_denied"

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

// originMatches returns true when the request's Origin header matches the
// expected gateway origin. Comparison is case-insensitive on the host
// component (RFC 6454 §6.1).
func originMatches(requestOrigin, expectedOrigin string) bool {
	if requestOrigin == "" || expectedOrigin == "" {
		return false
	}
	return strings.EqualFold(strings.TrimRight(requestOrigin, "/"),
		strings.TrimRight(expectedOrigin, "/"))
}

// PreviewPathPrefix is the reverse-proxied dev-preview surface
// (rest_preview.go HandlePreview) and the single source of truth for that path
// prefix across the security boundary: csrf.go's defaultExemptPrefixes and this
// file's Origin-check exemption both reference it, so the CSRF-exempt and
// Origin-exempt prefixes can never drift apart. The gateway package
// (rest.go registration, rest_preview.go path-strip) references it as
// middleware.PreviewPathPrefix for the same reason.
//
// Preview URLs are tokenized (/preview/<agent>/<token>/…) so an exact-path
// exemption can never match, and the path token IS the credential — a
// previewed app must be able to submit state-changing requests through the
// proxy without carrying an Origin header the SPA's main origin recognises
// (ADR-044 / FR-012).
const PreviewPathPrefix = "/preview/"

// isStateChangingMethod returns true for HTTP methods that modify state
// (POST, PUT, PATCH, DELETE). GET, HEAD, OPTIONS are safe by RFC 7231
// and bypass the Origin check.
func isStateChangingMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// RequireMatchingOriginOnStateChanging returns a middleware that rejects
// state-changing requests (POST/PUT/PATCH/DELETE) whose Origin header is
// missing or does not match the canonicalised cfg.Gateway.Host.
//
// GET (and HEAD, OPTIONS) requests bypass the check entirely.
//
// On rejection the middleware:
// 1. Returns 403 with {"error": "origin mismatch"}.
// 2. Emits a "dev.origin_denied" audit entry if auditLog is non-nil.
//
// The getCfg closure is called per request so it sees the current (possibly
// hot-reloaded) config. Pass nil to disable — this causes all state-changing
// requests to be rejected (fail-closed semantics).
//
// The auditLog parameter may be nil when the audit subsystem has not been
// wired up (tests or degraded boot). Logging is best-effort; rejection always
// proceeds regardless of audit write success.
func RequireMatchingOriginOnStateChanging(
	getCfg func() *config.Config,
	auditLog *audit.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// /preview/ is prefix-exempt for ALL methods (FR-012) — see
			// PreviewPathPrefix's doc comment.
			if strings.HasPrefix(r.URL.Path, PreviewPathPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			// GET (and other safe methods) bypass the check.
			if !isStateChangingMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			var cfg *config.Config
			if getCfg != nil {
				cfg = getCfg()
			}

			expected := canonicalGatewayOrigin(cfg)
			requestOrigin := r.Header.Get("Origin")

			if !originMatches(requestOrigin, expected) {
				// Emit audit entry (best-effort).
				if auditLog != nil {
					if logErr := auditLog.Log(&audit.Entry{
						Timestamp: time.Now().UTC(),
						Event:     DevOriginDeniedEvent,
						Decision:  audit.DecisionDeny,
						Details: map[string]any{
							"origin":   requestOrigin,
							"expected": expected,
						},
					}); logErr != nil {
						// HIGH-7 (silent-failure-hunter): origin denials are a
						// security event (CSRF probe / cross-origin attack
						// surface). Logging at Debug hides probe campaigns
						// from operators tailing at Warn+. Promote to Warn so
						// the failure to record the security event is itself
						// visible.
						slog.Warn("origin middleware: audit log write failed", "error", logErr)
					}
				}

				writeJSONError(w, http.StatusForbidden, "origin mismatch")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
