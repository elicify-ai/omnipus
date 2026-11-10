package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderR6BusyWindowRetainsOutcomeProgress is founder R6: work seen
// while owned activity is still busy stays with that same completed reminder.
// A real claim starts the Judge. The Judge provider is held only at its
// external call, after the ordinary reminder execution has finished. Releasing
// a no-tool unmet verdict clears the verifier without being new work. The same
// pending reminder must still count the earlier read. E-B26 clears that latch
// while advancing the window, so the later assessment increments.
func TestGoalReminderR6BusyWindowRetainsOutcomeProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	r6CarryGapInstallPlanRegistry(t, h)
	judge := r6CarryGapHoldJudge(t, h)
	adjudicationDone := r6CarryGapWatchAdjudication(t, h)
	path := grReadFixture(t)
	msg := h.reminder(t)
	worker := r6CarryGapRunReadThenClaim(t, h, msg, path, judge)
	turnID := r6CarryGapPendingTurn(t, h)
	require.Equal(t, worker.turnID, turnID, "the pending outcome must be this producing reminder, not a later steer")

	before := goalReminderWatermark(h.goalID)
	grSetStreak(t, h, 1)
	r6CarryGapSettleWithoutPublish(t, h)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "a real read resets the streak while the claim's verifier is still busy")
	r6CarryGapAssertReadConsumedByBusyWindow(t, h, before, turnID)

	grSetStreak(t, h, 2)
	r6CarryGapSettleWithoutPublish(t, h)
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "the same read must not count again as fresh progress while the verifier is still busy")
	r6CarryGapAssertPendingLatch(t, h, turnID, true)

	roundBefore := goalRecordForSession(t, h.id).Round
	judge.Release()
	r6CarryGapJoinAdjudication(t, adjudicationDone)
	r6CarryGapAssertUnmetIsNotProgress(t, h, turnID, roundBefore)
	r6CarryGapDiscardQueuedSteers(t, h)
	r6CarryGapAssertPendingLatch(t, h, turnID, true)

	grSetStreak(t, h, 0)
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "the retained read keeps this same reminder productive; forgetting it would increment to 1")
	r6CarryGapAssertAssessed(t, h, turnID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	r6CarryGapAssertOldReadNotFresh(t, h)
}

func r6CarryGapInstallPlanRegistry(t *testing.T, h *grHarness) {
	t.Helper()
	previous := currentVerifierSessionRegistry()
	t.Cleanup(func() { SetVerifierSessionRegistry(previous) })
	store := plan.New(filepath.Join(config.OmnipusHomeDir(), "plans"))
	h.al.SetPlanStore(store)
	GetTaskExecutor(h.al).SetPlanStore(store)
	engine := NewPlanEngine(h.al, store, h.al.taskStore, GetTaskExecutor(h.al))
	h.al.SetPlanEngine(engine)
	require.True(t, GetPlanEngine(h.al) == engine, "SETUP: the keeper must read the same verifier registry the claim path registers")
}

func r6CarryGapWatchAdjudication(t *testing.T, h *grHarness) <-chan struct{} {
	t.Helper()
	done := make(chan struct{}, 1)
	previous := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) {
		if sessionID == h.id {
			done <- struct{}{}
		}
		if previous != nil {
			previous(sessionID)
		}
	}
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = previous })
	return done
}

type r6CarryGapJudge struct {
	mu        sync.Mutex
	calls     int
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
	stopOnce  sync.Once
}

func r6CarryGapHoldJudge(t *testing.T, h *grHarness) *r6CarryGapJudge {
	t.Helper()
	judge, ok := h.al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok, "SETUP: the real Judge agent must already be registered")
	held := &r6CarryGapJudge{entered: make(chan struct{}), release: make(chan struct{})}
	judge.Provider = held
	t.Cleanup(held.Release)
	return held
}

func (j *r6CarryGapJudge) GetDefaultModel() string { return "r6-carry-gap-unmet-judge" }

func (j *r6CarryGapJudge) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	j.mu.Lock()
	j.calls++
	j.mu.Unlock()
	j.enterOnce.Do(func() { close(j.entered) })
	select {
	case <-j.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// No tool call. met=false leaves rounds remaining; it does not clear the goal.
	return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(false, "The claim is not proof of productive goal work.")}, nil
}

