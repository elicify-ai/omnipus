// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_claim_wiring_adr084_test.go carries the RED tests for ADR-084 §13
// (2026-10-23 amendment, issue #1431): every goal_claim(status: met) claim is
// recorded and adjudicated — an empty or whitespace-only evidence line is NOT
// refused — the FR-095 claimless tripwire never refuses a claim a successful
// goal_claim call recorded, an unmet ruling consumes a round as usual, and
// the round/try bounds end a chat goal and a task run failed. Every oracle
// comes from the amendment and the amended judge-active-reviewer spec
// (FR-087, FR-088, E-28, US-11 AC-2, SC-015, the FR-088 test rows), never
// from the implementation. The Judge is faked at the PROVIDER boundary only
// (providers.LLMProvider): the tool, the claim path, the goal store and the
// task executor are real.
//
// This is the file the spec's amended FR-088 test-coverage row names. It also
// replaces TestTaskExecutor_AdjudicateClaim_EmptyClaimFailsClosedWithoutVerifierDispatch
// (task_executor_adjudicate_claim_test.go), deleted per §13 "Tests and specs
// that must change": its premise — an empty claim refused before any verifier
// dispatch — is exactly what the amendment removes.

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wiringClaimMetAndPersist executes the REAL goal_claim tool on the working
// agent and persists the call exactly the way loop.go records a successful
// tool call (Result["text"] carrying the tool's own output) — the shape
// resolveToolClaim reads back (the b6ClaimMetAndPersist precedent). It exists
// separately so a REFUSAL fails naming the amendment, not a generic message.
func wiringClaimMetAndPersist(
	t *testing.T, claimTool tools.Tool, store *session.UnifiedStore, sid, callID, evidence string,
) {
	t.Helper()
	ctx := tools.WithTranscriptSessionID(context.Background(), sid)
	res := claimTool.Execute(ctx, map[string]any{"status": tools.GoalClaimStatusMet, "evidence": evidence})
	if res == nil {
		t.Fatal("goal_claim returned a nil result")
	}
	if res.IsError {
		t.Fatalf("ADR-084 §13 (issue #1431): goal_claim(status:met, evidence:%q) must be RECORDED, not refused — got an error: %q", evidence, res.ForLLM)
	}
	if err := store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: callID, Type: session.EntryTypeToolCall, Role: "assistant", Timestamp: time.Now().UTC(),
		ToolCalls: []session.ToolCall{{
			ID: session.ToolCallID(callID), Tool: tools.GoalClaimToolName, Status: "success",
			Result: map[string]any{"text": res.ForLLM},
		}},
	}); err != nil {
		t.Fatalf("persist goal_claim call %q: %v", callID, err)
	}
}

// TestGoalClaimWiring_EmptyEvidenceMetClaimIsRecordedAndAdjudicated — the
// amended FR-088 / US-11 AC-2 (ADR-084 §13, issue #1431): a chat goal whose
// worker calls goal_claim(status: met, evidence: "") has that claim RECORDED
// (no error result) and an adjudication scheduled after the turn; the Judge
// rules met or unmet; an unmet ruling consumes exactly one round and steers
// with its reason; and the tool path never touches the G-4 bare-claim ladder
// (the streak is NOT incremented — FR-091, retired by construction per §13).
func TestGoalClaimWiring_EmptyEvidenceMetClaimIsRecordedAndAdjudicated(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	cond := "write report.md summarising the three supplier quotes"
	gid := armGoalRecord(t, sid, cond, recordedGoalCriteria(cond), 0, time.Now())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)

	const judgeReason = "the empty evidence line names nothing and the transcript shows the criterion unmet"
	judge := &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: judgeReason}
	judgeInst.Provider = judge
	dispatches := recordGoalDispatches(al)

	claimTool, ok := agentInst.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("goal_claim is not registered on the working agent — the claim channel under test does not exist")
	}

	wiringClaimMetAndPersist(t, claimTool, store, sid, "red-empty-claim-1", "")
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	turn := &turnResult{finalContent: "I claim the goal met."}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, turn)

	if turn.goalDeferredAdjudication == nil {
		t.Fatal("an empty-evidence met claim must be recorded and schedule an adjudication (ADR-084 §13, issue #1431) — got no deferred adjudication work")
	}
	if got := turn.goalDeferredAdjudication.claimText; got != "" {
		t.Fatalf("deferred adjudication claimText = %q, want the recorded (empty) evidence line — JUDGE-FR-094 places it as the Judge's ClaimText", got)
	}
	al.dispatchDeferredGoalAdjudication(turn.goalDeferredAdjudication)

	if n := judge.callCount(); n != 1 {
		t.Fatalf("Judge calls = %d, want exactly 1 — the FR-095 tripwire must never refuse a claim a successful goal_claim call recorded (ADR-084 §13)", n)
	}
	if asked := judge.askedOnCall(1); len(asked) == 0 {
		t.Fatal("fixture broken: the Judge was asked about no criteria, so this adjudication would be indistinguishable from an empty one")
	}
	live := mustGoalRecord(t, gid)
	if live.Round != 1 {
		t.Errorf("rounds consumed = %d, want 1 — an unmet ruling uses an attempt as usual (ADR-084 §13, decision line 3)", live.Round)
	}
	if live.State != generated.GoalStateActive {
		t.Errorf("goal state after one unmet ruling within the bound = %q, want active", live.State)
	}
	// The bare-claim streak lives in the package-level trigger state, keyed by
	// goal id (FR-052); read it under its own mutex.
	trig := goalTriggers()
	trig.mu.Lock()
	streak := trig.bareClaimStreak[gid]
	trig.mu.Unlock()
	if streak != 0 {
		t.Errorf("bare-claim streak = %d, want 0 — the tool path must not touch the G-4 ladder (FR-091; §13: retired by construction)", streak)
	}
	evts := dispatches.all()
	if len(evts) != 1 {
		t.Fatalf("steers dispatched after the unmet ruling = %d, want exactly 1 (contents: %q)", len(evts), dispatches.contents())
	}
	if !strings.Contains(evts[0].Content, judgeReason) {
		t.Errorf("steer = %q, want it to carry the Judge's reason — the amended AC-2 says an unmet ruling consumes a round and steers", evts[0].Content)
	}
}

