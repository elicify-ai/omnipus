// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// plan_goal_read_parity_test.go — RED tests for the plan/goal write tools and
// the read-visibility table (tool-parity spec r17 §13.1 tests 9, 10, 11;
// scenarios 5.1–5.8, 4.1–4.12; §7.3, §7.8).
//
// ORACLE: §7.3 (update_plan authority, draft-only fields, bounds merge),
// PL-01 (workspace_id only), §7.8's visibility table and the §7.11 refusal
// texts. `update_plan`, `get_task`, `get_plan` and `get_goal` do not exist
// yet, so their subtests BLOCKED-fatal through the production catalog lookup;
// the create_plan rows run against the real tool and fail on the spec
// behaviour that is missing.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// t8RequireTool looks a tool up in the production general catalog and
// BLOCKED-fatals when it does not exist yet (never a skip).
func t8RequireTool(t *testing.T, name, specRef string) Tool {
	t.Helper()
	tool, ok := t8FindGeneralTool(name)
	if !ok {
		t.Fatalf("BLOCKED: %s not implemented — required by %s", name, specRef)
	}
	return tool
}

// -----------------------------------------------------------------------------
// Test 9 — TestUpdatePlan_StateAndAuthority (scenarios 5.1–5.6; §7.3)
// -----------------------------------------------------------------------------

func TestUpdatePlan_StateAndAuthority(t *testing.T) {
	const denialText = "update_plan denied: you are not the owner of a plan with that id"
	const dodFrozenText = "dod cannot be changed once the plan has left draft state"

	newStores := func(t *testing.T) (*plan.Store, *task.Store, *plan.Plan) {
		t.Helper()
		ps, ts := newPlanAndTaskStores(t)
		p := &plan.Plan{
			WorkspaceID: "ws-a", Title: "plan", Objective: "objective",
			State: plan.StateDraft, OwnerAgentID: "owner-agent",
			CreatedByKind: "agent", CreatedBy: "creator-agent",
		}
		require.NoError(t, ps.Create(p), "seed draft plan")
		return ps, ts, p
	}

	t.Run("5.1 owner edits objective and dod on a draft", func(t *testing.T) {
		ps, _, p := newStores(t)
		_ = ps
		_ = p
		t.Fatal("BLOCKED: update_plan not implemented — required by §7.3 PL-02–PL-08 (scenario 5.1)")
	})

	t.Run("5.2 title editable in any state", func(t *testing.T) {
		_, _, _ = newStores(t)
		t.Fatal("BLOCKED: update_plan not implemented — required by §7.3 PL-02 (scenario 5.2)")
	})

	t.Run("5.3 dod frozen once the plan has left draft", func(t *testing.T) {
		ps, _, p := newStores(t)
		state := plan.StateApproved
		_, err := ps.Update(p.ID, plan.Patch{State: &state})
		require.NoError(t, err, "fixture: approve the plan")
		t.Fatalf("BLOCKED: update_plan not implemented — required by §7.3 PL-06; the refusal text is %q", dodFrozenText)
	})

	t.Run("5.4 neither owner nor creator is denied with the §7.11 text", func(t *testing.T) {
		_, _, _ = newStores(t)
		t.Fatalf("BLOCKED: update_plan not implemented — required by §7.3; the denial text is %q", denialText)
	})

	t.Run("5.5 the agent creator may edit even when it is not the owner", func(t *testing.T) {
		_, _, _ = newStores(t)
		t.Fatal("BLOCKED: update_plan not implemented — required by §7.3 (creator authority, scenario 5.5)")
	})

	t.Run("5.6 a legacy plan with an empty CreatedByKind fails closed", func(t *testing.T) {
		ps, _, _ := newStores(t)
		legacy := &plan.Plan{
			WorkspaceID: "ws-a", Title: "legacy", State: plan.StateDraft,
			OwnerAgentID: "owner-agent", CreatedByKind: "", CreatedBy: "creator-agent",
		}
		require.NoError(t, ps.Create(legacy), "seed legacy plan")
		t.Fatalf("BLOCKED: update_plan not implemented — required by §7.3; a kind-empty plan has no agent creator (fail closed), denial text %q", denialText)
	})
}

