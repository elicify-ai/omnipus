// Omnipus — set_goal tool tests (spec tests 1-3, DS-1 rows: ADR-081 D2).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// fakeGoalRecordAccess is the in-memory GoalRecordAccess double used by every
// test in this file. condition/record are keyed by session id, mirroring the
// two session-meta fields the real wave-2 wiring reads/writes.
type fakeGoalRecordAccess struct {
	goalID    map[string]string
	condition map[string]string
	record    map[string]string
	readErr   error
	writeErr  error
	writes    int
}

func newFakeGoalRecordAccess() *fakeGoalRecordAccess {
	return &fakeGoalRecordAccess{goalID: map[string]string{}, condition: map[string]string{}, record: map[string]string{}}
}

// ReadGoalState mirrors the real wave-2 wiring's invariant (see
// agentLoopGoalRecordAccess.ReadGoalState's doc comment): GoalID is minted
// at the SAME moment GoalCondition is first set. Tests that set
// access.condition[sessionID] directly (most of this file, pre-dating
// ADR-082 D9) without also setting access.goalID[sessionID] get a
// deterministic synthesized id here rather than an empty string, so every
// existing "active goal" fixture keeps behaving like a real activated goal.
func (f *fakeGoalRecordAccess) ReadGoalState(sessionID string) (string, string, string, error) {
	if f.readErr != nil {
		return "", "", "", f.readErr
	}
	goalID := f.goalID[sessionID]
	if goalID == "" && f.condition[sessionID] != "" {
		goalID = "fake-goal-" + sessionID
	}
	return goalID, f.condition[sessionID], f.record[sessionID], nil
}

func (f *fakeGoalRecordAccess) WriteRecord(sessionID, recordJSON string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.record[sessionID] = recordJSON
	f.writes++
	return nil
}

func newSetGoalTool(access GoalRecordAccess) *SetGoalTool {
	return NewSetGoalTool(func() GoalRecordAccess { return access })
}

// setGoalCtx builds a tool context for an owner session (delegation depth 0)
// with an active goal already recorded on access for sessionID.
func setGoalCtx(sessionID, agentID string) context.Context {
	ctx := context.Background()
	ctx = WithTranscriptSessionID(ctx, sessionID)
	ctx = WithAgentID(ctx, agentID)
	return ctx
}

func minimalCriteriaArg() []any {
	return []any{
		map[string]any{"text": "the game is playable end to end", "judgment": "boolean"},
	}
}

