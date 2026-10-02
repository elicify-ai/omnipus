package mailhtml

// CSS url() handling — the artefact §2.C/§3(8) URL policy applied to CSS
// values, plus the pin-then-rewrite helpers the mint pipeline composes with
// its <img> extraction (grill I-05 / artefact §5: without the pin, an
// allowed background-image would silently never render; without the
// rewrite, the CSP would block it).
//
// The value shape is EXACTLY the one mailImageSrcRe already accepts for
// <img>: a token-scoped /mail-preview/ path (post-rewrite), an eligible
// https absolute URL (pre-rewrite, consent-gated at serve time), or an
// inline data:image raster. Everything else is dropped — http:, //host,
// relative, data:image/svg+xml (SVG smuggles a format the rest of the
// pipeline deliberately excludes), every other data: type, and cid: inside
// CSS (CID routing stays an HTML-src feature; artefact §3 row 8).

import (
	"regexp"
	"strconv"
	"strings"
)

// cssURLAllowedRe matches the EXACT value shapes a CSS url() may carry.
// It is the CSS twin of the gateway's mailImageSrcRe:
//   - "/mail-preview/part/" and "/mail-preview/img/" — the token-scoped
//     same-gateway prefixes (post-rewrite form; the CSP's img-src names
//     this prefix),
//   - "data:image/(png|gif|jpe?g|webp);base64," — the inline raster shape
//     the CSP's img-src data: branch admits and the sanitizer therefore
//     fully owns (P1: data:image/svg+xml must NOT match — it does not),
//   - "https://" — the pre-rewrite eligible remote form; the proxy pins it
//     at serve time and the consent gate guards it.
var cssURLAllowedRe = regexp.MustCompile(`^(?:/mail-preview/(?:part|img)/|data:image/(?:png|gif|jpe?g|webp);base64,|https://)`)

// cssURLRe finds every url() token in a CSS value or stylesheet fragment:
// url( optionally quoted, optionally whitespace-padded, case-insensitive
// property name; the URL body stops at the closing paren or quote.
var cssURLRe = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'\s"][^)]*?))\s*\)`)

// cssImageURLAllowed reports whether one decoded url() body satisfies the
// policy. An empty body never passes.
func cssImageURLAllowed(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	return cssURLAllowedRe.MatchString(u)
}

// CSSValueURLsAllowed reports whether EVERY url() token inside one CSS
// value satisfies the policy. Values with no url() pass (the handler is
// only bound to the URL-bearing properties). One non-conforming URL fails
// the whole declaration — a background shorthand is dropped in full rather
// than rewritten partially (P1).
func CSSValueURLsAllowed(value string) bool {
	matches := cssURLRe.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return true
	}
	for _, m := range matches {
		u := m[1]
		if u == "" {
			u = m[2]
		}
		if u == "" {
			u = m[3]
		}
		if !cssImageURLAllowed(u) {
			return false
		}
	}
	return true
}

// ExtractCSSImageURLs returns every eligible https url() body in the raw
// HTML (style attributes and <style> blocks alike), first-seen order,
// deduplicated, capped at max — the CSS half of the mint-time pin list
// (MC-41's model: the proxy fetches ONLY recorded URLs, by index).
func ExtractCSSImageURLs(raw string, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range cssURLRe.FindAllStringSubmatch(raw, -1) {
		u := m[1]
		if u == "" {
			u = m[2]
		}
		if u == "" {
			u = m[3]
		}
		u = strings.TrimSpace(u)
		if !strings.HasPrefix(strings.ToLower(u), "https://") || seen[u] || len(out) >= max {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// RewriteCSSImageURLs replaces every eligible https url() body with the
// token-scoped preview path carrying the given token placeholder and the
// URL's index — the CSS half of pin-then-rewrite. Run AFTER extraction with
// the same list so indexes line up (the caller composes this with its own
// <img> rewrite exactly like the signature mint composes extract+rewrite
// today).
func RewriteCSSImageURLs(raw string, urls []string, prefix string) string {
	if len(urls) == 0 {
		return raw
	}
	out := cssURLRe.ReplaceAllStringFunc(raw, func(tok string) string {
		m := cssURLRe.FindStringSubmatch(tok)
		u := m[1]
		if u == "" {
			u = m[2]
		}
		if u == "" {
			u = m[3]
		}
		u = strings.TrimSpace(u)
		for i, want := range urls {
			if u == want {
				return "url(\"" + prefix + strconv.Itoa(i) + "\")"
			}
		}
		return tok
	})
	return out
}
