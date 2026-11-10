package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Gate0 F3 / D13 one constructor + D14 proven-expiry distinction. The shared
// engine must agree with healthy indexed paging after retention, including an
// expired effect targeted by a retract. The standalone repair command that
// used to be exercised alongside it was removed by founder decision 2026-10-27
// (greenfield, no upgrade path); the engine's own classification stays under
// test here.
func TestChatHistoryRepair_ProvenExpiredEffectsAreNotInvalidReferences(t *testing.T) {
	for _, kind := range []string{"settle", "project", "retract"} {
		t.Run(kind+"_target_proven_expired", func(t *testing.T) {
			s, sid, home, retained := gate0ExpiredEffectFixture(t, kind)
			canonical := partitionBytes(t, s, sid)
			build, err := PrepareChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), sid), ChatHistoryIndexBuildOptions{})
			if assert.NoError(t, err, "F3: shared offline reconstruction must ignore ONLY references proven expired by validated retention facts") {
				require.NotNil(t, build)
				require.NoError(t, build.Publish(context.Background()))
				require.NoError(t, build.Close())
				require.NoError(t, VerifyChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), sid)))
				reopened, openErr := NewUnifiedStoreWithHome(s.BaseDir(), home)
				require.NoError(t, openErr)
				t.Cleanup(func() { require.NoError(t, reopened.Close()) })
				page := historyRead(t, reopened, sid, historyOptions(100))
				require.Equal(t, []string{"retained-one"}, historyIDs(page))
				require.Equal(t, historyExpectedPosition(sid, retained), page.Entries[0].Position, "reconstruction never renumbers retained original positions")
			}
			require.Equal(t, canonical, partitionBytes(t, s, sid), "offline build never rewrites canonical/effect bytes")
		})
	}
	t.Run("unexplained_missing_target_is_still_invalid_reference", func(t *testing.T) {
		home := t.TempDir()
		s, err := NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, s.Close()) })
		meta, err := s.NewSession(SessionTypeChat, "webchat", "mia")
		require.NoError(t, err)
		addresses := historyRows(t, s, meta.ID, "real-owner", 1)
		require.Len(t, historyRead(t, s, meta.ID, historyOptions(100)).Entries, 1)
		missing := addresses[0]
		missing.EntryID = "never-existed-owner" // same retained partition, no expiry proof can authorize this reference
		record, err := newEffectRecord(ToolCallEffect{Settle: &ToolCallSettle{Target: missing, IfStatus: "pending",
			ToolCall: ToolCall{ID: "missing-call", Tool: "read_file", Status: "success"}}})
		require.NoError(t, err)
		archive, err := s.archiveStore(meta.ID)
		require.NoError(t, err)
		_, err = archive.Append(record) // deliberate canonical-reference corruption at the filesystem edge, not a success oracle
		require.NoError(t, err)
		require.NoError(t, s.Close())
		before := partitionBytes(t, s, meta.ID)
		_, err = PrepareChatHistoryIndex(context.Background(), filepath.Join(s.BaseDir(), meta.ID), ChatHistoryIndexBuildOptions{})
		require.Error(t, err)
		var classified interface{ HistoryIndexBuildClass() string }
		require.ErrorAs(t, err, &classified)
		require.Equal(t, "invalid_reference", classified.HistoryIndexBuildClass())
		require.Equal(t, before, partitionBytes(t, s, meta.ID))
	})
}
