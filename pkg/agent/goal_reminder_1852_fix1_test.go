package agent

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Fix round 1 for #1852 (silent-failure-hunter, Important + Minor).
// Oracles from the issue and the review:
//   - a skipped helper never counts as progress — not through its transcript
//     and not through its committed final delivery either;
//   - helpers from before the goal never affect the reminder, also when the
//     activity watermark is older than the goal start;
//   - the skip is logged visibly once per goal and helper, and again only
//     after the helper's store came back and was lost again.

// gr1852AddFinishedHelper records a terminal helper that committed its final
// delivery to the chat, as a real finished steered helper does
// (pkg/session/lifecycle.go only allows FinalDelivery on completed/failed).
func gr1852AddFinishedHelper(t *testing.T, h *grHarness, id string, state session.LifecycleState, createdAt, lastActivity time.Time) string {
	t.Helper()
	if id == "" { // readable control: a live session in the store
		meta, err := h.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
		require.NoError(t, err)
		id = meta.ID
	} else {
		require.Nil(t, h.al.ResolveSessionStore(id), "SETUP: an archived helper must be missing from the live session store")
	}
	payload := []byte(`{"text":"helper final answer"}`)
	rec := &session.LifecycleRecord{
		SessionID: id, Generation: 1, State: state,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: h.id,
		Origin:    &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy: &session.SteeredBy{SteeringSessionID: h.id, RootSessionID: h.id},
		CreatedAt: createdAt, LastActivityAt: lastActivity,
		FinalDelivery: &session.FinalDeliveryCommit{
			Generation: 1, CommitID: "run-" + id, MessageID: fmt.Sprintf("%s:%d:final", id, 1),
			Outcome: "final_answer", ParentSessionID: h.id,
			PayloadHash: fmt.Sprintf("%x", sha256.Sum256(payload)), Payload: payload,
		},
	}
	if state == session.LifecycleFailed {
		rec.FailedReason = "helper run failed"
	}
	require.NoError(t, h.al.GetSessionLifecycleStore().Persist(rec))
	stored, err := h.al.GetSessionLifecycleStore().Load(id)
	require.NoError(t, err)
	require.NotNil(t, stored.FinalDelivery, "SETUP: the finished helper must durably carry its final delivery")
	require.Equal(t, "run-"+id, stored.FinalDelivery.CommitID)
	require.Equal(t, state, stored.State)
	return id
}

// gr1852CollectWith runs the real assessment reader with a caller-owned
// window (so state carried between assessments is the real carrier) and
// returns the log lines it wrote.
func gr1852CollectWith(t *testing.T, h *grHarness, work *goalReminderWork, since time.Time) (goalReminderProgress, []map[string]any, error) {
	t.Helper()
	rec := goalRecordForSession(t, h.id)
	logPath := filepath.Join(t.TempDir(), "agent.log")
	require.NoError(t, logger.EnableFileLogging(logPath))
	work.mu.Lock()
	progress, _, err := h.al.collectGoalReminderProgress(h.id, rec, work, since, time.Now().UTC())
	work.mu.Unlock()
	logger.DisableFileLogging()
	return progress, gr1852ReadLog(t, logPath), err
}

func gr1852NewWork() *goalReminderWork {
	return &goalReminderWork{events: make(map[string]time.Time), processes: make(map[string]*tools.ProcessSession)}
}

// Important: an archived in-window helper that finished with a committed
// final delivery is skipped; its final delivery must not credit progress.
func TestGoalReminder1852SkippedFinishedHelperFinalDeliveryIsNotProgress(t *testing.T) {
	for _, state := range []session.LifecycleState{session.LifecycleCompleted, session.LifecycleFailed} {
		t.Run(string(state), func(t *testing.T) {
			h := grNewRecordedBrake(t)
			start := gr1852GoalStart(t, h)
			at := start.Add(20 * time.Minute) // finished during the goal, after `since`
			id := gr1852AddFinishedHelper(t, h, "session_1852FINISHED"+string(state), state, start.Add(5*time.Minute), at)

			work := gr1852NewWork()
			progress, lines, err := gr1852CollectWith(t, h, work, start)
			require.NoError(t, err, "a skipped helper is never fatal")
			assert.Equal(t, goalReminderProgress{}, progress, "a skipped helper's final delivery is not progress")
			assert.False(t, work.observedProgress, "a skipped helper must not latch progress")
			assert.Empty(t, work.events, "a skipped helper must not record any seen activity")
			assert.Len(t, gr1852LinesNaming(lines, id), 1, "the skip is still logged visibly")
		})
	}
}

