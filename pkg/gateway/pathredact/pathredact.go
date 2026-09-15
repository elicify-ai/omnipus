// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package pathredact removes credential-bearing segments from an HTTP request
// path before that path is written to a log line or an audit record (ADR-067
// FR-003e / MV-23).
//
// It lives in its own leaf package for one structural reason: two of the six
// recording sites are in pkg/gateway/middleware, and pkg/gateway imports
// pkg/gateway/middleware — so the middleware package cannot import pkg/gateway.
// A leaf package with no dependencies beyond the standard library is importable
// by both without a cycle.
//
// # Why the pre-existing helper could not be reused
//
// gateway.sanitisePreviewPath takes the token as an argument. Five of the six
// recording sites never hold the token — they see only *http.Request. Worse,
// its no-token fallback assumes the token is the THIRD path segment. That holds
// for /serve/<agent>/<token>/… and /dev/<agent>/<token>/…, and is false for
// /library-preview/<token>/<path> (ADR-067 §10.5), where the token is the
// SECOND segment. Reusing it there would blank the wrong segment and leave the
// credential in the record.
//
// This helper is therefore PREFIX-DRIVEN: the prefix of the path decides which
// segment is the credential, so no caller has to supply the token.
package pathredact

import "strings"

// TokenPlaceholder replaces the credential segment of a recognised
// token-bearing path. The surrounding segments are preserved so an operator can
// still triage which route was hit.
const TokenPlaceholder = "<redacted>"

// Placeholder is the STATIC fallback. It is returned whenever a path is
// recognised as token-bearing but cannot be redacted segment-wise (a malformed
// or truncated shape), and whenever a path contains control characters.
//
// The fallback is deliberately a constant rather than the input: FR-003e
// requires that the raw path never reach a record, so an unrecognised shape on
// a credential-bearing prefix must degrade to a string that contains none of
// the request, not to the request itself.
const Placeholder = "<redacted-path>"

// prefixRule maps a route prefix to the index of its credential segment.
//
// tokenSegment is a 0-based index into the path's segments AFTER the leading
// "/" has been trimmed. For "/serve/agent-1/TOK/index.html" the segments are
// ["serve", "agent-1", "TOK", "index.html"], so the token is index 2.
type prefixRule struct {
	prefix       string
	tokenSegment int
}

// tokenBearingPrefixes is the complete set of gateway routes that carry a
// bearer credential inside the URL path.
//
//   - /library-preview/<token>/<relative-path>  — ADR-067 §10.5, token index 1
//   - /preview/<agent>/<token>/…                — ADR-044, token index 2
//   - /serve/<agent>/<token>/…                  — legacy serve, token index 2
//   - /dev/<agent>/<token>/…                    — legacy dev proxy, token index 2
//
// Adding a new credential-in-path route REQUIRES a row here. Without one,
// RequestPath returns the path unchanged and the credential lands in the audit
// chain.
var tokenBearingPrefixes = []prefixRule{
	{prefix: "/library-preview/", tokenSegment: 1},
	{prefix: "/preview/", tokenSegment: 2},
	{prefix: "/serve/", tokenSegment: 2},
	{prefix: "/dev/", tokenSegment: 2},
}

// RequestPath returns a form of urlPath that is safe to write to a log line or
// an audit entry.
//
// Behaviour, in order:
//
//  1. An empty path is returned empty.
//  2. A path containing control characters returns Placeholder. net/http has
//     already percent-decoded r.URL.Path, so an attacker-supplied "%0A" arrives
//     as a real newline and would otherwise forge log records.
//  3. A path on a token-bearing prefix has its credential segment replaced with
//     TokenPlaceholder; every other segment is preserved.
//  4. A token-bearing path too short (or with an empty credential segment) to
//     redact returns Placeholder — never the input.
//  5. Any other path is returned unchanged: it carries no credential, and
//     operators need the real route for triage.
//
// RequestPath is idempotent: redacting an already-redacted path is a no-op.
func RequestPath(urlPath string) string {
	if urlPath == "" {
		return ""
	}
	if hasControlChars(urlPath) {
		return Placeholder
	}
	rule, ok := matchPrefix(urlPath)
	if !ok {
		return urlPath
	}
	segments := strings.Split(strings.TrimPrefix(urlPath, "/"), "/")
	if len(segments) <= rule.tokenSegment || segments[rule.tokenSegment] == "" {
		return Placeholder
	}
	segments[rule.tokenSegment] = TokenPlaceholder
	return "/" + strings.Join(segments, "/")
}

// matchPrefix returns the rule for the LONGEST matching prefix. Longest-match
// is used rather than first-match so that adding a prefix which contains an
// existing one cannot silently shadow it.
func matchPrefix(urlPath string) (prefixRule, bool) {
	var (
		best  prefixRule
		found bool
	)
	for _, rule := range tokenBearingPrefixes {
		if !strings.HasPrefix(urlPath, rule.prefix) {
			continue
		}
		if !found || len(rule.prefix) > len(best.prefix) {
			best = rule
			found = true
		}
	}
	return best, found
}

// hasControlChars reports whether s contains any C0 control character or DEL.
// These never appear in a legitimate request path and are the log-injection
// vector that survives token redaction.
func hasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// TokenBearingPrefixes returns the configured prefixes and their credential
// segment indexes. Exported for tests that assert the table's completeness
// against ADR-067 §10.5 rather than against whatever the table happens to say.
func TokenBearingPrefixes() map[string]int {
	out := make(map[string]int, len(tokenBearingPrefixes))
	for _, rule := range tokenBearingPrefixes {
		out[rule.prefix] = rule.tokenSegment
	}
	return out
}
