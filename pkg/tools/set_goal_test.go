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
	condition map[string]string
	record    map[string]string
	readErr   error
	writeErr  error
	writes    int
}

func newFakeGoalRecordAccess() *fakeGoalRecordAccess {
	return &fakeGoalRecordAccess{condition: map[string]string{}, record: map[string]string{}}
}

func (f *fakeGoalRecordAccess) ReadGoalState(sessionID string) (string, string, error) {
	if f.readErr != nil {
		return "", "", f.readErr
	}
	return f.condition[sessionID], f.record[sessionID], nil
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
