// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_claim_overturn_resumes_b6_test.go is the deterministic cover for UAT
// eval B-6, "Overturned verdict resumes work"
// (docs/internal/uat/uat-plan-goal-plan-task-2026-09-13.md §5). The live eval
// could not be scored: in five attempts a real model never made a false `met`
// claim, so the Judge never had anything to overturn. The mechanism the eval
// exists to exercise is engine behaviour, not model behaviour, and it is
// pinned here with scripted providers on BOTH owner kinds:
//
//   - Chat-owned goal, tool channel (ADR-084 D12/D13): the worker calls the
//     REAL goal_claim tool with status met; the deferred adjudication runs; the
//     Judge overturns it; the goal stays active with one round consumed; the
//     worker is sent a steer that carries the Judge's reason; and the steered
//     round's own re-claim is adjudicated again — the goal ends only on a real
//     met verdict.
//   - Task-owned goal, the real executor (ExecuteTask): the worker ends its
//     turn with the completion marker the task prompt teaches; the Judge
//     overturns it; the task is re-dispatched rather than ended; and the
//     worker's NEXT attempt prompt carries the Judge's reason — asserted on the
//     request the worker's provider actually received, not on an intermediate
//     field — before a met verdict completes the task.
//
// Both oracles come from the specification, not from the implementation: the
// Judge's reason is a distinctive sentence this file invents, so it can only
// appear in what the worker receives if the engine carried it there.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	b6GoalText       = "write report.md summarising the three supplier quotes"
	b6JudgeReason    = "report.md exists but lists only two supplier quotes, not the three the goal requires"
	b6FirstEvidence  = "wrote report.md with the supplier quotes"
	b6SecondEvidence = "added the third supplier quote to report.md and re-read the file"
)

// b6ScriptedJudge overturns every claim before call metFromCall and upholds
// every claim from it onward. It answers each criterion the verifier prompt
// actually asks about, echoing the prompt's own ids, so the fixture never
// depends on ids minted inside the stores under test. It records the criterion
// texts each call was asked about, so a test can prove the fixture judged real
// criteria rather than an empty block (an empty block yields a different,
// "unjudgeable" unmet cause and would let an overturn assertion pass for the
// wrong reason).
type b6ScriptedJudge struct {
	mu          sync.Mutex
	calls       int
	askedTexts  [][]string
	metFromCall int
	reason      string
}

func (j *b6ScriptedJudge) Chat(
	_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	var user string
	for _, m := range messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	asked := parseJudgeCriteriaBlock(user)

	j.mu.Lock()
	j.calls++
	met := j.calls >= j.metFromCall
	texts := make([]string, 0, len(asked))
	for _, c := range asked {
		texts = append(texts, c.Text)
	}
	j.askedTexts = append(j.askedTexts, texts)
	reason := j.reason
	j.mu.Unlock()

	items := make([]string, 0, len(asked))
	for _, c := range asked {
		r := "the claim is supported by the recorded work"
		if !met {
			r = reason
		}
		items = append(items, fmt.Sprintf(`{"id":%q,"met":%t,"reason":%q}`, c.ID, met, r))
	}
	return &providers.LLMResponse{
		Content: fmt.Sprintf(`{"met": %t, "criteria": [%s]}`, met, strings.Join(items, ",")),
	}, nil
}

func (j *b6ScriptedJudge) GetDefaultModel() string { return "fake-judge-model" }

func (j *b6ScriptedJudge) callCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.calls
}

func (j *b6ScriptedJudge) askedOnCall(n int) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if n < 1 || n > len(j.askedTexts) {
		return nil
	}
	return j.askedTexts[n-1]
}

