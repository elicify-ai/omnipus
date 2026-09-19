// Omnipus - Plan authorship origin (CreatedByKind) tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Plan authorship origin is stamped explicitly at creation (see
// Plan.CreatedByKind's doc comment): create_plan stamps agent because every
// caller of the tool is an agent, and execute_plan's SD-A7 tiered-DoD gate
// reads the stamp via plan.AuthoredByAgent instead of re-deriving origin
// from a registry probe — the probe that misrouted a human named "admin"
// into the strict tier (PR732 CI). These tests pin both halves.

// seedPlanWithKind seeds a draft plan with a specific CreatedByKind (empty
// string = legacy kindless), plus one criteria-complete member task so the
// DoD gate under test is the only gate that can reject.
func seedPlanWithKind(t *testing.T, planStore *plan.Store, taskStore *task.Store, wsID, createdBy, kind string, dod bool) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Title: "Origin Test Plan", WorkspaceID: wsID, OwnerAgentID: "jim",
		CreatedBy:     createdBy,
		CreatedByKind: kind,
	}
	if dod {
		p.DoD = []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "plan done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
		}}
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	tk := &task.Task{
		Title: "member", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "worker", WorkspaceID: wsID, Status: task.StatusNext,
		PlanID: p.ID,
		Criteria: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
		}},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("seed member task: %v", err)
	}
	return p
}

// TestPlanCreateTool_StampsAgentKind proves create_plan stamps the explicit
// agent authorship kind at creation (PR732 origin repair, creation half).
func TestPlanCreateTool_StampsAgentKind(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "admin")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "Admin agent authored",
		"owner_agent_id": "admin",
		"dod":            validCriteriaArg(),
		"rationale":      "Agent-authored decomposition for the origin regression coverage.",
	})
	if res.IsError {
		t.Fatalf("create_plan: %s", res.ForLLM)
	}
	var out struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	got, err := planStore.Get(out.PlanID)
	if err != nil {
		t.Fatalf("get created plan: %v", err)
	}
	if got.CreatedByKind != plan.CreatedByKindAgent {
		t.Errorf("created_by_kind = %q, want %q", got.CreatedByKind, plan.CreatedByKindAgent)
	}
}

// TestPlanExecuteTool_ExplicitKindOverridesRegistryCollision proves the
// execute_plan DoD gate reads the explicit stamp: a plan authored by a HUMAN
// named "admin" (kind=user) passes the gate WITHOUT a DoD even though the
// registry probe would resolve "admin" to the Admin agent.
func TestPlanExecuteTool_ExplicitKindOverridesRegistryCollision(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithKind(t, planStore, taskStore, "ws-1", "admin", plan.CreatedByKindUser, false)

	tool := NewPlanExecuteTool(planStore, taskStore)
	// Checker deliberately wired to say "admin" IS an agent — the explicit
	// user kind must override it.
	tool.SetIsAgentIDChecker(func(id string) bool { return id == "admin" })

	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if res.IsError {
		t.Fatalf("execute_plan should pass the DoD gate on the explicit user kind: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "approved") {
		t.Errorf("unexpected result: %s", res.ForLLM)
	}
}

// TestPlanExecuteTool_AgentKindStillRequiresDoD proves the strict tier is
// NOT weakened: an explicitly agent-stamped plan without a DoD is rejected
// even when the registry probe would have called it human.
func TestPlanExecuteTool_AgentKindStillRequiresDoD(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithKind(t, planStore, taskStore, "ws-1", "admin", plan.CreatedByKindAgent, false)

	tool := NewPlanExecuteTool(planStore, taskStore)
	tool.SetIsAgentIDChecker(func(string) bool { return false })

	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if !res.IsError {
		t.Fatal("agent-stamped plan without DoD must still be rejected")
	}
	if !strings.Contains(res.ForLLM, "Definition of Done") {
		t.Errorf("unexpected error: %s", res.ForLLM)
	}
}

// TestPlanExecuteTool_LegacyKindlessFallsBackToChecker pins the legacy
// fallback: a kindless plan defers to the injected registry probe, and an
// unwired (nil) checker fail-closes to strict.
func TestPlanExecuteTool_LegacyKindlessFallsBackToChecker(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)

	// Legacy agent-looking plan, checker says agent -> strict tier fires.
	pAgent := seedPlanWithKind(t, planStore, taskStore, "ws-1", "admin", "", false)
	tool := NewPlanExecuteTool(planStore, taskStore)
	tool.SetIsAgentIDChecker(func(id string) bool { return id == "admin" })
	res := tool.Execute(context.Background(), map[string]any{"plan_id": pAgent.ID})
	if !res.IsError || !strings.Contains(res.ForLLM, "Definition of Done") {
		t.Fatalf("legacy kindless + agent probe must require DoD, got: %s", res.ForLLM)
	}

	// Legacy human-looking plan, checker says human -> soft tier passes.
	pHuman := seedPlanWithKind(t, planStore, taskStore, "ws-1", "daniel", "", false)
	tool2 := NewPlanExecuteTool(planStore, taskStore)
	tool2.SetIsAgentIDChecker(func(id string) bool { return id == "admin" })
	res2 := tool2.Execute(context.Background(), map[string]any{"plan_id": pHuman.ID})
	if res2.IsError {
		t.Fatalf("legacy kindless + human probe must pass soft tier, got: %s", res2.ForLLM)
	}

	// Unwired checker (nil) -> fail closed to strict even for a human-named
	// plan (the safe direction of wrong).
	pNil := seedPlanWithKind(t, planStore, taskStore, "ws-1", "daniel", "", false)
	tool3 := NewPlanExecuteTool(planStore, taskStore)
	res3 := tool3.Execute(context.Background(), map[string]any{"plan_id": pNil.ID})
	if !res3.IsError || !strings.Contains(res3.ForLLM, "Definition of Done") {
		t.Fatalf("nil checker must fail closed to strict, got: %s", res3.ForLLM)
	}
}
