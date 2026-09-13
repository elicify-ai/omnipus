// Omnipus — set_goal ADR-084 negative assertions (JUDGE-FR-110)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// set_goal_adr084_test.go carries JUDGE-FR-110's negative assertions: no
// behaviour change to set_goal.go was needed for this requirement (R-18,
// joint delivery plan — "JUDGE-FR-110 moves from G5 to E12, with
// pkg/tools/set_goal_adr084_test.go added to E12's write-set"; the joint
// delivery plan's own W9 row: "FR-110's negative assertions only — no
// behaviour change"). This file exists to PROVE that, as a durable
// regression guard: a criterion carrying no check, no quantitative form and
// no artifact-path shape validates and persists exactly like one that does
// — set_goal's schema never required, preferred, rewarded or defaulted a
// criterion into being machine-checkable.
package tools

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestSetGoal_DoesNotRequireOrPreferACheckableCriterion is JUDGE-FR-110's
// own named oracle. set_goal.go's Parameters() schema offers criteria items
// ONLY {text, judgment} — no check/behavior input shape exists at all (see
// mergeCriterionKindFromOld's own doc comment) — so a purely subjective
// criterion (judgment: boolean, no check, no quantitative threshold, no
// artifact path token in its text) must register and persist with nothing
// distinguishing it from one an operator might imagine "should" carry a
// check. This also pins the judgment enum to exactly the three values
// FR-110 names (boolean/quantitative/artifact) — a fourth value would be
// exactly the kind of task-type-classifier surface FR-110 forbids.
func TestSetGoal_DoesNotRequireOrPreferACheckableCriterion(t *testing.T) {
	const sessionID = "session_fr110_no_checkable"
	access := newFakeGoalRecordAccess()
	access.condition[sessionID] = "an active goal"
	tool := newSetGoalTool(access)

	res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{
		"definition": "Ship a decision the team is happy with.",
		"criteria": []any{
			// Deliberately no check, no quantitative threshold, no artifact
			// path — a criterion that "cannot" be machine-verified, judged
			// on prose alone.
			map[string]any{"text": "the team feels good about the decision", "judgment": "boolean"},
		},
	})
	if res.IsError {
		t.Fatalf("a criterion with no check/quantitative/artifact shape must register normally: %s", res.ForLLM)
	}

	var rec setGoalRecord
	if err := json.Unmarshal([]byte(access.record[sessionID]), &rec); err != nil {
		t.Fatalf("written record does not parse: %v", err)
	}
	if len(rec.Criteria) != 1 {
		t.Fatalf("want exactly 1 criterion persisted, got %d", len(rec.Criteria))
	}
	c := rec.Criteria[0]
	if c.Check != nil {
		t.Fatalf("a criterion this tool authors must never carry a Check payload — set_goal's schema has no check input, got %+v", c.Check)
	}
	if c.Behavior != nil {
		t.Fatalf("a criterion this tool authors must never carry a Behavior payload — set_goal's schema has no behavior input, got %+v", c.Behavior)
	}
	if c.Judgment != task.JudgmentBoolean {
		t.Fatalf("judgment = %q, want %q — persisted exactly as submitted, no upgrade/downgrade toward a checkable form", c.Judgment, task.JudgmentBoolean)
	}
	if c.Status != task.CritPending {
		t.Fatalf("status = %q, want pending — a subjective criterion starts pending exactly like any other", c.Status)
	}

	// The judgment enum itself is exactly these three values — pinned here
	// so a fourth value (e.g. a coding-task-specific kind) trips this test
	// rather than silently widening the surface FR-110 forbids growing a
	// classifier onto.
	params := tool.Parameters()
	properties, _ := params["properties"].(map[string]any)
	criteriaSchema, _ := properties["criteria"].(map[string]any)
	items, _ := criteriaSchema["items"].(map[string]any)
	itemProps, _ := items["properties"].(map[string]any)
	judgmentSchema, _ := itemProps["judgment"].(map[string]any)
	enumRaw, _ := judgmentSchema["enum"].([]string)
	want := []string{"boolean", "quantitative", "artifact"}
	if len(enumRaw) != len(want) {
		t.Fatalf("judgment enum = %v, want exactly %v", enumRaw, want)
	}
	for i, v := range want {
		if enumRaw[i] != v {
			t.Fatalf("judgment enum = %v, want exactly %v", enumRaw, want)
		}
	}
}
