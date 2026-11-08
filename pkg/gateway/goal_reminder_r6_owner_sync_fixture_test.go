package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lifecycle-completed row can precede the producing disposition.done. Keep
// the original 10s/10ms guard, but drive the ACTUAL keeper until that owner's
// assessment commits the same goal's fifth outcome. The fixture's synchronous
// hub tap delivers the generated pause frame before that keeper tick returns.
// No frame is fabricated, drained/requeued, or accepted on a mere idle hint.
func r6WarnAwaitProducingPause(t *testing.T, f *r6PublicFixture, before int) {
	t.Helper()
	require.Eventually(t, func() bool {
		assert.Equal(t, before, f.count(), "owner-settlement polling must not publish any extra notification")
		rec, err := f.al.GetSessionLifecycleStore().Load(f.root)
		if err != nil || rec.State != session.LifecycleCompleted || f.al.GetActiveTurnHookForSession(f.root) != nil {
			return false // readiness hint only, not full completion
		}
		f.age(t)
		f.engine.Tick(context.Background())
		assert.Equal(t, before, f.count(), "the fifth completion must not dispatch a sixth reminder while its owner settles")
		persisted, err := goal.NewStore(config.OmnipusHomeDir()).Get(f.goalID)
		require.NoError(t, err)
		require.Equal(t, f.goalID, persisted.GoalID)
		require.Equal(t, f.root, persisted.ActiveSessionID)
		assert.Contains(t, []int{4, 5}, persisted.ZeroOutputPushes, "only the unassessed fourth or committed fifth count is valid while waiting")
		return persisted.ZeroOutputPushes == 5
	}, 10*time.Second, 10*time.Millisecond, "the actual producing owner must settle before its same-goal pause is asserted")
}
