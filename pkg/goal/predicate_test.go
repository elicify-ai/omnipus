// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"errors"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

// mustCreateActive creates and activates a goal for ownerID, persists it,
// and returns the persisted record.
func mustCreateActive(t *testing.T, s *Store, ownerKind generated.GoalOwnerKind, ownerID string) *Goal {
	t.Helper()
	g := newTestGoal(t, ownerKind, ownerID)
	now := time.Now().UTC()
	if err := g.Activate("sess-"+ownerID, now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return g
}

// TestActiveGoalExists_C25 proves C-25's single predicate: false when no
// record exists, true once one is active, false again once it is
// terminated — the exact "does a goal exist" replacement for
// GoalCondition != "".
func TestActiveGoalExists_C25(t *testing.T) {
	s := newTestStore(t)

	exists, err := s.ActiveGoalExists(generated.GoalOwnerKindSession, "s1")
	if err != nil {
		t.Fatalf("ActiveGoalExists (none yet): %v", err)
	}
	if exists {
		t.Fatal("ActiveGoalExists = true before any goal was created")
	}

	g := mustCreateActive(t, s, generated.GoalOwnerKindSession, "s1")

	exists, err = s.ActiveGoalExists(generated.GoalOwnerKindSession, "s1")
	if err != nil {
		t.Fatalf("ActiveGoalExists (active): %v", err)
	}
	if !exists {
		t.Fatal("ActiveGoalExists = false for an active goal")
	}

	if _, err := s.Update(g.GoalID, func(gg *Goal) error {
		return gg.Terminate(generated.GoalStateMet, "done", time.Now().UTC())
	}); err != nil {
		t.Fatalf("Update (terminate): %v", err)
	}

	exists, err = s.ActiveGoalExists(generated.GoalOwnerKindSession, "s1")
	if err != nil {
		t.Fatalf("ActiveGoalExists (terminal): %v", err)
	}
	if exists {
		t.Fatal("ActiveGoalExists = true for a TERMINAL goal (D9: the record survives, but it is no longer active)")
	}
}

// TestActiveGoalExists_DifferentOwnerNotConfused proves the predicate is
// keyed on the full (owner_kind, owner_id) pair — an active goal for a
// different owner must not make this owner report true.
func TestActiveGoalExists_DifferentOwnerNotConfused(t *testing.T) {
	s := newTestStore(t)
	mustCreateActive(t, s, generated.GoalOwnerKindSession, "s1")

	exists, err := s.ActiveGoalExists(generated.GoalOwnerKindSession, "s2")
	if err != nil {
		t.Fatalf("ActiveGoalExists: %v", err)
	}
	if exists {
		t.Fatal("ActiveGoalExists = true for an owner with no goal of its own")
	}

	exists, err = s.ActiveGoalExists(generated.GoalOwnerKindTask, "s1")
	if err != nil {
		t.Fatalf("ActiveGoalExists: %v", err)
	}
	if exists {
		t.Fatal("ActiveGoalExists must not match on owner_id alone across a DIFFERENT owner_kind")
	}
}

// TestListActive_OnlyActiveGoals proves ListActive returns exactly the
// active-state records, excluding defining and terminal ones.
func TestListActive_OnlyActiveGoals(t *testing.T) {
	s := newTestStore(t)

	// A defining (never-activated) task-owned goal — must NOT appear.
	defining := newTestGoal(t, generated.GoalOwnerKindTask, "task-defining")
	if err := s.Create(defining); err != nil {
		t.Fatalf("Create (defining): %v", err)
	}

	// An active session-owned goal — must appear.
	active := mustCreateActive(t, s, generated.GoalOwnerKindSession, "session-active")

	// A terminal task-owned goal — must NOT appear.
	terminalGoal := mustCreateActive(t, s, generated.GoalOwnerKindTask, "task-terminal")
	if _, err := s.Update(terminalGoal.GoalID, func(g *Goal) error {
		return g.Terminate(generated.GoalStateExhausted, "budget", time.Now().UTC())
	}); err != nil {
		t.Fatalf("Update (terminate): %v", err)
	}

	got, err := s.ListActive()
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListActive returned %d records, want 1: %+v", len(got), got)
	}
	if got[0].GoalID != active.GoalID {
		t.Errorf("ListActive returned goal %q, want the active one %q", got[0].GoalID, active.GoalID)
	}
}

