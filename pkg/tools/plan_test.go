// Omnipus — Plan Agent Tools Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newPlanAndTaskStores returns fresh plan and task stores rooted under a
// shared temp dir (siblings, mirroring how the gateway roots them under
// $OMNIPUS_HOME).
func newPlanAndTaskStores(t *testing.T) (*plan.Store, *task.Store) {
	t.Helper()
	root := t.TempDir()
	return plan.New(filepath.Join(root, "plans")), task.New(filepath.Join(root, "tasks"))
}

// allowOwner is a PlanCreateTool owner validator stub that always allows.
func allowOwner(string) error { return nil }

// --- create_plan ---

// TestPlanCreateTool_Happy proves create_plan persists a draft plan carrying
// the given goal/DoD/owner (ADR-052 FR-001, US-1 Acceptance 1).
func TestPlanCreateTool_Happy(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "Ship v1.0",
		"goal":           "Ship the v1.0 release",
		"owner_agent_id": "jim",
		"dod":            validCriteriaArg(),
		"rationale":      "Single serial member, no parallel decomposition needed for v1.0.",
	})
	if res.IsError {
		t.Fatalf("create_plan: %s", res.ForLLM)
	}

	var out struct {
		PlanID string `json:"plan_id"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if out.State != string(plan.StateDraft) {
		t.Errorf("state = %q, want draft", out.State)
	}

	got, err := planStore.Get(out.PlanID)
	if err != nil {
		t.Fatalf("get created plan: %v", err)
	}
	if got.Title != "Ship v1.0" || got.OwnerAgentID != "jim" || got.WorkspaceID != "ws-1" {
		t.Errorf("unexpected plan fields: %+v", got)
	}
	if len(got.DoD) != 1 {
		t.Fatalf("expected 1 DoD criterion, got %d", len(got.DoD))
	}
	if got.CreatedBy != "jim" {
		t.Errorf("created_by = %q, want jim", got.CreatedBy)
	}
}

// TestPlanCreateTool_RejectsMissingDoD proves an agent-authored plan without
// a Definition of Done is rejected (tiered DoD collapsed to strict — every
// caller of this tool is an agent). ADR-052 FR-001, US-1 Acceptance 2.
func TestPlanCreateTool_RejectsMissingDoD(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "No DoD plan",
		"owner_agent_id": "jim",
	})
	if !res.IsError {
		t.Fatal("expected rejection for missing dod")
	}
	if !strings.Contains(res.ForLLM, "dod") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}

	all, _ := planStore.List(plan.Filter{WorkspaceID: "ws-1"})
	if len(all) != 0 {
		t.Fatal("no plan should have been persisted")
	}
}

// TestPlanCreateTool_RejectsMissingRationale proves create_plan requires
// rationale unconditionally (ADR-053 §Contract Surface) — every caller of
// this tool is an agent, mirroring the dod gate's own strict-tier collapse.
func TestPlanCreateTool_RejectsMissingRationale(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "No rationale plan",
		"owner_agent_id": "jim",
		"dod":            validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected rejection for missing rationale")
	}
	if !strings.Contains(res.ForLLM, "rationale") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}

	all, _ := planStore.List(plan.Filter{WorkspaceID: "ws-1"})
	if len(all) != 0 {
		t.Fatal("no plan should have been persisted")
	}
}

// TestPlanCreateTool_RejectsWhitespaceOnlyRationale proves a
// whitespace-only rationale is rejected the same as an absent one.
func TestPlanCreateTool_RejectsWhitespaceOnlyRationale(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "Whitespace rationale plan",
		"owner_agent_id": "jim",
		"dod":            validCriteriaArg(),
		"rationale":      "   \n\t  ",
	})
	if !res.IsError {
		t.Fatal("expected rejection for whitespace-only rationale")
	}
}

// TestPlanCreateTool_PersistsRationale proves a supplied rationale round-trips
// onto the persisted plan record.
func TestPlanCreateTool_PersistsRationale(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	const wantRationale = "Split into two lint-disjoint streams that converge at a single merge member."
	res := tool.Execute(ctx, map[string]any{
		"title":          "Rationale round-trip",
		"owner_agent_id": "jim",
		"dod":            validCriteriaArg(),
		"rationale":      wantRationale,
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
		t.Fatalf("get persisted plan: %v", err)
	}
	if got.Rationale != wantRationale {
		t.Fatalf("expected rationale %q to persist, got %q", wantRationale, got.Rationale)
	}
}

// TestPlanCreateTool_RejectsEmptyDoDArray proves an explicitly-empty dod
// array is treated the same as an absent one.
func TestPlanCreateTool_RejectsEmptyDoDArray(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "Empty DoD array",
		"owner_agent_id": "jim",
		"dod":            []any{},
	})
	if !res.IsError {
		t.Fatal("expected rejection for empty dod array")
	}
}

// TestPlanCreateTool_UnwiredOwnerValidator_FailsClosed proves an unwired
// owner validator denies the call by default (never a silent allow).
func TestPlanCreateTool_UnwiredOwnerValidator_FailsClosed(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore) // SetOwnerValidator never called

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "Unwired validator",
		"owner_agent_id": "jim",
		"dod":            validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected fail-closed rejection when owner validator is unwired")
	}

	all, _ := planStore.List(plan.Filter{WorkspaceID: "ws-1"})
	if len(all) != 0 {
		t.Fatal("no plan should have been persisted")
	}
}

// TestPlanCreateTool_OwnerValidatorRejects proves a validator-rejected
// owner_agent_id blocks plan creation.
func TestPlanCreateTool_OwnerValidatorRejects(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(func(id string) error {
		return errors.New("owner_agent_id \"ghost\" is not a registered agent")
	})

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "Bad owner",
		"owner_agent_id": "ghost",
		"dod":            validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected rejection for invalid owner_agent_id")
	}
	if !strings.Contains(res.ForLLM, "ghost") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}
}

// TestPlanCreateTool_ExplicitWorkspaceArg proves the "workspace" tool arg
// overrides the ctx-bound workspace.
func TestPlanCreateTool_ExplicitWorkspaceArg(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-ctx-bound")

	res := tool.Execute(ctx, map[string]any{
		"title":          "Explicit workspace",
		"owner_agent_id": "jim",
		"workspace":      "ws-explicit",
		"dod":            validCriteriaArg(),
		"rationale":      "Explicit workspace override, single member.",
	})
	if res.IsError {
		t.Fatalf("create_plan: %s", res.ForLLM)
	}
	all, _ := planStore.List(plan.Filter{WorkspaceID: "ws-explicit"})
	if len(all) != 1 {
		t.Fatalf("expected 1 plan in ws-explicit, got %d", len(all))
	}
}

// TestPlanCreateTool_NilStore_FailsClosed proves a nil store (metadata-only
// construction) never executes.
func TestPlanCreateTool_NilStore_FailsClosed(t *testing.T) {
	t.Parallel()
	tool := NewPlanCreateTool(nil)
	tool.SetOwnerValidator(allowOwner)

	res := tool.Execute(context.Background(), map[string]any{
		"title":          "x",
		"owner_agent_id": "jim",
		"dod":            validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected error with nil plan store")
	}
}

// --- execute_plan ---

// seedPlanWithMembers creates a draft plan with n member tasks in wsID,
// each carrying one criterion unless its index is in noCriteriaIdx. The
// seeded plan carries a satisfying (non-empty) DoD by default so the
// SD-A7 tiered-DoD gate (execute_plan's isAgentID fail-closed default —
// see TestPlanExecuteTool_* DoD-specific cases below for that gate
// exercised in isolation) never interferes with tests targeting a
// DIFFERENT check (criteria gap, empty plan, idempotent no-ops, terminal
// rejection).
func seedPlanWithMembers(t *testing.T, planStore *plan.Store, taskStore *task.Store, wsID string, n int, noCriteriaIdx map[int]bool) *plan.Plan {
	t.Helper()
	p := &plan.Plan{
		Title: "Test Plan", WorkspaceID: wsID, OwnerAgentID: "jim",
		CreatedBy: "jim",
		DoD: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "plan done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
		}},
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	for i := 0; i < n; i++ {
		tk := &task.Task{
			Title: "member", Prompt: "do it", Action: task.ActionLLM,
			AgentID: "worker", WorkspaceID: wsID, Status: task.StatusNext,
			PlanID: p.ID,
		}
		if !noCriteriaIdx[i] {
			tk.Criteria = []task.AcceptanceCriterion{{
				Kind: task.KindProse, Text: "done",
				Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
			}}
		}
		if err := taskStore.Create(tk); err != nil {
			t.Fatalf("seed member task: %v", err)
		}
	}
	return p
}

// TestPlanExecuteTool_Happy proves execute_plan approves a criteria-complete
// plan (ADR-052 FR-003, US-3 Acceptance 1).
func TestPlanExecuteTool_Happy(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithMembers(t, planStore, taskStore, "ws-1", 2, nil)

	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if res.IsError {
		t.Fatalf("execute_plan: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "approved") {
		t.Errorf("unexpected result: %s", res.ForLLM)
	}

	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateApproved {
		t.Errorf("state = %q, want approved", got.State)
	}
}

// TestPlanExecuteTool_RejectsCriteriaGap proves execute_plan rejects a plan
// with an uncriteria'd member, surfacing per-task task_errors, and leaves
// the plan draft (ADR-052 FR-004, US-3 Acceptance 2, Test 4).
func TestPlanExecuteTool_RejectsCriteriaGap(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithMembers(t, planStore, taskStore, "ws-1", 3, map[int]bool{1: true})

	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if !res.IsError {
		t.Fatal("expected rejection for a member with zero criteria")
	}

	var payload struct {
		Error      string `json:"error"`
		TaskErrors []struct {
			TaskID string `json:"task_id"`
			Title  string `json:"title"`
			Reason string `json:"reason"`
		} `json:"task_errors"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("expected structured task_errors JSON, got %q: %v", res.ForLLM, err)
	}
	if len(payload.TaskErrors) != 1 {
		t.Fatalf("expected exactly 1 task_error, got %d: %+v", len(payload.TaskErrors), payload.TaskErrors)
	}
	if payload.TaskErrors[0].Reason != "task has no acceptance criteria" {
		t.Errorf("unexpected reason: %s", payload.TaskErrors[0].Reason)
	}

	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateDraft {
		t.Errorf("plan state = %q, want draft (unchanged)", got.State)
	}
}

