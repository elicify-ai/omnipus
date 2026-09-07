// Omnipus — ADR-067 stage 2 performance measurements (spec §13.1 rows 37, 38,
// 39; §13.2 dataset DS-2 row 4).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run (the full specified dataset — 100,000 notes, slow and disk-hungry):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^$' -bench 'Benchmark(Search|InitialIndex|Reconcile)' -benchtime 1x ./pkg/knowledge/
//
// Run a fast smoke check that the harness itself works:
//
//	OMNIPUS_KB_BENCH_NOTES=2000 CGO_ENABLED=0 go test -tags goolm,stdjson -run '^$' -bench Benchmark -benchtime 1x ./pkg/knowledge/
//
// # Why these are benchmarks and not tests
//
// MV-1, MV-2 and MV-4 are measurements on a 100,000-note collection. Building
// that fixture takes minutes and gigabytes, so it must not run inside the
// ordinary suite — but the alternative the package shipped with was worse:
// nothing measured them at all, and the largest fixture anywhere in the suite
// was 748 notes, 0.7% of the specified dataset. A number nobody measures is a
// claim, not a property.
//
// # These benchmarks FAIL when the budget is exceeded
//
// A benchmark that only prints a number cannot tell anyone the budget was
// missed. Each one below calls b.Errorf when its stated MV threshold is
// breached, so a regression is a red run rather than a slightly larger number
// in a log nobody reads. They run only under -bench, so they cannot destabilise
// the ordinary suite.
//
// # The measurement is of THIS machine
//
// SC-001..SC-004 are stated without naming hardware. A pass here is evidence on
// the machine that ran it, not a universal claim; the CI runner is the one
// whose verdict counts for a release.
package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"
)

// benchDatasetNotes is DS-2 row 4's collection size. The environment variable
// exists so the harness can be smoke-tested in seconds; a run that does not
// print the full size is not evidence for MV-1..MV-4.
func benchDatasetNotes(b *testing.B) int {
	b.Helper()
	raw := os.Getenv("OMNIPUS_KB_BENCH_NOTES")
	if raw == "" {
		return 100_000
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		b.Fatalf("OMNIPUS_KB_BENCH_NOTES=%q is not a positive integer", raw)
	}
	return n
}

// MV budgets, restated from the spec's success criteria so a change to one is a
// visible change to the other.
const (
	mv1SearchP95Budget      = 500 * time.Millisecond // SC-001
	mv2InitialIndexPeakRSS  = 512 << 20              // SC-002, bytes
	mv4ReconcileUnchangedMs = 2 * time.Second        // SC-004
)

// benchVocabulary is the query set. Fixed, so two runs measure the same work.
var benchVocabulary = []string{
	"alpha", "bravo", "charlie", "delta", "echo",
	"foxtrot", "golf", "hotel", "india", "juliet",
}

// buildBenchCollection writes n notes into a fresh directory and returns it.
//
// Each note carries three vocabulary words and one rare term, so the query set
// exercises both a very common term (hundreds of thousands of segment hits, the
// expensive case MV-1 is about) and a selective one.
func buildBenchCollection(b *testing.B, n int) string {
	b.Helper()
	root := b.TempDir()
	for i := range n {
		rel := fmt.Sprintf("notes/%03d/note-%06d.md", i%512, i)
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			b.Fatal(err)
		}
		body := fmt.Sprintf("# Note %d\n\n%s %s %s and a rare marker term%06d.\n\nSee [[note-%06d]].\n",
			i,
			benchVocabulary[i%len(benchVocabulary)],
			benchVocabulary[(i*3)%len(benchVocabulary)],
			benchVocabulary[(i*7)%len(benchVocabulary)],
			i, (i+1)%n)
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	return root
}

// BenchmarkSearchP95 is spec test 37 (Bench_Search_p95_100k), MV-1 / SC-001:
// the 95th-percentile search response on a 100,000-note collection is under
// 500 ms.
//
// The oracle is a PERCENTILE over many queries, not a mean and not one timing:
// MV-1 is stated as a p95, and a mean hides exactly the tail the requirement is
// about. The index build is excluded from the timer — this measures query
// latency against a warm index, which is what an operator experiences.
func BenchmarkSearchP95(b *testing.B) {
	notes := benchDatasetNotes(b)
	home := b.TempDir()
	root := buildBenchCollection(b, notes)

	ix, err := OpenIndex(home, root)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = ix.Close() }()
	stats, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		b.Fatal(err)
	}
	if stats.Indexed != notes {
		b.Fatalf("indexed %d of %d notes — the fixture is not what was measured", stats.Indexed, notes)
	}

	tracker := NewProgressTracker()
	searcher, err := NewSearcher(ix, tracker)
	if err != nil {
		b.Fatal(err)
	}

	const samples = 200
	latencies := make([]time.Duration, 0, samples)

	b.ResetTimer()
	for range b.N {
		latencies = latencies[:0]
		for i := range samples {
			var query string
			if i%2 == 0 {
				query = benchVocabulary[i%len(benchVocabulary)]
			} else {
				query = fmt.Sprintf("term%06d", (i*4099)%notes)
			}
			start := time.Now()
			resp, sErr := searcher.Search(query, SearchOptions{})
			elapsed := time.Since(start)
			if sErr != nil {
				b.Fatal(sErr)
			}
			// A query that matches nothing is not a measurement of search.
			if i%2 == 0 && resp.Len() == 0 {
				b.Fatalf("query %q returned no results on a %d-note collection — "+
					"a benchmark over an empty answer measures nothing", query, notes)
			}
			latencies = append(latencies, elapsed)
		}
	}
	b.StopTimer()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p95 := latencies[(len(latencies)*95)/100]
	b.ReportMetric(float64(p95.Nanoseconds())/1e6, "p95_ms")
	b.ReportMetric(float64(notes), "notes")
	if p95 > mv1SearchP95Budget {
		b.Errorf("MV-1/SC-001: p95 search latency was %v over %d notes, budget %v",
			p95, notes, mv1SearchP95Budget)
	}
}

