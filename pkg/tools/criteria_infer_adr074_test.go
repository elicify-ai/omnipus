// Omnipus — ADR-074 D2/D3b tool-layer inference tests (pkg/tools half)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// criteria_infer_adr074_test.go — ADR-074 D2/D3b, judgment-first-criteria-spec
// tests #3 (the ADR-049 D2-rule-5 all-check bash-policy gate fires on
// INFERRED kinds in create_task) and #2b (behavior round-trips end-to-end
// through create_task, create_plan and plan_correct with kind omitted;
// create_task_in_workspace's half lives in pkg/sysagent/tools).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// kindOmittedCheckCriteria is an all-check criteria set that never says
// "check": the kind must be INFERRED from the payload for the D2-rule-5 gate
// to see it.
func kindOmittedCheckCriteria() []any {
	return []any{
		map[string]any{
			"text": "tests pass",
			"check": map[string]any{
				"command":            "go test ./...",
				"expected_exit_code": float64(0),
			},
		},
	}
}

// kindOmittedBehaviorCriteria is a behavior criterion that never says
// "behavior" — the explicit 0/0 pair ("never call bash") pins the pointer
// semantics through the whole write path.
func kindOmittedBehaviorCriteria() []any {
	return []any{
		map[string]any{
			"text": "never shells out",
			"behavior": map[string]any{
				"tool":      "bash",
				"min_count": float64(0),
				"max_count": float64(0),
			},
		},
	}
}

// TestTaskCreate_Gate_FiresOnInferredKind is spec test #3 (create_task half,
// ADR required-test #1): a kind-OMITTED all-check criteria set against an
// assignee whose bash policy is deny must be rejected by the D2-rule-5 gate —
// inference runs in the parser, BEFORE the gate, so the gate sees kind=check.
func TestTaskCreate_Gate_FiresOnInferredKind(t *testing.T) {
	t.Parallel()

	newTool := func(t *testing.T, policy string) (*TaskCreateTool, *task.Store) {
		t.Helper()
		store := task.New(t.TempDir())
		tool := NewTaskCreateTool(store)
		tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
		tool.SetBashPolicyChecker(func(string) (string, bool) { return policy, true })
		return tool, store
	}
	baseArgs := func(criteria []any) map[string]any {
		return map[string]any{"title": "t", "prompt": "p", "agent_id": "assignee", "criteria": criteria}
	}
	ctx := WithWorkspaceID(WithAgentID(context.Background(), "caller"), "ws")

	t.Run("deny_rejected", func(t *testing.T) {
		t.Parallel()
		tool, _ := newTool(t, "deny")
		res := tool.Execute(ctx, baseArgs(kindOmittedCheckCriteria()))
		if !res.IsError {
			t.Fatal("kind-omitted all-check vs bash:deny must be rejected — the gate must fire on INFERRED kinds")
		}
		if !strings.Contains(res.ForLLM, "D2 rule 5") {
			t.Errorf("expected the D2 rule 5 message, got: %s", res.ForLLM)
		}
	})

	t.Run("allow_accepted_and_persists_inferred_check", func(t *testing.T) {
		t.Parallel()
		tool, store := newTool(t, "allow")
		res := tool.Execute(ctx, baseArgs(kindOmittedCheckCriteria()))
		if res.IsError {
			t.Fatalf("kind-omitted check vs bash:allow must succeed, got: %s", res.ForLLM)
		}
		var out struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil || out.TaskID == "" {
			t.Fatalf("parse result %q: %v", res.ForLLM, err)
		}
		got, err := store.Get(out.TaskID)
		if err != nil {
			t.Fatalf("get created task: %v", err)
		}
		if len(got.Criteria) != 1 || got.Criteria[0].Kind != task.KindCheck {
			t.Errorf("persisted criteria = %+v, want one inferred kind=check", got.Criteria)
		}
	})

	t.Run("mixed_inferred_not_gated", func(t *testing.T) {
		t.Parallel()
		// One inferred check + one inferred prose: NOT all-check, so bash:deny
		// must not gate it.
		tool, _ := newTool(t, "deny")
		criteria := append(kindOmittedCheckCriteria(), map[string]any{"text": "reads well"})
		res := tool.Execute(ctx, baseArgs(criteria))
		if res.IsError {
			t.Fatalf("mixed inferred check+prose must not hit the all-check gate, got: %s", res.ForLLM)
		}
	})

	t.Run("kind_omitted_dual_payload_rejected", func(t *testing.T) {
		t.Parallel()
		tool, _ := newTool(t, "allow")
		criteria := []any{map[string]any{
			"text":     "ambiguous",
			"check":    map[string]any{"command": "true", "expected_exit_code": float64(0)},
			"behavior": map[string]any{"tool": "bash"},
		}}
		res := tool.Execute(ctx, baseArgs(criteria))
		if !res.IsError || !strings.Contains(res.ForLLM, "ambiguous") {
			t.Fatalf("kind-omitted dual-payload criterion must be rejected as ambiguous, got: %s", res.ForLLM)
		}
	})
}

