// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// ADR-093 amendment 2026-10-23 (founder decision) — RED pack for the
// launch-time steering backstop removal and the case-6/case-7 rulings.
// Oracle: docs/internal/architecture/ADR-093-open-conversation-must-keep-
// delegation.md, "Amended 2026-10-23" (Decision 1, 2, 3; the refusal
// keep-list; the case-6 and case-7 rulings), never the implementation.
//
// The founder's rulings this file pins:
//
//   - "this check must be removed, a session that ended can not fire
//     anything" — D2's launch-time backstop is gone; what guarantees it is
//     the TURN (Decision 3), not the record.
//   - Case 6: "if i stopped and give a new instruction so the agent restarts
//     should work" — a Stop only affects work in flight at Stop time; a new
//     turn (human message or a bound-session task restart) delegates
//     normally; an IN-FLIGHT fence still refuses visibly until the stop
//     lands.
//   - Case 7: ErrStaleGeneration is KEPT as the duplicate-launch guard — it
//     must never fire on a first launch; a Done task's bound-session
//     restart succeeds with the revived record at generation + 1.
//
// Plan (expectations written from the ADR before these tests ran): see the
// per-test comments. Real stores and real TaskExecutor/DelegateTool; the
// only doubles are LLM providers at the process edge (the established
// parked/completion fixtures).

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// adr093AmendmentDelegateArgs is the delegate(run) argument set every test
// in this file uses; the task text is arbitrary (the launch oracle does not
// depend on it).
func adr093AmendmentDelegateArgs() map[string]any {
	return map[string]any{
		"action":   "run",
		"agent_id": testDefaultAgentID,
		"task":     "Prepare the spreadsheet",
	}
}

// adr093AmendmentDelegateInSession executes the real delegate tool as a call
// from sessionID's turn (WithTranscriptSessionID — the production context a
// model-issued tool call carries).
func adr093AmendmentDelegateInSession(t *testing.T, al *AgentLoop, sessionID, callID string) *tools.ToolResult {
	t.Helper()
	tool := adr093DelegateTool(t, al)
	result := tool.Execute(tools.WithToolCallID(
		tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), sessionID), testDefaultAgentID),
		callID,
	), adr093AmendmentDelegateArgs())
	if result == nil {
		t.Fatal("delegate(run) returned nil")
	}
	return result
}

