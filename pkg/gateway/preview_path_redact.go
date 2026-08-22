// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — request-path redaction for logs and the audit chain
// (ADR-067 FR-003e, MV-23).
//
// A preview token lives in the URL path (ADR-067 §10.5), so any site that
// records a request path can write a live read credential into a log file or,
// worse, into the HMAC-chained audit record — which outlives log rotation.
//
// SIX places in pkg/gateway record a request path. Every one of them MUST pass
// the path through redactRequestPath (or pathredact.RequestPath directly, where
// the middleware packages cannot import pkg/gateway). The inventory and the
// build-breaking guard that fails when a SEVENTH appears live in
// preview_path_redact_test.go.
//
// Why a token-bearing path reaches a CSRF/rate-limit/bypass site at all. The
// rate limiter sits directly on the preview prefix, so a 429 there logs a path
// carrying a live token — that one is unconditional. The CSRF and bypass sites
// receive token-bearing paths from the OTHER credential-in-path routes
// (/serve/, /dev/), which are not prefix-exempt. /library-preview/ itself IS
// exempt (middleware.defaultExemptPrefixes) so that FR-003j's 405 and §10.3's
// policy survive a cookie-less POST — but that exemption is not what makes
// redaction unnecessary anywhere: redaction is a property of the recording
// SITE, and each of the six must hold it whatever route reaches it.

package gateway

import "github.com/elicify-ai/omnipus/pkg/gateway/pathredact"

// redactRequestPath is the package-gateway spelling of pathredact.RequestPath.
//
// The implementation lives in the leaf package pkg/gateway/pathredact because
// pkg/gateway imports pkg/gateway/middleware, so the middleware package — which
// holds two of the six recording sites — cannot import pkg/gateway. A leaf
// package is importable by both without a cycle, which is what keeps all six
// sites on ONE implementation instead of two that drift.
func redactRequestPath(urlPath string) string {
	return pathredact.RequestPath(urlPath)
}
