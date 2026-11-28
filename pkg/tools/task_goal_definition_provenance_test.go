// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"reflect"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// I-2 / S-111: criteria and DoD are instructions. An agent's update_task that
// changes either joins its turn's provenance into the task record and into the
// paired goal record (whose provenance a running task session's goal wakes
// read), in the same reconciled write. An unresolved turn joins an empty set
// and makes both unresolved (fail closed).
func TestUpdateTask_CriteriaOrDoDEditJoinsTheTurnsProvenance(t *testing.T) {
	telegram := bus.NewProvenance(bus.ConnectorOrigin("telegram"))
	rows := []struct {
		name   string
		args   map[string]any
		writer bus.Provenance
		want   []string
	}{
		{"criteria edit from a Telegram turn", map[string]any{"criteria": []any{map[string]any{"kind": "prose", "text": "the report is filed"}}}, telegram, []string{"connector:telegram", "web"}},
		{"dod edit from a Telegram turn", map[string]any{"dod": []any{map[string]any{"kind": "prose", "text": "the report is reviewed"}}}, telegram, []string{"connector:telegram", "web"}},
		{"criteria edit from an unresolved turn", map[string]any{"criteria": []any{map[string]any{"kind": "prose", "text": "the report is filed"}}}, bus.UnresolvedProvenance(), nil},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := task.New(t.TempDir())
			ctx := WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1")
			// Given: a web turn creates the task, so the task records {web}.
			webTurn := NewTurnOrigin()
			webTurn.AddProvenance(bus.NewProvenance(bus.OriginWeb))
			created := newProvenanceTool(t, store).Execute(WithTurnOrigin(ctx, webTurn), map[string]any{
				"title": "S-111 task", "prompt": "do the work", "agent_id": "jim",
				"criteria": validCriteriaArg(), "dod": validDoDArg(),
			})
			if created.IsError {
				t.Fatalf("create_task failed: %s", created.ForLLM)
			}
			rows, err := store.List(task.Filter{WorkspaceID: "ws-1"})
			if err != nil || len(rows) != 1 {
				t.Fatalf("instrument: want one created task, got %d (err %v)", len(rows), err)
			}
			taskID := rows[0].ID
			gs := GoalStoreForTasks(store)
			paired, err := gs.GetByOwner(generated.GoalOwnerKindTask, taskID)
			if err != nil {
				t.Fatalf("instrument: create_task paired no goal: %v", err)
			}
			if want := []string{"web"}; !reflect.DeepEqual(rows[0].Provenance, want) {
				t.Fatalf("instrument: the created task records %v, want %v", rows[0].Provenance, want)
			}
			// And the paired goal records {web} too.
			if _, serr := gs.Update(paired.GoalID, goal.Write{Mutate: func(g *goal.Goal) error {
				g.Provenance = []string{"web"}
				return nil
			}}); serr != nil {
				t.Fatalf("seed goal provenance: %v", serr)
			}

			// When: the agent edits criteria/DoD from the row's turn.
			origin := NewTurnOrigin()
			origin.AddProvenance(row.writer)
			args := map[string]any{"task_id": taskID}
			for k, v := range row.args {
				args[k] = v
			}
			res := NewTaskUpdateTool(store).Execute(WithTurnOrigin(ctx, origin), args)
			if res.IsError {
				t.Fatalf("update_task failed: %s", res.ForLLM)
			}

			// Then: the task record and its paired goal carry the join.
			gotTask, err := store.Get(taskID)
			if err != nil {
				t.Fatalf("Get task: %v", err)
			}
			if !reflect.DeepEqual(gotTask.Provenance, row.want) {
				t.Fatalf("task provenance after the edit = %v, want %v", gotTask.Provenance, row.want)
			}
			gotGoal, err := gs.Get(paired.GoalID)
			if err != nil {
				t.Fatalf("Get goal: %v", err)
			}
			if !reflect.DeepEqual(gotGoal.Provenance, row.want) {
				t.Fatalf("paired goal provenance after the edit = %v, want %v", gotGoal.Provenance, row.want)
			}
		})
	}
}

// SyncTaskGoalRecordJoining's two other partitions: a person's edit (nil
// join) leaves an existing goal's provenance unchanged, and an agent's first
// criteria+DoD on a legacy task with no paired goal creates the goal with the
// edit's provenance.
func TestSyncTaskGoalRecordJoining_PersonEditAndLegacyCreate(t *testing.T) {
	dod := []task.AcceptanceCriterion{{
		Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the report is reviewed",
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "s111"}, Status: task.CritPending,
	}}
	edited := &task.Task{ID: "task-s111-sync", Title: "S-111 task", Prompt: "do the work", Criteria: dod}

	t.Run("legacy task: the agent's first criteria and DoD create the goal with the edit's provenance", func(t *testing.T) {
		gs := goal.NewStore(t.TempDir())
		telegram := []string{"connector:telegram"}
		if err := SyncTaskGoalRecordJoining(gs, edited, true, dod, true, nil, &telegram); err != nil {
			t.Fatalf("SyncTaskGoalRecordJoining: %v", err)
		}
		g, err := gs.GetByOwner(generated.GoalOwnerKindTask, edited.ID)
		if err != nil {
			t.Fatalf("instrument: no goal was created: %v", err)
		}
		if !reflect.DeepEqual(g.Provenance, telegram) {
			t.Fatalf("created goal provenance = %v, want %v", g.Provenance, telegram)
		}
	})

	t.Run("person's edit joins nothing", func(t *testing.T) {
		gs := goal.NewStore(t.TempDir())
		if err := SyncTaskGoalRecordJoining(gs, edited, true, dod, true, nil, nil); err != nil {
			t.Fatalf("create the paired goal: %v", err)
		}
		g, err := gs.GetByOwner(generated.GoalOwnerKindTask, edited.ID)
		if err != nil {
			t.Fatalf("GetByOwner: %v", err)
		}
		if _, serr := gs.Update(g.GoalID, goal.Write{Mutate: func(cur *goal.Goal) error {
			cur.Provenance = []string{"web"}
			return nil
		}}); serr != nil {
			t.Fatalf("seed goal provenance: %v", serr)
		}
		if perr := SyncTaskGoalRecord(gs, edited, false, dod, true, nil); perr != nil {
			t.Fatalf("SyncTaskGoalRecord (person): %v", perr)
		}
		got, err := gs.Get(g.GoalID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if want := []string{"web"}; !reflect.DeepEqual(got.Provenance, want) {
			t.Fatalf("goal provenance after a person's edit = %v, want unchanged %v", got.Provenance, want)
		}
	})
}