// Important, through the real idle keeper: an existing streak of 4
// unproductive reminders is not reset by a skipped finished helper.
func TestGoalReminder1852SkippedFinishedHelperDoesNotResetStreak(t *testing.T) {
	for _, state := range []session.LifecycleState{session.LifecycleCompleted, session.LifecycleFailed} {
		t.Run(string(state), func(t *testing.T) {
			h := grNewRecordedBrake(t)
			start := gr1852GoalStart(t, h)
			gr1852AddFinishedHelper(t, h, "session_1852STREAK"+string(state), state, start.Add(5*time.Minute), time.Now().UTC().Add(-10*time.Minute))
			const priorStreak = 4 // one below goalZeroOutputPushMax (5): the next unproductive outcome pauses
			grSetStreak(t, h, priorStreak)
			msg := h.reminder(t) // real idle prepare path; fails if nothing is published
			assert.Equal(t, priorStreak, goalRecordForSession(t, h.id).ZeroOutputPushes,
				"the idle prepare path must not reset the streak for a skipped helper")
			grTextReminder(t, h, msg)
		})
	}
}

// Important, second scenario: with a stale watermark older than the goal
// start, a helper that finished before the goal started must not credit the
// goal through its final delivery (readable or archived alike).
func TestGoalReminder1852PreGoalFinishedHelperCannotCreditWithStaleWatermark(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	staleSince := start.Add(-2 * time.Hour)    // watermark left behind the (re)start
	finishedAt := start.Add(-30 * time.Minute) // after the stale watermark, before the goal start
	id := gr1852AddFinishedHelper(t, h, "session_1852PREGOALFINAL", session.LifecycleCompleted, start.Add(-90*time.Minute), finishedAt)

	work := gr1852NewWork()
	progress, lines, err := gr1852CollectWith(t, h, work, staleSince)
	require.NoError(t, err)
	assert.Equal(t, goalReminderProgress{}, progress, "a helper from before the goal never counts for the goal")
	assert.False(t, work.observedProgress)
	assert.Empty(t, gr1852LinesNaming(lines, id), "a helper from before the goal is not even opened")
}

// Minor: two consecutive assessments of the same goal emit one WARN for the
// same lost helper.
func TestGoalReminder1852SkipWarnsOncePerGoalAndHelper(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	at := start.Add(10 * time.Minute)
	lost := gr1852AddHelper(t, h, gr1852Helper{createdAt: at, lastActivity: at}, "session_1852WARNONCE")

	work := goalReminderWorkFor(h.goalID) // the real per-goal carrier the keeper uses
	_, first, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)
	_, second, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)
	require.Len(t, gr1852LinesNaming(first, lost), 1, "the first skip is logged")
	assert.Equal(t, "warn", gr1852LinesNaming(first, lost)[0]["level"])
	assert.Empty(t, gr1852LinesNaming(second, lost), "the same lost helper is not logged again for the same goal")

	// The dedup state is cleared with the goal's trigger state.
	h.al.clearGoalTriggerState("", h.goalID)
	_, third, err := gr1852CollectWith(t, h, goalReminderWorkFor(h.goalID), start)
	require.NoError(t, err)
	assert.Len(t, gr1852LinesNaming(third, lost), 1, "cleared goal state logs the skip again")
}

