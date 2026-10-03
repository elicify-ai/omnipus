package mailhtml

// <style>-block extraction for the HTML pipeline. bluemonday sanitizes
// element CONTENT only under AllowUnsafe — which must never be enabled for
// attacker-authored mail (artefact §6). The pipeline therefore lifts each
// <style> block out BEFORE bluemonday runs, sanitizes it with
// SanitizeStylesheet, and re-inserts the sanitized CSS AFTER sanitization.
//
// Why re-insertion carries no placeholder: an earlier revision marked each
// lifted block with a fixed literal ("MAILSTYLEBLOCK<i>PLACEHOLDER") and
// re-inserted it with strings.ReplaceAll over the whole sanitized document.
// The literal was typable by any sender: planted inside an attribute value it
// met the post-sanitise substitution, the sanitized CSS — quotes included —
// was written INTO that attribute, its quotes terminated the value, and the
// attacker's trailing text became fresh attributes the sanitiser never saw
// (a style="position:fixed" the 73-property allowlist never judged). The
// placeholder no longer exists: extraction REMOVES the element, and
// re-insertion PREPENDS the sanitized blocks at the top of the sanitized
// fragment. There is nothing to predict, plant or collide with, and no
// substitution ever fires inside a position mail content chose.

import (
	"regexp"
	"strings"
)

// styleBlockRe matches one <style> element's span. Case-insensitive,
// dot-all; the open tag's attributes are tolerated (mail emits <style
// type="text/css">).
var styleBlockRe = regexp.MustCompile(`(?is)(<style\b[^>]*>)(.*?)(</style>)`)

// ExtractStyleBlocks removes every <style> element from the HTML and returns
// the rewritten HTML plus the RAW block contents in order. A block already
// over blockMaxBytes is removed with empty content (the caller's sanitize
// pass would refuse it anyway) — the bounded-work guarantee applies before
// parsing, not after (P8).
func ExtractStyleBlocks(html string, blockMaxBytes int) (string, []string) {
	blocks := make([]string, 0, 4)
	out := styleBlockRe.ReplaceAllStringFunc(html, func(tag string) string {
		m := styleBlockRe.FindStringSubmatch(tag)
		content := m[2]
		if len(content) > blockMaxBytes {
			content = ""
		}
		blocks = append(blocks, content)
		return ""
	})
	return out, blocks
}

// ReplaceStyleBlocks prepends the sanitized CSS — each block as a fresh
// <style> element, in the original block order — at the very top of the
// sanitized fragment. The position is the pipeline's own choice inside
// already-sanitized output, never one mail content influenced. Styling is
// unchanged by the move: block rules apply document-wide wherever they sit,
// their relative order to each other is preserved, and inline style
// attributes beat block rules regardless of position. An empty sanitized CSS
// re-inserts an empty element (the block existed, it just carries nothing
// safe).
func ReplaceStyleBlocks(html string, sanitized []string) string {
	if len(sanitized) == 0 {
		return html
	}
	var b strings.Builder
	for _, css := range sanitized {
		b.WriteString("<style>")
		b.WriteString(css)
		b.WriteString("</style>")
	}
	b.WriteString(html)
	return b.String()
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
