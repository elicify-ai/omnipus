// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_review_fixes_test.go is the oracle suite for the goal-engine review
// findings (9, 10, 11) and the silent-failure findings SF-3, SF-5, SF-6 and
// SF-7 in pkg/agent's goal engine.
//
// Every test here drives a PRODUCTION entry point and supplies no wiring of
// its own beyond genuine arrange state. That is deliberate: the defect these
// findings share is a component that was built correctly and never connected,
// and the existing suites all passed because their harnesses performed the
// missing connection themselves. TestKeeperReachesAQuietTaskWithoutTestWiring
// below is the explicit statement of that rule — it clears the in-memory
// routing map before acting, so the ONLY route available is the one
// production persisted.
//
// Store-failure arrange: several tests fault the goal record's writes (and
// SF-3 its session transcript's reads) via the helpers in
// goal_store_fault_test.go. Reads still succeed and every write to the
// record under test fails, which is exactly the shape a full disk or a
// permissions fault produces in the field — and the shape under which each
// of SF-5/SF-6/SF-7/SF-9 rendered a failure as a success. Those faults are
// deliberately NOT permission bits: the CI worker runs as root, root ignores
// permission bits, and a chmod-based arrange therefore never fired there.
// See goal_store_fault_test.go's package comment for the mechanism and for
// the self-tests that keep the arrange honest.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- arrange helpers -------------------------------------------------------

// goalTranscriptContains reports whether any transcript entry on sid contains
// needle. The user-visible surface for a handover: this is what the operator
// reads in the run.
func goalTranscriptContains(t *testing.T, store *session.UnifiedStore, sid, needle string) bool {
	t.Helper()
	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatalf("ReadTranscript(%q): %v", sid, err)
	}
	for _, e := range entries {
		if strings.Contains(e.Content, needle) {
			return true
		}
	}
	return false
}

// seedTaskWithJudgeableGoal builds a task plus its paired DEFINING-phase goal
// record, exactly as rest_tasks.go's syncTaskGoalRecord does at task creation
// (GOAL-FR-009), with criteria whose ids the canned judge providers answer.
// It returns both, because a caller that wants to fault the task's goal
// record must be able to name it — the task's own id is NOT the goal id.
func seedTaskWithJudgeableGoal(
	t *testing.T, al *AgentLoop, taskID, condition string, srcChannel, srcChatID string,
) (*task.Task, *goal.Goal) {
	t.Helper()
	tk := &task.Task{
		ID: taskID, AgentID: "native-agent", WorkspaceID: "test-ws", Title: condition,
		Status: task.StatusNext, SourceChannel: srcChannel, SourceChatID: srcChatID,
	}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatalf("seedTaskWithJudgeableGoal: create task: %v", err)
	}
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.TaskExplicit,
		condition, "", recordedGoalCriteria(condition), newFloorDoD(), armedGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("seedTaskWithJudgeableGoal: goal.New: %v", err)
	}
	g.GoalID = newGoalID()
	if err := goal.NewStore(config.OmnipusHomeDir()).Create(g); err != nil {
		t.Fatalf("seedTaskWithJudgeableGoal: goal Create: %v", err)
	}
	return tk, g
}

// pushGoalActivityIntoThePast rewinds a goal record's activity clocks so the
// keeper's quiet window has already elapsed.
func pushGoalActivityIntoThePast(t *testing.T, goalID string, at time.Time) {
	t.Helper()
	past := at.UTC()
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(goalID, func(cur *goal.Goal) error {
		started := past
		cur.StartedAt = &started
		cur.LastActivityAt = past
		return nil
	}); err != nil {
		t.Fatalf("pushGoalActivityIntoThePast(%q): %v", goalID, err)
	}
}

// ===================== Finding 9: a met goal persists its verdict ==========

