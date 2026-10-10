// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Security review round 3 (F4, F5, N1): the AUTOMATIC start routes (queue drain,
// dependency unblock, retry) must run under the stored authority of the work
// item, re-authorized against the CURRENT delegation graph, and carry its
// budget. Founder ruling F13: an automatic run of a task whose stored initiator
// is the assignee itself needs no self-edge; any other initiator does.
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedAgentPlan creates a plan that agent initiator executed (approved, with
// Plan.InitiatedBy) holding one next member assigned to assignee.
func seedAgentPlan(t *testing.T, al *AgentLoop, initiator, assignee, memberID string, blockedBy ...string) (*plan.Plan, *task.Task) {
	t.Helper()
	planStore := plan.New(filepath.Join(omnipusHome(), "plans"))
	al.SetPlanStore(planStore)
	al.taskExecutor.SetPlanStore(planStore) // the gateway wires the executor's store at boot
	p := &plan.Plan{
		Title: "auto plan", WorkspaceID: testWS, OwnerAgentID: initiator, CreatedBy: initiator,
		DoD: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "plan done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: initiator},
		}},
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	m := &task.Task{
		ID: memberID, Title: "member", Prompt: "do it", Action: task.ActionLLM, AgentID: assignee,
		WorkspaceID: testWS, Status: task.StatusNext, PlanID: p.ID, BlockedBy: blockedBy,
		Criteria: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "done", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: initiator},
		}},
	}
	if err := GetTaskStore(al).Create(m); err != nil {
		t.Fatalf("create member: %v", err)
	}
	approved := plan.StateApproved
	ini := &task.Initiator{AgentID: initiator}
	if _, err := planStore.Update(p.ID, plan.Patch{State: &approved, InitiatedBy: &ini}); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	return p, m
}

func taskNow(t *testing.T, al *AgentLoop, id string) *task.Task {
	t.Helper()
	got, err := GetTaskStore(al).Get(id)
	if err != nil {
		t.Fatalf("get task %s: %v", id, err)
	}
	return got
}

func assertRefusedNoSession(t *testing.T, al *AgentLoop, id, wantInReason string) {
	t.Helper()
	got := taskNow(t, al, id)
	if got.Status != task.StatusFailed || got.SessionID != "" || !strings.Contains(got.Result, wantInReason) {
		t.Fatalf("task %s must be refused with no session mentioning %q: status=%q session=%q result=%q",
			id, wantInReason, got.Status, got.SessionID, got.Result)
	}
}

// F4: the queue drain starts a plan member under the plan's initiating agent,
// against the live graph.
func TestAutoInitiator_F4_QueueDrain_PlanMember(t *testing.T) {
	t.Run("edge removed after execute_plan: refused", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)}) // no caller->assignee edge
		_, m := seedAgentPlan(t, al, rtCaller, rtAssignee, "t-auto-q1")
		al.taskExecutor.CheckQueuedTasks(context.Background())
		assertRefusedNoSession(t, al, m.ID, "delegation policy")
	})
	t.Run("edge present: dispatched and carries the initiator", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		_, m := seedAgentPlan(t, al, rtCaller, rtAssignee, "t-auto-q2")
		al.taskExecutor.CheckQueuedTasks(context.Background())
		got := taskNow(t, al, m.ID)
		if got.SessionID == "" {
			t.Fatalf("member was not dispatched: status=%q result=%q", got.Status, got.Result)
		}
		if rec := lifecycleOf(t, al, got.SessionID); rec.InitiatedBy == nil || rec.InitiatedBy.AgentID != rtCaller {
			t.Fatalf("member run InitiatedBy = %+v, want %s", rec.InitiatedBy, rtCaller)
		}
	})
	t.Run("person-approved plan: dispatched with no edge", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
		p, m := seedAgentPlan(t, al, rtCaller, rtAssignee, "t-auto-q3")
		var person *task.Initiator
		if _, err := al.GetPlanStore().Update(p.ID, plan.Patch{InitiatedBy: &person}); err != nil {
			t.Fatalf("clear initiator: %v", err)
		}
		al.taskExecutor.CheckQueuedTasks(context.Background())
		if got := taskNow(t, al, m.ID); got.SessionID == "" {
			t.Fatalf("a person-approved plan's member must dispatch: status=%q result=%q", got.Status, got.Result)
		}
	})
}

// F4: dependency unblock dispatches the next member under the same authority.
func TestAutoInitiator_F4_DependencyUnblock_PlanMember(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
	_, first := seedAgentPlan(t, al, rtCaller, rtAssignee, "t-auto-d1")
	second := &task.Task{
		ID: "t-auto-d2", Title: "second", Prompt: "p", Action: task.ActionLLM, AgentID: rtAssignee,
		WorkspaceID: testWS, Status: task.StatusBlocked, PlanID: first.PlanID, BlockedBy: []string{first.ID},
		Criteria: first.Criteria,
	}
	if err := GetTaskStore(al).Create(second); err != nil {
		t.Fatalf("create second: %v", err)
	}
	done := task.StatusDone
	if _, err := GetTaskStore(al).Update(first.ID, task.Patch{Status: &done}); err != nil {
		t.Fatalf("complete first: %v", err)
	}
	al.taskExecutor.advanceBlockedTasks(context.Background(), first.ID)
	if got := taskNow(t, al, second.ID); got.SessionID != "" {
		t.Fatalf("the unblocked member started without the initiating agent's edge: session=%q", got.SessionID)
	}
}

