package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Q-T10: founder-authorized ALLOCATION-ONLY exception to fixture seam G.
// This is NOT buildChatHistoryDayFixture and must not replace it in writer,
// durability, retention or publication tests. The unit being measured is the
// real indexed reader, not 100k canonical saves or 100k derived publications.
//
// The existing store establishes a real empty identity/baseline and is closed
// before fixture I/O. The production record validator/encoder and actual day
// rollover are retained, but canonical writes are batched and synced ONCE PER
// DAY, with no per-row appender, stats update, or derived publication. The real
// offline builder reconstructs and verifies every source group; a freshly
// reopened store performs the measured reads. No index/position/projection
// implementation or expected page is fabricated here.
func seedCoreAllocationFixture(t testing.TB, store *UnifiedStore, sessionID string, days []chatHistoryFixtureDay) *UnifiedStore {
	t.Helper()
	historyState(t, store, sessionID)
	paths, err := store.transcriptPartitionPaths(sessionID)
	require.NoError(t, err)
	for _, path := range paths {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		require.Zero(t, info.Size(), "allocation fixture starts from a genuinely empty canonical session")
	}
	store.stopStatsFlusher()
	require.NoError(t, store.FlushSessionStats(sessionID))
	require.NoError(t, store.Close(), "no live writer or owned flusher remains during bulk fixture I/O")
	archive, err := NewArchiveDayStore(store.BaseDir(), sessionID)
	require.NoError(t, err)
	groups, partitions := 0, 0
	var previous time.Time
	for _, day := range days {
		at, parseErr := time.Parse("2006-01-02", day.Day)
		require.NoError(t, parseErr)
		require.Equal(t, day.Day, at.Format("2006-01-02"))
		require.True(t, previous.IsZero() || at.After(previous), "allocation fixture UTC days strictly increase")
		previous = at
		if len(day.Entries) == 0 {
			continue
		}
		writeCoreAllocationDay(t, archive, day, at)
		groups += len(day.Entries)
		partitions++
	}
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	build, err := PrepareChatHistoryIndex(context.Background(), sessionDir, ChatHistoryIndexBuildOptions{})
	require.NoError(t, err, "real offline reconstruction from actual canonical bytes")
	t.Cleanup(func() { require.NoError(t, build.Close()) })
	summary := build.Summary()
	require.Equal(t, sessionID, summary.SessionID)
	require.Equal(t, int64(groups), summary.SourceGroupCount, "offline index includes EVERY authored canonical group")
	require.Equal(t, partitions, summary.PartitionCount, "real rollover retained every nonempty day")
	require.NoError(t, build.Publish(context.Background()))
	require.NoError(t, build.Close())
	require.NoError(t, VerifyChatHistoryIndex(context.Background(), sessionDir), "published index verifies against canonical bytes")
	reopened, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	reopened.stopStatsFlusher()
	require.NoError(t, reopened.FlushSessionStats(sessionID))
	require.Len(t, historyState(t, reopened, sessionID).RetainedPartitions, partitions)
	return reopened
}

func writeCoreAllocationDay(t testing.TB, archive *ArchiveDayStore, day chatHistoryFixtureDay, at time.Time) {
	t.Helper()
	archive.mu.Lock()
	defer archive.mu.Unlock()
	key, path, err := archive.partitionForAppendLocked(day.Day)
	require.NoError(t, err, "use the production rollover, never rename or invent partitions in this fixture")
	require.Equal(t, day.Day, key)
	var body bytes.Buffer
	for _, entry := range day.Entries {
		entry.ViewMembership = ViewMembershipChat
		if entry.Timestamp.IsZero() {
			entry.Timestamp = at
		}
		line, encodeErr := encodeArchiveLine(ArchiveRecord{TranscriptEntry: entry})
		require.NoError(t, encodeErr, "production validation and encoded-record bound remain in force")
		_, err = body.Write(line)
		require.NoError(t, err)
		require.NoError(t, body.WriteByte('\n'))
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	require.NoError(t, err)
	info, err := file.Stat()
	require.NoError(t, err)
	require.Zero(t, info.Size(), "each actual rolled day starts empty; never overwrite an accepted fixture row")
	_, writeErr := file.Write(body.Bytes())
	syncErr := file.Sync()
	closeErr := file.Close()
	require.NoError(t, writeErr, "bulk canonical fixture write")
	require.NoError(t, syncErr, "canonical day is synced before offline index construction")
	require.NoError(t, closeErr)
	if day.MTime != nil {
		require.NoError(t, os.Chtimes(path, *day.MTime, *day.MTime))
	}
}
