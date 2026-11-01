package tools

// r17 PL-01/04/08. Plan authority and read-visibility cases were relocated to
// pkg/agent/t8_plan_read_parity_test.go with the same top-level names, replacing
// unconditional declarations with real AgentLoop registration/store assertions.
import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			"bounds":      map[string]any{"idle_expiry_days": float64(5)},
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
