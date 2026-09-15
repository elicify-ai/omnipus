// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"errors"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

// alwaysExists and neverExists are the two trivial sessionExists predicates
// most of this file's tests need; a couple of tests build a set-backed one
// instead where more than one session id is involved.
func alwaysExists(string) bool { return true }
func neverExists(string) bool  { return false }

// mustCreateTerminal creates, persists and terminates (with State/reason as
// given) a goal for ownerID, then force-sets its LastActivityAt to
// `lastActivityAt` via a direct Update — Terminate itself always stamps
// LastActivityAt to the "now" it is called with, so a second, explicit
// backdating step is required to build a fixture that is old enough (or
// young enough) for a specific retention-window test.
func mustCreateTerminal(t *testing.T, s *Store, ownerKind generated.GoalOwnerKind, ownerID string, state generated.GoalState, lastActivityAt time.Time) *Goal {
	t.Helper()
	g := newTestGoal(t, ownerKind, ownerID)
	activatedAt := lastActivityAt.Add(-time.Hour)
	if err := g.Activate("sess-"+ownerID, activatedAt); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := g.Terminate(state, "test fixture", lastActivityAt); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if _, err := s.Update(g.GoalID, func(gg *Goal) error {
		gg.State = g.State
		gg.TerminalReason = g.TerminalReason
		gg.LastActivityAt = lastActivityAt
		return nil
	}); err != nil {
		t.Fatalf("Update (terminate+backdate): %v", err)
	}
	got, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get after terminate: %v", err)
	}
	return got
}

// TestGoalRecordsSweptOnSessionRetention is GOAL-FR-043's named test
// (S-40): "Given one goal record older than the retention window and one
// inside it, when the sweep runs, then the first is removed and the second
// is kept." Mirrors pkg/session/retention_sweep_test.go's own
// TestRetentionSweep_DeletesAgedFiles in spirit: same cutoff arithmetic,
// same "older survives, inside kept" shape, applied to goal records instead
// of session transcript files.
func TestGoalRecordsSweptOnSessionRetention(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	old := mustCreateTerminal(t, s, generated.GoalOwnerKindSession, "aged",
		generated.GoalStateMet, now.Add(-10*24*time.Hour))
	recent := mustCreateTerminal(t, s, generated.GoalOwnerKindSession, "fresh",
		generated.GoalStateMet, now.Add(-3*24*time.Hour))

	removed, expired, err := Sweep(s, 7, alwaysExists, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (only the 10-day-old record)", removed)
	}
	if expired != 0 {
		t.Errorf("expired = %d, want 0 (no dangling active goal in this fixture)", expired)
	}

	if _, getErr := s.Get(old.GoalID); !errors.Is(getErr, entity.ErrNotFound) {
		t.Errorf("Get(%q) after sweep: error = %v, want errors.Is(err, entity.ErrNotFound) — the 10-day-old record must be gone", old.GoalID, getErr)
	}
	got, err := s.Get(recent.GoalID)
	if err != nil {
		t.Fatalf("Get(%q) after sweep: %v — the 3-day-old record must survive a 7-day window", recent.GoalID, err)
	}
	if got.State != generated.GoalStateMet {
		t.Errorf("surviving record state = %q, want unchanged %q", got.State, generated.GoalStateMet)
	}
}

