package agent

// Oracle: r17 D-3 / US-3. Real AgentLoop registration and configured stores.
// A nondefault ceiling catches a tool that only hardcodes the shipped value;
// changing it after wiring catches a tool that captured an obsolete snapshot.
import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8RegisteredCall(t *testing.T, inst *AgentInstance, ctx context.Context, name string, args map[string]any) *tools.ToolResult {
	t.Helper()
	_, ok := inst.Tools.Get(name)
	if !ok {
		t.Fatalf("BLOCKED: %s not implemented/registered — required by r17 §7.9/§16", name)
	}
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return inst.Tools.Execute(ctx, name, decoded)
}
func TestT8Budgets_LiveConfigurationThroughRegistry(t *testing.T) {
	al, inst, home := newPlanToolWiringTestLoop(t)
	ps := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(ps)
	cfg := al.GetConfig()
	cfg.Planning.TaskMaxAttempts = 2
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "planner-agent"), testHarnessWorkspaceMembershipID)
	tk := &task.Task{Title: "live ceiling", Prompt: "work", Action: task.ActionLLM, AgentID: "planner-agent", CreatedBy: "planner-agent", CreatedByAgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID, Status: task.StatusInbox}
	require.NoError(t, al.taskStore.Create(tk))
	for _, tc := range []struct {
		global, value int
		accept        bool
	}{{2, 2, true}, {2, 3, false}, {4, 3, true}, {1, 2, false}} {
		t.Run(fmt.Sprintf("ceiling-%d-value-%d", tc.global, tc.value), func(t *testing.T) {
			cfg.Planning.TaskMaxAttempts = tc.global
			before, err := al.taskStore.Get(tk.ID)
			require.NoError(t, err)
			r := t8RegisteredCall(t, inst, ctx, "update_task", map[string]any{"task_id": tk.ID, "max_attempts": tc.value})
			after, err := al.taskStore.Get(tk.ID)
			require.NoError(t, err)
			if tc.accept {
				require.False(t, r.IsError, "configured ceiling %d accepts %d: %s", tc.global, tc.value, r.ForLLM)
				require.NotNil(t, after.MaxAttempts)
				assert.Equal(t, tc.value, *after.MaxAttempts)
			} else {
				require.True(t, r.IsError, "configured ceiling %d must refuse %d", tc.global, tc.value)
				assert.Equal(t, fmt.Sprintf("max_attempts %d is above the global limit of %d. Use %d or less.", tc.value, tc.global, tc.global), r.ForLLM)
				assert.Equal(t, before, after, "refusal applies nothing")
			}
		})
	}
}

func TestT8PlanBounds_UpdateMergesAndUsesLiveCeiling(t *testing.T) {
	al, inst, home := newPlanToolWiringTestLoop(t)
	ps := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(ps)
	al.GetConfig().Planning.SupervisionMaxAttempts = 3
	idle := 5
	p := &plan.Plan{Title: "bounds", WorkspaceID: testHarnessWorkspaceMembershipID, State: plan.StateDraft, OwnerAgentID: "planner-agent", CreatedByKind: "agent", CreatedBy: "planner-agent", Bounds: &plan.PlanBounds{IdleExpiryDays: &idle}}
	require.NoError(t, ps.Create(p))
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "planner-agent"), testHarnessWorkspaceMembershipID)
	r := t8RegisteredCall(t, inst, ctx, "update_plan", map[string]any{"plan_id": p.ID, "bounds": map[string]any{"supervision_max_attempts": 2}})
	require.False(t, r.IsError, "3.7 valid partial bounds: %s", r.ForLLM)
	got, err := ps.Get(p.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Bounds)
	require.NotNil(t, got.Bounds.IdleExpiryDays)
	assert.Equal(t, 5, *got.Bounds.IdleExpiryDays)
	require.NotNil(t, got.Bounds.SupervisionMaxAttempts)
	assert.Equal(t, 2, *got.Bounds.SupervisionMaxAttempts)
	before := got
	r = t8RegisteredCall(t, inst, ctx, "update_plan", map[string]any{"plan_id": p.ID, "title": "must not save", "bounds": map[string]any{"supervision_max_attempts": 4}})
	require.True(t, r.IsError, "3.6 above ceiling must fail")
	assert.Equal(t, "supervision_max_attempts 4 is above the global limit of 3. Use 3 or less.", r.ForLLM)
	got, err = ps.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, before, got)

	// Changing the actual global after registry wiring must immediately change
	// the accepted range. A hardcoded or captured ceiling of 3 cannot pass.
	al.GetConfig().Planning.SupervisionMaxAttempts = 4
	r = t8RegisteredCall(t, inst, ctx, "update_plan", map[string]any{"plan_id": p.ID, "bounds": map[string]any{"supervision_max_attempts": 4}})
	require.False(t, r.IsError, "3.7 new live ceiling 4 accepts 4: %s", r.ForLLM)
	got, err = ps.Get(p.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Bounds)
	require.NotNil(t, got.Bounds.SupervisionMaxAttempts)
	assert.Equal(t, 4, *got.Bounds.SupervisionMaxAttempts)
	require.NotNil(t, got.Bounds.IdleExpiryDays)
	assert.Equal(t, 5, *got.Bounds.IdleExpiryDays, "partial live-cap edit preserves the other bound")
	before = got
	r = t8RegisteredCall(t, inst, ctx, "update_plan", map[string]any{"plan_id": p.ID, "title": "must still not save", "bounds": map[string]any{"supervision_max_attempts": 5}})
	require.True(t, r.IsError, "3.6 new live ceiling 4 refuses 5")
	assert.Equal(t, "supervision_max_attempts 5 is above the global limit of 4. Use 4 or less.", r.ForLLM)
	got, err = ps.Get(p.ID)
	require.NoError(t, err)
	assert.Equal(t, before, got, "the above-live-cap mixed edit applies nothing")
}
