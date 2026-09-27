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
	"log/slog"
	"net/http"
	"net/url"
	"path"
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
// headers (referrer, nosniff), the preview response CORS policy (FR-012),
// and the CSP (FR-014). The CSP's mode is an EXPLICIT parameter (fix6 TDA-3)
// — never inferred from the request context — so a future static-serving call
// site must decide the mode and cannot silently render Mode 1 pages under the
// Mode 2 CSP (default-src 'none') or vice versa. Mode 1 (mode1=true) replaces
// the workspace CSP with frame-ancestors 'none' only. Mode 2 (mode1=false)
// replaces it with the byte-stable template (buildPreviewCSP) — the same
// header the dev proxy sets — so a static page cannot fetch or form-POST the
// gateway origin. prefix is the percent-encoded /preview/{agent}/{token}
// prefix (no trailing slash); Mode 1 ignores it.
func setPreviewStaticHeaders(w http.ResponseWriter, mode1 bool, mainOrigin, prefix string) {
	setWorkspaceSecurityHeaders(w, mainOrigin)
	applyPreviewResponseCORS(w.Header())
	if mode1 {
		w.Header().Set("Content-Security-Policy", previewMode1CSP)
		return
	}
	w.Header().Set("Content-Security-Policy",
		buildPreviewCSP(mainOrigin, wsOriginFor(mainOrigin), prefix))
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
// root-relative redirect whose first segment is one of them is refused
// (DS-2 row 2, S-5.2: raw /api/v1/config is 502, not re-rooted).
//
// Ambiguity, left as the dataset reads it: ADR-094 §2.3 step 3 and the
// FR-013 prose re-root every root-relative Location, which would emit
// /api/v1/config (and /auth/callback) under the token prefix. DS-2 row 2
// and S-5.2 require the 502. This list is what makes that row pass; it is
// not widened or shrunk here. Shrinking it fails TestPreviewRedirectRule's
// row 2, which is the pinned oracle.
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
// Location verdicts, in order (FR-013 / ADR-094 §2.3):
//
//  1. resolve against the request URL (WHATWG via ResolveReference). Dot
//     segments, including %2e, are removed BEFORE the prefix check — Go's
//     ResolveReference leaves ".." in some relative merges, so the path is
//     path.Clean'd as well. 502 only when that normalised path is not
//     alias-origin + in-prefix (rows 6–7). An in-prefix value — dot-segment
//     or not — emits its RESOLVED, normalised form in the raw value's own
//     relative/absolute shape (rows 1, 10, 11): what is checked is what is
//     emitted (DS-2's "Resolved origin/path" column);
//  2. else a raw root-relative value (not protocol-relative "//") whose
//     first segment is a gateway namespace → 502 (row 2; see
//     previewReservedRootSegments);
//  3. else re-root that root-relative value under the percent-encoded
//     prefix and apply the same normalised check (row 5);
//  4. everything else → 502 with no Location (rows 3, 4, 8, 9).
//
// The prefix is compared in decoded form. The value passed in is
// percent-encoded (EntityID allows spaces and semicolons); url.URL.Path is
// decoded, so comparing the escaped prefix against it 502s a legal target.
func applyPreviewRedirectRule(
	resp *http.Response,
	clientOrigin *url.URL, // the origin the operator's browser is on
	clientBase *url.URL, // the request URL the browser resolves against
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
		previewRedirectRefused(resp, raw, "unparseable_location")
		return
	}

	decodedPrefix := previewPrefixDecoded(prefix)
	resolved := previewResolveLocation(clientBase, ref)
	if previewURLOnAliasOrigin(resolved, clientOrigin) && previewNormalisedInPrefix(resolved, decodedPrefix) {
		previewEmitLocation(resp, resolved, ref.IsAbs())
		return
	}

	if !previewRawRootRelative(raw, ref) {
		previewRedirectRefused(resp, raw, "not_root_relative")
		return
	}
	if previewFirstSegmentReserved(ref.Path) {
		previewRedirectRefused(resp, raw, "reserved_root_segment")
		return
	}
	reRooted := prefix + raw
	reRef, parseErr := url.Parse(reRooted)
	if parseErr != nil {
		previewRedirectRefused(resp, reRooted, "unparseable_reroot")
		return
	}
	reResolved := previewResolveLocation(clientBase, reRef)
	if previewURLOnAliasOrigin(reResolved, clientOrigin) && previewNormalisedInPrefix(reResolved, decodedPrefix) {
		previewEmitLocation(resp, reResolved, false) // reRooted is root-relative by construction
		return
	}
	previewRedirectRefused(resp, reRooted, "outside_allowed_prefix")
}