// Minor: a helper whose store comes back and is lost again is logged again.
func TestGoalReminder1852SkipWarnsAgainAfterStoreReturnsAndIsLostAgain(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	at := start.Add(10 * time.Minute)
	id := gr1852AddHelper(t, h, gr1852Helper{createdAt: at, lastActivity: at, readable: true}, "")
	store := h.al.GetSessionStore()
	live := filepath.Join(store.BaseDir(), id)
	archive := filepath.Join(t.TempDir(), id)
	archiveNow := func() {
		require.NoError(t, store.FlushAndEvictSessionMeta(id))
		require.NoError(t, os.Rename(live, archive))
		require.Nil(t, h.al.ResolveSessionStore(id), "SETUP: archived helper must be unresolvable")
	}
	restore := func() {
		require.NoError(t, os.Rename(archive, live))
		require.NotNil(t, h.al.ResolveSessionStore(id), "SETUP: restored helper must resolve again")
	}

	work := goalReminderWorkFor(h.goalID)
	archiveNow()
	_, lost1, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)
	restore()
	_, back, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)
	archiveNow()
	_, lost2, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)

	assert.Len(t, gr1852LinesNaming(lost1, id), 1, "first loss is logged")
	assert.Empty(t, gr1852LinesNaming(back, id), "a readable helper is not logged")
	assert.Len(t, gr1852LinesNaming(lost2, id), 1, "lost again after it came back: logged again")
}

// F1 control: a readable helper's real committed final delivery inside the
// window still counts as progress.
func TestGoalReminder1852ReadableFinishedHelperFinalDeliveryStillCounts(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	id := gr1852AddFinishedHelper(t, h, "", session.LifecycleCompleted, start.Add(5*time.Minute), start.Add(20*time.Minute))
	require.NotNil(t, h.al.ResolveSessionStore(id), "SETUP: the control helper is readable")

	work := gr1852NewWork()
	progress, lines, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)
	assert.True(t, progress.descendant, "a readable helper's committed final delivery is progress")
	assert.Contains(t, work.events, "child-final:run-"+id, "the credit comes from that helper's final delivery")
	assert.Empty(t, gr1852LinesNaming(lines, id), "a readable helper is not skipped")
}

// F1, ordinary count: with a skipped finished helper and no other progress,
// a completed text-only reminder is one ordinary unproductive outcome.
func TestGoalReminder1852SkippedFinishedHelperLeavesOrdinaryUnproductiveCount(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	gr1852AddFinishedHelper(t, h, "session_1852ORDINARY", session.LifecycleCompleted, start.Add(5*time.Minute), time.Now().UTC().Add(-10*time.Minute))
	first := h.reminder(t)
	grTextReminder(t, h, first)
	h.age(t)
	second := h.reminder(t)
	assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "one completed text-only reminder without progress")
	grTextReminder(t, h, second)
}

// F2: a helper whose store resolves but whose transcript really cannot be
// read is NOT the archived case: the assessment fails, the keeper consumes
// nothing, counts nothing and dispatches nothing, and recovers after repair.
func TestGoalReminder1852ReadableHelperTranscriptReadFailureHoldsTheAssessment(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	at := start.Add(10 * time.Minute)
	id := gr1852AddHelper(t, h, gr1852Helper{createdAt: at, lastActivity: at, readable: true}, "")

	first := h.reminder(t)
	grTextReminder(t, h, first)
	h.age(t)

	store := h.al.ResolveSessionStore(id)
	require.NotNil(t, store, "SETUP: the damaged helper's store still resolves")
	partition := filepath.Join(store.BaseDir(), id, "transcript.jsonl") // root-safe fault: pkg/session/transcript_dir_corrupt_test.go
	require.NoError(t, os.RemoveAll(partition))
	require.NoError(t, os.Mkdir(partition, 0o700))
	_, readErr := store.ReadTranscript(id)
	require.Error(t, readErr, "SETUP: the transcript read must really fail")

	_, _, err := gr1852CollectWith(t, h, gr1852NewWork(), start)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal reminder: read descendant activity", "a damaged transcript fails the assessment")

	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	pendingBefore := work.pending
	work.mu.Unlock()
	require.NotNil(t, pendingBefore, "SETUP: the completed reminder outcome is pending")
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "a held assessment counts nothing")
	work.mu.Lock()
	pendingAfter := work.pending
	work.mu.Unlock()
	assert.Same(t, pendingBefore, pendingAfter, "a held assessment does not consume the outcome")

	require.NoError(t, os.Remove(partition))
	second := h.reminder(t) // storage repaired: the next assessment succeeds and dispatches
	assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "the retried assessment counts the text-only outcome once")
	grTextReminder(t, h, second)
}

