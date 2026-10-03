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

// TestLifecycleStore_Persist_RejectsInvalidStoppedRecords pins the three
// D2/CRIT-001 rejection paths persistLocked enforces on every record landing
// LifecycleStopped ("a loud failure instead of a silently wrong or missing
// cause"): no StopNote at all, a StopNote whose cause is outside the closed
// five-value vocabulary, and a record that claims BOTH "stopped" (landed) and
// "still fenced by an in-flight Stop on this generation". Each case passes
// every EARLIER check in validateLifecycleRecordForPersist so the assertion
// pins the one rejection path it names; expected errors are the production
// strings, compared exactly — strings.Contains would not distinguish the
// stopped-specific fence message from the near-identical terminal-record one
// (that variant has its own test in lifecycle_edge_test.go).
//
// Stopped is deliberately NON-terminal, so none of these shapes can be caught
// by the earlier Terminal()+fence guard; a refactor dropping any of the three
// checks would let the bad record persist silently, which is exactly what the
// Exists assertions here refuse.
func TestLifecycleStore_Persist_RejectsInvalidStoppedRecords(t *testing.T) {
	cases := []struct {
		name string
		rec  func(sessionID string) *LifecycleRecord
		want string
	}{
		{
			name: "stopped_without_stop_note",
			rec: func(sessionID string) *LifecycleRecord {
				return &LifecycleRecord{
					SessionID:      sessionID,
					Generation:     1,
					State:          LifecycleStopped,
					OwnerScopeKind: OwnerScopeHuman,
					WorkspaceID:    "ws-1",
					AgentID:        "agent-1",
					// No StopNote, no Stop: the bare landed-stopped shape.
				}
			},
			want: "session: lifecycle: state stopped requires stop_note",
		},
		{
			name: "stopped_with_current_generation_stop_fence",
			rec: func(sessionID string) *LifecycleRecord {
				return &LifecycleRecord{
					SessionID:      sessionID,
					Generation:     1,
					State:          LifecycleStopped,
					OwnerScopeKind: OwnerScopeHuman,
					WorkspaceID:    "ws-1",
					AgentID:        "agent-1",
					// StopNote present with a VALID cause so this shape is
					// rejected by the fence check and nothing earlier; Stop
					// fences THIS generation (== Generation: the ahead-of-
					// record check passes, and stopped being non-terminal
					// leaves the terminal guard out of the way).
					StopNote: &StopNote{At: time.Now().UTC(), By: "human:qa-lead", Seq: 1, Cause: StopCauseStop},
					Stop:     &Stop{At: time.Now().UTC(), Generation: 1, By: Principal{Kind: PrincipalKindHuman, ID: "dan"}},
				}
			},
			want: "session: lifecycle: state stopped cannot carry a current-generation stop marker",
		},
		{
			name: "stop_note_with_invalid_cause",
			rec: func(sessionID string) *LifecycleRecord {
				return &LifecycleRecord{
					SessionID:      sessionID,
					Generation:     1,
					State:          LifecycleStopped,
					OwnerScopeKind: OwnerScopeHuman,
					WorkspaceID:    "ws-1",
					AgentID:        "agent-1",
					// StopNote present (so the missing-note check passes),
					// no Stop fence (so the fence check passes), cause
					// outside {stop, redirect_pause, cascade, restart,
					// timeout} — the ONLY five legal values per the
					// StopCause vocabulary.
					StopNote: &StopNote{At: time.Now().UTC(), By: "human:qa-lead", Seq: 1, Cause: "bogus"},
				}
			},
			want: `session: lifecycle: invalid stop_note.cause "bogus"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestLifecycleStore(t)
			rec := tc.rec("stopped-reject-" + tc.name)
			err := s.Persist(rec)
			if err == nil {
				t.Fatalf("Persist(%s) succeeded, want rejection with %q", tc.name, tc.want)
			}
			if err.Error() != tc.want {
				t.Errorf("Persist(%s) error = %q, want exactly %q", tc.name, err.Error(), tc.want)
			}
			if s.Exists(rec.SessionID) {
				t.Errorf("a rejected %s record must not land on disk", tc.name)
			}
		})
	}
}