// F13 line, standalone tasks: a stored initiator other than the assignee needs a
// current edge; the assignee itself does not.
func TestAutoInitiator_F13_StoredInitiatorLine(t *testing.T) {
	newTask := func(al *AgentLoop, id, assignee string, ini *task.Initiator) *task.Task {
		tk := &task.Task{
			ID: id, AgentID: assignee, WorkspaceID: testWS, Title: id, Status: task.StatusNext, Initiator: ini,
		}
		if err := GetTaskStore(al).Create(tk); err != nil {
			t.Fatalf("create: %v", err)
		}
		return tk
	}
	t.Run("another agent's initiator, edge removed: refused", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
		tk := newTask(al, "t-auto-s1", rtAssignee, &task.Initiator{AgentID: rtCaller})
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err == nil {
			t.Fatal("queue run under an initiator without an edge must be refused")
		}
		assertRefusedNoSession(t, al, tk.ID, "delegation policy")
	})
	t.Run("another agent's initiator, edge present: runs", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		tk := newTask(al, "t-auto-s2", rtAssignee, &task.Initiator{AgentID: rtCaller})
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
			t.Fatalf("queue run with the edge refused: %v", err)
		}
	})
	t.Run("the assignee itself, no self-edge: the queue may run it", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
		tk := newTask(al, "t-auto-s3", rtCaller, &task.Initiator{AgentID: rtCaller})
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
			t.Fatalf("queue run of an agent's own task without a self-edge must be allowed (F13): %v", err)
		}
	})
	t.Run("no stored initiator: a person's or scheduler's task runs", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, nil)
		tk := newTask(al, "t-auto-s4", rtAssignee, nil)
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
			t.Fatalf("a task with no stored initiator must run: %v", err)
		}
	})
	t.Run("direct run_task of one's own task still needs the self-edge", func(t *testing.T) {
		al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
		tk := newTask(al, "t-auto-s5", rtCaller, &task.Initiator{AgentID: rtCaller})
		isErr, out := runTaskAs(caller, tk.ID)
		if !isErr || !strings.Contains(out, "trust_set") {
			t.Fatalf("direct run_task of an own task without a self-edge = (err=%v) %q; want a denial", isErr, out)
		}
	})
}

// N1: the budget the creating agent had inherited is kept on the task and bounds
// the queued run.
func TestAutoInitiator_N1_StoredBudgetBoundsTheQueuedRun(t *testing.T) {
	zero, two := 0, 2
	t.Run("creator had no budget left: refused for depth", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		tk := &task.Task{ID: "t-auto-n1", AgentID: rtAssignee, WorkspaceID: testWS, Title: "n1", Status: task.StatusNext,
			Initiator: &task.Initiator{AgentID: rtCaller, Depth: 1, Inherited: &zero}}
		if err := GetTaskStore(al).Create(tk); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err == nil {
			t.Fatal("a queued run must not exceed the creator's inherited budget")
		}
		assertRefusedNoSession(t, al, tk.ID, "budget")
	})
	t.Run("creator had budget: the run carries what is left", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		tk := &task.Task{ID: "t-auto-n1b", AgentID: rtAssignee, WorkspaceID: testWS, Title: "n1b", Status: task.StatusNext,
			Initiator: &task.Initiator{AgentID: rtCaller, Depth: 1, Inherited: &two}}
		if err := GetTaskStore(al).Create(tk); err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
			t.Fatalf("queue run within budget refused: %v", err)
		}
		rec := lifecycleOf(t, al, taskNow(t, al, tk.ID).SessionID)
		if rec.InitiatedBy == nil || rec.InitiatedBy.Authorization.RemainingDepth > 1 {
			t.Fatalf("run budget = %+v, want RemainingDepth <= 1 (inherited 2, one hop)", rec.InitiatedBy)
		}
	})
}

// F5: an automatic retry keeps the failed run's authority; it is re-authorized
// against the current graph and cannot exceed the first run's budget.
func TestAutoInitiator_F5_Retry(t *testing.T) {
	prev := &session.InitiatedBy{
		AgentID: rtCaller, SessionID: "", Depth: 1,
		Authorization: session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 1},
	}
	mk := func(al *AgentLoop, id string) *task.Task {
		tk := &task.Task{ID: id, AgentID: rtAssignee, WorkspaceID: testWS, Title: id, Status: task.StatusNext}
		if err := GetTaskStore(al).Create(tk); err != nil {
			t.Fatalf("create: %v", err)
		}
		return tk
	}
	t.Run("edge removed after the first run: the retry is refused", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
		tk := mk(al, "t-auto-r1")
		if err := al.taskExecutor.reExecuteTask(context.Background(), tk.ID, nil, prev); err == nil {
			t.Fatal("a retry of an agent-initiated run without the edge must be refused")
		}
		assertRefusedNoSession(t, al, tk.ID, "delegation policy")
	})
	t.Run("edge present: the retry runs and does not exceed the first budget", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		tk := mk(al, "t-auto-r2")
		if err := al.taskExecutor.reExecuteTask(context.Background(), tk.ID, nil, prev); err != nil {
			t.Fatalf("retry with the edge refused: %v", err)
		}
		rec := lifecycleOf(t, al, taskNow(t, al, tk.ID).SessionID)
		if rec.InitiatedBy == nil || rec.InitiatedBy.Authorization.RemainingDepth > prev.Authorization.RemainingDepth {
			t.Fatalf("retry budget = %+v, want <= %d", rec.InitiatedBy, prev.Authorization.RemainingDepth)
		}
	})
	t.Run("a person's start retries as a person's start", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, nil)
		tk := mk(al, "t-auto-r3")
		if err := al.taskExecutor.reExecuteTask(context.Background(), tk.ID, nil, nil); err != nil {
			t.Fatalf("a person-started run's retry must not need an edge: %v", err)
		}
	})
}
