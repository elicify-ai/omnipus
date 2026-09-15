// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// restate_test.go is UAT E-1's entity-level oracle: a prose restate of an
// active goal must never leave one record describing two different pieces of
// work (a new prompt on top of the previous prompt's compiled ladder).
package goal

import (
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func restateFloorDoD() []task.AcceptanceCriterion {
	return []task.AcceptanceCriterion{newTestCriterion("", "no secrets appear in the output")}
}

// activeTestGoalWithOutcome builds an ACTIVE goal that has already been
// claimed and judged against its compiled ladder — the UAT E-1 shape at the
// moment the second /goal arrived.
func activeTestGoalWithOutcome(t *testing.T) *Goal {
	t.Helper()
	g := newTestGoal(t, generated.GoalOwnerKindSession, "session-e1")
	g.GoalID = "goal-e1"
	now := time.Now().UTC()
	if err := g.Activate("session-e1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := g.RecordClaim(generated.GoalLatestClaimStatusMet, "wrote alpha", now); err != nil {
		t.Fatalf("RecordClaim: %v", err)
	}
	verdict := &task.JudgeVerdict{Met: false, PerCriterion: []task.CriterionVerdict{
		{CriterionID: g.Criteria[0].ID, Met: false, Reason: "alpha file missing"},
	}}
	if err := g.RecordVerdict(verdict, "alpha file missing", now); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}
	g.Criteria[0].Status = task.CritUnmet
	g.QuestionRoundsUsed = 1
	g.ZeroOutputPushes = 1
	return g
}

func TestRestate_SupersedesTheCompiledDefinition(t *testing.T) {
	g := activeTestGoalWithOutcome(t)
	oldCriterionText := g.Criteria[0].Text
	oldDoDText := g.DoD[0].Text
	oldRound := g.Round
	now := time.Now().UTC().Add(time.Minute)

	changed, err := g.Restate("  write a file named beta.txt containing BETA ", restateFloorDoD(), now)
	if err != nil {
		t.Fatalf("Restate: %v", err)
	}
	if !changed {
		t.Fatal("Restate reported changed=false for a different prompt")
	}

	if g.Prompt != "write a file named beta.txt containing BETA" {
		t.Errorf("Prompt = %q, want the trimmed new intent", g.Prompt)
	}
	if g.Definition != "" {
		t.Errorf("Definition = %q, want empty — it restated the PREVIOUS prompt", g.Definition)
	}
	if len(g.Criteria) != 0 {
		t.Errorf("Criteria = %+v, want empty — the old ladder judged the previous prompt", g.Criteria)
	}
	if len(g.DoD) != 1 || g.DoD[0].Text != "no secrets appear in the output" {
		t.Errorf("DoD = %+v, want exactly the supplied floor item", g.DoD)
	}
	if g.LatestVerdict != nil || g.LatestClaim != nil || g.LatestReason != "" {
		t.Errorf("per-definition outcome not cleared: verdict=%v claim=%v reason=%q",
			g.LatestVerdict, g.LatestClaim, g.LatestReason)
	}

	if len(g.SupersededCriteria) != 1 {
		t.Fatalf("SupersededCriteria has %d entries, want 1 — the old ladder must be kept as history, not erased",
			len(g.SupersededCriteria))
	}
	hist := g.SupersededCriteria[0]
	if len(hist.Criteria) != 1 || hist.Criteria[0].Text != oldCriterionText || hist.Criteria[0].Status != task.CritUnmet {
		t.Errorf("history criteria = %+v, want the old criterion %q with its judged status unmet", hist.Criteria, oldCriterionText)
	}
	if len(hist.DoD) != 1 || hist.DoD[0].Text != oldDoDText {
		t.Errorf("history dod = %+v, want the old DoD item %q", hist.DoD, oldDoDText)
	}
	if !hist.SupersededAt.Equal(now) || !g.LastActivityAt.Equal(now) {
		t.Errorf("timestamps: superseded_at=%v last_activity_at=%v, want both %v", hist.SupersededAt, g.LastActivityAt, now)
	}

	// Same goal generation: identity, state and every budget counter survive.
	if g.GoalID != "goal-e1" || g.State != generated.GoalStateActive || g.ActiveSessionID != "session-e1" {
		t.Errorf("identity changed: id=%q state=%q session=%q", g.GoalID, g.State, g.ActiveSessionID)
	}
	if g.Round != oldRound || g.QuestionRoundsUsed != 1 || g.ZeroOutputPushes != 1 {
		t.Errorf("counters changed: round=%d (want %d) question_rounds=%d zero_output_pushes=%d",
			g.Round, oldRound, g.QuestionRoundsUsed, g.ZeroOutputPushes)
	}
	if err := g.Validate(); err != nil {
		t.Errorf("restated record fails Validate: %v", err)
	}
}

