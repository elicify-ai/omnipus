// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// I-2 / S-111 (spec instruction-writer table): a task's trigger (when it runs)
// and agent_id (who runs it) are instructions. An agent's update_task that
// changes ONLY one of them joins its turn's provenance into the task in the
// same write; an unresolved turn joins an empty set (fail closed). A change to
// a non-instruction field (priority) joins nothing.
func TestUpdateTask_TriggerOrAssigneeOnlyEditJoinsTheTurnsProvenance(t *testing.T) {
	telegram := bus.NewProvenance(bus.ConnectorOrigin("telegram"))
	futureOnce := triggerArg("once", map[string]any{"at_ms": int64(4102444800000)})
	rows := []struct {
		name   string
		args   map[string]any
		writer bus.Provenance
		want   []string
	}{
		{"trigger-only edit from a Telegram turn", map[string]any{"trigger": futureOnce}, telegram, []string{"connector:telegram", "web"}},
		{"assignee-only edit from a Telegram turn", map[string]any{"agent_id": "ray"}, telegram, []string{"connector:telegram", "web"}},
		{"trigger-only edit from an unresolved turn", map[string]any{"trigger": futureOnce}, bus.UnresolvedProvenance(), nil},
		{"assignee-only edit from an unresolved turn", map[string]any{"agent_id": "ray"}, bus.UnresolvedProvenance(), nil},
		{"control: priority-only edit from a Telegram turn joins nothing", map[string]any{"priority": float64(1)}, telegram, []string{"web"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := task.New(t.TempDir())
			ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
			// Given: a web turn creates the task, so the task records {web}.
			webTurn := NewTurnOrigin()
			webTurn.AddProvenance(bus.NewProvenance(bus.OriginWeb))
			created := newProvenanceTool(t, store).Execute(WithTurnOrigin(ctx, webTurn), map[string]any{
				"title": "S-111 trigger/assignee task", "prompt": "do the work", "agent_id": "jim",
				"criteria": validCriteriaArg(), "dod": validDoDArg(),
			})
			if created.IsError {
				t.Fatalf("create_task failed: %s", created.ForLLM)
			}
			listed, err := store.List(task.Filter{WorkspaceID: "ws-1"})
			if err != nil || len(listed) != 1 {
				t.Fatalf("instrument: want one created task, got %d (err %v)", len(listed), err)
			}
			taskID := listed[0].ID
			if want := []string{"web"}; !reflect.DeepEqual(listed[0].Provenance, want) {
				t.Fatalf("instrument: the created task records %v, want %v", listed[0].Provenance, want)
			}

			// When: the agent edits only the row's field from the row's turn.
			origin := NewTurnOrigin()
			origin.AddProvenance(row.writer)
			args := map[string]any{"task_id": taskID}
			for k, v := range row.args {
				args[k] = v
			}
			update := NewTaskUpdateTool(store)
			update.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
			res := update.Execute(WithTurnOrigin(ctx, origin), args)
			if res.IsError {
				t.Fatalf("update_task failed: %s", res.ForLLM)
			}

			// Then: the field changed and the task carries the expected join.
			got, err := store.Get(taskID)
			if err != nil {
				t.Fatalf("Get task: %v", err)
			}
			if _, ok := row.args["agent_id"]; ok && got.AgentID != "ray" {
				t.Fatalf("instrument: assignee not changed, got %q", got.AgentID)
			}
			if _, ok := row.args["trigger"]; ok && (got.Trigger == nil || got.Trigger.Type != task.TriggerOnce) {
				t.Fatalf("instrument: trigger not changed, got %+v", got.Trigger)
			}
			if !reflect.DeepEqual(got.Provenance, row.want) {
				t.Fatalf("task provenance after the edit = %v, want %v", got.Provenance, row.want)
			}
		})
	}
}
