// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

// ADR-054 Wave 4 (perf gate) — B4: does concurrent fan-out contend on the
// single sync.Mutex + O_APPEND + sequential-HMAC-chain design in Logger?
//
// ADR-054 §7 (D5) explicitly DEFERRED the audit-chain-scoping decision
// pending this measurement: "if it proves to be the ceiling, revisit."
// This file is that measurement. See docs/internal/architecture/
// ADR-054-entity-config-separation.md §7 and §12.
//
// Traces to: docs/internal/plan (Wave 4 perf gate prompt) — B4.
//
// Design: each testing.B "op" is a WHOLE BATCH of N concurrent Log() calls,
// not a single append. This makes the standard `ns/op` metric testing.B
// already reports equal to "wall-clock per N-writer batch" — exactly the
// number the ADR asks for (a per-call average would hide a lock convoy;
// timing a full concurrent batch as one unit does not). Divide the reported
// ns/op by N to recover a per-single-append estimate.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// benchChainKey is a fixed, deterministic 32-byte HMAC key for these
// benchmarks/tests only. Supplying it explicitly avoids NewLogger's
// insecure-dev-key fallback warning path (resolveChainKey) from firing
// repeatedly across benchmark sub-runs.
var benchChainKey = []byte("0123456789abcdef0123456789abcdef")

func init() {
	if len(benchChainKey) != 32 {
		panic(fmt.Sprintf("benchChainKey must be 32 bytes, got %d", len(benchChainKey)))
	}
}

// newPerfBenchLogger constructs a Logger rooted at dir with a fixed chain
// key. tb.Fatalf on construction failure — a benchmark/test that silently
// proceeds with a nil logger measures nothing.
func newPerfBenchLogger(tb testing.TB, dir string) *Logger {
	tb.Helper()
	l, err := NewLogger(LoggerConfig{
		Dir:               dir,
		HMACKey:           benchChainKey,
		AuditLogRequested: true,
	})
	if err != nil {
		tb.Fatalf("NewLogger: %v", err)
	}
	return l
}

// BenchmarkAuditAppend_Concurrent measures Logger.Log throughput at N = 1, 8,
// 32 concurrent writers (the ADR's requested fan-out levels). Each b.N
// iteration is one full batch of N goroutines each appending one distinct
// entry, then Wait()ing — so reported ns/op IS the batch wall-clock, and
// B/op + allocs/op are ReportAllocs()'s aggregate-per-batch figures.
//
// If the single sync.Mutex in writeLine is the bottleneck the ADR worries
// about, wall-clock-per-batch should scale roughly linearly with N (each
// goroutine serializes behind the mutex with no overlap). If the mutex is
// cheap relative to the syscalls it guards (or the OS scheduler overlaps
// goroutine wake-up with the previous writer's I/O), scaling will be
// sub-linear.
func BenchmarkAuditAppend_Concurrent(b *testing.B) {
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			logger := newPerfBenchLogger(b, dir)
			// Test cleanup: Close error is inconsequential — t.TempDir() removes
			// the backing directory regardless, and no test here asserts on it.
			defer func() {
				if closeErr := logger.Close(); closeErr != nil {
					_ = closeErr
				}
			}()

			var failed int64

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				wg.Add(n)
				for g := 0; g < n; g++ {
					go func(iter, g int) {
						defer wg.Done()
						entry := &Entry{
							Event:    EventToolCall,
							Decision: DecisionAllow,
							AgentID:  fmt.Sprintf("bench-agent-%d", g),
							Tool:     "perf_bench.tool",
							Details: map[string]any{
								"iter": iter,
								"g":    g,
							},
						}
						if err := logger.Log(entry); err != nil {
							atomic.AddInt64(&failed, 1)
						}
					}(i, g)
				}
				wg.Wait()
			}
			b.StopTimer()

			// Zero-tolerance: a benchmark that silently drops writes under
			// contention is not measuring the thing the ADR cares about.
			if got := atomic.LoadInt64(&failed); got > 0 {
				b.Fatalf("BLOCKED: %d/%d audit Log() calls failed under N=%d fan-out — "+
					"cannot report a throughput number for a lossy write path", got, int64(b.N)*int64(n), n)
			}
		})
	}
}

