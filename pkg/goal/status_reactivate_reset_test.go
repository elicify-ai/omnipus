// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// status_reactivate_reset_test.go is review finding 11's oracle: Reactivate
// must reset EVERY per-run counter and clear the PRIOR run's projected
// per-criterion statuses, not only the two counters R-04's sentence happens
// to name.
//
// Why this is its own file rather than another case in status_test.go: the
// existing TestReactivateTaskOwnedGoal asserts exactly the four fields R-04
// enumerates and passes with the defect fully present. A test that passes
// whether or not the keeper's own budgets reset is not a weaker version of
// this one — it is testing a different, smaller claim. Both are kept.
package goal

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// spendFirstRun drives g through a realistic FIRST run and leaves it
// terminal: activated, both keeper budgets spent, a verdict recorded and its
// per-criterion outcomes projected onto both ladders. This is the state a
// task's goal record is genuinely in when the task is re-run, reproduced from
// the same public API production uses.
func spendFirstRun(t *testing.T, g *Goal, now time.Time) {
	t.Helper()
	if err := g.Activate("run-1-session", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// The keeper's own two durable per-run budgets, spent to their bounds.
	// goalZeroOutputPushMax is 2 in pkg/agent; 2 is therefore "fully spent".
	g.ZeroOutputPushes = 2
	g.QuestionRoundsUsed = 3

	v := &task.JudgeVerdict{ID: "v1", Scope: task.VerdictScopeGoal, Met: false}
	if err := g.RecordVerdict(v, "one criterion still unmet", now); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}
	g.RecordAttempt(now)

	// The GOAL-FR-036 verdict->criterion projection's output, as the engine
	// writes it back (pkg/agent/goal_triggers.go's runGoalAdjudication).
	for i := range g.Criteria {
		g.Criteria[i].Status = task.CritUnmet
	}
	for i := range g.DoD {
		g.DoD[i].Status = task.CritMet
	}

	if err := g.Terminate(generated.GoalStateExhausted, "round budget exhausted", now.Add(time.Minute)); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
}

// TestReactivateResetsKeeperBudgets is the first half of review finding 11.
//
// Given a task-owned goal whose FIRST run spent both keeper budgets
// When the task is re-run and its goal record re-enters the active phase
// Then both budgets are back at zero.
//
// The defect this closes: Reactivate reset AttemptsUsed, Round, LatestReason,
// TerminalReason and LatestVerdict and left ZeroOutputPushes and
// QuestionRoundsUsed at whatever the previous run had spent. Both bounds are
// re-read from the STORE on every keeper tick, so a task whose first run used
// its two continue-pushes came back with the push bound already tripped: the
// keeper ladder was dead for that task's every later run, silently — a quiet
// re-run would never be pushed again and nothing would say why.
func TestReactivateResetsKeeperBudgets(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindTask, "task-1")
	now := time.Now().UTC()
	spendFirstRun(t, g, now)

	if err := g.Reactivate("run-2-session", now.Add(time.Hour)); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}

	if g.ZeroOutputPushes != 0 {
		t.Errorf("ZeroOutputPushes = %d after re-entry, want 0 — the bounded continue-push ladder "+
			"(GOAL-FR-017) re-reads this counter from the store on every keeper tick, so a value "+
			"carried over from the PRIOR run leaves the ladder permanently spent and this task's "+
			"every later run un-pushable (review finding 11)", g.ZeroOutputPushes)
	}
	if g.QuestionRoundsUsed != 0 {
		t.Errorf("QuestionRoundsUsed = %d after re-entry, want 0 — the forced-question budget is a "+
			"per-RUN budget on a record that outlives the run (review finding 11)", g.QuestionRoundsUsed)
	}
}

// TestReactivateClearsProjectedCriterionStatuses is the second half of review
// finding 11.
//
// Given a task-owned goal whose FIRST run's verdict was projected onto its
// criteria and definition-of-done
// When the task is re-run
// Then every criterion and DoD item is back at pending, while its text, id
// and authorship are untouched.
//
// The defect this closes: a fresh run started with criteria still painted met
// or unmet from the previous run's verdict — a judgement the user sees on the
// goal card that was never rendered against this run's work at all.
func TestReactivateClearsProjectedCriterionStatuses(t *testing.T) {
	g := newTestGoal(t, generated.GoalOwnerKindTask, "task-1")
	now := time.Now().UTC()

	// Capture identity BEFORE the run so the reset can be shown to touch the
	// status and nothing else.
	wantCriterionText := g.Criteria[0].Text
	wantCriterionID := g.Criteria[0].ID
	wantDoDText := g.DoD[0].Text

	spendFirstRun(t, g, now)

	// Precondition: the first run really did paint them (guards against a
	// vacuous pass if spendFirstRun ever stops projecting).
	if g.Criteria[0].Status != task.CritUnmet {
		t.Fatalf("arrange: Criteria[0].Status = %q, want %q before re-entry", g.Criteria[0].Status, task.CritUnmet)
	}
	if g.DoD[0].Status != task.CritMet {
		t.Fatalf("arrange: DoD[0].Status = %q, want %q before re-entry", g.DoD[0].Status, task.CritMet)
	}

	if err := g.Reactivate("run-2-session", now.Add(time.Hour)); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}

	for i, c := range g.Criteria {
		if c.Status != task.CritPending {
			t.Errorf("Criteria[%d].Status = %q after re-entry, want %q — a re-run must not open with the "+
				"PRIOR run's verdict already painted on its ladder (review finding 11)", i, c.Status, task.CritPending)
		}
	}
	for i, c := range g.DoD {
		if c.Status != task.CritPending {
			t.Errorf("DoD[%d].Status = %q after re-entry, want %q (review finding 11)", i, c.Status, task.CritPending)
		}
	}

	// The reset must clear the OUTCOME only: a re-run judges the same ladder.
	if g.Criteria[0].Text != wantCriterionText || g.Criteria[0].ID != wantCriterionID {
		t.Errorf("Criteria[0] identity changed across re-entry: id/text = %q/%q, want %q/%q — "+
			"Reactivate must clear judgements, never rewrite the criteria themselves",
			g.Criteria[0].ID, g.Criteria[0].Text, wantCriterionID, wantCriterionText)
	}
	if g.DoD[0].Text != wantDoDText {
		t.Errorf("DoD[0].Text changed across re-entry: %q, want %q", g.DoD[0].Text, wantDoDText)
	}

	// The prior run's judgement is not LOST, it is relocated: TerminalHistory
	// retains the verdict it came from. This is what makes clearing the
	// projection safe rather than destructive.
	if len(g.TerminalHistory) != 1 || g.TerminalHistory[0].Verdict == nil {
		t.Fatalf("TerminalHistory must retain the prior run's verdict (entries=%d) — clearing the "+
			"projected statuses is only correct because the outcome survives here",
			len(g.TerminalHistory))
	}
}
