package agent

// SCRATCH SUPPLEMENTARY CAUSE CONTROLS ONLY. Intended new primary:
// pkg/agent/goal_pause_b1_edges_contract_test.go. Real B1 bell/wiring oracles now
// live in the gateway boot composition using existing public PlanEngine.Tick.
// These checks alone are NOT bell reachability or newly earned RED.
import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGoalPauseB1EdgesContract(t *testing.T) {
	t.Run("ThreeActualFailedRemindersReachSharedPause", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		p := r2InstallFailureProvider(t, h)
		for n := 0; n < 3; n++ {
			r2FailedReminder(t, h, p, r2ReplyProviderError)
		}
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		grNoPublishedReminder(t, h.al)
		require.Equal(t, r2FailedTurnPause, goalRecordForSession(t, h.id).LatestReason)
		require.True(t, h.al.goalIsWaitingOnUser(h.goalID))
		require.Len(t, r2FailurePauseRows(t, h), 1)
	})
	t.Run("FiveActualCompletedUnproductiveRemindersReachSharedPause", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		for n := 0; n < 5; n++ {
			h.age(t)
			grTextReminder(t, h, h.reminder(t))
		}
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		grNoPublishedReminder(t, h.al)
		require.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes)
		require.True(t, h.al.goalIsWaitingOnUser(h.goalID))
		require.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	})
}
