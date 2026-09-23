// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_engine_supervise_judge_context_test.go — a plan carries no acceptance
// criteria of its own; its implicit criterion is that EVERY member task's own
// acceptance criteria are met, alongside the plan's own Definition of Done.
// buildPlanJudgeExtraContext previously showed the plan judge the title,
// objective and description only — never that implicit criterion, and never
// any evidence of it (each member task's own goal-record verdict). A task
// marked `done` whose own goal was never adjudicated is exactly the case this
// closes: the judge must see "never adjudicated", not assume MET from
// task.Status alone.

import (
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedPlanMemberGoal creates the goal record paired with taskID, sets it
// straight to the given terminal (or non-terminal) state and reason — same
// shortcut task_goal_terminal_test.go's seedTaskGoal/activateSeededTaskGoal
// pair takes, minus the Activate/Terminate ceremony this test does not need.
func seedPlanMemberGoal(t *testing.T, gs *goal.Store, taskID string, state generated.GoalState, reason string) {
	t.Helper()
	g, err := goal.New(generated.GoalOwnerKindTask, taskID, generated.GoalSourceTaskExplicit,
		"member task goal", "",
		[]task.AcceptanceCriterion{proseCriterion("", "the member task's own criterion")},
		[]task.AcceptanceCriterion{proseCriterion("", "the member task's own DoD")},
		config.DefaultGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("seedPlanMemberGoal: goal.New: %v", err)
	}
	g.State = state
	g.TerminalReason = reason
	if err := gs.Create(g); err != nil {
		t.Fatalf("seedPlanMemberGoal: Create: %v", err)
	}
}

func TestBuildPlanJudgeExtraContext_ImplicitCriterionSentence_AlwaysPresent(t *testing.T) {
	t.Parallel()
	gs := goal.NewStore(t.TempDir())
	p := &plan.Plan{Title: "T", Objective: "ship it"}

	for _, tasks := range [][]task.Task{
		nil,
		{{ID: "t1", Title: "Task 1", Status: task.StatusDone}},
	} {
		ctx := buildPlanJudgeExtraContext(p, tasks, gs)
		if !strings.Contains(ctx, "no acceptance criteria of its own") ||
			!strings.Contains(ctx, "member task") {
			t.Errorf("context for %d task(s) is missing the implicit-criterion sentence:\n%s", len(tasks), ctx)
		}
	}
}

func TestBuildPlanJudgeExtraContext_AllMembersMet_NamesEachOne(t *testing.T) {
	t.Parallel()
	gs := goal.NewStore(t.TempDir())
	seedPlanMemberGoal(t, gs, "t1", generated.GoalStateMet, "")
	seedPlanMemberGoal(t, gs, "t2", generated.GoalStateMet, "")

	tasks := []task.Task{
		{ID: "t1", Title: "Write the schema", Status: task.StatusDone},
		{ID: "t2", Title: "Write the client", Status: task.StatusDone},
	}
	p := &plan.Plan{Title: "T"}

	ctx := buildPlanJudgeExtraContext(p, tasks, gs)

	if !strings.Contains(ctx, "2 met, 0 unmet, 0 never adjudicated") {
		t.Errorf("context does not state the all-met tally:\n%s", ctx)
	}
	for _, want := range []string{"Write the schema", "Write the client"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("context does not name member task %q:\n%s", want, ctx)
		}
	}
	if !strings.Contains(ctx, "MET") {
		t.Errorf("context does not mark the members MET:\n%s", ctx)
	}
}

func TestBuildPlanJudgeExtraContext_OneUnmetMember_ShowsItAndItsReason(t *testing.T) {
	t.Parallel()
	gs := goal.NewStore(t.TempDir())
	seedPlanMemberGoal(t, gs, "t1", generated.GoalStateMet, "")
	seedPlanMemberGoal(t, gs, "t2", generated.GoalStateExhausted,
		"owning task reached failed: the CSV export never matched the schema")

	tasks := []task.Task{
		{ID: "t1", Title: "Write the schema", Status: task.StatusDone},
		{ID: "t2", Title: "Write the client", Status: task.StatusFailed},
	}
	p := &plan.Plan{Title: "T"}

	ctx := buildPlanJudgeExtraContext(p, tasks, gs)

	if !strings.Contains(ctx, "1 met, 1 unmet, 0 never adjudicated") {
		t.Errorf("context does not state the 1-unmet tally:\n%s", ctx)
	}
	if !strings.Contains(ctx, "Write the client") || !strings.Contains(ctx, "UNMET") {
		t.Errorf("context does not show the unmet member:\n%s", ctx)
	}
	if !strings.Contains(ctx, "the CSV export never matched the schema") {
		t.Errorf("context does not carry the unmet member's own reason:\n%s", ctx)
	}
}

func TestBuildPlanJudgeExtraContext_NeverAdjudicatedMember_NotSilentlyCountedMet(t *testing.T) {
	t.Parallel()
	gs := goal.NewStore(t.TempDir())
	seedPlanMemberGoal(t, gs, "t1", generated.GoalStateMet, "")
	// t2 has NO paired goal record at all — the exact shape of a task marked
	// done whose own goal was never adjudicated.

	tasks := []task.Task{
		{ID: "t1", Title: "Write the schema", Status: task.StatusDone},
		{ID: "t2", Title: "Write the client", Status: task.StatusDone},
	}
	p := &plan.Plan{Title: "T"}

	ctx := buildPlanJudgeExtraContext(p, tasks, gs)

	if !strings.Contains(ctx, "1 met, 0 unmet, 1 never adjudicated") {
		t.Errorf("context does not state the never-adjudicated tally:\n%s", ctx)
	}
	if !strings.Contains(ctx, "Write the client") || !strings.Contains(ctx, "never adjudicated") {
		t.Errorf("context does not show t2 as never adjudicated:\n%s", ctx)
	}
	// The critical negative: t2's line must not itself read as met just
	// because task.Status is done.
	for _, line := range strings.Split(ctx, "\n") {
		if strings.Contains(line, "Write the client") && strings.Contains(line, "MET") &&
			!strings.Contains(line, "never adjudicated") {
			t.Errorf("t2 (never adjudicated) was rendered as if MET: %q", line)
		}
	}
}

func TestBuildPlanJudgeExtraContext_LongMemberList_SummarisesAndListsFailuresInFull(t *testing.T) {
	t.Parallel()
	gs := goal.NewStore(t.TempDir())

	var tasks []task.Task
	const metCount = 25
	for i := 0; i < metCount; i++ {
		id := "met-" + string(rune('a'+i))
		seedPlanMemberGoal(t, gs, id, generated.GoalStateMet, "")
		tasks = append(tasks, task.Task{ID: id, Title: "Met task " + id, Status: task.StatusDone})
	}
	seedPlanMemberGoal(t, gs, "unmet-1", generated.GoalStateExhausted, "ran out of rounds")
	tasks = append(tasks, task.Task{ID: "unmet-1", Title: "The one that failed", Status: task.StatusFailed})

	p := &plan.Plan{Title: "T"}
	ctx := buildPlanJudgeExtraContext(p, tasks, gs)

	if !strings.Contains(ctx, "25 met, 1 unmet, 0 never adjudicated") {
		t.Errorf("long-list context does not state the tally:\n%s", ctx)
	}
	if !strings.Contains(ctx, "The one that failed") {
		t.Errorf("long-list context must list the unmet member in full:\n%s", ctx)
	}
	if strings.Contains(ctx, "Met task met-a") {
		t.Errorf("long-list context must NOT enumerate every met member — it should summarise them:\n%s", ctx)
	}
}
