// Omnipus — ADR-080 D-TYPES/D-DOD atomic-contract-foundation tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// goal_compile_adr080_test.go — this wave's Go-side coverage for ADR-080's
// D-TYPES sameShape-with-judgment amendment: two criteria retagged from one
// judgment to another are a real change, not "unchanged"; and D-DOD's
// legacy-goal load-time DoD backfill: loadCompiledGoal injects the built-in
// floor DoD into any pre-ADR-080 persisted goal carrying none, so a legacy
// goal always satisfies the wire schema's `dod: minItems: 1`.

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestSameShape_JudgmentIncorporated is ADR-080 D-TYPES' required test: two
// criteria with identical Text/Kind/Check-or-Behavior payload but DIFFERENT
// Judgment are NOT the same shape — a re-tag (e.g. boolean -> quantitative)
// on otherwise-unchanged wording is a real change the amendment diff must
// surface, not silently collapse into "unchanged".
func TestSameShape_JudgmentIncorporated(t *testing.T) {
	t.Parallel()

	t.Run("prose_same_judgment_is_same_shape", func(t *testing.T) {
		t.Parallel()
		a := task.AcceptanceCriterion{Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the email is polite"}
		b := task.AcceptanceCriterion{Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the email is polite"}
		if !sameShape(a, b) {
			t.Error("identical kind+judgment (both prose/boolean) must be sameShape")
		}
	})

	t.Run("prose_different_judgment_is_NOT_same_shape", func(t *testing.T) {
		t.Parallel()
		a := task.AcceptanceCriterion{Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "names competitors"}
		b := task.AcceptanceCriterion{Kind: task.KindProse, Judgment: task.JudgmentQuantitative, Text: "names competitors"}
		if sameShape(a, b) {
			t.Error("same kind/text but retagged judgment (boolean -> quantitative) must NOT be sameShape")
		}
	})

	t.Run("check_same_payload_and_judgment_is_same_shape", func(t *testing.T) {
		t.Parallel()
		check := &task.CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0}
		a := task.AcceptanceCriterion{Kind: task.KindCheck, Judgment: task.JudgmentBoolean, Check: check}
		b := task.AcceptanceCriterion{Kind: task.KindCheck, Judgment: task.JudgmentBoolean, Check: check}
		if !sameShape(a, b) {
			t.Error("identical check payload+judgment must be sameShape")
		}
	})

	t.Run("amendment_diff_surfaces_a_pure_judgment_retag_as_changed", func(t *testing.T) {
		t.Parallel()
		author := task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "u1"}
		current := &CompiledGoal{Criteria: []task.AcceptanceCriterion{
			{Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "names competitors", Author: author},
		}}
		proposed := &CompiledGoal{Criteria: []task.AcceptanceCriterion{
			{Kind: task.KindProse, Judgment: task.JudgmentQuantitative, Text: "names competitors", Author: author},
		}}
		amd := diffGoalAmendment(current, proposed)
		if len(amd.Changed) != 1 {
			t.Fatalf("Changed = %d items, want 1 (a pure judgment retag on identical text must surface as changed)", len(amd.Changed))
		}
		if len(amd.Added) != 0 || len(amd.Dropped) != 0 {
			t.Errorf("Added/Dropped should be empty for a same-text retag: added=%d dropped=%d", len(amd.Added), len(amd.Dropped))
		}
	})
}

// TestLoadCompiledGoal_LegacyDoDBackfill is ADR-080 D-DOD's required
// coverage: a pre-ADR-080 persisted CompiledGoal JSON (no `dod` key at all)
// loads with the built-in floor DoD injected, so the in-memory invariant
// "every loaded goal carries >= 1 DoD item" holds even for legacy data —
// mirroring what Goal.yaml's `dod: minItems: 1` will require of the wire
// record once a later wave serves it.
func TestLoadCompiledGoal_LegacyDoDBackfill(t *testing.T) {
	t.Parallel()

	t.Run("legacy_json_with_no_dod_key_gets_floor_backfill", func(t *testing.T) {
		t.Parallel()
		// A pre-ADR-080 CompiledGoal serialization: no "dod" field at all.
		legacy := `{"intent":"make tests pass","prompt":"make tests pass","criteria":[` +
			`{"id":"c1","kind":"prose","judgment":"boolean","text":"tests pass",` +
			`"author":{"kind":"user","id":"u1"},"status":"pending"}]}`
		g := loadCompiledGoal(legacy)
		if g == nil {
			t.Fatal("loadCompiledGoal returned nil for valid legacy JSON")
		}
		if len(g.DoD) == 0 {
			t.Fatal("legacy goal with no persisted dod must be backfilled with the built-in floor DoD (>= 1 item)")
		}
		for i, d := range g.DoD {
			if d.Provenance != task.ProvenanceFloor {
				t.Errorf("dod[%d].Provenance = %q, want %q (floor backfill)", i, d.Provenance, task.ProvenanceFloor)
			}
			if !task.IsValidJudgment(d.Judgment) {
				t.Errorf("dod[%d].Judgment = %q is not a valid judgment", i, d.Judgment)
			}
			if d.Text == "" {
				t.Errorf("dod[%d].Text is empty", i)
			}
		}
	})

	t.Run("goal_with_persisted_dod_is_not_overridden", func(t *testing.T) {
		t.Parallel()
		withDoD := `{"intent":"x","prompt":"x","criteria":[` +
			`{"id":"c1","kind":"prose","judgment":"boolean","text":"x",` +
			`"author":{"kind":"user","id":"u1"},"status":"pending"}],` +
			`"dod":[{"id":"d1","kind":"prose","judgment":"boolean","provenance":"stated",` +
			`"text":"custom dod item","author":{"kind":"user","id":"u1"},"status":"pending"}]}`
		g := loadCompiledGoal(withDoD)
		if g == nil {
			t.Fatal("loadCompiledGoal returned nil for valid JSON")
		}
		if len(g.DoD) != 1 || g.DoD[0].ID != "d1" {
			t.Fatalf("persisted dod was overridden by the floor backfill: got %+v", g.DoD)
		}
	})

	t.Run("floor_dod_is_byte_stable_across_repeated_loads", func(t *testing.T) {
		t.Parallel()
		legacy := `{"intent":"x","prompt":"x","criteria":[` +
			`{"id":"c1","kind":"prose","judgment":"boolean","text":"x",` +
			`"author":{"kind":"user","id":"u1"},"status":"pending"}]}`
		g1 := loadCompiledGoal(legacy)
		g2 := loadCompiledGoal(legacy)
		b1, err := json.Marshal(g1.DoD)
		if err != nil {
			t.Fatalf("marshal g1.DoD: %v", err)
		}
		b2, err := json.Marshal(g2.DoD)
		if err != nil {
			t.Fatalf("marshal g2.DoD: %v", err)
		}
		if string(b1) != string(b2) {
			t.Errorf("floor DoD is not byte-stable across loads:\n%s\nvs\n%s", b1, b2)
		}
	})
}

// TestNewFloorDoD_ValidatesShape proves the floor DoD constructor's output
// itself passes task.NormalizeCriteria — the floor items must be valid
// AcceptanceCriterion records, not just structurally present.
func TestNewFloorDoD_ValidatesShape(t *testing.T) {
	t.Parallel()
	dod := newFloorDoD()
	if len(dod) == 0 {
		t.Fatal("newFloorDoD returned zero items — the floor must guarantee >= 1")
	}
	if _, err := task.NormalizeCriteria(dod); err != nil {
		t.Fatalf("floor DoD failed NormalizeCriteria: %v", err)
	}
}
