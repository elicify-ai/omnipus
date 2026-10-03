package mailhtml

// The bluemonday policy layer: registers the 73-property table as
// per-property value policies on a caller-owned *bluemonday.Policy, and
// adds the artefact §2 Attributes presentation markup (class/dir/lang,
// legacy table attributes, the <font> element) to the caller's element
// allow-list.
//
// WHY per-property builders, and why exactly ONE policy per property: at
// sanitize time bluemonday ORs every stylePolicy registered for a property —
// the first passing entry wins — so a lax duplicate would silently widen a
// tight group. Every property is registered exactly once below.

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// styleMaxValueBytes bounds one declaration value on the simple screen.
// Real mail values (font stacks, shadow lists, spacing shorthand) sit far
// below this; the bound is DoS hygiene on hostile values, not a styling
// limit. (The whole style attribute is separately capped at 4 KB and a
// <style> block at 16 KB by the callers — artefact §5/E-5.)
const styleMaxValueBytes = 1024

// simpleValueDenied substrings — the artefact §3 rows 2–5, 10–14 deny set as
// VALUE screens. Any occurrence fails the declaration. Lowercase; bluemonday
// hands the handler the lowercased value.
var simpleValueDenied = []string{
	"url(", "expression(", "behavior", "-moz-binding", "binding",
	"javascript:", "vbscript:", "image-set(", "image(", "var(",
	"@import", "@font-face", "<", ">", "\\", "&",
}

// cssSimpleValueOK is the bounded deny-screen for the URL-free, non-enum
// allow-listed properties: bounded length, no URL token, no function or
// binding construct, no injection syntax. Escape tricks need no entry here —
// bluemonday decodes escapes via douceur BEFORE the handler runs (P4's
// decode-then-check ordering is the parser's, not this screen's).
func cssSimpleValueOK(value string) bool {
	if value == "" || len(value) > styleMaxValueBytes {
		return false
	}
	for _, bad := range simpleValueDenied {
		if strings.Contains(value, bad) {
			return false
		}
	}
	return true
}

// cssBackgroundValueOK is the URL-bearing handler (artefact §2.C: use a
// value HANDLER, not a regex — the background shorthand interleaves
// url/color/position tokens). Every url() in the value must satisfy the CSS
// URL policy; the non-url remainder must pass the simple screen's deny list
// (a background: red url(...) center/cover is legitimate).
func cssBackgroundValueOK(value string) bool {
	if value == "" || len(value) > styleMaxValueBytes {
		return false
	}
	if !CSSValueURLsAllowed(value) {
		return false
	}
	for _, bad := range simpleValueDenied[1:] { // "url(" itself is screened per-token above
		if strings.Contains(value, bad) {
			return false
		}
	}
	return true
}

// ApplyStylePolicy registers the 73-property allow-list on p — the ONE
// table, shared verbatim by the style-attribute path (bluemonday's own
// douceur declaration parse: decode-then-check, whole attribute emptied on
// parse error, all-stripped attribute dropped) and by the <style>-block pass
// (stylesheet.go walks the same table through the same handlers).
func ApplyStylePolicy(p *bluemonday.Policy) {
	// URL-bearing members: the value handler.
	p.AllowStyles(urlBearingProperties...).MatchingHandler(cssBackgroundValueOK).Globally()
	// Enum-restricted members: the closed sets.
	for prop, values := range enumProperties {
		p.AllowStyles(prop).MatchingEnum(values...).Globally()
	}
	// Everything else on the allow-list: the bounded deny-screen.
	p.AllowStyles(simpleValueProperties...).MatchingHandler(cssSimpleValueOK).Globally()
}

// AllowPresentationMarkup adds the artefact §2 Attributes presentation
// surface to the caller's policy: the attributes every allowed element may
// carry, the legacy table-family attributes, and the <font> element with its
// three attributes. The caller keeps element ownership (its structural
// allow-list is the MC-10 baseline); this adds ONLY the presentation layer.
func AllowPresentationMarkup(p *bluemonday.Policy) {
	// Every allowed element: class (required for <style> rules to target
	// anything), dir (RTL counterpart of the direction property), lang.
	p.AllowAttrs("class", "dir", "lang").Globally()
	// Legacy presentational attributes on the table family — a large share
	// of real marketing mail (artefact §2 table row). background carries the
	// URL policy as for background-image.
	p.AllowAttrs("align", "valign", "bgcolor", "width", "height", "border").OnElements("table", "td", "th", "tr")
	p.AllowAttrs("cellpadding", "cellspacing").OnElements("table")
	p.AllowAttrs("background").Matching(bannedURLAttrRe).OnElements("table", "td", "th", "tr")
	// img presentation attributes (src stays the caller's pipeline; alt and
	// the geometry attrs are presentation-only). srcset stays denied — a
	// second URL surface for negligible mail value.
	p.AllowAttrs("border").OnElements("img")
	// font element (mirrors signaturePolicy's legacy presentation element).
	p.AllowElements("font")
	p.AllowAttrs("color", "face", "size").OnElements("font")
}

// bannedURLAttrRe is the URL policy for the HTML background ATTRIBUTE: the
// same value shapes as the CSS url() policy. (Attribute values are not
// parse-decoded the way declarations are, so the check is the same
// shape-allowlist the img src pipeline uses — non-conforming values never
// survive.)
var bannedURLAttrRe = cssURLAllowedRe