func TestSetGoalTool_ValidatesAndWrites(t *testing.T) {
	const sessionID = "session_goal_1"
	const agentID = "mia"

	newActiveAccess := func() *fakeGoalRecordAccess {
		a := newFakeGoalRecordAccess()
		a.condition[sessionID] = "build a tetris game"
		return a
	}

	t.Run("valid minimal register backfills the DoD floor", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship a playable browser tetris game.",
			"criteria":   minimalCriteriaArg(),
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		var payload struct {
			Mode          string `json:"mode"`
			CriteriaCount int    `json:"criteria_count"`
			DoDCount      int    `json:"dod_count"`
		}
		if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
			t.Fatalf("result does not parse: %v (%q)", err, res.ForLLM)
		}
		if payload.Mode != "register" || payload.CriteriaCount != 1 || payload.DoDCount != 2 {
			t.Fatalf("bad payload: %+v", payload)
		}
		var rec setGoalRecord
		if err := json.Unmarshal([]byte(access.record[sessionID]), &rec); err != nil {
			t.Fatalf("written record does not parse: %v", err)
		}
		if len(rec.Criteria) != 1 || len(rec.DoD) != 2 {
			t.Fatalf("written record has wrong shape: %+v", rec)
		}
		if rec.DoD[0].ID != "goal-dod-floor-no-secrets" || rec.DoD[1].ID != "goal-dod-floor-grounded-claims" {
			t.Fatalf("floor DoD ids wrong: %+v", rec.DoD)
		}
		for _, c := range rec.DoD {
			if c.Provenance != task.ProvenanceFloor {
				t.Fatalf("floor DoD item %q has provenance %q, want floor", c.Text, c.Provenance)
			}
		}
	})

	t.Run("valid full register with 16 criteria, 4 dod, unicode text", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)

		criteriaArg := make([]any, 0, 16)
		judgments := []string{"boolean", "quantitative", "artifact"}
		for i := 0; i < 16; i++ {
			text := "criterion"
			if i == 0 {
				text = "the summary is written to notes.md — 测试 unicode 🎯"
			}
			criteriaArg = append(criteriaArg, map[string]any{
				"text":     text + " " + strconv.Itoa(i),
				"judgment": judgments[i%len(judgments)],
			})
		}
		dodArg := []any{
			map[string]any{"text": "no TODOs remain", "judgment": "boolean", "provenance": "stated"},
			map[string]any{"text": "follows workspace lint rules", "judgment": "boolean", "provenance": "workspace"},
			map[string]any{"text": "coverage above 80%", "judgment": "quantitative", "provenance": "inferred"},
			map[string]any{"text": "readme updated", "judgment": "artifact", "provenance": "stated"},
		}

		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship a fully-tested tetris/snake/asteroids portal.",
			"criteria":   criteriaArg,
			"dod":        dodArg,
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		var rec setGoalRecord
		if err := json.Unmarshal([]byte(access.record[sessionID]), &rec); err != nil {
			t.Fatalf("written record does not parse: %v", err)
		}
		if len(rec.Criteria) != 16 {
			t.Fatalf("want 16 criteria, got %d", len(rec.Criteria))
		}
		if len(rec.DoD) != 4 {
			t.Fatalf("want 4 dod entries (no floor backfill — dod was non-empty), got %d", len(rec.DoD))
		}
		if !strings.Contains(rec.Criteria[0].Text, "测试") {
			t.Fatalf("unicode text lost: %q", rec.Criteria[0].Text)
		}
	})

	t.Run("judgment bool is rejected", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship the game.",
			"criteria": []any{
				map[string]any{"text": "it works", "judgment": "bool"},
			},
		})
		if !res.IsError {
			t.Fatal("want an error for judgment \"bool\"")
		}
		if !strings.Contains(res.ForLLM, "bool") {
			t.Fatalf("error should name the offending value: %q", res.ForLLM)
		}
		if _, ok := access.record[sessionID]; ok {
			t.Fatal("nothing should have been written on rejection")
		}
	})

	t.Run("judgment empty is rejected (explicit-judgment path, not task-package inference)", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship the game.",
			"criteria": []any{
				map[string]any{"text": "it works", "judgment": ""},
			},
		})
		if !res.IsError {
			t.Fatal("want an error for empty judgment")
		}
		if !strings.Contains(res.ForLLM, "judgment is required") {
			t.Fatalf("error should name the missing judgment: %q", res.ForLLM)
		}
		if _, ok := access.record[sessionID]; ok {
			t.Fatal("nothing should have been written on rejection")
		}
	})

	t.Run("criteria [] is rejected", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship the game.",
			"criteria":   []any{},
		})
		if !res.IsError {
			t.Fatal("want an error for empty criteria")
		}
		if !strings.Contains(res.ForLLM, "criteria is required") {
			t.Fatalf("error should name criteria: %q", res.ForLLM)
		}
	})

	t.Run("definition empty is rejected", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "   ",
			"criteria":   minimalCriteriaArg(),
		})
		if !res.IsError {
			t.Fatal("want an error for empty definition")
		}
		if !strings.Contains(res.ForLLM, "definition is required") {
			t.Fatalf("error should name definition: %q", res.ForLLM)
		}
	})

	t.Run("assessment is validated, surfaced in the result, and absent from the persisted record", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship the game.",
			"criteria":   minimalCriteriaArg(),
			"assessment": map[string]any{
				"clarity":     "ambiguous",
				"assumptions": []any{"assuming single-player only"},
			},
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "\"assessment\"") || !strings.Contains(res.ForLLM, "ambiguous") {
			t.Fatalf("result should surface the assessment: %q", res.ForLLM)
		}
		written := access.record[sessionID]
		var asMap map[string]any
		if err := json.Unmarshal([]byte(written), &asMap); err != nil {
			t.Fatalf("written record does not parse: %v", err)
		}
		if _, present := asMap["assessment"]; present {
			t.Fatalf("assessment must NOT be persisted into the record: %s", written)
		}
		if strings.Contains(written, "ambiguous") || strings.Contains(written, "clarity") {
			t.Fatalf("assessment content leaked into the persisted record: %s", written)
		}
	})

	t.Run("invalid assessment clarity is rejected", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship the game.",
			"criteria":   minimalCriteriaArg(),
			"assessment": map[string]any{"clarity": "very clear"},
		})
		if !res.IsError {
			t.Fatal("want an error for an invalid clarity value")
		}
	})

	t.Run("dod provenance guessed is rejected", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship the game.",
			"criteria":   minimalCriteriaArg(),
			"dod": []any{
				map[string]any{"text": "no todos", "judgment": "boolean", "provenance": "guessed"},
			},
		})
		if !res.IsError {
			t.Fatal("want an error for provenance \"guessed\"")
		}
		if !strings.Contains(res.ForLLM, "guessed") {
			t.Fatalf("error should name the offending value: %q", res.ForLLM)
		}
		if _, ok := access.record[sessionID]; ok {
			t.Fatal("nothing should have been written on rejection")
		}
	})

	t.Run("dod provenance omitted is rejected (explicit-provenance path)", func(t *testing.T) {
		access := newActiveAccess()
		tool := newSetGoalTool(access)
		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship the game.",
			"criteria":   minimalCriteriaArg(),
			"dod": []any{
				map[string]any{"text": "no todos", "judgment": "boolean"},
			},
		})
		if !res.IsError {
			t.Fatal("want an error for a missing provenance")
		}
		if !strings.Contains(res.ForLLM, "provenance is required") {
			t.Fatalf("error should name provenance: %q", res.ForLLM)
		}
	})
}

