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

func lifecycleExpiredPending(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	archive, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	atDay(archive, "2026-04-01")
	_, err = store.SaveChatInput(meta.ID, "saved-input-aged-held", "mia", "held report in aged partition")
	require.NoError(t, err)
	atDay(archive, "2026-04-10")
	lifecycleAppendUser(t, store, meta.ID, "retained live conversation")
	ageFileTo(t, filepath.Join(archive.dir(), "2026-04-01.jsonl"), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	_, err = store.RetentionSweep(7)
	require.NoError(t, err)
	lifecycleAssertFreshHelperReport(t, store, meta.ID, "retention-aged-pending")
}

func lifecycleLivePending(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	const id, body = "saved-input-still-pending", "live held helper report"
	addr, err := store.SaveChatInput(sid, id, "mia", body)
	require.NoError(t, err)
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	pending, err := store.PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, id, pending[0].ID)
	require.Equal(t, body, pending[0].Content)
	lookedUp, placed, err := store.SavedChatInput(sid, id)
	require.NoError(t, err)
	require.Equal(t, addr, lookedUp)
	require.False(t, placed)
	lifecycleAssertFreshHelperReport(t, store, sid, "retention-live-pending")
	pending, err = store.PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, pending, 1, "placing a different new report does not retire the held input")
	require.Equal(t, body, pending[0].Content)
}

func lifecycleAllModelExpired(t *testing.T) {
	store, sid, archive, _ := wiredModelRetentionSession(t)
	atDay(archive, "2026-04-20")
	require.NoError(t, store.AppendTranscriptStrict(sid, TranscriptEntry{
		ID: "chat-only-current", Type: EntryTypeSystem, Role: "system", Content: "current chat notice",
		AgentID: "mia", Timestamp: time.Now().UTC(), ViewMembership: ViewMembershipChat,
	}))
	ageFileTo(t, filepath.Join(archive.dir(), "2026-04-10.jsonl"), time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 2, removed)
	n, err := archive.OrdinalCount()
	require.NoError(t, err)
	require.Equal(t, 4, n)
	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.Empty(t, view.Live)
	require.Equal(t, 4, view.State.Skip)
	require.Equal(t, 4, view.State.ClearFloor)
	lifecycleAssertFreshHelperReport(t, store, sid, "all-model-expired")
}

func lifecycleRetentionDisabled(t *testing.T) {
	store, sid, _, rolled := wiredModelRetentionSession(t)
	removed, err := store.RetentionSweep(0)
	require.NoError(t, err)
	require.Zero(t, removed)
	_, err = os.Stat(rolled)
	require.NoError(t, err)
	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, view.Live, 4)
	lifecycleAssertFreshHelperReport(t, store, sid, "retention-disabled")
}

func lifecycleRetentionBoundary(t *testing.T) {
	store, sid, _, rolled := wiredModelRetentionSession(t)
	ageFileTo(t, rolled, time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Zero(t, removed, "mtime equal to the cutoff survives")
	_, err = os.Stat(rolled)
	require.NoError(t, err)
	lifecycleAssertFreshHelperReport(t, store, sid, "retention-cutoff-equal")
}

func lifecycleRetainedAnchorExpires(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	ctx := context.Background()
	view, err := store.WindowView(ctx, sid)
	require.NoError(t, err)
	after := view.State.Clone()
	after.Skip = 3
	anchor := 2
	after.AnchorLine = &anchor
	require.NoError(t, store.CommitWindow(ctx, sid, view.State, after))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	view, err = store.WindowView(ctx, sid)
	require.NoError(t, err)
	require.Nil(t, view.State.AnchorLine, "real expiry retires a gone anchor explicitly")
	require.Len(t, view.Live, 1)
	lifecycleAssertFreshHelperReport(t, store, sid, "expired-anchor")
}

func lifecycleDeleteAndNew(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	_, err := store.SaveChatInput(sid, "saved-input-before-delete", "mia", "pending before delete")
	require.NoError(t, err)
	require.NoError(t, store.DeleteSession(sid))
	_, err = os.Stat(filepath.Join(store.BaseDir(), sid))
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = store.SaveChatInput(sid, "saved-input-deleted-refused", "mia", "do not resurrect a deleted recipient")
	require.Error(t, err)
	_, _, err = store.SavedChatInput(sid, "saved-input-before-delete")
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(store.BaseDir(), sid))
	require.ErrorIs(t, err, fs.ErrNotExist, "a refused save creates no replacement recipient")
	fresh, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NotEqual(t, sid, fresh.ID)
	lifecycleAssertFreshHelperReport(t, store, fresh.ID, "deletion-new-recipient")
}

func lifecycleRetireLastPending(t *testing.T) {
	store, sid, archive, _ := wiredModelRetentionSession(t)
	const id, body = "saved-input-retire-old", "retired held helper payload"
	addr, err := store.SaveChatInput(sid, id, "mia", body)
	require.NoError(t, err)
	require.NoError(t, store.RetireSavedInput(sid, id))
	_, err = os.Stat(store.savedInputIndexPath(sid))
	require.ErrorIs(t, err, fs.ErrNotExist, "normal last-entry retirement unlinks the sidecar")
	rec, err := archive.ReadAt(addr)
	require.NoError(t, err)
	require.Equal(t, body, rec.Content, "retirement is not payload deletion")
	lifecycleAssertFreshHelperReport(t, store, sid, "retire-last-pending")
}