// TestGoalClaimWiring_RepeatedEmptyMetClaimsEndGoalFailedAtTheRoundBound —
// ADR-084 §13 + SC-015 as amended: a goal whose worker repeats
// goal_claim(status: met, evidence: "") has every claim judged — exactly one
// Judge round per claim (INV-1) — and ends FAILED with a rounds_exhausted
// ending at the record's configured bound. The test loop is finite by
// construction; the terminal assertions are what prove the bound (not the
// loop) stops the defect.
func TestGoalClaimWiring_RepeatedEmptyMetClaimsEndGoalFailedAtTheRoundBound(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	cond := "write report.md summarising the three supplier quotes"
	// The record's MaxRounds is the configured bound under test (§13: the
	// record's bound, falling back to the global limit).
	gid := armGoalRecord(t, sid, cond, recordedGoalCriteria(cond), 0, time.Now())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)

	judge := &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: "no verified work in the transcript"}
	judgeInst.Provider = judge

	claimTool, ok := agentInst.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("goal_claim is not registered on the working agent — the claim channel under test does not exist")
	}

	const bound = armedGoalMaxRounds
	for i := 0; i < bound; i++ {
		time.Sleep(5 * time.Millisecond) // the claim scan reads entries strictly after the previous round's watermark (B6 precedent)
		wiringClaimMetAndPersist(t, claimTool, store, sid, fmt.Sprintf("red-empty-claim-%d", i), "")
		opts := processOptions{
			TranscriptStore: store, TranscriptSessionID: sid,
			Channel: "webchat", ChatID: "c1", SessionKey: "sk1",
		}
		if i == 0 {
			opts.UserInitiated = true
		} else {
			opts.SenderID = goalLoopFollowUpSenderID
		}
		turn := &turnResult{finalContent: "claiming met again with no evidence."}
		al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, turn)
		if turn.goalDeferredAdjudication == nil {
			t.Fatalf("round %d: the empty-evidence met claim was not recorded — no deferred adjudication was scheduled (ADR-084 §13)", i+1)
		}
		al.dispatchDeferredGoalAdjudication(turn.goalDeferredAdjudication)
	}

	if n := judge.callCount(); n != bound {
		t.Fatalf("Judge calls = %d, want exactly %d — one adjudication and one round per claim (INV-1; §13), none past the bound", n, bound)
	}
	final := mustGoalRecord(t, gid)
	// GoalStateExhausted is the record enum's terminal for a round-bound end
	// (the R-14 rename of "failed": done→met, failed→exhausted) — §13's
	// "ends the goal failed (rounds_exhausted)".
	if final.State != generated.GoalStateExhausted {
		t.Fatalf("goal state after %d empty-evidence claims = %q, want %q (failed at the configured bound; ADR-084 §13: goals always end done or failed)", bound, final.State, generated.GoalStateExhausted)
	}
	if final.Round != bound {
		t.Errorf("rounds used = %d, want %d", final.Round, bound)
	}
	out := requireOneGoalOutcome(t, store, sid, gid)
	if out.GoalOutcome.Ending != generated.GoalOutcomeEndingRoundsExhausted {
		t.Errorf("goal outcome ending = %q, want %q — the bound ends the goal rounds_exhausted (ADR-084 §13)", out.GoalOutcome.Ending, generated.GoalOutcomeEndingRoundsExhausted)
	}
}