func TestSetGoalTool_ScopePreconditions(t *testing.T) {
	t.Run("delegation depth > 0 refuses and writes nothing", func(t *testing.T) {
		const sessionID = "session_goal_deleg"
		access := newFakeGoalRecordAccess()
		access.condition[sessionID] = "an active goal"
		tool := newSetGoalTool(access)

		ctx := WithDelegationDepth(setGoalCtx(sessionID, "mia"), 1)
		res := tool.Execute(ctx, map[string]any{
			"definition": "Ship it.",
			"criteria":   minimalCriteriaArg(),
		})
		if !res.IsError {
			t.Fatal("want a refusal at delegation depth > 0")
		}
		if !strings.Contains(res.ForLLM, "owner-session-only") {
			t.Fatalf("error should name the delegation refusal: %q", res.ForLLM)
		}
		if access.writes != 0 {
			t.Fatalf("must not write on a delegation-depth refusal, got %d writes", access.writes)
		}
	})

	t.Run("goalless session refuses and writes nothing", func(t *testing.T) {
		const sessionID = "session_goal_none"
		access := newFakeGoalRecordAccess() // no condition set — goalless
		tool := newSetGoalTool(access)

		res := tool.Execute(setGoalCtx(sessionID, "mia"), map[string]any{
			"definition": "Ship it.",
			"criteria":   minimalCriteriaArg(),
		})
		if !res.IsError {
			t.Fatal("want a refusal on a goalless session")
		}
		if !strings.Contains(res.ForLLM, "no active goal") {
			t.Fatalf("error should name the goalless refusal: %q", res.ForLLM)
		}
		if access.writes != 0 {
			t.Fatalf("must not write on a goalless-session refusal, got %d writes", access.writes)
		}
	})
}

