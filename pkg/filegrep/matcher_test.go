// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"bytes"
	"testing"
)

// TestFileGrep_SensitiveLiteralFastPath pins MV-13 lever 1 (spec test 1): a
// case-sensitive literal pattern must never invoke the RE2 engine (seam-
// asserted via regexEngineInvocations), and the per-line match check must
// stay within the AllocsPerRun <= 2 gate.
func TestFileGrep_SensitiveLiteralFastPath(t *testing.T) {
	m, err := compile(Options{Query: "needle", Case: CaseSensitive})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if m.re != nil || m.foldRe != nil {
		t.Fatalf("sensitive literal must not compile any regex engine, got re=%v foldRe=%v", m.re, m.foldRe)
	}

	lines := [][]byte{
		[]byte("the quick brown fox jumps over the lazy dog and finds a needle in it"),
		[]byte("no match on this entirely unrelated line of ordinary text content"),
		[]byte("Needle with a capital N must not match the sensitive-case pattern"),
	}

	resetRegexEngineInvocationsForTest()
	var hits int
	for _, l := range lines {
		if _, ok := m.lineMatch(l); ok {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("want exactly 1 sensitive-literal hit, got %d", hits)
	}
	// MUTATION IT DIES ON: if lineMatch's literal branch is changed to route
	// through m.re/m.foldRe (e.g. compiling an ad-hoc regex per call instead
	// of using bytes.Index), this assertion catches it — the counter would
	// go non-zero.
	if got := regexEngineInvocationsForTest(); got != 0 {
		t.Fatalf("sensitive-literal fast path invoked the regex engine %d times, want 0", got)
	}

	// Positive control: prove the seam itself actually counts invocations,
	// so a "counter that never increments" bug can't hide behind this test.
	reMatcher, err := compile(Options{Query: "needle", Case: CaseSensitive, Regex: true})
	if err != nil {
		t.Fatalf("compile regex: %v", err)
	}
	resetRegexEngineInvocationsForTest()
	reMatcher.lineMatch(lines[0])
	if got := regexEngineInvocationsForTest(); got != 1 {
		t.Fatalf("regex-mode lineMatch should invoke the engine exactly once, got %d (seam is broken)", got)
	}

	// Alloc gate (MV-13, R2-MIN-008): <=2 allocations per scanned line.
	line := lines[0]
	allocs := testing.AllocsPerRun(1000, func() {
		m.lineMatch(line)
	})
	if allocs > 2 {
		t.Fatalf("sensitive-literal lineMatch allocs = %v, want <= 2", allocs)
	}
}

// TestFileGrep_CaseModesAndSmartCase pins MV-13/US-3 AS-9: smart/sensitive/
// insensitive over both content and names, and the folded-ASCII-scanner vs
// regex-fallback equivalence for non-ASCII patterns.
func TestFileGrep_CaseModesAndSmartCase(t *testing.T) {
	t.Run("sensitive matches only exact case", func(t *testing.T) {
		m, err := compile(Options{Query: "todo", Case: CaseSensitive})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m.lineMatch([]byte("a todo item")); !ok {
			t.Fatal("want lowercase todo to match")
		}
		if _, ok := m.lineMatch([]byte("a TODO item")); ok {
			t.Fatal("want uppercase TODO to NOT match sensitive todo")
		}
	})

	t.Run("insensitive matches any casing", func(t *testing.T) {
		m, err := compile(Options{Query: "todo", Case: CaseInsensitive})
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range []string{"a todo item", "a TODO item", "a ToDo item"} {
			if _, ok := m.lineMatch([]byte(line)); !ok {
				t.Fatalf("want insensitive match on %q", line)
			}
		}
		if !m.asciiFold {
			t.Fatal("pure-ASCII insensitive pattern must take the ASCII-fold scanner path")
		}
	})

	t.Run("smart-case derives from the pattern", func(t *testing.T) {
		lower, err := compile(Options{Query: "todo", Case: CaseSmart})
		if err != nil {
			t.Fatal(err)
		}
		if lower.fold != true {
			t.Fatal("lowercase pattern under smart-case must be insensitive")
		}
		if _, ok := lower.lineMatch([]byte("a TODO item")); !ok {
			t.Fatal("smart-case lowercase pattern should match TODO")
		}

		upper, err := compile(Options{Query: "TODO", Case: CaseSmart})
		if err != nil {
			t.Fatal(err)
		}
		if upper.fold {
			t.Fatal("uppercase pattern under smart-case must be sensitive")
		}
		if _, ok := upper.lineMatch([]byte("a todo item")); ok {
			t.Fatal("smart-case TODO should not match lowercase todo")
		}
		if _, ok := upper.lineMatch([]byte("a TODO item")); !ok {
			t.Fatal("smart-case TODO should match TODO")
		}
	})

	t.Run("smart-case applies to name matching too", func(t *testing.T) {
		m, err := compile(Options{Query: "report", Case: CaseSmart})
		if err != nil {
			t.Fatal(err)
		}
		if !m.nameMatch("Q3 report.md") {
			t.Fatal("want name match for report")
		}
		upper, err := compile(Options{Query: "Report", Case: CaseSmart})
		if err != nil {
			t.Fatal(err)
		}
		if upper.nameMatch("q3 report.md") {
			t.Fatal("smart-case Report should not match lowercase report in a name")
		}
	})

	t.Run("non-ASCII insensitive pattern falls back to the regex engine", func(t *testing.T) {
		m, err := compile(Options{Query: "café", Case: CaseInsensitive})
		if err != nil {
			t.Fatal(err)
		}
		if m.asciiFold {
			t.Fatal("non-ASCII pattern must not take the ASCII-fold scanner path")
		}
		if m.foldRe == nil {
			t.Fatal("non-ASCII insensitive literal must compile a fallback regex")
		}
		resetRegexEngineInvocationsForTest()
		if _, ok := m.lineMatch([]byte("visit the CAFÉ tomorrow")); !ok {
			t.Fatal("want insensitive Unicode match on CAFÉ")
		}
		if got := regexEngineInvocationsForTest(); got == 0 {
			t.Fatal("non-ASCII insensitive fallback should invoke the regex engine")
		}

		// Equivalence against the reference's bytes.ToLower fold for a
		// representative non-ASCII corpus (Latin-accented, Cyrillic, Greek).
		cases := []struct {
			line string
			want bool
		}{
			{"CAFÉ open now", true},
			{"café open now", true},
			{"no match here at all", false},
			{"ПРИВЕТ мир", false}, // different word entirely, no "café" anywhere
		}
		for _, c := range cases {
			refPos, refOK := referenceFold([]byte(c.line), "café")
			_, gotOK := m.lineMatch([]byte(c.line))
			if gotOK != refOK {
				t.Fatalf("line %q: fallback ok=%v, reference fold ok=%v", c.line, gotOK, refOK)
			}
			if gotOK && c.want && refPos < 0 {
				t.Fatalf("reference fold unexpectedly reported no position for %q", c.line)
			}
			if gotOK != c.want {
				t.Fatalf("line %q: got=%v want=%v", c.line, gotOK, c.want)
			}
		}
	})

	t.Run("Cyrillic and Greek fold equivalence", func(t *testing.T) {
		m, err := compile(Options{Query: "привет", Case: CaseInsensitive})
		if err != nil {
			t.Fatal(err)
		}
		lines := []string{"ПРИВЕТ мир", "привет мир", "Привет, друг", "no match"}
		for _, line := range lines {
			_, refOK := referenceFold([]byte(line), "привет")
			_, gotOK := m.lineMatch([]byte(line))
			if gotOK != refOK {
				t.Fatalf("line %q: fallback=%v reference=%v", line, gotOK, refOK)
			}
		}
	})
}

// referenceFold reproduces the ORIGINAL reference implementation's
// case-insensitive literal matching exactly (bytes.ToLower(line) then
// bytes.Index against strings.ToLower(lit)) — used only as the equivalence
// oracle in tests, never by production code.
func referenceFold(line []byte, lit string) (int, bool) {
	l := bytes.ToLower(line)
	q := bytes.ToLower([]byte(lit))
	i := bytes.Index(l, q)
	if i < 0 {
		return 0, false
	}
	return i, true
}

// TestFileGrep_RE2Semantics pins MV-4/US-3 AS-2: full RE2 regex mode
// (repetition, alternation, character classes) behaves as RE2, not as a
// literal search.
func TestFileGrep_RE2Semantics(t *testing.T) {
	m, err := compile(Options{Query: "ne{2}dle", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.lineMatch([]byte("find the needle here")); !ok {
		t.Fatal("ne{2}dle should match literal 'needle' under RE2 semantics")
	}
	if _, ok := m.lineMatch([]byte("find the nedle here")); ok {
		t.Fatal("ne{2}dle should not match 'nedle' (only one e)")
	}

	alt, err := compile(Options{Query: "(foo|bar)", Regex: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := alt.lineMatch([]byte("a foo appears")); !ok {
		t.Fatal("alternation should match foo")
	}
	if _, ok := alt.lineMatch([]byte("a bar appears")); !ok {
		t.Fatal("alternation should match bar")
	}
	if _, ok := alt.lineMatch([]byte("neither appears")); ok {
		t.Fatal("alternation should not match neither")
	}

	// Regex metacharacters in a LITERAL query (Options.Regex == false) must
	// match literally — FR-016 (the human bar never speaks regex).
	lit, err := compile(Options{Query: "f(x)", Regex: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lit.lineMatch([]byte("call f(x) now")); !ok {
		t.Fatal("literal f(x) should match its own text")
	}
	if _, ok := lit.lineMatch([]byte("call fx now")); ok {
		t.Fatal("literal f(x) should not match fx (parens are literal, not grouping)")
	}

	// Invalid regex must fail to compile (MV-1: 400 upstream).
	if _, err := compile(Options{Query: "(", Regex: true}); err == nil {
		t.Fatal("want a compile error for unbalanced paren regex")
	}
}

// TestFileGrep_LiteralPrefilterEquivalence pins MV-13's required-literal
// prefilter (spec test 4): a prefiltered matcher must decide every line
// identically to the same regex evaluated with no prefilter at all, across a
// range of patterns and lines — literal presence/absence is the only thing
// allowed to change which lines skip the regex engine, never the outcome.
func TestFileGrep_LiteralPrefilterEquivalence(t *testing.T) {
	patterns := []string{
		"needle",
		"ne{2}dle",
		".*needle.*",
		"^needle$",
		"nee?dle",
		"(foo|bar)needle",
		"[a-z]+needle[a-z]*",
	}
	lines := []string{
		"a needle in a haystack",
		"absolutely no match on this line",
		"needle",
		"fooneedle appears here",
		"barneedle also appears",
		"NEEDLE uppercase does not match sensitive",
		"nedle missing one e",
		"xneedlex surrounded",
		"",
		"needleneedleneedle repeated",
	}

	for _, pat := range patterns {
		t.Run(pat, func(t *testing.T) {
			withPrefilter, err := compile(Options{Query: pat, Regex: true})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			// Build a second matcher with the identical compiled regex but
			// force the prefilter off, to isolate its effect (plain scan).
			plain := *withPrefilter
			plain.requiredLit = nil

			for _, line := range lines {
				gotPos, gotOK := withPrefilter.lineMatch([]byte(line))
				wantPos, wantOK := plain.lineMatch([]byte(line))
				if gotOK != wantOK {
					t.Fatalf("pattern %q line %q: prefiltered ok=%v plain ok=%v", pat, line, gotOK, wantOK)
				}
				if gotOK && gotPos != wantPos {
					t.Fatalf("pattern %q line %q: prefiltered pos=%d plain pos=%d", pat, line, gotPos, wantPos)
				}
			}
		})
	}
}

// TestFileGrep_RequiredLiteralExtraction spot-checks the prefilter's literal
// extraction itself (not just its equivalence effect) so a regression that
// silently disables the prefilter (always returning "") passes the
// equivalence test above but is caught here.
func TestFileGrep_RequiredLiteralExtraction(t *testing.T) {
	cases := []struct {
		pattern string
		want    string // "" means "no prefilter expected"
	}{
		{"needle", "needle"},
		{"ne{2}dle", "dle"},
		{".*needle.*", "needle"},
		{"(foo|bar)", ""},      // alternation: no universally required literal
		{"(?i)needle", ""},     // case-folded literal: unsafe for a sensitive Contains check
		{"a?needle", "needle"}, // optional prefix, needle still required
		{"[a-z]+", ""},         // pure character class: nothing required
	}
	for _, c := range cases {
		got := requiredLiteral(c.pattern)
		if got != c.want {
			t.Errorf("requiredLiteral(%q) = %q, want %q", c.pattern, got, c.want)
		}
	}
}