// TestRunGoalAdjudication_RecordedEmptyEvidenceClaimReachesTheJudge — the
// §13 engine invariant at the deferred-dispatch seam:
// runGoalAdjudicationOutcome, driven the SAME way the claim path drives it
// (dispatchDeferredGoalAdjudication over a goalDeferredAdjudicationWork with
// an empty claimText), must NOT refuse an adjudication whose claim a
// successful goal_claim call recorded. TestRunGoalAdjudication_RejectsEmptyClaimText
// (goal_triggers_adr084_test.go) keeps guarding the genuinely CLAIMLESS
// caller — there no goal_claim happened; here one did ("a claim a successful
// goal_claim call recorded is not claimless, even when its evidence line is",
// §13). When the claim path signals claim-presence — §13 leaves the mechanism
// ("a flag on the existing call") to backend-lead — only this call site's
// arguments change; the assertions below are the requirement, not the
// mechanism.
func TestRunGoalAdjudication_RecordedEmptyEvidenceClaimReachesTheJudge(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	_, sid := newGoalTestSession(t, al, agentInst.ID) // the dispatcher re-reads the store itself; the test holds no store ref
	cond := "write report.md summarising the three supplier quotes"
	gid := armGoalRecord(t, sid, cond, recordedGoalCriteria(cond), 0, time.Now())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)

	const judgeReason = "the empty claim names no evidence and the transcript shows the criterion unmet"
	judge := &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: judgeReason}
	judgeInst.Provider = judge
	dispatches := recordGoalDispatches(al)

	// Exactly the payload checkGoalLoopAfterTurn records for a recorded
	// empty-evidence claim (FR-094: the evidence line, trimmed — empty here).
	work := &goalDeferredAdjudicationWork{
		agentInst: agentInst, sessionID: sid, claimText: "",
		goalID: gid, goalPrompt: cond, claimRecordedAt: time.Now().UTC(),
	}
	al.dispatchDeferredGoalAdjudication(work)

	if n := judge.callCount(); n != 1 {
		t.Fatalf("Judge calls = %d, want exactly 1 — ADR-084 §13: the FR-095 tripwire must never refuse a claim a successful goal_claim call recorded; the Judge receives the compiled criteria with the claim text placed last (possibly empty)", n)
	}
	if asked := judge.askedOnCall(1); len(asked) == 0 {
		t.Fatal("fixture broken: the Judge was asked about no criteria")
	}
	live := mustGoalRecord(t, gid)
	if live.Round != 1 {
		t.Errorf("rounds consumed = %d, want 1 — an unmet ruling consumes an attempt as usual (ADR-084 §13, decision line 3)", live.Round)
	}
	if live.State != generated.GoalStateActive {
		t.Errorf("goal state = %q, want active after one unmet ruling within the bound", live.State)
	}
	evts := dispatches.all()
	if len(evts) != 1 {
		t.Fatalf("steers dispatched = %d, want exactly 1 — an unmet ruling re-dispatches the worker with the Judge's steer (ADR-084 §13)", len(evts))
	}
	if !strings.Contains(evts[0].Content, judgeReason) {
		t.Errorf("steer = %q, want it to carry the Judge's reason", evts[0].Content)
	}
}

// TestTaskRun_RepeatedEmptyEvidenceClaimsEndTaskFailedWithinMaxAttempts —
// §13's task-ladder ruling: adjudicateRunClaim no longer pre-guards an empty
// evidence claim, so every goal_claim(status: met, evidence: "") in a task
// run is JUDGED; each unmet ruling spends the run's goal try; and the try
// budget ends the run, so the task ends failed within max_attempts instead of
// looping. Replaces TestTaskExecutor_AdjudicateClaim_EmptyClaimFailsClosedWithoutVerifierDispatch
// (deleted per §13 "Tests and specs that must change").
func TestTaskRun_RepeatedEmptyEvidenceClaimsEndTaskFailedWithinMaxAttempts(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("")) // every turn claims met with an empty evidence line (the last repeats)
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = 1 })
	judge := &b6ScriptedJudge{metFromCall: alwaysUnmet, reason: "the empty evidence line verifies nothing"}
	judgeInst.Provider = judge

	const maxAttempts = 2
	budget := maxAttempts
	tk := createTaskWithGoal(t, al, &task.Task{
		ID: "task-red-empty-claims", Title: "empty evidence claims", Prompt: "write report.md summarising the three supplier quotes.",
		Action: task.ActionLLM, AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &budget,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "report.md lists all three supplier quotes")},
	}, "the report file exists and lists the quotes")

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := t3WaitForTerminal(t, al, tk.ID, maxAttempts)

	if final.Status != task.StatusFailed {
		t.Fatalf("task status = %q, want failed — the try budget must end the task (ADR-084 §13 task-ladder ruling)", final.Status)
	}
	if final.AttemptCount != maxAttempts {
		t.Errorf("attempts used = %d, want %d — every empty-evidence claim is judged and spends its run's goal try; the task ends WITHIN max_attempts", final.AttemptCount, maxAttempts)
	}
	if n := judge.callCount(); n != maxAttempts {
		t.Fatalf("Judge calls = %d, want %d — one adjudication per run's empty-evidence claim; zero means the old pre-guard refused it instead of judging it (ADR-084 §13)", n, maxAttempts)
	}
	if asked := judge.askedOnCall(1); len(asked) == 0 {
		t.Fatal("fixture broken: the Judge was asked about no criteria on the first claim")
	}
	if turns := worker.requestSnapshot(); len(turns) != maxAttempts {
		t.Errorf("worker turns = %d, want %d — an unmet ruling spends the run's goal try and the task restarts within max_attempts", len(turns), maxAttempts)
	}
}
