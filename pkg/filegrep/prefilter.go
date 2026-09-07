// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import "regexp/syntax"

// requiredLiteral finds a literal substring that MUST appear (byte-for-byte,
// case-sensitively) in any string the given RE2 pattern matches, so
// matcher.lineMatch can bytes.Contains-prefilter a line before paying for a
// full RE2 evaluation (MV-13 required-literal prefilter). It returns "" when
// no such literal can be proven safe — e.g. the pattern is entirely
// case-folded, has top-level alternation, or is fully optional — in which
// case every line goes straight to the regex engine, exactly as the
// reference does for every line.
//
// grafana/regexp compiles the same RE2 syntax as the standard library, so
// parsing with the standard library's regexp/syntax package (rather than a
// duplicate) is safe and exact for this purpose.
func requiredLiteral(pattern string) string {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return ""
	}
	return longestRequiredLiteral(re.Simplify())
}

// longestRequiredLiteral walks a parsed, simplified regexp AST and returns
// the longest literal substring provably required by every match. It is
// conservative by construction: any AST shape it doesn't specifically
// recognize as "definitely required" (alternation, optional/star
// repetition, character classes, anchors, …) yields "" for that subtree,
// which only costs prefilter selectivity — never correctness, since an
// empty answer disables the prefilter rather than risking a false negative.
func longestRequiredLiteral(re *syntax.Regexp) string {
	switch re.Op {
	case syntax.OpLiteral:
		// A case-folded literal (from an (?i) pattern) doesn't survive a
		// case-sensitive bytes.Contains prefilter check — skip it.
		if re.Flags&syntax.FoldCase != 0 {
			return ""
		}
		return string(re.Rune)
	case syntax.OpCapture:
		if len(re.Sub) == 1 {
			return longestRequiredLiteral(re.Sub[0])
		}
	case syntax.OpPlus:
		// x+ requires at least one x.
		if len(re.Sub) == 1 {
			return longestRequiredLiteral(re.Sub[0])
		}
	case syntax.OpRepeat:
		// x{min,max} requires x only when min>=1.
		if re.Min >= 1 && len(re.Sub) == 1 {
			return longestRequiredLiteral(re.Sub[0])
		}
	case syntax.OpConcat:
		best := ""
		for _, sub := range re.Sub {
			if lit := longestRequiredLiteral(sub); len(lit) > len(best) {
				best = lit
			}
		}
		return best
	}
	// OpStar, OpQuest, OpAlternate, OpAnyChar*, OpCharClass, anchors,
	// OpEmptyMatch, etc: no literal is provably required from every match.
	return ""
}