func TestSetGoalTool_UpdateDiffs(t *testing.T) {
	const sessionID = "session_goal_update"
	const agentID = "mia"

	t.Run("update with no prior record is refused", func(t *testing.T) {
		access := newFakeGoalRecordAccess()
		access.condition[sessionID] = "an active goal"
		tool := newSetGoalTool(access)

		res := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"mode":       "update",
			"definition": "Ship it.",
			"criteria":   minimalCriteriaArg(),
		})
		if !res.IsError {
			t.Fatal("want a refusal for mode:update with no prior record")
		}
		if !strings.Contains(res.ForLLM, "no record has been registered") {
			t.Fatalf("error should name the missing record: %q", res.ForLLM)
		}
	})

	t.Run("register then update: diff reflects the change, built-in fallback", func(t *testing.T) {
		access := newFakeGoalRecordAccess()
		access.condition[sessionID] = "an active goal"
		tool := newSetGoalTool(access)

		registerRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship a tetris game.",
			"criteria": []any{
				map[string]any{"text": "tetris pieces rotate correctly", "judgment": "boolean"},
			},
		})
		if registerRes.IsError {
			t.Fatalf("register failed: %s", registerRes.ForLLM)
		}
		afterRegister := access.record[sessionID]
		if afterRegister == "" {
			t.Fatal("register must have written a record")
		}

		updateRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"mode":       "update",
			"definition": "Ship a tetris + snake game.",
			"criteria": []any{
				map[string]any{"text": "tetris pieces rotate correctly", "judgment": "boolean"},
				map[string]any{"text": "snake grows on eating food", "judgment": "boolean"},
			},
		})
		if updateRes.IsError {
			t.Fatalf("update failed: %s", updateRes.ForLLM)
		}
		var payload struct {
			Mode string `json:"mode"`
			Diff string `json:"diff"`
		}
		if err := json.Unmarshal([]byte(updateRes.ForLLM), &payload); err != nil {
			t.Fatalf("update result does not parse: %v (%q)", err, updateRes.ForLLM)
		}
		if payload.Mode != "update" {
			t.Fatalf("want mode update, got %q", payload.Mode)
		}
		if payload.Diff == "" {
			t.Fatal("update result must carry a non-empty diff summary")
		}
		if !strings.Contains(payload.Diff, "+1 added") {
			t.Fatalf("built-in diff fallback should report the one added criterion: %q", payload.Diff)
		}

		afterUpdate := access.record[sessionID]
		if afterUpdate == afterRegister {
			t.Fatal("the record must have actually changed after update")
		}
		var rec setGoalRecord
		if err := json.Unmarshal([]byte(afterUpdate), &rec); err != nil {
			t.Fatalf("updated record does not parse: %v", err)
		}
		if len(rec.Criteria) != 2 {
			t.Fatalf("want 2 criteria after update, got %d", len(rec.Criteria))
		}
	})

	t.Run("injected DiffFn seam is called with old and new record JSON and its result is used verbatim", func(t *testing.T) {
		access := newFakeGoalRecordAccess()
		access.condition[sessionID] = "an active goal"
		tool := newSetGoalTool(access)

		registerRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"definition": "Ship a tetris game.",
			"criteria": []any{
				map[string]any{"text": "tetris pieces rotate correctly", "judgment": "boolean"},
			},
		})
		if registerRes.IsError {
			t.Fatalf("register failed: %s", registerRes.ForLLM)
		}
		priorJSON := access.record[sessionID]

		var gotOld, gotNew string
		tool.SetDiffFn(func(oldRecordJSON, newRecordJSON string) string {
			gotOld = oldRecordJSON
			gotNew = newRecordJSON
			return "injected-diff-summary"
		})

		updateRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
			"mode":       "update",
			"definition": "Ship a tetris game, polished.",
			"criteria": []any{
				map[string]any{"text": "tetris pieces rotate correctly", "judgment": "boolean"},
			},
		})
		if updateRes.IsError {
			t.Fatalf("update failed: %s", updateRes.ForLLM)
		}
		if gotOld != priorJSON {
			t.Fatalf("DiffFn should receive the PRIOR record JSON: got %q want %q", gotOld, priorJSON)
		}
		if gotNew == "" || gotNew == gotOld {
			t.Fatalf("DiffFn should receive the freshly written NEW record JSON: %q", gotNew)
		}
		if !strings.Contains(updateRes.ForLLM, "injected-diff-summary") {
			t.Fatalf("result should carry the injected DiffFn's output verbatim: %q", updateRes.ForLLM)
		}
	})
}

