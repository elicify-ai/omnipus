package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A1 human reset is independent of successful AUTOMATIC reminders. The genuine
// human's success must not be confused with that separate reset control.
func TestGoalReminderHumanReplyResetsFailuresWithoutReminderSuccess(t *testing.T) {
	h := grNewRecordedBrake(t)
	provider := r2InstallFailureProvider(t, h)
	live := r2FailureNotices(t, h)
	grSetStreak(t, h, 2)
	for failure := 0; failure < 3; failure++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
	}
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	r2NoFourthWake(t, h)
	r2AssertFailurePause(t, h, live)
	firstRows := r2FailurePauseRows(t, h)
	require.Len(t, firstRows, 1)
	provider.mode.Store(r2ReplySuccess)
	human := h.human(t, "Continue this same goal and begin a new failure sequence.")
	call := grAwaitCall(t, h.root, human)
	require.True(t, call.human)
	h.finish(t, human, call)
	grAwaitOwner(t, call)
	require.False(t, h.al.goalIsWaitingOnUser(h.goalID))
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	// No successful automatic reminder occurs between the two failure cycles.
	for failure := 0; failure < 2; failure++ {
		r2FailedReminder(t, h, provider, r2ReplyProviderError)
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "the first two NEW failures after a genuine reply must not re-pause")
		assert.NotEqual(t, r2FailedTurnPause, goalRecordForSession(t, h.id).LatestReason)
		assert.Len(t, r2FailurePauseRows(t, h), 1, "the old pause must not become a new pause before three new failures")
		assert.Len(t, live(), 1)
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	}
	r2FailedReminder(t, h, provider, r2ReplyProviderError)
	for tick := 0; tick < 2; tick++ {
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		r2NoFourthWake(t, h)
		assert.True(t, h.al.goalIsWaitingOnUser(h.goalID), "the third NEW failure may pause the resumed goal again")
		assert.Equal(t, r2FailedTurnPause, goalRecordForSession(t, h.id).LatestReason)
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	}
	rows := r2FailurePauseRows(t, h)
	require.Len(t, rows, 2, "one actual pause per distinct three-failure sequence")
	assert.Equal(t, firstRows[0].ID, rows[0].ID)
	assert.NotEqual(t, rows[0].ID, rows[1].ID, "a new failure cycle must identify its own actual producing pause")
	events := live()
	require.Len(t, events, 2)
	for i, event := range events {
		require.NotNil(t, event.ChatNotice)
		require.NotNil(t, event.ChatNotice.Id)
		assert.Equal(t, rows[i].ID, *event.ChatNotice.Id)
		assert.Equal(t, r2FailedTurnPause, event.ChatNotice.Content)
		assert.Equal(t, "waiting_on_user", event.State)
	}
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
}

func r2FailurePauseRows(t *testing.T, h *grHarness) []session.TranscriptEntry {
	t.Helper()
	var rows []session.TranscriptEntry
	for _, row := range h.visibleHistory(t) {
		if row.Type == session.EntryTypeSystem && row.Role == "system" && row.Content == r2FailedTurnPause {
			rows = append(rows, row)
		}
	}
	return rows
}
