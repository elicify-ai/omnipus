package perf

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media"
)

const mediaEntries = 10_000

// BenchmarkMediaStoreResolve pre-loads 10k media refs, then benchmarks random
// Resolve lookups. This exercises the in-memory map under a read-heavy workload.
func BenchmarkMediaStoreResolve(b *testing.B) {
	b.ReportAllocs()

	dir := b.TempDir()
	store := media.NewFileMediaStore()

	// Create one real file to register under all 10k refs.
	// FileMediaStore.Store requires the file to exist at registration time.
	// We reuse the same physical file for all refs (different logical refs, same path).
	sharedFile := filepath.Join(dir, "media_payload.bin")
	payload := make([]byte, 128)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	if err := os.WriteFile(sharedFile, payload, 0o600); err != nil {
		b.Fatalf("BenchmarkMediaStoreResolve: create shared file: %v", err)
	}

	refs := make([]string, 0, mediaEntries)
	for i := 0; i < mediaEntries; i++ {
		ref, err := store.Store(sharedFile, media.MediaMeta{
			Filename:      fmt.Sprintf("file_%d.bin", i),
			ContentType:   "application/octet-stream",
			Source:        "perf-bench",
			CleanupPolicy: media.CleanupPolicyForgetOnly, // don't delete the shared file
		}, fmt.Sprintf("scope-%d", i))
		if err != nil {
			b.Fatalf("BenchmarkMediaStoreResolve: Store[%d]: %v", i, err)
		}
		refs = append(refs, ref)
	}

	b.ResetTimer()
	rng := rand.New(rand.NewSource(42)) // gosec rationale (out of gosec scope; kept as documentation): deterministic seed for reproducibility
	for i := 0; i < b.N; i++ {
		ref := refs[rng.Intn(len(refs))]
		path, err := store.Resolve(ref)
		if err != nil {
			b.Fatalf("BenchmarkMediaStoreResolve: Resolve: %v", err)
		}
		_ = path
	}
	b.StopTimer()

	b.ReportMetric(float64(mediaEntries), "entries_loaded")
}

// TestMediaResolveSLO pre-loads 10k media refs and asserts that the p99
// of 10k random Resolve calls is under 10 ms.
// The full latency distribution is logged on failure.
func TestMediaResolveSLO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	const (
		lookups     = 10_000
		p99BudgetMs = 10.0 // ms
	)

	dir := t.TempDir()
	store := media.NewFileMediaStore()

	sharedFile := filepath.Join(dir, "media_payload.bin")
	payload := make([]byte, 128)
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	if err := os.WriteFile(sharedFile, payload, 0o600); err != nil {
		t.Fatalf("TestMediaResolveSLO: create shared file: %v", err)
	}

	refs := make([]string, 0, mediaEntries)
	for i := 0; i < mediaEntries; i++ {
		ref, err := store.Store(sharedFile, media.MediaMeta{
			Filename:      fmt.Sprintf("file_%d.bin", i),
			ContentType:   "application/octet-stream",
			Source:        "perf-slo",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, fmt.Sprintf("slo-scope-%d", i))
		if err != nil {
			t.Fatalf("TestMediaResolveSLO: Store[%d]: %v", i, err)
		}
		refs = append(refs, ref)
	}

	latenciesMs := make([]float64, 0, lookups)
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < lookups; i++ {
		ref := refs[rng.Intn(len(refs))]

		start := time.Now()
		path, err := store.Resolve(ref)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("TestMediaResolveSLO lookup %d: Resolve: %v", i, err)
		}
		_ = path

		latenciesMs = append(latenciesMs, float64(elapsed.Microseconds())/1000.0)
	}

	sort.Float64s(latenciesMs)

	p99 := computePercentile(latenciesMs, 99)
	minMs := latenciesMs[0]
	maxMs := latenciesMs[len(latenciesMs)-1]
	p50 := computePercentile(latenciesMs, 50)
	p95 := computePercentile(latenciesMs, 95)

	t.Logf("TestMediaResolveSLO: %d lookups over %d refs", lookups, mediaEntries)
	t.Logf("  min=%.3f ms  p50=%.3f ms  p95=%.3f ms  p99=%.3f ms  max=%.3f ms",
		minMs, p50, p95, p99, maxMs)

	if p99 > p99BudgetMs {
		t.Errorf(
			"TestMediaResolveSLO FAILED: p99 resolve latency is %.3f ms, exceeds budget of %.0f ms. "+
				"Distribution: min=%.3f  p50=%.3f  p95=%.3f  p99=%.3f  max=%.3f (all ms). "+
				"Investigate lock contention in FileMediaStore.Resolve — the RWMutex should be uncontended here.",
			p99, p99BudgetMs, minMs, p50, p95, p99, maxMs,
		)
	}
}
