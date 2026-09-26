// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package middleware — the ADR-094 planted-cookie detector and clearer
// (FR-015 / DS-5).
//
// A previewed app (or anything else on the shared origin) can plant a SECOND
// cookie under a reserved gateway name (omnipus-session / csrf /
// __Host-csrf). A browser then sends both on every request, and the gateway's
// cookie reads are undefined-ordered over the pair — the planted value may
// win. The detector runs OUTSIDE CSRFMiddleware and BEFORE any credential
// read (DS-5 rows 1–4, round-2 MAJ-002): it scans the RAW Cookie header for a
// duplicated reserved name, marks the credential read failed (so the session
// read returns ErrSessionNotFound rather than trusting a possibly-planted
// value), emits the founder-corrected clear set (FR-015 verbatim), and answers
// state-changing requests with the typed planted_cookie_cleared error.
//
// Scope (MIN-007): the detector NEVER fires on a Mode 1 preview-Host request
// — the host dispatcher routes those away before this middleware runs, so a
// previewed app's own csrf-named cookie is never intercepted here.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// plantedReservedCookieNames are the cookie names whose duplication triggers
// the detector — exactly the gateway's own auth/CSRF cookie names, matched
// case-sensitively (cookie names are case-sensitive per RFC 6265; a
// "CSRF=" duplicate is the app's own cookie and is never intercepted —
// DS-5 row 5). Built from the constants the rest of this package issues
// cookies under so the set cannot drift from issuance.
var plantedReservedCookieNames = map[string]struct{}{
	SessionCookieName:  {}, // omnipus-session
	CSRFCookieName:     {}, // __Host-csrf
	CSRFCookieNameHTTP: {}, // csrf
}

// plantedCredentialFailedKey is the context marker set when a planted
// duplicate was detected: every downstream credential read must treat the
// request as unauthenticated (FR-015 step 2 — "marks the credential read
// failed") rather than trusting a possibly-planted cookie value.
type plantedCredentialFailedKey struct{}

// WithCredentialReadFailed returns a context marking the request's
// credential read as failed.
func WithCredentialReadFailed(ctx context.Context) context.Context {
	return context.WithValue(ctx, plantedCredentialFailedKey{}, true)
}

// CredentialReadFailed reports whether the request's credential read was
// marked failed by the planted-cookie detector.
func CredentialReadFailed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(plantedCredentialFailedKey{}).(bool)
	return v
}

// plantedReservedDuplicates scans the RAW Cookie header lines and returns the
// reserved names that appear MORE THAN once (across all lines and pairs).
// Raw scanning, not r.Cookie(): net/http's cookie parse drops pairs it
// dislikes, which could hide a duplicate from a parse-based detector the
// same way Response.Cookies() hides a malformed Set-Cookie (see
// neutralizeReservedSetCookies for the same fail-open trap).
func plantedReservedDuplicates(r *http.Request) []string {
	counts := map[string]int{}
	for _, headerLine := range r.Header.Values("Cookie") {
		for _, pair := range strings.Split(headerLine, ";") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			name := pair
			if i := strings.IndexByte(pair, '='); i >= 0 {
				name = pair[:i]
			}
			name = strings.TrimSpace(name)
			if _, reserved := plantedReservedCookieNames[name]; reserved {
				counts[name]++
			}
		}
	}
	var dup []string
	for _, name := range []string{SessionCookieName, CSRFCookieNameHTTP, CSRFCookieName} {
		if counts[name] > 1 {
			dup = append(dup, name)
		}
	}
	return dup
}

// plantedCookiePathPrefixes returns the request path and every /-boundary
// ancestor through "/": "/api/v1/agents" → ["/api/v1/agents", "/api/v1",
// "/api", "/"].
func plantedCookiePathPrefixes(path string) []string {
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	var prefixes []string
	seen := map[string]bool{}
	p := path
	for {
		if !seen[p] {
			seen[p] = true
			prefixes = append(prefixes, p)
		}
		if p == "/" {
			break
		}
		i := strings.LastIndexByte(p, '/')
		if i <= 0 {
			if !seen["/"] {
				seen["/"] = true
				prefixes = append(prefixes, "/")
			}
			break
		}
		p = p[:i]
	}
	return prefixes
}

