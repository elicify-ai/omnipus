// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"bytes"
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

func (m *matcher) nameMatch(name string) bool {
	_, ok := m.lineMatch([]byte(name))
	return ok
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
