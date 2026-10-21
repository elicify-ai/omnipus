package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (h *grHarness) journal(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.al.GetSessionLifecycleStore().Dir(), h.id+".jsonl"))
	require.NoError(t, err)
	return raw
}

func (h *grHarness) age(t *testing.T) {
	t.Helper()
	_, err := resolveGoalRecordStore().Update(h.goalID, func(g *goal.Goal) error {
		g.LastActivityAt = time.Now().UTC().Add(-time.Hour)
		return nil
	})
	require.NoError(t, err)
}

func (h *grHarness) finish(t *testing.T, run *grPending, call *grCall) {
	t.Helper()
	h.root.release(call)
	result := grJoin(t, run)
	require.NoError(t, result.err, "a legitimate reminder must complete through its real receiver")
	assert.Equal(t, grReply, result.reply)
	h.root.drainRequests()
}

func (h *grHarness) assertSettled(t *testing.T, call *grCall, prior *session.LifecycleRecord, before []byte) {
	t.Helper()
	current := h.load(t) // reopen: a cached running/completed flag is not proof
	assert.Equal(t, session.LifecycleCompleted, current.State, "R1: ordinary completion must durably settle the reminder")
	assert.Equal(t, prior.Generation+1, current.Generation, "R1: settlement must belong to the reminder's new generation")
	require.NotNil(t, current.ExecutionID)
	assert.NotEqual(t, prior.ExecutionID.RunID, current.ExecutionID.RunID, "R1: do not falsely pass by rereading the already-completed prior execution")
	assert.Equal(t, call.record.ExecutionID, current.ExecutionID, "completion must settle exactly the producing execution")
	assert.Nil(t, current.SteeredBy)
	assert.Nil(t, h.al.activeTurnForCancel(h.id, CancelScope{SessionID: h.id, TurnOnly: true}), "completed reminder must retire its live turn")
	assert.Nil(t, h.al.executionDispositionFor(call.claim), "completed reminder must release its execution owner")
	if assert.NotNil(t, call.owner, "R1: ordinary execution must own reminder settlement") {
		select {
		case <-call.owner.done:
		default:
			t.Error("R1: reminder receiver returned before its ordinary owner settled")
		}
	}
	assert.True(t, bytes.HasPrefix(h.journal(t), before), "admission and settlement must append, never overwrite the prior terminal journal")
}

