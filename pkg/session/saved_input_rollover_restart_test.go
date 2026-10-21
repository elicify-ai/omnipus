package session

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// PTA-02 / SFH-R2-01: the real rollover writer publishes its day mark before
// appendLineLocked creates the new current file. Reopen at exactly that boundary;
// an absent, never-published current partition must not hide intact old records.
func TestSavedInputInterruptedRolloverRestart(t *testing.T) {
	for _, operation := range []string{"lookup", "retry_save"} {
		t.Run(operation, func(t *testing.T) {
			s := newTestStore(t)
			day := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
			setArchiveDay(s, &day)
			meta, err := s.NewSession(SessionTypeChat, "", "agent")
			require.NoError(t, err)
			const id, body = "saved-input-before-rollover", "helper report consumed before restart"
			source, err := s.SaveChatInput(meta.ID, id, "agent", body)
			require.NoError(t, err)
			_, _, err = s.PlaceSavedInput(context.Background(), meta.ID, source)
			require.NoError(t, err)
			expectedHistory := []providers.Message{{Role: "user", Content: body}}
			require.Equal(t, expectedHistory, s.GetHistory(meta.ID))
			dir := filepath.Join(s.BaseDir(), meta.ID)
			currentPath := filepath.Join(dir, archiveCurrentFile)
			before, err := os.ReadFile(currentPath)
			require.NoError(t, err)
			require.NotEmpty(t, before)
			_, err = os.Stat(s.savedInputIndexPath(meta.ID))
			require.ErrorIs(t, err, fs.ErrNotExist, "SETUP: the consumed input was retired")

			archive, err := s.archiveStore(meta.ID)
			require.NoError(t, err)
			nextDay := day.AddDate(0, 0, 1).Format(transcriptDayLayout)
			archive.mu.Lock()
			key, appendPath, rolloverErr := archive.partitionForAppendLocked(nextDay)
			archive.mu.Unlock()
			require.NoError(t, rolloverErr, "run the actual rename/mark writer, not a fabricated disk layout")
			require.Equal(t, nextDay, key)
			require.Equal(t, currentPath, appendPath)
			rolledPath := filepath.Join(dir, source.PartitionKey+".jsonl")
			rolled, err := os.ReadFile(rolledPath)
			require.NoError(t, err)
			require.Equal(t, before, rolled, "the report and its consumption record were rolled intact")
			mark, err := os.ReadFile(filepath.Join(dir, archiveDayMarkFile))
			require.NoError(t, err)
			require.Equal(t, nextDay+"\n", string(mark))
			_, err = os.Stat(currentPath)
			require.ErrorIs(t, err, fs.ErrNotExist, "stop before appendLineLocked creates today's file")
			require.NoError(t, s.Close())

			// Fresh runtime/store state, over the actual interrupted writer's files.
			reopened, err := NewUnifiedStore(s.BaseDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			_, err = os.Stat(currentPath)
			require.ErrorIs(t, err, fs.ErrNotExist, "reopening must not conceal the boundary by creating a file")
			if operation == "lookup" {
				addr, placed, lookupErr := reopened.SavedChatInput(meta.ID, id)
				require.NoError(t, lookupErr, "an intact rollover archive must remain readable after restart")
				require.Equal(t, source, addr)
				require.True(t, placed, "the consumed report must not become pending again")
			} else {
				addr, saveErr := reopened.SaveChatInput(meta.ID, id, "agent", body)
				require.NoError(t, saveErr, "an idempotent retry must accept the intact rolled records")
				require.Equal(t, source, addr)
			}

			after, err := os.ReadFile(rolledPath)
			require.NoError(t, err)
			require.Equal(t, before, after, "lookup/retry must neither lose nor duplicate a record")
			require.Equal(t, expectedHistory, reopened.GetHistory(meta.ID))
			pending, err := reopened.PendingChatInputs(context.Background(), meta.ID)
			require.NoError(t, err)
			require.Empty(t, pending, "restart must not revive the already-consumed input")
			_, err = os.Stat(reopened.savedInputIndexPath(meta.ID))
			require.ErrorIs(t, err, fs.ErrNotExist, "no invented pending index")
			_, err = os.Stat(currentPath)
			require.ErrorIs(t, err, fs.ErrNotExist, "read/idempotent retry must not manufacture a new partition")
		})
	}
}
