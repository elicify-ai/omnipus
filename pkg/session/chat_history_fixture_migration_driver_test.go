package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The same existing owned-root/session/index checks apply to these additional
// fixture actions. They never adopt an unindexed archive or truncate content.
func appendChatHistoryFixtureEntries(t testing.TB, store *UnifiedStore, sid string, days []chatHistoryFixtureDay, empty bool) []ArchiveAddress {
	t.Helper()
	_, err := store.GetMeta(sid)
	require.NoError(t, err, "append fixture addresses an existing identity")
	state, err := store.readChatHistoryIndexState(context.Background(), sid)
	require.NoError(t, err, "append fixture requires a healthy existing index")
	require.Equal(t, ChatHistoryIndexFormatVersion, state.FormatVersion)
	require.NotEmpty(t, state.IndexGeneration)
	if empty {
		paths, err := store.transcriptPartitionPaths(sid)
		require.NoError(t, err)
		for _, path := range paths {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Zero(t, info.Size(), "initial canonical seed must not overwrite existing records")
		}
	}
	archive, err := store.archiveStore(sid)
	require.NoError(t, err)
	oldStoreClock, oldArchiveClock := store.archiveNow, archive.now
	defer func() { store.archiveNow = oldStoreClock; archive.now = oldArchiveClock }()
	var previous time.Time
	var addresses []ArchiveAddress
	for _, day := range days {
		at, err := time.Parse("2006-01-02", day.Day)
		require.NoError(t, err)
		require.Equal(t, day.Day, at.Format("2006-01-02"))
		require.True(t, previous.IsZero() || at.After(previous), "append fixture days strictly increase")
		previous = at
		store.archiveNow = func() time.Time { return at }
		archive.now = store.archiveNow
		for _, entry := range day.Entries {
			address, err := store.AppendTranscriptAddressed(sid, entry)
			require.NoError(t, err, "actual canonical append must maintain the existing history index")
			require.Equal(t, day.Day, address.PartitionKey, "never substitute a different append day")
			addresses = append(addresses, address)
		}
	}
	_, err = store.readChatHistoryIndexState(context.Background(), sid)
	require.NoError(t, err, "canonical append must leave history available")
	return addresses
}

func validateChatHistoryFixtureAge(t testing.TB, store *UnifiedStore, plan chatHistoryFixturePlan) {
	t.Helper()
	require.NotNil(t, plan.AgedAt, "aged fixture requires an explicit historical acceptance time")
	require.False(t, plan.AgedAt.IsZero())
	require.True(t, plan.AgedAt.Before(time.Now().UTC()), "aged fixture must actually be in the past")
	require.GreaterOrEqual(t, len(plan.Days), 2, "aged fixture exercises an actual rolled old-day partition")
	require.NotEmpty(t, plan.Title)
	meta, err := store.GetMeta(plan.SessionID)
	require.NoError(t, err)
	require.Equal(t, meta.AgentID, plan.AgentID, "fixture cannot change the REST-created owner agent")
	for _, day := range plan.Days {
		require.LessOrEqual(t, day.Day, plan.AgedAt.UTC().Format("2006-01-02"), "no current-day data substitutes for the aged archive")
	}
}

func ageChatHistoryFixtureMeta(t testing.TB, store *UnifiedStore, plan chatHistoryFixturePlan) {
	t.Helper()
	h := store.lockSession(plan.SessionID)
	meta, err := store.readMetaLocked(plan.SessionID)
	if err == nil {
		meta.CreatedAt = *plan.AgedAt
		meta.UpdatedAt = *plan.AgedAt
		meta.Title = plan.Title
		err = store.writeMetaLocked(plan.SessionID, meta)
		if err == nil {
			// Runtime metadata updates are deliberately monotonic. This stopped,
			// test-owned store stages a past identity: match its cache to the
			// old values just written so Close cannot flush today's timestamp.
			store.cacheMu.Lock()
			store.metaCache[plan.SessionID] = meta.Clone()
			store.cacheMu.Unlock()
		}
	}
	h.Unlock()
	require.NoError(t, err, "aged metadata goes through the actual session writer")
	_, err = store.readChatHistoryIndexState(context.Background(), plan.SessionID)
	require.NoError(t, err, "aged metadata cannot excuse an unavailable canonical index")
}

func ageChatHistoryFixtureFiles(t testing.TB, directory string, at time.Time) {
	t.Helper()
	var directories []string
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		require.False(t, entry.Type()&os.ModeSymlink != 0, "aging cannot follow a symlink")
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		return os.Chtimes(path, at, at)
	}))
	for i := len(directories) - 1; i >= 0; i-- {
		require.NoError(t, os.Chtimes(directories[i], at, at))
	}
}
