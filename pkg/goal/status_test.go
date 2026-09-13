// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestIsTerminalState(t *testing.T) {
	cases := []struct {
		state generated.GoalState
		want  bool
	}{
		{generated.GoalStateDefining, false},
		{generated.GoalStateActive, false},
		{generated.GoalStateMet, true},
		{generated.GoalStateExhausted, true},
		{generated.GoalStateExpired, true},
		{generated.GoalStateCleared, true},
	}
	for _, c := range cases {
		if got := IsTerminalState(c.state); got != c.want {
			t.Errorf("IsTerminalState(%q) = %v, want %v", c.state, got, c.want)
		}
	}
}

// TestActivateFromDefining proves the normal defining->active transition
// (D2, GOAL-FR-010): binds to a session and stamps StartedAt.
func TestActivateFromDefining(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()
	if err := g.Activate("sess-1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if g.State != generated.GoalStateActive {
		t.Errorf("State = %q, want active", g.State)
	}
	if g.ActiveSessionID != "sess-1" {
		t.Errorf("ActiveSessionID = %q, want %q", g.ActiveSessionID, "sess-1")
	}
	if g.StartedAt == nil || !g.StartedAt.Equal(now) {
		t.Errorf("StartedAt = %v, want %v", g.StartedAt, now)
	}
}

// TestActivateRejectsNonDefining proves Activate refuses a goal that is
// already active or terminal — activation is a ONE-WAY transition out of
// defining.
func TestActivateRejectsNonDefining(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()
	if err := g.Activate("sess-1", now); err != nil {
		t.Fatalf("first Activate: %v", err)
	}
	if err := g.Activate("sess-2", now); err == nil {
		t.Fatal("second Activate on an already-active goal: want error, got nil")
	}
	if g.ActiveSessionID != "sess-1" {
		t.Errorf("a rejected second Activate must not have re-pointed ActiveSessionID: got %q", g.ActiveSessionID)
	}
}

// TestActivateRejectsEmptySessionID proves a session id is mandatory.
func TestActivateRejectsEmptySessionID(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	if err := g.Activate("", time.Now().UTC()); err == nil {
		t.Fatal("Activate(\"\"): want error, got nil")
	}
}

// TestTerminateFreezesActiveSessionID (Goal.yaml's active_session_id
// description, EC-10) proves Terminate does NOT blank ActiveSessionID — the
// terminal record must still name the session that carried it via the last
// value the field held.
func TestTerminateFreezesActiveSessionID(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()
	if err := g.Activate("sess-1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := g.Terminate(generated.GoalStateMet, "all criteria satisfied", now.Add(time.Minute)); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if g.State != generated.GoalStateMet {
		t.Errorf("State = %q, want met", g.State)
	}
	if g.TerminalReason != "all criteria satisfied" {
		t.Errorf("TerminalReason = %q, want %q", g.TerminalReason, "all criteria satisfied")
	}
	if g.ActiveSessionID != "sess-1" {
		t.Errorf("ActiveSessionID = %q, want it FROZEN at %q, not blanked (D9/EC-10)", g.ActiveSessionID, "sess-1")
	}
}

// TestTerminateRejectsNonTerminalTargetState proves Terminate refuses a
// target state that is not one of the four terminal values.
func TestTerminateRejectsNonTerminalTargetState(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()
	if err := g.Activate("sess-1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := g.Terminate(generated.GoalStateActive, "bogus", now); err == nil {
		t.Fatal("Terminate(active): want error (active is not terminal), got nil")
	}
	if err := g.Terminate(generated.GoalStateDefining, "bogus", now); err == nil {
		t.Fatal("Terminate(defining): want error (defining is not terminal), got nil")
	}
}

// TestTerminateRejectsNonActiveSource proves Terminate refuses a goal that
// is still in the defining phase (never activated, so there is nothing to
// end).
func TestTerminateRejectsNonActiveSource(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	if err := g.Terminate(generated.GoalStateMet, "reason", time.Now().UTC()); err == nil {
		t.Fatal("Terminate on a still-defining goal: want error, got nil")
	}
}

// TestReactivateRefusesSessionOwnedGoal (R-04: "A session-owned goal has no
// such re-entry edge — a terminal chat goal stays terminal") is the
// negative half of R-04's answer to OQ-10.
func TestReactivateRefusesSessionOwnedGoal(t *testing.T) {
	g := newTestGoal(t, "session", "s1")
	now := time.Now().UTC()
	if err := g.Activate("sess-1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := g.Terminate(generated.GoalStateMet, "done", now); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if err := g.Reactivate("sess-2", now.Add(time.Hour)); err == nil {
		t.Fatal("Reactivate on a session-owned terminal goal: want error, got nil (R-04: a session-owned goal has no re-entry edge)")
	}
	if g.State != generated.GoalStateMet {
		t.Errorf("a refused Reactivate must not have changed State: got %q", g.State)
	}
}

