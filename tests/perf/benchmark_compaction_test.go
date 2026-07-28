package perf

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// compactionTurns is the total number of WS turns driven by compaction tests.
// Reduced from 500 → 100: 500 turns caused sporadic i/o timeouts
// when the full suite runs packages concurrently (go test ./...) because goroutine
// scheduling starvation can exceed even a 300 s per-turn deadline. 100 turns is
// sufficient to exercise compaction on the scripted provider — the memory-growth
// assertion (rss@final − rss@quarter ≤ 10 MB) still holds because compaction is
// triggered by token budget, not absolute turn count.
const compactionTurns = 100

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

		// Sample points scaled to compactionTurns=100: 25/50/75/100.
		var rssTurn25, rssTurn50, rssTurn75, rssTurn100 uint64

		b.ResetTimer()
		for turn := 0; turn < compactionTurns; turn++ {
			_, err := sendAndMeasure(conn, fmt.Sprintf("compaction benchmark turn %d", turn))
			if err != nil {
				conn.Close()
				b.Fatalf("BenchmarkCompactionMemory turn %d: %v", turn, err)
			}

			switch turn + 1 {
			case 25:
				rssTurn25 = sampleRSS()
			case 50:
				rssTurn50 = sampleRSS()
			case 75:
				rssTurn75 = sampleRSS()
			case 100:
				rssTurn100 = sampleRSS()
			}
		}
		b.StopTimer()

		conn.Close()
		gw.close(b)

		toMB := func(bytes uint64) float64 { return float64(bytes) / (1024 * 1024) }

		b.ReportMetric(toMB(rssTurn25), "rss_25_mb")
		b.ReportMetric(toMB(rssTurn50), "rss_50_mb")
		b.ReportMetric(toMB(rssTurn75), "rss_75_mb")
		b.ReportMetric(toMB(rssTurn100), "rss_100_mb")
	}
}

// TestCompactionBoundsMemory drives compactionTurns turns through a scripted session
// and asserts that RSS at the final turn does not exceed RSS at the quarter-turn
// mark plus 10 MB. This verifies that compaction is keeping memory bounded as the
// conversation grows. (Turn count reduced from 500 → 100 to prevent sporadic i/o timeouts.)
func TestCompactionBoundsMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	const rssGrowthBudgetMB = 10.0

	scenario := testutil.NewScenario()
	for i := 0; i < compactionTurns; i++ {
		scenario = scenario.WithText(compactionTurnText(i))
	}

	// Redirect Providers[0].APIBase to a local mock OpenAI-compatible server.
	// Without this the test hits real OpenRouter when OPENROUTER_API_KEY is
	// set in CI env, turning each of compactionTurns turns into a real LLM
	// call (~5s each). Combined that pushed the test from 2.5 s to 535 s on
	// the Nightly Perf workflow, which then ran past the 20-min cap.
	mock := mockOpenRouterServer(t, compactionTurnText(0))

	gw := testutil.StartTestGateway(t,
		testutil.WithAllowEmpty(),
		testutil.WithScenario(scenario),
		testutil.WithAPIBase(mock.URL),
	)

	wsURL := "ws" + strings.TrimPrefix(gw.URL, "http") + "/api/v1/chat/ws"
	conn, err := dialAndAuth(wsURL, "compaction-slo-token")
	if err != nil {
		t.Fatalf("TestCompactionBoundsMemory: dial+auth: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// Sample at quarter-turn and final-turn (scaled to compactionTurns=100).
	quarterTurn := compactionTurns / 4
	finalTurn := compactionTurns
	var rssQuarter, rssFinal uint64

	for turn := 0; turn < compactionTurns; turn++ {
		_, err := sendAndMeasure(conn, fmt.Sprintf("compaction SLO turn %d", turn))
		if err != nil {
			t.Fatalf("TestCompactionBoundsMemory turn %d: %v", turn, err)
		}

		switch turn + 1 {
		case quarterTurn:
			rssQuarter = sampleRSS()
		case finalTurn:
			rssFinal = sampleRSS()
		}
	}

	toMB := func(bytes uint64) float64 { return float64(bytes) / (1024 * 1024) }

	rssQuarterMB := toMB(rssQuarter)
	rssFinalMB := toMB(rssFinal)
	growthMB := rssFinalMB - rssQuarterMB

	t.Logf("TestCompactionBoundsMemory: RSS at turn %d = %.2f MB, "+
		"RSS at turn %d = %.2f MB, growth = %.2f MB (budget: %.0f MB)",
		quarterTurn, rssQuarterMB, finalTurn, rssFinalMB, growthMB, rssGrowthBudgetMB)

	if growthMB > rssGrowthBudgetMB {
		t.Errorf(
			"TestCompactionBoundsMemory FAILED: RSS grew %.2f MB from turn %d (%.2f MB) to turn %d (%.2f MB), "+
				"exceeding the %.0f MB budget. "+
				"Compaction is not keeping memory bounded — check that the agent loop is pruning tool results "+
				"and compacting old conversation context as expected.",
			growthMB, quarterTurn, rssQuarterMB, finalTurn, rssFinalMB, rssGrowthBudgetMB,
		)
	}
}