func (j *r6CarryGapJudge) Release() { j.stopOnce.Do(func() { close(j.release) }) }

func (j *r6CarryGapJudge) callCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.calls
}

type r6CarryGapWorker struct {
	h      *grHarness
	path   string
	callID [2]string
	mu     sync.Mutex
	calls  int
	turnID string
	owner  *executionDisposition
}

func (w *r6CarryGapWorker) GetDefaultModel() string { return "r6-carry-gap-read-claim" }

func (w *r6CarryGapWorker) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	w.noteLive()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	switch w.calls {
	case 1:
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{
			ID: w.callID[0], Name: "read_file", Arguments: map[string]any{"path": w.path},
		}}}, nil
	case 2:
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{
			ID: w.callID[1], Name: "goal_claim", Arguments: map[string]any{"status": "met", "evidence": "The reference was read."},
		}}}, nil
	default:
		return &providers.LLMResponse{Content: "I believe this work is complete."}, nil
	}
}

func r6CarryGapRunReadThenClaim(t *testing.T, h *grHarness, msg bus.InboundMessage, path string, judge *r6CarryGapJudge) *r6CarryGapWorker {
	t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	worker := &r6CarryGapWorker{h: h, path: path, callID: [2]string{"r6-carry-read", "r6-carry-claim"}}
	inst.Provider = worker
	finished := make(chan struct{})
	turnErr := make(chan error, 1)
	go func() {
		defer close(finished)
		_, err := h.al.processSystemMessage(h.ctx, msg)
		turnErr <- err
	}()
	t.Cleanup(func() {
		judge.Release()
		select {
		case <-finished:
		case <-time.After(grWatchdog):
			t.Error("producing reminder did not finish after the Judge was released")
		}
	})
	select {
	case err := <-turnErr:
		require.NoError(t, err, "the producing reminder must finish before its Judge is released")
	case <-time.After(grWatchdog):
		t.Fatal("producing reminder did not finish")
	}
	inst.Provider = h.root
	select {
	case <-judge.entered:
	case <-time.After(grWatchdog):
		t.Fatal("the real Judge provider was not reached after the producing execution finished")
	}
	require.True(t, h.al.goalAdjudicationInFlight(h.id), "the claim must have registered the real verifier, not a test-made busy flag")
	require.Equal(t, session.LifecycleCompleted, h.load(t).State, "the ordinary reminder execution must already be complete")
	require.NotNil(t, worker.owner, "SETUP: the producing turn must expose its ordinary execution owner")
	select {
	case <-worker.owner.done:
	default:
		t.Fatal("the producing execution owner must be closed while the Judge provider is still held")
	}
	r6CarryGapAssertReadAndClaim(t, h, worker)
	return worker
}

func (w *r6CarryGapWorker) noteLive() {
	var owner *executionDisposition
	var turnID string
	w.h.al.activeTurnStates.Range(func(_, value any) bool {
		ts, ok := value.(*turnState)
		if !ok || ts.agentID != testDefaultAgentID || ts.transcriptSessionID != w.h.id {
			return true
		}
		ts.mu.RLock()
		owner = ts.opts.executionDisposition
		turnID = ts.turnID
		ts.mu.RUnlock()
		return false
	})
	if turnID == "" {
		return
	}
	w.mu.Lock()
	w.owner = owner
	w.turnID = turnID
	w.mu.Unlock()
}

func r6CarryGapAssertReadAndClaim(t *testing.T, h *grHarness, worker *r6CarryGapWorker) {
	t.Helper()
	worker.mu.Lock()
	require.Equal(t, 3, worker.calls, "read_file and goal_claim must both execute before the final text")
	worker.mu.Unlock()
	entries, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	found := map[string]session.ToolCall{}
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			found[string(call.ID)] = call
		}
	}
	readCall, readOK := found[worker.callID[0]]
	claimCall, claimOK := found[worker.callID[1]]
	require.True(t, readOK && claimOK, "both tool executions must be archived on the producing turn")
	assert.Equal(t, "read_file", readCall.Tool)
	assert.Equal(t, "success", readCall.Status, "the read must really succeed; a claim alone is not progress")
	assert.Equal(t, "goal_claim", claimCall.Tool)
	assert.Equal(t, "success", claimCall.Status)
	assert.Equal(t, "met", claimCall.Parameters["status"])
}

