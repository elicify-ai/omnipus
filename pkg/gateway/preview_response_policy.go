// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — the ADR-094 Mode 2 preview response policy: the CSP
// template (FR-014/DS-7), the response CORS policy (FR-012/S-2.8) and the
// redirect rule (FR-013/DS-2, S-5.4–S-5.5).
//
// The CSP template is a byte-stable literal — it is the spec's static
// tripwire oracle, transcribed directive for directive, so any drift flips
// TestPreviewCSPHeaderSet's byte-identical row. The redirect rule implements
// the DS-2 verdict table with the browser-realistic resolution base: the
// Location is resolved the way the OPERATOR'S BROWSER will resolve it —
// against the origin the preview was served on (the request's own Host) and
// the current document path — which is what makes the alias-origin rows
// (rows 3/4/10/11) agree with the comment pins ("the Director does not
// rewrite Host").
package gateway

import (
	"net/http"
	"net/url"
	"strings"
)

// previewMode1CSP is the ENTIRE CSP for Mode 1 static responses: the response
// is same-origin (it was served from the label host the page itself lives on),
// so the only thing a CSP must add is to refuse being framed by anyone else
// (FR-014). No source directives, no sandbox — the source list IS the origin
// itself.
const previewMode1CSP = "frame-ancestors 'none'"

// setPreviewStaticHeaders applies the preview static-response header set —
// the ONE code path for both modes' static serving: workspace security
// headers, the preview response CORS policy (FR-012), and the CSP (FR-014:
// Mode 1 overrides the workspace CSP with frame-ancestors 'none' only; Mode 2
// keeps the workspace CSP).
func setPreviewStaticHeaders(w http.ResponseWriter, r *http.Request, mainOrigin string) {
	setWorkspaceSecurityHeaders(w, mainOrigin)
	applyPreviewResponseCORS(w.Header())
	if previewHostLabelFromContext(r.Context()) != "" {
		w.Header().Set("Content-Security-Policy", previewMode1CSP)
	}
}

// buildPreviewCSP renders the Mode 2 CSP template byte-stably: origin is the
// canonical gateway origin, prefix the percent-encoded /preview/{agent}/{token}
// prefix (no trailing slash), wsOrigin the ws:// form of the same authority.
// This function IS the static tripwire — do not reformat, reorder, or
// "normalize" it; the trailing "\n" per directive is part of the template.
func buildPreviewCSP(origin, wsOrigin, prefix string) string {
	return "default-src 'none';\n" +
		"script-src " + origin + prefix + " 'unsafe-inline' 'unsafe-eval';\n" +
		"style-src " + origin + prefix + ";\n" +
		"img-src " + origin + prefix + " data:;\n" +
		"font-src " + origin + prefix + " data:;\n" +
		"media-src " + origin + prefix + ";\n" +
		"connect-src " + origin + prefix + " " + wsOrigin + prefix + ";\n" +
		"form-action " + origin + prefix + ";\n" +
		"worker-src " + origin + prefix + " blob:;\n" +
		"base-uri 'none';\n" +
		"object-src 'none';\n" +
		"frame-ancestors 'none';\n"
}

// wsOriginFor derives the ws:// form of an http(s) origin for the CSP
// connect-src directive.
func wsOriginFor(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return ""
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "wss://" + u.Host
	}
	return "ws://" + u.Host
}

// previewReservedRootSegments are the gateway's own first path segments. A
// root-relative redirect target under one of them is a gateway namespace —
// never the previewed app's — and is refused (DS-2 row 2) rather than
// re-rooted under the preview prefix (row 5).
var previewReservedRootSegments = map[string]struct{}{
	"api":             {},
	"preview":         {},
	"auth":            {},
	"webhook":         {},
	"library-preview": {},
}

// applyPreviewResponseCORS installs the preview response CORS policy (FR-012/
// S-2.8): every successful preview response carries ACAO:* with the upstream's
// own ACAO/ACAC deleted first — reads only, no allow-credentials, and the
// upstream can never widen or narrow the policy by emitting its own headers.
func applyPreviewResponseCORS(h http.Header) {
	h.Del("Access-Control-Allow-Origin")
	h.Del("Access-Control-Allow-Credentials")
	h.Set("Access-Control-Allow-Origin", "*")
}

