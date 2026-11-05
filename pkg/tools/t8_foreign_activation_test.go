package tools

// Oracle: r17 §7.7/§7.11, D-2/D-9 and scenarios 18.2, 18.15–18.18,
// 18.23. Real tool/store/workspace records. These do not claim the full
// scheduler/drain/session integration; that remains a distinct obligation.
import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskTrigger_ForeignActivationToolBoundary(t *testing.T) {
	const triggerRefusal = "This task runs in another workspace: only its assignee there or a person can set its trigger. Nothing was changed."
	const statusRefusal = "This task runs in another workspace: only its assignee there or a person can make it ready to run. Nothing was changed."
	for _, tc := range []struct {
		id          string
		status      bool
		edgeRemoved bool
	}{
		{"18.15 foreign trigger", false, false}, {"18.16 foreign next", true, false}, {"18.17 delegation edge removed", false, true},
	} {
		t.Run(tc.id, func(t *testing.T) {
			store, _ := parityStorePair(t)
			tk := parityTask(t, store, "agent-a", "agent-b", "ws-b", task.StatusInbox, false)
			before, err := json.Marshal(tk)
			require.NoError(t, err)
			tool := NewTaskUpdateTool(store)
			if tc.edgeRemoved {
				tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial {
					return &DelegationDenial{Reason: "policy: no delegation edge"}
				})
			}
			args := map[string]any{"task_id": tk.ID, "trigger": triggerArg("once", map[string]any{"at_ms": time.Now().Add(7 * 24 * time.Hour).UnixMilli()})}
			want := triggerRefusal
			if tc.status {
				delete(args, "trigger")
				args["status"] = "next"
				want = statusRefusal
			}
			r := tool.Execute(parityCtx("agent-a"), args)
			require.True(t, r.IsError, "foreign activation must be refused: %s", r.ForLLM)
			assert.Equal(t, want, r.ForLLM)
			got, err := store.Get(tk.ID)
			require.NoError(t, err)
			after, err := json.Marshal(got)
			require.NoError(t, err)
			assert.JSONEq(t, string(before), string(after), "nothing changed, including engine metadata")
		})
	}
	t.Run("18.18 receiver-local attachment positive control", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-b", task.StatusInbox, false)
		at := time.Now().Add(7 * 24 * time.Hour).UnixMilli()
		ctx := WithAgentID(WithWorkspaceID(context.Background(), "ws-b"), "agent-b")
		r := NewTaskUpdateTool(store).Execute(ctx, map[string]any{"task_id": tk.ID, "trigger": triggerArg("once", map[string]any{"at_ms": at})})
		require.False(t, r.IsError, "destination-local assignment can be scheduled: %s", r.ForLLM)
		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		require.NotNil(t, got.Trigger)
		assert.Equal(t, task.TriggerOnce, got.Trigger.Type)
		require.NotNil(t, got.Trigger.Config.AtMs)
		assert.Equal(t, at, *got.Trigger.Config.AtMs)
		assert.Equal(t, task.StatusInbox, got.Status)
	})
	t.Run("18.23 authorised foreign manual clear", func(t *testing.T) {
		store, _ := parityStorePair(t)
		tk := parityTask(t, store, "agent-a", "agent-b", "ws-b", task.StatusInbox, false)
		at := time.Now().Add(7 * 24 * time.Hour).UnixMilli()
		tr := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
		_, err := store.Update(tk.ID, task.Patch{Trigger: &tr})
		require.NoError(t, err)
		r := NewTaskUpdateTool(store).Execute(parityCtx("agent-a"), map[string]any{"task_id": tk.ID, "trigger": triggerArg("manual", map[string]any{})})
		require.False(t, r.IsError, "a clear is not activation: %s", r.ForLLM)
		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		assert.Equal(t, task.StatusInbox, got.Status)
		if got.Trigger != nil {
			assert.Equal(t, task.TriggerManual, got.Trigger.Type)
		}
	})
}
func TestTaskTrigger_ForeignCreateRefusalAndManualDelivery(t *testing.T) {
	home := t.TempDir()
	for _, w := range []workspace.Workspace{{ID: "ws-a", Name: "A", Status: "active", CoreTeam: []string{"agent-a"}}, {ID: "ws-b", Name: "B", Status: "active", CoreTeam: []string{"agent-b"}}} {
		require.NoError(t, workspace.SaveRecord(home, w))
	}
	for _, scheduled := range []bool{false, true} {
		name := "manual delivery control"
		if scheduled {
			name = "18.2 foreign scheduled create"
		}
		t.Run(name, func(t *testing.T) {
			store, _ := parityStorePair(t)
			tool := parityCreateTool(store)
			tool.SetHome(home)
			args := map[string]any{"title": "foreign task", "prompt": "receiver-local work", "agent_id": "agent-b", "workspace_id": "ws-b", "criteria": validCriteriaArg(), "dod": validDoDArg()}
			if scheduled {
				args["trigger"] = triggerArg("once", map[string]any{"at_ms": time.Now().Add(7 * 24 * time.Hour).UnixMilli()})
			}
			r := tool.Execute(parityCtx("agent-a"), args)
			if scheduled {
				require.True(t, r.IsError, "foreign create with trigger is refused: %s", r.ForLLM)
				assert.Equal(t, "Tasks for agents in another workspace start only when their team picks them up there: create it without a trigger, or ask the assignee (or a person) to add the schedule after pickup. Nothing was created.", r.ForLLM)
				assert.Nil(t, parityFindTaskByTitle(t, store, "foreign task"))
				return
			}
			require.False(t, r.IsError, "manual positive control reaches real destination membership: %s", r.ForLLM)
			got := parityFindTaskByTitle(t, store, "foreign task")
			require.NotNil(t, got)
			assert.Equal(t, "ws-b", got.WorkspaceID)
			assert.Equal(t, task.StatusInbox, got.Status)
			assert.Nil(t, got.Trigger)
		})
	}
}