// TestSetGoalTool_UpdateMergesKindFromOldRecord is review-round-1 finding
// #7(a): this tool's own Parameters() schema has no check/behavior input
// shape (criteria items offer only text+judgment) — so mode:update's
// hardcoded KindProse must not silently downgrade a marker-authored
// machine-verifiable criterion the agent merely re-submits by text as part
// of an otherwise-unrelated steering update. The prior record here is seeded
// directly (simulating a marker-authored record from compileGoalIntent,
// which this tool cannot itself author) with one Kind=check criterion
// carrying a real Check payload.
func TestSetGoalTool_UpdateMergesKindFromOldRecord(t *testing.T) {
	const sessionID = "session_goal_kind_merge"
	const agentID = "mia"

	access := newFakeGoalRecordAccess()
	access.condition[sessionID] = "an active goal"

	priorRec := setGoalRecord{
		Definition: "Ship a tetris game.",
		Criteria: []task.AcceptanceCriterion{
			{
				ID: "c-check-1", Kind: task.KindCheck, Judgment: task.JudgmentBoolean,
				Text:   "the test suite passes",
				Check:  &task.CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0},
				Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: agentID},
				Status: task.CritPending,
			},
			{
				ID: "c-prose-1", Kind: task.KindProse, Judgment: task.JudgmentBoolean,
				Text:   "the game feels fun to play",
				Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: agentID},
				Status: task.CritPending,
			},
		},
	}
	priorJSON, err := json.Marshal(priorRec)
	if err != nil {
		t.Fatalf("seeding prior record: %v", err)
	}
	access.record[sessionID] = string(priorJSON)

	tool := newSetGoalTool(access)
	updateRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
		"mode":       "update",
		"definition": "Ship a tetris game, polished.",
		"criteria": []any{
			// Re-submitted by TEXT only — this tool's schema cannot express
			// kind/check at all, so a caller merely restating an existing
			// machine-verifiable criterion has no way to preserve it itself.
			map[string]any{"text": "the test suite passes", "judgment": "boolean"},
			// A genuinely NEW criterion — must stay prose (no old match).
			map[string]any{"text": "the controls feel responsive", "judgment": "boolean"},
		},
	})
	if updateRes.IsError {
		t.Fatalf("update failed: %s", updateRes.ForLLM)
	}

	var after setGoalRecord
	if err := json.Unmarshal([]byte(access.record[sessionID]), &after); err != nil {
		t.Fatalf("updated record does not parse: %v", err)
	}
	if len(after.Criteria) != 2 {
		t.Fatalf("want 2 criteria after update, got %d: %+v", len(after.Criteria), after.Criteria)
	}

	var checkCrit, newCrit *task.AcceptanceCriterion
	for i := range after.Criteria {
		switch after.Criteria[i].Text {
		case "the test suite passes":
			checkCrit = &after.Criteria[i]
		case "the controls feel responsive":
			newCrit = &after.Criteria[i]
		}
	}
	if checkCrit == nil {
		t.Fatal("the re-submitted 'test suite passes' criterion is missing from the updated record")
	}
	if checkCrit.Kind != task.KindCheck {
		t.Fatalf("finding #7(a): re-submitting a matching-text criterion on update must KEEP its old Kind, "+
			"got Kind=%q want %q", checkCrit.Kind, task.KindCheck)
	}
	if checkCrit.Check == nil || checkCrit.Check.Command != "go test ./..." {
		t.Fatalf("finding #7(a): the old Check payload must be carried onto the merged criterion, got %+v", checkCrit.Check)
	}
	if newCrit == nil {
		t.Fatal("the genuinely new 'controls feel responsive' criterion is missing from the updated record")
	}
	if newCrit.Kind != task.KindProse {
		t.Fatalf("a criterion with NO match in the old record must stay prose (the tool's only authorable kind), got %q", newCrit.Kind)
	}
}

