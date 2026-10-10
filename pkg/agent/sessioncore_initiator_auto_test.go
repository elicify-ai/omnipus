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
	"io/fs"
	"os"
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

// seedFailedRun puts task id into the state a failed first attempt leaves it in
// (in_progress, bound to a session whose lifecycle record carries the given
// authority) without starting a worker, so the real retry decision and the real
// queue pickup can be driven deterministically.
func seedFailedRun(t *testing.T, al *AgentLoop, id, assignee string, ib *session.InitiatedBy, sb *session.SteeredBy) (*task.Task, string) {
	t.Helper()
	tk := &task.Task{ID: id, AgentID: assignee, WorkspaceID: testWS, Title: id, Status: task.StatusNext}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatalf("create: %v", err)
	}
	sid := "session_seed_" + id
	rec := &session.LifecycleRecord{
		SessionID: sid, Generation: 1, State: session.LifecycleQueued, AgentID: assignee, WorkspaceID: testWS,
		OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindTask, TaskID: id},
		InitiatedBy: ib, SteeredBy: sb,
	}
	if sb != nil {
		rec.OwnerScopeKind, rec.OwnerScopeID = session.OwnerScopeParentSession, sb.SteeringSessionID
	}
	if err := al.GetSessionLifecycleStore().Persist(rec); err != nil {
		t.Fatalf("persist record: %v", err)
	}
	ip, sidp := task.StatusInProgress, sid
	got, err := GetTaskStore(al).Update(id, task.Patch{Status: &ip, SessionID: &sidp})
	if err != nil {
		t.Fatalf("mark in progress: %v", err)
	}
	return got, sid
}

func removeLifecycleFile(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	removed := 0
	root := filepath.Join(omnipusHome(), "session_lifecycle")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(d.Name(), sessionID) {
			if os.Remove(path) == nil {
				removed++
			}
		}
		return nil
	})
	if removed == 0 {
		t.Fatalf("test setup: found no lifecycle file for %s under %s", sessionID, root)
	}
}

var failedRunAuthority = &session.InitiatedBy{
	AgentID: rtCaller, Depth: 1,
	Authorization: session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 1},
}

