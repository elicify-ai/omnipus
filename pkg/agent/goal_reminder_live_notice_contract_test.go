package agent

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent R2 oracle from the live-visibility dispatch, not the production
// notice constant or a private notice/entry-ID builder.
const grLiveNoticeText = "Goal reminder: continuing work toward the goal"

// TestGoalReminderLiveNoticeContract is deliberately separate from the older
// history-only assertion: deleting ONLY TurnStartPayload.ChatNotice must fail
// here even when the public transcript still contains the correct system row.
// No parallel subtests: the existing real keeper fixture uses global state.
func TestGoalReminderLiveNoticeContract(t *testing.T) {
	t.Run("FirstReminderCarriesLiveSystemNoticeMatchingReopenedHistory", grLiveFirstReminderNotice)
	t.Run("SecondReminderCarriesDistinctLiveNoticeAndPreservesFirst", grLiveSecondReminderNotice)
}

func grLiveFirstReminderNotice(t *testing.T) {
	h := newGRHarness(t)
	prior := h.completedRoot(t)
	h.arm(t)
	sub := grLiveNoticeSubscribe(t, h)
	drops := h.al.EventDrops(EventKindTurnStart)

	run := h.system(h.reminder(t)) // actual keeper -> notifier -> bus message -> receiver
	call := grAwaitCall(t, h.root, run)
	h.assertAdmission(t, call, prior)
	started := grLiveNoticeAwaitTurnStart(t, sub)
	notice := grLiveNoticeAssertFrame(t, h, started, call)
	id, stamp := *notice.Id, *notice.Timestamp

	h.finish(t, run, call)
	grLiveNoticeAssertHistory(t, h, notice)
	assert.Equal(t, id, *notice.Id, "R2: completing the turn must not replace the published notice ID")
	assert.Equal(t, stamp, *notice.Timestamp, "R2: completing the turn must preserve the published notice timestamp")
	assert.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grLiveNoticeText), "R2: one real reminder has one visible system event")
	assert.Equal(t, drops, h.al.EventDrops(EventKindTurnStart), "the live-subscription instrument must not lose a TurnStart event")
}

func grLiveSecondReminderNotice(t *testing.T) {
	h := newGRHarness(t)
	prior := h.completedRoot(t)
	h.arm(t)
	sub := grLiveNoticeSubscribe(t, h)
	drops := h.al.EventDrops(EventKindTurnStart)

	firstRun := h.system(h.reminder(t))
	firstCall := grAwaitCall(t, h.root, firstRun)
	h.assertAdmission(t, firstCall, prior)
	first := grLiveNoticeAssertFrame(t, h, grLiveNoticeAwaitTurnStart(t, sub), firstCall)
	firstID, firstStamp := *first.Id, *first.Timestamp
	h.finish(t, firstRun, firstCall)
	prior = h.load(t)

	// Age only the real goal's activity clock. The first actual turn must
	// re-arm the keeper; no test clears its settling/consumption markers.
	h.age(t)
	secondRun := h.system(h.reminder(t))
	secondCall := grAwaitCall(t, h.root, secondRun)
	h.assertAdmission(t, secondCall, prior)
	second := grLiveNoticeAssertFrame(t, h, grLiveNoticeAwaitTurnStart(t, sub), secondCall)
	assert.NotEqual(t, firstID, *second.Id, "R2: distinct actual reminders must carry distinct live notice IDs")
	assert.Equal(t, firstID, *first.Id, "R2: the second reminder must not overwrite the first published notice")
	assert.Equal(t, firstStamp, *first.Timestamp, "R2: the first notice's timestamp must remain stable")

	h.finish(t, secondRun, secondCall)
	grLiveNoticeAssertHistory(t, h, first)
	grLiveNoticeAssertHistory(t, h, second)
	assert.Equal(t, 2, grHasSystemLine(h.visibleHistory(t), grLiveNoticeText), "R2: both real reminders remain separate visible system events")
	assert.Equal(t, drops, h.al.EventDrops(EventKindTurnStart), "the live-subscription instrument must not lose either TurnStart event")
}

func grLiveNoticeSubscribe(t *testing.T, h *grHarness) EventSubscription {
	t.Helper()
	// Room for the bounded single-turn event burst while the real model
	// provider is held. The test also checks the actual TurnStart drop count.
	sub := h.al.SubscribeEvents(64)
	require.NotEqual(t, uint64(0), sub.ID, "SETUP: subscribe to the real agent event bus, not an unwired/closed channel")
	t.Cleanup(func() { h.al.UnsubscribeEvents(sub.ID) })
	return sub
}