// TestSetGoalTool_UpdateDoD_OmittedCarriesForward_ExplicitEmptyResetsFloor is
// review-round-1 finding #7(b): an omitted `dod` arg on mode:update must
// carry the PRIOR DoD forward unchanged (the caller didn't touch it) — not
// silently replace it with the generic floor. An EXPLICIT empty `dod: []`,
// by contrast, is a deliberate "reset to the floor" instruction and must
// still apply the floor, exactly as it always has.
func TestSetGoalTool_UpdateDoD_OmittedCarriesForward_ExplicitEmptyResetsFloor(t *testing.T) {
	const sessionID = "session_goal_dod_carry"
	const agentID = "mia"

	access := newFakeGoalRecordAccess()
	access.condition[sessionID] = "an active goal"
	tool := newSetGoalTool(access)

	// register with an explicit, CUSTOM (non-floor) DoD.
	registerRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
		"definition": "Ship a tetris game.",
		"criteria":   minimalCriteriaArg(),
		"dod": []any{
			map[string]any{"text": "a custom quality gate", "judgment": "boolean", "provenance": "stated"},
		},
	})
	if registerRes.IsError {
		t.Fatalf("register failed: %s", registerRes.ForLLM)
	}
	var afterRegister setGoalRecord
	if err := json.Unmarshal([]byte(access.record[sessionID]), &afterRegister); err != nil {
		t.Fatalf("registered record does not parse: %v", err)
	}
	if len(afterRegister.DoD) != 1 || afterRegister.DoD[0].Text != "a custom quality gate" {
		t.Fatalf("setup: register must have persisted the custom DoD, got %+v", afterRegister.DoD)
	}

	// update with `dod` OMITTED entirely — must carry the custom DoD forward
	// unchanged, not fall back to the floor.
	updateRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
		"mode":       "update",
		"definition": "Ship a tetris game, polished.",
		"criteria":   minimalCriteriaArg(),
	})
	if updateRes.IsError {
		t.Fatalf("update (dod omitted) failed: %s", updateRes.ForLLM)
	}
	var afterOmitted setGoalRecord
	if err := json.Unmarshal([]byte(access.record[sessionID]), &afterOmitted); err != nil {
		t.Fatalf("updated record does not parse: %v", err)
	}
	if len(afterOmitted.DoD) != 1 || afterOmitted.DoD[0].Text != "a custom quality gate" {
		t.Fatalf("finding #7(b): an omitted dod on update must carry the PRIOR DoD forward unchanged, got %+v", afterOmitted.DoD)
	}

	// update with `dod` EXPLICITLY empty — a deliberate reset to the floor.
	updateReset := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
		"mode":       "update",
		"definition": "Ship a tetris game, polished, reset.",
		"criteria":   minimalCriteriaArg(),
		"dod":        []any{},
	})
	if updateReset.IsError {
		t.Fatalf("update (dod explicit empty) failed: %s", updateReset.ForLLM)
	}
	var afterReset setGoalRecord
	if err := json.Unmarshal([]byte(access.record[sessionID]), &afterReset); err != nil {
		t.Fatalf("reset record does not parse: %v", err)
	}
	wantFloor := setGoalFloorDoD()
	if len(afterReset.DoD) != len(wantFloor) {
		t.Fatalf("an EXPLICIT empty dod on update must reset to the floor (%d items), got %d: %+v",
			len(wantFloor), len(afterReset.DoD), afterReset.DoD)
	}
	for i, want := range wantFloor {
		if afterReset.DoD[i].ID != want.ID {
			t.Fatalf("floor DoD item %d id = %q, want %q", i, afterReset.DoD[i].ID, want.ID)
		}
	}
}

