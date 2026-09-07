// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Command gen writes the FIXED, committed benchmark corpus for
// BenchmarkFileGrep_* (spec test 36, SC-008): 200 files of ~2 KiB each
// spread across 10 subdirectories, under
// pkg/filegrep/testdata/corpus/. Generation is entirely deterministic —
// arithmetic word-cycling, no math/rand, no time/date input — so re-running
// this program always reproduces byte-identical output. It lives under
// testdata/ (a directory the Go toolchain ignores for `go build ./...` /
// `go vet ./...` / package discovery) so it never affects the filegrep
// package's own build; it is invoked explicitly:
//
//	go run ./pkg/filegrep/testdata/gen > /dev/null && git status
//
// The corpus itself (its output) is committed alongside this generator so
// benchmarks read fixed, inspectable files rather than regenerating at
// benchmark time.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// words is the fixed vocabulary content is built from. "needle" appears
// here like any other word — which files actually CONTAIN it is controlled
// separately below, so benchmarks can target a known hit rate.
var words = []string{
	"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog",
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur", "adipiscing",
	"elit", "sed", "do", "eiusmod", "tempor", "incididunt", "ut", "labore",
	"et", "dolore", "magna", "aliqua", "haystack", "search", "engine",
	"regex", "pattern", "match", "token", "scanner", "walker", "budget",
	"deadline", "excerpt", "gitignore", "workspace", "mount", "confined",
}

const (
	numFiles     = 200
	numDirs      = 10
	targetBytes  = 2048
	linesPerFile = 40
)

func main() {
	root := "pkg/filegrep/testdata/corpus"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, "removeall:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdirall:", err)
		os.Exit(1)
	}

	for i := 0; i < numFiles; i++ {
		dir := filepath.Join(root, fmt.Sprintf("dir%02d", i%numDirs))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "mkdirall:", err)
			os.Exit(1)
		}
		path := filepath.Join(dir, fmt.Sprintf("file%03d.txt", i))
		content := genFile(i)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "writefile:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("wrote %d files under %s\n", numFiles, root)
}

// genFile deterministically builds one file's content from its index alone:
// every 5th file (i%5==0) contains the literal "needle" on one line, near
// the end, so BenchmarkFileGrep_* patterns have a known, fixed hit rate
// (40 of 200 files) without depending on any random source.
func genFile(i int) string {
	var b strings.Builder
	for line := 0; line < linesPerFile; line++ {
		if i%5 == 0 && line == linesPerFile-3 {
			b.WriteString("this line intentionally contains the needle for benchmark hits\n")
			continue
		}
		writeWordLine(&b, i, line)
	}
	// Pad/trim to close to targetBytes by repeating filler lines — keeps
	// every file a comparable size for stable per-file benchmark timing.
	for b.Len() < targetBytes {
		writeWordLine(&b, i, b.Len())
	}
	return b.String()
}

func writeWordLine(b *strings.Builder, i, salt int) {
	const wordsPerLine = 8
	for w := 0; w < wordsPerLine; w++ {
		idx := (i*31 + salt*17 + w*7) % len(words)
		if w > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(words[idx])
	}
	b.WriteByte('\n')
}
