package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// T64/A2.3: system content is intentionally not shortened (A2.1). Two4MiB
// contents alone equal8MiB; real public envelopes make that candidate exceed
// the ceiling, so one source returns below the100-row floor. No guessed meter.
func TestChatHistoryPage_ByteCeilingCarrierBelowFloor(t *testing.T) {
	s, sid, _ := pagingProjectionFixture(t)
	body := strings.Repeat("s", 4194304)
	for i := 0; i < 3; i++ {
		require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: fmt.Sprintf("ceiling-structured-%d", i), Type: session.EntryTypeSystem, Role: "system", AgentID: "mia", ViewMembership: session.ViewMembershipChat, Content: body}))
	}
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 100, MinRows: 100}, historyPageProjectionDeps{})
	require.NoError(t, err)
	p, err := r.Read(context.Background(), s)
	require.NoError(t, err, "real prepared carrier validation accepts byte_ceiling underfill")
	require.Len(t, p.Response.Entries, 1)
	require.Equal(t, "ceiling-structured-2", p.Response.Entries[0].Id)
	require.Equal(t, "byte_ceiling", p.Response.PageInfo.StopReason)
	require.Equal(t, 1, p.Response.PageInfo.RowCount)
	require.Equal(t, 100, p.Response.PageInfo.MinRows)
	require.EqualValues(t, 8388608, p.Response.PageInfo.ByteCeiling)
	require.LessOrEqual(t, p.Response.PageInfo.ProjectedBytes, int64(8388608))
	require.True(t, p.Response.HasEarlier)
	require.Equal(t, p.Response.Entries[0].HistoryPosition, p.Response.NextBefore)
	require.Nil(t, r.memo, "rejected lookahead/preparation cache is discarded before the carrier escapes")
	require.NotContains(t, string(p.encodedResponse), "ceiling-structured-1", "no rejected lookahead group on the public carrier")
}