// BenchmarkInitialIndexPeakRSS is spec test 38 (Bench_InitialIndex_PeakRSS_100k),
// MV-2 / SC-002: peak memory during a first index stays under 512 MB.
//
// The oracle is the process's high-water RESIDENT set, normalised across
// platforms (Linux reports kilobytes, macOS bytes). Go's heap statistics are
// deliberately not used: they measure the wrong thing — a mapped scorch segment
// is resident and is not on Go's heap, and FR-034's whole claim is about what
// the process holds, not what the allocator tracked.
func BenchmarkInitialIndexPeakRSS(b *testing.B) {
	notes := benchDatasetNotes(b)

	before, ok := peakRSSBytes()
	if !ok {
		b.Skipf("peak resident memory is not observable on this platform; MV-2 must be measured elsewhere")
	}

	for range b.N {
		home := b.TempDir()
		root := buildBenchCollection(b, notes)
		ix, err := OpenIndex(home, root)
		if err != nil {
			b.Fatal(err)
		}
		stats, err := ix.SyncWith(context.Background(), SyncOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if stats.Indexed != notes {
			b.Fatalf("indexed %d of %d notes", stats.Indexed, notes)
		}
		if stats.BatchCommits <= 1 {
			b.Fatalf("FR-034: %d documents were committed in %d batch(es) — a single "+
				"whole-collection batch makes this measurement meaningless",
				stats.Indexed, stats.BatchCommits)
		}
		if err = ix.Close(); err != nil {
			b.Fatal(err)
		}
	}

	after, ok := peakRSSBytes()
	if !ok {
		b.Fatal("peak resident memory became unobservable mid-run")
	}
	b.ReportMetric(float64(after)/(1<<20), "peak_rss_MB")
	b.ReportMetric(float64(after-before)/(1<<20), "rss_delta_MB")
	b.ReportMetric(float64(notes), "notes")
	if after > mv2InitialIndexPeakRSS {
		b.Errorf("MV-2/SC-002: peak resident memory reached %d MB indexing %d notes, budget %d MB",
			after>>20, notes, mv2InitialIndexPeakRSS>>20)
	}
}

// BenchmarkReconcileUnchanged is spec test 39 (Bench_Reconcile_Unchanged_100k),
// MV-4 / SC-004: the freshness check over an unchanged 100,000-file collection
// completes in under 2 seconds.
//
// The load-bearing assertion is not the clock — it is that the run REPARSED
// NOTHING. A reconcile that re-indexed the whole collection in under two
// seconds would satisfy a timing-only oracle while proving the opposite of
// FR-033.
func BenchmarkReconcileUnchanged(b *testing.B) {
	notes := benchDatasetNotes(b)
	home := b.TempDir()
	root := buildBenchCollection(b, notes)

	ix, err := OpenIndex(home, root)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = ix.Close() }()
	first, err := ix.SyncWith(context.Background(), SyncOptions{})
	if err != nil {
		b.Fatal(err)
	}
	if first.Indexed != notes {
		b.Fatalf("first index covered %d of %d notes", first.Indexed, notes)
	}

	var slowest time.Duration
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		again, sErr := ix.SyncWith(context.Background(), SyncOptions{})
		elapsed := time.Since(start)
		if sErr != nil {
			b.Fatal(sErr)
		}
		if again.Indexed != 0 {
			b.Fatalf("FR-033: a reconcile of an unchanged collection re-parsed %d files; "+
				"only size, mtime or content-hash changes may trigger a re-parse", again.Indexed)
		}
		if again.Scanned != notes {
			b.Fatalf("the reconcile scanned %d of %d files — it did not look at the whole collection",
				again.Scanned, notes)
		}
		if elapsed > slowest {
			slowest = elapsed
		}
	}
	b.StopTimer()

	b.ReportMetric(float64(slowest.Nanoseconds())/1e6, "slowest_ms")
	b.ReportMetric(float64(notes), "files")
	if slowest > mv4ReconcileUnchangedMs {
		b.Errorf("MV-4/SC-004: the slowest unchanged freshness check over %d files took %v, budget %v",
			notes, slowest, mv4ReconcileUnchangedMs)
	}
}
