package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func gate0ExpiredEffectFixture(t *testing.T, kind string) (*UnifiedStore, string, string, ArchiveAddress) {
	t.Helper()
	home := t.TempDir()
	s, err := NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	meta, err := s.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID
	owner := TranscriptEntry{ID: "expired-owner", Type: EntryTypeToolCall, AgentID: "mia", ViewMembership: ViewMembershipChat,
		ToolCalls: []ToolCall{{ID: "expired-call", Tool: "read_file", Status: "pending", Parameters: map[string]any{}}}}
	addresses := buildChatHistoryDayFixture(t, s, sid, []chatHistoryFixtureDay{{Day: "2026-01-01", Entries: []TranscriptEntry{owner}}})
	a, err := s.archiveStore(sid)
	require.NoError(t, err)
	oldStoreClock, oldArchiveClock := s.archiveNow, a.now
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.archiveNow = func() time.Time { return day }
	a.now = s.archiveNow
	defer func() { s.archiveNow, a.now = oldStoreClock, oldArchiveClock }()
	var oldProjection ArchiveAddress
	if kind == "retract" {
		oldProjection, err = s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: addresses[0], ToolCallID: "expired-call", ContentState: "capped", Text: "old projection"}})
		require.NoError(t, err)
		require.Equal(t, "2026-01-01", oldProjection.PartitionKey, "actual effect target belongs to the partition about to expire")
	}
	day = day.AddDate(0, 0, 1)
	retained, err := s.AppendTranscriptAddressed(sid, TranscriptEntry{ID: "retained-one", Role: "user", Content: "retained canonical source",
		AgentID: "mia", ViewMembership: ViewMembershipChat, Timestamp: day})
	require.NoError(t, err)
	var effect ArchiveAddress
	switch kind {
	case "settle":
		effect, err = s.SettleToolCall(sid, addresses[0], "pending", ToolCall{ID: "expired-call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "real newer settlement"}})
	case "project":
		effect, err = s.ProjectToolCalls(sid, []ToolCallProjectionEdit{{Target: addresses[0], ToolCallID: "expired-call", ContentState: "capped", Text: "real newer projection"}})
	case "retract":
		effect, err = s.RetractToolCallEffects(sid, []ArchiveAddress{oldProjection})
	default:
		t.Fatalf("unknown F3 effect fixture %s", kind)
	}
	require.NoError(t, err)
	require.Equal(t, "2026-01-02", effect.PartitionKey, "newer actual effect survives old partition expiry")
	require.Len(t, historyRead(t, s, sid, historyOptions(100)).Entries, 2, "healthy actual indexed owner/effect path before retention")
	before := historyState(t, s, sid)
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	oldPath := filepath.Join(s.BaseDir(), sid, "2026-01-01.jsonl")
	require.NoError(t, os.Chtimes(oldPath, old, old))
	removed, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, removed, "real sweep removes exactly the old rolled owner/effect partition")
	require.NoFileExists(t, oldPath)
	after := historyState(t, s, sid)
	require.NotEqual(t, before.RetentionGeneration, after.RetentionGeneration)
	require.NotContains(t, after.RetainedPartitions, "2026-01-01")
	_, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, historyExpectedPosition(sid, addresses[0]))
	require.NoError(t, err)
	require.True(t, found, "expiry is proven by real validated index/tombstone facts, not inferred from missing target")
	page := historyRead(t, s, sid, historyOptions(100))
	require.Equal(t, []string{"retained-one"}, historyIDs(page), "ordinary indexed paging already handles the proven expired target, no orphan result")
	require.Equal(t, historyExpectedPosition(sid, retained), page.Entries[0].Position)
	require.NoError(t, s.Close())
	return s, sid, home, retained
}
