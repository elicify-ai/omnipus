package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate0 F1, spec D1a/B21/B31: zero is valid for a genuinely empty tree, not
// for a checkpoint that still claims forty covered canonical occurrences.
func TestChatHistoryCheckpoint_NonemptyMissingRootIsUnavailable(t *testing.T) {
	t.Run("genuine_empty_root_zero_remains_readable", func(t *testing.T) {
		s, sid := newEffectsSession(t)
		cp, err := historyLoadCheckpoint(context.Background(), filepath.Join(s.BaseDir(), sid), sid)
		require.NoError(t, err)
		require.Zero(t, cp.Root)
		require.Zero(t, cp.Rank)
		page := historyRead(t, s, sid, historyOptions(40))
		require.Empty(t, page.Entries)
		require.Equal(t, 0, page.PageInfo.RowCount)
		require.False(t, page.HasEarlier)
		require.False(t, page.HasLater)
	})
	for _, fault := range []string{"missing_root_member", "zero_root_with_nonempty_coverage"} {
		t.Run(fault, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			addresses := historyRows(t, s, sid, "root-proof", 40)
			positive := historyRead(t, s, sid, historyOptions(40))
			require.Len(t, positive.Entries, 40, "real valid nonempty baseline before corrupting only its private root")
			require.Equal(t, historyExpectedPosition(sid, addresses[0]), positive.Entries[0].Position)
			dir := filepath.Join(s.BaseDir(), sid)
			checkpoint := filepath.Join(dir, ChatHistoryIndexDirectory, "checkpoint.json")
			cp, err := historyLoadCheckpoint(context.Background(), dir, sid)
			require.NoError(t, err)
			require.Positive(t, cp.Root)
			require.EqualValues(t, 40, cp.Rank)
			require.NotEmpty(t, cp.ArchiveEnds)
			require.NotNil(t, cp.Anchor)
			generation, err := os.Stat(filepath.Join(dir, ChatHistoryIndexDirectory, "generations", cp.File))
			require.NoError(t, err)
			require.Equal(t, cp.MutationEnd, generation.Size(), "instrument: NO pending derived suffix")
			for partition, end := range cp.ArchiveEnds {
				path, pathErr := historyArchivePath(dir, partition)
				require.NoError(t, pathErr)
				info, statErr := os.Stat(path)
				require.NoError(t, statErr)
				require.Equal(t, end, info.Size(), "instrument: covered canonical ends are exact, no suffix can rescue a missing root")
			}
			require.NoError(t, s.Close())
			canonical := partitionBytes(t, s, sid)
			body, err := os.ReadFile(checkpoint)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &fields))
			before := make(map[string]string)
			for key, value := range fields {
				if key != "root" {
					before[key] = string(value)
				}
			}
			if fault == "missing_root_member" {
				delete(fields, "root")
			} else {
				fields["root"] = json.RawMessage("0")
			}
			for key, value := range before {
				require.Equal(t, value, string(fields[key]), "rank/covered ends/anchor/footer metadata stay intact")
			}
			body, err = json.Marshal(fields)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(checkpoint, body, 0o600))
			reopened, err := NewUnifiedStore(s.BaseDir())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			page, err := reopened.ReadChatHistoryPage(context.Background(), sid, historyOptions(40))
			if assert.ErrorIs(t, err, ErrChatHistoryUnavailable,
				"F1/D1a: nonempty root loss MUST be unavailable/index_inconsistent, never successful empty history (got entries=%d rows=%d)", len(page.Entries), page.PageInfo.RowCount) {
				var unavailable *ChatHistoryUnavailableError
				require.ErrorAs(t, err, &unavailable)
				assert.Equal(t, "index_inconsistent", unavailable.Reason)
			}
			require.Equal(t, canonical, partitionBytes(t, reopened, sid), "root refusal never resets, trims or rewrites canonical data")
		})
	}
}
