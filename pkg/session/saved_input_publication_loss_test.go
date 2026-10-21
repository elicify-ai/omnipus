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

// SFH-R3-01 / PTA-03: a retained ordinal Slot proves a consumption record was
// published, even when its Source survives in yesterday's rolled partition.
// The pending index is independent publication evidence for chat-only records.
func TestSavedInputMissingPublishedCurrent(t *testing.T) {
	for _, evidence := range []string{"ordinal", "pending_index"} {
		for _, operation := range []string{"lookup", "retry_save"} {
			t.Run(evidence+"_"+operation, func(t *testing.T) {
				s := newTestStore(t)
				day := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
				setArchiveDay(s, &day)
				meta, err := s.NewSession(SessionTypeChat, "", "agent")
				require.NoError(t, err)
				const id, body = "saved-input-already-delivered", "helper report already delivered once"
				source, err := s.SaveChatInput(meta.ID, id, "agent", body)
				require.NoError(t, err)
				if evidence == "ordinal" {
					day = day.AddDate(0, 0, 1)
				}
				_, _, err = s.PlaceSavedInput(context.Background(), meta.ID, source)
				require.NoError(t, err)
				archive, err := s.archiveStore(meta.ID)
				require.NoError(t, err)
				rows, err := archive.readOrdinalRows(0, 1)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Equal(t, &source, rows[0].Source, "SETUP: the publication row names the surviving payload")
				_, err = os.Stat(s.savedInputIndexPath(meta.ID))
				require.ErrorIs(t, err, fs.ErrNotExist, "SETUP: the already-consumed report was retired")
				if evidence == "pending_index" {
					day = day.AddDate(0, 0, 1)
					held, saveErr := s.SaveChatInput(meta.ID, "saved-input-other-held", "agent", "other published held report")
					require.NoError(t, saveErr)
					require.Equal(t, day.Format(transcriptDayLayout), held.PartitionKey)
					require.Equal(t, source.PartitionKey, rows[0].Slot.PartitionKey,
						"SETUP: no ordinal names the missing day; the pending address is the proof")
				} else {
					require.Equal(t, day.Format(transcriptDayLayout), rows[0].Slot.PartitionKey)
					require.NotEqual(t, source.PartitionKey, rows[0].Slot.PartitionKey,
						"SETUP: the lost placement and surviving source are different partitions")
				}

				dir := filepath.Join(s.BaseDir(), meta.ID)
				indexPath := s.savedInputIndexPath(meta.ID)
				var pendingBefore []byte
				if evidence == "pending_index" {
					pendingBefore, err = os.ReadFile(indexPath)
					require.NoError(t, err)
				}
				ordinalBefore, err := os.ReadFile(archive.ordinalRowsPath())
				require.NoError(t, err)
				rolledPath := filepath.Join(dir, source.PartitionKey+".jsonl")
				rolledBefore, err := os.ReadFile(rolledPath)
				require.NoError(t, err)
				rawSource, _, err := archive.ReadAtRaw(source)
				require.NoError(t, err)
				currentPath := filepath.Join(dir, archiveCurrentFile)
				currentBefore, err := os.ReadFile(currentPath)
				require.NoError(t, err)
				require.NotEmpty(t, currentBefore, "SETUP: this file held published content, unlike the rollover control")
				require.NoError(t, os.Remove(currentPath))
				require.NoError(t, s.Close())
				reopened, err := NewUnifiedStore(s.BaseDir())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, reopened.Close()) })

				var failure error
				if operation == "lookup" {
					_, _, failure = reopened.SavedChatInput(meta.ID, id)
				} else {
					_, failure = reopened.SaveChatInput(meta.ID, id, "agent", body)
				}

				require.ErrorIs(t, failure, fs.ErrNotExist, "lost published content is not successful unconsumed state")
				require.ErrorContains(t, failure, "saved input integrity: current partition")
				if evidence == "ordinal" {
					_, err = os.Stat(indexPath)
					require.ErrorIs(t, err, fs.ErrNotExist, "do not recreate the already-delivered report's pending entry")
					pending, pendingErr := reopened.PendingChatInputs(context.Background(), meta.ID)
					require.NoError(t, pendingErr)
					require.Empty(t, pending, "no saved report may be re-admitted after the failed lookup/retry")
				} else {
					pendingAfter, readErr := os.ReadFile(indexPath)
					require.NoError(t, readErr)
					require.Equal(t, pendingBefore, pendingAfter, "do not add an old consumed report to the existing pending index")
				}
				ordinalAfter, err := os.ReadFile(archive.ordinalRowsPath())
				require.NoError(t, err)
				require.Equal(t, ordinalBefore, ordinalAfter, "do not rewrite or discard publication evidence")
				rolledAfter, err := os.ReadFile(rolledPath)
				require.NoError(t, err)
				require.Equal(t, rolledBefore, rolledAfter)
				afterSource, _, err := archive.ReadAtRaw(source)
				require.NoError(t, err)
				require.Equal(t, rawSource, afterSource)
				_, err = os.Stat(currentPath)
				require.ErrorIs(t, err, fs.ErrNotExist, "do not manufacture an empty replacement for lost content")
			})
		}
	}
}
