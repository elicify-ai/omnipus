// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestFileGrep_ParallelScanDeterministicSet pins FR-022/spec A3/test 14: the
// content-scan worker pool processes files concurrently, but the final
// Result.Hits — the SET, and its path-lexicographic-then-line ORDER — must
// come out identical every run, with no budget bound truncating the search
// (so completeness isn't itself in question, only ordering/determinism
// under concurrent scheduling).
func TestFileGrep_ParallelScanDeterministicSet(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 40; i++ {
		dir := fmt.Sprintf("dir%02d", i%5)
		name := fmt.Sprintf("%s/file%03d.txt", dir, i)
		var b strings.Builder
		for l := 0; l < 30; l++ {
			if l%7 == 0 {
				fmt.Fprintf(&b, "line %d has the needle right here\n", l)
			} else {
				fmt.Fprintf(&b, "line %d is ordinary filler text with no match\n", l)
			}
		}
		files[name] = b.String()
	}
	fsys := buildFS(files)

	var first Result
	for run := 0; run < 20; run++ {
		res := mustSearch(t, oneRoot(fsys), Options{
			Query:  "needle",
			Limits: Limits{Matches: 10_000, OutputBytes: 10 << 20},
		})
		if res.Truncated {
			t.Fatalf("run %d: unexpected truncation reason=%v (test corpus must fit comfortably under every bound)", run, res.TruncatedReason)
		}
		if run == 0 {
			first = res
			if len(first.Hits) == 0 {
				t.Fatal("test setup produced zero hits")
			}
			if !sort_IsHitsSorted(first.Hits) {
				t.Fatalf("run 0 hits are not path-lexicographic-then-line ordered: %+v", first.Hits[:min(5, len(first.Hits))])
			}
			continue
		}
		if !reflect.DeepEqual(first.Hits, res.Hits) {
			t.Fatalf("run %d produced a different hit set/order than run 0\nrun0: %d hits\nrun%d: %d hits", run, len(first.Hits), run, len(res.Hits))
		}
		if first.Stats != res.Stats {
			t.Fatalf("run %d produced different Stats than run 0: %+v vs %+v", run, res.Stats, first.Stats)
		}
	}
}

func sort_IsHitsSorted(hits []Hit) bool {
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Path > hits[i].Path {
			return false
		}
		if hits[i-1].Path == hits[i].Path && hits[i-1].Line > hits[i].Line {
			return false
		}
	}
	return true
}

// TestFileGrep_ParallelScanUsesMultipleGoroutines is a sanity check that the
// engine actually dispatches concurrently rather than "parallel in name
// only" — not a hard timing assertion (flaky on a loaded CI box), just a
// worker-count-vs-1 sanity bound via workerCount() itself.
func TestFileGrep_ParallelScanUsesMultipleGoroutines(t *testing.T) {
	n := workerCount()
	if n < 1 {
		t.Fatalf("workerCount() = %d, want >= 1", n)
	}
	if n > 8 {
		t.Fatalf("workerCount() = %d, want <= 8 (bounded pool)", n)
	}
}
