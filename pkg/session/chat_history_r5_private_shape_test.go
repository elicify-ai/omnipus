package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A2.8 / SEC-R5-1b / CHECK F1: skipping private bytes must not skip their
// ModelPayload type check. Use the real streamed and full-entry archive readers
// on the same saved bytes, not just the scanner's privateOK flag. Malformed
// private shape is corrupt_archive; it is distinct from an unreadable scan's D4
// stand-in. The independent CHECK owns the M2 mutation RED, not this author.
func TestChatHistoryPage_R5PrivateMemberOutlineMatchesFullArchive(t *testing.T) {
	for _, tc := range []struct { // not-wire-format: private malformed-storage cases.
		name, private string
		valid         bool
	}{
		{"number", `42`, false},
		{"string", `"PRIVATE_R5_WRONG_SHAPE"`, false},
		{"object_with_wrong_role_type", `{"role":42,"content":"PRIVATE_R5_BAD_ROLE"}`, false},
		{"valid_object", `{"role":"assistant","content":"PRIVATE_R5_OUTLINE_CONTROL"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			public := TranscriptEntry{
				ID: "r5-outline-owner", Type: EntryTypeMessage, Role: "assistant", AgentID: "mia", ViewMembership: ViewMembershipBoth,
				Timestamp: time.Date(2026, 10, 31, 7, 0, 0, 0, time.UTC), TurnID: "r5-outline-turn", Content: "PUBLIC_R5_OUTLINE_CONTROL",
			}
			encodedPublic, err := json.Marshal(public)
			require.NoError(t, err)
			raw := []byte(string(encodedPublic[:len(encodedPublic)-1]) + `,"model_message":` + tc.private + "}\n")
			require.True(t, json.Valid(raw), "wrong type is not malformed JSON syntax")
			// These are new, owned saved-partition fixtures, including corrupt
			// shapes an ordinary typed producer cannot emit. No accepted live
			// record, index, or production code is rewritten.
			dir := t.TempDir()
			address := ArchiveAddress{PartitionKey: historyR5Day, EntryID: public.ID}
			path := filepath.Join(dir, address.PartitionKey+".jsonl")
			require.NoError(t, os.WriteFile(path, raw, 0o600))
			sum := sha256.Sum256(raw)
			meta := historyRecord{Address: address, Length: int64(len(raw)), Digest: hex.EncodeToString(sum[:])}
			cut := ChatHistoryReadCut{sessionID: "r5-outline-session"}
			streamIns := newHistoryStandIns()
			var streamedMetrics, fullMetrics ChatHistoryReadMetrics
			streamed, streamErr := historyStreamArchive(WithChatHistoryReadMetrics(context.Background(), &streamedMetrics), dir, meta, cut, "page", "selected", streamIns)
			full, fullErr := historyReadArchive(WithChatHistoryReadMetrics(context.Background(), &fullMetrics), dir, meta, cut, "page", "selected")
			require.Equal(t, int64(1), streamedMetrics.SelectedArchiveRecords, "streamed reader reached the real count/digest gate")
			require.Equal(t, int64(len(raw)), streamedMetrics.SelectedArchiveBytes)
			require.Equal(t, int64(1), fullMetrics.SelectedArchiveRecords)
			require.Equal(t, int64(len(raw)), fullMetrics.SelectedArchiveBytes)
			if !tc.valid {
				var streamedClass, fullClass *ChatHistoryIndexBuildError
				require.ErrorAs(t, streamErr, &streamedClass, "archive boundary must refuse wrong private type, not return a usable public record")
				require.Equal(t, "corrupt_archive", streamedClass.Class)
				require.EqualError(t, streamErr, "history index build: corrupt_archive", "fixed content-free archive diagnostic")
				require.Equal(t, ArchiveRecord{}, streamed, "private-shape refusal exposes no partial record")
				require.ErrorAs(t, fullErr, &fullClass, "the full-entry archive reader must refuse the SAME saved bytes")
				require.Equal(t, "corrupt_archive", fullClass.Class)
				require.EqualError(t, fullErr, "history index build: corrupt_archive")
			} else {
				require.NoError(t, streamErr)
				require.NoError(t, fullErr)
				require.Equal(t, ArchiveRecord{TranscriptEntry: public}, streamed, "valid private member is nulled; all public fields remain exact")
				require.Equal(t, ArchiveRecord{TranscriptEntry: public, ModelMessage: &ModelPayload{Role: "assistant", Content: "PRIVATE_R5_OUTLINE_CONTROL"}}, full, "full-entry archive retains the entire valid private payload")
			}
			require.Empty(t, streamIns.values, "no private-only stand-in retained at the archive boundary")

			// Also pin the reduced document itself: syntax stays valid while
			// type validity is reported separately, and no private value is kept.
			ins := newHistoryStandIns()
			reduced, privateOK, err := historyStreamRecordChecked(bytes.NewReader(raw), ins)
			require.NoError(t, err)
			require.Equal(t, tc.valid, privateOK)
			var want, got map[string]any
			require.NoError(t, json.Unmarshal(encodedPublic, &want))
			want["model_message"] = nil
			require.NoError(t, json.Unmarshal(reduced, &got))
			require.Equal(t, want, got, "the entire reduced record is the public source plus private null")
			require.NotContains(t, string(reduced), "PRIVATE_R5_")
			require.Empty(t, ins.values, "skipped private strings never create stand-ins")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, raw, after, "both readers leave canonical bytes untouched")
			t.Logf("private_outline valid=%t archive_bytes=%d public_id=%s", tc.valid, len(raw), public.ID)
		})
	}
}
