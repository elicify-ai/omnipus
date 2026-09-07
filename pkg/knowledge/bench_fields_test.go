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
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
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

// ---------------------------------------------------------------------------
// A-13 — ADR-068's stated open risk, measured
// ---------------------------------------------------------------------------
//
// A-13, in the ADR's own words: "Whether bleve returns a stored field cheaply
// on this hit path, and whether Store: true on a 64-byte field at 100,000
// documents is acceptable against D20's W0 rebuild, are open. This ADR has been
// wrong about storage four times by assuming; a stated gap is worth more than a
// fifth guess."
//
// So there are two numbers, and they are different questions:
//
//	A13a  the QUERY cost — does adding source_hash to req.Fields make a search
//	      measurably slower? Measured at the maximum page size D15.5b allows
//	      (200), because the cost is per returned hit and 200 is the worst case.
//	A13b  the DISK cost — what does Store: true on a 64-byte field add to the
//	      index? Measured by building the same documents twice under two
//	      mappings that differ ONLY in that flag, which is the only way to
//	      attribute the difference to the flag rather than to the corpus.
//
// Neither asserts a budget, because neither has one. They report.

// a13PageSize is D15.5b's maximum page. The stored-field fetch is per RETURNED
// HIT, so the largest page is where the cost is largest.
const a13PageSize = 200

// a13Search runs one search at the page size, optionally retrieving the stored
// freshness field, and returns how many hits came back.
func a13Search(tb testing.TB, ix *Index, query string, withHash bool) int {
	tb.Helper()
	q := bleveQuery.NewMatchQuery(query)
	q.SetField(fieldBody)
	req := bleve.NewSearchRequestOptions(q, a13PageSize, 0, false)
	if withHash {
		req.Fields = []string{fieldPath, fieldKind, fieldOffset, fieldSourceHash}
	} else {
		req.Fields = []string{fieldPath, fieldKind, fieldOffset}
	}
	req.SortBy([]string{"-_score", "_id"})
	res, err := ix.idx.Search(req)
	if err != nil {
		tb.Fatalf("search: %v", err)
	}
	return len(res.Hits)
}

// BenchmarkA13StoredFieldQueryCost is A13a: the per-hit cost of the freshness
// stored field on the query path, with and without, over the same warm index.
func BenchmarkA13StoredFieldQueryCost(b *testing.B) {
	n := fieldBenchNotes(b)
	root := buildFieldBenchCollection(b, n)
	home := b.TempDir()
	ix, err := OpenIndex(home, root)
	if err != nil {
		b.Fatalf("OpenIndex: %v", err)
	}
	defer func() { _ = ix.Close() }()
	if _, err := ix.Sync(context.Background()); err != nil {
		b.Fatalf("Sync: %v", err)
	}

	// A term every note carries, so every run fills the page. A selective term
	// would measure an empty page and report a cost that is not the one A-13
	// asks about.
	const query = "reviewer"
	if got := a13Search(b, ix, query, true); got != a13PageSize {
		b.Fatalf("the probe query returned %d hits, want a full page of %d — "+
			"a partial page measures the wrong thing", got, a13PageSize)
	}

	b.Run("without_source_hash", func(b *testing.B) {
		for b.Loop() {
			a13Search(b, ix, query, false)
		}
		b.ReportMetric(float64(a13PageSize), "hits/op")
	})
	b.Run("with_source_hash", func(b *testing.B) {
		for b.Loop() {
			a13Search(b, ix, query, true)
		}
		b.ReportMetric(float64(a13PageSize), "hits/op")
	})
}

// a13MappingWithStoredHash returns the shipping mapping, optionally with the
// source_hash field's Store flag turned OFF.
//
// It mutates the real buildIndexMapping rather than restating it, because the
// question is what the SHIPPING mapping costs; a restated copy would measure a
// mapping nobody runs.
func a13MappingWithStoredHash(store bool) *bleveMapping.IndexMappingImpl {
	m := buildIndexMapping()
	fm := bleve.NewTextFieldMapping()
	fm.Analyzer = "keyword"
	fm.Store = store
	fm.Index = false
	fm.IncludeTermVectors = false
	fm.IncludeInAll = false
	fm.DocValues = false
	m.DefaultMapping.Properties[fieldSourceHash] = bleveMapping.NewDocumentMapping()
	m.DefaultMapping.AddFieldMappingsAt(fieldSourceHash, fm)
	return m
}

