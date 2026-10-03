package mailhtml

// The <style>-block pass. bluemonday has no stylesheet sanitiser — raw
// element content passes through only under AllowUnsafe, which must never
// be enabled for attacker-authored mail (artefact §6, verified in its
// sanitize.go). This pass therefore: parses the block with douceur's
// grammar parser, keeps ONLY qualified rules with screened simple selectors
// and ONLY the @media at-rule (nested rules sanitised recursively), filters
// every declaration through the SAME 73-property table the attribute path
// uses, and emits from the kept parsed nodes. Malformed input fails CLOSED
// to an empty stylesheet (P8) — the body stays readable, the block is gone.

import (
	"strings"

	"github.com/aymerick/douceur/css"
	"github.com/aymerick/douceur/parser"
)

// vendorPrefixes is bluemonday's own prefix list (its sanitize.go strips
// these from a property before policy lookup). The stylesheet pass strips
// the SAME list so mso-line-height-rule and line-height-rule meet the same
// table — attribute and block decisions cannot drift.
var vendorPrefixes = []string{
	"-webkit-", "-moz-", "-ms-", "-o-", "mso-", "-xv-", "-atsc-", "-wap-",
	"-khtml-", "prince-", "-ah-", "-hp-", "-ro-", "-rim-", "-tc-",
}

// stripVendorPrefix lowercases and prefix-strips a property name the way
// bluemonday does before lookup.
func stripVendorPrefix(property string) string {
	p := strings.ToLower(strings.TrimSpace(property))
	for _, pre := range vendorPrefixes {
		p = strings.TrimPrefix(p, pre)
	}
	return p
}

// declarationAllowed applies the ONE policy table to one parsed
// declaration: prefix-stripped property membership, then the same value
// screen group the attribute path binds (URL-bearing → URL policy; enum →
// closed set; else the bounded deny-screen). The value is screened in its
// lowercased form, matching bluemonday's own handler input.
func declarationAllowed(property, value string) bool {
	// P4: decode identifier escapes BEFORE lookup - the browser reads
	// col-backslash-6fr as "color"; the policy must judge the same truth.
	prop := stripVendorPrefix(decodeCSSIdent(property))
	if !IsAllowedProperty(prop) {
		return false
	}
	lv := strings.ToLower(value)
	if values, ok := enumProperties[prop]; ok {
		for _, v := range values {
			if lv == v {
				return true
			}
		}
		return false
	}
	for _, u := range urlBearingProperties {
		if prop == u {
			return cssBackgroundValueOK(lv)
		}
	}
	return cssSimpleValueOK(lv)
}

// selectorAllowed screens one comma-separated selector for the v1 surface:
// simple type/class/descendant selectors only — no universal "*", no
// attribute selectors, no child/sibling combinators, no pseudo-classes, no
// ids (artefact §2: smaller parser surface, no mail needs them).
func selectorAllowed(sel string) bool {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return false
	}
	parts := strings.Fields(sel)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !simpleSelectorPartAllowed(part) {
			return false
		}
	}
	return true
}

// simpleSelectorPartAllowed reports whether one whitespace-separated
// selector part is a plain type or class name.
func simpleSelectorPartAllowed(part string) bool {
	if part == "*" || strings.ContainsAny(part, "*[]>,+~:#()=\"") {
		return false
	}
	if strings.HasPrefix(part, ".") {
		// class: at least one ident character after the dot
		return len(part) > 1
	}
	// type: an ident (letter-first per CSS, but servers emit any case —
	// require letter-or-underscore start, ident chars after)
	if len(part) == 0 {
		return false
	}
	c := part[0]
	isIdentStart := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
	if !isIdentStart {
		return false
	}
	for i := 1; i < len(part); i++ {
		c := part[i]
		isIdentChar := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '-' || c == '_'
		if !isIdentChar {
			return false
		}
	}
	return true
}

