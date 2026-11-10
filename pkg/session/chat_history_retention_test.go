package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func historyRetentionFixture(t *testing.T, expired int) (*UnifiedStore, string, []ArchiveAddress) {
	t.Helper()
	s, sid := newEffectsSession(t)
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	days := []chatHistoryFixtureDay{historyDayRows("2026-01-01", "first", 20, nil), historyDayRows("2026-01-02", "middle", 20, nil), historyDayRows("2026-01-03", "last", 20, nil), historyDayRows("2026-01-04", "current", 20, nil)}
	if expired >= 0 {
		days[expired].MTime = &old
	}
	addresses := buildChatHistoryDayFixture(t, s, sid, days)
	return s, sid, addresses
}

func TestChatHistoryRetention_ActualRetainedContinuation(t *testing.T) {
	for _, which := range []int{0, 1, 2} {
		t.Run([]string{"first_expired_exact_full_terminal_has_no_before", "middle_expired_neighbors_keep_order_and_positions", "last_rolled_expired_current_retained_truthful_after"}[which], func(t *testing.T) {
			s, sid, addresses := historyRetentionFixture(t, which)
			before := historyState(t, s, sid)
			removed, err := s.RetentionSweep(90)
			require.NoError(t, err)
			require.Equal(t, 1, removed)
			after := historyState(t, s, sid)
			require.NotEqual(t, before.RetentionGeneration, after.RetentionGeneration)
			require.NotContains(t, after.RetainedPartitions, addresses[which*20].PartitionKey)
			opts := historyOptions(100)
			p := historyRead(t, s, sid, opts)
			want := make([]string, 0, 60)
			for i, a := range addresses {
				if i/20 != which {
					want = append(want, a.EntryID)
				}
			}
			require.Equal(t, want, historyIDs(p))
			require.False(t, p.HasEarlier)
			require.False(t, p.HasLater)
			require.Empty(t, p.NextBefore)
			require.Empty(t, p.NextAfter)
			opts = historyOptions(20)
			opts.Before = historyExpectedPosition(sid, addresses[60])
			older := historyRead(t, s, sid, opts)
			if which == 0 {
				require.Equal(t, addresses[40].EntryID, older.Entries[0].Entry.ID, "nearest retained segment before current")
				require.True(t, older.HasEarlier, "middle retained partition still precedes this boundary page")
				opts.Before = older.PageInfo.FirstPosition
				terminal := historyRead(t, s, sid, opts)
				require.Equal(t, addresses[20].EntryID, terminal.Entries[0].Entry.ID)
				require.Len(t, terminal.Entries, 20)
				require.False(t, terminal.HasEarlier, "exactly-full first retained page is terminal")
				require.Empty(t, terminal.NextBefore)
			}
			// A known expired fence still selects by private order, never404/latest.
			opts = historyOptions(20)
			opts.After = historyExpectedPosition(sid, addresses[which*20+19])
			forward := historyRead(t, s, sid, opts)
			require.Equal(t, addresses[(which+1)*20].EntryID, forward.Entries[0].Entry.ID)
			for i := which * 20; i < (which+1)*20; i++ {
				pos := historyExpectedPosition(sid, addresses[i])
				tomb, found, tombErr := s.lookupChatHistoryTombstone(context.Background(), sid, pos)
				require.NoError(t, tombErr)
				require.True(t, found)
				require.Equal(t, pos, tomb.Position)
				typ := reflect.TypeOf(tomb)
				require.Equal(t, 2, typ.NumField(), "only token/rank, not a rich DTO pretending to be a tombstone")
				require.Equal(t, "Position", typ.Field(0).Name)
				require.Equal(t, "Rank", typ.Field(1).Name)
			}
			historyAssertNoRichExpiredMetadata(t, s, sid, addresses[which*20].EntryID)
			stable := historyState(t, s, sid)
			removed, err = s.RetentionSweep(90)
			require.NoError(t, err)
			require.Zero(t, removed)
			require.Equal(t, stable.RetentionGeneration, historyState(t, s, sid).RetentionGeneration)
		})
	}
	historyRetentionSpecialCases(t)
}

