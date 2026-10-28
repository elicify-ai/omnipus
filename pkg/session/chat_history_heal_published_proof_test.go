package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FIX-ROUND-1 row 2 / sfh F1: a failed existing published-tree proof is real
// damage, not permission to forget saved ranks and reconstruct a new baseline.
func TestChatHistoryHeal_PublishedProofFailureIsNeverBlessed(t *testing.T) {
	for _, kind := range []string{"corrupt_child", "corrupt_child_with_expired_position", "invalid_saved_rank", "healthy_child", "healthy_child_with_expired_position"} {
		t.Run(kind, func(t *testing.T) {
			logs := selfHealCaptureLogs(t)
			withExpiry := kind == "corrupt_child_with_expired_position" || kind == "healthy_child_with_expired_position"
			s, sid, addresses, expired := healFixPublishedProofFixture(t, withExpiry)
			if kind == "invalid_saved_rank" {
				healFixInvalidateSavedRank(t, s, sid, addresses[0])
			} else if kind == "corrupt_child" || kind == "corrupt_child_with_expired_position" {
				healFixCorruptPublishedChild(t, s, sid)
			}
			before := selfHealFiles(t, historyIndexDir(s, sid), false)
			canonical := partitionBytes(t, s, sid)
			err := s.CheckChatHistoryReadiness(context.Background(), sid)
			healthy := kind == "healthy_child" || kind == "healthy_child_with_expired_position"
			if !healthy {
				if assert.IsType(t, &ChatHistoryUnavailableError{}, err, "published proof failure must not be silently blessed as a successful rebuild") {
					var unavailable *ChatHistoryUnavailableError
					require.ErrorAs(t, err, &unavailable)
					assert.Equal(t, "index_inconsistent", unavailable.Reason)
					assert.ErrorIs(t, err, ErrChatHistoryUnavailable)
				}
				assert.Empty(t, selfHealEvents(t, logs, sid, selfHealOKLog), "a corrupt published child/rank must never be logged as ordinary lag recovery")
				assert.Equal(t, before, selfHealFiles(t, historyIndexDir(s, sid), false), "old saved positions/ranks and damaged files must remain byte-identical")
			} else {
				require.NoError(t, err, "strict existing proof must not disable healing of a healthy child tree")
				healFixRequireAddresses(t, s, sid, addresses)
				records := selfHealEvents(t, logs, sid, selfHealOKLog)
				require.Len(t, records, 1)
				selfHealRequireLogBase(t, records[0], sid, 1)
				if expired != "" {
					tomb, found, lookupErr := s.lookupChatHistoryTombstone(context.Background(), sid, expired)
					require.NoError(t, lookupErr)
					require.True(t, found, "a healthy demand rebuild must carry an expired-position mapping forward")
					require.Equal(t, expired, tomb.Position)
				}
			}
			assert.Equal(t, canonical, partitionBytes(t, s, sid), "published-proof refusal/recovery never changes canonical bytes")
			require.NotContains(t, logs.text(), selfHealBody)
		})
	}
}

func healFixPublishedProofFixture(t *testing.T, expiry bool) (*UnifiedStore, string, []ArchiveAddress, HistoryPosition) {
	t.Helper()
	if !expiry {
		f := newSelfHealFixture(t)
		return f.store, f.sid, f.addresses, ""
	}
	s, sid, original := historyRetentionFixture(t, 0)
	s.stopStatsFlusher()
	removed, err := s.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	expired := historyExpectedPosition(sid, original[0])
	tomb, found, err := s.lookupChatHistoryTombstone(context.Background(), sid, expired)
	require.NoError(t, err)
	require.True(t, found, "positive fixture control: a REAL retention sweep published the expired mapping")
	require.Equal(t, expired, tomb.Position)
	// The existing fixture has four 20-record days; expiry removes day one.
	addresses := append([]ArchiveAddress(nil), original[20:]...)
	addresses = append(addresses, historyUnpublishedArchive(t, s, sid, selfHealBacklog)...)
	_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
	selfHealRequireUnavailable(t, err, "catchup_limit")
	return s, sid, addresses, expired
}

