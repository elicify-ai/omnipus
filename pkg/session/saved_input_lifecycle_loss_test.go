package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSavedInputStorageLifecycleLossMatrix(t *testing.T) {
	t.Run("live_slot_after_retention", func(t *testing.T) {
		store, sid, archive, _ := wiredModelRetentionSession(t)
		removed, err := store.RetentionSweep(7)
		require.NoError(t, err)
		require.Equal(t, 1, removed)
		require.NoError(t, os.Remove(filepath.Join(archive.dir(), archiveCurrentFile)))
		lifecycleAssertNewReportRefused(t, store, sid, archive, "live-slot")
	})
	t.Run("required_anchor_before_skip", func(t *testing.T) {
		store, sid, archive, rolled := wiredModelRetentionSession(t)
		view, err := store.WindowView(context.Background(), sid)
		require.NoError(t, err)
		after := view.State.Clone()
		after.Skip = 3
		anchor := 2
		after.AnchorLine = &anchor
		require.NoError(t, store.CommitWindow(context.Background(), sid, view.State, after))
		require.NoError(t, os.Remove(rolled), "not retention: the anchor is still explicitly required")
		lifecycleAssertNewReportRefused(t, store, sid, archive, "required-anchor")
	})
	t.Run("live_reference_requires_older_source", func(t *testing.T) {
		store := newUnifiedStoreForTest(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		archive, err := store.backend.store(meta.ID)
		require.NoError(t, err)
		atDay(archive, "2026-04-01")
		lifecycleAppendUser(t, store, meta.ID, "old conversation")
		source, err := store.SaveChatInput(meta.ID, "saved-input-required-source", "mia", "live report in older file")
		require.NoError(t, err)
		atDay(archive, "2026-04-02")
		_, _, err = store.PlaceSavedInput(context.Background(), meta.ID, source)
		require.NoError(t, err)
		atDay(archive, "2026-04-10")
		lifecycleAppendUser(t, store, meta.ID, "retained conversation")
		view, err := store.WindowView(context.Background(), meta.ID)
		require.NoError(t, err)
		after := view.State.Clone()
		after.Skip = 1
		require.NoError(t, store.CommitWindow(context.Background(), meta.ID, view.State, after))
		require.NoError(t, os.Remove(filepath.Join(archive.dir(), "2026-04-01.jsonl")))
		lifecycleAssertNewReportRefused(t, store, meta.ID, archive, "required-older-source")
	})
	t.Run("pending_source_is_not_expired_by_a_trim", func(t *testing.T) {
		store := newUnifiedStoreForTest(t)
		meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		archive, err := store.backend.store(meta.ID)
		require.NoError(t, err)
		atDay(archive, "2026-04-01")
		_, err = store.SaveChatInput(meta.ID, "saved-input-still-required", "mia", "held report must not disappear")
		require.NoError(t, err)
		atDay(archive, "2026-04-10")
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, TranscriptEntry{
			ID: "current-chat-only", Role: "system", Type: EntryTypeSystem, Content: "current chat note",
			AgentID: "mia", Timestamp: time.Now().UTC(), ViewMembership: ViewMembershipChat,
		}))
		require.NoError(t, os.Remove(filepath.Join(archive.dir(), "2026-04-01.jsonl")))
		lifecycleAssertNewReportRefused(t, store, meta.ID, archive, "required-pending-source")
	})
	t.Run("unreadable_window_meta", func(t *testing.T) {
		store, sid, archive, _ := wiredModelRetentionSession(t)
		require.NoError(t, os.WriteFile(filepath.Join(archive.dir(), archiveBackendMetaFile), []byte("{"), 0o600))
		lifecycleAssertNewReportRefused(t, store, sid, archive, "invalid-window-meta")
	})
}

func lifecycleAssertNewReportRefused(t *testing.T, store *UnifiedStore, sid string, archive *ArchiveDayStore, suffix string) {
	t.Helper()
	before := partitionBytes(t, store, sid)
	n, err := archive.OrdinalCount()
	require.NoError(t, err)
	addr, err := store.SaveChatInput(sid, "saved-input-loss-"+suffix, "mia", "must not hide required live loss")
	require.Error(t, err, "a live required record or state is gone: never certify an empty/unconsumed history")
	require.Equal(t, ArchiveAddress{}, addr)
	require.Equal(t, before, partitionBytes(t, store, sid), "refusal does not replace the missing archive or append a new payload")
	after, countErr := archive.OrdinalCount()
	require.NoError(t, countErr)
	require.Equal(t, n, after, "required loss does not truncate publication history")
}
