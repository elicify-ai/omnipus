// Package mailhtml implements the inbound mail CSS policy: the parsed,
// default-deny allow-list from the security artefact
// (receipts/css-allowlist.md — security-lead, 2026-10-02, adopted as-is by
// the w4 spec; 73 properties + exactly one at-rule) with real parsing, not
// character-class filtering.
//
// Two gates, different jobs (artefact §principles): the CSP/sandbox decides
// what fetched resources can DO; this sanitiser decides what styling EXISTS.
// Style attributes go through bluemonday's own CSS layer (douceur's
// declaration parser — decode-then-check, fail-closed on parse error);
// <style> blocks go through the stylesheet pass in stylesheet.go (douceur's
// grammar parse, walk, filter, emit from the kept nodes). The two paths share
// ONE property/value table below, so an attribute decision and a
// stylesheet decision can never drift.
//
// Inbound mail is attacker-authored. The outbound signature character-class
// regex (pkg/email/compose.go::sigStyleAttrRe) is deliberately NOT reused:
// it admits position:fixed and bans parentheses, so it both under- and
// over-blocks (artefact §5). Outbound/signature policies are untouched by
// this package.
package mailhtml

// The allow table, transcribed from the artefact §2 — 73 properties in five
// groups. Order within a group is the artefact's own order.

// typographyAndText is group A — 21 properties.
var typographyAndText = []string{
	"color", "opacity", "font", "font-family", "font-size", "font-style",
	"font-weight", "font-variant", "line-height", "letter-spacing",
	"word-spacing", "text-align", "text-transform", "text-decoration",
	"text-indent", "white-space", "word-break", "word-wrap", "overflow-wrap",
	"vertical-align", "direction",
}

// boxModelAndTables is group B — 38 properties.
var boxModelAndTables = []string{
	"margin", "margin-top", "margin-right", "margin-bottom", "margin-left",
	"padding", "padding-top", "padding-right", "padding-bottom",
	"padding-left",
	"width", "height", "max-width", "min-width", "max-height", "min-height",
	"border", "border-width", "border-style", "border-color",
	"border-top", "border-right", "border-bottom", "border-left",
	"border-radius", "border-top-left-radius", "border-top-right-radius",
	"border-bottom-left-radius", "border-bottom-right-radius",
	"border-collapse", "border-spacing", "table-layout",
	"display", "float", "clear", "overflow", "overflow-x", "overflow-y",
}

// backgroundsAndLists is group C — 9 properties. background and
// background-image are the URL-bearing members and get the URL policy, not
// the simple-value screen (artefact §2.C: the same value shape
// mailImageSrcRe accepts for <img>).
var backgroundsAndLists = []string{
	"background-color", "background-image", "background",
	"background-repeat", "background-position", "background-size",
	"list-style", "list-style-type", "list-style-position",
}

// decorative is group D — 2 properties (shadow syntax has no url() form).
var decorative = []string{"box-shadow", "text-shadow"}

// outlookAliases is group E — 3 properties under their PREFIX-STRIPPED
// names: bluemonday strips vendor prefixes (mso-, -ms-, …) from the property
// before policy lookup, so "mso-table-lspace" is looked up as
// "table-lspace". Listing the stripped names admits the Outlook-only
// declarations real mail carries; the prefix stripping widens nothing
// dangerous because no generic name like "binding" or "filter" is ever
// allow-listed (artefact §2.E).
var outlookAliases = []string{"line-height-rule", "table-lspace", "table-rspace"}

// AllowedProperties returns every allow-listed property name — the single
// table the attribute policy and the stylesheet pass both consult. Length is
// 73; the tests pin that count and the artefact's per-group counts.
func AllowedProperties() []string {
	out := make([]string, 0, 73)
	out = append(out, typographyAndText...)
	out = append(out, boxModelAndTables...)
	out = append(out, backgroundsAndLists...)
	out = append(out, decorative...)
	out = append(out, outlookAliases...)
	return out
}

// allowedPropertySet is the membership set built once.
var allowedPropertySet = func() map[string]bool {
	set := make(map[string]bool, 73)
	for _, p := range AllowedProperties() {
		set[p] = true
	}
	return set
}()

// IsAllowedProperty reports whether a (prefix-stripped, lowercased) property
// name is on the allow-list. Everything not on it is dropped — default deny;
// the artefact's deny list names only the entries with an attack or parity
// reason and is not exhaustive by construction.
func IsAllowedProperty(name string) bool {
	return allowedPropertySet[name]
}

// urlBearingProperties get the URL policy handler.
var urlBearingProperties = []string{"background", "background-image"}

// enumProperties map each enum-restricted property to its closed value set
// (artefact §2.B: display's enum list is exactly these nine — no grid, no
// contents; overflow's four; float/clear's members; the two table keywords;
// direction's two). Values are matched case-insensitively (bluemonday hands
// the handler a lowercased value).
var enumProperties = map[string][]string{
	"display":             {"inline", "inline-block", "block", "none", "table", "table-row", "table-cell", "flex", "inline-flex"},
	"float":               {"left", "right", "none"},
	"clear":               {"none", "left", "right", "both"},
	"overflow":            {"visible", "hidden", "auto", "scroll"},
	"overflow-x":          {"visible", "hidden", "auto", "scroll"},
	"overflow-y":          {"visible", "hidden", "auto", "scroll"},
	"border-collapse":     {"collapse", "separate"},
	"table-layout":        {"auto", "fixed"},
	"direction":           {"ltr", "rtl"},
	"list-style-position": {"inside", "outside"},
}

// simpleValueProperties are the allow-listed properties whose values are
// screened by the bounded deny-screen (cssSimpleValueOK): every listed
// property EXCEPT the URL-bearing and enum members. Shadows, lengths,
// colours, font stacks — visual-only values with no URL form.
var simpleValueProperties = func() []string {
	enumSet := make(map[string]bool, len(enumProperties))
	for p := range enumProperties {
		enumSet[p] = true
	}
	urlSet := make(map[string]bool, len(urlBearingProperties))
	for _, p := range urlBearingProperties {
		urlSet[p] = true
	}
	var out []string
	for _, p := range AllowedProperties() {
		if enumSet[p] || urlSet[p] {
			continue
		}
		out = append(out, p)
	}
	return out
}()
