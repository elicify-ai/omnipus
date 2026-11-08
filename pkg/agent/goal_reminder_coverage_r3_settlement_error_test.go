package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: R3-SF01 and E71-CR-F4, 2026-10-23 two-finding dispatch.
// The provider is the external edge; admission, ownership, stores, commands
// and keeper publications are real. These cases must never run in parallel.
func TestGoalReminderR3ReviewRegressions(t *testing.T) {
	t.Run("FailedSettlementErrorRetainsOutcome", r3FailedSettlementErrorRetainsOutcome)
	t.Run("ColdFailurePauseStatusBeforeCold", func(t *testing.T) { r3ColdFailurePauseStatus(t, true) })
	t.Run("ColdFailurePauseStatusAfterCold", func(t *testing.T) { r3ColdFailurePauseStatus(t, false) })
}

func r3FailedSettlementErrorRetainsOutcome(t *testing.T) {
	h, provider, msg := r2TwoFailuresAtNextWake(t)
	events := r3CaptureReviewEvents(t, h)
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	failuresBefore, assessedBefore := work.failedReminders, work.lastAssessedTurn
	work.mu.Unlock()
	require.Equal(t, 2, failuresBefore, "SETUP: two real failed turns must already be assessed")
	prior := h.load(t)
	beforeCalls := h.root.count()
	// Supply the same real target in the worker's existing correlation slot;
	// the queued wake's immutable generation/run metadata is untouched.
	msg.SessionID = msg.AsyncTranscriptSessionID
	require.Equal(t, h.id, msg.SessionID)
	require.True(t, h.al.dispatchSystemMessageToSessionWorker(msg, "webchat"))
	worker := h.worker(t)
	var call *grCall
	var fault *r3LifecycleWriteFault
	joined := false
	t.Cleanup(func() {
		h.root.openAll()
		if !joined {
			if call != nil {
				grAwaitOwner(t, call)
			}
			r6GateJoinWorker(t, h, worker)
		}
		if fault != nil {
			fault.repair(t)
		}
	})
	select {
	case call = <-h.root.requests:
	case <-time.After(grWatchdog):
		t.Fatal("SETUP: actual automatic worker did not reach its existing provider gate")
	}
	h.assertAdmission(t, call, prior)
	require.Equal(t, session.LifecycleRunning, h.load(t).State)
	require.Equal(t, h.goalID, call.turn.opts.goalReminderGoalID)
	fault = r3CanonicalLifecycleWriteFault(t, h)
	fault.deny(t)
	r3RequireHealthyGoalAndSession(t, h, "held-provider")
	require.Equal(t, call.record.ExecutionID, h.load(t).ExecutionID)
	h.root.release(call)
	grAwaitOwner(t, call)
	unexpected := r6GateJoinWorker(t, h, worker)
	joined = true
	for _, extra := range unexpected {
		require.Equal(t, call.claim, extra.claim, "SETUP: only retries of the SAME actual failed turn may enter")
		require.Same(t, call.owner, extra.owner)
	}
	completedCalls := h.root.count()
	require.Greater(t, completedCalls, beforeCalls)
	call.owner.mu.Lock()
	settlementErr, turnErr, ran := call.owner.result, call.owner.turnErr, call.owner.turnRan
	call.owner.mu.Unlock()
	require.True(t, ran)
	require.ErrorIs(t, turnErr, errR2ProviderFailure, "SETUP: retain the original actual provider-turn failure separately")
	require.NotErrorIs(t, turnErr, os.ErrPermission, "SETUP: the write fault must not replace the turn's original error")
	require.False(t, call.turn.stopRequested.Load())
	require.False(t, call.turn.cancelFired.Load())
	require.ErrorIs(t, settlementErr, os.ErrPermission, "SETUP: owner must close with the real final-write permission error")
	require.Contains(t, settlementErr.Error(), "settle root", "SETUP: fail final root settlement, not admission or the fence read")
	var pathErr *os.PathError
	require.ErrorAs(t, settlementErr, &pathErr)
	require.Equal(t, fault.path, pathErr.Path, "SETUP: error must name the canonical journal, not a mirror/directory")
	require.Equal(t, "open", pathErr.Op)
	require.False(t, errors.Is(settlementErr, errR2ProviderFailure), "SETUP: settlement result is NOT the expected provider error")
	require.Equal(t, session.LifecycleRunning, h.load(t).State)
	require.Equal(t, call.record.ExecutionID, h.load(t).ExecutionID)
	require.Equal(t, fault.before, h.journal(t), "SETUP: only the unsuccessful final write may touch this journal")
	require.Nil(t, h.al.executionDispositionFor(call.claim))
	require.Nil(t, h.al.ownLiveTurn(h.id))
	pending := r6WarnPendingOutcome(h.goalID)
	require.NotNil(t, pending, "SETUP: retain the natural failed producing outcome")
	require.True(t, pending.failed)
	require.Same(t, call.owner, pending.execution)
	require.Equal(t, call.turn.turnID, pending.turnID)
	require.Len(t, events.errorsAt("settlement"), 1, "SETUP: real outer worker must report its final-state failure before assessment")
	for tick := 0; tick < 3; tick++ {
		r3RequireHealthyGoalAndSession(t, h, fmt.Sprintf("assessment-%d", tick))
		h.al.goalQuietWindowSettle(time.Now())
		r3AssertSettlementHeld(t, h, pending, assessedBefore, events, completedCalls)
	}
	fault.repair(t) // owner and worker are already fully joined; do not retry them
	assert.Equal(t, fault.before, h.journal(t))
	provider.mode.Store(r2ReplySuccess)
	human := h.human(t, "Continue the same goal after storage repair.")
	humanCall := grAwaitCall(t, h.root, human)
	require.True(t, humanCall.human)
	require.NotEqual(t, call.claim.RunID, humanCall.claim.RunID, "human recovery must mint a real new execution, not reuse the failed owner")
	h.finish(t, human, humanCall)
	grAwaitOwner(t, humanCall)
	assert.Equal(t, session.LifecycleCompleted, h.load(t).State)
	assert.Equal(t, humanCall.record.ExecutionID, h.load(t).ExecutionID)
	assert.Nil(t, r6WarnPendingOutcome(h.goalID), "actual human entry clears the unusable pending outcome")
	work.mu.Lock()
	failuresAfter := work.failedReminders
	work.mu.Unlock()
	assert.Equal(t, 0, failuresAfter)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	assert.False(t, h.al.goalIsWaitingOnUser(h.goalID))
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	h.age(t)
	grTextReminder(t, h, h.reminder(t))
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
	call.owner.mu.Lock()
	retainedErr := call.owner.result
	call.owner.mu.Unlock()
	assert.ErrorIs(t, retainedErr, os.ErrPermission, "recovery must not erase/re-finish the old owner")
}

