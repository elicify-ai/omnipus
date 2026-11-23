package agent

// Spec rev8 S79/FR-041: corrupt the real parent's journal only after the actual
// delegate has created a readable, web-origin helper. No seeded child lineage.
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualUnreadableParent(t *testing.T) {
	t.Run("S79_actual_helper_parent_unreadable_withholds", func(t *testing.T) {
		f, parent, _ := programsDelegateFixture(t)
		positive, result := programsCountInput(t, f, parent, "s79-connected-control", programsWebInput())
		require.Equal(t, "16", positive, "instrument: actual connected Dev child receives the saved token")
		require.NotContains(t, result, "Connected programs were not used for this command:")
		child := programsDelegateFromInput(t, f, parent, "s79-actual-delegate", programsWebInput())
		store := f.al.GetSessionLifecycleStore()
		record, err := store.Load(child)
		require.NoError(t, err)
		require.Equal(t, parent, record.SteeringSessionID(), "Given: actual canonical parent edge")
		require.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode)
		require.Equal(t, []string{"web"}, record.Provenance, "Given: actual delegate wrote the readable web lineage")
		meta, err := f.al.GetSessionStore().GetMeta(child)
		require.NoError(t, err)
		require.Equal(t, parent, meta.ParentSessionID)
		inst, ok := f.al.GetRegistry().GetAgent("dev")
		require.True(t, ok)
		observer := &programsGoalOriginProvider{wu5ScriptedProvider: f.provider}
		inst.Provider = observer // Observe the paid-provider edge; no context mutation.
		control, result := programsCountInput(t, f, child, "s79-readable-chain", programsWebInput())
		require.Equal(t, "16", control, "instrument: this same real helper's ordinary re-entry is usable before parent corruption")
		require.NotContains(t, result, "Connected programs were not used for this command:")
		joinGoalFixtureRuns(t, f.al)
		parentPath := filepath.Join(store.Dir(), parent+".jsonl")
		original, err := os.ReadFile(parentPath)
		require.NoError(t, err)
		require.NotEmpty(t, original)
		childPath := filepath.Join(store.Dir(), child+".jsonl")
		childBefore, err := os.ReadFile(childPath)
		require.NoError(t, err)
		lock := store.Lock(parent)
		lock.Lock()
		err = os.WriteFile(parentPath, []byte("not a readable lifecycle record\n"), 0o600)
		lock.Unlock()
		require.NoError(t, err)
		t.Cleanup(func() {
			joinGoalFixtureRuns(t, f.al)
			lock.Lock()
			defer lock.Unlock()
			assert.NoError(t, os.WriteFile(parentPath, original, 0o600))
		})
		_, err = store.Load(parent)
		require.ErrorContains(t, err, "contains no readable lifecycle record", "instrument: actual parent journal is unreadable, not merely an absent test path")
		childAfter, err := os.ReadFile(childPath)
		require.NoError(t, err)
		require.Equal(t, childBefore, childAfter, "instrument: corrupting the parent has not changed the actual child's journal")
		before := len(observer.observed())
		count, result := programsCountInput(t, f, child, "s79-corrupt-chain", programsWebInput())
		joinGoalFixtureRuns(t, f.al)
		assert.Equal(t, "0", count, "S79: the actual unreadable ancestor must withhold even with a fresh web input")
		assert.Contains(t, result, programsUnresolvedNotice, "S79: the exact unresolved notice, not a web or connector notice")
		origins := observer.observed()[before:]
		require.GreaterOrEqual(t, len(origins), 2)
		for _, actual := range origins[:2] {
			assert.Equal(t, bus.UnresolvedProvenance(), actual, "S79: receiving command/result context is unresolved, never a permissive web fallback")
		}
		t.Logf("actual helper parent unreadable; readable-chain-count=%s corrupt-chain-count=%s command_ran=true receiving_origins=%v", control, count, origins[:2])
	})
}
