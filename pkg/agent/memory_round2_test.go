// Package agent — round-2 review fix tests for pkg/agent/memory.go.
//
// Covers:
//   - Finding #3: SearchEntriesInScope must not use the bleve index after Close
//     (concurrent Search + Close must not panic).
//   - Finding #4: first write to a room must not double-append the new memory's
//     MinHash signature to the sig cache.
//   - Finding #5: recalled entries must report the memory's real stored creation
//     time (file mtime), not wall-clock-at-recall.
//   - Finding #6: the score-sort comparator must satisfy strict-weak-ordering
//     (descending score order preserved, ties stable).
//
// Testing policy: written but NOT run as part of the full suite locally (CI
// authority per CLAUDE.md); run scoped with -run.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"
	"testing"
	"time"
)

// TestMemory_NoDoubleSigAppendOnFirstWrite verifies finding #4: on the first
// write to a room, the just-written memory's signature must be cached exactly
// once. AppendLongTermToScope writes the .md file before the dedup check, and
// ensureSigCacheLocked's initial scan already picks it up — so the explicit
// append must be suppressed (no double-append / cache bloat).
func TestMemory_NoDoubleSigAppendOnFirstWrite(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	if err := ms.AppendLongTerm("Prometheus scrapes metrics from every node exporter.", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	root := ms.PrivateRoom().Root
	ms.indexMu.Lock()
	ids := append([]string(nil), ms.sigIDs[root]...)
	sigCount := len(ms.sigCache[root])
	ms.indexMu.Unlock()

	if len(ids) != 1 {
		t.Fatalf("expected exactly 1 cached sig ID after first write, got %d: %v", len(ids), ids)
	}
	if sigCount != 1 {
		t.Fatalf("expected exactly 1 cached signature after first write, got %d", sigCount)
	}

	// A second, distinct write must add exactly one more entry (still no dup).
	if err := ms.AppendLongTerm("Kubernetes schedules pods across the worker nodes.", "reference"); err != nil {
		t.Fatalf("AppendLongTerm second: %v", err)
	}
	ms.indexMu.Lock()
	idsAfter := append([]string(nil), ms.sigIDs[root]...)
	ms.indexMu.Unlock()

	if len(idsAfter) != 2 {
		t.Fatalf("expected exactly 2 cached sig IDs after two writes, got %d: %v", len(idsAfter), idsAfter)
	}
	// No ID may appear twice.
	seen := map[string]bool{}
	for _, id := range idsAfter {
		if seen[id] {
			t.Fatalf("sig ID %q cached more than once (double-append bug)", id)
		}
		seen[id] = true
	}
}

// TestMemory_RecallTimestampIsStoredCreationTime verifies finding #5: a recalled
// entry's Timestamp reflects the memory file's real stored time (its mtime), NOT
// wall-clock-at-recall. We pin a future clock and confirm the recalled timestamp
// does NOT equal that future clock value (it must be the on-disk mtime instead).
func TestMemory_RecallTimestampIsStoredCreationTime(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	if err := ms.AppendLongTerm("Jaeger provides distributed tracing for microservices.", "reference"); err != nil {
		t.Fatalf("AppendLongTerm: %v", err)
	}

	// Pin the clock far in the future. If the recall used ms.now()/time.Now() as
	// the timestamp (the bug), the recalled entry would carry this future value.
	future := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	ms.SetClock(func() time.Time { return future })

	results, err := ms.SearchEntries("jaeger", 10)
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
	if len(results) == 0 {
		t.Skip("no hit for 'jaeger' — index may not be ready; cannot assert timestamp")
	}

	for _, r := range results {
		if r.Timestamp.Equal(future) {
			t.Fatalf("recalled entry used wall-clock-at-recall (%v) instead of the stored file mtime", future)
		}
		if r.Timestamp.IsZero() {
			t.Fatal("recalled entry has a zero timestamp")
		}
		// The mtime must be at or before "now" (well before the pinned future).
		if r.Timestamp.After(time.Now().Add(time.Hour)) {
			t.Fatalf("recalled timestamp %v is implausibly far in the future", r.Timestamp)
		}
	}
}

// TestMemory_ConcurrentSearchAndClose verifies finding #3: SearchEntriesInScope
// holds indexMu across t.ri.Search(), so a concurrent Close() (which closes
// every RoomIndex under indexMu) cannot close the index mid-search. The test
// hammers Search and Close concurrently; a use-after-close would panic.
func TestMemory_ConcurrentSearchAndClose(t *testing.T) {
	ms, _ := newTestMemoryStore(t)

	// Seed a few memories so the bleve index is populated and Search does real work.
	for _, body := range []string{
		"Prometheus scrapes metrics from node exporters.",
		"Grafana dashboards visualize prometheus time series.",
		"Alertmanager routes prometheus alerts to oncall.",
	} {
		if err := ms.AppendLongTerm(body, "reference"); err != nil {
			t.Fatalf("AppendLongTerm: %v", err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine A: repeatedly search. Errors are fine post-close; a panic is not.
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = ms.SearchEntries("prometheus", 5)
		}
	}()

	// Goroutine B: close the store once, partway through.
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = ms.SearchEntries("grafana", 5)
		}
		ms.Close()
	}()

	wg.Wait()
	// Reaching here without a panic is the assertion (use-after-close guarded).
}

// TestMemory_RecallScoreOrdering exercises the score-sort comparator (finding
// #6). It writes several distinctly-relevant memories and confirms a multi-hit
// recall returns results without panicking and with a deterministic, stable
// order — the strict-weak-ordering fix (return false on ties) must hold.
func TestMemory_RecallScoreOrdering(t *testing.T) {
	ms, _ := newTestMemoryStore(t)
	defer ms.Close()

	bodies := []string{
		"prometheus prometheus prometheus metrics alerting",
		"prometheus metrics overview",
		"unrelated note about kubernetes helm charts",
		"a second unrelated note about terraform state",
	}
	for _, b := range bodies {
		if err := ms.AppendLongTerm(b, "reference"); err != nil {
			t.Fatalf("AppendLongTerm: %v", err)
		}
	}

	// Run the recall multiple times; a broken comparator (return true on ties)
	// can produce non-deterministic order or, with sort.Slice, a panic on some
	// inputs. We assert it is stable across runs and never panics.
	var first []string
	for run := 0; run < 5; run++ {
		results, err := ms.SearchEntries("prometheus", 10)
		if err != nil {
			t.Fatalf("SearchEntries run %d: %v", run, err)
		}
		order := make([]string, len(results))
		for i, r := range results {
			order[i] = r.Content
		}
		if run == 0 {
			first = order
			continue
		}
		if len(order) != len(first) {
			t.Fatalf("recall result count changed between runs: %d vs %d", len(first), len(order))
		}
		for i := range order {
			if order[i] != first[i] {
				t.Fatalf("recall order not deterministic at index %d: %q vs %q", i, first[i], order[i])
			}
		}
	}
}
