package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Architect ruling arch-tail-ruling.md, Required tests 1–3: a catch-up overflow
// is not permission to replace a genuinely damaged derived index. The damage
// checks must win before catchup_limit, while benign crash residue still heals.
func TestChatHistoryHeal_DamagedDerivedTailRefusedBeyondCatchupLimit(t *testing.T) {
	for _, kind := range []string{"invalid_complete_frame", "published_index_truncated", "undamaged_control", "torn_unpublished_tail"} {
		t.Run(kind, func(t *testing.T) {
			logs := selfHealCaptureLogs(t)
			f := newSelfHealFixture(t) // one saved +213 accepted-but-unindexed records
			cp := historyCheckpointOf(t, f.store, f.sid)
			path := filepath.Join(historyIndexDir(f.store, f.sid), "generations", cp.File)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Positive(t, cp.MutationEnd)
			require.Equal(t, cp.MutationEnd, info.Size(), "positive fixture control: damage begins at the real published end, not an unused file")
			selfHealPlantDerivedTail(t, path, cp.MutationEnd, kind)
			before := selfHealFiles(t, historyIndexDir(f.store, f.sid), false)
			canonical := partitionBytes(t, f.store, f.sid)
			builds := selfHealObserveBuilds(t, f.sid)

			err = f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
			if kind == "invalid_complete_frame" || kind == "published_index_truncated" {
				// Nonfatal assertions deliberately retain every independent safety
				// check in the RED receipt even when the old code returns nil.
				if assert.IsType(t, &ChatHistoryUnavailableError{}, err, "real derived-index damage must be refused, not silently rebuilt") {
					var unavailable *ChatHistoryUnavailableError
					require.ErrorAs(t, err, &unavailable)
					assert.Equal(t, "index_inconsistent", unavailable.Reason, "damage must win over catchup_limit even when 213 records behind")
					assert.ErrorIs(t, err, ErrChatHistoryUnavailable)
				}
				assert.Zero(t, builds.Load(), "real derived-index damage must not enter the rebuild engine")
				assert.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog), "no success warning may hide damage under trigger=catchup_limit")
				assert.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), "refusal must occur before even a failed rebuild attempt")
				assert.Equal(t, before, selfHealFiles(t, historyIndexDir(f.store, f.sid), false), "the entire damaged index folder must remain byte-identical for diagnosis")
			} else {
				require.NoError(t, err, "healthy lag or a merely torn unpublished tail must still heal in this one demand call")
				require.EqualValues(t, 1, builds.Load(), "positive control: the observer must see exactly one real full build")
				selfHealRequireIndexed(t, f)
				records := selfHealEvents(t, logs, f.sid, selfHealOKLog)
				require.Len(t, records, 1)
				selfHealRequireLogBase(t, records[0], f.sid, 1)
				require.Equal(t, "rebuild", records[0]["action"])
				require.Equal(t, int64(1+selfHealBacklog), selfHealLogNumber(t, records[0], "records"))
				require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
			}
			assert.Equal(t, canonical, partitionBytes(t, f.store, f.sid), "neither refusal nor crash-tail recovery may rewrite any canonical record")
			selfHealRequirePrivateLogs(t, logs, f)
		})
	}
}

// Only temporary derived DATA is altered; never production code. A complete,
// checksummed node with a self-reference is the ruling's Left>=offset example.
// Missing one byte solely from a NEW unpublished frame is instead benign torn
// crash residue. Truncation below the saved published end is genuine damage.
func selfHealPlantDerivedTail(t testing.TB, path string, publishedEnd int64, kind string) {
	t.Helper()
	switch kind {
	case "undamaged_control":
		return
	case "published_index_truncated":
		require.NoError(t, os.Truncate(path, publishedEnd-1))
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, publishedEnd-1, info.Size(), "exact one-byte truncation of the published index")
		return
	case "invalid_complete_frame", "torn_unpublished_tail":
		left := int64(0)
		if kind == "invalid_complete_frame" {
			left = publishedEnd + 1 // node references are one-based: point to this very frame
		}
		frame, err := historyEncodeFrame(historyFrame{Node: &historyNode{
			Key: "test/derived-tail", Value: json.RawMessage(`1`), Left: left,
		}})
		require.NoError(t, err)
		if kind == "torn_unpublished_tail" {
			frame = frame[:len(frame)-1] // only the never-published frame is incomplete
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		require.NoError(t, err)
		n, writeErr := file.Write(frame)
		require.NoError(t, errors.Join(writeErr, file.Sync(), file.Close()))
		require.Equal(t, len(frame), n)
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, publishedEnd+int64(len(frame)), info.Size(), "the fixture must change the actual current index's physical suffix")
	default:
		t.Fatalf("unknown derived-tail fixture %q", kind)
	}
}
