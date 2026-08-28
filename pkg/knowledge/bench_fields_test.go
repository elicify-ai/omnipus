// Omnipus — ADR-068 D21.2 / D16.5: what fielded indexing actually costs.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// D21.2 adds five fields to every index document and D16.5 adds a stored
// 64-byte hash to every one of them. Both cost disk and both cost time, and
// the ADR's own history is four storage decisions made by assuming. So the
// numbers are MEASURED here, on a corpus that looks like a vault rather than
// like the existing benchmark's frontmatter-free notes, and they are measured
// the same way before and after the change so the two runs are comparable.
//
// Run:
//
//	CGO_ENABLED=1 go test -tags goolm,stdjson -run '^$' -bench 'BenchmarkFielded' \
//	    -benchtime 1x ./pkg/knowledge/
//
// Corpus size defaults to 5,000 notes (ADR-068's stated design target for a
// working vault); OMNIPUS_KB_FIELD_BENCH_NOTES overrides it.
package knowledge

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fieldBenchNotes is the corpus size for the cost benchmarks.
func fieldBenchNotes(b *testing.B) int {
	b.Helper()
	raw := os.Getenv("OMNIPUS_KB_FIELD_BENCH_NOTES")
	if raw == "" {
		return 5_000
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		b.Fatalf("OMNIPUS_KB_FIELD_BENCH_NOTES=%q is not a positive integer", raw)
	}
	return n
}

// fieldBenchProperties is the frontmatter every note in the corpus carries.
// Six properties is what a working vault note actually has — a type, a status,
// a date, an owner, a couple of tags — and it is the shape D21.2 exists to make
// queryable. The existing BenchmarkSearchP95 corpus has NO frontmatter at all,
// which is why it cannot measure this.
var fieldBenchStatuses = []string{"prospect", "active", "churned", "dormant", "won"}

// buildFieldBenchCollection writes n notes carrying frontmatter, headings and
// prose into a fresh directory and returns it.
func buildFieldBenchCollection(tb testing.TB, n int) string {
	tb.Helper()
	root := tb.TempDir()
	for i := range n {
		rel := fmt.Sprintf("notes/%03d/note-%06d.md", i%512, i)
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			tb.Fatal(err)
		}
		body := fmt.Sprintf(`---
title: Account %d
type: account
status: %s
owner: analyst-%02d
opened: 2026-0%d-1%d
tags:
  - %s
  - quarterly
---

# Account %d

%s %s %s and a rare marker term%06d.

## History

The account was reviewed in the %s quarter and the notes below record what the
reviewer found. See [[note-%06d]] for the neighbouring account.

## Next steps

Follow up with analyst-%02d before the next review window closes.
`,
			i,
			fieldBenchStatuses[i%len(fieldBenchStatuses)],
			i%64,
			(i%9)+1, i%10,
			benchVocabulary[i%len(benchVocabulary)],
			i,
			benchVocabulary[i%len(benchVocabulary)],
			benchVocabulary[(i*3)%len(benchVocabulary)],
			benchVocabulary[(i*7)%len(benchVocabulary)],
			i,
			benchVocabulary[(i*5)%len(benchVocabulary)],
			(i+1)%n,
			i%64,
		)
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			tb.Fatal(err)
		}
	}
	return root
}

// dirBytes is the on-disk size of everything under dir.
func dirBytes(tb testing.TB, dir string) int64 {
	tb.Helper()
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, iErr := d.Info()
		if iErr != nil {
			return iErr
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		tb.Fatalf("sizing %s: %v", dir, err)
	}
	return total
}

// BenchmarkFieldedIndexCost reports the two numbers D21.2 must be judged on:
// how long a full initial index of a realistic corpus takes, and how many bytes
// the index occupies afterwards.
//
// It is deliberately NOT a budget assertion. There is no stated budget for
// either number, and inventing one here would turn a measurement into a
// threshold nobody agreed. It reports; a human compares two runs.
func BenchmarkFieldedIndexCost(b *testing.B) {
	n := fieldBenchNotes(b)
	root := buildFieldBenchCollection(b, n)
	corpusBytes := dirBytes(b, root)

	for b.Loop() {
		home := b.TempDir()
		ix, err := OpenIndex(home, root)
		if err != nil {
			b.Fatalf("OpenIndex: %v", err)
		}
		start := time.Now()
		stats, err := ix.Sync(context.Background())
		elapsed := time.Since(start)
		if err != nil {
			_ = ix.Close()
			b.Fatalf("Sync: %v", err)
		}
		if stats.Indexed != n {
			_ = ix.Close()
			b.Fatalf("indexed %d notes, want %d", stats.Indexed, n)
		}
		indexBytes := dirBytes(b, ix.Dir())
		if err := ix.Close(); err != nil {
			b.Fatalf("Close: %v", err)
		}

		b.ReportMetric(float64(n), "notes")
		b.ReportMetric(elapsed.Seconds(), "sync_s")
		b.ReportMetric(float64(indexBytes)/(1<<20), "index_MiB")
		b.ReportMetric(float64(corpusBytes)/(1<<20), "corpus_MiB")
		b.ReportMetric(float64(indexBytes)/float64(corpusBytes), "index/corpus")
	}
}
