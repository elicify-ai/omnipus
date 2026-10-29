package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// W4/M14: enqueue is an in-process connector handoff, not network delivery.
func TestGoalReminderConnectorPauseRetriesOnceAfterQueueRepair(t *testing.T) {
	h := grNewRecordedBrake(t)
	fifth := h.reminder(t)
	grSetStreak(t, h, 4)
	grTextReminder(t, h, fifth)
	beforeCalls := h.root.count()
	h.al.recordGoalRouting(h.id, h.goalID, "telegram", "tg-"+h.id,
		"agent:"+testDefaultAgentID+":session:"+h.id, testDefaultAgentID)
	r6GateRequirePauseRoute(t, h)
	store := plan.New(filepath.Join(config.OmnipusHomeDir(), "plans"))
	_, err := store.List(plan.Filter{})
	require.NoError(t, err)
	engine := NewPlanEngine(h.al, store, h.al.taskStore, GetTaskExecutor(h.al))
	h.al.SetPlanEngine(engine)
	var rejected atomic.Int32
	h.al.SetEventSyncTap(func(event Event) {
		payload, ok := event.Payload.(ErrorPayload)
		if event.Kind == EventKindError && ok && payload.SessionID == h.id && payload.Stage == "goal_record_read" &&
			strings.Contains(payload.Message, "outbound queue is full or closed; notice remains pending") {
			rejected.Add(1)
		}
	})
	t.Cleanup(func() { h.al.SetEventSyncTap(nil) })
	r6GateFillOutbound(t, h.al.bus)
	require.False(t, h.al.bus.TryPublishOutbound(bus.OutboundMessage{Channel: "telegram", Content: "full-queue proof"}), "SETUP: the open outbound queue must actually reject another item")
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		r2ConnectorTick(t, h, engine)
		grNoPublishedReminder(t, h.al)
		assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes)
		assert.Equal(t, beforeCalls, h.root.count(), "failed connector handoff cannot dispatch a sixth paid reminder")
	}
	require.Greater(t, rejected.Load(), int32(0), "SETUP: positively observe the real same-session rejected pause handoff")
	first := r2PublicPauseRow(t, h)
	fullQueue := r2DrainOutbound(t, h.al.bus)
	for _, message := range fullQueue {
		assert.NotEqual(t, grPauseLine, message.Content, "the rejected connector pause must not be counted as enqueued")
	}
	probe := bus.OutboundMessage{Channel: "telegram", ChatID: "r2-open-queue", Content: "repaired queue proof"}
	require.True(t, h.al.bus.TryPublishOutbound(probe), "SETUP: repair must prove the queue is OPEN, not merely empty and closed")
	require.Equal(t, []bus.OutboundMessage{probe}, r2DrainOutbound(t, h.al.bus))

	h.age(t)
	r2ConnectorTick(t, h, engine)
	delivered := r2DrainOutbound(t, h.al.bus)
	require.Len(t, delivered, 1, "the next real paused-goal observation must retry the previously rejected connector pause")
	assert.Equal(t, "telegram", delivered[0].Channel)
	assert.Equal(t, "tg-"+h.id, delivered[0].ChatID)
	assert.Equal(t, h.id, delivered[0].SessionID)
	assert.Equal(t, grPauseLine, delivered[0].Content)
	for tick := 0; tick < 3; tick++ {
		h.age(t)
		r2ConnectorTick(t, h, engine)
		assert.Empty(t, r2DrainOutbound(t, h.al.bus), "successful same-notice enqueue must not repeat on later observations")
		grNoPublishedReminder(t, h.al)
		assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes)
		assert.Equal(t, beforeCalls, h.root.count())
		notice := r2PublicPauseRow(t, h)
		assert.Equal(t, first.ID, notice.ID, "queue repair must keep the same durable public notice identity")
		assert.Equal(t, first.Timestamp, notice.Timestamp)
	}
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
}

func r2PublicPauseRow(t *testing.T, h *grHarness) session.TranscriptEntry {
	t.Helper()
	var rows []session.TranscriptEntry
	for _, entry := range h.visibleHistory(t) {
		if entry.Content == grPauseLine {
			rows = append(rows, entry)
		}
	}
	require.Len(t, rows, 1, "exactly one public transcript pause survives every failed and successful connector observation")
	assert.Equal(t, session.EntryTypeSystem, rows[0].Type)
	assert.Equal(t, "system", rows[0].Role)
	assert.Equal(t, h.goalID, rows[0].GoalID)
	require.NotEmpty(t, rows[0].ID)
	return rows[0]
}

func r2DrainOutbound(t *testing.T, messageBus *bus.MessageBus) []bus.OutboundMessage {
	t.Helper()
	var messages []bus.OutboundMessage
	for {
		select {
		case message, ok := <-messageBus.OutboundChan():
			require.True(t, ok, "SETUP: the connector queue must stay open throughout this scenario")
			messages = append(messages, message)
		default:
			return messages
		}
	}
}

func r2ConnectorTick(t *testing.T, h *grHarness, engine *PlanEngine) {
	t.Helper()
	done := make(chan struct{})
	go func() { engine.Tick(context.Background()); close(done) }()
	defer func() {
		select {
		case <-done:
		default:
			assert.True(t, r6GateUnblockPause(done, h.al.bus.OutboundChan()), "CLEANUP: every launched keeper tick must join")
		}
	}()
	select {
	case <-done:
		return
	case <-time.After(r6GatePausePublishBudget):
		reached := r6GateFifthPauseReached(t, h)
		joined := r6GateUnblockPause(done, h.al.bus.OutboundChan())
		require.True(t, joined, "CLEANUP: actual keeper must join after drain")
		require.True(t, reached, "SETUP: a blocked tick that never reached the fifth pause is not a connector witness")
		t.Error("full connector queue must not block the actual pause tick beyond the existing publish budget")
	}
}