// TestAdr093GoalContinuationWakeTurn_CanDelegate is the triage repro
// verbatim (amendment point 5, "Added by the founder's rulings"): an
// ordinary main session's record reads terminal-completed — written when its
// previous human turn ended (D3) — a system wake publishes
// (async_notifier's Channel-"system" carrier → processSystemMessage), the
// wake turn runs WITHOUT reviving the record (MIN-004, kept), and a
// delegate(run) inside that live wake turn MUST SUCCEED with the child
// lifecycle file existing. This is the founder's Jarvis incident: before the
// amendment, D2's backstop refused exactly this call with D5's "Delegation
// is unavailable…" sentence while the conversation was visibly running.
func TestAdr093GoalContinuationWakeTurn_CanDelegate(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	// The triage's stale state: state=completed, generation 1, written by the
	// previous turn's settlement.
	parent := adr093Record(parentID, 1, session.LifecycleCompleted)
	adr093Persist(t, al, parent)

	provider, release := installParkedProvider(t, al)
	defer release()

	// The goal-continuation wake publishes through the system channel — the
	// carrier async_notifier.Notify builds (Channel "system") — which never
	// reaches the ordinary revival tail.
	wakeDone := make(chan error, 1)
	go func() {
		_, err := al.processSystemMessage(context.Background(), bus.InboundMessage{
			Channel:                  "system",
			ChatID:                   "webchat:" + parentID,
			Content:                  "goal reminder: continue the round",
			AsyncTranscriptSessionID: parentID,
			AsyncOriginAgentID:       testDefaultAgentID,
		})
		wakeDone <- err
	}()
	adr093WaitForEntered(t, provider, 30*time.Second)

	// The wake turn is LIVE while its record still reads completed — the
	// stale state the amendment removes as an obstacle (MIN-004 keeps the
	// no-revival half).
	stale := adr093Load(t, al, parentID)
	if stale.Generation != 1 || stale.State != session.LifecycleCompleted || stale.ResumedFrom != "" {
		t.Fatalf("record under the live wake turn = gen %d state %q resumed_from %q — MIN-004 (kept by the amendment): the wake never revives the record", stale.Generation, stale.State, stale.ResumedFrom)
	}

	// delegate(run) inside that wake turn must succeed and the child
	// lifecycle file must exist (amendment Decision 1; old spec: D5 refusal).
	result := adr093AmendmentDelegateInSession(t, al, parentID, "call-adr093-wake-delegate")
	if result.IsError {
		t.Fatalf("delegate(run) inside the live goal-continuation wake turn was refused:\n%s\n— ADR-093 amendment 2026-10-23 Decision 1: a running turn whose parent record is terminal CAN delegate (old spec: D5 refusal)", result.ForLLM)
	}
	childID := adr093ChildSessionIDFromResult(t, result.ForLLM, parentID)
	if _, err := al.GetSessionLifecycleStore().Load(childID); err != nil {
		t.Fatalf("child lifecycle file for %s missing after the delegated wake turn (amendment Decision 1: the child is published): %v", childID, err)
	}

	// The launch is not a revival and not a write: the parent's record is
	// exactly as the wake left it (D1; only a human message revives — D4).
	after := adr093Load(t, al, parentID)
	if after.Generation != 1 || after.State != session.LifecycleCompleted || after.ResumedFrom != "" {
		t.Fatalf("parent after delegation = gen %d state %q resumed_from %q — D1: the launch does not write the parent; a delegate is not the human's newer instruction", after.Generation, after.State, after.ResumedFrom)
	}

	release()
	select {
	case err := <-wakeDone:
		if err != nil {
			t.Fatalf("processSystemMessage (the wake turn): %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the wake turn did not return after the provider was released")
	}
	joinCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !al.WaitForActiveRequestsContext(joinCtx) {
		t.Fatal("the wake and delegated turns did not join after the provider was released")
	}
}

// TestAdr093NewTurnAfterStop_CanDelegate is case 6's delegate-path half
// (founder ruling: "if i stopped and give a new instruction so the agent
// restarts should work"). A LANDED-stopped root, a new human message starts
// a turn — ordinary admission revives the record first (same generation,
// fresh execution identity, fence and note cleared) — and a delegate(run)
// inside that new turn succeeds; the child lifecycle file exists. The ADR
// notes the delegate path "already satisfies the ruling from this amendment
// alone" (the revival clears the marker; the launch reads no marker after
// D2's removal), so this test is the ruling's keep-guard: green before the
// implementation change, and it must stay green after it.
func TestAdr093NewTurnAfterStop_CanDelegate(t *testing.T) {
	al, original, provider := r5AdmitRootWithAnswers(t,
		"partial root work", "root resumed exactly once", "helper spreadsheet prepared")
	sid := original.SessionID

	// Stop while the original run is parked; the owner lands the Stop.
	r5StopRoot(t, al, original)
	provider.open(0)
	joinGoalFixtureRuns(t, al)
	landed := rootReopenedRecord(t, al, sid)
	if landed.State != session.LifecycleStopped || landed.Stop != nil || landed.StopNote == nil || landed.Generation != original.Generation {
		t.Fatalf("setup: root after stop = %+v, want the landed stopped shape (same generation, fence cleared, note retained)", landed)
	}

	// The founder's new instruction starts a new turn: ordinary admission
	// revives the record BEFORE any tool runs (ADR-093 D4, unchanged).
	msg := rootHumanMessage(sid)
	msg.GatewayUserID = "r5-owner"
	msg.Content = "Right, delegate the spreadsheet work."
	if err := al.enqueueSteeringFromMessage(msg); err != nil {
		t.Fatalf("human message into the landed-stopped root: %v", err)
	}
	r1AwaitProvider(t, provider, 1)
	resumed := rootReopenedRecord(t, al, sid)
	if resumed.Generation != original.Generation || resumed.State != session.LifecycleRunning || resumed.Terminal() || resumed.Stop != nil || resumed.StopNote != nil {
		t.Fatalf("record under the new turn = %+v, want the same-generation revival (running, fence and note cleared)", resumed)
	}

	// delegate(run) inside that new turn must launch normally — no leftover
	// stop marker may refuse it (case 6 ruling).
	result := adr093AmendmentDelegateInSession(t, al, sid, "call-adr093-after-stop-delegate")
	if result.IsError {
		t.Fatalf("delegate(run) inside the new turn after Stop was refused:\n%s\n— case 6 (ADR-093 amendment 2026-10-23): once the user sends a new instruction and the agent starts a new turn, delegate must launch normally", result.ForLLM)
	}
	childID := adr093ChildSessionIDFromResult(t, result.ForLLM, sid)
	if _, err := al.GetSessionLifecycleStore().Load(childID); err != nil {
		t.Fatalf("child lifecycle file for %s missing after the post-stop delegation: %v", childID, err)
	}

	provider.open(1)
	provider.open(2)
	joinCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !al.WaitForActiveRequestsContext(joinCtx) {
		t.Fatal("the resumed and delegated turns did not join after the provider was released")
	}
}

// adr093AmendmentBoundTask binds a task to a REAL main session and seeds the
// bound session's lifecycle record directly in the exact shape a ruling
// speaks about — the same direct-seeding pattern u6SeedStoppedMain
// established. The S1 seam under test (boundSessionRetryable -> revive ->
// Dispatch) reads the record by t.SessionID and is session-shape agnostic;
// driving a full judged run to produce the binding would couple these RED
// tests to the goal-loop machinery, which no ruling names.
type adr093AmendmentBoundTask struct {
	al      *AgentLoop
	taskID  string
	boundID string
}

func adr093AmendmentNewBoundTask(t *testing.T, id string) *adr093AmendmentBoundTask {
	t.Helper()
	al, _ := newRunTaskPolicyLoop(t, nil)
	tk := createRunnableTask(t, al, id, rtAssignee)
	if _, err := al.GetSessionStore().GetOrCreateMainSession(testWS, rtAssignee); err != nil {
		t.Fatalf("setup: create the bound session: %v", err)
	}
	boundID, err := session.MainSessionID(testWS, rtAssignee)
	require.NoError(t, err, "setup: compute the bound session id")
	if _, err := GetTaskStore(al).Update(tk.ID, task.Patch{SessionID: &boundID}); err != nil {
		t.Fatalf("setup: bind the session to the task: %v", err)
	}
	return &adr093AmendmentBoundTask{al: al, taskID: tk.ID, boundID: boundID}
}

// adr093AmendmentSeedRecord persists the bound session's lifecycle record at
// generation 1 (task origin with this task's id, as persistLocked requires),
// with mutate applying the ruled shape on top.
func (h *adr093AmendmentBoundTask) seedRecord(t *testing.T, mutate func(*session.LifecycleRecord)) *session.LifecycleRecord {
	t.Helper()
	rec := &session.LifecycleRecord{
		SessionID:      h.boundID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    testWS,
		AgentID:        rtAssignee,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: h.taskID},
	}
	if mutate != nil {
		mutate(rec)
	}
	require.NoError(t, h.al.GetSessionLifecycleStore().Persist(rec), "setup: seed the bound record")
	return rec
}