// TestMetVerdictIsPersistedBeforeTermination is review finding 9.
//
// Given a goal whose claim the Judge adjudicates MET
// When the adjudication completes and the goal terminates
// Then the record carries the winning verdict, the consumed round and the
// verdict's reason — not just a `met` state.
//
// The defect this closes: the met branch called clearGoal and returned
// without persisting anything. RecordVerdict was reached ONLY on the unmet
// path, so a SUCCESSFUL goal terminated with LatestVerdict == nil, Round
// still at its pre-adjudication value and LatestReason unchanged — directly
// contradicting Goal.Terminate's own stated contract that "the record
// survives with its criteria, their final statuses, THE VERDICT, the reason".
// The existing terminal-transition suite asserts state, criteria and
// TerminalReason and passes with the defect fully present.
func TestMetVerdictIsPersistedBeforeTermination(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	g := seedCriteriaOntoActiveGoal(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")})

	judgeInst.Provider = metJudgeProvider("every test is green")

	result := &turnResult{finalContent: "[goal:evidence] all tests green\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateMet {
		t.Fatalf("arrange/precondition: record state = %q, want %q — the met path did not run at all",
			after.State, generated.GoalStateMet)
	}
	if after.LatestVerdict == nil {
		t.Fatal("finding 9: a MET goal terminated with LatestVerdict == nil. Goal.Terminate's contract is " +
			"that the record survives WITH its verdict; runGoalAdjudication's met branch must call " +
			"RecordVerdict before clearGoal, exactly as its unmet branch does")
	}
	if !after.LatestVerdict.Met {
		t.Fatalf("finding 9: the persisted verdict says met=%v on a MET outcome — the wrong verdict was stored",
			after.LatestVerdict.Met)
	}
	if after.Round != 1 {
		t.Fatalf("finding 9: Round = %d after the goal's first (and winning) adjudication, want 1. "+
			"One adjudication is one round on the met path exactly as on the unmet path", after.Round)
	}
	if after.LatestReason == "" {
		t.Fatal("finding 9: LatestReason is empty after a MET verdict — the judge's reason must be retained " +
			"on the record, not only on the unmet path")
	}
}

// TestMetTaskGoalLeavesRealHistoryOnRerun is finding 9's downstream
// consequence, stated as its own oracle because it is the one a user would
// actually notice.
//
// Given a task-owned goal whose run SUCCEEDED
// When the task is re-run and the record re-enters the active phase
// Then the retained terminal-history entry carries that run's verdict and
// round.
//
// The defect: Reactivate builds the history entry from LatestVerdict and
// Round. With neither persisted on the met path, a successful run appended
// {Verdict: nil, Round: 0} — a task's successful runs left NO history at all,
// which is precisely the outcome a history is kept for.
func TestMetTaskGoalLeavesRealHistoryOnRerun(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	const condition = "ship the CSV exporter"
	tk, _ := seedTaskWithJudgeableGoal(t, al, "t-history-1", condition, "webchat", "c1")

	sid, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync: %v", err)
	}
	rec := activeGoalForSession(sid)
	if rec == nil {
		t.Fatal("arrange: the task's goal record must be active on the run's session")
	}

	judgeInst.Provider = metJudgeProvider("exporter shipped")
	opts := processOptions{
		TranscriptStore: al.GetSessionStore(), TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", IsTaskRun: true,
	}
	result := &turnResult{finalContent: "[goal:evidence] exporter merged\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)

	if got := readGoalRecord(t, rec.GoalID); got.State != generated.GoalStateMet {
		t.Fatalf("arrange/precondition: record state = %q, want met", got.State)
	}

	// The re-run: exactly what activateTaskGoal does when the task runs again.
	reran, uerr := goal.NewStore(config.OmnipusHomeDir()).Update(rec.GoalID, func(cur *goal.Goal) error {
		return cur.Reactivate("second-run-session", time.Now().UTC())
	})
	if uerr != nil {
		t.Fatalf("Reactivate: %v", uerr)
	}
	if len(reran.TerminalHistory) != 1 {
		t.Fatalf("TerminalHistory entries = %d, want 1", len(reran.TerminalHistory))
	}
	entry := reran.TerminalHistory[0]
	if entry.Verdict == nil {
		t.Fatal("finding 9: the retained history entry for a SUCCESSFUL run carries a nil verdict — " +
			"a met run left no history of why it was met")
	}
	if !entry.Verdict.Met {
		t.Fatalf("the retained history entry's verdict says met=%v for a successful run", entry.Verdict.Met)
	}
	if entry.Round != 1 {
		t.Fatalf("the retained history entry records Round = %d for a one-round successful run, want 1", entry.Round)
	}
}

// TestMetGoalIsNotTerminatedWhenItsVerdictCannotBePersisted is finding 9's
// failure branch, and covers SF-8's "the card sticks on judging forever" for
// the met path.
//
// Given a MET verdict and a goal store that refuses writes
// When the adjudication completes
// Then the goal is NOT marked met, and the card is repainted active rather
// than left on the judging pill.
//
// Terminating a goal whose winning verdict the store refused would freeze it
// `met` with no evidence of why, permanently — and the pre-fix code emitted
// no frame at all after the judging pill on any met-path failure, so the
// user's goal card span on `judging` with nothing ever correcting it.
func TestMetGoalIsNotTerminatedWhenItsVerdictCannotBePersisted(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	g := seedCriteriaOntoActiveGoal(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")})

	judgeInst.Provider = metJudgeProvider("every test is green")

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	result := &turnResult{finalContent: "[goal:evidence] all tests green\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	failGoalRecordWrites(t, g.GoalID)
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
	cleanup()

	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateActive {
		t.Fatalf("finding 9: record state = %q after a met verdict the store REFUSED, want %q — "+
			"a goal must not be frozen terminal with no record of the verdict that ended it",
			after.State, generated.GoalStateActive)
	}

	payloads := goalStatusPayloadsFor(c, sid)
	if len(payloads) == 0 {
		t.Fatal("no goal status frames emitted at all")
	}
	last := payloads[len(payloads)-1]
	if last.State == goalPillJudging {
		t.Fatal("SF-8: the LAST frame emitted is still the judging pill — the goal card sticks on `judging` " +
			"forever with nothing correcting it. A failed met path must repaint the card")
	}
	if last.State != goalPillActive {
		t.Fatalf("SF-8: last frame state = %q, want %q — the goal is still running, so the card must say so",
			last.State, goalPillActive)
	}
}

// ============ Finding 10: a task goal's routing is recorded ================

// TestActivateTaskGoalRecordsRouting is review finding 10's direct oracle.
//
// Given a task whose goal record is activated by the production dispatch path
// When the session is minted
// Then the goal record carries a route.
//
// The defect: activateTaskGoal never called recordGoalRouting — its only two
// call sites were the chat ones. A task-owned goal therefore had no
// RouteChannel/RouteChatID on the record and no entry in the in-memory map.
func TestActivateTaskGoalRecordsRouting(t *testing.T) {
	t.Run("from the task's own chat origin", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		tk, _ := seedTaskWithJudgeableGoal(t, al, "t-route-1", "ship it", "telegram", "chat-77")

		sid, err := al.taskExecutor.createTaskSessionSync(tk)
		if err != nil {
			t.Fatalf("createTaskSessionSync: %v", err)
		}
		rec := activeGoalForSession(sid)
		if rec == nil {
			t.Fatal("the task's goal record must be active on the run's session")
		}
		if rec.RouteChannel != "telegram" || rec.RouteChatID != "chat-77" {
			t.Fatalf("finding 10: goal record route = %q/%q, want the task's own origin telegram/chat-77. "+
				"activateTaskGoal must record routing the way the two chat activation paths do",
				rec.RouteChannel, rec.RouteChatID)
		}
	})

	t.Run("board-created task falls back to a reachable system destination", func(t *testing.T) {
		al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
		tk, _ := seedTaskWithJudgeableGoal(t, al, "t-route-2", "ship it", "", "")

		sid, err := al.taskExecutor.createTaskSessionSync(tk)
		if err != nil {
			t.Fatalf("createTaskSessionSync: %v", err)
		}
		rec := activeGoalForSession(sid)
		if rec == nil {
			t.Fatal("the task's goal record must be active on the run's session")
		}
		if rec.RouteChannel != "system" || rec.RouteChatID != "task:"+tk.ID {
			t.Fatalf("finding 10: goal record route = %q/%q, want system/task:%s — a board/REST-created "+
				"task has no chat origin, and AsyncNotifier.Notify rejects an empty destination outright, "+
				"so the fallback is what makes such a task reachable at all",
				rec.RouteChannel, rec.RouteChatID, tk.ID)
		}
	})
}

// TestKeeperReachesAQuietTaskWithoutTestWiring is finding 10's END-TO-END
// oracle, and the one that states this suite's rule: the test supplies NO
// routing of its own.
//
// Given a running task whose goal was activated by PRODUCTION
// When the keeper's follow-up dispatch runs with the in-memory routing map
// EMPTY (the state after a gateway restart)
// Then the push reaches the task's own session on the task's own channel,
// and the record carries no routing-lost note.
//
// The existing task-keeper suite (goal_triggers_task_test.go) passes only
// because its harness calls al.recordGoalRouting itself. Delete that one line
// and every test in it goes silent: no push is dispatched at all, and the
// goal's LatestReason is stamped "keeper cannot reach the goal's channel —
// routing lost", which then surfaces to the user in the goal status frame as
// if the goal itself were broken. This test clears the map on purpose so the
// ONLY route available is the one activateTaskGoal must persist.
//
// Scope note, recorded rather than worked around: the entry point exercised
// here is dispatchGoalAsyncFollowUp — the exact function finding 10 names as
// dying in routeFor — and NOT the whole goalQuietWindowSettle sweep. Driving
// the full sweep against a real task session does not reach the dispatch at
// all, for a SECOND and unrelated reason discovered while writing this test:
// createTaskSessionSync mints the task's session in the agent's PER-AGENT
// store (AgentLoop.GetAgentStore -> <agent workspace>/sessions) while
// goalQuietWindowSettle resolves session meta from the SHARED store
// (AgentLoop.GetSessionStore -> $OMNIPUS_HOME/sessions), so the sweep logs
// "could not read the goal's session meta — skipping" and returns. That is a
// separate defect from this one, it is not in this fixer's finding list, and
// papering over it here (by minting the session in the shared store the way
// goal_triggers_task_test.go's own helper does) would be the very
// test-supplies-the-wiring pattern this suite exists to eliminate.
func TestKeeperReachesAQuietTaskWithoutTestWiring(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	const condition = "ship the CSV exporter"
	tk, _ := seedTaskWithJudgeableGoal(t, al, "t-keeper-1", condition, "webchat", "c1")

	sid, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync: %v", err)
	}
	rec := activeGoalForSession(sid)
	if rec == nil {
		t.Fatal("the task's goal record must be active on the run's session")
	}

	// The restart: drop every in-memory trigger entry, including the routing
	// map. From here the ONLY route available is the persisted one.
	resetGoalTriggerStateForTest()
	dispatch := recordGoalDispatches(al)

	al.dispatchGoalAsyncFollowUp(sid, rec.GoalID, goalIdleSettleSourceKind, goalContinuePushPrompt(condition))

	evts := dispatch.all()
	if len(evts) != 1 {
		t.Fatalf("finding 10: the keeper dispatched %d follow-ups for a task, want exactly 1. "+
			"With no route on the record, dispatchGoalAsyncFollowUp dies in routeFor and NO keeper push "+
			"ever reaches a quiet task. Dispatched: %q", len(evts), dispatch.contents())
	}
	got := evts[0]
	if got.TranscriptSessionID != sid {
		t.Fatalf("dispatched session = %q, want the task run's own session %q", got.TranscriptSessionID, sid)
	}
	if got.Channel != "webchat" || got.ChatID != "c1" {
		t.Fatalf("dispatched destination = %q/%q, want the persisted route webchat/c1", got.Channel, got.ChatID)
	}
	if got.Content != goalContinuePushPrompt(condition) {
		t.Fatalf("dispatched content = %q, want the bounded continue-push", got.Content)
	}
	if got.SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("dispatched sender = %q, want %q — checkGoalLoopAfterTurn's origin gate drops anything else",
			got.SenderCanonicalID, goalLoopFollowUpSenderID)
	}

	after := readGoalRecord(t, rec.GoalID)
	if strings.Contains(after.LatestReason, "routing lost") {
		t.Fatalf("finding 10: LatestReason = %q — the routing-lost note was stamped onto a task whose "+
			"route production should have recorded, and it surfaces to the user in the goal status frame",
			after.LatestReason)
	}
}

