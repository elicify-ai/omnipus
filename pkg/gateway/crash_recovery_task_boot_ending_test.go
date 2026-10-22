//go:build goolm && stdjson

package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// DESIGN Decision 1 + TASK-ENDING-ARCHITECT: a bound execution already ended
// Interrupted is not a crash-failed task. The real gateway boot reconciler must
// keep its task, active goal and open run through repeated boots.
func TestCrashRecovery_InterruptedTaskIsNotCrashFailedByTaskBootReconciliation(t *testing.T) {
	for _, reason := range []string{"tool_limit", "reconciliation_required"} {
		t.Run(reason, func(t *testing.T) {
			if !session.IsValidLifecycleState(session.LifecycleState("interrupted")) {
				t.Fatal("BLOCKED: durable task Interrupted ending not implemented — required by DESIGN Q14/Q15 and task-ending architecture ruling")
			}
			api := newTestRestAPIAlignedStores(t)
			ws := ensureTestWorkspace(t, api)
			setWorkspaceCoreTeam(t, api, ws, []string{"mia"})
			tk := createTaskViaAPI(t, api, "execution ended but task remains", ws)
			next := task.StatusNext
			description := "ready"
			_, err := api.taskStore.Update(tk.Id, task.Patch{Status: &next, Description: &description})
			require.NoError(t, err)
			meta, err := api.agentLoop.GetSessionStore().NewSession(session.SessionTypeTask, "webchat", "mia")
			require.NoError(t, err)
			require.NoError(t, api.agentLoop.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{TaskID: &tk.Id, WorkspaceID: &ws}))
			progress := task.StatusInProgress
			agentID := "mia"
			_, err = api.taskStore.Update(tk.Id, task.Patch{Status: &progress, AgentID: &agentID, SessionID: &meta.ID})
			require.NoError(t, err)
			store := tools.GoalStoreForTasks(api.taskStore)
			g, err := store.GetByOwner(gen.GoalOwnerKindTask, tk.Id)
			require.NoError(t, err)
			g, err = store.Update(g.GoalID, func(value *goal.Goal) error {
				return value.Activate(meta.ID, time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC))
			})
			require.NoError(t, err)
			run, opened, err := api.taskStore.OpenRun(tk.Id, nil, task.RunKindManual, meta.ID, nil)
			require.NoError(t, err)
			require.True(t, opened)
			fields := map[string]any{"session_id": meta.ID, "generation": 1, "state": "interrupted", "interruption_reason": reason, "owner_scope_kind": "human", "workspace_id": ws, "agent_id": "mia", "goal_ref": g.GoalID, "origin": map[string]any{"kind": "task", "task_id": tk.Id}, "execution_id": map[string]any{"run_id": "ended-task-execution", "boot_seq": api.agentLoop.CurrentBootEpoch()}}
			data, err := json.Marshal(fields)
			require.NoError(t, err)
			var rec session.LifecycleRecord
			require.NoError(t, json.Unmarshal(data, &rec))
			require.NoError(t, api.agentLoop.GetSessionLifecycleStore().Persist(&rec))
			for range 2 {
				boot := session.NewBootEpochStore(api.homePath)
				_, err = boot.Mint()
				require.NoError(t, err)
				api.agentLoop.SetBootEpochStore(boot)
				api.reconcileStuckTasks()
				after, err := api.taskStore.Get(tk.Id)
				require.NoError(t, err)
				assert.Equal(t, task.StatusInProgress, after.Status)
				assert.Equal(t, meta.ID, after.SessionID)
				assert.Equal(t, 0, after.AttemptCount)
				assert.NotEqual(t, true, crashTaskLifecycleJSON(t, after)["crash_interrupted"], "an already-ended limit/hold is never fresh crash-Retry eligibility")
				runs, err := api.taskStore.ListRuns(tk.Id)
				require.NoError(t, err)
				assert.Equal(t, []task.TaskRun{*run}, runs, "boot cannot close the open run as a crash failure")
				active, err := store.Get(g.GoalID)
				require.NoError(t, err)
				assert.Equal(t, g, active, "boot does not exhaust/reset the active goal or spend its counters")
			}
		})
	}
}