// adr093AmendmentSetStatus writes the task status a ruling's scenario names
// (a board re-run sets a finished task back to a startable state).
func (h *adr093AmendmentBoundTask) setStatus(t *testing.T, s task.Status) {
	t.Helper()
	if _, err := GetTaskStore(h.al).Update(h.taskID, task.Patch{Status: &s}); err != nil {
		t.Fatalf("setup: set task status %q: %v", s, err)
	}
}

// TestAdr093BoundSessionRestart_LandedStop_RevivesSameGeneration is case 6's
// task surface (ADR-093 amendment, case-6 fix): a bound-session task restart
// after a LANDED stop succeeds via revive-then-dispatch — "a landed stop
// clears on the same generation exactly as a chat resume". OLD: the
// re-dispatch hit reserveDispatch's Stopped() refusal
// (steer.ErrDispatchCancelled) and failed the task with D5's sentence. NEW:
// the restart succeeds; the bound record shows the same-generation revival
// (fence and note cleared).
func TestAdr093BoundSessionRestart_LandedStop_RevivesSameGeneration(t *testing.T) {
	h := adr093AmendmentNewBoundTask(t, "t-adr093-restart-landed-stop")

	// Seed the landed-stopped shape: state LifecycleStopped, note retained,
	// live fence already cleared (the landed shape
	// TestAdr093RootRevival_ClassifyFirstOneTurnOneGeneration pins).
	h.seedRecord(t, func(rec *session.LifecycleRecord) {
		rec.State = session.LifecycleStopped
		rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: "human:dan", Seq: 1, Cause: session.StopCauseStop}
	})
	h.setStatus(t, task.StatusNext) // restartable: not Failed, so boundSessionRetryable passes

	sid, err := h.al.taskExecutor.StartTaskNow(context.Background(), h.taskID)
	if err != nil {
		t.Fatalf("bound-session task restart after a landed stop = %v — case 6 (ADR-093 amendment 2026-10-23): the restart must succeed via revive-then-dispatch (old spec: ErrDispatchCancelled refusal with the D5 sentence)", err)
	}
	require.Equal(t, h.boundID, sid, "the restart re-dispatches the task's own bound session")

	revived := adr093Load(t, h.al, h.boundID)
	if revived.Generation != 1 {
		t.Fatalf("bound record generation after the restart = %d, want 1 — a landed stop clears on the SAME generation (case-6 fix)", revived.Generation)
	}
	if revived.State == session.LifecycleStopped || revived.StopNote != nil || revived.Stop != nil {
		t.Fatalf("bound record after the restart = state %q note %+v stop %+v, want the landed stop cleared (fence and note gone)", revived.State, revived.StopNote, revived.Stop)
	}

	joinGoalFixtureRuns(t, h.al)
	got, gerr := GetTaskStore(h.al).Get(h.taskID)
	require.NoError(t, gerr)
	require.NotEqual(t, task.StatusFailed, got.Status, "the restart must not fail the task")
}

