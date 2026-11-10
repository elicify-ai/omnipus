package session

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatHistoryRecovery_EmptyFreshCheckpointIsNoOp(t *testing.T) {
	s, sid := newEffectsSession(t)
	before := historyTree(t, historyIndexDir(s, sid))
	r, err := s.reconcileChatHistoryIndex(context.Background(), sid)
	require.NoError(t, err, "real zero-delta reconciler, not a declaration returning not-implemented")
	require.Zero(t, r.ObservedItems)
	require.Zero(t, r.AppliedItems)
	require.False(t, r.Published)
	historyState(t, s, sid)
	require.Equal(t, before, historyTree(t, historyIndexDir(s, sid)), "zero recovery writes no byte/mtime")
}

func TestChatHistoryRetention_UnknownTombstoneLookupIsBoundedAbsent(t *testing.T) {
	s, sid := newEffectsSession(t)
	_, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, HistoryPosition("hp1_"+strings.Repeat("A", 43)))
	require.NoError(t, err, "real bounded tombstone accessor on a healthy fresh session")
	require.False(t, found, "unknown token is not an expired occurrence and never crosses a session")
}

func TestChatHistoryReadCut_EmptyPageStillRequiresRealReader(t *testing.T) {
	s, sid := newEffectsSession(t)
	p := historyRead(t, s, sid, historyOptions(40))
	require.Empty(t, p.Entries)
	require.False(t, p.HasEarlier)
	require.False(t, p.HasLater)
	require.Equal(t, "latest", p.PageInfo.Mode)
	require.Equal(t, 40, p.PageInfo.Limit)
	require.Equal(t, 20, p.PageInfo.MinRows)
	require.NotEmpty(t, p.Cut.Generation())
	require.Equal(t, sid, p.Cut.SessionID())
}