func healFixCorruptPublishedChild(t testing.TB, s *UnifiedStore, sid string) {
	t.Helper()
	dir := filepath.Join(s.BaseDir(), sid)
	cp := historyCheckpointOf(t, s, sid)
	tree, err := historyOpenTree(context.Background(), dir, cp, false)
	require.NoError(t, err)
	root, err := tree.node(cp.Root)
	require.NoError(t, err)
	child := root.Left
	if child == 0 {
		child = root.Right
	}
	require.Positive(t, child, "positive fixture control: corruption is in a real non-root published child")
	require.Less(t, child, cp.Root)
	_, rootEnd, err := tree.readFrame(cp.Root-1, cp.MutationEnd)
	require.NoError(t, err)
	_, childEnd, err := tree.readFrame(child-1, cp.MutationEnd)
	require.NoError(t, err, "child frame is valid before corruption")
	require.NoError(t, tree.file.Close())
	path := filepath.Join(historyIndexDir(s, sid), "generations", cp.File)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.EqualValues(t, cp.MutationEnd, len(before), "no damaged unpublished tail is involved in this case")
	offset := child - 1 + historyFrameHeader // references are one-based; alter only this child's payload
	require.Less(t, offset, childEnd)
	after := bytes.Clone(before)
	after[offset] ^= 1 // complete frame, unchanged length, wrong checksum
	require.Equal(t, before[cp.Root-1:rootEnd], after[cp.Root-1:rootEnd], "published ROOT frame remains byte-identical")
	require.Equal(t, before[cp.MutationEnd:], after[cp.MutationEnd:], "unpublished tail remains intact")
	require.NoError(t, os.WriteFile(path, after, 0o600))
}

func healFixInvalidateSavedRank(t testing.TB, s *UnifiedStore, sid string, address ArchiveAddress) {
	t.Helper()
	dir := filepath.Join(s.BaseDir(), sid)
	cp := historyCheckpointOf(t, s, sid)
	tree, err := historyOpenTree(context.Background(), dir, cp, true)
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.file.Close()) }()
	// Use the REAL node writer and publisher: framing/checksums/root stay valid,
	// but saved rank zero violates the strict existing-proof contract in sfh F1.
	key := "p/" + string(historyExpectedPosition(sid, address))
	var rank uint64
	found, err := tree.get(key, &rank)
	require.NoError(t, err)
	require.True(t, found, "positive control: alter a real saved position/rank, not an unused test key")
	require.Positive(t, rank)
	require.NoError(t, tree.set(key, uint64(0)))
	require.NoError(t, historyPublish(dir, tree, &cp, "test_invalid_saved_rank", 0))
	found, err = tree.get(key, &rank)
	require.NoError(t, err)
	require.True(t, found)
	require.Zero(t, rank)
}

func healFixRequireAddresses(t testing.TB, s *UnifiedStore, sid string, want []ArchiveAddress) {
	t.Helper()
	cp := historyCheckpointOf(t, s, sid)
	tree, err := historyOpenTree(context.Background(), filepath.Join(s.BaseDir(), sid), cp, false)
	require.NoError(t, err)
	var got []ArchiveAddress
	walkErr := tree.walk(func(n historyNode) error {
		if len(n.Key) >= 2 && n.Key[:2] == "r/" {
			var record historyRecord
			if err := json.Unmarshal(n.Value, &record); err != nil {
				return err
			}
			got = append(got, record.Address)
		}
		return nil
	})
	require.NoError(t, errors.Join(walkErr, tree.file.Close()))
	require.Len(t, got, len(want), "every accepted retained canonical address appears exactly once")
	require.ElementsMatch(t, want, got)
}
