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

func (f *archiveMatrixFixture) reopenAndCheck(t *testing.T, operation string, probePublished, wantLoss bool) {
	t.Helper()
	diskBefore := archiveMatrixSnapshot(t, f.dir())
	require.NoError(t, f.store.Close())
	reopened, err := NewUnifiedStore(f.store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	day, err := time.Parse(transcriptDayLayout, f.nextDay)
	require.NoError(t, err)
	setArchiveDay(reopened, &day) // install before archiveStore captures the clock
	archive, err := reopened.archiveStore(f.sid)
	require.NoError(t, err)
	var operationErr error
	if operation == "lookup" {
		addr, placed, lookupErr := reopened.SavedChatInput(f.sid, archiveMatrixInputID)
		operationErr = lookupErr
		if !wantLoss {
			require.NoError(t, lookupErr, "intact records must survive every real writer cut and atomic equivalent")
			require.Equal(t, f.source, addr)
			require.True(t, placed, "an old consumed report must not be revived")
		}
	} else {
		addr, retryErr := reopened.SaveChatInput(f.sid, archiveMatrixInputID, "agent", archiveMatrixBody)
		operationErr = retryErr
		if !wantLoss {
			require.NoError(t, retryErr, "idempotent retry must resolve actual surviving record locations")
			require.Equal(t, f.source, addr)
		}
	}
	if wantLoss {
		require.Error(t, operationErr, "required content loss/corruption/ambiguity must not become empty or unconsumed success")
		require.Equal(t, diskBefore, archiveMatrixSnapshot(t, f.dir()), "a refused read/retry must not rewrite surviving evidence")
		_, err = os.Stat(reopened.savedInputIndexPath(f.sid))
		require.ErrorIs(t, err, fs.ErrNotExist, "do not queue the old delivered input again")
		return
	}
	require.NoError(t, operationErr)
	for _, record := range []struct {
		addr ArchiveAddress
		raw  []byte
	}{{f.source, f.sourceRaw}, {f.placement, f.placedRaw}} {
		raw, _, readErr := archive.ReadAtRaw(record.addr)
		require.NoError(t, readErr, "acceptance must support real subsequent stable-address reads, not merely bypass the guard")
		require.Equal(t, record.raw, raw)
	}
	count, err := archive.OrdinalCount()
	require.NoError(t, err)
	wantCount := 1
	if probePublished {
		wantCount++
	}
	require.Equal(t, wantCount, count, "only a complete framed publication row is adopted on reopening")
	pending, err := reopened.PendingChatInputs(context.Background(), f.sid)
	require.NoError(t, err)
	require.Empty(t, pending)
	_, err = os.Stat(reopened.savedInputIndexPath(f.sid))
	require.ErrorIs(t, err, fs.ErrNotExist, "no consumed input was made pending")
	require.Equal(t, diskBefore, archiveMatrixSnapshot(t, f.dir()), "lookup/retry preserves every surviving archive byte and publication row")
	n := 0
	for _, message := range reopened.GetHistory(f.sid) {
		if message.Content == archiveMatrixBody {
			n++
		}
	}
	require.Equal(t, 1, n, "the consumed report appears exactly once in real model history")

	// A subsequent real write must complete/restart rollover without re-pointing
	// the old addresses. This catches a read-only bypass of the guard/resolver.
	_, err = reopened.SaveChatInput(f.sid, "saved-input-legitimate-next", "agent", "new report after recovery")
	require.NoError(t, err)
	addr, placed, err := reopened.SavedChatInput(f.sid, archiveMatrixInputID)
	require.NoError(t, err)
	require.Equal(t, f.source, addr)
	require.True(t, placed)
	for _, record := range []struct {
		addr ArchiveAddress
		raw  []byte
	}{{f.source, f.sourceRaw}, {f.placement, f.placedRaw}} {
		raw, _, readErr := archive.ReadAtRaw(record.addr)
		require.NoError(t, readErr)
		require.Equal(t, record.raw, raw)
	}
	pending, err = reopened.PendingChatInputs(context.Background(), f.sid)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "saved-input-legitimate-next", pending[0].ID, "only the genuinely new report may be pending")
}

func archiveMatrixMissingSourceControl(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	_, err = s.SaveChatInput(meta.ID, "saved-input-missing-source", "agent", "held report must not vanish silently")
	require.NoError(t, err)
	indexPath := s.savedInputIndexPath(meta.ID)
	before, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(s.BaseDir(), meta.ID, archiveCurrentFile)))
	require.NoError(t, s.Close())
	reopened, err := NewUnifiedStore(s.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	pending, readErr := reopened.PendingChatInputs(context.Background(), meta.ID)
	require.ErrorIs(t, readErr, fs.ErrNotExist, "SFH-3: missing indexed content is an error")
	require.Nil(t, pending)
	after, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