// F3: separate clocks — goal start S, helper activity S+10m, watermark
// S+20m, persisted goal activity S+30m. The helper is still in the window
// (WARN when unavailable), and a readable helper's old output is not counted
// again. Neither the watermark nor the latest goal activity is the start.
func TestGoalReminder1852WindowUsesGoalStartNotTheWatermark(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	helperAt, since, goalActiveAt := start.Add(10*time.Minute), start.Add(20*time.Minute), start.Add(30*time.Minute)
	_, err := resolveGoalRecordStore().Update(h.goalID, goal.Write{Mutate: func(g *goal.Goal) error {
		g.LastActivityAt = goalActiveAt
		return nil
	}})
	require.NoError(t, err)
	persisted, err := goal.NewStore(config.OmnipusHomeDir()).Get(h.goalID)
	require.NoError(t, err)
	require.NotNil(t, persisted.StartedAt)
	require.True(t, persisted.StartedAt.Equal(start), "SETUP: the goal start stays S")
	require.True(t, persisted.LastActivityAt.Equal(goalActiveAt), "SETUP: the persisted goal activity clock is S+30m")
	require.True(t, effectiveGoalActivity(persisted).Equal(goalActiveAt), "SETUP: the latest goal activity differs from the start")
	lost := gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-time.Hour), lastActivity: helperAt}, "session_1852CLOCKS")
	readable := gr1852AddHelper(t, h, gr1852Helper{createdAt: start.Add(-time.Hour), lastActivity: helperAt, readable: true, outputAt: helperAt}, "")

	progress, lines, err := gr1852CollectWith(t, h, gr1852NewWork(), since)
	require.NoError(t, err)
	assert.Equal(t, goalReminderProgress{}, progress, "output older than the watermark is not new progress")
	assert.Len(t, gr1852LinesNaming(lines, lost), 1, "active since the goal start: still in the window, so its loss is visible")
	assert.Empty(t, gr1852LinesNaming(lines, readable))
}

// F3: creation alone puts a helper in the window — created at S with no
// activity stamp, and with one from before S.
func TestGoalReminder1852CreatedAtGoalStartAloneIsInWindow(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	zero := gr1852AddHelper(t, h, gr1852Helper{createdAt: start}, "session_1852CREATEDNOACT")
	pre := gr1852AddHelper(t, h, gr1852Helper{createdAt: start, lastActivity: start.Add(-time.Hour)}, "session_1852CREATEDPREACT")

	progress, lines, err := gr1852CollectWith(t, h, gr1852NewWork(), start)
	require.NoError(t, err)
	assert.Equal(t, goalReminderProgress{}, progress)
	assert.Len(t, gr1852LinesNaming(lines, zero), 1, "created at the goal start with no activity is in the window")
	assert.Len(t, gr1852LinesNaming(lines, pre), 1, "created at the goal start with older activity is in the window")
}

// F3: an unavailable running helper with pre-goal stamps and no final
// delivery: in the window, warned once, never progress.
func TestGoalReminder1852UnavailableRunningHelperWarnsOnceWithoutProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	start := gr1852GoalStart(t, h)
	old := start.Add(-2 * time.Hour)
	id := gr1852AddHelper(t, h, gr1852Helper{createdAt: old, lastActivity: old, state: session.LifecycleRunning}, "session_1852RUNNINGLOST")

	work := goalReminderWorkFor(h.goalID)
	p1, first, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)
	p2, second, err := gr1852CollectWith(t, h, work, start)
	require.NoError(t, err)
	assert.Equal(t, goalReminderProgress{}, p1)
	assert.Equal(t, goalReminderProgress{}, p2)
	assert.Len(t, gr1852LinesNaming(first, id), 1, "the running helper is in the window, so its loss is logged")
	assert.Empty(t, gr1852LinesNaming(second, id), "logged once per goal and helper")
}