func r6CarryGapPendingTurn(t *testing.T, h *grHarness) string {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	require.NotNil(t, work.pending, "the completed producing reminder must be waiting for assessment")
	require.NotEmpty(t, work.pending.turnID)
	return work.pending.turnID
}

func r6CarryGapSettleWithoutPublish(t *testing.T, h *grHarness) {
	t.Helper()
	require.True(t, h.al.goalAdjudicationInFlight(h.id), "this settle must still see the real verifier")
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
}

func r6CarryGapAssertReadConsumedByBusyWindow(t *testing.T, h *grHarness, before time.Time, turnID string) {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	toolAt := work.toolActivityAt
	work.mu.Unlock()
	mark := goalReminderWatermark(h.goalID)
	require.True(t, toolAt.After(before), "the read must land after the reminder's earlier window")
	require.True(t, mark.After(before), "the busy settle must advance the fresh window")
	require.False(t, toolAt.After(mark), "the read must no longer sit inside the fresh window")
	r6CarryGapAssertPendingLatch(t, h, turnID, true)
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
}

func r6CarryGapAssertPendingLatch(t *testing.T, h *grHarness, turnID string, retained bool) {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	require.NotNil(t, work.pending)
	assert.Equal(t, turnID, work.pending.turnID, "the unmet verdict must not replace the producing reminder")
	assert.Equal(t, retained, work.observedProgress, "the busy window must keep the read for that same outcome")
}

func r6CarryGapJoinAdjudication(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(grWatchdog):
		t.Fatal("the real unmet adjudication did not finish after the Judge was released")
	}
}

func r6CarryGapAssertUnmetIsNotProgress(t *testing.T, h *grHarness, turnID string, roundBefore int) {
	t.Helper()
	require.False(t, h.al.goalAdjudicationInFlight(h.id), "Unregister must clear the verifier before the reminder is assessed")
	rec := goalRecordForSession(t, h.id)
	assert.Equal(t, "active", string(rec.State), "an unmet verdict with rounds remaining must not clear the goal")
	assert.Equal(t, roundBefore+1, rec.Round, "one unmet verdict consumes one round and is still not progress")
	assert.Less(t, rec.Round, rec.MaxRounds, "rounds must remain, so this is not exhaustion")
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "the verdict, its round, and the goal clock must not change the streak")
	r6CarryGapAssertPendingLatch(t, h, turnID, true)
	assert.Equal(t, 1, r6CarryGapJudgeCalls(t, h), "the unmet Judge must answer once, with no tool call")
}

func r6CarryGapJudgeCalls(t *testing.T, h *grHarness) int {
	t.Helper()
	judge, ok := h.al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	require.True(t, ok)
	held, ok := judge.Provider.(*r6CarryGapJudge)
	require.True(t, ok)
	return held.callCount()
}

func r6CarryGapDiscardQueuedSteers(t *testing.T, h *grHarness) {
	t.Helper()
	for {
		select {
		case <-h.al.bus.InboundChan():
		default:
			return
		}
	}
}

func r6CarryGapAssertAssessed(t *testing.T, h *grHarness, turnID string) {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	assert.Nil(t, work.pending, "assessment clears the producing reminder; the unmet verdict must not have cleared it earlier")
	assert.Equal(t, turnID, work.lastAssessedTurn, "the assessment must be of the same producing turn")
}

func r6CarryGapAssertOldReadNotFresh(t *testing.T, h *grHarness) {
	t.Helper()
	select {
	case msg := <-h.al.bus.InboundChan():
		require.Equal(t, h.id, msg.AsyncTranscriptSessionID)
		grTextReminder(t, h, msg)
		grSetStreak(t, h, 2)
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "the old read must not reset a later seeded text outcome")
	default:
		grSetStreak(t, h, 2)
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "with nothing new to assess, the old read must not reset a freshly seeded streak")
	}
}