func (h *grHarness) launchReviewer(t *testing.T, callID string) *session.LifecycleRecord {
	t.Helper()
	launcher := NewSteerLauncher(h.al)
	launched, err := launcher.Launch(h.ctx, steer.LaunchRequest{
		SteeringSessionID: h.id, TargetAgentID: grReviewerID,
		Task: "Independently review the work toward this goal.", Label: "Independent reviewer",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if !assert.NoError(t, err, "R1: real reviewer launch during a real keeper turn must not refuse its parent as inactive") {
		return nil
	}
	ls := session.NewLifecycleStore(h.al.GetSessionLifecycleStore().Dir())
	rec, err := ls.Load(launched.SessionID)
	require.NoError(t, err)
	require.NotNil(t, rec.SteeredBy)
	assert.Equal(t, h.id, rec.SteeredBy.SteeringSessionID)
	assert.Equal(t, h.id, rec.SteeredBy.RootSessionID)
	assert.Equal(t, h.id, rec.SteeredBy.ReportingTarget.SessionID)
	assert.Equal(t, grReviewerID, rec.AgentID)
	assert.Equal(t, testDefaultAgentID, rec.ParentAgentID)
	meta, err := h.al.GetSessionStore().GetMeta(launched.SessionID)
	require.NoError(t, err)
	assert.Equal(t, h.id, meta.ParentSessionID)
	assert.Equal(t, session.SessionTypeDelegate, meta.Type)
	dispatched, err := launcher.Dispatch(h.ctx, launched.SessionID, launched.Generation)
	require.NoError(t, err, "reviewer must execute, not merely exist as a queued record")
	require.Equal(t, steer.DispatchRunning, dispatched.State)
	var call *grCall
	select {
	case call = <-h.reviewer.requests:
	case <-time.After(grWatchdog):
		t.Fatal("real dispatched reviewer did not execute at its provider boundary")
	}
	require.NoError(t, call.readErr)
	require.NotNil(t, call.record)
	assert.Equal(t, launched.SessionID, call.sessionID)
	assert.Equal(t, session.LifecycleRunning, call.record.State)
	assert.Equal(t, launched.Generation, call.record.Generation)
	require.NotNil(t, call.record.ExecutionID)
	assert.NotEmpty(t, call.record.ExecutionID.RunID)
	assert.NotEqual(t, uint64(0), call.record.ExecutionID.BootSeq, "reviewer must use a genuine nonzero boot epoch")
	want := executionClaim{SessionID: launched.SessionID, Generation: launched.Generation,
		RunID: call.record.ExecutionID.RunID, BootSeq: call.record.ExecutionID.BootSeq}
	assert.Equal(t, want, call.claim, "reviewer must have its own legitimate bound identity")
	require.NotNil(t, call.owner, "reviewer completion needs the real execution owner")
	assert.Equal(t, want, call.owner.claim, "the actual reviewer owner must match its own independently launched session and generation")
	h.reviewer.release(call)
	select {
	case <-call.owner.done:
	case <-time.After(grWatchdog):
		t.Fatal("reviewer compute exited without settling its real completion owner")
	}
	settled, err := ls.Load(launched.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCompleted, settled.State)
	assert.Equal(t, call.record.ExecutionID, settled.ExecutionID)
	require.NotNil(t, settled.FinalDelivery, "reviewer must have a durable committed final handback")
	assert.Equal(t, call.record.ExecutionID.RunID, settled.FinalDelivery.CommitID)
	h.reviewer.drainRequests()
	return settled
}

// A receive-side refusal is observed by racing its synchronous return against
// the external request channel. There is no 'sleep then hope nothing ran'.
func (h *grHarness) rejectReminder(t *testing.T, msg bus.InboundMessage, reason string) {
	t.Helper()
	before := h.root.count()
	run := h.system(msg)
	select {
	case call := <-h.root.requests:
		t.Errorf("R3: %s started forbidden reminder compute: durable=%+v live_claim=%+v", reason, call.record, call.claim)
		h.root.release(call)
	case <-run.done:
	case <-time.After(grWatchdog):
		t.Fatalf("SETUP: %s neither refused nor reached provider", reason)
	}
	result := grJoin(t, run)
	if result.err != nil {
		assert.True(t, errors.Is(result.err, steer.ErrStaleGeneration) ||
			errors.Is(result.err, steer.ErrDispatchCancelled) || errors.Is(result.err, ErrPreviousExecutionPending),
			"refusal must identify admission/Stop/staleness, not an unrelated failure: %v", result.err)
	}
	assert.Equal(t, before, h.root.count(), "R3: %s must not spend another provider call", reason)
	h.root.drainRequests()
}

func (h *grHarness) assertNoKeeperWake(t *testing.T, reason string) {
	t.Helper()
	before := goalRecordForSession(t, h.id)
	h.al.goalQuietWindowSettle(time.Now())
	select {
	case msg := <-h.al.bus.InboundChan():
		t.Errorf("R3/R6: %s must suppress automatic reminders, got %+v", reason, msg)
	default:
	}
	assert.Equal(t, before.ZeroOutputPushes, goalRecordForSession(t, h.id).ZeroOutputPushes,
		"R3/R6: %s must not consume an unproductive reminder", reason)
}

func (h *grHarness) visibleHistory(t *testing.T) []session.TranscriptEntry {
	t.Helper()
	fresh, err := session.NewUnifiedStore(h.al.GetSessionStore().BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, fresh.Close()) })
	view, err := fresh.ReadTranscriptView(h.id)
	require.NoError(t, err)
	return view.Entries
}

func grHasSystemLine(entries []session.TranscriptEntry, text string) int {
	n := 0
	for _, entry := range entries {
		if entry.Type == session.EntryTypeSystem && entry.Role == "system" && entry.Content == text {
			n++
		}
	}
	return n
}

func (h *grHarness) assertOneLiveRoot(t *testing.T) {
	t.Helper()
	var ids []string
	h.al.activeTurnStates.Range(func(_, value any) bool {
		if ts, ok := value.(*turnState); ok && ts.transcriptSessionID == h.id {
			ids = append(ids, ts.turnID)
		}
		return true
	})
	assert.Len(t, ids, 1, "one goal root may have only one live turn: %v", ids)
}

func (h *grHarness) worker(t *testing.T) *sessionWorker {
	t.Helper()
	var w *sessionWorker
	h.al.sessionWorkers.Range(func(key, value any) bool {
		if strings.HasSuffix(fmt.Sprint(key), ":"+h.id) {
			w, _ = value.(*sessionWorker)
			return false
		}
		return true
	})
	require.NotNil(t, w, "real per-session worker must own the serialized traffic")
	return w
}

func grAwaitOwner(t *testing.T, call *grCall) {
	t.Helper()
	if !assert.NotNil(t, call.owner, "execution needs the ordinary settlement owner") {
		return
	}
	select {
	case <-call.owner.done:
	case <-time.After(grWatchdog):
		t.Fatal("execution owner did not settle after provider release")
	}
}

func grStop(t *testing.T, h *grHarness) {
	t.Helper()
	result, err := h.al.StopSession(context.Background(), StopRequest{
		SessionID: h.id, Channel: "web", By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "goal-reminder-owner"},
	})
	require.NoError(t, err)
	require.NoError(t, result.RootErr)
	require.Empty(t, result.Report.Unreachable)
}