func grLiveNoticeAwaitTurnStart(t *testing.T, sub EventSubscription) Event {
	t.Helper()
	watchdog := time.NewTimer(grWatchdog) // deadlock guard, never a success/timing oracle
	defer watchdog.Stop()
	for {
		select {
		case event, open := <-sub.C:
			require.True(t, open, "live event subscription closed before the actual TurnStart")
			if event.Kind == EventKindTurnStart {
				// Never filter for a nonnil ChatNotice: the missing-carrier
				// mutation must produce the direct nil-notice failure below.
				return event
			}
		case <-watchdog.C:
			t.Fatal("R2: actual keeper-driven admitted turn reached its provider but no TurnStart arrived through the real live subscription")
		}
	}
}

func grLiveNoticeAssertFrame(t *testing.T, h *grHarness, event Event, call *grCall) generated.ReplayMessageFrame {
	t.Helper()
	require.NotNil(t, call.turn, "SETUP: correlate the event to the actually executing turn")
	assert.Equal(t, testDefaultAgentID, event.Meta.AgentID, "the live notice must come from the real goal's working agent")
	assert.Equal(t, call.turn.turnID, event.Meta.TurnID, "R2: inspect this admitted reminder's TurnStart, not an unrelated or earlier event")
	start, ok := event.Payload.(TurnStartPayload)
	require.True(t, ok, "TurnStart must contain the actual typed production payload")
	assert.True(t, start.IsRoot, "the real keeper reminder is an ordinary root turn")
	assert.Equal(t, "webchat", start.Channel)
	assert.Equal(t, "chat-"+h.id, start.ChatID)
	require.NotNil(t, start.ChatNotice, "R2: live TurnStart must carry the generated reminder notice; correct saved history alone cannot make this pass")
	notice := *start.ChatNotice
	assert.Equal(t, "replay_message", notice.Type, "reuse the existing generated chat frame")
	assert.Equal(t, "system", notice.Role, "R2: a live reminder must never be a user bubble")
	assert.Equal(t, grLiveNoticeText, notice.Content, "R2: live chat must receive the independently specified plain system line")
	assert.Equal(t, h.id, notice.SessionId, "R2: route the live notice to the actual conversation")
	require.NotNil(t, notice.GoalId, "R2: preserve the real active goal association")
	assert.Equal(t, h.goalID, *notice.GoalId)
	require.NotNil(t, notice.Id, "R2: the live notice needs its persisted entry ID for stable deduplication")
	require.NotEmpty(t, *notice.Id)
	require.NotNil(t, notice.Timestamp, "R2: the live notice must carry the persisted event timestamp")
	require.NotEmpty(t, *notice.Timestamp)
	grLiveNoticeAssertHistory(t, h, notice)
	return notice
}

func grLiveNoticeAssertHistory(t *testing.T, h *grHarness, notice generated.ReplayMessageFrame) {
	t.Helper()
	require.NotNil(t, notice.Id)
	require.NotNil(t, notice.Timestamp)
	require.NotNil(t, notice.GoalId)
	stamp, err := time.Parse(time.RFC3339Nano, *notice.Timestamp)
	require.NoError(t, err, "R2: live timestamp must be a valid contract date-time, got %q", *notice.Timestamp)
	assert.False(t, stamp.IsZero(), "R2: a zero timestamp cannot identify a real reminder event")
	var matching []session.TranscriptEntry
	for _, entry := range h.visibleHistory(t) { // reopens the real store/public projection
		if entry.ID == *notice.Id {
			matching = append(matching, entry)
		}
	}
	require.Len(t, matching, 1, "R2: live notice ID %q must identify exactly one reopened public history row", *notice.Id)
	row := matching[0]
	assert.Equal(t, "system", string(row.Type))
	assert.Equal(t, "system", row.Role)
	assert.Equal(t, grLiveNoticeText, row.Content)
	assert.Equal(t, h.goalID, row.GoalID)
	assert.Equal(t, notice.Role, row.Role, "R2: live/history role must agree")
	assert.Equal(t, notice.Content, row.Content, "R2: live/history text must agree")
	assert.Equal(t, *notice.GoalId, row.GoalID, "R2: live/history goal association must agree")
	assert.Equal(t, row.Timestamp.UTC(), stamp.UTC(), "R2: live/history timestamp must be the exact same instant, not a fresh timestamp on display")
	if notice.AgentId != nil {
		assert.Equal(t, row.AgentID, *notice.AgentId, "optional live agent metadata must agree with its durable row")
	}
}