// ================== SF-3: the claim-scan watermark ========================

// TestClaimScanWatermarkSurvivesATranscriptReadError is SF-3.
//
// Given a real goal_claim tool call in the transcript
// When the transcript read FAILS on the turn that would resolve it
// Then the claim is still found on the next turn.
//
// The defect: the watermark was advanced unconditionally, including when
// resolveToolClaim returned the zero tuple because the READ failed (it logs a
// WARN and returns found=false, indistinguishable from "scanned cleanly, no
// claim"). A single transient read error moved the scan window past a real,
// unread claim, so no later pass would ever look there again — the agent had
// claimed completion and nothing ever adjudicated it.
func TestClaimScanWatermarkSurvivesATranscriptReadError(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	judgeInst.Provider = metJudgeProvider("done")

	g := seedActiveGoalRecord(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")}, nil)

	// A real, successful goal_claim tool call, recorded exactly as loop.go's
	// tcRecord construction persists one.
	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: "claim-1", Type: session.EntryTypeToolCall, Role: "assistant", Timestamp: time.Now().UTC(),
		ToolCalls: []session.ToolCall{{
			ID: "tc-1", Tool: tools.GoalClaimToolName, Status: "success",
			Result: map[string]any{"text": `{"status":"met","evidence":"all tests green","goal_id":"` + g.GoalID + `"}`},
		}},
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict: %v", err)
	}

	// The storage fault: every read of this session's transcript now ERRORS
	// (not "returns nothing" — see failTranscriptReads), until restored.
	restoreTranscript := failTranscriptReads(t, store, sid)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	// Turn 1: the read fails. Nothing is resolved — that part is correct and
	// unavoidable. What must NOT happen is the watermark moving.
	failed := &turnResult{finalContent: "still working"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, failed)
	if failed.goalDeferredAdjudication != nil {
		t.Fatal("arrange: the transcript read was expected to fail, but a claim was resolved anyway — " +
			"the read-fault arrange did not reach the claim scan")
	}
	if _, ok := al.goalClaimScanWatermark(g.GoalID); ok {
		t.Fatal("SF-3: the claim-scan watermark was advanced after a FAILED transcript read. " +
			"The scan window has now moved past an unread, real claim and no later pass will ever find it")
	}

	// Turn 2: storage recovers, with the transcript's own bytes back in
	// place. The claim must still be discoverable.
	restoreTranscript()
	recovered := &turnResult{finalContent: "still working"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, recovered)
	if recovered.goalDeferredAdjudication == nil {
		t.Fatal("SF-3: the real goal_claim call was never resolved once storage recovered — a transient " +
			"read error silently skipped a completion claim forever")
	}
	if got := recovered.goalDeferredAdjudication.claimText; got != "all tests green" {
		t.Fatalf("resolved claim evidence = %q, want the tool call's own evidence argument", got)
	}
}

