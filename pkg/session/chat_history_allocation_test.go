package session

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// T06/D-A1: isolated core-only B/op regression test. This is NOT the full
// gateway/carrier history_page_measurement receipt required by T18/UAT.
// Both owned flushers are stopped/joined; construction is outside measurement.
func TestChatHistoryIndex_PrefixIndependentCoreAllocations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows-deferred #1465: hangs on the hosted Windows runner; founder accepted red plus skip 2026-10-30, register #1504")
	}
	var allocated [2]int64
	for k, prefix := range []int{1000, 100000} {
		s, sid := newEffectsSession(t)
		historyState(t, s, sid)
		days := make([]chatHistoryFixtureDay, 90)
		for d := range days {
			days[d].Day = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Format("2006-01-02")
		}
		for i := 0; i < prefix; i++ {
			d := i * 90 / prefix
			days[d].Entries = append(days[d].Entries, TranscriptEntry{ID: fmt.Sprintf("prefix-%06d", i), Role: "user", Content: strings.Repeat("x", 1100), AgentID: "mia"})
		}
		for i := 0; i < 200; i++ {
			days[89].Entries = append(days[89].Entries, TranscriptEntry{ID: fmt.Sprintf("common-%03d", i), Role: "user", Content: "identical measured suffix", AgentID: "mia"})
		}
		buildChatHistoryDayFixture(t, s, sid, days)
		s.stopStatsFlusher()
		require.NoError(t, s.FlushSessionStats(sid))
		historyRead(t, s, sid, historyOptions(40)) // Independently check a real operation before trusting benchmark green.
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				p, err := s.ReadChatHistoryPage(context.Background(), sid, historyOptions(40))
				if err != nil {
					b.Fatalf("real reader benchmark: %v", err)
				}
				if len(p.Entries) != 40 {
					b.Fatalf("expected40 real selected groups, got%d", len(p.Entries))
				}
			}
		})
		require.Positive(t, result.N)
		allocated[k] = result.AllocedBytesPerOp()
		require.Positive(t, allocated[k], "missing measurement is not0 evidence")
		t.Logf("core-only prefix=%d sample_count=%d allocated_bytes=%d", prefix, result.N, allocated[k])
	}
	delta := allocated[1] - allocated[0]
	if delta < 0 {
		delta = -delta
	}
	require.LessOrEqual(t, delta*100, allocated[0]*10, "adopted<=10%% core allocation variance; no omitted-prefix content intermediate")
}