// F5: the failed run's authority is persisted on the task by the retry decision
// itself, so the queue pickup after a capacity refusal (or any other route)
// runs under it: re-authorized against the CURRENT graph, with the failed run's
// budget.
func TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup(t *testing.T) {
	t.Run("edge removed before pickup: refused", func(t *testing.T) {
		home := seedWorkspaceGraph(t, testWS, true, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		al, _ := newRunTaskPolicyLoopAt(t, home)
		tk, sid := seedFailedRun(t, al, "t-f5-a", rtAssignee, failedRunAuthority, nil)
		if redispatch := al.taskExecutor.consumeTaskAttempt(context.Background(), tk, sid, "boom", nil); redispatch != tk.ID {
			t.Fatalf("retry decision = %q, want a restart of %s", redispatch, tk.ID)
		}
		if got := taskNow(t, al, tk.ID); got.Initiator == nil || got.Initiator.AgentID != rtCaller || !got.Initiator.Direct {
			t.Fatalf("the retry must persist the failed run's authority on the task, got %+v", got.Initiator)
		}
		rewriteWorkspaceGraph(t, home, testWS, true, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
		al.taskExecutor.CheckQueuedTasks(context.Background()) // the ordinary pickup, not the immediate retry
		got := taskNow(t, al, tk.ID)
		if got.Status != task.StatusFailed || !strings.Contains(got.Result, "delegation policy") {
			t.Fatalf("queue pickup of the retry without the edge: status=%q result=%q", got.Status, got.Result)
		}
	})
	t.Run("edge present: pickup runs within the failed run's budget", func(t *testing.T) {
		home := seedWorkspaceGraph(t, testWS, true, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		al, _ := newRunTaskPolicyLoopAt(t, home)
		tk, sid := seedFailedRun(t, al, "t-f5-b", rtAssignee, failedRunAuthority, nil)
		al.taskExecutor.consumeTaskAttempt(context.Background(), tk, sid, "boom", nil)
		al.taskExecutor.CheckQueuedTasks(context.Background())
		got := taskNow(t, al, tk.ID)
		if got.SessionID == "" || got.SessionID == sid {
			t.Fatalf("retry was not picked up into a fresh run: session=%q status=%q result=%q", got.SessionID, got.Status, got.Result)
		}
		rec := lifecycleOf(t, al, got.SessionID)
		if rec.InitiatedBy == nil || rec.InitiatedBy.AgentID != rtCaller ||
			rec.InitiatedBy.Authorization.RemainingDepth > failedRunAuthority.Authorization.RemainingDepth {
			t.Fatalf("retry run authority = %+v, want %s within %d", rec.InitiatedBy, rtCaller, failedRunAuthority.Authorization.RemainingDepth)
		}
	})
	t.Run("a tighter steering budget is the one carried", func(t *testing.T) {
		home := seedWorkspaceGraph(t, testWS, true, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		al, _ := newRunTaskPolicyLoopAt(t, home)
		steered := &session.SteeredBy{
			SteeringSessionID: "session_steerer", RootSessionID: "session_steerer",
			ReportingTarget: session.ReportingTarget{SessionID: "session_steerer", Channel: "webchat", ChatID: "c"},
			Authorization:   session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 0},
		}
		wide := &session.InitiatedBy{AgentID: rtCaller, Depth: 1,
			Authorization: session.Authorization{Mode: session.AuthorizationModeTask, RemainingDepth: 2}}
		tk, sid := seedFailedRun(t, al, "t-f5-c", rtAssignee, wide, steered)
		al.taskExecutor.consumeTaskAttempt(context.Background(), tk, sid, "boom", nil)
		got := taskNow(t, al, tk.ID)
		if got.Initiator == nil || got.Initiator.Inherited == nil || *got.Initiator.Inherited != 1 {
			t.Fatalf("carried budget = %+v, want the steering minimum 0 (+1 hop = 1), not the wider initiating 2", got.Initiator)
		}
		al.taskExecutor.CheckQueuedTasks(context.Background())
		rec := lifecycleOf(t, al, taskNow(t, al, tk.ID).SessionID)
		if rec.InitiatedBy == nil || rec.InitiatedBy.Authorization.RemainingDepth != 0 {
			t.Fatalf("retry run budget = %+v, want RemainingDepth 0", rec.InitiatedBy)
		}
	})
	t.Run("a person-started run retries as a person's start", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, nil)
		tk, sid := seedFailedRun(t, al, "t-f5-d", rtAssignee, nil, nil)
		al.taskExecutor.consumeTaskAttempt(context.Background(), tk, sid, "boom", nil)
		if got := taskNow(t, al, tk.ID); got.Initiator != nil {
			t.Fatalf("a person's run must not gain an initiator, got %+v", got.Initiator)
		}
		al.taskExecutor.CheckQueuedTasks(context.Background())
		if got := taskNow(t, al, tk.ID); got.SessionID == "" || got.SessionID == sid {
			t.Fatalf("person-started retry was not picked up: session=%q result=%q", got.SessionID, got.Result)
		}
	})
}

// N2: if the failed run's authority cannot be read, the retry is refused
// visibly. Unknown authority is never a person's start. New-session storage
// stays healthy, so only the read error can be what stops it.
func TestAutoInitiator_N2_UnreadableRetryAuthorityFailsClosed(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
	tk, sid := seedFailedRun(t, al, "t-n2", rtAssignee, failedRunAuthority, nil)
	removeLifecycleFile(t, al, sid)
	if redispatch := al.taskExecutor.consumeTaskAttempt(context.Background(), tk, sid, "boom", nil); redispatch != "" {
		t.Fatalf("an unreadable authority must not be retried, got redispatch %q", redispatch)
	}
	got := taskNow(t, al, tk.ID)
	if got.Status != task.StatusFailed || !strings.Contains(got.Result, "authority could not be read") {
		t.Fatalf("task must fail visibly: status=%q result=%q", got.Status, got.Result)
	}
	al.taskExecutor.CheckQueuedTasks(context.Background())
	if again := taskNow(t, al, tk.ID); again.SessionID != sid {
		t.Fatalf("no new session may start after the refusal, session=%q", again.SessionID)
	}
}