// mediaPreludeAllowed screens an @media prelude: a media query cannot fetch
// or execute anything — it only switches declarations — so the screen is
// bounded and construct-free, not a full grammar (artefact §2 at-rules row).
func mediaPreludeAllowed(prelude string) bool {
	prelude = strings.TrimSpace(prelude)
	if prelude == "" || len(prelude) > 512 {
		return false
	}
	for _, bad := range []string{"url(", "expression(", "behavior", "@", "<", ">", "\\", "var(", ";"} {
		if strings.Contains(strings.ToLower(prelude), bad) {
			return false
		}
	}
	return true
}

// SanitizeStylesheet runs the <style>-block pass: parse → walk → filter →
// emit. css is the block's text content; maxBytes bounds the input (a block
// over the cap is refused whole — the caller passes 16384 per E-5/artefact
// §5). Output is the emitted kept rules (possibly empty); a parse error
// yields "" (fail-closed). Work is bounded: the walk visits each parsed node
// once and emission stops at maxBytes of output, dropping remaining rules —
// the emitted text is always a complete, valid stylesheet.
func SanitizeStylesheet(cssText string, maxBytes int) string {
	if maxBytes <= 0 || len(cssText) > maxBytes {
		return ""
	}
	sheet, err := parser.Parse(cssText)
	if err != nil {
		return ""
	}
	if sheet == nil {
		return ""
	}
	var kept []*css.Rule
	outLen := 0
	for _, rule := range sheet.Rules {
		keptRule, ok := sanitizeRule(rule)
		if !ok {
			continue
		}
		n := len(keptRule.String())
		if outLen+n > maxBytes {
			break
		}
		outLen += n
		kept = append(kept, keptRule)
	}
	if len(kept) == 0 {
		return ""
	}
	out := css.NewStylesheet()
	out.Rules = kept
	return out.String()
}

// sanitizeRule filters one top-level rule. ok is false when nothing of the
// rule survives.
func sanitizeRule(rule *css.Rule) (*css.Rule, bool) {
	if rule == nil {
		return nil, false
	}
	switch rule.Kind {
	case css.QualifiedRule:
		sels := make([]string, 0, len(rule.Selectors))
		for _, s := range rule.Selectors {
			if selectorAllowed(s) {
				sels = append(sels, s)
			}
		}
		if len(sels) == 0 {
			return nil, false
		}
		decls := filterDeclarations(rule.Declarations)
		if len(decls) == 0 {
			return nil, false
		}
		out := css.NewRule(css.QualifiedRule)
		out.Selectors = sels
		out.Declarations = decls
		return out, true
	case css.AtRule:
		// Exactly ONE at-rule survives: @media (artefact §2 at-rules).
		// @import is a post-sanitisation stylesheet fetch; @font-face is the
		// data:-font path the CSP would otherwise admit; @keyframes and the
		// rest are parser surface with zero mail value. Everything else is
		// dropped whole, without dropping unrelated safe content.
		if !strings.EqualFold(rule.Name, "@media") {
			return nil, false
		}
		if !mediaPreludeAllowed(rule.Prelude) {
			return nil, false
		}
		nested := make([]*css.Rule, 0, len(rule.Rules))
		for _, sub := range rule.Rules {
			if keptSub, ok := sanitizeRule(sub); ok {
				nested = append(nested, keptSub)
			}
		}
		if len(nested) == 0 {
			return nil, false
		}
		out := css.NewRule(css.AtRule)
		out.Name = "@media"
		out.Prelude = strings.TrimSpace(rule.Prelude)
		out.Rules = nested
		return out, true
	}
	return nil, false
}

// filterDeclarations keeps only the parsed declarations that pass the shared
// table, preserving order (and !important — a legitimate mail-automation
// suffix; the value it modifies is still screened).
func filterDeclarations(decls []*css.Declaration) []*css.Declaration {
	if len(decls) == 0 {
		return nil
	}
	out := make([]*css.Declaration, 0, len(decls))
	for _, d := range decls {
		if d == nil {
			continue
		}
		if declarationAllowed(d.Property, d.Value) {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
