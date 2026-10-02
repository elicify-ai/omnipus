package mailhtml

// <style>-block extraction for the HTML pipeline. bluemonday sanitizes
// element CONTENT only under AllowUnsafe — which must never be enabled for
// attacker-authored mail (artefact §6). The pipeline therefore lifts each
// <style> block's content out BEFORE bluemonday runs, sanitizes it with
// SanitizeStylesheet, and re-inserts the sanitized CSS after sanitization —
// the same placeholder-then-substitute pattern the preview pipeline already
// uses for its token-scoped image paths.

import (
	"fmt"
	"regexp"
	"strings"
)

// styleBlockRe matches one <style> element's content. Case-insensitive,
// dot-all; the open tag's attributes are tolerated (mail emits <style
// type="text/css">).
var styleBlockRe = regexp.MustCompile(`(?is)(<style\b[^>]*>)(.*?)(</style>)`)

// StyleBlockPlaceholder renders the placeholder token for block i. It is
// plain ASCII text (no HTML punctuation) so bluemonday passes it through
// untouched, and it is namespaced so real mail content cannot collide.
func StyleBlockPlaceholder(i int) string {
	return fmt.Sprintf("MAILSTYLEBLOCK%dPLACEHOLDER", i)
}

// ExtractStyleBlocks replaces every <style> block's content with its
// placeholder and returns the rewritten HTML plus the RAW block contents in
// order. A block already over blockMaxBytes is replaced with an empty
// content here (the caller's sanitize pass would refuse it anyway) — the
// bounded-work guarantee applies before parsing, not after (P8).
func ExtractStyleBlocks(html string, blockMaxBytes int) (string, []string) {
	blocks := make([]string, 0, 4)
	out := styleBlockRe.ReplaceAllStringFunc(html, func(tag string) string {
		m := styleBlockRe.FindStringSubmatch(tag)
		content := m[2]
		if len(content) > blockMaxBytes {
			content = ""
		}
		blocks = append(blocks, content)
		return m[1] + StyleBlockPlaceholder(len(blocks)-1) + m[3]
	})
	return out, blocks
}

// ReplaceStyleBlocks swaps the placeholders back for their sanitized
// contents (block i's sanitized CSS, or "" to leave the element empty).
func ReplaceStyleBlocks(html string, sanitized []string) string {
	for i, css := range sanitized {
		html = strings.ReplaceAll(html, StyleBlockPlaceholder(i), css)
	}
	return html
}

// StyleMaxAttrBytes is the style-attribute cap (E-5/artefact §5: 4 KB,
// Gmail parity); StyleMaxBlockBytes is the <style> block cap (Gmail's own
// 16 KB bound — parity and DoS hygiene in one number).
const (
	StyleMaxAttrBytes  = 4096
	StyleMaxBlockBytes = 16 << 10
)

// styleAttrAnyRe matches a style ATTRIBUTE with any quoted value; the
// over-cap DECISION is a length check in the replacement function — RE2
// caps repeat counts at 1000, so the cap number cannot live in the pattern
// itself.
var styleAttrAnyRe = regexp.MustCompile(`(?i)\sstyle\s*=\s*("[^"]*"|'[^']*')`)

// StripOversizedStyleAttrs removes over-cap style attributes from raw HTML
// (bounded work on hostile input, P8).
func StripOversizedStyleAttrs(html string) string {
	return styleAttrAnyRe.ReplaceAllStringFunc(html, func(attr string) string {
		// attr = ` style="…"` / ` style='…'`; the value sits between the
		// first and last quote.
		first := strings.IndexAny(attr, `"'`)
		if first < 0 || first+1 >= len(attr) {
			return ""
		}
		value := attr[first+1 : len(attr)-1]
		if len(value) > StyleMaxAttrBytes {
			return ""
		}
		return attr
	})
}