// ================== SF-6: an uncounted push is unbounded ==================

// TestKeeperDoesNotDispatchAnUncountedPush is SF-6.
//
// Given a quiet goal and a goal store that refuses writes
// When the keeper's quiet-window sweep runs
// Then no continue-push is dispatched, and the keeper re-arms rather than
// wedging.
//
// The defect: both keeper ladders computed the next push count, WARNed on
// persist failure and dispatched the follow-up ANYWAY — while the bound is
// re-read from the store on every tick. With the store unwritable the counter
// never moves, so the "bounded" ladder becomes an unbounded one: a real agent
// turn, spending real tokens, every quiet window, forever.
func TestKeeperDoesNotDispatchAnUncountedPush(t *testing.T) {
	resetGoalTriggerStateForTest()
	withShortIdleWindow(t, 2*time.Second)
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)

	g := seedActiveGoalRecord(t, sid, "ship the exporter",
		recordedGoalCriteria("ship the exporter"), nil)
	pushGoalActivityIntoThePast(t, g.GoalID, time.Now().Add(-1*time.Hour))
	al.recordGoalRouting(sid, g.GoalID, "webchat", "c1", "sk1", agentInst.ID)

	dispatch := recordGoalDispatches(al)
	failGoalRecordWrites(t, g.GoalID)

	al.goalQuietWindowSettle(time.Now())

	if evts := dispatch.all(); len(evts) != 0 {
		t.Fatalf("SF-6: the keeper dispatched %d follow-up(s) whose push count it could NOT persist, want 0. "+
			"The bound is re-read from the store every tick, so an uncounted push makes the ladder "+
			"unbounded. Dispatched: %q", len(evts), dispatch.contents())
	}
	if al.goalIsIdleSettling(g.GoalID) {
		t.Fatal("SF-6: the idleSettling marker was left set after a push that never dispatched — the keeper " +
			"is now wedged at the goalIsIdleSettling gate and will never retry, even once storage recovers")
	}
}