func TestRestate_SamePromptChangesNothing(t *testing.T) {
	g := activeTestGoalWithOutcome(t)
	before := len(g.Criteria)
	changed, err := g.Restate("  make the tests pass\n", restateFloorDoD(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Restate: %v", err)
	}
	if changed {
		t.Fatal("re-sending the identical intent must not supersede the record")
	}
	if len(g.Criteria) != before || len(g.SupersededCriteria) != 0 || g.LatestVerdict == nil || g.Definition == "" {
		t.Errorf("record mutated by a no-op restate: criteria=%d superseded=%d verdict=%v definition=%q",
			len(g.Criteria), len(g.SupersededCriteria), g.LatestVerdict, g.Definition)
	}
}

func TestRestate_RecordlessGoalAddsNoEmptyHistoryEntry(t *testing.T) {
	g := activeTestGoalWithOutcome(t)
	g.Criteria = nil
	if _, err := g.Restate("something else entirely", restateFloorDoD(), time.Now().UTC()); err != nil {
		t.Fatalf("Restate: %v", err)
	}
	if len(g.SupersededCriteria) != 0 {
		t.Errorf("SupersededCriteria = %+v, want none — there was no registered ladder to supersede", g.SupersededCriteria)
	}
	if g.Prompt != "something else entirely" {
		t.Errorf("Prompt = %q", g.Prompt)
	}
}

func TestRestate_Refusals(t *testing.T) {
	t.Run("defining", func(t *testing.T) {
		g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
		if _, err := g.Restate("new intent", restateFloorDoD(), time.Now().UTC()); err == nil {
			t.Fatal("a defining goal must not be restated")
		}
		if g.Prompt != "make the tests pass" {
			t.Errorf("refused restate mutated Prompt to %q", g.Prompt)
		}
	})
	t.Run("terminal", func(t *testing.T) {
		g := activeTestGoalWithOutcome(t)
		if err := g.Terminate(generated.GoalStateMet, "condition met", time.Now().UTC()); err != nil {
			t.Fatalf("Terminate: %v", err)
		}
		if _, err := g.Restate("new intent", restateFloorDoD(), time.Now().UTC()); err == nil {
			t.Fatal("a terminal goal must not be re-opened by a restate")
		}
		if len(g.Criteria) == 0 {
			t.Error("refused restate emptied a terminal record's criteria")
		}
	})
	t.Run("empty_prompt", func(t *testing.T) {
		g := activeTestGoalWithOutcome(t)
		_, err := g.Restate("   ", restateFloorDoD(), time.Now().UTC())
		if err == nil || !strings.Contains(err.Error(), "prompt is required") {
			t.Fatalf("err = %v, want a prompt-required refusal", err)
		}
		if len(g.Criteria) == 0 {
			t.Error("refused restate emptied the criteria")
		}
	})
	t.Run("empty_floor_dod", func(t *testing.T) {
		g := activeTestGoalWithOutcome(t)
		if _, err := g.Restate("new intent", nil, time.Now().UTC()); err == nil {
			t.Fatal("a restate must not leave the goal with no definition of done")
		}
		if len(g.Criteria) == 0 || g.Prompt != "make the tests pass" {
			t.Errorf("refused restate mutated the record: prompt=%q criteria=%d", g.Prompt, len(g.Criteria))
		}
	})
}