// BenchmarkA13StoredFieldDiskCost is A13b. It builds the SAME documents twice,
// under two mappings differing only in source_hash's Store flag, and reports
// both sizes and the delta per document.
func BenchmarkA13StoredFieldDiskCost(b *testing.B) {
	n := fieldBenchNotes(b)
	root := buildFieldBenchCollection(b, n)

	// Harvest the real documents once, through the real indexing path, so what
	// is measured is what the indexer actually writes.
	home := b.TempDir()
	src, err := OpenIndex(home, root)
	if err != nil {
		b.Fatalf("OpenIndex: %v", err)
	}
	if _, err := src.Sync(context.Background()); err != nil {
		b.Fatalf("Sync: %v", err)
	}
	docs := a13HarvestDocs(b, root)
	if err := src.Close(); err != nil {
		b.Fatalf("Close: %v", err)
	}

	for b.Loop() {
		stored := a13BuildSized(b, docs, true)
		bare := a13BuildSized(b, docs, false)
		b.ReportMetric(float64(len(docs)), "docs")
		b.ReportMetric(float64(stored)/(1<<20), "stored_MiB")
		b.ReportMetric(float64(bare)/(1<<20), "unstored_MiB")
		b.ReportMetric(float64(stored-bare)/float64(len(docs)), "delta_B/doc")
	}
}

// a13HarvestDocs re-reads the collection through the real per-note field
// extraction and returns the documents an index would hold.
func a13HarvestDocs(tb testing.TB, root string) []indexDoc {
	tb.Helper()
	scan, err := Scan(root)
	if err != nil {
		tb.Fatalf("Scan: %v", err)
	}
	out := make([]indexDoc, 0, len(scan.Entries))
	for _, e := range scan.Entries {
		if e.Kind != ScanKindNote {
			continue
		}
		data, rErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.RelPath))) //nolint:gosec // test fixture
		if rErr != nil {
			tb.Fatalf("read %s: %v", e.RelPath, rErr)
		}
		hash, _, hErr := hashReader(bytes.NewReader(data))
		if hErr != nil {
			tb.Fatalf("hash %s: %v", e.RelPath, hErr)
		}
		nf, bodyStart := extractNoteFields(data, e.RelPath)
		out = append(out, indexDoc{
			Path:       e.RelPath,
			Name:       nameTokensFor(e.RelPath),
			Kind:       string(ScanKindNote),
			Title:      nf.Title,
			Headings:   newHeadingCollector().feed(data, 0),
			PropKeys:   nf.PropKeys,
			PropValues: nf.PropValues,
			Props:      nf.Props,
			SourceHash: hash,
			Body:       string(data[bodyStart:]),
		})
	}
	return out
}

