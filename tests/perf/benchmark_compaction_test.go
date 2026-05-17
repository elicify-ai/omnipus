//go:build !cgo

package perf

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
)

const compactionTurns = 500

// compactionTurnText produces a distinct medium-length response for turn i.
// Using unique content per turn exercises compaction of varied (non-repetitive) content.
func compactionTurnText(i int) string {
	return fmt.Sprintf(
		"Turn %d — this is a medium-length response with enough tokens to meaningfully "+
			"populate the session. It contains varied content so compaction must process "+
			"distinct message bodies rather than repeated identical strings. "+
			"Paragraph break follows. The agent loop processes each turn sequentially, "+
			"accumulating transcript entries until the compaction threshold is reached, "+
			"at which point older messages are summarized and pruned from in-memory context.",
		i,
	)
}

// sampleRSS returns the current heap in-use bytes from runtime.MemStats.
// It calls runtime.GC() twice to stabilize the reading: the first pass
// promotes finalizer-queued objects and clears reachable garbage, and the
// second pass collects the floating garbage released by the first. Using
// HeapInuse from MemStats is more deterministic than OS-level RSS because it
// measures the Go allocator's view of live heap, which is unaffected by OS
// memory-overcommit and GC-scheduling jitter.
func sampleRSS() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

// BenchmarkCompactionMemory drives a single session to 500 turns using a
// ScenarioProvider with 500 distinct responses. It samples heap at turns
// 50, 100, 200, and 500 and reports them as custom metrics.
func BenchmarkCompactionMemory(b *testing.B) {
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		scenario := testutil.NewScenario()
		for i := 0; i < compactionTurns; i++ {
			scenario = scenario.WithText(compactionTurnText(i))
		}

		gw := startPerfGateway(b, scenario)

		wsURL := "ws" + strings.TrimPrefix(gw.URL, "http") + "/api/v1/chat/ws"
		conn, err := dialAndAuth(wsURL, "compaction-token")
		if err != nil {
			b.Fatalf("BenchmarkCompactionMemory: dial+auth: %v", err)
		}

		var rssTurn50, rssTurn100, rssTurn200, rssTurn500 uint64

		b.ResetTimer()
		for turn := 0; turn < compactionTurns; turn++ {
			_, err := sendAndMeasure(conn, fmt.Sprintf("compaction benchmark turn %d", turn))
			if err != nil {
				conn.Close()
				b.Fatalf("BenchmarkCompactionMemory turn %d: %v", turn, err)
			}

			switch turn + 1 {
			case 50:
				rssTurn50 = sampleRSS()
			case 100:
				rssTurn100 = sampleRSS()
			case 200:
				rssTurn200 = sampleRSS()
			case 500:
				rssTurn500 = sampleRSS()
			}
		}
		b.StopTimer()

		conn.Close()
		gw.close(b)

		toMB := func(bytes uint64) float64 { return float64(bytes) / (1024 * 1024) }

		b.ReportMetric(toMB(rssTurn50), "rss_50_mb")
		b.ReportMetric(toMB(rssTurn100), "rss_100_mb")
		b.ReportMetric(toMB(rssTurn200), "rss_200_mb")
		b.ReportMetric(toMB(rssTurn500), "rss_500_mb")
	}
}

// TestCompactionBoundsMemory drives 500 turns through a scripted session and
// asserts that RSS at turn 500 does not exceed RSS at turn 50 plus 10 MB.
// This verifies that compaction is keeping memory bounded as the conversation grows.
func TestCompactionBoundsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	const rssGrowthBudgetMB = 10.0

	scenario := testutil.NewScenario()
	for i := 0; i < compactionTurns; i++ {
		scenario = scenario.WithText(compactionTurnText(i))
	}

	gw := testutil.StartTestGateway(t,
		testutil.WithAllowEmpty(),
		testutil.WithScenario(scenario),
	)

	wsURL := "ws" + strings.TrimPrefix(gw.URL, "http") + "/api/v1/chat/ws"
	conn, err := dialAndAuth(wsURL, "compaction-slo-token")
	if err != nil {
		t.Fatalf("TestCompactionBoundsMemory: dial+auth: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	var rssTurn50, rssTurn500 uint64

	for turn := 0; turn < compactionTurns; turn++ {
		_, err := sendAndMeasure(conn, fmt.Sprintf("compaction SLO turn %d", turn))
		if err != nil {
			t.Fatalf("TestCompactionBoundsMemory turn %d: %v", turn, err)
		}

		switch turn + 1 {
		case 50:
			rssTurn50 = sampleRSS()
		case 500:
			rssTurn500 = sampleRSS()
		}
	}

	toMB := func(bytes uint64) float64 { return float64(bytes) / (1024 * 1024) }

	rss50MB := toMB(rssTurn50)
	rss500MB := toMB(rssTurn500)
	growthMB := rss500MB - rss50MB

	t.Logf("TestCompactionBoundsMemory: RSS at turn 50 = %.2f MB, "+
		"RSS at turn 500 = %.2f MB, growth = %.2f MB (budget: %.0f MB)",
		rss50MB, rss500MB, growthMB, rssGrowthBudgetMB)

	if growthMB > rssGrowthBudgetMB {
		t.Errorf(
			"TestCompactionBoundsMemory FAILED: RSS grew %.2f MB from turn 50 (%.2f MB) to turn 500 (%.2f MB), "+
				"exceeding the %.0f MB budget. "+
				"Compaction is not keeping memory bounded — check that the agent loop is pruning tool results "+
				"and compacting old conversation context as expected.",
			growthMB, rss50MB, rss500MB, rssGrowthBudgetMB,
		)
	}
}