// TestSetGoal_ResultCarriesGoalIDAndRecord is T-21 (ADR-082 D9/FR-016,
// ui-independent-turns-spec.md S-15): the success result must carry
// goal_id and the FULL registered record (definition/criteria/dod), not
// just the counts — this is what lets the SPA's dedicated set_goal tool UI
// render the goal card directly from the call's own result, anchored at
// the call's position, instead of the deleted GoalThreadTailCards mount
// (which only ever read the goal_status frame via goalPills). A mode:update
// (amend) call must report the SAME goal_id — ADR-081/ADR-053: GoalID never
// changes within a goal generation, only a fresh goal (not exercised here)
// mints a new one — with the amended record.
func TestSetGoal_ResultCarriesGoalIDAndRecord(t *testing.T) {
	const sessionID = "session_goal_result_shape"
	const agentID = "mia"
	const wantGoalID = "goal-result-shape-1"

	access := newFakeGoalRecordAccess()
	access.goalID[sessionID] = wantGoalID
	access.condition[sessionID] = "an active goal"
	tool := newSetGoalTool(access)

	type resultPayload struct {
		Mode          string                     `json:"mode"`
		GoalID        string                     `json:"goal_id"`
		Definition    string                     `json:"definition"`
		CriteriaCount int                        `json:"criteria_count"`
		DoDCount      int                        `json:"dod_count"`
		Criteria      []task.AcceptanceCriterion `json:"criteria"`
		DoD           []task.AcceptanceCriterion `json:"dod"`
	}

	registerRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
		"definition": "Ship a playable browser tetris game.",
		"criteria":   minimalCriteriaArg(),
	})
	if registerRes.IsError {
		t.Fatalf("register failed: %s", registerRes.ForLLM)
	}
	var registered resultPayload
	if err := json.Unmarshal([]byte(registerRes.ForLLM), &registered); err != nil {
		t.Fatalf("register result does not parse: %v (%q)", err, registerRes.ForLLM)
	}
	if registered.GoalID == "" {
		t.Fatal("register result must carry a non-empty goal_id")
	}
	if registered.GoalID != wantGoalID {
		t.Fatalf("goal_id = %q, want the session's minted id %q", registered.GoalID, wantGoalID)
	}
	if registered.Definition != "Ship a playable browser tetris game." {
		t.Fatalf("result definition = %q, want the registered definition", registered.Definition)
	}
	if len(registered.Criteria) != 1 || registered.Criteria[0].Text != "the game is playable end to end" {
		t.Fatalf("result must carry the FULL criteria array, got %+v", registered.Criteria)
	}
	if len(registered.DoD) != 2 {
		t.Fatalf("result must carry the FULL dod array (floor-backfilled), got %+v", registered.DoD)
	}
	if registered.CriteriaCount != len(registered.Criteria) || registered.DoDCount != len(registered.DoD) {
		t.Fatalf("criteria_count/dod_count must still match the array lengths: counts=%d/%d arrays=%d/%d",
			registered.CriteriaCount, registered.DoDCount, len(registered.Criteria), len(registered.DoD))
	}

	amendRes := tool.Execute(setGoalCtx(sessionID, agentID), map[string]any{
		"mode":       "update",
		"definition": "Ship a playable browser tetris game with sound.",
		"criteria": []any{
			map[string]any{"text": "the game is playable end to end", "judgment": "boolean"},
			map[string]any{"text": "sound effects play on line clear", "judgment": "boolean"},
		},
	})
	if amendRes.IsError {
		t.Fatalf("amend failed: %s", amendRes.ForLLM)
	}
	var amended resultPayload
	if err := json.Unmarshal([]byte(amendRes.ForLLM), &amended); err != nil {
		t.Fatalf("amend result does not parse: %v (%q)", err, amendRes.ForLLM)
	}
	if amended.GoalID != registered.GoalID {
		t.Fatalf("amend must report the SAME goal_id as register, got %q want %q", amended.GoalID, registered.GoalID)
	}
	if amended.Definition != "Ship a playable browser tetris game with sound." {
		t.Fatalf("amended result definition = %q, want the amended definition", amended.Definition)
	}
	if len(amended.Criteria) != 2 {
		t.Fatalf("amended result must carry the amended (2-item) criteria array, got %+v", amended.Criteria)
	}
}
