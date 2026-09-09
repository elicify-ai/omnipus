// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"bytes"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/grafana/regexp"
)

// regexEngineInvocations is a test seam (MV-13, spec test 1
// TestFileGrep_SensitiveLiteralFastPath): every time the RE2 engine actually
// evaluates a line — real regex mode, or the non-ASCII case-insensitive
// literal fallback — this counter increments. Production code never reads
// it; it exists solely so tests can assert the sensitive-literal fast path
// (and a prefilter's negative branch) never reach the regex engine at all.
var regexEngineInvocations atomic.Int64

// resetRegexEngineInvocationsForTest and regexEngineInvocationsForTest are
// the test-seam accessors. Unexported: co-located _test.go files in this
// package use them directly rather than exporting production API surface
// for a test-only counter.
func resetRegexEngineInvocationsForTest()  { regexEngineInvocations.Store(0) }
func regexEngineInvocationsForTest() int64 { return regexEngineInvocations.Load() }

// matcher is the compiled query. Exactly one matching strategy is active
// (MV-13):
//
//   - re != nil: full RE2 (Options.Regex == true). requiredLit, when
//     non-nil, is a substring proven to be required by every match — lines
//     failing a plain bytes.Contains check never reach the regex engine
//     (the "required-literal prefilter").
//   - re == nil && !fold: case-sensitive literal. litBytes is precompiled
//     once at Compile time so the per-line check (bytes.Index) is a single
//     zero-allocation call — the regex engine is never invoked at all.
//   - re == nil && fold && asciiFold: case-insensitive literal whose
//     pattern is pure ASCII. indexFoldASCII does a hand-rolled ASCII
//     case-folded substring search, zero allocation, regex engine never
//     invoked.
//   - re == nil && fold && !asciiFold: case-insensitive literal whose
//     pattern contains a non-ASCII byte. Falls back to the regex engine
//     ("(?i)" + escaped literal) so Unicode case folding (accented Latin,
//     Cyrillic, Greek, …) matches case-insensitively. Recorded divergence
//     (MV-13 "recorded mechanism, honestly testable"): RE2's (?i) applies
//     Unicode *simple case folding*, which is not always identical to the
//     reference's bytes.ToLower(line)-then-compare fold for exotic
//     characters whose fold target crosses scripts (e.g. a rune that folds
//     to an ASCII letter). Ordinary multi-script text (accented Latin,
//     Cyrillic, Greek, CJK) folds identically under both mechanisms —
//     TestFileGrep_CaseModesAndSmartCase pins this equivalence against a
//     representative non-ASCII corpus, not an exhaustive Unicode audit.
type matcher struct {
	lit       string
	litBytes  []byte // precompiled []byte(lit) for the sensitive fast path
	fold      bool
	asciiFold bool   // true when fold && lit is ASCII-only
	litLower  []byte // precompiled ASCII-lowered lit, for the folded scanner

	re          *regexp.Regexp
	requiredLit []byte // regex-mode only; nil when no literal is provably required

	foldRe *regexp.Regexp // insensitive-literal, non-ASCII fallback engine

	contextN int

	// words is KB-7b's multi-term AND matchers: one literal matcher per
	// whitespace-separated word of Options.Query, built ONLY when
	// Options.MatchAllWords is set, Options.Regex is false, and Query splits
	// into two or more words. A single-word query leaves this nil and falls
	// straight through the ordinary top-level matcher fields above —
	// MatchAllWords has no effect on a one-word query, since "all N words
	// present" and "the one word present" are the same question.
	//
	// Each entry is a full matcher built by the SAME compile() this type
	// itself goes through (one word's Options carrying that word as Query,
	// Regex forced false, Case forced to the PARENT query's resolved mode —
	// never re-derived per word, so "smart" case is one decision for the
	// whole query, not one inconsistent decision per word). That reuse is
	// deliberate: every existing fast path (sensitive literal, ASCII fold,
	// non-ASCII fold) is inherited for free, per word, with no new matching
	// code.
	words []*matcher
}

func clampContext(n int) int {
	if n < 0 {
		return 0
	}
	if n > 5 {
		return 5
	}
	return n
}

func (o Options) effectiveCase() CaseMode {
	switch o.Case {
	case CaseSensitive, CaseInsensitive:
		return o.Case
	default: // smart: any uppercase letter => sensitive (MV-13)
		for _, r := range o.Query {
			if unicode.IsUpper(r) {
				return CaseSensitive
			}
		}
		return CaseInsensitive
	}
}

func compile(o Options) (*matcher, error) {
	m := &matcher{contextN: clampContext(o.ContextLines)}
	cs := o.effectiveCase()

	if o.Regex {
		pat := o.Query
		if cs == CaseInsensitive {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, err
		}
		m.re = re
		if lit := requiredLiteral(pat); lit != "" {
			m.requiredLit = []byte(lit)
		}
		return m, nil
	}

	m.lit = o.Query
	m.litBytes = []byte(o.Query)
	m.fold = cs == CaseInsensitive
	if m.fold {
		if isASCIIString(o.Query) {
			m.asciiFold = true
			m.litLower = asciiLower(m.litBytes)
		} else {
			foldRe, err := regexp.Compile("(?i)" + regexp.QuoteMeta(o.Query))
			if err != nil {
				return nil, err
			}
			m.foldRe = foldRe
		}
	}

	// KB-7b: build the per-word matchers for MatchAllWords mode. Regex is
	// excluded above (this whole branch is under `if o.Regex` returning
	// early), so reaching here already means literal mode.
	if o.MatchAllWords {
		words := strings.Fields(o.Query)
		if len(words) >= 2 {
			m.words = make([]*matcher, 0, len(words))
			for _, w := range words {
				wm, err := compile(Options{Query: w, Case: cs})
				if err != nil {
					return nil, err
				}
				m.words = append(m.words, wm)
			}
		}
	}
	return m, nil
}