// applyPreviewRedirectRule applies the DS-2 verdict table to a proxied
// response (Mode 2 only — DS-2b: under a label Host the app owns its host's
// navigation and every Location is emitted unchanged).
//
// Statuses: a redirect status is ruled; 304 is untouched (S-5.5); any other
// status has a stray Location deleted (S-5.5).
//
// Location verdicts, in order:
//
//  1. dot-segments (decoded, incl. %2e) anywhere in the target path → 502
//     (rows 6–7);
//  2. alias-origin + in-prefix (resolved the way the browser resolves it)
//     → emit raw unchanged (rows 1, 10, 11);
//  3. root-relative raw → gateway-reserved first segment → 502 (row 2),
//     else re-root under the prefix (row 5);
//  4. everything else → 502 with no Location (rows 3, 4, 8, 9).
func applyPreviewRedirectRule(
	resp *http.Response,
	clientOrigin *url.URL, // the origin the operator's browser is on
	clientBase *url.URL, // the client document URL (origin + prefix + path)
	prefix string,
) {
	switch resp.StatusCode {
	case http.StatusNotModified:
		return // 304's Location is untouched
	case http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect:
		// ruled below
	default:
		resp.Header.Del("Location")
		return
	}

	raw := resp.Header.Get("Location")
	if raw == "" {
		return
	}
	ref, err := url.Parse(raw)
	if err != nil {
		previewRedirectRefused(resp)
		return
	}

	// 1. Dot segments — decoded, so %2e counts (rows 6–7).
	if pathHasDotSegments(ref.Path) {
		previewRedirectRefused(resp)
		return
	}

	// 2. Resolve the way the browser will: against the client document URL.
	resolved := clientBase.ResolveReference(ref)
	if previewURLOnAliasOrigin(resolved, clientOrigin) && previewPathInPrefix(resolved.Path, prefix) {
		// Emit raw unchanged (rows 1, 10, 11).
		return
	}

	// 3. Root-relative raw: reserved-root refuse, else re-root under the
	// prefix (rows 2 and 5).
	if !ref.IsAbs() && !strings.HasPrefix(raw, "//") && strings.HasPrefix(ref.Path, "/") {
		first := ref.Path
		if i := strings.IndexByte(first[1:], '/'); i >= 0 {
			first = first[:i+1]
		}
		if _, reserved := previewReservedRootSegments[strings.ToLower(strings.Trim(first, "/"))]; reserved {
			previewRedirectRefused(resp)
			return
		}
		resp.Header.Set("Location", prefix+raw)
		return
	}

	// 4. Everything else: refuse.
	previewRedirectRefused(resp)
}

// previewRedirectRefused rewrites the proxied response to a 502 with no
// Location (DS-2's Verdict column: 502, Location empty).
func previewRedirectRefused(resp *http.Response) {
	resp.StatusCode = http.StatusBadGateway
	resp.Header.Del("Location")
}

// previewPathInPrefix reports whether p is the prefix or under it.
func previewPathInPrefix(p, prefix string) bool {
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// previewURLOnAliasOrigin reports whether u sits on MIN-003's alias set of
// clientOrigin: the same host:port, or a loopback sibling (localhost /
// 127.0.0.1) on the same port when the origin itself is loopback.
func previewURLOnAliasOrigin(u, origin *url.URL) bool {
	if u == nil || origin == nil {
		return false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	if previewURLPort(u) != previewURLPort(origin) {
		return false
	}
	uHost := strings.ToLower(u.Hostname())
	oHost := strings.ToLower(origin.Hostname())
	if uHost == oHost {
		return true
	}
	// Loopback siblings are interchangeable aliases of each other (MIN-003).
	loopback := map[string]struct{}{"localhost": {}, "127.0.0.1": {}}
	_, uLoop := loopback[uHost]
	_, oLoop := loopback[oHost]
	return uLoop && oLoop
}

// previewURLPort resolves a URL's port, filling the scheme default for an
// implicit port.
func previewURLPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// pathHasDotSegments reports whether the DECODED path contains "." or ".."
// segments.
func pathHasDotSegments(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}