func r3AssertSettlementHeld(t *testing.T, h *grHarness, pending *goalReminderOutcome, assessed string, events *r3ReviewEvents, calls int) {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	actualPending, failures, actualAssessed := work.pending, work.failedReminders, work.lastAssessedTurn
	work.mu.Unlock()
	assert.Same(t, pending, actualPending, "R3-SF01: a settlement error must retain the SAME natural pending outcome/owner")
	assert.Equal(t, 2, failures, "R3-SF01: unsuccessful owner settlement cannot become the third assessed failure")
	assert.Equal(t, assessed, actualAssessed, "R3-SF01: the failed settlement must not advance assessment identity")
	g := goalRecordForSession(t, h.id)
	assert.Equal(t, 2, g.ZeroOutputPushes)
	assert.Equal(t, h.goalID, g.GoalID)
	assert.Equal(t, "active", string(g.State))
	assert.NotEqual(t, r2FailedTurnPause, g.LatestReason)
	assert.False(t, h.al.goalIsWaitingOnUser(h.goalID))
	assert.Empty(t, r2FailurePauseRows(t, h), "R3-SF01: infrastructure failure is not an ordinary third-failure pause")
	assert.Empty(t, events.pauseNotices())
	grNoPublishedReminder(t, h.al)
	assert.Equal(t, calls, h.root.count())
	var assessments []ErrorPayload
	for _, e := range events.errorsAt("goal_record_read") {
		if strings.Contains(e.Message, "assess completed goal reminder (no new reminder dispatched)") {
			assessments = append(assessments, e)
		}
	}
	assert.NotEmpty(t, assessments, "R3-SF01: assessment must visibly report its own settlement refusal, not rely on an earlier outer error")
	for _, e := range assessments {
		assert.Contains(t, e.Message, "settle root")
	}
	found := false
	for _, row := range h.visibleHistory(t) {
		found = found || (row.Role == "system" && strings.Contains(row.Content, "assess completed goal reminder (no new reminder dispatched)") && strings.Contains(row.Content, "settle root"))
	}
	assert.True(t, found, "R3-SF01: assessment error must survive a fresh transcript-view read")
}

type r3LifecycleWriteFault struct {
	path     string
	mode     os.FileMode
	before   []byte
	restored bool
}