// lineMatch reports whether line matches, and the byte offset of the first
// match (for excerpt centering).
func (m *matcher) lineMatch(line []byte) (int, bool) {
	if m.re != nil {
		if m.requiredLit != nil && !bytes.Contains(line, m.requiredLit) {
			return 0, false
		}
		regexEngineInvocations.Add(1)
		loc := m.re.FindIndex(line)
		if loc == nil {
			return 0, false
		}
		return loc[0], true
	}
	if m.fold {
		if m.asciiFold {
			i := indexFoldASCII(line, m.litLower)
			if i < 0 {
				return 0, false
			}
			return i, true
		}
		regexEngineInvocations.Add(1)
		loc := m.foldRe.FindIndex(line)
		if loc == nil {
			return 0, false
		}
		return loc[0], true
	}
	// Sensitive-literal fast path (MV-13 test 1): litBytes is precompiled at
	// Compile time, so this is one zero-allocation bytes.Index call — the
	// regex engine is structurally unreachable from this branch.
	i := bytes.Index(line, m.litBytes)
	if i < 0 {
		return 0, false
	}
	return i, true
}

// nameMatch reports whether name matches the query, honoring
// Options.MatchAllWords the same way content scanning does (review finding
// I2). When MatchAllWords split the query into two or more words (m.words
// populated — see compile's own doc comment), a name matches only when
// EVERY word is present somewhere in it, each checked INDEPENDENTLY via the
// word's own matcher — never by testing the whole, unsplit query as one
// literal.
//
// Before this fix, nameMatch always delegated to lineMatch with the
// original, unsplit query — so a query like "quarterly report" (two words,
// MatchAllWords true, exactly what the SPA's Library file search bar always
// sends) looked for the literal substring "quarterly report", space
// included. A file named "quarterly-report-2026.pdf" contains both words
// but joins them with a hyphen, not a space, so the whole-literal test
// failed and the file produced NO hit at all. Per-word independent
// substring matching sidesteps any separator entirely — each word
// ("quarterly", "report") is checked on its own, so it matches regardless
// of whether the name joins its words with a hyphen, underscore, dot, or
// nothing. This is the ONLY name-matchable path for a binary file (FR-005:
// binary content is never scanned), so before this fix a second query word
// made every binary file (image, pdf, docx, xlsx, …) match strictly WORSE
// than a one-word query — never better.
func (m *matcher) nameMatch(name string) bool {
	if len(m.words) >= 2 {
		nameBytes := []byte(name)
		for _, w := range m.words {
			if _, ok := w.lineMatch(nameBytes); !ok {
				return false
			}
		}
		return true
	}
	_, ok := m.lineMatch([]byte(name))
	return ok
}

// wordMatch reports which of m.words matched line (setting found[i] true for
// each one, never clearing an already-true entry — presence accumulates
// across the whole file, it does not need to hold on every line at once),
// and returns the earliest match position on THIS line across every word,
// for excerpting the representative line. ok is false when no word matched
// on this particular line.
//
// Only ever called when len(m.words) >= 2 (KB-7b's MatchAllWords mode);
// found must be the same length as m.words.
func (m *matcher) wordMatch(line []byte, found []bool) (pos int, ok bool) {
	best := -1
	for i, w := range m.words {
		if p, matched := w.lineMatch(line); matched {
			found[i] = true
			ok = true
			if best == -1 || p < best {
				best = p
			}
		}
	}
	if !ok {
		return 0, false
	}
	return best, true
}

// allWordsFound reports whether every word matcher has been seen at least
// once — the KB-7b document-level AND gate: a file is a hit only when this
// is true by the time the whole file has been scanned.
func allWordsFound(found []bool) bool {
	for _, f := range found {
		if !f {
			return false
		}
	}
	return true
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func asciiLower(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return out
}

// indexFoldASCII returns the byte offset of the first ASCII-case-insensitive
// occurrence of needleLower (already lower-cased) in s, or -1. Zero
// allocation: it folds bytes in place during comparison rather than
// allocating a lowered copy of the haystack (MV-13 folded scanner).
func indexFoldASCII(s, needleLower []byte) int {
	n := len(needleLower)
	if n == 0 {
		return 0
	}
	if n > len(s) {
		return -1
	}
	c0 := needleLower[0]
	last := len(s) - n
	for i := 0; i <= last; i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != c0 {
			continue
		}
		j := 1
		for ; j < n; j++ {
			d := s[i+j]
			if d >= 'A' && d <= 'Z' {
				d += 'a' - 'A'
			}
			if d != needleLower[j] {
				break
			}
		}
		if j == n {
			return i
		}
	}
	return -1
}
