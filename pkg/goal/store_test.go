// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

// TestNewStore_CreatesEntityDirectory proves NewStore pre-creates
// $OMNIPUS_HOME/entities/goals — the same fresh-install fix
// pkg/agentstore.New applies (a bare entity.Store's first Create would
// otherwise fail opening its sidecar lock file before any directory
// exists).
func TestNewStore_CreatesEntityDirectory(t *testing.T) {
	home := t.TempDir()
	s := NewStore(home)
	wantDir := filepath.Join(home, "entities", "goals")
	if s.Dir() != wantDir {
		t.Errorf("Dir() = %q, want %q", s.Dir(), wantDir)
	}
	info, err := os.Stat(wantDir)
	if err != nil {
		t.Fatalf("entities/goals directory was not pre-created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q exists but is not a directory", wantDir)
	}
}

// TestStore_CreateGetRoundTrip proves a Create persists a goal that Get can
// then read back, byte-for-byte on the fields that matter.
func TestStore_CreateGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.GoalID == "" {
		t.Fatal("Create did not stamp a GoalID")
	}

	got, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GoalID != g.GoalID {
		t.Errorf("GoalID = %q, want %q", got.GoalID, g.GoalID)
	}
	if got.OwnerKind != g.OwnerKind || got.OwnerID != g.OwnerID {
		t.Errorf("owner mismatch: got %s/%s, want %s/%s", got.OwnerKind, got.OwnerID, g.OwnerKind, g.OwnerID)
	}
	if got.Prompt != g.Prompt {
		t.Errorf("Prompt = %q, want %q", got.Prompt, g.Prompt)
	}
	if len(got.Criteria) != len(g.Criteria) || len(got.DoD) != len(g.DoD) {
		t.Errorf("criteria/dod length mismatch: got %d/%d, want %d/%d", len(got.Criteria), len(got.DoD), len(g.Criteria), len(g.DoD))
	}
}

// TestStore_GetNotFound proves Get on an unknown id wraps entity.ErrNotFound
// so errors.Is keeps working through this package's wrapper.
func TestStore_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Get("does-not-exist")
	if !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("Get error = %v, want errors.Is(err, entity.ErrNotFound)", err)
	}
}

// TestStore_CreateRejectsInvalidGoal proves Create runs Goal.Validate
// before ever touching disk — an invalid goal must not create a file.
func TestStore_CreateRejectsInvalidGoal(t *testing.T) {
	s := newTestStore(t)
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	g.OwnerID = "" // invalid: FR-002 requires owner_id

	if err := s.Create(g); err == nil {
		t.Fatal("Create with empty owner_id: want error, got nil")
	}
	entries, _ := os.ReadDir(s.Dir())
	if len(entries) != 0 {
		t.Errorf("Create rejected the goal but still wrote %d file(s) to %q", len(entries), s.Dir())
	}
}

// TestStore_UpdateRoundTrip proves Update's mutate closure result is what a
// subsequent Get observes — the single-writer path C-09 requires.
func TestStore_UpdateRoundTrip(t *testing.T) {
	s := newTestStore(t)
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	now := time.Now().UTC()
	updated, err := s.Update(g.GoalID, func(gg *Goal) error {
		return gg.Activate("sess-1", now)
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.State != generated.GoalStateActive {
		t.Errorf("Update's return value: State = %q, want active", updated.State)
	}

	// C-09's oracle: read back from the STORE, not from the in-memory value
	// Update returned, to prove the mutation was actually persisted.
	reread, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if reread.State != generated.GoalStateActive {
		t.Errorf("re-read State = %q, want active (the mutation must be durably persisted, not just returned)", reread.State)
	}
	if reread.ActiveSessionID != "sess-1" {
		t.Errorf("re-read ActiveSessionID = %q, want %q", reread.ActiveSessionID, "sess-1")
	}
}

// TestStore_UpdateAttemptsUsedObservableAfterCall is C-09's exact named
// oracle: "the goal record's attempts-used counter, read back from
// pkg/goal's store after the call, is unchanged" — proven here for the
// POSITIVE case (it DOES change when RecordAttempt is called) so a
// regression that stops persisting AttemptsUsed is caught: a test that only
// asserted "unchanged" would pass vacuously against an implementation that
// never persisted anything.
func TestStore_UpdateAttemptsUsedObservableAfterCall(t *testing.T) {
	s := newTestStore(t)
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	before, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get (before): %v", err)
	}
	if before.AttemptsUsed != 0 {
		t.Fatalf("AttemptsUsed before any RecordAttempt = %d, want 0", before.AttemptsUsed)
	}

	if _, updErr := s.Update(g.GoalID, func(gg *Goal) error {
		gg.RecordAttempt(time.Now().UTC())
		return nil
	}); updErr != nil {
		t.Fatalf("Update: %v", updErr)
	}

	after, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get (after): %v", err)
	}
	if after.AttemptsUsed != 1 {
		t.Fatalf("AttemptsUsed after one RecordAttempt, re-read from the store = %d, want 1", after.AttemptsUsed)
	}
}

// TestStore_UpdateNotFound proves Update on an unknown id fails rather than
// silently creating a record — a read-modify-write has no "modify" without
// a "read".
func TestStore_UpdateNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Update("does-not-exist", func(g *Goal) error { return nil })
	if !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("Update error = %v, want errors.Is(err, entity.ErrNotFound)", err)
	}
}

// TestStore_UpdateMutateErrorAbortsWrite proves a mutate closure returning
// an error writes NOTHING — the read-modify-write cycle is all-or-nothing.
func TestStore_UpdateMutateErrorAbortsWrite(t *testing.T) {
	s := newTestStore(t)
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	sentinel := errors.New("mutate refused")
	_, err := s.Update(g.GoalID, func(gg *Goal) error {
		gg.LatestReason = "should never be persisted"
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Update error = %v, want errors.Is(err, sentinel)", err)
	}

	got, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LatestReason != "" {
		t.Errorf("LatestReason = %q, want empty — a failed mutate must not have persisted", got.LatestReason)
	}
}

// TestStore_Delete proves Delete removes the record and a subsequent Get
// reports not-found.
func TestStore_Delete(t *testing.T) {
	s := newTestStore(t)
	g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.Delete(g.GoalID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(g.GoalID); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("Get after Delete: error = %v, want errors.Is(err, entity.ErrNotFound)", err)
	}
}

// TestStore_ListSortedAndComplete proves List returns every created record.
func TestStore_ListSortedAndComplete(t *testing.T) {
	s := newTestStore(t)
	ids := map[string]bool{}
	for i := 0; i < 3; i++ {
		g := newTestGoal(t, generated.GoalOwnerKindSession, "s1")
		if err := s.Create(g); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids[g.GoalID] = true
		time.Sleep(time.Millisecond) // keep CreatedAt strictly increasing for a stable sort
	}

	got, skipped, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped = %v, want none", skipped)
	}
	if len(got) != 3 {
		t.Fatalf("List returned %d records, want 3", len(got))
	}
	for _, g := range got {
		if !ids[g.GoalID] {
			t.Errorf("List returned unexpected goal id %q", g.GoalID)
		}
		delete(ids, g.GoalID)
	}
	if len(ids) != 0 {
		t.Errorf("List did not return %d created goal(s): %v", len(ids), ids)
	}
}
