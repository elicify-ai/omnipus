package agent

import (
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RAM-cold keeper bookkeeping, not a physical restart or UAT. Durable stores
// and the original real goal/session/notice remain exactly the same objects.
func r3ColdFailurePauseStatus(t *testing.T, statusBeforeCold bool) {
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	events := r3CaptureReviewEvents(t, h)
	grSetStreak(t, h, 2)
	for i := 0; i < 3; i++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
	}
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	firstRows := r2FailurePauseRows(t, h)
	require.Len(t, firstRows, 1, "SETUP: three ACTUAL failed turns must save the first pause")
	first := firstRows[0]
	require.NotEmpty(t, first.ID)
	require.False(t, first.Timestamp.IsZero())
	require.True(t, h.al.goalIsWaitingOnUser(h.goalID))
	require.Len(t, events.pauseNotices(), 1)
	originalReplay := events.pauseNotices()[0].ChatNotice
	require.NotNil(t, originalReplay.Id)
	require.NotNil(t, originalReplay.Timestamp)
	require.Equal(t, first.ID, *originalReplay.Id)
	require.Equal(t, first.Timestamp.Format(time.RFC3339Nano), *originalReplay.Timestamp)
	persistedGoal := goalRecordForSession(t, h.id)
	persistedLife := h.load(t)
	calls := h.root.count()
	if statusBeforeCold {
		r3RealPausedStatus(t, h)
	}
	resetGoalTriggerStateForTest() // existing seam: only process-local keeper maps
	h.al.SetGoalRouteSessionStore()
	require.Equal(t, persistedGoal, goalRecordForSession(t, h.id), "SETUP: cold RAM must not rewrite durable goal")
	require.Equal(t, persistedLife, h.load(t))
	if !statusBeforeCold {
		r3RealPausedStatus(t, h)
	}
	for tick := 0; tick < 3; tick++ {
		h.al.goalQuietWindowSettle(time.Now())
		rows := r2FailurePauseRows(t, h)
		assert.Len(t, rows, 1, "E71-CR-F4: a read-only user row and cold RAM do not start a new pause cycle")
		if len(rows) > 0 {
			assert.Equal(t, first.ID, rows[0].ID)
			assert.Equal(t, first.Timestamp, rows[0].Timestamp)
		}
		assert.Len(t, events.pauseNotices(), 1, "E71-CR-F4: the same committed pause must not emit a second replay notice")
		assert.Equal(t, persistedLife, h.load(t))
		assert.Equal(t, persistedGoal, goalRecordForSession(t, h.id))
		assert.Equal(t, calls, h.root.count())
		grNoPublishedReminder(t, h.al)
	}
	// A normal accepted human is different from a read-only command. Do not
	// make an older historical pause stand in for this NEW actual failure cycle.
	provider.mode.Store(r2ReplySuccess)
	human := h.human(t, "Continue this same goal for a new work cycle.")
	call := grAwaitCall(t, h.root, human)
	require.True(t, call.human)
	h.finish(t, human, call)
	grAwaitOwner(t, call)
	require.False(t, h.al.goalIsWaitingOnUser(h.goalID))
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	for failure := 0; failure < 3; failure++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
		if failure < 2 {
			assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "two NEW actual failures cannot re-pause the genuine resumed cycle")
		}
	}
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		grNoPublishedReminder(t, h.al)
	}
	rows := r2FailurePauseRows(t, h)
	assert.Len(t, rows, 2, "exactly the original and one genuine NEW three-failure pause must be saved")
	// Identify the new cycle by its location AFTER the real human transcript
	// row, not by a guessed notice format or whichever duplicate happened last.
	history := h.visibleHistory(t)
	lastHuman := -1
	for i, row := range history {
		if row.Role == "user" {
			lastHuman = i
		}
	}
	require.GreaterOrEqual(t, lastHuman, 0)
	var afterHuman []session.TranscriptEntry
	for _, row := range history[lastHuman+1:] {
		if row.Role == "system" && row.Content == r2FailedTurnPause {
			afterHuman = append(afterHuman, row)
		}
	}
	require.Len(t, afterHuman, 1, "genuine new cycle needs its own one actual pause, never reuse any old saved notice")
	assert.NotEqual(t, first.ID, afterHuman[0].ID)
	live := events.pauseNotices()
	assert.Len(t, live, 2, "only the two genuine failure cycles may emit replay notices")
	newCycleReplays := 0
	for _, event := range live {
		if event.ChatNotice.Id != nil && *event.ChatNotice.Id == afterHuman[0].ID {
			newCycleReplays++
		}
	}
	assert.Equal(t, 1, newCycleReplays)
	assert.True(t, afterHuman[0].Timestamp.After(first.Timestamp))
	assert.Equal(t, h.goalID, afterHuman[0].GoalID)
	assert.True(t, h.al.goalIsWaitingOnUser(h.goalID))
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
}

func r3RealPausedStatus(t *testing.T, h *grHarness) {
	t.Helper()
	goalBefore := goalRecordForSession(t, h.id)
	lifeBefore := h.load(t)
	callsBefore := h.root.count()
	before, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	// Use the real non-web intake: processMessage itself appends the USER row
	// before command handling, unlike a fixture-only manually authored notice.
	msg := bus.InboundMessage{
		Channel: "telegram", ChatID: "chat-" + h.id, SessionID: h.id,
		SessionKey:    fmt.Sprintf("agent:%s:session:%s", testDefaultAgentID, h.id),
		GatewayUserID: "goal-reminder-owner", UserInitiated: true, Content: "/goal status",
		Sender: bus.SenderInfo{CanonicalID: "human:goal-reminder-owner"},
	}
	answer, _, err := h.al.processMessage(h.ctx, msg)
	require.NoError(t, err)
	require.Contains(t, answer, r2FailedTurnPause, "SETUP: real read-only goal status must report the saved failure pause")
	after, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	require.Greater(t, len(after), len(before))
	found := false
	for _, row := range after[len(before):] {
		found = found || (row.Role == "user" && row.Content == msg.Content)
	}
	require.True(t, found, "SETUP: actual status intake must persist a real USER row")
	require.Equal(t, goalBefore, goalRecordForSession(t, h.id), "read-only status must not reset the failure brake")
	require.Equal(t, lifeBefore, h.load(t), "read-only status must not mint execution/generation")
	require.Equal(t, callsBefore, h.root.count(), "read-only status must not reach a provider")
	require.Nil(t, h.al.ownLiveTurn(h.id))
	require.Nil(t, h.al.executionDispositionFor(h.al.executionClaimFor(lifeBefore)))
}
