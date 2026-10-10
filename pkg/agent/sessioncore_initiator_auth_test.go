// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Initiator-carrying authorization (architect answer 2026-10-10): the cases
// that need the new carrier types (R1 control, R2, R3 control, R4, R6, R7, R8).
// The refusal cases that can run against unfixed code are in
// sessioncore_initiator_auth_behaviour_test.go.
package agent

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func lifecycleOf(t *testing.T, al *AgentLoop, sessionID string) *session.LifecycleRecord {
	t.Helper()
	rec, err := al.GetSessionLifecycleStore().Load(sessionID)
	if err != nil {
		t.Fatalf("load lifecycle %q: %v", sessionID, err)
	}
	return rec
}

func runTaskSession(t *testing.T, caller *AgentInstance, taskID string) string {
	t.Helper()
	isErr, out := runTaskAs(caller, taskID)
	if isErr {
		t.Fatalf("run_task %s refused: %s", taskID, out)
	}
	i := strings.Index(out, `"session_id":"`)
	if i < 0 {
		t.Fatalf("run_task result has no session_id: %s", out)
	}
	rest := out[i+len(`"session_id":"`):]
	return rest[:strings.Index(rest, `"`)]
}

// R1 control: with the self-edge an agent runs its own task, and the run's
// record names the initiating agent.
func TestInitiatorAuth_R1_Control_SelfEdgeRunsAndStampsInitiator(t *testing.T) {
	al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtCaller, nil, nil)})
	tk := createRunnableTask(t, al, "t-ia-r1c", rtCaller)
	rec := lifecycleOf(t, al, runTaskSession(t, caller, tk.ID))
	if rec.InitiatedBy == nil || rec.InitiatedBy.AgentID != rtCaller {
		t.Fatalf("run record InitiatedBy = %+v, want the initiating agent %s", rec.InitiatedBy, rtCaller)
	}
}

// A run a person started carries no initiator.
func TestInitiatorAuth_PersonStartCarriesNoInitiator(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, nil)
	tk := createRunnableTask(t, al, "t-ia-person", rtAssignee)
	sess, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("person start: %v", err)
	}
	if rec := lifecycleOf(t, al, sess); rec.InitiatedBy != nil {
		t.Fatalf("a person's start must carry no initiator, got %+v", rec.InitiatedBy)
	}
}

// R2 (founder ruling F13): creating or reassigning a task to oneself stays
// ungated; the same self-target is gated for a delegate or a run.
func TestInitiatorAuth_R2_TaskCreateToSelfStaysUngated(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)}) // no self-edge
	perf := config.PerformanceConfig{MaxDelegationDepth: 3}
	ctx := tools.WithWorkspaceID(context.Background(), testWS)

	taskTools := buildDelegationDenyCheckerForTaskReassignment(rtCaller, perf, config.DelegationModeTask)
	if d := taskTools(ctx, rtCaller); d != nil {
		t.Fatalf("create_task/update_task to oneself must stay ungated without a self-edge, got %+v", d)
	}
	delegate := buildDelegationDenyCheckerForDelegate(rtCaller, perf, config.DelegationModeTask)
	if d := delegate(ctx, rtCaller); d == nil {
		t.Fatal("a delegate to oneself without a self-edge must be denied")
	}
}

// R3 control: an agent-executed plan records its initiator.
func TestInitiatorAuth_R3_Control_ApprovedPlanRecordsInitiator(t *testing.T) {
	al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
	planStore := plan.New(filepath.Join(omnipusHome(), "plans"))
	al.SetPlanStore(planStore)
	p := &plan.Plan{
		Title: "ia plan", WorkspaceID: testWS, OwnerAgentID: rtCaller, CreatedBy: rtCaller,
		DoD: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "plan done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: rtCaller},
		}},
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if err := GetTaskStore(al).Create(&task.Task{
		ID: "t-ia-r3c", Title: "m", Prompt: "p", Action: task.ActionLLM, AgentID: rtAssignee,
		WorkspaceID: testWS, Status: task.StatusNext, PlanID: p.ID,
		Criteria: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "done", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: rtCaller},
		}},
	}); err != nil {
		t.Fatalf("create member: %v", err)
	}
	if res := caller.Tools.Execute(context.Background(), "execute_plan", map[string]any{"plan_id": p.ID}); res == nil || res.IsError {
		t.Fatalf("execute_plan with the edge refused: %+v", res)
	}
	got, _ := planStore.Get(p.ID)
	if got.InitiatedBy == nil || got.InitiatedBy.AgentID != rtCaller {
		t.Fatalf("Plan.InitiatedBy = %+v, want the executing agent %s", got.InitiatedBy, rtCaller)
	}
}