// TestReactivateTaskOwnedGoal (R-04, the answer to OQ-10) proves the full
// contract: "resetting attempts_used and rounds_used to 0, clearing
// latest_reason and the current verdict, and appending the prior verdict to
// a retained history array rather than overwriting it."
func TestReactivateTaskOwnedGoal(t *testing.T) {
	g := newTestGoal(t, "task", "task-1")
	now := time.Now().UTC()
	if err := g.Activate("run-1-session", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	v := &task.JudgeVerdict{ID: "v1", Scope: task.VerdictScopeGoal, Met: false}
	if err := g.RecordVerdict(v, "3 tests still failing", now); err != nil {
		t.Fatalf("RecordVerdict: %v", err)
	}
	g.RecordAttempt(now)
	g.RecordAttempt(now)
	if err := g.Terminate(generated.GoalStateExhausted, "round budget exhausted", now.Add(time.Minute)); err != nil {
		t.Fatalf("Terminate: %v", err)
	}

	priorRound := g.Round
	priorAttempts := g.AttemptsUsed
	priorState := g.State
	priorReason := g.TerminalReason

	reactivateAt := now.Add(time.Hour)
	if err := g.Reactivate("run-2-session", reactivateAt); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}

	if g.State != generated.GoalStateActive {
		t.Errorf("State = %q, want active", g.State)
	}
	if g.ActiveSessionID != "run-2-session" {
		t.Errorf("ActiveSessionID = %q, want %q (re-pointed to the NEW run's session)", g.ActiveSessionID, "run-2-session")
	}
	if g.AttemptsUsed != 0 {
		t.Errorf("AttemptsUsed = %d, want reset to 0", g.AttemptsUsed)
	}
	if g.Round != 0 {
		t.Errorf("Round = %d, want reset to 0", g.Round)
	}
	if g.LatestReason != "" {
		t.Errorf("LatestReason = %q, want cleared", g.LatestReason)
	}
	if g.TerminalReason != "" {
		t.Errorf("TerminalReason = %q, want cleared", g.TerminalReason)
	}
	if g.LatestVerdict != nil {
		t.Error("LatestVerdict must be cleared (\"clearing ... the current verdict\")")
	}
	if g.StartedAt == nil || !g.StartedAt.Equal(reactivateAt) {
		t.Errorf("StartedAt = %v, want %v (a fresh start for the new run)", g.StartedAt, reactivateAt)
	}

	// The prior run's outcome must be APPENDED to TerminalHistory, not lost.
	if len(g.TerminalHistory) != 1 {
		t.Fatalf("TerminalHistory len = %d, want 1", len(g.TerminalHistory))
	}
	entry := g.TerminalHistory[0]
	if entry.State != priorState {
		t.Errorf("TerminalHistory[0].State = %q, want %q", entry.State, priorState)
	}
	if entry.TerminalReason != priorReason {
		t.Errorf("TerminalHistory[0].TerminalReason = %q, want %q", entry.TerminalReason, priorReason)
	}
	if entry.Round != priorRound {
		t.Errorf("TerminalHistory[0].Round = %d, want %d", entry.Round, priorRound)
	}
	if entry.AttemptsUsed != priorAttempts {
		t.Errorf("TerminalHistory[0].AttemptsUsed = %d, want %d", entry.AttemptsUsed, priorAttempts)
	}
	if entry.Verdict == nil || entry.Verdict.ID != v.ID {
		t.Errorf("TerminalHistory[0].Verdict = %+v, want the prior run's verdict %+v", entry.Verdict, v)
	}
}

// TestReactivateAccumulatesHistoryAcrossMultipleReruns proves a SECOND
// re-run appends a second entry rather than overwriting the first — a
// task's full adjudication history across every re-run must survive.
func TestReactivateAccumulatesHistoryAcrossMultipleReruns(t *testing.T) {
	g := newTestGoal(t, "task", "task-1")
	now := time.Now().UTC()

	if err := g.Activate("run-1", now); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := g.Terminate(generated.GoalStateExhausted, "run 1 exhausted", now); err != nil {
		t.Fatalf("Terminate 1: %v", err)
	}
	if err := g.Reactivate("run-2", now.Add(time.Hour)); err != nil {
		t.Fatalf("Reactivate 1: %v", err)
	}
	if err := g.Terminate(generated.GoalStateMet, "run 2 met", now.Add(2*time.Hour)); err != nil {
		t.Fatalf("Terminate 2: %v", err)
	}
	if err := g.Reactivate("run-3", now.Add(3*time.Hour)); err != nil {
		t.Fatalf("Reactivate 2: %v", err)
	}

	if len(g.TerminalHistory) != 2 {
		t.Fatalf("TerminalHistory len = %d, want 2 (one per completed prior run)", len(g.TerminalHistory))
	}
	if g.TerminalHistory[0].TerminalReason != "run 1 exhausted" {
		t.Errorf("TerminalHistory[0].TerminalReason = %q, want %q", g.TerminalHistory[0].TerminalReason, "run 1 exhausted")
	}
	if g.TerminalHistory[1].TerminalReason != "run 2 met" {
		t.Errorf("TerminalHistory[1].TerminalReason = %q, want %q", g.TerminalHistory[1].TerminalReason, "run 2 met")
	}
}

// TestReactivateRejectsNonTerminal proves Reactivate refuses a task-owned
// goal that is not currently terminal (still defining, or already active).
func TestReactivateRejectsNonTerminal(t *testing.T) {
	g := newTestGoal(t, "task", "task-1")
	if err := g.Reactivate("run-1", time.Now().UTC()); err == nil {
		t.Fatal("Reactivate on a still-defining goal: want error, got nil")
	}
	if err := g.Activate("run-1", time.Now().UTC()); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := g.Reactivate("run-2", time.Now().UTC()); err == nil {
		t.Fatal("Reactivate on an already-active goal: want error, got nil")
	}
}
