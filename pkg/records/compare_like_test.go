// Omnipus — spec FR-022b / FR-011a / DS-4: LIKE's mechanics, stated so nobody
// has to read them off the implementation.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// ---------------------------------------------------------------------------
// Every expectation in this file is derived from the requirement, not observed.
// The four decisions FR-022b makes about `LIKE`, in the order they bite:
//
//  1. ANCHORED — it matches the WHOLE value, exactly as SQL's does.
//  2. `%` is any sequence, `_` is exactly one, `\` escapes.
//  3. The fold applies to the subject and the pattern's LITERAL SEGMENTS only.
//  4. `_` counts against the FOLDED subject, because folding changes rune count.
//
// (1) is the single most likely divergence in the whole requirement, because
// `LIKE` replaced an operator called `contains`. (4) is the one a test writer
// would otherwise read off the implementation, which is why the spec states it
// as literal cases and why they are reproduced here verbatim.
// ---------------------------------------------------------------------------

// TestLike_AnchoredWholeValue is decision (1). `'vendors' LIKE 'vendor'` is
// FALSE — the spec carries that exact case as a DS-4 literal.
func TestLike_AnchoredWholeValue(t *testing.T) {
	cases := []struct {
		subject, pattern string
		want             bool
		why              string
	}{
		{"vendors", "vendor", false, "DS-4, verbatim: a wildcard-free pattern is an EXACT match, not a prefix"},
		{"vendors", "vendor%", true, "DS-4, verbatim"},
		{"vendors", "vendors", true, "a wildcard-free pattern that IS the value"},
		{"vendors", "%vendor", false, "anchored at the end too"},
		{"vendors", "%vendor%", true, "the substring form, written the way SQL writes it"},
		{"vendors", "%", true, "matches everything — which is why FR-022a refuses it at the query layer"},
		{"", "", true, "the empty pattern matches only the empty value"},
		{"a", "", false, "and nothing else"},
		{"", "%", true, "`%` matches the empty sequence"},
	}
	for _, tc := range cases {
		if got := likeMatch(FoldKey(tc.subject), tc.pattern); got != tc.want {
			t.Errorf("%q LIKE %q = %v, want %v — %s", tc.subject, tc.pattern, got, tc.want, tc.why)
		}
	}
}

// TestLike_Wildcards is decision (2).
func TestLike_Wildcards(t *testing.T) {
	cases := []struct {
		subject, pattern string
		want             bool
		why              string
	}{
		{"abc", "a_c", true, "`_` is exactly one character"},
		{"abc", "a__c", false, "exactly one, not one-or-more"},
		{"abbc", "a__c", true, "two `_` are exactly two"},
		{"ac", "a_c", false, "`_` is one, never zero"},
		{"ac", "a%c", true, "`%` includes the empty sequence"},
		{"abbbbc", "a%c", true, "and any length"},
		{"abc", "%", true, "one `%` covers everything"},
		{"abc", "%%%", true, "runs of `%` collapse and still cover everything"},
		{"abcabc", "%abc", true, "backtracking: the last `abc` is the one that matches"},
		{"abcabd", "%abc", false, "and it fails when nothing does"},
		{"aXbXc", "a_b_c", true, "interleaved single wildcards"},
		{"banana", "%an%an%", true, "overlapping runs"},
		{"banana", "b%n%n%a", true, "several stars"},
		{"banana", "b%n%n%n%a", false, "one `n` too many"},
	}
	for _, tc := range cases {
		if got := likeMatch(FoldKey(tc.subject), tc.pattern); got != tc.want {
			t.Errorf("%q LIKE %q = %v, want %v — %s", tc.subject, tc.pattern, got, tc.want, tc.why)
		}
	}
}

