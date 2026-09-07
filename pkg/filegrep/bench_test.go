// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"context"
	"os"
	"strings"
	"testing"
)

// corpusFS opens the FIXED, committed benchmark corpus
// (pkg/filegrep/testdata/corpus/, generated deterministically by
// testdata/gen/main.go — see that file's header) as a confined fs.FS via
// os.Root, matching production's confinement mechanism.
func corpusFS(b *testing.B) []Root {
	b.Helper()
	r, err := os.OpenRoot("testdata/corpus")
	if err != nil {
		b.Fatalf("open fixed corpus (run `go run ./pkg/filegrep/testdata/gen` first if missing): %v", err)
	}
	b.Cleanup(func() { r.Close() })
	return oneRoot(r.FS())
}

// BenchmarkFileGrep_SensitiveLiteral measures the MV-13 lever-1 fast path
// (bytes.Index, regex engine bypassed) over the fixed corpus.
func BenchmarkFileGrep_SensitiveLiteral(b *testing.B) {
	roots := corpusFS(b)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Search(ctx, roots, Options{Query: "needle", Case: CaseSensitive}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFileGrep_InsensitiveLiteral measures the ASCII-fold scanner
// lever over the fixed corpus.
func BenchmarkFileGrep_InsensitiveLiteral(b *testing.B) {
	roots := corpusFS(b)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Search(ctx, roots, Options{Query: "NEEDLE", Case: CaseInsensitive}); err != nil {
			b.Fatal(err)
		}
	}
}

// benchRegexOpts and benchPrefilterOpts pin Case: CaseSensitive explicitly.
// Without it, Options.Case defaults to CaseSmart, and since both patterns
// below are all-lowercase, smart-case would silently select
// CaseInsensitive — compile() then prepends "(?i)" to the pattern, which
// case-folds every literal node and defeats requiredLiteral's FoldCase
// safety guard (prefilter.go). That would make BenchmarkFileGrep_Prefilter
// silently measure plain regex, not the prefilter — caught during
// development by asserting against the actual compiled matcher.requiredLit
// rather than requiredLiteral() on the raw pattern string (which doesn't
// see the (?i) Options.Case would add).
var (
	benchRegexOpts     = Options{Query: "[nN][eE][eE][dD][lL][eE]", Regex: true, Case: CaseSensitive}
	benchPrefilterOpts = Options{Query: ".*needle.*", Regex: true, Case: CaseSensitive}
)

// BenchmarkFileGrep_Regex measures plain RE2 evaluation with NO
// required-literal prefilter available over the fixed corpus, as the
// baseline SC-008 compares the fast paths against. The pattern matches
// exactly the same content as "needle" (per-character classes fold to a
// single case-folded literal in the parsed AST — see prefilter.go's
// FoldCase guard — which is exactly what defeats the prefilter here: a
// case-folded literal is unsafe for a case-sensitive bytes.Contains check).
func BenchmarkFileGrep_Regex(b *testing.B) {
	roots := corpusFS(b)
	ctx := context.Background()
	m, err := compile(benchRegexOpts)
	if err != nil {
		b.Fatal(err)
	}
	if m.requiredLit != nil {
		b.Fatal("test setup: this pattern must compile with no required literal, or the benchmark isn't measuring plain-regex cost")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Search(ctx, roots, benchRegexOpts); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFileGrep_Prefilter measures a regex pattern WITH a provable
// required literal ("needle" itself, wrapped so it must still go through
// RE2 for the actual match) — SC-008 compares this against
// BenchmarkFileGrep_Regex (same corpus, same effective hit set) to quantify
// the required-literal prefilter's speedup.
func BenchmarkFileGrep_Prefilter(b *testing.B) {
	roots := corpusFS(b)
	ctx := context.Background()
	m, err := compile(benchPrefilterOpts)
	if err != nil {
		b.Fatal(err)
	}
	if m.requiredLit == nil {
		b.Fatal("test setup: this pattern must compile with a required literal for the prefilter to engage")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Search(ctx, roots, benchPrefilterOpts); err != nil {
			b.Fatal(err)
		}
	}
}

// --- Matcher-level micro-benchmarks -----------------------------------
//
// The four BenchmarkFileGrep_* above measure the whole engine (walk +
// gitignore probing + worker-pool dispatch + I/O + matching) over the
// spec's own fixed 200-file/~2KiB corpus size — at that scale, per-call
// walk/I/O overhead dominates the total, so the SC-008 ratio between
// matching STRATEGIES is real but small relative to ns/op. These
// micro-benchmarks isolate matcher.lineMatch itself (the actual lever)
// against a realistic, mostly-non-matching line — the common case during a
// real scan — to give SC-008 a clean, corpus-size-independent signal for
// the algorithmic differential alone. Both sets of numbers are recorded;
// see the commit message.
var benchLine = []byte(strings.Repeat("the quick brown fox jumps over the lazy dog and rests ", 3))

func BenchmarkMatcherLineMatch_SensitiveLiteral(b *testing.B) {
	m, err := compile(Options{Query: "needle", Case: CaseSensitive})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.lineMatch(benchLine)
	}
}

func BenchmarkMatcherLineMatch_InsensitiveLiteral(b *testing.B) {
	m, err := compile(Options{Query: "NEEDLE", Case: CaseInsensitive})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.lineMatch(benchLine)
	}
}

func BenchmarkMatcherLineMatch_Regex(b *testing.B) {
	m, err := compile(benchRegexOpts)
	if err != nil {
		b.Fatal(err)
	}
	if m.requiredLit != nil {
		b.Fatal("test setup: this pattern must have no required literal")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.lineMatch(benchLine)
	}
}

func BenchmarkMatcherLineMatch_Prefilter(b *testing.B) {
	m, err := compile(benchPrefilterOpts)
	if err != nil {
		b.Fatal(err)
	}
	if m.requiredLit == nil {
		b.Fatal("test setup: this pattern must have a required literal")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.lineMatch(benchLine)
	}
}
