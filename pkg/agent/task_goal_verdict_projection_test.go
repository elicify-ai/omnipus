// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_goal_verdict_projection_test.go is UAT defect 3's oracle: a task-owned
// goal record's OWN criteria[] mirror must carry the verdict's per-criterion
// outcome (ADR-086 D8, GOAL-FR-036/FR-040/FR-041). UAT record eed50f19 showed
// criterion 2b9872ae as `pending` on the goal record while its verdict said
// met:true and the task's REST view said `met`: adjudicateClaim projected the
// criteria half onto the task record only, and the goal record received the
// DoD half alone. recordTaskGoalVerdict now projects both of the record's own
// lists in the same store write that records the verdict.
package agent

import (
	"context"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestTaskVerdictProjectsOntoTheGoalRecordsOwnCriteria_UATDefect3(t *testing.T) {
	cases := []struct {
		name           string
		unmetTexts     []string
		wantCritOnBoth task.CriterionStatus
		wantDoD        task.CriterionStatus
	}{
		{name: "all_met", wantCritOnBoth: task.CritMet, wantDoD: task.CritMet},
		// The UAT shape: the criterion met, the DoD not. The verdict is unmet
		// overall and the task goes back to `next`, but the per-criterion
		// outcomes are still facts the record must show (FR-040 is not
		// conditioned on the overall verdict).
		{name: "criterion_met_dod_unmet", unmetTexts: []string{dodTestDoDText}, wantCritOnBoth: task.CritMet, wantDoD: task.CritUnmet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			unmet := map[string]bool{}
			for _, txt := range tc.unmetTexts {
				unmet[txt] = true
			}
			judgeInst.Provider = &perCriterionJudgeProvider{unmet: unmet}

			stored := mustCreateInProgressTask(t, al, &task.Task{
				AgentID: "native-agent", WorkspaceID: "test-ws",
				Title:    "ship the CSV export",
				Prompt:   "make the export endpoint return CSV",
				Criteria: []task.AcceptanceCriterion{proseCriterion("", dodTestCriterionText)},
			})
			_, taskSessionID := newGoalTestSession(t, al, "native-agent")
			// Production pairing (rest_tasks.go / tools/task.go
			// syncTaskGoalRecord): the goal record's criteria are the task's
			// STORED criteria, ids included.
			seedActiveTaskGoalWithDoD(t, stored.ID, taskSessionID, stored.Prompt,
				stored.Criteria, []task.AcceptanceCriterion{proseCriterion("", dodTestDoDText)})
			criterionID := stored.Criteria[0].ID

			al.taskExecutor.adjudicateClaim(context.Background(), stored, taskSessionID,
				"Implemented the CSV export and exercised the endpoint by hand.", nil)

			rec, err := goal.NewStore(config.OmnipusHomeDir()).GetByOwner(generated.GoalOwnerKindTask, stored.ID)
			if err != nil {
				t.Fatalf("read paired goal record: %v", err)
			}
			if rec.LatestVerdict == nil {
				t.Fatal("precondition: the goal record carries no verdict, so this row is not exercising the projection")
			}
			var verdictSaysMet, found bool
			for _, pc := range rec.LatestVerdict.PerCriterion {
				if pc.CriterionID == criterionID {
					verdictSaysMet, found = pc.Met, true
				}
			}
			if !found || !verdictSaysMet {
				t.Fatalf("precondition: the verdict must judge criterion %s met (found=%v met=%v)", criterionID, found, verdictSaysMet)
			}

			if len(rec.Criteria) != 1 || rec.Criteria[0].ID != criterionID {
				t.Fatalf("goal record criteria = %+v, want the task's criterion %s", rec.Criteria, criterionID)
			}
			if rec.Criteria[0].Status != tc.wantCritOnBoth {
				t.Errorf("goal record criteria[0].status = %q, want %q: the record's own mirror must reflect "+
					"latest_verdict.per_criterion (D8), not stay pending while the verdict says met", rec.Criteria[0].Status, tc.wantCritOnBoth)
			}
			if len(rec.DoD) != 1 || rec.DoD[0].Status != tc.wantDoD {
				t.Errorf("goal record dod = %+v, want one item with status %q", rec.DoD, tc.wantDoD)
			}

			final, err := GetTaskStore(al).Get(stored.ID)
			if err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if len(final.Criteria) != 1 || final.Criteria[0].Status != tc.wantCritOnBoth {
				t.Errorf("task criteria = %+v, want status %q: the task view and the goal record must agree", final.Criteria, tc.wantCritOnBoth)
			}
		})
	}
}