// TestGoalRecordsSweptOnSessionRetention_ActiveGoalNeverAgedOut proves the
// age-based half of Sweep (FR-043) never removes a non-terminal record no
// matter how stale its LastActivityAt is — D14's "the record is now
// retained" concern is about terminal records piling up (US-10's title),
// not an active goal doing its job. Backdating an ACTIVE goal's
// LastActivityAt far past the window and running Sweep with its session
// still resolving (alwaysExists) must leave it completely untouched.
func TestGoalRecordsSweptOnSessionRetention_ActiveGoalNeverAgedOut(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	g := newTestGoal(t, generated.GoalOwnerKindSession, "long-runner")
	if err := g.Activate("sess-long-runner", now.Add(-90*24*time.Hour)); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Update(g.GoalID, func(gg *Goal) error {
		gg.LastActivityAt = now.Add(-90 * 24 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("Update (backdate): %v", err)
	}

	removed, expired, err := Sweep(s, 7, alwaysExists, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 0 || expired != 0 {
		t.Fatalf("removed=%d expired=%d, want 0/0 — an active goal with a resolving session must never be touched by age alone", removed, expired)
	}
	if _, err := s.Get(g.GoalID); err != nil {
		t.Errorf("Get after sweep: %v — a 90-day-idle but still ACTIVE goal must survive a 7-day sweep", err)
	}
}

// TestGoalDoesNotOutliveOwner is GOAL-FR-044's named test (S-41/EC-10): "A
// goal record exists whose active session was swept by retention -> it is
// terminal-expired at the next sweep rather than left pointing at a
// missing session." This covers the half of FR-044 this wave (S3) owns —
// the OTHER half, a task's own deletion cascading to its goal record, is a
// direct call from pkg/task/store.go (wave E5) and is not exercised here.
func TestGoalDoesNotOutliveOwner(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	g := mustCreateActive(t, s, generated.GoalOwnerKindSession, "orphaned")
	sessionID := g.ActiveSessionID
	if sessionID == "" {
		t.Fatal("fixture bug: mustCreateActive must set ActiveSessionID")
	}

	// Pass 1: the goal's session no longer exists. It must be
	// terminal-expired right now, but NOT removed in this same pass —
	// Terminate stamps LastActivityAt to `now`, so a 30-day window has
	// nothing yet to remove.
	removed, expired, err := Sweep(s, 30, neverExists, now)
	if err != nil {
		t.Fatalf("Sweep (pass 1): %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1 — the dangling active goal must be terminal-expired", expired)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0 — a goal expired in THIS pass must not also be deleted in it", removed)
	}

	got, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get after pass 1: %v — EC-10 keeps the record, it does not erase it", err)
	}
	if got.State != generated.GoalStateExpired {
		t.Errorf("State after pass 1 = %q, want %q", got.State, generated.GoalStateExpired)
	}
	if got.TerminalReason == "" {
		t.Error("TerminalReason must be set when a goal is terminal-expired for a missing session")
	}
	if got.ActiveSessionID != sessionID {
		t.Errorf("ActiveSessionID = %q, want unchanged %q (Terminate freezes it, never blanks it)", got.ActiveSessionID, sessionID)
	}

	// Pass 2, "the next sweep" (EC-10's own wording): once the
	// now-terminal record itself ages past the window, it is removed like
	// any other terminal record (FR-043's ordinary rule, exercised again
	// here to prove the two FRs compose into one lifecycle rather than
	// needing separate machinery).
	later := now.Add(31 * 24 * time.Hour)
	removed, expired, err = Sweep(s, 30, neverExists, later)
	if err != nil {
		t.Fatalf("Sweep (pass 2): %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1 — the now-aged expired record must be removed on the next sweep", removed)
	}
	if expired != 0 {
		t.Fatalf("expired = %d, want 0 — nothing left to expire in pass 2", expired)
	}
	if _, err := s.Get(g.GoalID); !errors.Is(err, entity.ErrNotFound) {
		t.Errorf("Get after pass 2: error = %v, want errors.Is(err, entity.ErrNotFound)", err)
	}
}

// TestGoalDoesNotOutliveOwner_ExistingSessionIsUntouched is the negative
// twin of TestGoalDoesNotOutliveOwner: an active goal whose session DOES
// still resolve must never be terminal-expired, however the retention
// window is set.
func TestGoalDoesNotOutliveOwner_ExistingSessionIsUntouched(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	g := mustCreateActive(t, s, generated.GoalOwnerKindSession, "still-running")

	removed, expired, err := Sweep(s, 1, alwaysExists, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 0 || expired != 0 {
		t.Fatalf("removed=%d expired=%d, want 0/0 — the owning session still exists", removed, expired)
	}
	got, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get after sweep: %v", err)
	}
	if got.State != generated.GoalStateActive {
		t.Errorf("State = %q, want unchanged %q", got.State, generated.GoalStateActive)
	}
}

// TestGoalDoesNotOutliveOwner_TaskOwnedGoalAlsoExpires proves EC-10 applies
// identically to a task-owned goal, not only a session-owned one — GOAL-FR-002's
// owner reference is orthogonal to which owner kind dangles.
func TestGoalDoesNotOutliveOwner_TaskOwnedGoalAlsoExpires(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	g := mustCreateActive(t, s, generated.GoalOwnerKindTask, "task-1")

	_, expired, err := Sweep(s, 30, neverExists, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	got, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != generated.GoalStateExpired {
		t.Errorf("State = %q, want %q", got.State, generated.GoalStateExpired)
	}
}

// TestSweep_ZeroRetentionIsNoOp mirrors
// pkg/session/retention_sweep_test.go's TestRetentionSweep_ZeroRetentionIsNoOp:
// retentionDays <= 0 must not touch anything, including an obviously
// eligible (aged terminal, and separately dangling active) record.
func TestSweep_ZeroRetentionIsNoOp(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	aged := mustCreateTerminal(t, s, generated.GoalOwnerKindSession, "aged",
		generated.GoalStateMet, now.Add(-365*24*time.Hour))
	dangling := mustCreateActive(t, s, generated.GoalOwnerKindSession, "dangling")

	for _, days := range []int{0, -1, -100} {
		removed, expired, err := Sweep(s, days, neverExists, now)
		if err != nil {
			t.Fatalf("Sweep(%d): %v", days, err)
		}
		if removed != 0 || expired != 0 {
			t.Fatalf("Sweep(%d): removed=%d expired=%d, want 0/0", days, removed, expired)
		}
	}

	if _, err := s.Get(aged.GoalID); err != nil {
		t.Errorf("aged record must survive retentionDays<=0: %v", err)
	}
	got, err := s.Get(dangling.GoalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != generated.GoalStateActive {
		t.Errorf("State = %q, want unchanged %q — retentionDays<=0 must not terminal-expire either", got.State, generated.GoalStateActive)
	}
}

// TestSweep_NilArgumentsError proves Sweep reports both defensive nil
// cases as errors rather than panicking or silently doing nothing that
// looks like a legitimate empty sweep.
func TestSweep_NilArgumentsError(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	if _, _, err := Sweep(nil, 7, alwaysExists, now); err == nil {
		t.Error("Sweep(nil store, ...) must return an error")
	}
	if _, _, err := Sweep(s, 7, nil, now); err == nil {
		t.Error("Sweep(..., nil sessionExists, ...) must return an error")
	}
}

// TestSweep_DefiningGoalNeverAgedOut proves a goal still in the defining
// phase (not yet active, GOAL-FR-009/D2) — which has no ActiveSessionID at
// all — is left alone by both passes even when badly stale.
func TestSweep_DefiningGoalNeverAgedOut(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	g := newTestGoal(t, generated.GoalOwnerKindSession, "still-defining")
	if err := s.Create(g); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Update(g.GoalID, func(gg *Goal) error {
		gg.LastActivityAt = now.Add(-365 * 24 * time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("Update (backdate): %v", err)
	}

	removed, expired, err := Sweep(s, 7, neverExists, now)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 0 || expired != 0 {
		t.Fatalf("removed=%d expired=%d, want 0/0 — a defining-phase goal has no ActiveSessionID to dangle and is not terminal", removed, expired)
	}
	if _, err := s.Get(g.GoalID); err != nil {
		t.Errorf("Get after sweep: %v", err)
	}
}
