// Omnipus — spec §8 R-10 / FR-022b: SQL's LIKE, evaluated in Go.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "strings"

// ---------------------------------------------------------------------------
// LIKE, AND THE FOUR THINGS ABOUT IT THAT ARE EASY TO GET WRONG
//
// FR-022b adopts SQL's `LIKE` with SQL's own meaning. That sentence hides four
// decisions, each of which the spec states because a test writer who is not told
// reads the answer off whatever the implementation happened to do:
//
//  1. IT IS ANCHORED. `LIKE` matches the WHOLE value, exactly as SQL's does.
//     `'vendors' LIKE 'vendor'` is FALSE; `'vendors' LIKE 'vendor%'` is TRUE.
//     This is the single most likely divergence in the whole requirement,
//     because `LIKE` replaced an operator called `contains` and R-9's phrasing
//     ("matches an element by pattern") reads like substring matching to anyone
//     who has just deleted one. A wildcard-free pattern is therefore an exact —
//     folded — match, which is the property FR-022a's empty-pattern refusal
//     depends on.
//
//  2. `%` AND `_` ARE THE WILDCARDS, `\` IS THE ESCAPE. `%` matches any sequence
//     of characters including none; `_` matches exactly one.
//
//  3. THE FOLD APPLIES TO THE SUBJECT AND TO THE PATTERN'S LITERAL SEGMENTS
//     ONLY — never to `%`, `_` or `\`. Folding a metacharacter would change what
//     it means; folding the escape character's operand would change what it
//     escapes.
//
//  4. `_` COUNTS AGAINST THE FOLDED SUBJECT, because folding CHANGES RUNE COUNT.
//     `straße` is 6 runes and folds to `strasse`, which is 7; `ﬁle` folds 3 to
//     4; `İ` folds 1 to 2. So `'straße' LIKE 'stra_e'` is FALSE (the folded
//     subject has two characters where the pattern has one) and
//     `'straße' LIKE 'stra__e'` is TRUE. Both are spec DS-4 cases and both are
//     asserted in compare_like_test.go.
//
// The subject arrives ALREADY FOLDED (textualAnswer folds it once), and the
// pattern arrives RAW so that this file can tell a metacharacter from a literal
// before anything is folded. Getting that boundary backwards is how (3) breaks.
// ---------------------------------------------------------------------------

type likeElemKind uint8

const (
	likeChar likeElemKind = iota // one literal character of the FOLDED pattern
	likeAny                      // `%` — any sequence, including empty
	likeOne                      // `_` — exactly one character
)

type likeElem struct {
	kind likeElemKind
	ch   rune
}

// parseLikePattern turns a raw SQL LIKE pattern into a flat element sequence,
// folding each maximal LITERAL RUN as a unit and leaving the metacharacters
// alone.
//
// Folding a run as a unit rather than rune by rune is required, not incidental:
// full case folding is defined over strings (`ß` -> `ss`, `ﬁ` -> `fi`), so a
// per-rune fold would produce a different answer for the same segment.
//
// `\` escapes the character after it, which then joins the literal run. A
// trailing `\` with nothing to escape is itself a literal backslash — SQL leaves
// this implementation-defined, and a refusal here would reject a pattern the
// caller may have meant, so the permissive reading is taken and stated.
func parseLikePattern(pattern string) []likeElem {
	var elems []likeElem
	var lit strings.Builder

	flush := func() {
		if lit.Len() == 0 {
			return
		}
		for _, r := range FoldKey(lit.String()) {
			elems = append(elems, likeElem{kind: likeChar, ch: r})
		}
		lit.Reset()
	}

	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '%':
			flush()
			// Collapse runs of `%`: they are equivalent, and collapsing keeps
			// the matcher's backtracking bounded on a pathological pattern.
			if n := len(elems); n == 0 || elems[n-1].kind != likeAny {
				elems = append(elems, likeElem{kind: likeAny})
			}
		case '_':
			flush()
			elems = append(elems, likeElem{kind: likeOne})
		case '\\':
			if i+1 < len(runes) {
				i++
				lit.WriteRune(runes[i])
				continue
			}
			lit.WriteRune('\\')
		default:
			lit.WriteRune(runes[i])
		}
	}
	flush()
	return elems
}

// likeMatch answers `subject LIKE pattern`, anchored, with `subject` already
// folded by the caller and `pattern` raw.
//
// The algorithm is the standard linear-time wildcard match with one backtrack
// point per `%`: O(len(subject) x len(pattern)) worst case and O(n) on ordinary
// patterns, with no recursion, so a pattern like `%a%a%a%a%a%a%a%b` cannot turn
// a query into a denial of service. FR-023c bounds the filter tree; nothing
// bounds the pattern, so the matcher has to be the thing that does not blow up.
func likeMatch(subject, pattern string) bool {
	s := []rune(subject)
	p := parseLikePattern(pattern)

	si, pi := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && (p[pi].kind == likeOne || (p[pi].kind == likeChar && p[pi].ch == s[si])):
			si++
			pi++
		case pi < len(p) && p[pi].kind == likeAny:
			star = pi
			mark = si
			pi++
		case star >= 0:
			// The last `%` consumes one more character and we retry from there.
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	// Anchoring: whatever is left of the pattern must be able to match nothing,
	// which only `%` can.
	for pi < len(p) && p[pi].kind == likeAny {
		pi++
	}
	return pi == len(p)
}

// likePatternMatchesEverything reports whether a pattern imposes no constraint
// at all — FR-022a's refusal condition.
//
// The requirement names the empty string and `'%'` alone. This function is
// deliberately BROADER: any pattern that parses to nothing but `%` elements
// matches every value, so `%%`, `%%%` and a pattern of only escaped-nothing land
// in the same refusal. The justification is engine-independent (revision 6, m-2):
// a pattern that matches every value returns a whole-table result dressed up as
// a filtered one, and that is true of `LIKE` in any implementation.
//
// An empty pattern parses to zero elements and is caught by the same test.
func likePatternMatchesEverything(pattern string) bool {
	for _, e := range parseLikePattern(pattern) {
		if e.kind != likeAny {
			return false
		}
	}
	return true
}