// TestListActiveByOwnerKind_ExcludesTaskGoals (GOAL-FR-049/D12, R-22) proves
// the session-only filter the active-goal admission counter needs: a
// task-owned active goal must not be counted when filtering to session.
func TestListActiveByOwnerKind_ExcludesTaskGoals(t *testing.T) {
	s := newTestStore(t)
	sessionGoal := mustCreateActive(t, s, generated.GoalOwnerKindSession, "s1")
	mustCreateActive(t, s, generated.GoalOwnerKindTask, "t1")

	sessionOnly, err := s.ListActiveByOwnerKind(generated.GoalOwnerKindSession)
	if err != nil {
		t.Fatalf("ListActiveByOwnerKind(session): %v", err)
	}
	if len(sessionOnly) != 1 || sessionOnly[0].GoalID != sessionGoal.GoalID {
		t.Errorf("ListActiveByOwnerKind(session) = %+v, want exactly [%q] (task-owned goals are EXEMPT from the admission counter, D12)", sessionOnly, sessionGoal.GoalID)
	}

	taskOnly, err := s.ListActiveByOwnerKind(generated.GoalOwnerKindTask)
	if err != nil {
		t.Fatalf("ListActiveByOwnerKind(task): %v", err)
	}
	if len(taskOnly) != 1 {
		t.Errorf("ListActiveByOwnerKind(task) = %+v, want exactly 1 record", taskOnly)
	}
}

// TestGetActiveByOwner_NotFound proves the ErrOwnerNotFound sentinel is
// returned (wrapped, errors.Is-compatible) for an owner with no active
// goal.
func TestGetActiveByOwner_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetActiveByOwner(generated.GoalOwnerKindSession, "nope")
	if !errors.Is(err, ErrOwnerNotFound) {
		t.Fatalf("GetActiveByOwner error = %v, want errors.Is(err, ErrOwnerNotFound)", err)
	}
}

// TestGetByOwner_TaskUniqueAcrossPhases (R-04) proves GetByOwner finds a
// task-owned goal's SINGLE record regardless of its current phase —
// defining, active, or terminal all resolve to the same one record.
func TestGetByOwner_TaskUniqueAcrossPhases(t *testing.T) {
	s := newTestStore(t)
	g := newTestGoal(t, generated.GoalOwnerKindTask, "task-1")
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	found, err := s.GetByOwner(generated.GoalOwnerKindTask, "task-1")
	if err != nil {
		t.Fatalf("GetByOwner (defining): %v", err)
	}
	if found.GoalID != g.GoalID {
		t.Errorf("GetByOwner returned %q, want %q", found.GoalID, g.GoalID)
	}

	now := time.Now().UTC()
	if _, err := s.Update(g.GoalID, func(gg *Goal) error { return gg.Activate("sess-1", now) }); err != nil {
		t.Fatalf("Update (activate): %v", err)
	}
	found, err = s.GetByOwner(generated.GoalOwnerKindTask, "task-1")
	if err != nil {
		t.Fatalf("GetByOwner (active): %v", err)
	}
	if found.GoalID != g.GoalID {
		t.Errorf("GetByOwner returned %q, want %q", found.GoalID, g.GoalID)
	}
}

// TestCreate_RefusesSecondGoalForSameTask (R-04, ErrOwnerAlreadyHasGoal)
// proves the one-goal-per-task-for-its-whole-life invariant is enforced at
// Create, in every phase — including a still-defining one, not only an
// active or terminal one.
func TestCreate_RefusesSecondGoalForSameTask(t *testing.T) {
	s := newTestStore(t)
	first := newTestGoal(t, generated.GoalOwnerKindTask, "task-1")
	if err := s.Create(first); err != nil {
		t.Fatalf("Create (first): %v", err)
	}

	second := newTestGoal(t, generated.GoalOwnerKindTask, "task-1")
	err := s.Create(second)
	if !errors.Is(err, ErrOwnerAlreadyHasGoal) {
		t.Fatalf("Create (second, same task, still defining): error = %v, want errors.Is(err, ErrOwnerAlreadyHasGoal)", err)
	}
}

// TestCreate_AllowsMultipleGoalsForSameSession proves a session-owned goal
// has NO such uniqueness constraint — ADR-081 D1 allows a session to open
// more than one /goal across its life (each prior one having gone
// terminal).
func TestCreate_AllowsMultipleGoalsForSameSession(t *testing.T) {
	s := newTestStore(t)
	first := mustCreateActive(t, s, generated.GoalOwnerKindSession, "session-1")
	if _, err := s.Update(first.GoalID, func(g *Goal) error {
		return g.Terminate(generated.GoalStateMet, "done", time.Now().UTC())
	}); err != nil {
		t.Fatalf("Update (terminate first): %v", err)
	}

	second := newTestGoal(t, generated.GoalOwnerKindSession, "session-1")
	if err := s.Create(second); err != nil {
		t.Fatalf("Create (second goal, same session, after first went terminal): %v", err)
	}
}
