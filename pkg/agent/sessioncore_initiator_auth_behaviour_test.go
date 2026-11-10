// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Initiator-carrying authorization (architect answer 2026-10-10, R1/R3/R5).
// This file uses only APIs that exist before the change, so its refusal cases
// fail against the unfixed code; the cases that need the new carrier types are
// in sessioncore_initiator_auth_test.go.
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

func lifecycleCount(t *testing.T, al *AgentLoop) int {
	t.Helper()
	recs, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("list lifecycle records: %v", err)
	}
	return len(recs)
}

// R1 (F1-self): the agent's own standalone task, with the self-edge removed,
// is refused whatever the task's stored origin: empty, or an active chat.
func TestInitiatorAuth_R1_OwnTaskWithoutSelfEdge_Refused(t *testing.T) {
	for _, origin := range []string{"empty origin", "active chat origin"} {
		t.Run(origin, func(t *testing.T) {
			al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
			tk := &task.Task{
				ID: "t-ia-r1", AgentID: rtCaller, WorkspaceID: testWS, Title: "own", Status: task.StatusNext,
			}
			if origin == "active chat origin" {
				tk.OriginSessionID = newTestSteeringSessionOwnedBy(t, al, "", rtCaller)
			}
			if err := GetTaskStore(al).Create(tk); err != nil {
				t.Fatalf("create task: %v", err)
			}
			before := lifecycleCount(t, al)

			isErr, out := runTaskAs(caller, tk.ID)
			if !isErr || !strings.Contains(out, "trust_set") {
				t.Fatalf("run_task = (err=%v) %q; want a trust_set denial", isErr, out)
			}
			assertNotStarted(t, al, tk.ID)
			if got := lifecycleCount(t, al); got != before {
				t.Fatalf("a refused run minted %d lifecycle record(s)", got-before)
			}
		})
	}
}

// R3 (F4): a person-authored draft plan whose member is assigned to B, with no
// caller->B edge, is not started by A's execute_plan; it stays a draft and B
// gets no session. With the edge it is approved.
func TestInitiatorAuth_R3_ExecutePlanNeedsAssigneeEdge(t *testing.T) {
	seed := func(t *testing.T, edges []graphEdge) (*AgentLoop, *AgentInstance, *plan.Plan, *task.Task) {
		al, caller := newRunTaskPolicyLoop(t, edges)
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
		member := &task.Task{
			ID: "t-ia-member", Title: "member", Prompt: "do it", Action: task.ActionLLM,
			AgentID: rtAssignee, WorkspaceID: testWS, Status: task.StatusNext, PlanID: p.ID,
			Criteria: []task.AcceptanceCriterion{{
				Kind: task.KindProse, Text: "done",
				Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: rtCaller},
			}},
		}
		if err := GetTaskStore(al).Create(member); err != nil {
			t.Fatalf("create member: %v", err)
		}
		return al, caller, p, member
	}

	t.Run("no edge refused, plan stays draft", func(t *testing.T) {
		al, caller, p, member := seed(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
		res := caller.Tools.Execute(context.Background(), "execute_plan", map[string]any{"plan_id": p.ID})
		if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "trust_set") || !strings.Contains(res.ForLLM, rtAssignee) {
			t.Fatalf("execute_plan without an edge = %+v; want a trust_set denial naming %s", res, rtAssignee)
		}
		got, err := al.GetPlanStore().Get(p.ID)
		if err != nil || got.State != plan.StateDraft {
			t.Fatalf("plan must stay a draft, got %v / %v", got, err)
		}
		m, _ := GetTaskStore(al).Get(member.ID)
		if m.SessionID != "" || m.Status != task.StatusNext {
			t.Fatalf("refused execute_plan touched the member: status=%q session=%q", m.Status, m.SessionID)
		}
	})
	t.Run("edge present approves", func(t *testing.T) {
		al, caller, p, _ := seed(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
		res := caller.Tools.Execute(context.Background(), "execute_plan", map[string]any{"plan_id": p.ID})
		if res == nil || res.IsError {
			t.Fatalf("execute_plan with the edge refused: %+v", res)
		}
		got, _ := al.GetPlanStore().Get(p.ID)
		if got.State != plan.StateApproved {
			t.Fatalf("state = %q, want approved", got.State)
		}
	})
}

// R5 (F4 control): a plan a person approved carries no initiator, so its member
// dispatches with no agent edge.
func TestInitiatorAuth_R5_PersonApprovedPlanDispatchesWithoutEdge(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
	member := createRunnableTask(t, al, "t-ia-r5", rtAssignee)
	if err := al.taskExecutor.executeTaskPlanVerified(context.Background(), member.ID); err != nil {
		t.Fatalf("a person-approved plan's member must dispatch without an edge: %v", err)
	}
	got, _ := GetTaskStore(al).Get(member.ID)
	if got.SessionID == "" {
		t.Fatal("member was not dispatched (no session)")
	}
}
