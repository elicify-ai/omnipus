package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// T06/D-A1: isolated core-only B/op regression test. This is NOT the full
// gateway/carrier history_page_measurement receipt required by T18/UAT.
// Both owned flushers are stopped/joined; construction is outside measurement.
// Q-T10: the allocation-only fixture uses batched canonical setup plus the real
// offline builder. See seedCoreAllocationFixture's explicit seam-G exception;
// this test is not evidence for per-row writer/durability throughput.
func TestChatHistoryIndex_PrefixIndependentCoreAllocations(t *testing.T) {
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
		s = seedCoreAllocationFixture(t, s, sid, days)
		// Setup/index construction is complete before either the warm-up or the
		// benchmark timer. Independently verify the exact identical suffix first.
		warm := historyRead(t, s, sid, historyOptions(40))
		want := make([]string, 40)
		for i := range want {
			want[i] = fmt.Sprintf("common-%03d", 160+i) // final40 of the authored200-row suffix
		}
		require.Equal(t, want, historyIDs(warm))
		for _, entry := range warm.Entries {
			require.Equal(t, "identical measured suffix", entry.Entry.Content)
		}
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

// Q-T10 fixture-integrity guard: the exact same assertions are run first with
// the original real-writer fixture, then with the allocation-only setup. It is
// expected to pass on the original code; it guards fixture semantics, not a
// newly alleged product defect or a Windows runtime acceptance.
func TestChatHistoryAllocationFixture_ExactRolledRows(t *testing.T) {
	s, sid := newEffectsSession(t)
	days := []chatHistoryFixtureDay{
		{Day: "2026-01-01", Entries: []TranscriptEntry{
			{ID: "old-a", Role: "user", Content: "old first", AgentID: "mia"},
			{ID: "old-b", Role: "user", Content: "old second", AgentID: "mia"},
		}},
		{Day: "2026-01-02", Entries: []TranscriptEntry{
			{ID: "tail-a", Role: "user", Content: "tail first", AgentID: "mia"},
			{ID: "tail-b", Role: "user", Content: "tail second", AgentID: "mia"},
		}},
	}
	s = seedCoreAllocationFixture(t, s, sid, days)
	p := historyRead(t, s, sid, historyOptions(40))
	require.Equal(t, []string{"old-a", "old-b", "tail-a", "tail-b"}, historyIDs(p), "fixture never omits or invents a canonical row")
	require.Equal(t, []string{"old first", "old second", "tail first", "tail second"}, []string{
		p.Entries[0].Entry.Content, p.Entries[1].Entry.Content, p.Entries[2].Entry.Content, p.Entries[3].Entry.Content,
	}, "real indexed reader preserves exact authored content across the rollover")
	require.False(t, p.HasEarlier, "all four authored groups are included")
	require.False(t, p.HasLater, "the last authored group is the real end")
	require.Len(t, historyState(t, s, sid).RetainedPartitions, 2, "two real UTC partitions, not four rows in one current file")
}
