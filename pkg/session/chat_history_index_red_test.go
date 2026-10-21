package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// T06/B21/B22/B31/B40. Actual private read events, not a stopwatch or a full
// reader used as an oracle. The100000-prefix cases are remote-CI work only.
func TestChatHistoryIndex_HealthyCutAndBoundedReads(t *testing.T) {
	for _, prefix := range []int{1000, 100000} {
		for _, limit := range []int{40, 100} {
			t.Run(fmt.Sprintf("prefix%d_limit%d", prefix, limit), func(t *testing.T) {
				s, sid := newEffectsSession(t)
				historyState(t, s, sid) // Fail at the real named runtime seam while unimplemented.
				days := make([]chatHistoryFixtureDay, 90)
				for i := 0; i < prefix; i++ {
					d := i * 90 / prefix
					days[d].Day = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, d).Format("2006-01-02")
					days[d].Entries = append(days[d].Entries, TranscriptEntry{ID: fmt.Sprintf("prefix-%06d", i), Role: "user", Content: strings.Repeat("p", 1100), AgentID: "mia", Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
				}
				for i := 1; i <= 200; i++ {
					days[89].Entries = append(days[89].Entries, TranscriptEntry{ID: fmt.Sprintf("suffix-%03d", i), Role: "user", Content: "same suffix", AgentID: "mia", Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
				}
				buildChatHistoryDayFixture(t, s, sid, days)
				s.stopStatsFlusher()
				require.NoError(t, s.FlushSessionStats(sid))
				var reads historyReadRecorder
				historyObserve(t, &reads)
				p := historyRead(t, s, sid, historyOptions(limit))
				want := make([]string, limit)
				for i := range want {
					want[i] = fmt.Sprintf("suffix-%03d", 201-limit+i)
				}
				require.Equal(t, want, historyIDs(p))
				require.True(t, p.HasEarlier)
				require.False(t, p.HasLater)
				selected, lookahead := 0, 0
				selectedIDs := map[string]bool{}
				for _, id := range want {
					selectedIDs[id] = true
				}
				for _, e := range reads.snapshot() {
					if e.Phase != "record_read" || e.Stream != "archive" || e.Scope != "page" {
						continue
					}
					require.Greater(t, e.Bytes, int64(0), "actual encoded I/O observed")
					require.NotNil(t, e.Address)
					require.NotContains(t, e.Address.EntryID, "prefix-", "no omitted prefix decoding")
					if selectedIDs[e.Address.EntryID] {
						selected++
					} else {
						require.Equal(t, fmt.Sprintf("suffix-%03d", 200-limit), e.Address.EntryID, "only the nearest one unselected candidate may be read")
						lookahead++
					}
				}
				require.Equal(t, limit, selected, "one actual selected body per ordinary group")
				require.LessOrEqual(t, lookahead, 1, "metadata-only lookahead may read0, never a mandatory41st body")
			})
		}
	}
	for _, pending := range []int{0, 1, 63, 64, 65} {
		t.Run(fmt.Sprintf("pending%d", pending), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "base", 1)
			before := historyState(t, s, sid)
			files := historyTree(t, historyIndexDir(s, sid))
			addresses := historyUnpublishedArchive(t, s, sid, pending)
			var reads historyReadRecorder
			historyObserve(t, &reads)
			for attempt := 0; attempt < 2; attempt++ {
				start := len(reads.snapshot())
				r, err := s.reconcileChatHistoryIndex(context.Background(), sid)
				historyAssertSuffixReads(t, reads.snapshot()[start:], addresses)
				if pending == 65 {
					historyRequireUnavailable(t, err, "catchup_limit")
					require.Equal(t, 65, r.ObservedItems)
					require.Zero(t, r.AppliedItems)
					require.False(t, r.Published)
					require.Equal(t, before, historyState(t, s, sid))
					continue
				}
				require.NoError(t, err, "real bounded reconciler")
				want := pending
				if attempt == 1 {
					want = 0
				}
				require.Equal(t, want, r.ObservedItems)
				require.Equal(t, want, r.AppliedItems)
				require.Equal(t, want, r.ArchiveRecords)
				require.Equal(t, want > 0, r.Published)
			}
			if pending == 0 {
				require.Equal(t, files, historyTree(t, historyIndexDir(s, sid)), "zero delta changes no bytes/mode/mtime")
			}
			if pending <= 64 {
				p := historyRead(t, s, sid, historyOptions(100))
				require.Len(t, p.Entries, 1+pending)
			}
		})
	}
	t.Run("missing_baseline_is_not_empty", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		historyRows(t, s, sid, "base", 1)
		require.NoError(t, os.RemoveAll(historyIndexDir(s, sid)))
		_, err := s.ReadChatHistoryPage(context.Background(), sid, historyOptions(40))
		historyRequireUnavailable(t, err, "index_missing")
		historyRows(t, s, sid, "new-send", 1)
		_, err = s.ReadChatHistoryPage(context.Background(), sid, historyOptions(40))
		historyRequireUnavailable(t, err, "index_missing")
		require.NoFileExists(t, filepath.Join(historyIndexDir(s, sid), "checkpoint.json"), "new canonical send cannot seed a suffix-only baseline")
	})
}
