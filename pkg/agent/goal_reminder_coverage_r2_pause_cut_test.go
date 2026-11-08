package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// W2/M12: both legal publication orders are asserted. The post-fresh-read
// schedule distinguishes losing the mutex from losing the independent recheck.
func TestGoalReminderPauseCutOrdersRealHumanPublication(t *testing.T) {
	t.Run("PostFreshReadPauseThenHumanActiveLast", func(t *testing.T) { r2PauseCompetesWithHuman(t, true) })
	t.Run("HumanAcceptedBeforeFinalReadHasNoStalePause", func(t *testing.T) { r2PauseCompetesWithHuman(t, false) })
}

func r2CompletedFifthForPublication(t *testing.T) (*grHarness, *grCall) {
	t.Helper()
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 4) // fifth actual completed unproductive reminder is the boundary
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	h.finish(t, run, call)
	grAwaitOwner(t, call)
	pending := r6WarnPendingOutcome(h.goalID)
	require.NotNil(t, pending, "SETUP: retain the natural completed producing outcome")
	require.Same(t, call.owner, pending.execution)
	h.age(t)
	return h, call
}

func r2GoalPublications(t *testing.T, h *grHarness) func() []GoalStatusChangedPayload {
	t.Helper()
	var mu sync.Mutex
	var events []GoalStatusChangedPayload
	h.al.SetEventSyncTap(func(event Event) {
		payload, ok := event.Payload.(GoalStatusChangedPayload)
		if event.Kind != EventKindGoalStatusChanged || !ok || payload.SessionID != h.id || payload.GoalID != h.goalID {
			return
		}
		mu.Lock()
		events = append(events, payload)
		mu.Unlock()
	})
	t.Cleanup(func() { h.al.SetEventSyncTap(nil) })
	return func() []GoalStatusChangedPayload {
		mu.Lock()
		defer mu.Unlock()
		return append([]GoalStatusChangedPayload(nil), events...)
	}
}

func r2PauseCompetesWithHuman(t *testing.T, postFreshRead bool) {
	t.Helper()
	h, fifth := r2CompletedFifthForPublication(t)
	publications := r2GoalPublications(t, h)
	reached, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var reachedOnce, releaseOnce sync.Once
	unhold := func() { releaseOnce.Do(func() { close(release) }) }
	checkpoint := func(sessionID, goalID string) {
		if sessionID == h.id && goalID == h.goalID {
			reachedOnce.Do(func() { close(reached) })
			<-release
		}
	}
	seams := &goalReminderPassiveSeams{goalPauseBeforeWaiting: checkpoint}
	if postFreshRead {
		seams = &goalReminderPassiveSeams{goalPauseAfterFreshRead: checkpoint}
	}
	prev := goalReminderSeams.Swap(seams)
	var human *grPending
	t.Cleanup(func() {
		unhold()
		h.root.openAll()
		if human != nil {
			grJoin(t, human)
		}
		r2AwaitPauser(t, settled)
		goalReminderSeams.Store(prev)
	})
	go func() {
		defer close(settled)
		h.al.goalQuietWindowSettle(time.Now())
	}()
	select {
	case <-reached:
	case <-settled:
		t.Fatal("SETUP: actual fifth-outcome pauser missed its specified passive point")
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: actual fifth-outcome pauser did not reach its specified passive point")
	}
	require.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes)
	work := goalReminderWorkFor(h.goalID)
	unprotected := work.mu.TryLock()
	if unprotected {
		work.mu.Unlock() // release only this test's own successful scheduling probe
	}
	humanFirst := !postFreshRead || unprotected
	human = h.human(t, "Continue this same goal while its pause is being accepted.")
	if humanFirst {
		call := grAwaitCall(t, h.root, human)
		require.True(t, call.human)
		h.finish(t, human, call)
		grAwaitOwner(t, call)
	} else {
		// The actual human has a real admitted identity/owner, then blocks on
		// resume's shared work mutex. Do NOT wait for provider/body completion.
		owner := r2AwaitHumanAdmission(t, h, human, fifth.record)
		unhold()
		r2AwaitPauser(t, settled)
		call := grAwaitCall(t, h.root, human)
		require.True(t, call.human)
		require.Same(t, owner, call.owner)
		h.finish(t, human, call)
		grAwaitOwner(t, call)
	}
	unhold()
	r2AwaitPauser(t, settled)
	r2AssertHumanPublicationOrder(t, h, fifth, publications(), !humanFirst)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.False(t, h.al.goalIsWaitingOnUser(h.goalID), "a committed human resume must not be overwritten by the late pauser")
	work.mu.Lock()
	assessed, pending := work.lastAssessedTurn, work.pending
	work.mu.Unlock()
	assert.Equal(t, fifth.turn.turnID, assessed, "the pause belongs to the SAME actual producing outcome")
	assert.Nil(t, pending)
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	_, published := r6GateTakeReminder(h)
	assert.True(t, published, "the same goal must remain eligible after the joined human resume")
}