// ================== SF-7: handovers must follow the transition ============

// TestIdleExpiryWritesNoHandoverWhenTheTransitionFails is SF-7.
//
// Given a goal idle past the expiry window and a goal store that refuses
// writes
// When the idle-expiry sweep runs
// Then nothing is written into the user's transcript and the goal stays
// active.
//
// The defect: the handover was written to the user's OWN transcript BEFORE
// the terminal transition was attempted, and clearGoal's return value was
// discarded. On a store failure the user read "Goal X idle-expired" in their
// transcript while the record was still ACTIVE and the keeper kept sweeping
// it — a statement about their own goal that was simply false, with no way
// for them to tell.
func TestIdleExpiryWritesNoHandoverWhenTheTransitionFails(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	g := seedActiveGoalRecord(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")}, nil)
	pushGoalActivityIntoThePast(t, g.GoalID, time.Now().Add(-30*24*time.Hour))

	failGoalRecordWrites(t, g.GoalID)
	al.goalIdleExpirySweep(config.PlanningConfig{}, time.Now().UTC())

	if goalTranscriptContains(t, store, sid, "idle-expired") {
		t.Fatal("SF-7: an idle-expiry handover was written into the user's own transcript while the " +
			"terminal transition had FAILED and the goal is still active. The user reads that their " +
			"goal ended; it did not")
	}
	after := readGoalRecord(t, g.GoalID)
	if after.State != generated.GoalStateActive {
		t.Fatalf("precondition: record state = %q, want active — the store-failure arrange did not bite",
			after.State)
	}
}

// ================== SF-5: never render a rejected projection ==============

// TestRejectedProjectionIsNotEmittedToTheUI is SF-5.
//
// Given an UNMET verdict whose criterion-status projection the store refuses
// When the adjudication emits its goal status frame
// Then the frame carries the DURABLE criterion statuses, not the projected
// ones.
//
// The defect: the projection was persisted best-effort and then emitted to
// the UI regardless of whether the persist succeeded. The user watched the
// criterion ticks flip while the durable record still said pending; on reload
// they reverted. A persistence fault rendered as a success is the one failure
// shape a user can neither see nor report accurately.
//
// The observable is the frame emitted by the round-advance-persist-failed
// branch, which is the branch a frozen store reaches — it carries exactly the
// projected/unprojected lists this finding is about.
func TestRejectedProjectionIsNotEmittedToTheUI(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	g := seedCriteriaOntoActiveGoal(t, sid, "make the tests pass",
		[]task.AcceptanceCriterion{terminalTestCriterion("the tests pass")})

	for _, c := range readGoalRecord(t, g.GoalID).Criteria {
		if c.Status != task.CritPending {
			t.Fatalf("arrange: criterion %q starts at %q, want %q", c.ID, c.Status, task.CritPending)
		}
	}

	judgeInst.Provider = unmetJudgeProvider("one test still fails")

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	result := &turnResult{finalContent: "[goal:evidence] partial\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	failGoalRecordWrites(t, g.GoalID)
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)
	cleanup()

	// The durable truth, read back: the projection was refused.
	for _, crit := range readGoalRecord(t, g.GoalID).Criteria {
		if crit.Status != task.CritPending {
			t.Fatalf("precondition: durable criterion %q = %q, want %q — the store-failure arrange did not bite",
				crit.ID, crit.Status, task.CritPending)
		}
	}

	var withCriteria *GoalStatusChangedPayload
	for i, p := range goalStatusPayloadsFor(c, sid) {
		if len(p.Criteria) > 0 {
			withCriteria = &goalStatusPayloadsFor(c, sid)[i]
		}
	}
	if withCriteria == nil {
		t.Fatal("no goal status frame carrying criteria was emitted — nothing to assert against")
	}
	for _, crit := range withCriteria.Criteria {
		if crit.Status != task.CritPending {
			t.Fatalf("SF-5: the emitted frame paints criterion %q as %q while the durable record still "+
				"says %q. The user watches the tick flip and it reverts on reload — a rejected write "+
				"must never be rendered as a success",
				crit.ID, crit.Status, task.CritPending)
		}
	}
}

// ================== SF-4: an unparseable claim is never silent ============

// TestUnparseableGoalClaimIsReportedNotSwallowed is SF-4.
//
// Given a successful goal_claim tool call whose recorded result is not the
// expected JSON object
// When the claim scan runs
// Then the claim is still skipped (it is genuinely unusable) but the
// rejection is REPORTED — counted, and WARNed beside the counter.
//
// The defect: the skip was a bare `|| json.Unmarshal(...) != nil ||` arm of
// one condition with ZERO logging. The result shape is produced in
// pkg/tools/goal_claim.go and persisted by loop.go's tcRecord construction,
// two independent places it can drift; if it ever drifts, EVERY claim stops
// resolving at once and the goal loop looks merely quiet rather than broken.
// The counter is the assertable half (the same precedent
// taskGoalTranscriptWriteFailures set for this package); the WARN beside it
// carries the detail a log reader needs.
func TestUnparseableGoalClaimIsReportedNotSwallowed(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	cases := []struct {
		name   string
		result map[string]any
	}{
		{"no result text at all", map[string]any{}},
		{"result text is not JSON", map[string]any{"text": "the goal is done"}},
		{"JSON object with an empty status", map[string]any{"text": `{"status":"","evidence":"x"}`}},
	}
	for i, c := range cases {
		if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
			ID: "bad-claim-" + c.name, Type: session.EntryTypeToolCall, Role: "assistant",
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
			ToolCalls: []session.ToolCall{{
				ID: "tc-bad", Tool: tools.GoalClaimToolName, Status: "success", Result: c.result,
			}},
		}); err != nil {
			t.Fatalf("AppendTranscriptStrict(%s): %v", c.name, err)
		}
	}

	before := GoalClaimUnparseableResults()
	found, status, evidence, goalID, readErr := al.resolveToolClaim(store, sid, time.Time{})
	if readErr != nil {
		t.Fatalf("the transcript read must succeed here: %v", readErr)
	}
	if found {
		t.Fatal("an unparseable goal_claim result must NOT resolve as a claim — there is nothing usable in it")
	}
	// The three payload returns must be empty alongside found=false: a caller
	// that reads them without checking found must not pick up a half-parsed
	// status/evidence/goal id out of an unusable claim.
	if status != "" || evidence != "" || goalID != "" {
		t.Fatalf("an unresolved claim must carry no payload, got status=%q evidence=%q goal_id=%q",
			status, evidence, goalID)
	}
	if got := GoalClaimUnparseableResults() - before; got != uint64(len(cases)) {
		t.Fatalf("SF-4: %d unparseable goal_claim results were reported, want %d. Each unusable claim "+
			"must be counted and WARNed; a silent skip means that if the result shape ever drifts, every "+
			"claim stops resolving at once with nothing anywhere saying so", got, len(cases))
	}
}