// plantedCookieExpires is the fixed past expiry stamped on every clear line.
var plantedCookieExpires = time.Unix(0, 0).UTC().Format(http.TimeFormat)

// appendPlantedClearSet writes the FR-015 clear set for name onto h: for
// every /-boundary prefix P of reqPath, at both P and P+"/", a clearing
// Set-Cookie line in the Domain=localhost form (at every such path INCLUDING
// "/") and, at every such path EXCEPT exactly "/", the host-only form (the
// genuine Path=/ host-only cookie is never in the set — CRIT-001). Every
// line is empty-valued with Max-Age=0 and a past Expires. Secure mirrors the
// request posture, except __Host-csrf which mirrors its ISSUANCE posture and
// is always Secure (DS-5 row 4). HttpOnly mirrors issuance per name.
func appendPlantedClearSet(h http.Header, name, reqPath string, requestSecure bool) {
	secure := requestSecure
	httpOnly := true
	if name == CSRFCookieName {
		secure = true // __Host-csrf never legitimately exists without Secure
	}
	if name == CSRFCookieNameHTTP {
		httpOnly = false // mirrors ClearCSRFCookie's issuance posture
	}
	for _, p := range plantedCookiePathPrefixes(reqPath) {
		for _, q := range []string{p, p + "/"} {
			line := name + "=; Path=" + q + "; Max-Age=0; Expires=" + plantedCookieExpires
			if secure {
				line += "; Secure"
			}
			if httpOnly {
				line += "; HttpOnly"
			}
			// The Domain form everywhere, INCLUDING at "/" — but never the
			// host-only form at exactly "/" (that is the genuine cookie's
			// shape; clearing it is a no-op at best and a logout loop at
			// worst, CRIT-001).
			h.Add("Set-Cookie", line+"; Domain=localhost")
			if q != "/" {
				h.Add("Set-Cookie", line)
			}
		}
	}
}

// PlantedCookieGuard is the ADR-094 FR-015 middleware: detect duplicated
// reserved cookie names on the RAW Cookie header, mark the credential read
// failed, emit the clear set, and refuse state-changing requests with the
// typed planted_cookie_cleared error. It must wrap OUTSIDE CSRFMiddleware so
// a planted csrf duplicate is answered by THIS envelope, not CSRF's generic
// mismatch (DS-5 row 3).
func PlantedCookieGuard() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dup := plantedReservedDuplicates(r)
			if len(dup) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			// FR-015 step 2: every downstream credential read treats this
			// request as unauthenticated — a GET then fails the session read
			// with 401 (DS-5 row 1) instead of trusting a planted value.
			r = r.WithContext(WithCredentialReadFailed(r.Context()))
			requestSecure := RequestIsSecure(r)
			for _, name := range dup {
				appendPlantedClearSet(w.Header(), name, r.URL.Path, requestSecure)
			}
			if isStateChangingMethod(r.Method) {
				writePlantedCookieCleared(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// plantedCookieClearedCode / plantedCookieClearedMessage are the typed
// error's wire values (FR-015 step 1, Q4 round-2 CRIT fix). The message is
// verbatim from the spec.
const (
	plantedCookieClearedCode    = "planted_cookie_cleared"
	plantedCookieClearedMessage = "Omnipus cleared cookies set by a preview — please retry"
)

// writePlantedCookieCleared answers a state-changing request whose Cookie
// header carried a duplicated reserved name: HTTP 403 with the standard JSON
// error envelope carrying code planted_cookie_cleared, plus the clear-set
// headers already stamped on w by the caller.
func writePlantedCookieCleared(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q}`,
		plantedCookieClearedCode, plantedCookieClearedMessage)
}