// TestLike_Escape is `\`, and the reason folding must not touch it: folding the
// escape character's OPERAND would change what it escapes.
func TestLike_Escape(t *testing.T) {
	cases := []struct {
		subject, pattern string
		want             bool
		why              string
	}{
		{"100%", `100\%`, true, `an escaped % is a literal percent sign`},
		{"100x", `100\%`, false, "and matches nothing else"},
		{"100%", "100%", true, "unescaped, the % is a wildcard and matches the empty sequence"},
		{"a_c", `a\_c`, true, "an escaped _ is a literal underscore"},
		{"abc", `a\_c`, false, "and does not match an arbitrary character"},
		{`a\c`, `a\\c`, true, "an escaped backslash is a literal backslash"},
		{`50%off`, `50\%%`, true, "an escape followed by a real wildcard"},
		{`x`, `\`, false, "a trailing escape with nothing to escape is a literal backslash, which x is not"},
		{`\`, `\`, true, "...and it matches a literal backslash"},
	}
	for _, tc := range cases {
		if got := likeMatch(FoldKey(tc.subject), tc.pattern); got != tc.want {
			t.Errorf("%q LIKE %q = %v, want %v — %s", tc.subject, tc.pattern, got, tc.want, tc.why)
		}
	}
}

// TestLike_FoldingAndRuneCount is decisions (3) and (4), and it is the part of
// the requirement most likely to be discovered rather than implemented.
//
// FR-011a: `straße` is 6 runes and folds to `strasse`, which is 7; `ﬁle` folds 3
// to 4; `İ` folds 1 to 2. `_` means "exactly one character", so it CANNOT mean
// the same thing before and after folding — and the rule is that it counts
// against the FOLDED subject.
//
// The two cases the spec states as literals are the first two rows.
func TestLike_FoldingAndRuneCount(t *testing.T) {
	cases := []struct {
		subject, pattern string
		want             bool
		why              string
	}{
		{"straße", "stra_e", false,
			"DS-4, verbatim: the folded subject `strasse` has TWO characters where the pattern has one"},
		{"straße", "stra__e", true,
			"DS-4, verbatim: two `_` for the two characters `ß` folds to"},
		{"straße", "STRASSE", true,
			"a wildcard-free pattern folds too, so the German ß pair matches as an exact LIKE"},
		{"STRASSE", "straße", true,
			"and it is symmetric — the pattern's literal segment is folded, not just the subject"},
		{"straße", "stra%e", true,
			"`%` does not care how many characters folding produced"},
		{"ﬁx", "_x", false,
			"the ligature folds to `fi`, TWO characters, so one `_` leaves an `i` the pattern has no room for"},
		{"ﬁx", "__x", true,
			"two `_` have room for both"},
		{"ﬁle", "_ile", true,
			"NOT a counter-example, and it is here because it looks like one: `_` takes the `f` and the pattern's own literal `ile` absorbs the `i` the fold produced. A rune-count case only discriminates when the character AFTER the folded one differs from the first character the fold produced"},
		{"ﬁle", "file", true,
			"and the folded literal matches exactly"},
		{"İstanbul", "_stanbul", false,
			"Turkish dotted İ folds to i+U+0307 — TWO characters. This is the same fact AC-8.9e asserts for equality, arriving through `_`"},
		{"İstanbul", "__stanbul", true,
			"...so two `_` match it"},
		{"Müller", "m_ller", true,
			"an ordinary single-rune fold, so `_` is one"},
		{"ΣΊΣΥΦΟΣ", "σίσυφος", true,
			"Greek final sigma: the folded forms agree even though the raw spellings do not"},
	}
	for _, tc := range cases {
		if got := likeMatch(FoldKey(tc.subject), tc.pattern); got != tc.want {
			t.Errorf("%q LIKE %q = %v, want %v — %s", tc.subject, tc.pattern, got, tc.want, tc.why)
		}
	}
}

// TestLike_PatternMatchesEverything is FR-022a's predicate, which is the query
// layer's refusal condition rather than a matching rule.
func TestLike_PatternMatchesEverything(t *testing.T) {
	for _, pattern := range []string{"", "%", "%%", "%%%%"} {
		if !likePatternMatchesEverything(pattern) {
			t.Errorf("FR-022a: %q imposes no constraint and must be reported as such", pattern)
		}
	}
	for _, pattern := range []string{"a", "%a", "a%", "_", "%_%", `\%`, `\\`} {
		if likePatternMatchesEverything(pattern) {
			t.Errorf("FR-022a: %q DOES constrain the value and must not be refused", pattern)
		}
	}
	// The proof that the predicate is not a syntactic check on the string: an
	// escaped `%` is a literal, so `\%` constrains, while a bare `%` does not.
	if likeMatch("anything", `\%`) {
		t.Error(`\% is a literal percent sign and must not match "anything"`)
	}
	if !likeMatch("%", `\%`) {
		t.Error(`\% must match a literal percent sign`)
	}
}

// TestLike_LinearOnPathologicalPatterns is a bound, not a behaviour: FR-023c
// bounds the filter TREE and nothing bounds the PATTERN, so the matcher itself
// has to be the thing that does not blow up.
//
// A backtracking implementation written recursively goes exponential on this
// input. If this test ever hangs, that is what happened.
func TestLike_LinearOnPathologicalPatterns(t *testing.T) {
	subject := ""
	pattern := ""
	for i := 0; i < 24; i++ {
		subject += "a"
		pattern += "a%"
	}
	pattern += "b"
	if likeMatch(subject, pattern) {
		t.Errorf("the pathological pattern must not match a subject with no `b`")
	}
	// And the matching case, so the guard is not satisfied by a matcher that
	// always returns false.
	if !likeMatch(subject+"b", pattern) {
		t.Errorf("the same pattern must match when the trailing `b` is there")
	}
}
