// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — shared workspace/preview security-header and
// Content-Type helpers (buildWorkspaceCSP, setWorkspaceSecurityHeaders,
// contentTypeForPath, resolveMainOrigin).
//
// These originally backed a GET /api/v1/workspace/{agent_id}/{path...}
// endpoint (HandleWorkspace) that was built but never registered on any mux
// (see issue #470) and was removed as dead code; the helpers themselves
// survive because pkg/gateway/rest_preview.go's ADR-044 `/preview/` handlers
// (the mechanism that superseded the workspace-read endpoint) still use them.
//
// Security headers :
// - Referrer-Policy: no-referrer
// - Content-Security-Policy: (locked-down policy, no framing)
// - X-Content-Type-Options: nosniff
//
// Streaming threshold :
// - ≤ 1,048,576 bytes → buffered read (os.ReadFile equivalent)
// - > 1,048,576 bytes → streamed (os.Open + io.Copy)

package gateway

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// workspaceStreamingThreshold is the file size boundary for buffered vs
// streamed delivery. Files above this size are streamed
// via io.Copy; files at or below this size are read into memory first.
const workspaceStreamingThreshold = 1 << 20 // 1,048,576 bytes

// buildWorkspaceCSP returns the Content-Security-Policy applied to all
// workspace-read and serve responses (FR-007 / FR-007c / FR-007e).
//
// Directives:
//   - default-src 'none' — deny everything by default
//   - script-src 'unsafe-inline' — permit inline scripts in HTML artifacts
//   - style-src 'unsafe-inline' — permit inline CSS
//   - img-src 'self' data: blob: — images from same origin + data URIs
//   - connect-src 'self' — hydrated SPA builds (Vite, Next.js exports)
//     can fetch their own /data.json; external network blocked. Changed
//     from 'none' in CR-01 / FR-007c.
//   - form-action 'self' — dev-iframe POSTs to its own origin; foreign-
//     origin POSTs blocked. Changed from 'none' in CR-01 to support
//     FR-023a (the dropped Origin middleware on /dev/).
//   - frame-ancestors '<mainOrigin>' — only the SPA's own origin may
//     embed served content. Falls back to '*' when mainOrigin is empty
//     (host=0.0.0.0/[::] without public_url set — see FR-007e). Defense
//     against T-04 (foreign embed of leaked-token URL).
//   - base-uri 'none' — forbid <base> tag hijacking
//   - object-src 'none' — no plugins
//
// mainOrigin is the SPA's browser-realistic origin (e.g.
// "http://1.2.3.4:5000"). Empty triggers the FR-007e fallback to '*'.
// The WARN about this fallback is emitted once at boot in gateway.Run
// (setupAndStartServices), not here — see F-8 fix.
func buildWorkspaceCSP(mainOrigin string) string {
	frameAncestors := "*"
	if mainOrigin != "" {
		frameAncestors = mainOrigin
	}
	return "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
		"img-src 'self' data: blob:; connect-src 'self'; form-action 'self'; " +
		"frame-ancestors " + frameAncestors + "; base-uri 'none'; object-src 'none'"
}

// workspaceContentType maps lowercase file extensions to MIME types per
// FR-020a. Keys include the leading dot.
var workspaceContentType = map[string]string{
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".css":  "text/css",
	".js":   "application/javascript",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".svg":  "image/svg+xml",
	".webp": "image/webp",
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".pdf":  "application/pdf",
}

// contentTypeForPath resolves the Content-Type for the given file path
// based on its extension. Unknown extensions return application/octet-stream.
func contentTypeForPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ct, ok := workspaceContentType[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}

// setWorkspaceSecurityHeaders writes the security headers to w.
//
// mainOrigin is the SPA's browser-realistic origin used for the CSP
// `frame-ancestors` directive (FR-007 / FR-007c). Pass "" to opt into the
// FR-007e fallback (`frame-ancestors '*'`) — appropriate when the gateway
// is bound to 0.0.0.0/[::] and the operator has not configured
// gateway.public_url. The fallback emits a one-time WARN at boot.
func setWorkspaceSecurityHeaders(w http.ResponseWriter, mainOrigin string) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", buildWorkspaceCSP(mainOrigin))
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// resolveMainOrigin computes the SPA's browser-realistic origin for the
// CSP frame-ancestors directive on /serve/ and /dev/ responses
// (FR-007 / FR-007e). Resolution order:
//
//  1. cfg.Gateway.PublicURL — explicit reverse-proxy origin set by operator.
//  2. <scheme>://<host>:<port> — derived from cfg.Gateway.Host+Port.
//     Returns "" when host="0.0.0.0" or "[::]" (a non-browser-realistic
//     wildcard bind), triggering the FR-007e '*' fallback.
//
// Returns "" when no realistic origin can be derived. Callers MUST pass
// the result through to setWorkspaceSecurityHeaders / buildWorkspaceCSP
// without further validation — the helpers handle the empty-string
// fallback path.
func resolveMainOrigin(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if pu := strings.TrimSpace(cfg.Gateway.PublicURL); pu != "" {
		return strings.TrimRight(pu, "/")
	}
	host := strings.TrimSpace(cfg.Gateway.Host)
	if host == "" || host == "0.0.0.0" || host == "[::]" || host == "::" {
		// Wildcard bind has no browser-realistic origin — return empty so
		// buildWorkspaceCSP falls back to '*' with the WARN-once log.
		return ""
	}
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
