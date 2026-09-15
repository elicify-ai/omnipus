// Omnipus — Plan/Task membership gate tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_plan_membership_test.go — the draft-only plan-membership rule
// (operator directive: "adding a task to a running plan must not be possible
// in neither for the agent nor via the ui").
//
// tools.ValidateTaskPlanMembership is the ONE gate every direct-attach path
// pays: this tool (create_task), the System Agent's create_task_in_workspace,
// and the REST surface's POST /tasks and PATCH /tasks/{id}. These tests drive
// the exported gate directly across the whole state matrix, and then drive it
// once more THROUGH create_task so the wiring is proven and not just the
// predicate.
package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedPlanInState persists a plan in ws-1 and walks it to `state` through the
// real lifecycle (the store's transition matrix refuses illegal jumps, so a
// state reached this way is one the product can actually produce).
func seedPlanInState(t *testing.T, planStore *plan.Store, state plan.State) *plan.Plan {
	t.Helper()
	p := &plan.Plan{Title: "P", WorkspaceID: "ws-1", OwnerAgentID: "jim", CreatedBy: "jim"}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	for _, step := range lifecycleStepsTo(state) {
		s := step
		if _, err := planStore.Update(p.ID, plan.Patch{State: &s}); err != nil {
			t.Fatalf("drive plan to %s (step %s): %v", state, s, err)
		}
	}
	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("re-read seeded plan: %v", err)
	}
	if got.State != state {
		t.Fatalf("seeded plan state = %q, want %q", got.State, state)
	}
	return got
}

// lifecycleStepsTo returns the legal transition sequence from draft to state.
func lifecycleStepsTo(state plan.State) []plan.State {
	switch state {
	case plan.StateDraft:
		return nil
	case plan.StateApproved:
		return []plan.State{plan.StateApproved}
	case plan.StateRunning:
		return []plan.State{plan.StateApproved, plan.StateRunning}
	case plan.StateDone:
		return []plan.State{plan.StateApproved, plan.StateRunning, plan.StateDone}
	case plan.StateFailed:
		return []plan.State{plan.StateApproved, plan.StateRunning, plan.StateFailed}
	}
	return nil
}

// TestValidateTaskPlanMembership_StateMatrix is the whole rule in one table:
// DRAFT is the only state that accepts a new member. approved and running are
// refused because plan.Lint runs once, at approve, and never again — a member
// attached afterwards is never checked for write-set overlap or join-point
// obligations against the members already there. done and failed are refused
// as before, with their own distinct wording.
func TestValidateTaskPlanMembership_StateMatrix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state       plan.State
		wantErr     bool
		wantFragmnt string
	}{
		{plan.StateDraft, false, ""},
		{plan.StateApproved, true, "membership is frozen"},
		{plan.StateRunning, true, "membership is frozen"},
		{plan.StateDone, true, "(terminal)"},
		{plan.StateFailed, true, "(terminal)"},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			t.Parallel()
			planStore, _ := newPlanAndTaskStores(t)
			p := seedPlanInState(t, planStore, tc.state)

			err := ValidateTaskPlanMembership(planStore, p.ID, "ws-1")

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("state %s: want accept, got %v", tc.state, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("state %s: want refusal, got nil", tc.state)
			}
			if !strings.Contains(err.Error(), tc.wantFragmnt) {
				t.Errorf("state %s: error %q must contain %q — a locked plan and a dead plan are "+
					"different operator problems", tc.state, err, tc.wantFragmnt)
			}
			if !strings.Contains(err.Error(), string(tc.state)) {
				t.Errorf("state %s: error %q must name the plan's state", tc.state, err)
			}
			if !strings.Contains(err.Error(), p.ID) {
				t.Errorf("state %s: error %q must name the plan", tc.state, err)
			}
		})
	}
}

// TestValidateTaskPlanMembership_CrossWorkspaceStaysDistinct proves the two
// preconditions did not collapse into one: a DRAFT plan in another workspace
// is still refused, and still with the workspace diagnosis.
func TestValidateTaskPlanMembership_CrossWorkspaceStaysDistinct(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	p := seedPlanInState(t, planStore, plan.StateDraft)

	err := ValidateTaskPlanMembership(planStore, p.ID, "ws-other")
	if err == nil {
		t.Fatal("cross-workspace plan_id must be refused")
	}
	if !strings.Contains(err.Error(), "different workspace") {
		t.Errorf("error %q must diagnose the workspace mismatch", err)
	}
	if strings.Contains(err.Error(), "membership is frozen") {
		t.Errorf("a draft plan in the wrong workspace is a workspace problem, not a lifecycle one: %q", err)
	}
}

// TestValidateTaskPlanMembership_EmptyPlanIDIsANoOp proves a create/patch that
// never mentions a plan is entirely unaffected — including when the store is
// unwired.
func TestValidateTaskPlanMembership_EmptyPlanIDIsANoOp(t *testing.T) {
	t.Parallel()
	if err := ValidateTaskPlanMembership(nil, "", "ws-1"); err != nil {
		t.Fatalf("empty plan_id with an unwired store must be a no-op, got %v", err)
	}
}

// TestTaskCreate_PlanMembership_RunningPlanRefused drives the gate THROUGH the
// create_task tool, so the wiring is covered and not just the predicate: an
// agent handed a running plan's id must be refused, and no task may be left
// behind.
func TestTaskCreate_PlanMembership_RunningPlanRefused(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanInState(t, planStore, plan.StateRunning)

	tool := NewTaskCreateTool(taskStore)
	tool.SetPlanStore(planStore)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title": "smuggled member", "prompt": "do it", "agent_id": "worker",
		"plan_id": p.ID, "write_set": []any{"src/app.ts"},
		"criteria": validCriteriaArg(), "dod": validDoDArg(),
	})

	if !res.IsError {
		t.Fatal("create_task naming a RUNNING plan must be refused")
	}
	if !strings.Contains(res.ForLLM, "membership is frozen") {
		t.Errorf("refusal %q must come from the membership gate, not an earlier gate", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, string(plan.StateRunning)) {
		t.Errorf("refusal %q must name the plan's state", res.ForLLM)
	}

	all, err := taskStore.List(task.Filter{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("a refused attach must persist no task; got %d", len(all))
	}
}

// TestTaskCreate_PlanMembership_ApprovedPlanRefused is the cap-waiting sibling.
// An `approved` plan has already been linted and is admitted to `running` by
// the engine at any tick without a re-lint, so it is exactly as un-lintable as
// a running one.
func TestTaskCreate_PlanMembership_ApprovedPlanRefused(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanInState(t, planStore, plan.StateApproved)

	tool := NewTaskCreateTool(taskStore)
	tool.SetPlanStore(planStore)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title": "late member", "prompt": "do it", "agent_id": "worker",
		"plan_id": p.ID, "criteria": validCriteriaArg(), "dod": validDoDArg(),
	})

	if !res.IsError {
		t.Fatal("create_task naming an APPROVED plan must be refused")
	}
	if !strings.Contains(res.ForLLM, "membership is frozen") {
		t.Errorf("refusal %q must come from the membership gate", res.ForLLM)
	}
}