// TestAdr093BoundSessionRestart_InFlightFence_RefusesVisibly is case 6's
// kept refusal: an IN-FLIGHT stop fence — stop stamped, dying turn not yet
// exited — still refuses the restart VISIBLY until the stop lands. OLD: the
// refusal surfaced as D5's "Delegation is unavailable..." sentence. NEW: the
// refusal is the in-flight wording (revive_inbound.go::
// inboundStopFenceInFlight): "... is stopping (a stop is in flight for its
// current generation); retry once the stop has landed".
func TestAdr093BoundSessionRestart_InFlightFence_RefusesVisibly(t *testing.T) {
	h := adr093AmendmentNewBoundTask(t, "t-adr093-restart-inflight")

	// Seed the in-flight fence shape: live state, current-generation Stop,
	// the effect targeting the session's run (the fenced shape r5StopRoot
	// asserts after a real Stop).
	h.seedRecord(t, func(rec *session.LifecycleRecord) {
		rec.Stop = &session.Stop{At: time.Now().UTC(), Generation: rec.Generation, By: session.Principal{Kind: session.PrincipalKindHuman, ID: "dan"}}
		rec.StopEffect = &session.StopEffect{ControlID: "ctl-adr093-inflight", Target: session.StopEffectTarget{Generation: rec.Generation, RunID: "run-in-flight"}}
	})
	h.setStatus(t, task.StatusNext)

	_, err := h.al.taskExecutor.StartTaskNow(context.Background(), h.taskID)
	require.Error(t, err, "an in-flight stop fence still refuses the restart until the stop lands (case 6 keeps this refusal)")
	require.Contains(t, err.Error(), "is stopping (a stop is in flight for its current generation); retry once the stop has landed",
		"the refusal must be the VISIBLE in-flight wording (revive_inbound.go::inboundStopFenceInFlight), got %q", err.Error())
	require.NotContains(t, err.Error(), "Delegation is unavailable",
		"the refusal must not carry the deleted D5 sentence (ADR-093 amendment 2026-10-23 Decision 2), got %q", err.Error())
}