func r3CanonicalLifecycleWriteFault(t *testing.T, h *grHarness) *r3LifecycleWriteFault {
	t.Helper()
	path := filepath.Join(h.al.GetSessionLifecycleStore().Dir(), h.id+".jsonl")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	probe, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	require.NoError(t, err, "SETUP: canonical journal must be writable before fault")
	require.NoError(t, probe.Close())
	return &r3LifecycleWriteFault{path: path, mode: info.Mode().Perm(), before: before}
}

func (f *r3LifecycleWriteFault) deny(t *testing.T) {
	t.Helper()
	require.NoError(t, os.Chmod(f.path, f.mode&^0o222))
	probe, err := os.OpenFile(f.path, os.O_RDWR|os.O_APPEND, 0)
	if probe != nil {
		require.NoError(t, probe.Close())
	}
	require.ErrorIs(t, err, os.ErrPermission, "SETUP: a fresh canonical append-open must actually fail; root bypass is not RED")
	actual, err := os.ReadFile(f.path)
	require.NoError(t, err)
	require.Equal(t, f.before, actual)
	info, err := os.Stat(filepath.Dir(f.path))
	require.NoError(t, err)
	require.NotZero(t, info.Mode().Perm()&0o200, "SETUP: never fault the directory")
}

func (f *r3LifecycleWriteFault) repair(t *testing.T) {
	t.Helper()
	if f.restored {
		return
	}
	require.NoError(t, os.Chmod(f.path, f.mode))
	probe, err := os.OpenFile(f.path, os.O_RDWR|os.O_APPEND, 0)
	require.NoError(t, err, "SETUP: exact permission repair restores canonical write access")
	n, writeErr := probe.Write(nil) // real fresh write operation; never append a fake lifecycle state
	closeErr := probe.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
	require.Zero(t, n)
	info, err := os.Stat(f.path)
	require.NoError(t, err)
	require.Equal(t, f.mode, info.Mode().Perm())
	f.restored = true
}

func r3RequireHealthyGoalAndSession(t *testing.T, h *grHarness, id string) {
	t.Helper()
	g, err := resolveGoalRecordStore().Update(h.goalID, func(g *goal.Goal) error {
		g.LastActivityAt = time.Now().UTC().Add(-time.Hour)
		return nil
	})
	require.NoError(t, err, "SETUP: separate goal store must remain writable")
	require.Equal(t, h.goalID, g.GoalID)
	written, err := h.al.GetSessionStore().AppendTranscriptOnce(h.id, session.TranscriptEntry{
		ID: "r3-storage-health-" + id, Type: session.EntryTypeSystem, Role: "system",
		Content: "Storage health control.", Timestamp: time.Now().UTC(),
	})
	require.NoError(t, err, "SETUP: separate transcript store must remain writable")
	require.True(t, written, "SETUP: health control must perform a fresh real append")
	meta, err := h.al.GetSessionStore().GetMeta(h.id)
	require.NoError(t, err)
	require.Equal(t, "goal-reminder-owner", meta.Owner)
	require.NotEmpty(t, h.visibleHistory(t))
	h.load(t) // fresh canonical lifecycle reader, not a stale cached mirror
}

type r3ReviewEvents struct {
	mu     sync.Mutex
	events []Event
	h      *grHarness
}

func r3CaptureReviewEvents(t *testing.T, h *grHarness) *r3ReviewEvents {
	t.Helper()
	c := &r3ReviewEvents{h: h}
	h.al.SetEventSyncTap(func(e Event) { c.mu.Lock(); c.events = append(c.events, e); c.mu.Unlock() })
	t.Cleanup(func() { h.al.SetEventSyncTap(nil) })
	return c
}

func (c *r3ReviewEvents) errorsAt(stage string) []ErrorPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ErrorPayload
	for _, e := range c.events {
		p, ok := e.Payload.(ErrorPayload)
		if e.Kind == EventKindError && ok && p.SessionID == c.h.id && p.Stage == stage {
			out = append(out, p)
		}
	}
	return out
}

func (c *r3ReviewEvents) pauseNotices() []GoalStatusChangedPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []GoalStatusChangedPayload
	for _, e := range c.events {
		p, ok := e.Payload.(GoalStatusChangedPayload)
		if e.Kind == EventKindGoalStatusChanged && ok && p.SessionID == c.h.id && p.GoalID == c.h.goalID && p.ChatNotice != nil && p.ChatNotice.Content == r2FailedTurnPause {
			out = append(out, p)
		}
	}
	return out
}
