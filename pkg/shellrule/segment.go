// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "strings"

// Segmenter splits a chained command into the individual commands it
// contains, at chain operators (`&&`/`||`/`;`/`|`/newline). The caller
// passes in pkg/tools/shell_subst_guard.go::splitShellSegments (a package
// value, not an export — see doc.go) so this package never implements a
// second tokenizer for the same job (ADR-092 D3: "no new parser").
type Segmenter func(command string) []string

// HeadResolver extracts the normalised command-name token at the head of
// one segment, mirroring
// pkg/tools/shell_subst_guard.go::shellCommandHeadDetailed's three-value
// return: (head, fromExpansion, normalised). fromExpansion is true when the
// head is built from a shell expansion (`$C f`) and therefore cannot be
// resolved by any text-based matcher. normalised is true when the returned
// token is not byte-identical to the command literal (case-folded, or a
// directory prefix stripped) — ADR-092 FR-040 requires an ALLOW rule never
// be satisfied from a normalised head, since normalisation is exactly the
// shape a look-alike binary (`./cat`, `CAT`) exploits.
type HeadResolver func(segment string) (head string, fromExpansion bool, normalised bool)

// blindSpotReason names why a segment's head cannot be confidently resolved
// to a rule-matchable binary. ADR-092 FR-020 requires each of these route
// to ask, never to a resolved head.
type blindSpotReason string

const (
	blindNone             blindSpotReason = ""
	blindUnbalancedQuote  blindSpotReason = "unbalanced quote in segment"
	blindBraceExpansion   blindSpotReason = "leading brace expansion"
	blindNoResolvableHead blindSpotReason = "no resolvable command head"
	blindFromExpansion    blindSpotReason = "command head built from a shell expansion"
	blindNormalisedHead   blindSpotReason = "command head only resolves via normalisation"
)

// classifySegment resolves seg's head via resolve and reports the blind-spot
// reason, if any. allowRuleContext is true when the classification result
// will be used to satisfy an ALLOW rule (FR-040's normalised-head
// restriction applies only there — a DENY match is still correct off a
// normalised head, since under-matching a deny is the unsafe direction).
func classifySegment(seg string, resolve HeadResolver, allowRuleContext bool) (head string, reason blindSpotReason) {
	if hasUnbalancedQuote(seg) {
		return "", blindUnbalancedQuote
	}
	if looksLikeBraceExpansion(seg) {
		return "", blindBraceExpansion
	}
	head, fromExpansion, normalised := resolve(seg)
	// fromExpansion is checked before the empty-head case: a head built
	// ENTIRELY from an expansion (`$C f`) reports head=="" AND
	// fromExpansion=true, and must be attributed to the expansion, not
	// folded into the generic "no resolvable head" reason (e.g. a bare
	// redirection like `> out`, which reports head=="" and
	// fromExpansion=false) — the two are different blind spots with
	// different causes.
	if fromExpansion {
		return "", blindFromExpansion
	}
	if head == "" {
		return "", blindNoResolvableHead
	}
	if normalised && allowRuleContext {
		return "", blindNormalisedHead
	}
	return head, blindNone
}

// hasUnbalancedQuote reports whether seg contains an odd number of
// unescaped `'` or `"` characters — the signature of a segment produced by
// splitting a quoted separator (`echo "a;b"` splits, byte-wise, into `echo
// "a` and `b"`, each holding one dangling quote). ADR-092 FR-020 (R12)
// requires this route to ask rather than resolve a head from either half.
func hasUnbalancedQuote(seg string) bool {
	single, double := 0, 0
	for i := 0; i < len(seg); i++ {
		switch seg[i] {
		case '\\':
			i++ // skip the escaped character, POSIX or not — never let it count
		case '\'':
			single++
		case '"':
			double++
		}
	}
	return single%2 != 0 || double%2 != 0
}

// looksLikeBraceExpansion reports whether seg's head is shaped like a bash
// brace expansion (`{cat,/etc/passwd}`), which the reused segment/head
// tokenizer does not model (it sees the literal token, not the words bash
// would expand it into). ADR-092 FR-020 (R14) requires this route to ask.
func looksLikeBraceExpansion(seg string) bool {
	trimmed := strings.TrimLeft(seg, " \t")
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	closeIdx := strings.IndexByte(trimmed, '}')
	if closeIdx < 0 {
		return false
	}
	return strings.ContainsRune(trimmed[:closeIdx], ',')
}
