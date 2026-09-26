// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway/middleware — the ADR-094 FR-010 Fetch-Metadata navigation
// guard.
//
// A previewed app (or any page the operator was lured onto) must not be able
// to TOP-LEVEL-NAVIGATE the operator's tab onto a gateway API address: a
// document navigation to /api/v1/... runs the API with the browser's ambient
// credentials (cookies) — the classic navigation-based CSRF/riding vector the
// CSRF middleware's Origin check cannot see (navigations carry no Origin the
// SPA could pre-register). The guard reads Sec-Fetch-Dest: when the request
// is a document navigation aimed at /api/v1/* and is not one of FR-010's
// GET-only exemptions, it is refused BEFORE the handler (S-2.13 — the
// requirement is that the request never reaches the handler; the refusal
// status is implementer freedom, A-1).
package middleware

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// navigationExemptLibraryDownload matches FR-010's first exemption,
// /api/v1/library/{workspaceId}/download — a GET the SPA legitimately
// navigates to for workspace downloads.
var navigationExemptLibraryDownload = regexp.MustCompile(`^/api/v1/library/[^/]+/download$`)

// NavigationGuard rejects top-level document navigations (Sec-Fetch-Dest:
// document) onto /api/v1/* except FR-010's GET-only exemptions:
//
//   - /api/v1/library/{id}/download (GET)
//   - /api/v1/media/workspace/...   (GET)
//   - /api/v1/media/...             (GET)
//
// The exemptions are GET-only: a POST to an exempt-shaped address is still
// refused (FR-010, Q3). Requests without Sec-Fetch-Dest pass — the guard
// narrows by fetch metadata, never by its absence (old clients, curl, the
// SPA's own XHR).
func NavigationGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "document") &&
				strings.HasPrefix(r.URL.Path, "/api/v1/") && !navigationExempt(r) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				w.WriteHeader(http.StatusForbidden)
				_, _ = fmt.Fprintf(w, `{"error":%q}`,
					"navigation to this gateway address is not allowed")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// navigationExempt reports whether r is one of FR-010's exemptions. Every
// exemption is GET-only (Q3): a non-GET is never exempt.
func navigationExempt(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/v1/media/") {
		// Covers both media exemptions: /api/v1/media/workspace/... and
		// /api/v1/media/... (the workspace prefix is the media prefix's
		// child).
		return true
	}
	return navigationExemptLibraryDownload.MatchString(p)
}