// TestPlanExecuteTool_RejectsLintViolation proves execute_plan runs
// plan-lint (ADR-053 FR-156/159, G-16) and rejects a plan whose two
// parallel (no blocked_by ordering) member tasks declare overlapping
// write_set paths — the rejection surfaces the structured violation list
// and leaves the plan draft, mirroring RejectsCriteriaGap's own contract.
func TestPlanExecuteTool_RejectsLintViolation(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{
		Title: "Lint-violating plan", WorkspaceID: "ws-1", OwnerAgentID: "jim",
		CreatedBy: "jim",
		DoD: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "plan done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
		}},
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	criteria := []task.AcceptanceCriterion{{
		Kind: task.KindProse, Text: "done",
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
	}}
	for _, id := range []string{"stream-a", "stream-b"} {
		tk := &task.Task{
			ID: id, Title: "member " + id, Prompt: "do it", Action: task.ActionLLM,
			AgentID: "worker", WorkspaceID: "ws-1", Status: task.StatusNext,
			PlanID: p.ID, Criteria: criteria, WriteSet: []string{"shared/config.go"},
		}
		if err := taskStore.Create(tk); err != nil {
			t.Fatalf("seed member task %s: %v", id, err)
		}
	}

	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if !res.IsError {
		t.Fatal("expected rejection for overlapping parallel write_sets")
	}

	var payload struct {
		Error          string `json:"error"`
		LintViolations []struct {
			Kind      string   `json:"kind"`
			MemberIDs []string `json:"member_ids"`
			Paths     []string `json:"paths"`
			Reason    string   `json:"reason"`
		} `json:"lint_violations"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("expected structured lint_violations JSON, got %q: %v", res.ForLLM, err)
	}
	if len(payload.LintViolations) != 1 {
		t.Fatalf("expected exactly 1 lint violation, got %d: %+v", len(payload.LintViolations), payload.LintViolations)
	}
	v := payload.LintViolations[0]
	if v.Kind != "write_set_overlap" {
		t.Errorf("unexpected violation kind: %s", v.Kind)
	}
	if len(v.Paths) == 0 || v.Paths[0] != "shared/config.go" {
		t.Errorf("expected the overlapping path named, got %v", v.Paths)
	}

	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateDraft {
		t.Errorf("plan state = %q, want draft (unchanged)", got.State)
	}
}

// TestPlanExecuteTool_RejectsEmptyPlan proves execute_plan rejects a plan
// with zero member tasks (ADR-052 FR-030).
func TestPlanExecuteTool_RejectsEmptyPlan(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithMembers(t, planStore, taskStore, "ws-1", 0, nil)

	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if !res.IsError {
		t.Fatal("expected rejection for an empty plan")
	}
	if !strings.Contains(res.ForLLM, "zero member") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}

	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateDraft {
		t.Errorf("plan state = %q, want draft (unchanged)", got.State)
	}
}

// TestPlanExecuteTool_RejectsMissingDoD_AgentAuthored proves the SD-A7
// tiered-DoD gate rejects a draft plan with zero DoD when CreatedBy
// resolves to an agent (strict tier), mirroring handlePlanApprove
// (rest_plans.go) exactly, and leaves the plan draft.
func TestPlanExecuteTool_RejectsMissingDoD_AgentAuthored(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{
		Title: "No DoD", WorkspaceID: "ws-1", OwnerAgentID: "jim",
		CreatedBy: "jim", DoD: nil,
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	tk := &task.Task{
		Title: "member", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "worker", WorkspaceID: "ws-1", Status: task.StatusNext,
		PlanID: p.ID,
		Criteria: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
		}},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("seed member task: %v", err)
	}

	tool := NewPlanExecuteTool(planStore, taskStore)
	tool.SetIsAgentIDChecker(func(string) bool { return true })
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if !res.IsError {
		t.Fatal("expected rejection for an agent-authored plan with zero DoD")
	}
	if !strings.Contains(res.ForLLM, "Definition of Done") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}

	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateDraft {
		t.Errorf("plan state = %q, want draft (unchanged)", got.State)
	}
}

// TestPlanExecuteTool_AllowsMissingDoD_HumanAuthored proves the SD-A7 gate's
// soft tier: a plan whose CreatedBy does NOT resolve to an agent may be
// executed with zero DoD, mirroring handlePlanApprove's human-authored path.
func TestPlanExecuteTool_AllowsMissingDoD_HumanAuthored(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{
		Title: "No DoD, human", WorkspaceID: "ws-1", OwnerAgentID: "jim",
		CreatedBy: "alice", DoD: nil,
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	tk := &task.Task{
		Title: "member", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "worker", WorkspaceID: "ws-1", Status: task.StatusNext,
		PlanID: p.ID,
		Criteria: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
		}},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("seed member task: %v", err)
	}

	tool := NewPlanExecuteTool(planStore, taskStore)
	tool.SetIsAgentIDChecker(func(id string) bool { return id == "jim" }) // "alice" is not an agent
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if res.IsError {
		t.Fatalf("expected a human-authored plan with zero DoD to be approved: %s", res.ForLLM)
	}

	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateApproved {
		t.Errorf("plan state = %q, want approved", got.State)
	}
}

// TestPlanExecuteTool_UnwiredIsAgentIDChecker_FailsClosed proves that when
// no isAgentID checker is installed, execute_plan treats CreatedBy as
// agent-authored (strict tier) rather than silently skipping the SD-A7
// gate — the same fail-closed discipline as PlanCreateTool.validateOwner.
func TestPlanExecuteTool_UnwiredIsAgentIDChecker_FailsClosed(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{
		Title: "No DoD, unwired checker", WorkspaceID: "ws-1", OwnerAgentID: "jim",
		CreatedBy: "jim", DoD: nil,
	}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	tk := &task.Task{
		Title: "member", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "worker", WorkspaceID: "ws-1", Status: task.StatusNext,
		PlanID: p.ID,
		Criteria: []task.AcceptanceCriterion{{
			Kind: task.KindProse, Text: "done",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "jim"},
		}},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("seed member task: %v", err)
	}

	tool := NewPlanExecuteTool(planStore, taskStore) // isAgentID never set
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if !res.IsError {
		t.Fatal("expected fail-closed rejection with no isAgentID checker installed")
	}
	if !strings.Contains(res.ForLLM, "Definition of Done") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}
}

// TestPlanExecuteTool_AlreadyRunning_Idempotent proves execute_plan is a
// no-op (not re-approved) for a plan already running.
func TestPlanExecuteTool_AlreadyRunning_Idempotent(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithMembers(t, planStore, taskStore, "ws-1", 1, nil)

	approved := plan.StateApproved
	if _, err := planStore.Update(p.ID, plan.Patch{State: &approved}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	running := plan.StateRunning
	if _, err := planStore.Update(p.ID, plan.Patch{State: &running}); err != nil {
		t.Fatalf("run: %v", err)
	}

	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if res.IsError {
		t.Fatalf("expected idempotent success, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "already running") {
		t.Errorf("unexpected result: %s", res.ForLLM)
	}
}

// TestPlanExecuteTool_AlreadyApproved_Idempotent proves execute_plan on an
// already-approved plan reports the queue note without re-approving.
func TestPlanExecuteTool_AlreadyApproved_Idempotent(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithMembers(t, planStore, taskStore, "ws-1", 1, nil)

	approved := plan.StateApproved
	if _, err := planStore.Update(p.ID, plan.Patch{State: &approved}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if res.IsError {
		t.Fatalf("expected idempotent success, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "queued behind cap") {
		t.Errorf("unexpected result: %s", res.ForLLM)
	}
}

// TestPlanExecuteTool_RejectsTerminalPlan proves execute_plan rejects a
// done/failed plan.
func TestPlanExecuteTool_RejectsTerminalPlan(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := seedPlanWithMembers(t, planStore, taskStore, "ws-1", 1, nil)

	approved := plan.StateApproved
	running := plan.StateRunning
	done := plan.StateDone
	if _, err := planStore.Update(p.ID, plan.Patch{State: &approved}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := planStore.Update(p.ID, plan.Patch{State: &running}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := planStore.Update(p.ID, plan.Patch{State: &done}); err != nil {
		t.Fatalf("finish: %v", err)
	}

	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": p.ID})
	if !res.IsError {
		t.Fatal("expected rejection for a terminal (done) plan")
	}
}

// TestPlanExecuteTool_NotFound proves execute_plan rejects an unknown plan_id.
func TestPlanExecuteTool_NotFound(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	tool := NewPlanExecuteTool(planStore, taskStore)
	res := tool.Execute(context.Background(), map[string]any{"plan_id": "does-not-exist"})
	if !res.IsError {
		t.Fatal("expected rejection for a nonexistent plan")
	}
}

// --- create_task plan_id linkage (ADR-052 FR-002, US-2) ---

// TestTaskCreate_PlanLinkage_Happy proves create_task's optional plan_id
// links a new task to a plan in the same workspace.
func TestTaskCreate_PlanLinkage_Happy(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{Title: "P", WorkspaceID: "ws-1", OwnerAgentID: "jim", CreatedBy: "jim"}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	tool := NewTaskCreateTool(taskStore)
	tool.SetPlanStore(planStore)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title": "member task", "prompt": "do it", "agent_id": "worker",
		"plan_id": p.ID, "criteria": validCriteriaArg(),
	})
	if res.IsError {
		t.Fatalf("create_task with plan_id: %s", res.ForLLM)
	}

	var created struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &created); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	got, err := taskStore.Get(created.TaskID)
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if got.PlanID != p.ID {
		t.Errorf("plan_id = %q, want %q", got.PlanID, p.ID)
	}
}

// TestTaskCreate_PlanLinkage_CrossWorkspaceRejected proves a plan_id naming
// a plan in a different workspace is rejected (ADR-052 US-2 Acceptance 2).
func TestTaskCreate_PlanLinkage_CrossWorkspaceRejected(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{Title: "P", WorkspaceID: "ws-other", OwnerAgentID: "jim", CreatedBy: "jim"}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}

	tool := NewTaskCreateTool(taskStore)
	tool.SetPlanStore(planStore)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title": "member task", "prompt": "do it", "agent_id": "worker",
		"plan_id": p.ID, "criteria": validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected rejection for cross-workspace plan_id")
	}

	all, _ := taskStore.List(task.Filter{WorkspaceID: "ws-1"})
	if len(all) != 0 {
		t.Fatal("no task should have been persisted")
	}
}

// TestTaskCreate_PlanLinkage_TerminalPlanRejected proves linking to a
// terminal (done) plan is rejected.
func TestTaskCreate_PlanLinkage_TerminalPlanRejected(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{Title: "P", WorkspaceID: "ws-1", OwnerAgentID: "jim", CreatedBy: "jim"}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	approved := plan.StateApproved
	running := plan.StateRunning
	done := plan.StateDone
	if _, err := planStore.Update(p.ID, plan.Patch{State: &approved}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := planStore.Update(p.ID, plan.Patch{State: &running}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := planStore.Update(p.ID, plan.Patch{State: &done}); err != nil {
		t.Fatalf("finish: %v", err)
	}

	tool := NewTaskCreateTool(taskStore)
	tool.SetPlanStore(planStore)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title": "member task", "prompt": "do it", "agent_id": "worker",
		"plan_id": p.ID, "criteria": validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected rejection for a terminal plan")
	}
}

// TestTaskCreate_PlanLinkage_UnwiredStore_FailsClosed proves a plan_id arg
// with no plan store wired fails closed rather than silently linking.
func TestTaskCreate_PlanLinkage_UnwiredStore_FailsClosed(t *testing.T) {
	t.Parallel()
	_, taskStore := newPlanAndTaskStores(t)

	tool := NewTaskCreateTool(taskStore) // SetPlanStore never called
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title": "member task", "prompt": "do it", "agent_id": "worker",
		"plan_id": "some-plan", "criteria": validCriteriaArg(),
	})
	if !res.IsError {
		t.Fatal("expected fail-closed rejection when plan store is unwired")
	}
}

// TestTaskCreate_NoPlanID_Unaffected proves an ordinary create_task call
// with no plan_id is entirely unaffected by the plan store being unwired.
func TestTaskCreate_NoPlanID_Unaffected(t *testing.T) {
	t.Parallel()
	_, taskStore := newPlanAndTaskStores(t)

	tool := NewTaskCreateTool(taskStore) // SetPlanStore never called
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	res := tool.Execute(ctx, map[string]any{
		"title": "no plan", "prompt": "do it", "agent_id": "worker",
		"criteria": validCriteriaArg(),
	})
	if res.IsError {
		t.Fatalf("plan-less create_task must be unaffected by an unwired plan store: %s", res.ForLLM)
	}
}