// previewEmitLocation writes the RESOLVED, normalised Location the verdict
// just cleared (DS-2's "Resolved origin/path" column: what is checked is
// what is emitted — a raw in-prefix dot-segment value emits resolved, not
// raw). The raw value's form is preserved: an absolute Location stays
// absolute (rows 10–11), a relative one stays root-relative (rows 1, 5);
// query and fragment survive. When normalisation left the path unchanged
// the original wire encoding is kept byte-for-byte (EscapedPath honours the
// parsed RawPath — a percent-encoded agent id emits as the gateway mints
// it); only a dot-segment-resolved path is re-encoded.
func previewEmitLocation(resp *http.Response, resolved *url.URL, absolute bool) {
	out := *resolved
	if cleaned := previewNormalisedPath(&out); cleaned != out.Path {
		out.Path = cleaned
		out.RawPath = "" // force re-encode: the parsed encoding names the raw dot-segment path
	}
	if !absolute {
		out.Scheme, out.Host, out.User = "", "", nil
	}
	resp.Header.Set("Location", out.String())
}

// previewResolveLocation resolves ref against the request URL. A nil base
// (no request URL) falls back to the reference itself.
func previewResolveLocation(base, ref *url.URL) *url.URL {
	if base == nil {
		return ref
	}
	return base.ResolveReference(ref)
}

// previewPrefixDecoded returns prefix in the same decoded form as
// url.URL.Path. The caller passes the percent-encoded prefix.
func previewPrefixDecoded(prefix string) string {
	decoded, err := url.PathUnescape(prefix)
	if err != nil || decoded == "" {
		return prefix
	}
	return decoded
}

// previewNormalisedPath returns u's path with dot segments removed — the ONE
// normalisation both the prefix check and the emit use (previewEmitLocation),
// so what is checked is what is emitted. path.Clean (not filepath) so a
// backslash stays a character — URL paths are slash-separated on every OS,
// and the backslash re-root case depends on that.
func previewNormalisedPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	p := path.Clean(u.Path)
	if p == "." {
		p = "/"
	}
	return p
}

// previewNormalisedInPrefix reports whether u's path, after dot-segment
// removal, is the decoded prefix or under it.
func previewNormalisedInPrefix(u *url.URL, decodedPrefix string) bool {
	if u == nil {
		return false
	}
	return previewPathInPrefix(previewNormalisedPath(u), decodedPrefix)
}

// previewRawRootRelative reports a root-relative Location: a path that
// starts with "/" and is neither an absolute URL nor protocol-relative
// ("//host/..."). Protocol-relative values are excluded from re-rooting
// (DS-2 row 3).
func previewRawRootRelative(raw string, ref *url.URL) bool {
	if ref == nil || ref.IsAbs() || strings.HasPrefix(raw, "//") {
		return false
	}
	return strings.HasPrefix(ref.Path, "/")
}

// previewFirstSegmentReserved reports whether p's first segment is a
// gateway namespace (DS-2 row 2). p is the decoded path.
func previewFirstSegmentReserved(p string) bool {
	if p == "" || p[0] != '/' {
		return false
	}
	first := p[1:]
	if i := strings.IndexByte(first, '/'); i >= 0 {
		first = first[:i]
	}
	_, reserved := previewReservedRootSegments[strings.ToLower(first)]
	return reserved
}

// previewRedirectRefused rewrites the proxied response to a 502 with no
// Location (DS-2's Verdict column: 502, Location empty). fix6 SF-2: the
// refusal is recorded server-side — slog.Warn with the refusal reason and
// the REDACTED redirect target. The raw Location never reaches the log:
// query and fragment are stripped (a redirect target may carry secrets in
// its query — an OAuth code, a reset token), control characters are
// neutralised and token-bearing path segments are redacted via
// redactRequestPath (a re-rooted target carries the /preview/<agent>/<token>
// prefix — the token is a live credential).
func previewRedirectRefused(resp *http.Response, rawLocation, reason string) {
	slog.Warn("preview: redirect refused (502)",
		"reason", reason,
		"target", previewRedactedLocation(rawLocation))
	resp.StatusCode = http.StatusBadGateway
	resp.Header.Del("Location")
}

// previewRedactedLocation returns a raw Location value in a form safe to
// log: query and fragment stripped, capped at 256 characters, then
// redactRequestPath (control-character neutralisation plus token-bearing
// segment redaction). fix6 SF-2.
func previewRedactedLocation(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if len(raw) > 256 {
		raw = raw[:256]
	}
	return redactRequestPath(raw)
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