// R4 (F4): the edge existed at execute_plan and was removed before the member
// dispatched. The member is authorized against the live graph at dispatch: it
// fails visibly, no session, and a plan-judge sees a failed member.
func TestInitiatorAuth_R4_EdgeRemovedBeforeDispatch_MemberFails(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
	al, _ := newRunTaskPolicyLoopAt(t, home)
	member := createRunnableTask(t, al, "t-ia-r4", rtAssignee)
	rewriteWorkspaceGraph(t, home, testWS, true, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
	before := lifecycleCount(t, al)

	err := al.taskExecutor.executeTaskPlanInitiated(context.Background(), member.ID,
		&task.Initiator{AgentID: rtCaller})
	if err == nil || !strings.Contains(err.Error(), "delegation policy") {
		t.Fatalf("dispatch after edge removal = %v; want a delegation-policy refusal", err)
	}
	got, _ := GetTaskStore(al).Get(member.ID)
	if got.Status != task.StatusFailed || got.SessionID != "" || !strings.Contains(got.Result, "delegation policy") {
		t.Fatalf("member must fail visibly with no session: status=%q session=%q result=%q", got.Status, got.SessionID, got.Result)
	}
	if after := lifecycleCount(t, al); after != before {
		t.Fatalf("a refused member minted %d lifecycle record(s)", after-before)
	}
}

// R6 (F5): the A->B edge depth is preserved on B's run and bounds what B starts
// or delegates next. Control: a person's start of the same task does not.
func TestInitiatorAuth_R6_EdgeDepthBoundsTheInitiatedRun(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(rtCaller, rtAssignee, nil, intPtr(1)),
		edge(rtAssignee, rtThird, nil, nil),
	})
	al, caller := newRunTaskPolicyLoopAt(t, home)
	al.GetConfig().Performance.MaxDelegationDepth = 3

	tkB := createRunnableTask(t, al, "t-ia-r6-b", rtAssignee)
	sess := runTaskSession(t, caller, tkB.ID)
	rec := lifecycleOf(t, al, sess)
	if rec.InitiatedBy == nil || rec.InitiatedBy.Authorization.RemainingDepth != 0 || rec.InitiatedBy.Depth != 1 {
		t.Fatalf("B's run InitiatedBy = %+v; want depth 1 and RemainingDepth 0 (A->B depth 1)", rec.InitiatedBy)
	}

	// B delegating onward from that run is out of budget. The launcher reads
	// the steering session's lifecycle record, so give B a chat session whose
	// record carries the run's InitiatedBy (the carrier proved above).
	l := NewSteerLauncher(al)
	delegateFrom := func(initiated *session.InitiatedBy, callID string) error {
		chat := newTestSteeringSessionOwnedBy(t, al, "", rtAssignee)
		if err := al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
			SessionID: chat, Generation: 1, State: session.LifecycleQueued, AgentID: rtAssignee,
			OwnerScopeKind: session.OwnerScopeHuman, InitiatedBy: initiated,
		}); err != nil {
			t.Fatalf("persist steerer record: %v", err)
		}
		_, err := l.Launch(tools.WithWorkspaceID(context.Background(), testWS), steer.LaunchRequest{
			SteeringSessionID: chat, TargetAgentID: rtThird, Task: "onward",
			Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
		})
		return err
	}
	if err := delegateFrom(rec.InitiatedBy, "call-r6"); !errors.Is(err, steer.ErrDepthExceeded) {
		t.Fatalf("B's delegate to C = %v; want ErrDepthExceeded", err)
	}

	// B starting C's task from that run is out of budget too.
	tkC := createRunnableTask(t, al, "t-ia-r6-c", rtThird)
	bInst, _ := al.GetRegistry().GetAgent(rtAssignee)
	res := bInst.Tools.Execute(tools.WithTranscriptSessionID(context.Background(), sess), "run_task",
		map[string]any{"task_id": tkC.ID})
	if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "depth") {
		t.Fatalf("B's run_task of C's task = %+v; want a depth denial", res)
	}
	assertNotStarted(t, al, tkC.ID)

	// Control: a run a person started carries no InitiatedBy, so B is free to
	// delegate onward.
	tkB2 := createRunnableTask(t, al, "t-ia-r6-b2", rtAssignee)
	sess2, serr := al.taskExecutor.StartTaskNow(context.Background(), tkB2.ID)
	if serr != nil {
		t.Fatalf("person start: %v", serr)
	}
	if got := lifecycleOf(t, al, sess2).InitiatedBy; got != nil {
		t.Fatalf("person start carried InitiatedBy %+v", got)
	}
	if err := delegateFrom(nil, "call-r6-ctl"); err != nil {
		t.Fatalf("with no InitiatedBy B must be free to delegate to C, got %v", err)
	}
}