// BenchmarkAuditAppend_Concurrent_Fsync is the supplementary "worst case"
// companion to BenchmarkAuditAppend_Concurrent: DecisionDeny entries take
// criticalEventNeedsSync's fsync path (audit.go's writeLine calls
// l.file.Sync() before returning), a materially different and disk-latency-
// dependent cost profile from the buffered/bulk-allow path measured above.
// Since a fsync'd write holds the mutex for longer, this is the more
// relevant case for "is the mutex actually the ceiling" — a slow disk here
// would show up as PER-OP cost growing (not just batch wall-clock), unlike
// the allow-path benchmark where per-op cost stayed flat.
func BenchmarkAuditAppend_Concurrent_Fsync(b *testing.B) {
	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			dir := b.TempDir()
			logger := newPerfBenchLogger(b, dir)
			defer func() {
				if closeErr := logger.Close(); closeErr != nil {
					_ = closeErr
				}
			}()

			var failed int64

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup
				wg.Add(n)
				for g := 0; g < n; g++ {
					go func(iter, g int) {
						defer wg.Done()
						entry := &Entry{
							Event:    EventToolPolicyDenyAttempted,
							Decision: DecisionDeny,
							AgentID:  fmt.Sprintf("bench-agent-%d", g),
							Tool:     "perf_bench.tool",
							Details: map[string]any{
								"iter": iter,
								"g":    g,
							},
						}
						if err := logger.Log(entry); err != nil {
							atomic.AddInt64(&failed, 1)
						}
					}(i, g)
				}
				wg.Wait()
			}
			b.StopTimer()

			if got := atomic.LoadInt64(&failed); got > 0 {
				b.Fatalf("BLOCKED: %d/%d audit Log() (fsync path) calls failed under N=%d fan-out",
					got, int64(b.N)*int64(n), n)
			}
		})
	}
}

// TestAuditLog_ConcurrentAppend is the -race correctness companion to
// BenchmarkAuditAppend_Concurrent (per the task rule: "a benchmark that
// races is measuring nothing"). It fires N goroutines at the same Logger
// concurrently, then verifies:
//  1. every Log() call succeeded (no silent drop),
//  2. the on-disk HMAC chain is still valid (the mutex actually serializes
//     the chain-embedding step, so concurrent appends don't corrupt it),
//  3. exactly N entries landed (no lost writes, no duplicate writes).
//
// Run with: go test -race -run 'ConcurrentAppend' ./pkg/audit/...
func TestAuditLog_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	logger := newPerfBenchLogger(t, dir)
	defer func() {
		if closeErr := logger.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	const n = 32
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			entry := &Entry{
				Event:    EventToolCall,
				Decision: DecisionAllow,
				AgentID:  fmt.Sprintf("concurrent-agent-%d", g),
				Tool:     "concurrency.probe",
				Details:  map[string]any{"g": g},
			}
			errs[g] = logger.Log(entry)
		}(g)
	}
	wg.Wait()

	for g, err := range errs {
		if err != nil {
			t.Fatalf("Log() goroutine %d failed: %v", g, err)
		}
	}

	result, err := logger.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Valid {
		t.Fatalf("BLOCKED: audit HMAC chain broken after %d concurrent appends "+
			"(broken_at=%d reason=%q) — the single sync.Mutex in writeLine does NOT "+
			"safely serialize concurrent Log() calls against this chain design",
			n, result.BrokenAt, result.Reason)
	}
	if result.EntriesScanned != n {
		t.Fatalf("expected exactly %d entries in the chain after %d concurrent Log() calls, "+
			"got %d scanned — a concurrent append was silently lost or duplicated",
			n, n, result.EntriesScanned)
	}
}
