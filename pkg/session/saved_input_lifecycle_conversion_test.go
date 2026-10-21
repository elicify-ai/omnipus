package session

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func lifecycleMoveAgentStore(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	const id, body = "saved-input-move-held", "held report survives whole-store transfer"
	addr, err := store.SaveChatInput(sid, id, "mia", body)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	home := t.TempDir()
	shared := filepath.Join(home, "sessions")
	require.NoError(t, CutoverSavedChatsAtBootWithAgentStores(shared, []string{store.BaseDir()}))
	_, err = os.Stat(filepath.Join(store.BaseDir(), sid))
	require.ErrorIs(t, err, fs.ErrNotExist, "real cutover moved the source directory")
	moved := lifecycleOpenStore(t, shared, home)
	lookedUp, placed, err := moved.SavedChatInput(sid, id)
	require.NoError(t, err)
	require.Equal(t, addr, lookedUp, "moving the whole bundle preserves addressed offsets")
	require.False(t, placed)
	pending, err := moved.PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, body, pending[0].Content)
	lifecycleAssertFreshHelperReport(t, moved, sid, "move-agent-store")
}

func lifecycleNormalizeChat(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "sessions")
	const sid = "legacy-chat-lifecycle"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	convSeedLegacyChatDir(t, base, sid, "mia", "jim", "Retained chat", "", created, created)
	convSeedLegacyTranscript(t, base, sid, "legacy-message", "retained legacy chat", "mia", created)
	require.NoError(t, CutoverSavedChatsAtBoot(base))
	store := lifecycleOpenStore(t, base, home)
	meta, err := store.GetMeta(sid)
	require.NoError(t, err)
	require.Equal(t, SessionTypeChat, meta.Type)
	require.Equal(t, "mia", meta.AgentID)
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "retained legacy chat", entries[0].Content)
	lifecycleAssertFreshHelperReport(t, store, sid, "normalize-chat")
}

func lifecycleConvertModelArchive(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "sessions")
	const sid = "legacy-model-lifecycle"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	convSeedLegacyChatDir(t, base, sid, "mia", "", "Retained model", "chat", created, created)
	const raw = "{\"role\":\"user\",\"content\":\"evicted legacy message\",\"ts\":1}\n" +
		"{\"role\":\"user\",\"content\":\"retained legacy message\",\"ts\":2}\n"
	convSeedContext(t, base, sid, raw, `{"key":"legacy-model-lifecycle","skip":1,"count":2}`)
	require.NoError(t, CutoverSavedChatsAtBoot(base))
	_, err := os.Stat(filepath.Join(base, convContextDir, sid+".jsonl"))
	require.ErrorIs(t, err, fs.ErrNotExist, "completed conversion retired the legacy model source")
	store := lifecycleOpenStore(t, base, home)
	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.Equal(t, 1, view.State.Skip)
	require.Len(t, view.Live, 1)
	require.Equal(t, "retained legacy message", view.Live[0].Message.Content)
	lifecycleAssertFreshHelperReport(t, store, sid, "convert-model")
}

func lifecycleConvertFlatSource(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, "sessions")
	const sid = "legacy-flat-lifecycle"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	convSeedLegacyChatDir(t, base, sid, "mia", "", "Flat source", "chat", created, created)
	raw := convLegacyJSONL("retained flat source bytes")
	convWriteBytes(t, filepath.Join(base, sid+".jsonl"), raw)
	require.NoError(t, CutoverSavedChatsAtBoot(base))
	_, err := os.Stat(filepath.Join(base, sid+".jsonl"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	retained, err := os.ReadFile(filepath.Join(base, sid, "context.jsonl"))
	require.NoError(t, err)
	require.Equal(t, raw, retained)
	// This case certifies new-report continuation only. It does not certify a
	// runtime reader for the retired context filename; that inventory note stays open.
	store := lifecycleOpenStore(t, base, home)
	lifecycleAssertFreshHelperReport(t, store, sid, "convert-flat")
}

func lifecycleOpenStore(t *testing.T, base, home string) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStoreWithHome(base, home)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func lifecycleSettleToolCall(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	addr := appendCall(t, store, sid, "lifecycle-call", "pending", time.Now().UTC())
	before := partitionBytes(t, store, sid)
	_, err := store.SettleToolCall(sid, addr, "pending", ToolCall{
		ID: "lifecycle-call", Tool: "web_search", Status: "success", Result: map[string]any{"text": "completed result"},
	})
	require.NoError(t, err)
	require.Equal(t, "success", toolCallOf(t, store, sid, "lifecycle-call").Status)
	requirePrefixOf(t, before, partitionBytes(t, store, sid))
	lifecycleAssertFreshHelperReport(t, store, sid, "settle-call")
}

func lifecycleProjectToolCall(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	addr := appendCall(t, store, sid, "lifecycle-call", "success", time.Now().UTC())
	before := partitionBytes(t, store, sid)
	_, err := store.ProjectToolCalls(sid, []ToolCallProjectionEdit{{
		Target: addr, ToolCallID: "lifecycle-call", ContentState: "capped", Text: "short result",
	}})
	require.NoError(t, err)
	require.Equal(t, "short result", toolCallOf(t, store, sid, "lifecycle-call").Result["text"])
	requirePrefixOf(t, before, partitionBytes(t, store, sid))
	lifecycleAssertFreshHelperReport(t, store, sid, "project-call")
}

func lifecycleRetractToolCall(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	addr := appendCall(t, store, sid, "lifecycle-call", "success", time.Now().UTC())
	effect, err := store.ProjectToolCalls(sid, []ToolCallProjectionEdit{{
		Target: addr, ToolCallID: "lifecycle-call", ContentState: "emptied", Text: "hidden",
	}})
	require.NoError(t, err)
	before := partitionBytes(t, store, sid)
	_, err = store.RetractToolCallEffects(sid, []ArchiveAddress{effect})
	require.NoError(t, err)
	require.Equal(t, "original lifecycle-call", toolCallOf(t, store, sid, "lifecycle-call").Result["text"])
	requirePrefixOf(t, before, partitionBytes(t, store, sid))
	lifecycleAssertFreshHelperReport(t, store, sid, "retract-projection")
}
