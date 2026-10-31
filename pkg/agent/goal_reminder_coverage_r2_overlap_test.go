package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// W3/M13 is a stored-reader contract, not an executed check/task producer.
// Both canonical runs stay open: no CloseRun/completion can mask evidence.
func TestGoalReminderCompatibleRunWindowsRejectAmbiguousEvidence(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	store := GetTaskStore(h.al)
	tk := mustCreateTask(t, store, &task.Task{
		Title: "competing evidence windows", WorkspaceID: testHarnessWorkspaceMembershipID,
		AgentID: testDefaultAgentID, Status: task.StatusNext,
	})
	ownedSession := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	owned := r6GateOpenRun(t, h, tk.ID, h.id, ownedSession, nil)
	r6GateWaitPastSecond(t, owned.StartedAt)
	positive := r6GateRecordEvidence(t, h, tk.ID, "r2-unique-owned", 1)
	r6GateRequireOpenWindow(t, store, owned, positive, ownedSession)
	runs, err := store.ListRuns(tk.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1, "SETUP: unique owned interior window is the positive control")
	grSetStreak(t, h, 1)
	h.age(t)
	fresh := h.reminder(t)
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "SETUP: actual unique owned evidence must reset before ambiguity is introduced")
	grTextReminder(t, h, fresh)

	foreignInitiator := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	foreignSession := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	r6GatePersistRunLifecycle(t, h, tk.ID, foreignInitiator, foreignSession)
	occurrence := time.Now().UnixMilli()
	foreign, created, err := store.OpenRun(tk.ID, &occurrence, task.RunKindManual, foreignSession, []string{h.id})
	require.NoError(t, err)
	require.True(t, created, "SETUP: distinct occurrence must create a competing canonical run, not deduplicate the owned run")
	require.NotEqual(t, owned.RunID, foreign.RunID)
	require.NotNil(t, foreign.OccurrenceMs)
	require.Equal(t, occurrence, *foreign.OccurrenceMs)
	r6GateWaitPastSecond(t, foreign.StartedAt)
	ambiguous := r6GateRecordEvidence(t, h, tk.ID, "r2-compatible-with-both", 1)
	require.NotEqual(t, positive.ID, ambiguous.ID)
	r6GateRequireOpenWindow(t, store, owned, ambiguous, ownedSession)
	r6GateRequireOpenWindow(t, store, foreign, ambiguous, foreignSession)
	runs, err = store.ListRuns(tk.ID)
	require.NoError(t, err)
	require.Len(t, runs, 2, "SETUP: the real reader must receive BOTH compatible retained windows")
	for _, run := range runs {
		require.Nil(t, run.EndedAt, "SETUP: no new owned completion may credit the ambiguous phase")
		require.True(t, r6GateRunSecond(t, ambiguous.RecordedAt).After(r6GateRunSecond(t, run.StartedAt)))
	}
	scope, err := h.al.reminderScope(h.id)
	require.NoError(t, err)
	require.True(t, scope.sessions[ownedSession])
	require.False(t, scope.sessions[foreignSession], "SETUP: recipient membership is not canonical goal ownership")
	grSetStreak(t, h, 2)
	r2AssertAmbiguousEvidenceDoesNotLatch(t, h, scope)
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "a completed text-only reminder with only ambiguous new evidence is unproductive")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	grTextReminder(t, h, next)
}

func r2AssertAmbiguousEvidenceDoesNotLatch(t *testing.T, h *grHarness, scope *goalReminderScope) {
	t.Helper()
	rec := goalRecordForSession(t, h.id)
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	before := make(map[string]time.Time, len(work.events))
	for key, at := range work.events {
		before[key] = at
	}
	priorLatch := work.observedProgress
	pending := work.pending
	progress, err := h.al.reminderEvidence(rec, scope, work, goalReminderWatermark(h.goalID))
	after := make(map[string]time.Time, len(work.events))
	for key, at := range work.events {
		after[key] = at
	}
	afterLatch := work.observedProgress
	afterPending := work.pending
	work.mu.Unlock()
	require.False(t, priorLatch, "SETUP: the owned positive must already be consumed through the real keeper")
	require.NotNil(t, pending, "SETUP: a genuine separately completed text reminder must be pending")
	assert.NoError(t, err)
	assert.False(t, progress, "a second compatible window makes this actual evidence unattributable")
	assert.False(t, afterLatch, "unattributable evidence must not latch progress before being rejected")
	assert.Equal(t, before, after, "the rejected record must not mutate the seen-event map")
	assert.Same(t, pending, afterPending, "a reader probe must not manufacture or consume the real pending outcome")
}