func historyRetentionSpecialCases(t *testing.T) {
	t.Helper()
	t.Run("retention_disabled_generation_unchanged", func(t *testing.T) {
		s, sid, _ := historyRetentionFixture(t, 0)
		before := historyState(t, s, sid)
		n, err := s.RetentionSweep(0)
		require.NoError(t, err)
		require.Zero(t, n)
		require.Equal(t, before, historyState(t, s, sid))
	})
	t.Run("token_rank_only_tombstones_rich_fragment_retired", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		days := []chatHistoryFixtureDay{historyDayRows("2026-01-01", "EXPIRED_ID_CANARY", 2, &old), historyDayRows("2026-01-02", "retained", 1, nil)}
		days[0].Entries[0].Content = "EXPIRED_BODY_CANARY"
		days[0].Entries[0].Timestamp = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
		a := buildChatHistoryDayFixture(t, s, sid, days)
		_, err := s.RetentionSweep(90)
		require.NoError(t, err)
		first, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, historyExpectedPosition(sid, a[0]))
		require.NoError(t, err)
		require.True(t, found)
		second, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, historyExpectedPosition(sid, a[1]))
		require.NoError(t, err)
		require.True(t, found)
		require.Greater(t, second.Rank, first.Rank)
		historyAssertNoRichExpiredMetadata(t, s, sid, "EXPIRED_ID_CANARY", "EXPIRED_BODY_CANARY", "2001-02-03T04:05:06")
		opts := historyOptions(20)
		opts.WindowStart = first.Position
		opts.WindowEnd = second.Position
		p := historyRead(t, s, sid, opts)
		require.Empty(t, p.Entries)
		require.Equal(t, "window_refresh", p.PageInfo.Mode)
		require.False(t, p.HasEarlier)
		require.False(t, p.HasLater)
		require.NoError(t, s.DeleteSession(sid))
		require.NoDirExists(t, historyIndexDir(s, sid), "whole-session deletion removes tombstone ledger")
	})
	t.Run("unknown_foreign_fence_still404", func(t *testing.T) {
		s, sid, _ := historyRetentionFixture(t, 0)
		o := historyOptions(20)
		o.Before = HistoryPosition("hp1_" + strings.Repeat("A", 43))
		_, err := s.ReadChatHistoryPage(context.Background(), sid, o)
		require.ErrorIs(t, err, ErrHistoryPositionNotFound)
	})
	t.Run("permission_failure_not_expiry_and_partial_success_truthful", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		buildChatHistoryDayFixture(t, s, sid, []chatHistoryFixtureDay{historyDayRows("2026-01-01", "a", 1, &old), historyDayRows("2026-01-02", "b", 1, &old), historyDayRows("2026-01-03", "c", 1, nil)})
		before := historyState(t, s, sid)
		remove := removePartitionFn
		removePartitionFn = func(path string) error {
			if strings.HasSuffix(path, "2026-01-02.jsonl") {
				return os.ErrPermission
			}
			return os.Remove(path)
		}
		t.Cleanup(func() { removePartitionFn = remove })
		n, err := s.RetentionSweep(90)
		require.ErrorIs(t, err, os.ErrPermission)
		require.Equal(t, 1, n, "first real deletion still counts")
		after := historyState(t, s, sid)
		require.NotEqual(t, before.RetentionGeneration, after.RetentionGeneration)
		require.NotContains(t, after.RetainedPartitions, "2026-01-01")
		require.Contains(t, after.RetainedPartitions, "2026-01-02")
		require.False(t, isArchiveSharingBusy(os.ErrPermission), "generic permission is never Windows sharing-busy")
	})
}

// D-H2 combined deletion/archive bound, with a real persisted catalog. Direct
// unlink here is explicitly the crash-residue fixture, not a substitute sweep.
func TestChatHistoryRetention_DeleteGapCombinedBoundAndZeroRemovalRetry(t *testing.T) {
	for _, tc := range []struct{ archive, deleted int }{{0, 1}, {0, 63}, {0, 64}, {0, 65}, {32, 32}, {33, 32}} {
		t.Run(fmt.Sprintf("archive%d_delete%d", tc.archive, tc.deleted), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			days := make([]chatHistoryFixtureDay, tc.deleted+1)
			for i := range days {
				day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02")
				days[i] = historyDayRows(day, fmt.Sprintf("d%d", i), 1, nil)
			}
			a := buildChatHistoryDayFixture(t, s, sid, days)
			before := historyState(t, s, sid)
			for i := 0; i < tc.deleted; i++ {
				require.NoError(t, os.Remove(filepath.Join(s.BaseDir(), sid, a[i].PartitionKey+".jsonl")))
			}
			historyUnpublishedArchive(t, s, sid, tc.archive)
			archive, err := s.archiveStore(sid)
			require.NoError(t, err)
			notice, err := archive.SweepExpired(90)
			require.NoError(t, err)
			require.Zero(t, notice.RemovedPartitions, "next sweep cannot re-report files already unlinked before crash")
			r, err := s.reconcileChatHistoryIndex(context.Background(), sid)
			total := tc.archive + tc.deleted
			if total > 64 {
				historyRequireUnavailable(t, err, "catchup_limit")
				require.Equal(t, 65, r.ObservedItems)
				require.Zero(t, r.AppliedItems)
				require.False(t, r.Published)
				require.Equal(t, before, historyState(t, s, sid))
				return
			}
			require.NoError(t, err)
			require.Equal(t, total, r.ObservedItems)
			require.Equal(t, total, r.AppliedItems)
			require.Equal(t, tc.archive, r.ArchiveRecords)
			require.Equal(t, tc.deleted, r.ExpiredPartitions)
			after := historyState(t, s, sid)
			require.NotEqual(t, before.RetentionGeneration, after.RetentionGeneration)
			p := historyRead(t, s, sid, historyOptions(100))
			require.Len(t, p.Entries, 1+tc.archive)
			require.False(t, p.HasEarlier)
			r, err = s.reconcileChatHistoryIndex(context.Background(), sid)
			require.NoError(t, err)
			require.Zero(t, r.AppliedItems)
			require.False(t, r.Published)
			require.Equal(t, after, historyState(t, s, sid))
		})
	}
}
