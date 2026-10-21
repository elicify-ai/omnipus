package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderContract derives its oracles from dispatch R1-R3, not from
// implementation helpers. Do not run in parallel: the keeper state is global.
func TestGoalReminderContract(t *testing.T) {
	t.Run("CompletedRootReminderLaunchesReviewer", func(t *testing.T) {
		h := newGRHarness(t)
		prior := h.completedRoot(t)
		h.arm(t)
		run := h.system(h.reminder(t))
		call := grAwaitCall(t, h.root, run)
		h.assertAdmission(t, call, prior)
		launcher := NewSteerLauncher(h.al)
		child, err := launcher.Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: h.id, TargetAgentID: grReviewerID,
			Task: "Independently review the work toward this goal.", Label: "Independent reviewer",
			Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "reminder-review"},
		})
		if assert.NoError(t, err, "R1: real reviewer launch during a real keeper turn must not refuse its parent as inactive") {
			rec, loadErr := session.NewLifecycleStore(h.al.GetSessionLifecycleStore().Dir()).Load(child.SessionID)
			require.NoError(t, loadErr)
			require.NotNil(t, rec.SteeredBy)
			assert.Equal(t, h.id, rec.SteeredBy.SteeringSessionID)
			assert.Equal(t, h.id, rec.SteeredBy.RootSessionID)
			assert.Equal(t, grReviewerID, rec.AgentID)
		}
		h.root.release(call)
		result := grJoin(t, run)
		require.NoError(t, result.err)
		assert.Equal(t, grReply, result.reply)
	})
}
