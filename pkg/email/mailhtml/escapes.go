package mailhtml

// CSS escape decoding. The artefact's P4 proof obligation: the parser
// (or this layer) must decode `\6f`-style escapes BEFORE policy lookup, so
// `col\6fr:red` is judged as `color` and SURVIVES while `ht\74 tps://…` is
// judged as its decoded — dangerous — URL and DROPS. Douceur's parser does
// NOT decode identifier escapes (verified: a style attribute
// `col\6fr:red` reaches bluemonday's property lookup as the literal
// `col\6fr` and is dropped — fail-closed, but it fails P4's survive half).
// This decoder restores the browser's interpretation, and it runs on BOTH
// paths: the property name in the stylesheet pass's own check, and the
// style attribute's property names via DecodeStyleAttrProperties before
// bluemonday sees them.

import (
	"regexp"
	"strings"

	"github.com/aymerick/douceur/parser"
)

// decodeCSSIdent decodes CSS identifier escapes per CSS Syntax §4.3.7:
// "\" + 1..6 hex digits + optional single whitespace terminator, or
// "\" + any other character (that character, literally).
func decodeCSSIdent(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		j := i + 1
		var val rune
		n := 0
		for n < 6 && j < len(s) {
			c := s[j]
			var v rune
			switch {
			case c >= '0' && c <= '9':
				v = rune(c - '0')
			case c >= 'a' && c <= 'f':
				v = rune(c-'a') + 10
			case c >= 'A' && c <= 'F':
				v = rune(c-'A') + 10
			default:
				goto hexDone
			}
			val = val*16 + v
			j++
			n++
		}
	hexDone:
		if n > 0 {
			// One whitespace after the escape is the terminator, consumed.
			if j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			b.WriteRune(val)
			i = j
			continue
		}
		// Escaped non-hex character: that character, literally.
		b.WriteByte(s[i+1])
		i += 2
	}
	return b.String()
}

// styleAttrRe matches a style ATTRIBUTE with a double- or single-quoted
// value (the pipeline's tolerant quoting), for the decode pass.
var styleAttrRe = regexp.MustCompile(`(?i)(\sstyle\s*=\s*)("([^"]*)"|'([^']*)')`)

// DecodeStyleAttrProperties rewrites every style attribute's PROPERTY NAMES
// with their escape-decoded form, parsed through douceur's declaration
// parser (values pass through untouched; only the identifier positions are
// decoded). A value the parser rejects is left as-is — bluemonday's own
// parse then fails closed on it, as before.
func DecodeStyleAttrProperties(html string) string {
	return styleAttrRe.ReplaceAllStringFunc(html, func(m string) string {
		groups := styleAttrRe.FindStringSubmatch(m)
		prefix, value := groups[1], groups[3]
		if value == "" {
			value = groups[4]
		}
		decls, err := parser.ParseDeclarations(value + ";")
		if err != nil || len(decls) == 0 {
			return m
		}
		parts := make([]string, 0, len(decls))
		for _, d := range decls {
			parts = append(parts, decodeCSSIdent(d.Property)+": "+d.Value)
		}
		// The re-emitted attribute is always double-quoted, so a decoded
		// value carrying a double quote (from a single-quoted attribute, or
		// a \22 identifier escape) must not terminate it — the raw re-emission
		// truncated the attribute and the styling was lost (the F4 finding).
		// &quot; re-decodes to the same byte when bluemonday parses the
		// attribute, so the value it judges is exactly the browser's reading
		// of the original. & is deliberately NOT escaped: bluemonday decodes
		// entities once, exactly as the browser would on the original text.
		return prefix + `"` + strings.ReplaceAll(strings.Join(parts, "; "), `"`, "&quot;") + `"`
	})
}
