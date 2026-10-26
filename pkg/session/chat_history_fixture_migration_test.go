package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Test-only migration contract from the RED-E2E Q1=A dispatch: arbitrary
// transcript/effect fixtures use the canonical writer; aged fixtures stay old.
func TestChatHistoryFixture_LegacyCanonicalMigration(t *testing.T) {
	t.Run("append preserves authored records and indexed baseline", func(t *testing.T) {
		root, sid := migrationFixtureRoot(t)
		at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
		first := TranscriptEntry{ID: "original", Type: EntryTypeMessage, Role: "assistant", Content: "original body", AgentID: "mia", Timestamp: at}
		runMigrationFixtureDriver(t, root, map[string]any{
			"action": "seed_days", "session_id": sid,
			"days": []chatHistoryFixtureDay{{Day: "2026-10-17", Entries: []TranscriptEntry{first}}},
		})
		second := TranscriptEntry{ID: "appended", Type: EntryTypeMessage, Role: "assistant", Content: "appended body", AgentID: "mia", Timestamp: at.Add(time.Minute),
			ToolCalls: []ToolCall{{ID: "authored-call", Tool: "read_file", Status: "success", Parameters: map[string]any{"path": "authored.txt"}, Result: map[string]any{"content": "authored result"}}}}
		runMigrationFixtureDriver(t, root, map[string]any{
			"action": "append_entries", "session_id": sid,
			"days": []chatHistoryFixtureDay{{Day: "2026-10-17", Entries: []TranscriptEntry{second}}},
		})
		store, err := NewUnifiedStoreWithHome(filepath.Join(root, "sessions"), root)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		entries, err := store.ReadTranscript(sid)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		require.Equal(t, []string{"original", "appended"}, []string{entries[0].ID, entries[1].ID})
		require.Equal(t, first.Content, entries[0].Content)
		require.Equal(t, second.Content, entries[1].Content)
		require.Equal(t, second.ToolCalls, entries[1].ToolCalls)
		state, err := store.readChatHistoryIndexState(context.Background(), sid)
		require.NoError(t, err, "canonical fixture must leave a readable index, not hide unavailable history")
		require.Equal(t, ChatHistoryIndexFormatVersion, state.FormatVersion)
		require.NotEmpty(t, state.IndexGeneration)
	})
	t.Run("aged identity and rollover never substitute current day data", func(t *testing.T) {
		root, sid := migrationFixtureRoot(t)
		at := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
		older := at.AddDate(0, 0, -1)
		days := []chatHistoryFixtureDay{
			{Day: older.Format("2006-01-02"), MTime: &at, Entries: []TranscriptEntry{{ID: "aged-user", Role: "user", Content: "aged user body", AgentID: "mia", Timestamp: at}}},
			{Day: at.Format("2006-01-02"), MTime: &at, Entries: []TranscriptEntry{{ID: "aged-assistant", Role: "assistant", Content: "aged assistant body", AgentID: "mia", Timestamp: at}}},
		}
		runMigrationFixtureDriver(t, root, map[string]any{
			"action": "seed_aged", "session_id": sid, "agent_id": "mia", "title": "Aged session (7d ago)", "aged_at": at, "days": days,
		})
		store, err := NewUnifiedStoreWithHome(filepath.Join(root, "sessions"), root)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, store.Close()) })
		meta, err := store.GetMeta(sid)
		require.NoError(t, err)
		require.Equal(t, at, meta.CreatedAt)
		require.Equal(t, at, meta.UpdatedAt)
		require.Equal(t, "Aged session (7d ago)", meta.Title)
		entries, err := store.ReadTranscript(sid)
		require.NoError(t, err)
		require.Len(t, entries, 2)
		require.Equal(t, []string{"aged-user", "aged-assistant"}, []string{entries[0].ID, entries[1].ID})
		require.Equal(t, at, entries[0].Timestamp)
		require.Equal(t, at, entries[1].Timestamp)
		for _, name := range []string{older.Format("2006-01-02") + ".jsonl", "transcript.jsonl"} {
			info, statErr := os.Stat(filepath.Join(root, "sessions", sid, name))
			require.NoError(t, statErr, "actual canonical rollover must preserve an old-day filename")
			require.Equal(t, at.UnixMilli(), info.ModTime().UnixMilli())
		}
		mark, err := os.ReadFile(filepath.Join(root, "sessions", sid, "transcript.day"))
		require.NoError(t, err)
		require.Equal(t, "2026-07-01\n", string(mark), "no current-day append replaces the authored old archive")
		_, err = store.readChatHistoryIndexState(context.Background(), sid)
		require.NoError(t, err, "the aged canonical fixture must be available before retention")
	})
}

func migrationFixtureRoot(t *testing.T) (string, string) {
	t.Helper()
	root, err := os.MkdirTemp("", "omnipus-e2e-gwproc-migration-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(root)) })
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.json"), []byte("{\"version\":1}\n"), 0o600))
	store, err := NewUnifiedStoreWithHome(filepath.Join(root, "sessions"), root)
	require.NoError(t, err)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, store.Close())
	return root, meta.ID
}

func runMigrationFixtureDriver(t *testing.T, root string, fields map[string]any) {
	t.Helper()
	fields["schema_version"] = 1
	fields["data_root"] = root
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "plan.json")
	require.NoError(t, os.WriteFile(file, data, 0o600))
	t.Setenv("OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN", file)
	TestChatHistoryFixtureDriver(t)
}