// -----------------------------------------------------------------------------
// Test 10 — TestCreatePlan_AliasRemoved_DescriptionBounds
// (scenarios 5.7, 5.8; §7.3 PL-01, PL-04, PL-08)
// -----------------------------------------------------------------------------

func TestCreatePlan_AliasRemoved_DescriptionBounds(t *testing.T) {
	t.Run("5.7 the workspace alias is refused as an unknown argument", func(t *testing.T) {
		ps, _ := newPlanAndTaskStores(t)
		tool := NewPlanCreateTool(ps)
		tool.SetOwnerValidator(allowOwner)

		ctx := WithAgentID(WithWorkspaceID(context.Background(), "ws-a"), "agent-a")
		res := tool.Execute(ctx, map[string]any{
			"title": "alias probe", "owner_agent_id": "agent-a", "workspace": "ws-other",
			"dod": []any{map[string]any{"text": "the plan is verified"}}, "rationale": "T8 parity probe",
		})
		require.True(t, res.IsError,
			"create_plan must not accept the removed `workspace` alias (PL-01); got success: %s", res.ForLLM)
		assert.Contains(t, res.ForLLM, "workspace", "the refusal names the argument")

		// Through the production registry the same call is the generic
		// unknown-argument refusal naming the argument.
		tool2, ok := t8FindGeneralTool("create_plan")
		require.True(t, ok, "create_plan is an existing production tool")
		reg := NewToolRegistry()
		reg.Register(tool2)
		res = reg.Execute(ctx, "create_plan", map[string]any{
			"title": "alias probe", "owner_agent_id": "agent-a", "workspace": "ws-other",
			"dod": []any{map[string]any{"text": "the plan is verified"}}, "rationale": "T8 parity probe",
		})
		require.True(t, res.IsError)
		assert.Contains(t, res.ForLLM, `invalid arguments for tool "create_plan"`,
			"the registry's generic refusal, naming the argument")
	})

	t.Run("5.8 description and bounds are created and read back", func(t *testing.T) {
		// This row proves persistence, not a configurable ceiling. Five days is
		// below D-3's seven-day global. The missing live-ceiling binding is
		// reported separately by TestBudgets_CeilingWins; a test-only global
		// cannot configure this production tool.

		ps, _ := newPlanAndTaskStores(t)
		tool := NewPlanCreateTool(ps)
		tool.SetOwnerValidator(allowOwner)

		ctx := WithAgentID(WithWorkspaceID(context.Background(), "ws-a"), "agent-a")
		res := tool.Execute(ctx, map[string]any{
			"title": "with description", "owner_agent_id": "agent-a",
			"description": "the long description",
			"bounds":      map[string]any{"idle_expiry_days": 5},
			"dod":         validDoDArg(),
			"rationale":   "T8 persistence fixture with one bounded plan",
		})
		require.False(t, res.IsError, "description and in-ceiling bounds are accepted: %s", res.ForLLM)

		// The stored plan carries both (the read half through get_plan is
		// test 11/35b's).
		var out struct {
			PlanID string `json:"plan_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "parse result: %s", res.ForLLM)
		got, err := ps.Get(out.PlanID)
		require.NoError(t, err)
		assert.Equal(t, "the long description", got.Description, "description is stored")
		require.NotNil(t, got.Bounds, "bounds are stored")
		require.NotNil(t, got.Bounds.IdleExpiryDays)
		assert.Equal(t, 5, *got.Bounds.IdleExpiryDays)
	})
}

// -----------------------------------------------------------------------------
// Test 11 — TestReads_VisibilityTable (scenarios 4.1–4.12; §7.8)
// -----------------------------------------------------------------------------

func TestReads_VisibilityTable(t *testing.T) {
	// Fixture factory: two agents in one workspace plus a second workspace;
	// the same-id fixture pairs are built per row.
	fixture := func(t *testing.T) (*task.Store, *plan.Store) {
		t.Helper()
		ps, ts := newPlanAndTaskStores(t)
		return ts, ps
	}

	t.Run("4.1–4.4 get_task/get_plan/get_goal return the record (visible caller)", func(t *testing.T) {
		ts, ps := fixture(t)
		_ = ps
		tk := parityTask(t, ts, "agent-a", "agent-a", "ws-a", task.StatusInbox, false)
		_ = tk
		t8RequireTool(t, "get_task", "spec §7.8 / §7.9 (scenario 4.1)")
		t.Fatal("BLOCKED: get_task not implemented — required by §7.8 (scenarios 4.1–4.4)")
	})

	t.Run("4.5 get_goal with no goal is the plain no-goal result, not an error", func(t *testing.T) {
		fixture(t)
		t8RequireTool(t, "get_goal", "spec §7.8 (scenario 4.5)")
		t.Fatal("BLOCKED: get_goal not implemented — required by §7.8 (scenario 4.5)")
	})

	t.Run("4.6/4.9 byte-identical not-found for one id across unreadable and absent fixtures", func(t *testing.T) {
		fixture(t)
		t8RequireTool(t, "get_task", "spec §7.8 (scenario 4.6)")
		t.Fatalf("BLOCKED: get_task/get_plan not implemented — the same-id two-fixture oracle (R4-MIN-002) cannot run; the not-found template to assert byte-for-byte is %q", t8TaskNotFoundTemplate)
	})

	t.Run("4.8 a turn without a workspace reads its own assignee task elsewhere", func(t *testing.T) {
		ts, _ := fixture(t)
		tk := parityTask(t, ts, "agent-a", "agent-a", "ws-b", task.StatusInbox, false)
		ctx := WithAgentID(WithWorkspaceID(context.Background(), ""), "agent-a")
		_ = ctx
		_ = tk
		t8RequireTool(t, "get_task", "spec §7.8 (scenario 4.8)")
		t.Fatal("BLOCKED: get_task not implemented — required by §7.8 (scenario 4.8)")
	})

	t.Run("4.10 same workspace, unrelated agent reads not-found, matching list_tasks membership", func(t *testing.T) {
		ts, _ := fixture(t)
		// Task X in ws-a created by agent-b and assigned to agent-b:
		// unreadable for agent-a.
		tk := parityTask(t, ts, "agent-b", "agent-b", "ws-a", task.StatusInbox, false)
		_ = tk
		t8RequireTool(t, "get_task", "spec §7.8 (scenario 4.10)")
		t.Fatal("BLOCKED: get_task not implemented — required by §7.8 (scenario 4.10)")
	})

	t.Run("4.11 a person-created task reads only for its assignee", func(t *testing.T) {
		ts, _ := fixture(t)
		tk := parityTask(t, ts, "", "agent-b", "ws-a", task.StatusInbox, false)
		_ = tk
		t8RequireTool(t, "get_task", "spec §7.8 (scenario 4.11)")
		t.Fatal("BLOCKED: get_task not implemented — required by §7.8 (scenario 4.11)")
	})

	t.Run("4.12 plan visibility: member-task assignee in, unrelated plan out", func(t *testing.T) {
		ts, ps := fixture(t)
		p := &plan.Plan{WorkspaceID: "ws-a", Title: "P3", State: plan.StateRunning, OwnerAgentID: "agent-b"}
		require.NoError(t, ps.Create(p))
		tk := parityTask(t, ts, "agent-b", "agent-a", "ws-a", task.StatusNext, false)
		_, err := ts.Update(tk.ID, task.Patch{PlanID: &p.ID})
		require.NoError(t, err)
		t8RequireTool(t, "get_plan", "spec §7.8 (scenario 4.12)")
		t.Fatal("BLOCKED: get_plan not implemented — required by §7.8 (scenario 4.12)")
	})

}

// t8TaskNotFoundTemplate is the §7.8 not-found template the read tools must
// render byte-for-byte with the requested id echoed (spec §7.8; the GREEN
// side asserts strings.ReplaceAll(template, "<id>", want) against each
// refusal). It is the shared oracle constant for rows 4.6/4.9, not a
// test of its own — a string operation on a literal would prove nothing.
const t8TaskNotFoundTemplate = `task "<id>" not found`
