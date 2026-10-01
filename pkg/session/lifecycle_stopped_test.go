package session

import (
	"testing"
	"time"
)

// Task U1: stopped replaces the three superseded states without becoming
// immutable. String inputs keep this regression runnable before the rename.
func TestLifecycleStopped_ReplacesSupersededStates(t *testing.T) {
	cases := []struct {
		state    LifecycleState
		valid    bool
		terminal bool
	}{
		{state: "stopped", valid: true},
		{state: "paused"},
		{state: "cancelled"},
		{state: "timed_out"},
		{state: "queued", valid: true},
		{state: "running", valid: true},
		{state: "needs_input", valid: true},
		{state: "completed", valid: true, terminal: true},
		{state: "failed", valid: true, terminal: true},
		{state: ""},
		{state: "bogus"},
	}
	for _, tc := range cases {
		t.Run(string(tc.state), func(t *testing.T) {
			if got := IsValidLifecycleState(tc.state); got != tc.valid {
				t.Errorf("IsValidLifecycleState(%q) = %v, want %v", tc.state, got, tc.valid)
			}
			if got := IsTerminalLifecycleState(tc.state); got != tc.terminal {
				t.Errorf("IsTerminalLifecycleState(%q) = %v, want %v", tc.state, got, tc.terminal)
			}
		})
	}
}

// This proves the store's real continuation primitive, not just enum validity.
// Reachable human-message/follow_up continuation is a separate integration check.
func TestLifecycleStopped_CanResumeSameGeneration(t *testing.T) {
	store := NewLifecycleStore(t.TempDir())
	stopped := &LifecycleRecord{
		SessionID:      "stopped-session",
		Generation:     1,
		State:          "stopped",
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "workspace-1",
		AgentID:        "agent-1",
		// D2/CRIT-001: persistLocked now rejects any record landing
		// LifecycleStopped with no StopNote. This fixture predates that
		// invariant (backend-lead's stop_note commit 57c1a20ba) — cause
		// "stop" because the session named IS the direct target, matching
		// StopCauseStop's own doc comment.
		StopNote: &StopNote{At: time.Now().UTC(), By: "human:qa-lead", Seq: 1, Cause: StopCauseStop},
	}
	if err := store.Persist(stopped); err != nil {
		t.Fatalf("Persist(stopped) must succeed before continuation: %v", err)
	}
	if err := store.Mutate(stopped.SessionID, func(rec *LifecycleRecord) error {
		if rec == nil {
			return ErrLifecycleNotFound
		}
		if rec.State != "stopped" || rec.Terminal() {
			t.Fatalf("before continuation: state=%q terminal=%v, want stopped and non-terminal", rec.State, rec.Terminal())
		}
		rec.State = LifecycleRunning
		return nil
	}); err != nil {
		t.Fatalf("resume stopped session on its own generation: %v", err)
	}
	resumed, err := store.Load(stopped.SessionID)
	if err != nil {
		t.Fatalf("Load(resumed): %v", err)
	}
	if resumed.SessionID != stopped.SessionID || resumed.Generation != 1 {
		t.Fatalf("resumed identity=(%q, %d), want (%q, 1)", resumed.SessionID, resumed.Generation, stopped.SessionID)
	}
	if resumed.State != LifecycleRunning || resumed.Terminal() {
		t.Fatalf("resumed state=%q terminal=%v, want running and non-terminal", resumed.State, resumed.Terminal())
	}
}
