package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// SFH-2/4: persisted corruption is not proof that a report is absent or unconsumed.
func TestSavedInputReviewR1Integrity(t *testing.T) {
	for _, contents := range []string{"null", "[]", `{"held":null}`} {
		t.Run("invalid_index_"+contents, func(t *testing.T) {
			s := newTestStore(t)
			meta, err := s.NewSession(SessionTypeChat, "", "agent")
			require.NoError(t, err)
			addr, err := s.SaveChatInput(meta.ID, "saved-input-held", "agent", "held helper report")
			require.NoError(t, err)
			archive, err := s.archiveStore(meta.ID)
			require.NoError(t, err)
			before, _, err := archive.ReadAtRaw(addr)
			require.NoError(t, err)
			path := s.savedInputIndexPath(meta.ID)
			require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))

			pending, readErr := s.PendingChatInputs(context.Background(), meta.ID)

			after, _, err := archive.ReadAtRaw(addr)
			require.NoError(t, err)
			require.Equal(t, before, after, "corruption must not alter the saved report")
			stored, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, contents, string(stored), "reads must not guess or rewrite the index")
			require.Nil(t, pending)
			require.Error(t, readErr, "an existing invalid index must not become empty success")
			if contents == "null" {
				require.EqualError(t, readErr, fmt.Sprintf("saved input index %q: expected a JSON object, got null", meta.ID))
			}
		})
	}

	for _, partition := range []string{"current", "rolled"} {
		for _, evidence := range []string{"placement", "poll_marker"} {
			for _, operation := range []string{"lookup", "retry_save"} {
				t.Run(partition+"_corrupt_"+evidence+"_"+operation, func(t *testing.T) {
					s := newTestStore(t)
					day := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
					setArchiveDay(s, &day)
					meta, err := s.NewSession(SessionTypeChat, "", "agent")
					require.NoError(t, err)
					const id, body = "saved-input-consumed", "already handled helper report"
					source, err := s.SaveChatInput(meta.ID, id, "agent", body)
					require.NoError(t, err)
					archive, err := s.archiveStore(meta.ID)
					require.NoError(t, err)
					var consumed ArchiveAddress
					if evidence == "placement" {
						_, _, err = s.PlaceSavedInput(context.Background(), meta.ID, source)
						require.NoError(t, err)
						require.NoError(t, archive.ScanAll(func(addr ArchiveAddress, rec ArchiveRecord) bool {
							if rec.ModelRef != nil && rec.ModelRef.EntryID == id {
								consumed = addr
							}
							return true
						}))
					} else {
						consumed, err = s.AppendTranscriptAddressed(meta.ID, TranscriptEntry{
							ID: "consumed-consumed", Role: "system", Content: "consumed consumed", Timestamp: day,
						})
						require.NoError(t, err)
						require.NoError(t, s.RetireSavedInput(meta.ID, id))
					}
					require.NotEmpty(t, consumed.EntryID, "SETUP: real consumption evidence exists")
					_, err = os.Stat(s.savedInputIndexPath(meta.ID))
					require.ErrorIs(t, err, fs.ErrNotExist, "SETUP: the consumed report was retired")
					if partition == "rolled" {
						day = day.AddDate(0, 0, 1)
						_, err = s.AppendTranscriptAddressed(meta.ID, TranscriptEntry{
							ID: "later-chat", Role: "assistant", Content: "later day", Timestamp: day,
						})
						require.NoError(t, err)
					}
					path := filepath.Join(s.BaseDir(), meta.ID, "transcript.jsonl")
					if partition == "rolled" {
						path = filepath.Join(s.BaseDir(), meta.ID, consumed.PartitionKey+".jsonl")
					}
					data, err := os.ReadFile(path)
					require.NoError(t, err)
					corrupt := append(append([]byte(nil), data[:consumed.ByteOffset]...), []byte("{\n")...)
					require.NoError(t, os.WriteFile(path, corrupt, 0o600))
					// Positive control: recall remains tolerant, but sees the healthy source.
					found := false
					require.NoError(t, archive.ScanAll(func(_ ArchiveAddress, rec ArchiveRecord) bool {
						found = found || rec.ID == id
						return true
					}))
					require.True(t, found)

					var integrityErr error
					if operation == "lookup" {
						_, _, integrityErr = s.SavedChatInput(meta.ID, id)
					} else {
						_, integrityErr = s.SaveChatInput(meta.ID, id, "agent", body)
					}

					after, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, corrupt, after, "integrity checks must never rewrite the archive")
					require.Error(t, integrityErr, "corrupt consumption evidence cannot certify unconsumed")
					require.ErrorContains(t, integrityErr, fmt.Sprintf("saved input integrity: decode %s@%d:", consumed.PartitionKey, consumed.ByteOffset))
					var syntaxErr *json.SyntaxError
					require.ErrorAs(t, integrityErr, &syntaxErr)
					_, err = os.Stat(s.savedInputIndexPath(meta.ID))
					require.ErrorIs(t, err, fs.ErrNotExist, "do not reindex an input from incomplete consumption evidence")
				})
			}
		}
	}
}