// TestAdr093BoundSessionRestart_DoneTask_StartsNextGeneration is case 7's
// narrowed surface (ADR-093 amendment): a DONE task's bound-session restart
// succeeds — finalizeTaskLifecycle lands a judged task's bound session
// terminal, and under the founder's "restarts should work" principle (F890-1:
// sessions are always resumable) the bound session is revived BEFORE the
// re-dispatch: terminal -> NEXT generation via resumed_from, transcript
// continuity kept. OLD: the re-dispatch hit reserveDispatch's ErrTerminal
// and failed the task. NEW: the restart succeeds; the record shows
// generation + 1 with resumed_from set.
func TestAdr093BoundSessionRestart_DoneTask_StartsNextGeneration(t *testing.T) {
	h := adr093AmendmentNewBoundTask(t, "t-adr093-restart-done")

	// Seed the judged end state the amendment names: the bound session lands
	// terminal-completed (finalizeTaskLifecycle), and the task is Done.
	h.seedRecord(t, func(rec *session.LifecycleRecord) {
		rec.State = session.LifecycleCompleted
	})
	h.setStatus(t, task.StatusDone) // a Done task is NOT refused by boundSessionRetryable

	sid, err := h.al.taskExecutor.StartTaskNow(context.Background(), h.taskID)
	if err != nil {
		t.Fatalf("Done task's bound-session restart = %v — case 7 (ADR-093 amendment 2026-10-23): the restart must succeed after the narrowing (old spec: ErrTerminal refusal with the D5 sentence)", err)
	}
	require.Equal(t, h.boundID, sid, "the restart re-dispatches the task's own bound session")

	revived := adr093Load(t, h.al, h.boundID)
	if revived.Generation != 2 {
		t.Fatalf("bound record generation after the restart = %d, want 2 — terminal revives on the NEXT generation (case-7 narrowing)", revived.Generation)
	}
	if revived.ResumedFrom != h.boundID {
		t.Fatalf("bound record resumed_from after the restart = %q, want %q — the terminal revival mints the next generation via resumed_from", revived.ResumedFrom, h.boundID)
	}
	if revived.Terminal() {
		t.Fatalf("bound record after the restart = %+v, want the revived (non-terminal) generation", revived)
	}

	joinGoalFixtureRuns(t, h.al)
	got, gerr := GetTaskStore(h.al).Get(h.taskID)
	require.NoError(t, gerr)
	require.NotEqual(t, task.StatusFailed, got.Status, "the restart must not fail the task")
}

