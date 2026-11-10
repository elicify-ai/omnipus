package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r6GatePausePublishBudget is the publish bound asyncNotifierImpl.Notify
// already uses. A pause notice is not a sixth reminder and must not outlive it.
const r6GatePausePublishBudget = 5 * time.Second

// TestGoalReminderR6NonWebPausePublishReturns is the fifth completed text
// reminder on a telegram route. The outbound buffer is full. PlanEngine.Tick
// must return inside the existing publish budget. The streak stays five and
// no sixth reminder is published. Tick is called directly so the test does
// not also start the engine loop.
func TestGoalReminderR6NonWebPausePublishReturns(t *testing.T) {
	h := grNewRecordedBrake(t)
	fifth := h.reminder(t)
	grSetStreak(t, h, 4)
	grTextReminder(t, h, fifth)
	h.al.recordGoalRouting(h.id, h.goalID, "telegram", "tg-"+h.id,
		"agent:"+testDefaultAgentID+":session:"+h.id, testDefaultAgentID)
	h.age(t)
	r6GateRequirePauseRoute(t, h)

	store := plan.New(filepath.Join(config.OmnipusHomeDir(), "plans"))
	_, err := store.List(plan.Filter{})
	require.NoError(t, err, "SETUP: Tick must be able to list plans before it can reach the pause")
	engine := NewPlanEngine(h.al, store, h.al.taskStore, GetTaskExecutor(h.al))
	h.al.SetPlanEngine(engine)
	filled := r6GateFillOutbound(t, h.al.bus)

	done := make(chan struct{})
	go func() {
		engine.Tick(context.Background())
		close(done)
	}()
	joined := false
	t.Cleanup(func() {
		if !joined {
			r6GateUnblockPause(done, h.al.bus.OutboundChan())
		}
	})

	select {
	case <-done:
		joined = true
		require.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes, "SETUP: Tick returned without assessing the fifth completed reminder")
		require.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grPauseLine), "SETUP: a returned tick must have reached the pause notice")
	case <-time.After(r6GatePausePublishBudget):
		if !r6GateFifthPauseReached(t, h) {
			r6GateUnblockPause(done, h.al.bus.OutboundChan())
			t.Fatal("SETUP: Tick is still running but the pause notice was not written, so this is not the channel send")
		}
		t.Errorf("telegram pause publish did not return within %s, the budget asyncNotifierImpl.Notify already uses; outbound buffer held %d messages", r6GatePausePublishBudget, filled)
		if !r6GateUnblockPause(done, h.al.bus.OutboundChan()) {
			t.Fatal("CLEANUP: Tick did not return after the outbound bus was drained")
		}
		joined = true
	}

	select {
	case msg := <-h.al.bus.InboundChan():
		t.Errorf("the fifth unproductive outcome must not publish a sixth reminder, got %+v", msg)
	default:
	}
	assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes, "the pause records five outcomes and must not keep counting while the notice is published")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
}

func r6GateRequirePauseRoute(t *testing.T, h *grHarness) {
	t.Helper()
	require.Equal(t, steer.AudienceUser, h.al.audienceFor(context.Background(), steer.BoundaryFinalReply, h.id),
		"SETUP: the non-web pause publish runs only for a user audience")
	route, err := goalTriggers().routeFor(h.id)
	require.NoError(t, err)
	require.Equal(t, "telegram", route.channel, "SETUP: the pause route must be a non-web channel")
	require.NotEmpty(t, route.chatID)
}

func r6GateFillOutbound(t *testing.T, messageBus *bus.MessageBus) int {
	t.Helper()
	filled := 0
	for messageBus.TryPublishOutbound(bus.OutboundMessage{Channel: "telegram", ChatID: "filler", Content: "filler"}) {
		filled++
		if filled > 10000 {
			t.Fatal("SETUP: outbound buffer did not fill")
		}
	}
	require.Greater(t, filled, 0, "SETUP: the outbound buffer must actually be full")
	return filled
}

func r6GateFifthPauseReached(t *testing.T, h *grHarness) bool {
	t.Helper()
	if goalRecordForSession(t, h.id).ZeroOutputPushes != 5 {
		return false
	}
	return grHasSystemLine(h.visibleHistory(t), grPauseLine) == 1
}

func r6GateUnblockPause(done <-chan struct{}, outbound <-chan bus.OutboundMessage) bool {
	timer := time.NewTimer(grWatchdog)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return true
		case <-outbound:
		case <-timer.C:
			return false
		}
	}
}
