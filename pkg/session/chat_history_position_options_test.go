package session

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// D2/T02: independent Python uint64-BE/UTF-8/SHA256 vectors, never values
// copied from the product encoder or a prior read response.
func TestChatHistoryPosition_CoreGoldenVectorsAndCanonicalParser(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "tests", "chat-history-paging-be", "position-vectors.json"))
	require.NoError(t, err)
	var vectors []map[string]string
	require.NoError(t, json.Unmarshal(b, &vectors))
	require.Len(t, vectors, 6)
	for _, v := range vectors {
		t.Run(v["name"], func(t *testing.T) {
			offset, err := strconv.ParseInt(v["byte_offset"], 10, 64)
			require.NoError(t, err)
			got, err := historyPositionForAddress(v["session_id"], ArchiveAddress{PartitionKey: v["partition_key"], ByteOffset: offset, EntryID: v["entry_id"]})
			require.NoError(t, err, "real D2 encoder")
			require.Equal(t, HistoryPosition(v["expected"]), got)
			parsed, err := ParseHistoryPosition(v["expected"])
			require.NoError(t, err, "real canonical parser")
			require.Equal(t, got, parsed)
		})
	}
	for _, raw := range []string{"", "null", "hp2_" + strings.Repeat("A", 43), "hp1_" + strings.Repeat("A", 42), "hp1_" + strings.Repeat("A", 44), "hp1_" + strings.Repeat("!", 43), "hp1_" + strings.Repeat("A", 42) + "B", "hp1_" + strings.Repeat("A", 43) + "="} {
		t.Run("invalid_"+raw, func(t *testing.T) {
			p, err := ParseHistoryPosition(raw)
			require.ErrorIs(t, err, ErrInvalidHistoryPosition)
			require.Empty(t, p, "invalid syntax/noncanonical trailing bits never normalize silently")
		})
	}
}

func TestChatHistoryPosition_RolloverRestartAndUnchangedOfflineReconstruction(t *testing.T) {
	s, sid := newEffectsSession(t)
	addresses := buildChatHistoryDayFixture(t, s, sid, []chatHistoryFixtureDay{historyDayRows("2026-01-01", "old", 2, nil), historyDayRows("2026-01-02", "new", 1, nil)})
	want := []HistoryPosition{historyExpectedPosition(sid, addresses[0]), historyExpectedPosition(sid, addresses[1]), historyExpectedPosition(sid, addresses[2])}
	p := historyRead(t, s, sid, historyOptions(40))
	for i, e := range p.Entries {
		require.Equal(t, want[i], e.Position)
	}
	require.NoError(t, s.Close())
	reopened, err := NewUnifiedStore(s.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	p = historyRead(t, reopened, sid, historyOptions(40))
	for i, e := range p.Entries {
		require.Equal(t, want[i], e.Position, "restart preserves final-byte occurrence")
	}
	canonical := partitionBytes(t, reopened, sid)
	require.NoError(t, reopened.Close(), "offline reconstruction starts only after the source store is stopped/joined")
	require.NoError(t, os.RemoveAll(historyIndexDir(reopened, sid)))
	build, err := PrepareChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), sid), ChatHistoryIndexBuildOptions{})
	require.NoError(t, err, "shared offline constructor")
	require.NotNil(t, build)
	defer func() { require.NoError(t, build.Close()) }()
	require.NoError(t, build.Publish(context.Background()))
	require.NoError(t, VerifyChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), sid)))
	final, err := NewUnifiedStore(s.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, final.Close()) })
	p = historyRead(t, final, sid, historyOptions(40))
	for i, e := range p.Entries {
		require.Equal(t, want[i], e.Position, "offline reconstruction cannot renumber unchanged bytes")
	}
	require.Equal(t, canonical, partitionBytes(t, final, sid))
}

func TestChatHistoryPage_CoreOptionsAndInvalidWeighing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		weigh ChatHistoryWeigher
	}{
		{"nil", nil},
		{"error", func(context.Context, ChatHistoryPage) (ChatHistoryWeight, error) {
			return ChatHistoryWeight{}, errors.New("private projection canary")
		}},
		{"negative", func(context.Context, ChatHistoryPage) (ChatHistoryWeight, error) {
			return ChatHistoryWeight{RESTBytes: -1, WSBytes: 1, ProjectedBytes: 1}, nil
		}},
		{"overflow", func(context.Context, ChatHistoryPage) (ChatHistoryWeight, error) {
			n := int64(math.MaxInt64)
			n++
			return ChatHistoryWeight{RESTBytes: n, WSBytes: 1, ProjectedBytes: 1}, nil
		}},
		{"inconsistent", func(context.Context, ChatHistoryPage) (ChatHistoryWeight, error) {
			return ChatHistoryWeight{RESTBytes: 10, WSBytes: 20, ProjectedBytes: 10}, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "a", 1)
			p, err := s.ReadChatHistoryPage(context.Background(), sid, ChatHistoryPageOptions{Limit: 40, Weigh: tc.weigh})
			require.ErrorIs(t, err, ErrHistoryProjection, "bad dependency fails, no guessed0/estimate/full-read fallback")
			require.Empty(t, p.Entries)
			require.NotContains(t, err.Error(), "private projection canary")
		})
	}
	for _, budget := range []int64{2097152, 4194304, 8388608} {
		t.Run("positive_budget_"+strconv.FormatInt(budget, 10), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "a", 1)
			opts := historyOptions(40)
			opts.SoftByteBudget = budget
			p := historyRead(t, s, sid, opts)
			require.Equal(t, budget, p.PageInfo.SoftByteBudget)
			require.Equal(t, 40, p.PageInfo.Limit)
			require.Equal(t, 20, p.PageInfo.MinRows)
		})
	}
	t.Run("internal_zero_defaults_are_not_external_zero", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		p := historyRead(t, s, sid, ChatHistoryPageOptions{Weigh: historyFixtureWeigher})
		require.Equal(t, 40, p.PageInfo.Limit)
		require.Equal(t, 20, p.PageInfo.MinRows)
		require.EqualValues(t, 4194304, p.PageInfo.SoftByteBudget)
	})
	for _, limit := range []int{-1, 19, 101} {
		t.Run("invalid_limit_"+strconv.Itoa(limit), func(t *testing.T) {
			s, sid := newEffectsSession(t)
			_, err := s.ReadChatHistoryPage(context.Background(), sid, historyOptions(limit))
			require.Error(t, err, "invalid internal limits refuse rather than clamp")
		})
	}
}
