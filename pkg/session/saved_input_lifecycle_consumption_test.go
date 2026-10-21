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

func lifecycleConsumedPlacementExpires(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	archive, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	atDay(archive, "2026-04-01")
	const id, body = "saved-input-consumed-before-expiry", "already delivered helper report"
	addr, err := store.SaveChatInput(meta.ID, id, "mia", body)
	require.NoError(t, err)
	atDay(archive, "2026-04-02")
	_, _, err = store.PlaceSavedInput(context.Background(), meta.ID, addr)
	require.NoError(t, err)
	atDay(archive, "2026-04-10")
	lifecycleAppendUser(t, store, meta.ID, "retained conversation")
	placementPath := filepath.Join(archive.dir(), "2026-04-02.jsonl")
	ageFileTo(t, placementPath, time.Date(2026, 4, 2, 0, 0, 0, 0, time.UTC))
	// File-modification age, not the filename's day, owns retention. The older
	// source is deliberately younger by mtime than the placement partition.
	ageFileTo(t, filepath.Join(archive.dir(), "2026-04-01.jsonl"), time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	_, err = os.Stat(placementPath)
	require.ErrorIs(t, err, fs.ErrNotExist)
	rec, err := archive.ReadAt(addr)
	require.NoError(t, err)
	require.Equal(t, body, rec.Content)
	view, err := store.WindowView(context.Background(), meta.ID)
	require.NoError(t, err)
	require.Equal(t, 1, view.State.Skip)
	require.Len(t, view.Live, 1)
	lifecycleAssertFreshHelperReport(t, store, meta.ID, "consumed-placement-expired")
	lookedUp, placed, err := store.SavedChatInput(meta.ID, id)
	require.NoError(t, err)
	require.Equal(t, addr, lookedUp)
	require.True(t, placed, "retained ordinal Source metadata still proves prior consumption")
	retried, err := store.SaveChatInput(meta.ID, id, "mia", body)
	require.NoError(t, err)
	require.Equal(t, addr, retried)
	pending, err := store.PendingChatInputs(context.Background(), meta.ID)
	require.NoError(t, err)
	require.Empty(t, pending, "expiry is not permission to revive a consumed input")
	_, _, err = store.PlaceSavedInput(context.Background(), meta.ID, addr)
	require.ErrorIs(t, err, ErrAlreadyConsumed, "direct placement also preserves once-only consumption")
}