// b6ClaimMetAndPersist executes the REAL goal_claim tool registered on the
// working agent, then persists the call exactly the way loop.go's tool-call
// record stores a successful plain-text tool result (Result["text"] carrying
// the tool's own output) — the shape resolveToolClaim reads back.
func b6ClaimMetAndPersist(
	t *testing.T, claimTool tools.Tool, store *session.UnifiedStore, sid, callID, evidence string,
) {
	t.Helper()
	ctx := tools.WithTranscriptSessionID(context.Background(), sid)
	res := claimTool.Execute(ctx, map[string]any{"status": tools.GoalClaimStatusMet, "evidence": evidence})
	if res == nil {
		t.Fatal("goal_claim returned a nil result")
	}
	if res.IsError {
		t.Fatalf("goal_claim(met) was refused on a chat session that carries an active goal: %q", res.ForLLM)
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

// TestB6_ChatGoal_OverturnedGoalClaimMet_SteersWorkerAndNextClaimIsJudged
//
// Given a chat-owned goal with an unmet criterion
// When the worker calls goal_claim(status: met) and the Judge overturns it
// Then the goal stays active with exactly one round consumed, the worker is
// sent one steer carrying the Judge's reason, addressed to the same session
// and agent and stamped as a goal-loop follow-up (so the origin gate admits
// the steered turn)
// And when the steered round re-claims, that claim is adjudicated again and
// the goal ends met on round 2, with no further steer.
func TestB6_ChatGoal_OverturnedGoalClaimMet_SteersWorkerAndNextClaimIsJudged(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	gid := armGoalRecord(t, sid, b6GoalText, recordedGoalCriteria(b6GoalText), 0, time.Now())
	al.recordGoalRouting(sid, gid, "webchat", "c1", "sk1", agentInst.ID)

	judge := &b6ScriptedJudge{metFromCall: 2, reason: b6JudgeReason}
	judgeInst.Provider = judge
	dispatches := recordGoalDispatches(al)

	claimTool, ok := agentInst.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("goal_claim is not registered on the working agent — the claim channel under test does not exist")
	}

	// --- Round 1: a false `met` claim, made through the tool.
	b6ClaimMetAndPersist(t, claimTool, store, sid, "b6-claim-1", b6FirstEvidence)
	userOpts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	turn1 := &turnResult{finalContent: "I wrote report.md."}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, userOpts, turn1)
	if turn1.goalDeferredAdjudication == nil {
		t.Fatal("a goal_claim(met) call must record deferred adjudication work")
	}
	if got := turn1.goalDeferredAdjudication.claimText; got != b6FirstEvidence {
		t.Fatalf("claim text = %q, want the tool call's own evidence %q (JUDGE-FR-094)", got, b6FirstEvidence)
	}
	al.dispatchDeferredGoalAdjudication(turn1.goalDeferredAdjudication)

	if n := judge.callCount(); n != 1 {
		t.Fatalf("Judge calls after the first claim = %d, want 1", n)
	}
	if asked := judge.askedOnCall(1); len(asked) == 0 {
		t.Fatal("fixture broken: the Judge was asked about no criteria, so the overturn below would be for the wrong reason")
	}

	// The overturn must not end the goal.
	live := activeGoalForSession(sid)
	if live == nil || live.GoalID != gid {
		t.Fatalf("the goal is no longer active after an OVERTURNED claim (got %+v) — an unmet verdict under "+
			"the round bound must resume work, not end the goal", live)
	}
	if live.Round != 1 {
		t.Errorf("rounds used after one overturned claim = %d, want 1", live.Round)
	}
	if !strings.Contains(live.LatestReason, b6JudgeReason) {
		t.Errorf("latest reason on the goal = %q, want it to carry the Judge's reason", live.LatestReason)
	}

	// The worker must be told why, exactly once, in the same session.
	evts := dispatches.all()
	if len(evts) != 1 {
		t.Fatalf("steers dispatched after the overturn = %d, want exactly 1 (contents: %q)", len(evts), dispatches.contents())
	}
	steer := evts[0]
	if !strings.Contains(steer.Content, b6JudgeReason) {
		t.Errorf("steer = %q, want it to carry the Judge's reason — without it the worker re-claims blind", steer.Content)
	}
	if !strings.Contains(steer.Content, b6GoalText) {
		t.Errorf("steer = %q, want it to restate the goal it is steering toward", steer.Content)
	}
	if !strings.Contains(steer.Content, "UNMET") {
		t.Errorf("steer = %q, want it to say the Judge found the attempt UNMET", steer.Content)
	}
	if steer.SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Errorf("steer sender = %q, want %q — otherwise the steered turn is dropped by the goal hook's origin gate",
			steer.SenderCanonicalID, goalLoopFollowUpSenderID)
	}
	if steer.TranscriptSessionID != sid {
		t.Errorf("steer session = %q, want %q (the goal's own session)", steer.TranscriptSessionID, sid)
	}
	if steer.AgentID != agentInst.ID {
		t.Errorf("steer agent = %q, want %q (the agent working the goal)", steer.AgentID, agentInst.ID)
	}

	// --- Round 2: the steered turn does the work and claims again.
	time.Sleep(5 * time.Millisecond) // the claim scan reads entries strictly after round 1's watermark
	b6ClaimMetAndPersist(t, claimTool, store, sid, "b6-claim-2", b6SecondEvidence)
	steeredOpts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", SenderID: goalLoopFollowUpSenderID,
	}
	turn2 := &turnResult{finalContent: "Added the third quote."}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, steeredOpts, turn2)
	if turn2.goalDeferredAdjudication == nil {
		t.Fatal("the steered round's re-claim was not recorded — after an overturn the goal must accept a new claim")
	}
	if got := turn2.goalDeferredAdjudication.claimText; got != b6SecondEvidence {
		t.Fatalf("round-2 claim text = %q, want the NEW evidence %q, not round 1's", got, b6SecondEvidence)
	}
	al.dispatchDeferredGoalAdjudication(turn2.goalDeferredAdjudication)

	if n := judge.callCount(); n != 2 {
		t.Fatalf("Judge calls after the re-claim = %d, want 2", n)
	}
	final := mustGoalRecord(t, gid)
	if final.State != generated.GoalStateMet {
		t.Fatalf("goal state after the upheld re-claim = %q, want %q", final.State, generated.GoalStateMet)
	}
	if final.Round != 2 {
		t.Errorf("rounds used at the end = %d, want 2 (one overturned, one upheld)", final.Round)
	}
	if n := len(dispatches.all()); n != 1 {
		t.Errorf("steers dispatched in total = %d, want 1 — an upheld claim must not steer", n)
	}
}

