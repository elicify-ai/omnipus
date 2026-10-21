package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoalReminderCompletedOutcomeControls(t *testing.T) {
	t.Run("PendingPublicationDoesNotAdvance", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		msg := h.reminder(t)
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "a notification waiting for its receiver is not a completed outcome")
		grNoPublishedReminder(t, h.al)
		grTextReminder(t, h, msg)
	})
	t.Run("RunningFifthCannotPauseBeforeCompletion", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		msg := h.reminder(t)
		grSetStreak(t, h, 4)
		run := h.system(msg)
		call := grAwaitCall(t, h.root, run)
		require.Equal(t, session.LifecycleRunning, h.load(t).State)
		h.age(t)
		h.assertNoKeeperWake(t, "the fifth reminder still executing at its actual provider")
		assert.Equal(t, 4, goalRecordForSession(t, h.id).ZeroOutputPushes)
		assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
		h.finish(t, run, call)
		grAwaitOwner(t, call)
		h.age(t)
		grAssertFifthPause(t, h)
	})
	t.Run("FailedRealNotificationDoesNotAdvance", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		grSetStreak(t, h, 1) // Below both old and new brakes: this cannot pass merely by hitting a cap.
		h.al.bus.Close()
		require.ErrorIs(t, h.al.bus.PublishInbound(context.Background(), bus.InboundMessage{}), bus.ErrBusClosed, "positive failure instrument: real bus is closed")
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "failed notification produces no completed reminder outcome")
		assert.Equal(t, 1, h.root.count(), "only the actual bootstrap root executed")
	})
	t.Run("StaleWakeAfterHumanDoesNotAdvance", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		stale := h.reminder(t)
		run := h.human(t, "Use my later real instruction, not the pending wake.")
		call := grAwaitCall(t, h.root, run)
		h.finish(t, run, call)
		before := goalRecordForSession(t, h.id).ZeroOutputPushes
		h.rejectReminder(t, stale, "obsolete reminder superseded by a genuine human execution")
		assert.Equal(t, before, goalRecordForSession(t, h.id).ZeroOutputPushes)
		assert.Equal(t, 0, before, "human input resets the original goal's consecutive streak")
	})
	t.Run("RepeatedAssessmentOfOneCompletionCannotSpendTwice", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		grTextReminder(t, h, h.reminder(t))
		h.age(t)
		pending := h.reminder(t)
		assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes)
		for i := 0; i < 3; i++ {
			h.age(t)
			h.al.goalQuietWindowSettle(time.Now())
			assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "one actual completion is assessed only once, even across further ticks")
			grNoPublishedReminder(t, h.al)
		}
		grTextReminder(t, h, pending)
	})
	t.Run("EndedGoalDoesNotDispatchOrAssess", func(t *testing.T) {
		h := grNewRecordedBrake(t)
		grSetStreak(t, h, 1)
		_, handled := h.al.clearGoalStatus(h.id, h.al.GetSessionStore(), goalClearNoteUser)
		require.True(t, handled)
		before, loadErr := resolveGoalRecordStore().Get(h.goalID)
		require.NoError(t, loadErr)
		require.NotEqual(t, "active", string(before.State))
		h.al.goalQuietWindowSettle(time.Now())
		grNoPublishedReminder(t, h.al)
		after, loadErr := resolveGoalRecordStore().Get(h.goalID)
		require.NoError(t, loadErr)
		assert.Equal(t, before.ZeroOutputPushes, after.ZeroOutputPushes)
	})
}

func TestGoalReminderSuppressionDoesNotAssess(t *testing.T) {
	for _, state := range []string{"Stop", "PendingQuestion", "WaitingOnUser", "Blocked"} {
		t.Run(state, func(t *testing.T) {
			h := grNewRecordedBrake(t)
			switch state {
			case "Stop":
				run := h.human(t, "Keep working until I press the real Stop.")
				call := grAwaitCall(t, h.root, run)
				grStop(t, h)
				h.root.release(call)
				grJoin(t, run)
				grAwaitOwner(t, call)
				stopped := h.load(t)
				require.Equal(t, session.LifecycleStopped, stopped.State, "the control must land a real Stop, not target an already terminal root")
				require.NotNil(t, stopped.StopNote)
			case "PendingQuestion":
				reg := askuser.NewRegistry(h.al.GetSessionStore(), nil, askuser.Options{})
				t.Cleanup(reg.Quiesce)
				require.NoError(t, reg.CreatePending(&askuser.PendingSet{
					CardID: "r6-pending-question", TranscriptSessionID: h.id,
					RoutingSessionKey: "agent:" + testDefaultAgentID + ":session:" + h.id,
					AgentID:           testDefaultAgentID, Channel: "webchat", ChatID: "chat-" + h.id,
					Owner: "goal-reminder-owner", CreatedAt: time.Now().UTC(), Status: askuser.StatusPending,
					Questions: []askuser.Question{{Header: "Scope", Question: "Which scope should the work use?", Options: []askuser.Option{{Label: "This"}, {Label: "Other"}}}},
				}))
				h.al.SetAskUserRegistry(reg)
				require.True(t, h.al.goalHasParkedCard(h.id))
			case "WaitingOnUser":
				grActionReminder(t, h, h.reminder(t), "goal_claim", map[string]any{"status": "waiting_on_user", "evidence": "The operator must answer before work continues."})
				require.True(t, h.al.goalIsWaitingOnUser(h.goalID))
			case "Blocked":
				grActionReminder(t, h, h.reminder(t), "goal_claim", map[string]any{"status": "blocked", "evidence": "The external dependency is unavailable."})
				require.True(t, h.al.goalIsBlocked(h.goalID))
			}
			grSetStreak(t, h, 1)
			h.age(t)
			h.assertNoKeeperWake(t, state)
			assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes)
		})
	}
}

// No new signal source is introduced: use the existing real claim tool and
// the existing deferred-adjudication completion hook only as a JOIN barrier.
func grBrakeClaimToolNotProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 2)
	done := make(chan struct{}, 1)
	old := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(id string) {
		if id == h.id {
			done <- struct{}{}
		}
	}
	defer func() { goalDeferredAdjudicationDoneFn = old }()
	p := grActionReminder(t, h, msg, "goal_claim", map[string]any{"status": "met", "evidence": "I assert completion, but produced no new work."})
	require.Contains(t, p.toolText, `"status":"met"`, "actual successful tool result, not a prose-only stand-in")
	select {
	case <-done:
	case <-time.After(grWatchdog):
		t.Fatal("actual claim adjudication did not join")
	}
	rec := goalRecordForSession(t, h.id)
	require.NotNil(t, rec.LatestClaim)
	require.Equal(t, "met", string(rec.LatestClaim.Status))
	require.Equal(t, "active", string(rec.State), "the deterministic external Judge rejects the unsupported claim")
	// An actual unmet verdict may enqueue its own non-idle steer. Consume it
	// through the production receiver; never mistake it for a keeper outcome.
	select {
	case follow := <-h.al.bus.InboundChan():
		grTextReminder(t, h, follow)
	default:
	}
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "successful goal_claim-only work is excluded from productive executed tools")
	grTextReminder(t, h, next)
}

// The public goal store is reopened by the normal assertions. This helper is
// used only to arrange a recordless record, not to forge a runtime identity.
func grNewRecordlessBrake(t *testing.T) *grHarness {
	t.Helper()
	h := grNewRecordedBrake(t)
	_, err := resolveGoalRecordStore().Update(h.goalID, func(g *goal.Goal) error {
		g.Criteria = nil
		g.Definition = ""
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, goalRecordForSession(t, h.id).Criteria)
	return h
}
