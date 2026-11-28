package agent

// Spec rev8 S79/D5 row13/FR-041: actual helper-chain edges, with the Programs
// limit's 15/16/17 boundaries independent of the configured delegation ceiling.
import (
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualHelperHopBound(t *testing.T) {
	t.Run("S79_D5_actual_fifteen_sixteen_seventeen_hop_chain", func(t *testing.T) {
		f, root, _ := programsDelegateFixture(t)
		// This is an ordinary configured authority limit, not the Programs
		// provenance limit. It must allow all seventeen actual launches.
		f.al.GetConfig().Performance.MaxDelegationDepth = 32
		positive, result := programsCountInput(t, f, root, "s79-hops-control", programsWebInput())
		require.Equal(t, "16", positive, "instrument: connected web command works before the real chain")
		require.NotContains(t, result, "Connected programs were not used for this command:")
		parent := root
		ids := map[string]bool{root: true}
		for depth := 1; depth <= 17; depth++ {
			child := programsDelegateFromInput(t, f, parent, fmt.Sprintf("s79-hop-launch-%d", depth), programsWebInput())
			require.False(t, ids[child], "instrument: every actual launch minted a distinct child")
			ids[child] = true
			record, err := f.al.GetSessionLifecycleStore().Load(child)
			require.NoError(t, err)
			require.Equal(t, parent, record.SteeringSessionID(), "actual launch at depth%d has the real parent edge", depth)
			require.Equal(t, session.ProvenanceModeLineage, record.ProvenanceMode)
			require.Equal(t, []string{"web"}, record.Provenance, "the actual delegate producer wrote this child's web lineage")
			parent = child
			if depth < 15 {
				continue
			}
			walk := child
			seen := map[string]bool{}
			for walk != root {
				require.False(t, seen[walk], "instrument: actual chain is not cyclic")
				seen[walk] = true
				r, err := f.al.GetSessionLifecycleStore().Load(walk)
				require.NoError(t, err)
				walk = r.SteeringSessionID()
				require.NotEmpty(t, walk, "instrument: actual chain reaches the original root")
			}
			require.Len(t, seen, depth, "instrument: exact actual persisted hop count")
			inst, ok := f.al.GetRegistry().GetAgent("dev")
			require.True(t, ok)
			delegateProvider := inst.Provider
			observer := &programsGoalOriginProvider{wu5ScriptedProvider: f.provider}
			inst.Provider = observer
			count, result := programsCountInput(t, f, child, fmt.Sprintf("s79-hops-%d", depth), programsWebInput())
			joinGoalFixtureRuns(t, f.al)
			inst.Provider = delegateProvider
			want := "16" // FR-041: only MORE THAN sixteen hops is unresolved.
			if depth == 17 {
				want = "0"
			}
			assert.Equal(t, want, count, "S79/D5: actual %d-hop command byte count", depth)
			if depth == 17 {
				assert.Contains(t, result, programsUnresolvedNotice, "S79/D5: exact hop-bound unresolved notice")
				origins := observer.observed()
				require.GreaterOrEqual(t, len(origins), 2)
				for _, actual := range origins[:2] {
					assert.Equal(t, bus.UnresolvedProvenance(), actual, "S79/D5: the real seventeen-hop receiving context is unresolved")
				}
			} else {
				assert.NotContains(t, result, "Connected programs were not used for this command:", "S79 boundary: readable %d-hop work is still eligible", depth)
			}
			t.Logf("actual persisted helper hops=%d command_ran=true count=%s expected=%s", depth, count, want)
		}
	})
}
