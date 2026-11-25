// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"reflect"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// I-2 / S-111: an agent's edit of a task's prompt is synchronised into the
// paired goal's prompt, which a running task session's goal wakes read; the
// edit's provenance join (the task write's Patch.JoinProvenance) lands on the
// goal in the same goal write. A person's edit (nil) joins nothing.
func TestSyncTaskGoalPrompt_JoinsTheTaskWritesProvenance(t *testing.T) {
	telegram := []string{"connector:telegram"}
	unresolved := []string{}
	rows := []struct {
		name string
		join *[]string
		want []string
	}{
		{"agent edit from a Telegram turn joins", &telegram, []string{"connector:telegram", "web"}},
		{"agent edit from an unresolved turn makes the goal unresolved", &unresolved, nil},
		{"person's edit joins nothing", nil, []string{"web"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			gs := goal.NewStore(t.TempDir())
			criterion := task.AcceptanceCriterion{
				Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the report exists",
				Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "s111"}, Status: task.CritPending,
			}
			g, err := goal.New(generated.GoalOwnerKindTask, "task-s111", generated.GoalSourceTaskExplicit,
				"old prompt", "", []task.AcceptanceCriterion{criterion}, []task.AcceptanceCriterion{criterion}, 20, time.Now().UTC())
			if err != nil {
				t.Fatalf("goal.New: %v", err)
			}
			g.Provenance = []string{"web"}
			if cerr := gs.Create(g); cerr != nil {
				t.Fatalf("Create: %v", cerr)
			}
			edited := &task.Task{ID: "task-s111", Title: "S-111 task", Prompt: "new prompt from the edit"}
			if serr := SyncTaskGoalPrompt(gs, edited, row.join); serr != nil {
				t.Fatalf("SyncTaskGoalPrompt: %v", serr)
			}
			got, err := gs.Get(g.GoalID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Prompt != "new prompt from the edit" {
				t.Fatalf("instrument: the goal prompt was not synchronised (%q)", got.Prompt)
			}
			if !reflect.DeepEqual(got.Provenance, row.want) {
				t.Fatalf("goal provenance after the prompt sync = %v, want %v", got.Provenance, row.want)
			}
		})
	}
}