// R7: the launcher no longer authorizes task origin. Nil Initiator and no edge
// is admitted with the global budget (a person's or the scheduler's start); a
// set Initiator is stamped onto the record and caps the child's budget.
func TestInitiatorAuth_R7_LauncherTaskOrigin(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{edge("someone", "else", nil, nil)})
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 5
	l := NewSteerLauncher(al)

	steerer := newCallerSteeringSession(t, al, testWS)
	res, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer, TargetAgentID: testDefaultAgentID, Task: "t",
		Origin: steer.Origin{Kind: steer.OriginKindTask, TaskID: "task-r7a", CallID: "c-r7a"},
	})
	if err != nil {
		t.Fatalf("nil Initiator, no edge: task-origin launch must be admitted, got %v", err)
	}
	if rec := lifecycleOf(t, al, res.SessionID); rec.InitiatedBy != nil {
		t.Fatalf("nil Initiator must leave InitiatedBy nil, got %+v", rec.InitiatedBy)
	}

	steerer2 := newCallerSteeringSession(t, al, testWS)
	want := &session.InitiatedBy{
		AgentID: "starter", SessionID: "s-starter", Depth: 1,
		Authorization: session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 0},
	}
	res2, err := l.Launch(ctxWS(testWS, 0), steer.LaunchRequest{
		SteeringSessionID: steerer2, TargetAgentID: testDefaultAgentID, Task: "t",
		Origin:    steer.Origin{Kind: steer.OriginKindTask, TaskID: "task-r7b", CallID: "c-r7b"},
		Initiator: want,
	})
	if err != nil {
		t.Fatalf("launch with Initiator: %v", err)
	}
	rec := lifecycleOf(t, al, res2.SessionID)
	if !reflect.DeepEqual(rec.InitiatedBy, want) {
		t.Fatalf("stamped InitiatedBy = %+v, want %+v", rec.InitiatedBy, want)
	}
	if rec.SteeredBy == nil || rec.SteeredBy.Authorization.RemainingDepth != 0 {
		t.Fatalf("child budget must be capped by the initiator's RemainingDepth 0, got %+v", rec.SteeredBy)
	}
}

// R8: the carriers survive a store reload.
func TestInitiatorAuth_R8_CarriersSurviveReload(t *testing.T) {
	dir := t.TempDir()
	ls := session.NewLifecycleStore(filepath.Join(dir, "lc"))
	want := &session.InitiatedBy{
		AgentID: "a", SessionID: "s", Depth: 2,
		Authorization: session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 1},
	}
	if err := ls.Persist(&session.LifecycleRecord{
		SessionID: "sess-r8", Generation: 1, State: session.LifecycleQueued, AgentID: "b",
		OwnerScopeKind: session.OwnerScopeHuman, InitiatedBy: want,
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	got, err := session.NewLifecycleStore(filepath.Join(dir, "lc")).Load("sess-r8")
	if err != nil || !reflect.DeepEqual(got.InitiatedBy, want) {
		t.Fatalf("reloaded lifecycle InitiatedBy = %+v (%v), want %+v", got.InitiatedBy, err, want)
	}
	if b, ok := got.OnwardBudget(); !ok || b != 1 {
		t.Fatalf("OnwardBudget = (%d,%v), want (1,true)", b, ok)
	}

	ps := plan.New(filepath.Join(dir, "plans"))
	p := &plan.Plan{Title: "p", WorkspaceID: "ws", OwnerAgentID: "a", CreatedBy: "a"}
	if err := ps.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	ini := &task.Initiator{AgentID: "a", SessionID: "s", Depth: 2}
	if _, err := ps.Update(p.ID, plan.Patch{InitiatedBy: &ini}); err != nil {
		t.Fatalf("set initiator: %v", err)
	}
	reloaded, err := plan.New(filepath.Join(dir, "plans")).Get(p.ID)
	if err != nil || !reflect.DeepEqual(reloaded.InitiatedBy, ini) {
		t.Fatalf("reloaded Plan.InitiatedBy = %+v (%v), want %+v", reloaded.InitiatedBy, err, ini)
	}
	var none *task.Initiator
	if _, err := ps.Update(p.ID, plan.Patch{InitiatedBy: &none}); err != nil {
		t.Fatalf("clear initiator: %v", err)
	}
	cleared, _ := plan.New(filepath.Join(dir, "plans")).Get(p.ID)
	if cleared.InitiatedBy != nil {
		t.Fatalf("a person's approval must clear the initiator, got %+v", cleared.InitiatedBy)
	}
}