// TestAdr093StaleGeneration_DuplicateAdmissionOnly is case 7's KEEP
// characterisation: ErrStaleGeneration only fires on a same-session
// duplicate/late admission — the record's generation moved between the read
// and the admission (a second wake racing a revival) — and NEVER on a first
// launch of a new turn. Characterization test: it pins the kept dispatch
// guard's boundary; the oracle is the amendment's case-7 assessment.
func TestAdr093StaleGeneration_DuplicateAdmissionOnly(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(parentID, 1, session.LifecycleRunning))
	l := NewSteerLauncher(al)

	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "first helper",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-adr093-stale-first"},
	})
	require.NoError(t, err, "setup: the fresh child launches")

	// A FIRST dispatch of a fresh child: never stale (case 7: "It cannot
	// block a first launch of a new turn").
	if _, err := l.Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		t.Fatalf("first dispatch of a fresh child = %v — case 7 KEEP: ErrStaleGeneration never fires on a first launch", err)
	}

	// A duplicate/late admission of the same session whose generation lost a
	// race against a revival (the record moved between read and admission):
	// refused with the kept sentinel.
	require.NoError(t, al.GetSessionLifecycleStore().Mutate(res.SessionID, func(rec *session.LifecycleRecord) error {
		rec.Generation = rec.Generation + 1
		rec.State = session.LifecycleRunning
		return nil
	}), "setup: a racing revival moves the child's generation")
	_, err = l.Dispatch(context.Background(), res.SessionID, res.Generation)
	require.ErrorIs(t, err, steer.ErrStaleGeneration,
		"case 7 KEEP: a duplicate/late admission on a moved generation refuses with steer.ErrStaleGeneration, got %v", err)
}

// TestAdr093KeepList_LaunchRefusalsStayShort sweeps the launch-input rows of
// the amendment's refusal keep-list: every KEEP refusal "returns its short
// reason", and removing D2's backstop must not take any of them with it.
// Depth, delegation policy, cannot-report and skill refusals keep their own
// dedicated suites (delegation_depth_integration_test.go,
// delegation_enforce_test.go, delegate_cannot_report_test.go,
// delegation_deny_wiring_test.go); this sweep pins the launcher's own
// input/identity refusals, which sit before any record is read.
func TestAdr093KeepList_LaunchRefusalsStayShort(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	steerer := newTestSteeringSession(t, al, adr093Workspace)
	l := NewSteerLauncher(al)

	t.Run("title required", func(t *testing.T) {
		_, err := l.Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: steerer,
			TargetAgentID:     testDefaultAgentID,
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-keep-title"},
		})
		require.ErrorIs(t, err, steer.ErrTitleRequired, "keep-list: bad input returns its short reason")
	})
	t.Run("task id required", func(t *testing.T) {
		_, err := l.Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: steerer,
			TargetAgentID:     testDefaultAgentID,
			Task:              "task without an id",
			Origin:            steer.Origin{Kind: steer.OriginKindTask},
		})
		require.ErrorIs(t, err, steer.ErrTaskIDRequired, "keep-list: bad input returns its short reason")
	})
	t.Run("unknown target agent", func(t *testing.T) {
		_, err := l.Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: steerer,
			TargetAgentID:     "no-such-agent-adr093",
			Task:              "unknown target",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-keep-agent"},
		})
		require.ErrorIs(t, err, steer.ErrAgentUnknown, "keep-list: unknown target agent returns its short reason")
	})
	t.Run("no launcher configured", func(t *testing.T) {
		_, err := NewSteerLauncher(nil).Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: steerer,
			TargetAgentID:     testDefaultAgentID,
			Task:              "no loop wired",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-keep-launcher"},
		})
		require.ErrorIs(t, err, steer.ErrStoreWrite, "keep-list: no launcher configured returns its short reason (ErrStoreWrite wrap)")
	})
	t.Run("dispatch no launcher configured", func(t *testing.T) {
		_, err := NewSteerLauncher(nil).Dispatch(context.Background(), steerer, 1)
		require.ErrorIs(t, err, steer.ErrStoreWrite, "keep-list: no launcher configured returns its short reason on Dispatch too")
	})
}