// TestBehavior_EndToEnd_CreateTask is spec test #2b for create_task: a
// kind-omitted behavior criterion round-trips schema -> parser -> store with
// kind inferred and the explicit 0/0 pointer pair intact.
func TestBehavior_EndToEnd_CreateTask(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tool := NewTaskCreateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
	ctx := WithWorkspaceID(WithAgentID(context.Background(), "caller"), "ws")

	res := tool.Execute(ctx, map[string]any{
		"title": "no shell", "prompt": "p", "agent_id": "assignee",
		"criteria": kindOmittedBehaviorCriteria(),
	})
	if res.IsError {
		t.Fatalf("create_task with kind-omitted behavior criterion failed: %s", res.ForLLM)
	}
	var out struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil || out.TaskID == "" {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	got, err := store.Get(out.TaskID)
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	assertInferredNeverCallBehavior(t, got.Criteria)
}

// TestBehavior_EndToEnd_CreatePlan is spec test #2b for create_plan: same
// round-trip through the plan store's DoD.
func TestBehavior_EndToEnd_CreatePlan(t *testing.T) {
	t.Parallel()
	planStore, _ := newPlanAndTaskStores(t)
	tool := NewPlanCreateTool(planStore)
	tool.SetOwnerValidator(allowOwner)
	ctx := WithWorkspaceID(WithAgentID(context.Background(), "jim"), "ws-1")

	res := tool.Execute(ctx, map[string]any{
		"title":          "No-shell plan",
		"owner_agent_id": "jim",
		"dod":            kindOmittedBehaviorCriteria(),
		"rationale":      "single member, behavior-bounded",
	})
	if res.IsError {
		t.Fatalf("create_plan with kind-omitted behavior DoD failed: %s", res.ForLLM)
	}
	var out struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil || out.PlanID == "" {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	got, err := planStore.Get(out.PlanID)
	if err != nil {
		t.Fatalf("get created plan: %v", err)
	}
	assertInferredNeverCallBehavior(t, got.DoD)
}

// TestBehavior_EndToEnd_PlanCorrect is spec test #2b for plan_correct: a
// kind-omitted behavior criterion on an appended tail member reaches the
// correction engine with the kind inferred and pointers intact.
func TestBehavior_EndToEnd_PlanCorrect(t *testing.T) {
	t.Parallel()
	f := newParkedPlan(t)
	f.addMember(t, "m-existing", task.StatusInProgress, []task.AcceptanceCriterion{proseCriterion("works")})
	spy := &correctionSpy{honest: false}
	tool := f.tool(spy)

	res := tool.Execute(supervisorCtx(), map[string]any{
		"plan_id":              f.planID,
		"verb":                 "append",
		"falsified_assumption": "the original tail was missing a no-shell guard member",
		"tail_members": []any{map[string]any{
			"title":    "guarded member",
			"criteria": kindOmittedBehaviorCriteria(),
		}},
	})
	if res.IsError {
		t.Fatalf("plan_correct append with kind-omitted behavior criterion failed: %s", res.ForLLM)
	}
	if len(spy.calls) != 1 || len(spy.calls[0].TailMembers) != 1 {
		t.Fatalf("engine did not receive exactly one tail member: %+v", spy.calls)
	}
	assertInferredNeverCallBehavior(t, spy.calls[0].TailMembers[0].Criteria)
}

// assertInferredNeverCallBehavior asserts criteria is exactly one criterion
// whose kind was INFERRED as behavior with the explicit {0,0} "never call"
// pointer pair preserved.
func assertInferredNeverCallBehavior(t *testing.T, criteria []task.AcceptanceCriterion) {
	t.Helper()
	if len(criteria) != 1 {
		t.Fatalf("want exactly 1 criterion, got %d", len(criteria))
	}
	c := criteria[0]
	if c.Kind != task.KindBehavior {
		t.Fatalf("kind = %q, want inferred behavior", c.Kind)
	}
	if c.Behavior == nil {
		t.Fatal("behavior payload missing")
	}
	if c.Behavior.Tool != "bash" {
		t.Errorf("behavior.tool = %q, want bash", c.Behavior.Tool)
	}
	if c.Behavior.MinCount == nil || *c.Behavior.MinCount != 0 {
		t.Errorf("EXPLICIT min_count 0 not preserved: %v", c.Behavior.MinCount)
	}
	if c.Behavior.MaxCount == nil || *c.Behavior.MaxCount != 0 {
		t.Errorf("EXPLICIT max_count 0 not preserved: %v", c.Behavior.MaxCount)
	}
}

// TestCriteriaSchemas_RequiredRelaxedToText is spec test #5's required half
// (D3b): the three pkg/tools criteria schemas require only text.
func TestCriteriaSchemas_RequiredRelaxedToText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		params map[string]any
		path   []string
	}{
		{"create_task", (&TaskCreateTool{}).Parameters(), []string{"criteria"}},
		{"create_plan", (&PlanCreateTool{}).Parameters(), []string{"dod"}},
		{"plan_correct", (&PlanCorrectTool{}).Parameters(), []string{"tail_members", "criteria"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			items := criterionItemsSchema(t, tc.params, tc.path...)
			req, ok := items["required"].([]string)
			if !ok || len(req) != 1 || req[0] != "text" {
				t.Errorf("criterion items required = %v, want [text] (ADR-074 D3b)", items["required"])
			}
		})
	}
}