// a13BuildSized indexes docs under one of the two mappings and returns the
// resulting index's size on disk.
func a13BuildSized(tb testing.TB, docs []indexDoc, storeHash bool) int64 {
	tb.Helper()
	dir := filepath.Join(tb.TempDir(), "bleve")
	idx, err := bleve.NewUsing(dir, a13MappingWithStoredHash(storeHash),
		scorch.Name, scorch.Name, bleveOpenConfig())
	if err != nil {
		tb.Fatalf("NewUsing: %v", err)
	}
	batch := idx.NewBatch()
	for i, d := range docs {
		if err := batch.Index(segmentDocID(d.Path, 0), d); err != nil {
			tb.Fatalf("batch index: %v", err)
		}
		if (i+1)%indexBatchMaxDocs == 0 {
			if err := idx.Batch(batch); err != nil {
				tb.Fatalf("commit: %v", err)
			}
			batch = idx.NewBatch()
		}
	}
	if err := idx.Batch(batch); err != nil {
		tb.Fatalf("final commit: %v", err)
	}

	// FORCE A MERGE BEFORE SIZING, and this is not a refinement — without it
	// the measurement is worthless. scorch merges in the background, so an
	// index closed immediately after its last batch keeps a varying number of
	// un-merged .zap segments. The first version of this benchmark did exactly
	// that and reported 68.9, 725.6 and MINUS 139.1 bytes per document over
	// three runs of identical work: the unstored index came out LARGER than
	// the stored one, which is a measurement of merge timing wearing the
	// costume of a measurement of storage. Merging to one segment makes the
	// two builds comparable, because it makes them the same shape.
	adv, advErr := idx.Advanced()
	if advErr != nil {
		tb.Fatalf("Advanced: %v", advErr)
	}
	sc, ok := adv.(*scorch.Scorch)
	if !ok {
		tb.Fatalf("index is not scorch (%T); the size comparison would be merge-timing noise", adv)
	}
	if err := sc.ForceMerge(context.Background(), nil); err != nil {
		tb.Fatalf("ForceMerge: %v", err)
	}
	// ForceMerge returns when the merge is DONE, not when the superseded
	// segment files are GONE: scorch drops those in the cleanup that follows a
	// persist. A directory sized the instant ForceMerge returns still holds
	// both the merged segment and everything it replaced, which is the
	// residual half of the noise above — and why the wait below fails the run
	// rather than shrugging.
	// The superseded segments are removed by the cleanup that FOLLOWS a
	// persist, not by ForceMerge itself, so the wait is here and the assertion
	// is after the close.
	a13WaitForSingleSegment(tb, dir)
	if err := idx.Close(); err != nil {
		tb.Fatalf("Close: %v", err)
	}
	a13RequireSingleSegment(tb, dir)
	return a13SegmentBytes(tb, dir)
}

// a13RequireSingleSegment asserts that exactly one .zap segment remains, and
// FAILS rather than reporting a size taken over any other number.
//
// Reporting a size taken over an unknown number of superseded segments is how
// this benchmark produced a NEGATIVE per-document cost for a field that can
// only add bytes. A number that cannot be trusted must stop the run, not be
// printed with a caveat nobody reads.
// It is checked AFTER Close, not before, and that ordering was learned the hard
// way: polling the live directory found ZERO segments for a full minute.
// ForceMerge produces the merged segment in memory and deletes the ones it
// superseded, and the persister writes the new one out later — so between those
// two moments the directory legitimately holds no segment at all. Close is what
// makes the on-disk state final, and after Close there is nothing left running
// that could change the answer.
func a13WaitForSingleSegment(tb testing.TB, dir string) {
	tb.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		segs, err := filepath.Glob(filepath.Join(dir, "store", "*.zap"))
		if err != nil {
			tb.Fatalf("glob segments: %v", err)
		}
		if len(segs) == 1 {
			return
		}
		if time.Now().After(deadline) {
			tb.Fatalf("90s after a forced single-segment merge the index still holds %d segments", len(segs))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func a13RequireSingleSegment(tb testing.TB, dir string) {
	tb.Helper()
	segs, err := filepath.Glob(filepath.Join(dir, "store", "*.zap"))
	if err != nil {
		tb.Fatalf("glob segments: %v", err)
	}
	if len(segs) != 1 {
		tb.Fatalf("after a forced single-segment merge and a close, the index holds %d segments, want 1; "+
			"a size taken now would measure merge timing, not the stored field", len(segs))
	}
}

// a13SegmentBytes sizes the SEGMENT files only.
//
// They live under <dir>/store, not <dir> — a glob one level too high found
// zero segments and failed the run, which is exactly what a hard assertion is
// for: the earlier version of this benchmark would have reported a size of
// zero and a per-document delta of zero, and zero is a plausible-looking
// answer to "what does one more stored field cost".
//
// root.bolt is excluded deliberately: it is bbolt's page-allocated metadata
// store, its size moves in whole pages for reasons unrelated to any field, and
// the stored value being measured lives entirely in the zap segment.
func a13SegmentBytes(tb testing.TB, dir string) int64 {
	tb.Helper()
	segs, err := filepath.Glob(filepath.Join(dir, "store", "*.zap"))
	if err != nil {
		tb.Fatalf("glob segments: %v", err)
	}
	var total int64
	for _, s := range segs {
		info, sErr := os.Stat(s)
		if sErr != nil {
			tb.Fatalf("stat %s: %v", s, sErr)
		}
		total += info.Size()
	}
	return total
}
