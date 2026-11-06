package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// FIX-ROUND-1 row 8 / pta F3: assert durable membership and a no-write fixed
// point, not the zero value of a potentially absent Go map key.
func healFixEmptyCatalogFixedPoint(t testing.TB, s *UnifiedStore, sid string) {
	t.Helper()
	cp := historyCheckpointOf(t, s, sid)
	require.Contains(t, cp.ArchiveEnds, historyEarlierDay)
	require.Equal(t, int64(0), cp.ArchiveEnds[historyEarlierDay])
	before := historyTree(t, historyIndexDir(s, sid))
	for i := 0; i < 2; i++ {
		result, err := s.reconcileChatHistoryIndex(context.Background(), sid)
		require.NoError(t, err)
		require.Zero(t, result.ObservedItems, "the empty catalog fact was already durably recorded")
		require.False(t, result.Published, "a known-empty day is not republished on every read")
		require.Equal(t, before, historyTree(t, historyIndexDir(s, sid)))
	}
	base := s.BaseDir()
	require.NoError(t, s.Close())
	reopened, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	after := historyCheckpointOf(t, reopened, sid)
	require.Contains(t, after.ArchiveEnds, historyEarlierDay, "empty coverage survives process/store restart")
	require.Equal(t, int64(0), after.ArchiveEnds[historyEarlierDay])
	require.Equal(t, before, historyTree(t, historyIndexDir(reopened, sid)), "boot does not rewrite an already-complete empty catalog fact")
	result, err := reopened.reconcileChatHistoryIndex(context.Background(), sid)
	require.NoError(t, err)
	require.Zero(t, result.ObservedItems)
	require.False(t, result.Published)
	require.Equal(t, before, historyTree(t, historyIndexDir(reopened, sid)))
}
