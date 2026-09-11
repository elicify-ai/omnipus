// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestIsReservedCriterionID (FR-007) proves the three reserved id forms are
// recognised and that an ordinary UUID-shaped id is not.
func TestIsReservedCriterionID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{ReservedCriterionIDSoftTierImplicit, true},
		{ReservedCriterionIDGoalCondition, true},
		{"goal-dod-floor-no-secrets", true},
		{"goal-dod-floor-grounded-claims", true},
		{"goal-dod-floor-", true}, // the bare prefix itself
		{"550e8400-e29b-41d4-a716-446655440000", false},
		{"", false},
		{"soft-tier-implicitly-different", false}, // must not prefix-match past the exact reserved literal for the non-prefix reserved ids... see note below
	}
	for _, c := range cases {
		got := IsReservedCriterionID(c.id)
		if got != c.want {
			t.Errorf("IsReservedCriterionID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

// TestGoalNewRejectsReservedCriterionID (FR-007) proves a criterion carrying
// a reserved id is refused at construction — the gap task.NormalizeCriteria
// alone leaves open (it only mints an id when one is ABSENT, so an explicit
// collision passes through it unchanged).
func TestGoalNewRejectsReservedCriterionID(t *testing.T) {
	reserved := newTestCriterion(ReservedCriterionIDGoalCondition, "should be rejected")
	_, err := New(
		"session", "s1", "chat_compiled",
		"prompt", "definition",
		[]task.AcceptanceCriterion{reserved},
		[]task.AcceptanceCriterion{newTestCriterion("", "fine dod item")},
		20, time.Now().UTC(),
	)
	if err == nil {
		t.Fatal("New with a criterion carrying the reserved goal-condition id: want error, got nil")
	}
}

// TestGoalSetCriteriaRejectsReservedID mirrors
// TestGoalNewRejectsReservedCriterionID for the SetCriteria mutator.
func TestGoalSetCriteriaRejectsReservedID(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	reserved := newTestCriterion(ReservedCriterionIDSoftTierImplicit, "should be rejected")
	if err := g.SetCriteria([]task.AcceptanceCriterion{reserved}, time.Now().UTC()); err == nil {
		t.Fatal("SetCriteria with the reserved soft-tier-implicit id: want error, got nil")
	}
}

// TestGoalSetDoDRejectsEmpty (D11/D15) proves SetDoD refuses to leave a goal
// with no definition-of-done items.
func TestGoalSetDoDRejectsEmpty(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	if err := g.SetDoD(nil, time.Now().UTC()); err == nil {
		t.Fatal("SetDoD(nil): want error (D11: at least one DoD item is mandatory), got nil")
	}
	// The goal's existing (valid) DoD must be untouched by the rejected call.
	if len(g.DoD) == 0 {
		t.Error("SetDoD's rejected call must not have cleared the existing DoD")
	}
}

// TestGoalSetCriteriaNormalizesAndPersists proves SetCriteria mints a fresh
// id for a criterion with none supplied (delegating to
// task.NormalizeCriteria), and that the new list is what Validate/marshal
// sees afterward.
func TestGoalSetCriteriaNormalizesAndPersists(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	before := g.LastActivityAt
	time.Sleep(time.Millisecond)
	now := time.Now().UTC()
	if err := g.SetCriteria([]task.AcceptanceCriterion{newTestCriterion("", "new criterion")}, now); err != nil {
		t.Fatalf("SetCriteria: %v", err)
	}
	if len(g.Criteria) != 1 {
		t.Fatalf("Criteria len = %d, want 1", len(g.Criteria))
	}
	if g.Criteria[0].ID == "" {
		t.Error("SetCriteria did not mint an id for a criterion with none supplied")
	}
	if g.Criteria[0].Status != task.CritPending {
		t.Errorf("Criteria[0].Status = %q, want %q (a freshly-set criterion is unjudged)", g.Criteria[0].Status, task.CritPending)
	}
	if !g.LastActivityAt.After(before) {
		t.Error("SetCriteria did not bump LastActivityAt")
	}
	if err := g.Validate(); err != nil {
		t.Errorf("Validate after SetCriteria: %v", err)
	}
}

// TestGoalSupersedeCriteriaRetainsHistory (ADR-081 set_goal(mode: update))
// proves SupersedeCriteria snapshots the PRIOR lists into
// SupersededCriteria before installing the new ones, and that repeated
// supersessions accumulate rather than overwrite.
func TestGoalSupersedeCriteriaRetainsHistory(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	firstCriteria := g.Criteria
	firstDoD := g.DoD
	now1 := time.Now().UTC()

	newCriteria1 := []task.AcceptanceCriterion{newTestCriterion("", "revision 1 criterion")}
	newDoD1 := []task.AcceptanceCriterion{newTestCriterion("", "revision 1 dod")}
	if err := g.SupersedeCriteria(newCriteria1, newDoD1, now1); err != nil {
		t.Fatalf("SupersedeCriteria (1st): %v", err)
	}
	if len(g.SupersededCriteria) != 1 {
		t.Fatalf("SupersededCriteria len = %d, want 1", len(g.SupersededCriteria))
	}
	if g.SupersededCriteria[0].Criteria[0].Text != firstCriteria[0].Text {
		t.Errorf("first supersession did not snapshot the ORIGINAL criteria: got %q, want %q",
			g.SupersededCriteria[0].Criteria[0].Text, firstCriteria[0].Text)
	}
	if g.SupersededCriteria[0].DoD[0].Text != firstDoD[0].Text {
		t.Errorf("first supersession did not snapshot the ORIGINAL dod: got %q, want %q",
			g.SupersededCriteria[0].DoD[0].Text, firstDoD[0].Text)
	}
	if g.Criteria[0].Text != "revision 1 criterion" {
		t.Errorf("current Criteria not replaced: got %q", g.Criteria[0].Text)
	}

	now2 := now1.Add(time.Minute)
	newCriteria2 := []task.AcceptanceCriterion{newTestCriterion("", "revision 2 criterion")}
	newDoD2 := []task.AcceptanceCriterion{newTestCriterion("", "revision 2 dod")}
	if err := g.SupersedeCriteria(newCriteria2, newDoD2, now2); err != nil {
		t.Fatalf("SupersedeCriteria (2nd): %v", err)
	}
	if len(g.SupersededCriteria) != 2 {
		t.Fatalf("SupersededCriteria len after 2nd supersession = %d, want 2 (history must ACCUMULATE, not overwrite)", len(g.SupersededCriteria))
	}
	if g.SupersededCriteria[1].Criteria[0].Text != "revision 1 criterion" {
		t.Errorf("second entry did not snapshot revision 1's criteria: got %q", g.SupersededCriteria[1].Criteria[0].Text)
	}
	if g.Criteria[0].Text != "revision 2 criterion" {
		t.Errorf("current Criteria not updated to revision 2: got %q", g.Criteria[0].Text)
	}
}

// TestGoalSupersedeCriteriaRejectsEmptyDoD mirrors SetDoD's D11 rule for the
// supersede path.
func TestGoalSupersedeCriteriaRejectsEmptyDoD(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	err := g.SupersedeCriteria([]task.AcceptanceCriterion{newTestCriterion("", "c")}, nil, time.Now().UTC())
	if err == nil {
		t.Fatal("SupersedeCriteria with empty dod: want error, got nil")
	}
	if len(g.SupersededCriteria) != 0 {
		t.Error("a rejected SupersedeCriteria call must not have appended to SupersededCriteria")
	}
}

// TestValidateCriteriaListRejectsInvalidShape proves validateCriteriaList
// surfaces task.NormalizeCriteria's own shape errors (not just the
// reserved-id check this package adds) — e.g. a check-kind criterion with no
// command.
func TestValidateCriteriaListRejectsInvalidShape(t *testing.T) {
	bad := task.AcceptanceCriterion{
		Kind:     task.KindCheck,
		Judgment: task.JudgmentBoolean,
		Text:     "a check with no command",
		Author:   task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "u1"},
		// Check is nil — invalid for KindCheck.
	}
	err := validateCriteriaList([]task.AcceptanceCriterion{bad}, "criteria")
	if err == nil {
		t.Fatal("validateCriteriaList with an invalid check criterion: want error, got nil")
	}
	if errors.Is(err, ErrOwnerNotFound) {
		t.Error("sanity: this error must not be ErrOwnerNotFound")
	}
}

// TestFloorDoDIDsPersistInDoDButNotCriteria pins the GOAL-FR-007 carve-out
// found by wave E4: the three reserved id forms are reserved for two
// different reasons, and only two of them are "never persisted".
//
// soft-tier-implicit and goal-condition are ephemeral — minted at judge or
// compile time and never written to a record — so they are rejected in both
// lists. The goal-dod-floor-* prefix is fixed-identity, not ephemeral: the
// floor Definition of Done is deliberately persisted with stable sentinel
// ids so it is byte-stable across reloads, and so pkg/agent/goal_compile.go's
// newFloorDoD and pkg/tools/set_goal.go's setGoalFloorDoD write records that
// are indistinguishable on disk. Rejecting it outright made every set_goal
// call that omits an explicit dod fail, because that is the path that
// backfills the floor.
func TestFloorDoDIDsPersistInDoDButNotCriteria(t *testing.T) {
	floor := func(id string) task.AcceptanceCriterion {
		return task.AcceptanceCriterion{
			ID: id, Kind: task.KindProse, Judgment: task.JudgmentBoolean,
			Provenance: task.ProvenanceFloor,
			Text:       "No secrets or credentials appear in the output.",
			Author:     task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "system"},
			Status:     task.CritPending,
		}
	}
	cases := []struct {
		name    string
		id      string
		list    string
		wantErr bool
	}{
		{"floor id is legal in the dod list", "goal-dod-floor-no-secrets", "dod", false},
		{"second floor id is legal in the dod list", "goal-dod-floor-grounded-claims", "dod", false},
		{"floor id is a namespace collision in the criteria list", "goal-dod-floor-no-secrets", "criteria", true},
		{"soft-tier-implicit stays banned in dod", ReservedCriterionIDSoftTierImplicit, "dod", true},
		{"soft-tier-implicit stays banned in criteria", ReservedCriterionIDSoftTierImplicit, "criteria", true},
		{"goal-condition stays banned in dod", ReservedCriterionIDGoalCondition, "dod", true},
		{"goal-condition stays banned in criteria", ReservedCriterionIDGoalCondition, "criteria", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCriteriaList([]task.AcceptanceCriterion{floor(tc.id)}, tc.list)
			if tc.wantErr && err == nil {
				t.Fatalf("validateCriteriaList(%q, %q): want an error, got nil", tc.id, tc.list)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateCriteriaList(%q, %q): want nil, got %v", tc.id, tc.list, err)
			}
		})
	}
}
