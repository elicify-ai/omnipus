package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeUnusedPID is a PID value picked well above any real process ID this
// sandbox will ever allocate but still within a valid pid_t range, so
// killProcessGroup's underlying syscall.Kill(-pid, ...) reliably returns
// ESRCH (verified: treated as success — see session_process_unix.go). Using a
// distinct fake PID per test session keeps kill/no-kill assertions readable
// in failure output without depending on any real subprocess.
const fakeUnusedPIDBase = 999990000

// TestSessionManager_KillAllForSession covers FR-B10/FR-B11/FR-B14: only the
// running sessions owned by the requested sessionID are killed, done/other-
// owner sessions are left untouched, and the kill counter/INFO log line fire
// once per session actually killed.
func TestSessionManager_KillAllForSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		sessions      []*ProcessSession
		killSessionID string
		wantKilled    int
		wantStatus    map[string]string // ProcessSession.ID -> expected Status after the call
	}{
		{
			name: "kills only running sessions owned by the target session",
			sessions: []*ProcessSession{
				{ID: "s1", OwnerSessionID: "owner-A", Status: "running", PID: fakeUnusedPIDBase + 1, StartTime: time.Now().Unix() - 30},
				{ID: "s2", OwnerSessionID: "owner-A", Status: "running", PID: fakeUnusedPIDBase + 2, StartTime: time.Now().Unix() - 5},
				{ID: "s3", OwnerSessionID: "owner-B", Status: "running", PID: fakeUnusedPIDBase + 3, StartTime: time.Now().Unix()},
			},
			killSessionID: "owner-A",
			wantKilled:    2,
			wantStatus: map[string]string{
				"s1": "done", // killProcess sets Status "done" on a successful kill
				"s2": "done",
				"s3": "running", // different owner — untouched
			},
		},
		{
			name: "skips already-done sessions for the same owner",
			sessions: []*ProcessSession{
				{ID: "s4", OwnerSessionID: "owner-C", Status: "running", PID: fakeUnusedPIDBase + 4, StartTime: time.Now().Unix()},
				{ID: "s5", OwnerSessionID: "owner-C", Status: "done", PID: fakeUnusedPIDBase + 5, StartTime: time.Now().Unix()},
			},
			killSessionID: "owner-C",
			wantKilled:    1,
			wantStatus: map[string]string{
				"s4": "done",
				"s5": "done", // already done, untouched by Kill(), still "done"
			},
		},
		{
			name: "no sessions for the given owner is a no-op",
			sessions: []*ProcessSession{
				{ID: "s6", OwnerSessionID: "owner-D", Status: "running", PID: fakeUnusedPIDBase + 6, StartTime: time.Now().Unix()},
			},
			killSessionID: "owner-does-not-exist",
			wantKilled:    0,
			wantStatus: map[string]string{
				"s6": "running",
			},
		},
		{
			name:          "empty sessionID is a no-op, never a panic",
			sessions:      []*ProcessSession{{ID: "s7", OwnerSessionID: "", Status: "running", PID: fakeUnusedPIDBase + 7, StartTime: time.Now().Unix()}},
			killSessionID: "",
			wantKilled:    0,
			wantStatus:    map[string]string{"s7": "running"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewSessionManager()
			for _, s := range tt.sessions {
				sm.Add(s)
			}

			before := sm.KilledBackgroundSessionsCount()

			killed := sm.KillAllForSession(tt.killSessionID)
			require.Equal(t, tt.wantKilled, killed, "unexpected killed count")

			after := sm.KilledBackgroundSessionsCount()
			require.Equal(t, before+int64(tt.wantKilled), after,
				"KilledBackgroundSessionsCount must increment by exactly the number of sessions actually killed (FR-B14)")

			for id, wantStatus := range tt.wantStatus {
				got, err := sm.Get(id)
				require.NoError(t, err)
				require.Equal(t, wantStatus, got.GetStatus(), "unexpected status for session %s", id)
			}
		})
	}
}

// TestSessionManager_KillAllForSession_DoesNotAffectOtherOwners is a focused
// scoping regression test mirroring BDD Scenario "Canceling one session does
// not affect another session's background work" (User Story 5, Acceptance
// Scenario 3) directly at the SessionManager level.
func TestSessionManager_KillAllForSession_DoesNotAffectOtherOwners(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	sessA := &ProcessSession{ID: "a1", OwnerSessionID: "session-A", Status: "running", PID: fakeUnusedPIDBase + 100, StartTime: time.Now().Unix()}
	sessB := &ProcessSession{ID: "b1", OwnerSessionID: "session-B", Status: "running", PID: fakeUnusedPIDBase + 101, StartTime: time.Now().Unix()}
	sm.Add(sessA)
	sm.Add(sessB)

	killed := sm.KillAllForSession("session-A")
	require.Equal(t, 1, killed)

	gotA, err := sm.Get("a1")
	require.NoError(t, err)
	require.Equal(t, "done", gotA.GetStatus(), "session A's background work must be killed")

	gotB, err := sm.Get("b1")
	require.NoError(t, err)
	require.Equal(t, "running", gotB.GetStatus(), "session B's background work must be left untouched")
}