func r2AwaitPauser(t *testing.T, settled <-chan struct{}) {
	t.Helper()
	select {
	case <-settled:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: actual released pauser did not join")
	}
}

func r2AwaitHumanAdmission(t *testing.T, h *grHarness, human *grPending, prior *session.LifecycleRecord) *executionDisposition {
	t.Helper()
	watchdog := time.NewTimer(grWatchdog)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer watchdog.Stop()
	defer ticker.Stop()
	for {
		rec, err := h.al.GetSessionLifecycleStore().Load(h.id)
		require.NoError(t, err, "SETUP: actual human admission storage must remain healthy")
		if rec.Generation > prior.Generation && rec.ExecutionID != nil {
			owner := h.al.executionDispositionFor(h.al.executionClaimFor(rec))
			if owner != nil {
				require.Equal(t, prior.Generation+1, rec.Generation)
				require.NotEqual(t, prior.ExecutionID.RunID, rec.ExecutionID.RunID)
				return owner
			}
		}
		select {
		case <-ticker.C:
		case <-human.done:
			t.Fatal("SETUP: genuine human exited before its blocked actual admission could be observed")
		case <-watchdog.C:
			t.Fatal("SETUP: genuine human did not acquire an actual admitted identity/owner")
		}
	}
}

func r2AssertHumanPublicationOrder(t *testing.T, h *grHarness, fifth *grCall, events []GoalStatusChangedPayload, pauseFirst bool) {
	t.Helper()
	var rows []session.TranscriptEntry
	for _, row := range h.visibleHistory(t) {
		if row.Role == "system" && row.Content == grPauseLine {
			rows = append(rows, row)
		}
	}
	wantNotices := 0
	if pauseFirst {
		wantNotices = 1
	}
	assert.Len(t, rows, wantNotices, "human-first forbids stale notices; pause-first permits one legitimate producing notice")
	require.NotEmpty(t, events, "the actual human must synchronously publish resumed active status")
	assert.Equal(t, "active", events[len(events)-1].State, "active must be LAST, never followed by stale waiting")
	seenActive, noticeCount := false, 0
	for _, event := range events {
		if event.State == "active" {
			seenActive = true
		}
		if event.State == "waiting_on_user" {
			assert.True(t, pauseFirst, "a human-first acceptance forbids any stale waiting publication")
			assert.False(t, seenActive, "a legitimate pause may publish only BEFORE human-active")
		}
		if event.ChatNotice == nil {
			continue
		}
		noticeCount++
		assert.Equal(t, "waiting_on_user", event.State)
		assert.Equal(t, "replay_message", event.ChatNotice.Type)
		assert.Equal(t, h.id, event.ChatNotice.SessionId)
		assert.Equal(t, "system", event.ChatNotice.Role)
		assert.Equal(t, grPauseLine, event.ChatNotice.Content)
		require.NotNil(t, event.ChatNotice.Id)
		require.NotNil(t, event.ChatNotice.GoalId)
		assert.Equal(t, h.goalID, *event.ChatNotice.GoalId)
		if len(rows) == 1 {
			assert.Equal(t, rows[0].ID, *event.ChatNotice.Id, "live replay must identify the actual persisted pause, not a second fabricated notice")
			assert.Equal(t, h.goalID, rows[0].GoalID)
			assert.Equal(t, session.EntryTypeSystem, rows[0].Type)
		}
	}
	assert.True(t, seenActive)
	assert.Equal(t, wantNotices, noticeCount)
	require.NotNil(t, fifth.turn)
}