// TestB6_TaskGoal_OverturnedClaim_SameRunCarriesJudgeFeedback
//
// Given a task with an acceptance criterion and its paired goal record
// When the worker claims completion through goal_claim and the Judge overturns
// that claim
// Then the task keeps working in the SAME run and session — an overturned claim
// spends a goal try, not a task attempt (founder decision 2026-09-14, issue
// #710) — and a second worker turn actually runs
// And the second turn's request, as received by the worker's provider, carries
// the Judge's reason, while the first turn's did not
// And when the Judge upholds the second claim the task completes done and the
// goal ends met on its second try, never having been ended in between.
func TestB6_TaskGoal_OverturnedClaim_SameRunCarriesJudgeFeedback(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md and re-read it"), turnClaimMet(b6SecondEvidence))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &b6ScriptedJudge{metFromCall: 2, reason: b6JudgeReason}
	judgeInst.Provider = judge

	const taskID = "task-b6-overturn"
	const criterionText = "report.md lists all three supplier quotes"
	maxAttempts := 3
	tk := &task.Task{
		ID: taskID, Title: "supplier quote report", Prompt: "Write report.md summarising the three supplier quotes.",
		Action: task.ActionLLM, AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
		Status: task.StatusNext, MaxAttempts: &maxAttempts,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", criterionText)},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	// The defining-phase goal record task creation authors (GOAL-FR-012):
	// ExecuteTask activates it against the session the run mints.
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.GoalSourceTaskExplicit, tk.Prompt, "",
		[]task.AcceptanceCriterion{proseCriterion("", criterionText)}, newFloorDoD(),
		config.DefaultGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("goal.New: %v", err)
	}
	if cerr := goal.NewStore(config.OmnipusHomeDir()).Create(g); cerr != nil {
		t.Fatalf("create paired goal record: %v", cerr)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), taskID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := t3WaitForTerminal(t, al, taskID, 2)

	if final.Status != task.StatusDone {
		t.Fatalf("task status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("task attempts used = %d, want 0 (the overturned claim spends a goal try inside the run)", final.AttemptCount)
	}
	if n := judge.callCount(); n != 2 {
		t.Errorf("Judge calls = %d, want 2 (one overturn, one upheld)", n)
	}
	if asked := judge.askedOnCall(1); len(asked) == 0 {
		t.Fatal("fixture broken: the Judge was asked about no criteria on the first claim")
	}

	reqs := worker.requestSnapshot()
	if len(reqs) != 2 {
		t.Fatalf("worker turns = %d, want 2 — an overturned claim must be followed by another turn", len(reqs))
	}
	if strings.Contains(reqs[0], b6JudgeReason) {
		t.Fatal("fixture broken: the first turn's request already contained the Judge's reason before any verdict existed")
	}
	if !strings.Contains(reqs[1], b6JudgeReason) {
		t.Errorf("the second turn's request does not carry the Judge's reason — the worker was sent back blind "+
			"and can only repeat the claim that was just overturned.\nsecond request:\n%s", reqs[1])
	}
	runs, rerr := al.taskStore.ListRuns(taskID)
	if rerr != nil {
		t.Fatalf("ListRuns: %v", rerr)
	}
	if len(runs) != 1 || runs[0].SessionID != final.SessionID {
		t.Errorf("runs = %+v, final session = %q — both tries must happen in the run's one session", runs, final.SessionID)
	}

	// completeTaskWithResult archives the task session (the signal
	// t3WaitForTerminal returns on) BEFORE it ends the paired goal record, so
	// the goal's terminal state is read with a bounded wait.
	rec := readTaskGoal(t, taskID)
	for deadline := time.Now().Add(10 * time.Second); rec.State == generated.GoalStateActive && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		rec = readTaskGoal(t, taskID)
	}
	if rec.State != generated.GoalStateMet {
		t.Errorf("paired goal state = %q, want %q", rec.State, generated.GoalStateMet)
	}
	if rec.Round != 2 {
		t.Errorf("paired goal tries used = %d, want 2 (one overturned, one upheld)", rec.Round)
	}
	if len(rec.TerminalHistory) != 0 {
		t.Errorf("the paired goal carries %d terminal-history entr(ies) — it was ENDED by the overturn and "+
			"re-activated, instead of staying active within the run: %+v", len(rec.TerminalHistory), rec.TerminalHistory)
	}
}