// ================== SF-9: the task path fails as loudly as chat ===========

// TestTaskGoalActivationFailureIsLoud is SF-9.
//
// Given a task whose goal record cannot be activated (the store refuses the
// write)
// When the task's session is minted
// Then the failure is returned AND written into the task's own session
// transcript, so a run with no goal loop is visible rather than merely quiet.
//
// The defect: activateTaskGoal was a void function that swallowed three
// distinct failures — ErrOwnerNotFound at DEBUG, the other two bare-returned
// after one WARN. The CHAT side fails in front of the user
// (createAndActivateSessionGoalRecord's error makes applyGoalCommandPrompt
// reply "Could not start the goal loop"), so a task ran to completion with no
// adjudication and no criteria judged while nothing an operator reads said
// so. That asymmetry breaks ADR-086's identical-behaviour promise
// (GOAL-FR-013) in the direction that hides a defect.
func TestTaskGoalActivationFailureIsLoud(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	tk, taskGoal := seedTaskWithJudgeableGoal(t, al, "t-loud-1", "ship it", "webchat", "c1")

	failGoalRecordWrites(t, taskGoal.GoalID)

	sid, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync must still succeed — GOAL-FR-023: a task with no usable goal "+
			"still runs: %v", err)
	}
	if sid == "" {
		t.Fatal("the task session must still be created")
	}

	if aerr := al.taskExecutor.activateTaskGoal(tk, sid); aerr == nil {
		t.Fatal("SF-9: activateTaskGoal returned nil for a goal record the store refused to activate. " +
			"The task now runs with NO goal loop and the caller cannot tell")
	}

	sessStore := al.GetAgentStore(tk.AgentID)
	if sessStore == nil {
		t.Fatal("the agent's session store must exist")
	}
	if !goalTranscriptContains(t, sessStore, sid, "goal could not be activated") {
		t.Fatal("SF-9: nothing was written into the task's own session transcript about the failed goal " +
			"activation. The chat side fails in front of the user; the task side must be equally visible " +
			"in the surface an operator actually reads — the run transcript")
	}
	if !goalTranscriptContains(t, sessStore, sid, "WITHOUT a goal loop") {
		t.Fatal("SF-9: the transcript line must say what the failure COSTS — that this run has no goal " +
			"loop and no criteria will be adjudicated — not merely that something failed")
	}
}
