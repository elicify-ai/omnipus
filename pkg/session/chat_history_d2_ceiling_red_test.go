package session

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// T64/B74: an explicit public-carrier weigher is the existing process boundary.
// Discrete counts/positions, never elapsed time, prove selection and continuation.
func TestChatHistoryPage_ByteCeilingStopsBelowFloor(t *testing.T) {
	const ceiling int64 = 8388608 // A2.3, server-owned8MiB, not an observed result.
	for _, delta := range []int64{-1, 0, 1} {
		t.Run(fmt.Sprintf("second_candidate_ceiling_%+d", delta), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			addresses := historyRows(t, s, sid, "ceiling", 3)
			weigh := func(_ context.Context, p ChatHistoryPage) (ChatHistoryWeight, error) {
				cost := int64(4194305) // One complete group,1 byte above the soft budget.
				if len(p.Entries) == 2 {
					cost = ceiling + delta
				}
				if len(p.Entries) >= 3 {
					cost = ceiling + 100
				}
				return ChatHistoryWeight{RESTBytes: cost, WSBytes: cost, ProjectedBytes: cost}, nil
			}
			p := historyRead(t, s, sid, ChatHistoryPageOptions{Limit: 20, MinRows: 20, Weigh: weigh})
			wantCount := 2
			if delta > 0 {
				wantCount = 1
			}
			require.Len(t, p.Entries, wantCount, "A2.3: stop BEFORE the first candidate that crosses8MiB, even below floor20")
			require.Equal(t, "byte_ceiling", p.PageInfo.StopReason, "ceiling outranks coincident soft-budget stop")
			require.Less(t, p.PageInfo.RowCount, p.PageInfo.MinRows)
			require.LessOrEqual(t, p.PageInfo.ProjectedBytes, ceiling)
			require.True(t, p.HasEarlier)
			require.Equal(t, historyExpectedPosition(sid, addresses[3-wantCount]), p.NextBefore)
			require.Equal(t, historyExpectedPosition(sid, addresses[2]), p.Entries[len(p.Entries)-1].Position)
			// A subsequent real read resumes at the immediate retained predecessor,
			// not a repeated boundary or a hidden discarded lookahead.
			before := p.NextBefore
			older := historyRead(t, s, sid, ChatHistoryPageOptions{Limit: 20, MinRows: 1, Before: before, Weigh: historyFixtureWeigher})
			require.Len(t, older.Entries, 3-wantCount)
			require.Equal(t, historyExpectedPosition(sid, addresses[2-wantCount]), older.Entries[len(older.Entries)-1].Position)
			require.NotContains(t, historyIDs(older), p.Entries[0].Entry.ID)
		})
	}
}
