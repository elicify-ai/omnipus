package tools

import (
	"strings"
	"testing"
)

// A goal always has both acceptance criteria and a definition of done
// (founder decision, ADR-091 wp-tooldesc). parseDelegateGoal takes the two
// TOP-LEVEL "criteria"/"dod" arguments delegate now publishes (mirroring
// create_task's flat shape) and must refuse a criteria-only or dod-only
// call cleanly, rather than let it reach pkg/goal.Goal.Validate's deep
// "dod must contain at least one item" failure.

func validDelegateCriterion(text string) map[string]any {
	return map[string]any{"text": text}
}

func TestParseDelegateGoal_BothSupplied_GoalCreated(t *testing.T) {
	criteria := []any{validDelegateCriterion("The checkout defect is identified")}
	dod := []any{validDelegateCriterion("Focused tests pass")}

	goal, err := parseDelegateGoal(criteria, dod)
	if err != nil {
		t.Fatalf("parseDelegateGoal(criteria, dod) = %v, want no error", err)
	}
	if goal == nil {
		t.Fatal("parseDelegateGoal(criteria, dod) = nil goal, want a goal with both lists")
	}
	if len(goal.Criteria) != 1 || goal.Criteria[0].Text != "The checkout defect is identified" {
		t.Errorf("Criteria = %+v, want one item with the supplied text", goal.Criteria)
	}
	if len(goal.DoD) != 1 || goal.DoD[0].Text != "Focused tests pass" {
		t.Errorf("DoD = %+v, want one item with the supplied text", goal.DoD)
	}
}

func TestParseDelegateGoal_NeitherSupplied_NoGoal(t *testing.T) {
	goal, err := parseDelegateGoal(nil, nil)
	if err != nil {
		t.Fatalf("parseDelegateGoal(nil, nil) = %v, want no error — no goal is the default", err)
	}
	if goal != nil {
		t.Fatalf("parseDelegateGoal(nil, nil) = %+v, want nil (no goal)", goal)
	}
}

func TestParseDelegateGoal_CriteriaOnly_Refused(t *testing.T) {
	criteria := []any{validDelegateCriterion("The checkout defect is identified")}

	goal, err := parseDelegateGoal(criteria, nil)
	if err == nil {
		t.Fatalf("parseDelegateGoal(criteria, nil) = %+v, nil error; want a refusal — a goal needs both", goal)
	}
	if goal != nil {
		t.Errorf("parseDelegateGoal(criteria, nil) returned a goal %+v alongside an error", goal)
	}
	if !strings.Contains(err.Error(), "dod") {
		t.Errorf("error = %q, want it to name the missing dod", err.Error())
	}
}

func TestParseDelegateGoal_DoDOnly_Refused(t *testing.T) {
	dod := []any{validDelegateCriterion("Focused tests pass")}

	goal, err := parseDelegateGoal(nil, dod)
	if err == nil {
		t.Fatalf("parseDelegateGoal(nil, dod) = %+v, nil error; want a refusal — a goal needs both", goal)
	}
	if goal != nil {
		t.Errorf("parseDelegateGoal(nil, dod) returned a goal %+v alongside an error", goal)
	}
	if !strings.Contains(err.Error(), "criteria") {
		t.Errorf("error = %q, want it to name the missing criteria", err.Error())
	}
}
