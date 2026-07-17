package tools

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionManager_AddGet(t *testing.T) {
	sm := NewSessionManager()
	session := &ProcessSession{
		ID:        "test-1",
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	}

	sm.Add(session)

	got, err := sm.Get("test-1")
	require.NoError(t, err)
	require.Equal(t, "test-1", got.ID)
}

func TestSessionManager_Remove(t *testing.T) {
	sm := NewSessionManager()
	session := &ProcessSession{
		ID:        "test-1",
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	}
	sm.Add(session)
	sm.Remove("test-1")

	_, err := sm.Get("test-1")
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSessionManager_List(t *testing.T) {
	sm := NewSessionManager()
	sm.Add(&ProcessSession{
		ID:        "test-1",
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	})
	sm.Add(&ProcessSession{
		ID:        "test-2",
		Command:   "echo world",
		Status:    "running",
		StartTime: 1001,
	})
	sm.Add(&ProcessSession{
		ID:        "test-3",
		Command:   "echo done",
		Status:    "done",
		StartTime: 1002,
	})

	sessions := sm.List()
	require.Len(t, sessions, 3)

	ids := make(map[string]bool)
	for _, s := range sessions {
		ids[s.ID] = true
	}
	require.True(t, ids["test-1"])
	require.True(t, ids["test-2"])
	require.True(t, ids["test-3"])
}

func TestProcessSession_IsDone(t *testing.T) {
	session := &ProcessSession{Status: "running"}
	require.False(t, session.IsDone())

	session.Status = "done"
	require.True(t, session.IsDone())

	session.Status = "exited"
	require.True(t, session.IsDone())

	session.Status = "canceled"
	require.True(t, session.IsDone())
}

// TestSessionManager_KillAll covers the AgentLoop.Close() shutdown reaper:
// every currently-running session (regardless of owner) is killed, sessions
// already in a terminal state are left untouched, and the returned count
// reflects exactly the number actually killed.
func TestSessionManager_KillAll(t *testing.T) {
	sm := NewSessionManager()

	running1 := &ProcessSession{
		ID:             "run-1",
		OwnerSessionID: "owner-X",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 300,
		StartTime:      time.Now().Unix(),
	}
	running2 := &ProcessSession{
		ID:             "run-2",
		OwnerSessionID: "owner-Y",
		Status:         "running",
		PID:            fakeUnusedPIDBase + 301,
		StartTime:      time.Now().Unix(),
	}
	done1 := &ProcessSession{
		ID:             "done-1",
		OwnerSessionID: "owner-Z",
		Status:         "done",
		PID:            fakeUnusedPIDBase + 302,
		StartTime:      time.Now().Unix(),
	}
	killed1 := &ProcessSession{
		ID:             "killed-1",
		OwnerSessionID: "owner-Z",
		Status:         "killed",
		PID:            fakeUnusedPIDBase + 303,
		StartTime:      time.Now().Unix(),
	}
	sm.Add(running1)
	sm.Add(running2)
	sm.Add(done1)
	sm.Add(killed1)

	killed, failed := sm.KillAll()
	require.Equal(t, 2, killed, "KillAll must kill exactly the running sessions")
	require.Equal(t, 0, failed, "no kill should fail in this scenario")

	got1, err := sm.Get("run-1")
	require.NoError(t, err)
	require.True(t, got1.IsDone(), "running session must be killed by KillAll")

	got2, err := sm.Get("run-2")
	require.NoError(t, err)
	require.True(t, got2.IsDone(), "running session must be killed by KillAll")

	gotDone, err := sm.Get("done-1")
	require.NoError(t, err)
	require.Equal(t, "done", gotDone.GetStatus(), "already-terminal session must be left untouched")

	gotKilled, err := sm.Get("killed-1")
	require.NoError(t, err)
	require.Equal(t, "killed", gotKilled.GetStatus(), "already-terminal session must be left untouched")
}

// TestSessionManager_KillAll_EmptyManagerIsNoOp guards against a panic or
// spurious kill count on a manager with no sessions at all — the state a
// gateway that never spawned any background work shuts down in.
func TestSessionManager_KillAll_EmptyManagerIsNoOp(t *testing.T) {
	sm := NewSessionManager()
	killed, failed := sm.KillAll()
	require.Equal(t, 0, killed)
	require.Equal(t, 0, failed)
}

// TestSessionManager_KillAll_KillFailure_CountsAsFailed forces the Kill()
// failure branch via the killProcessGroupFn test seam (a real fake PID
// always succeeds via ESRCH-as-success — see killProcessGroup's own doc
// comment — so there is no way to reach this branch with a real syscall in
// a test environment) and asserts the failure is counted in `failed`, NOT
// silently swallowed as before (7-reviewer gate finding: kill failures were
// previously invisible outside a bare slog.Warn).
func TestSessionManager_KillAll_KillFailure_CountsAsFailed(t *testing.T) {
	origKillFn := killProcessGroupFn
	t.Cleanup(func() { killProcessGroupFn = origKillFn })
	wantErr := errors.New("forced kill failure for test")
	killProcessGroupFn = func(pid int) error { return wantErr }

	sm := NewSessionManager()
	sess := &ProcessSession{
		ID:        "fail-shutdown-1",
		Status:    "running",
		PID:       fakeUnusedPIDBase + 901,
		StartTime: time.Now().Unix(),
	}
	sm.Add(sess)

	killed, failed := sm.KillAll()
	require.Equal(t, 0, killed, "a failed kill must not be counted as killed")
	require.Equal(t, 1, failed, "a failed kill must be counted as failed")

	got, err := sm.Get("fail-shutdown-1")
	require.NoError(t, err)
	require.Equal(t, "running", got.GetStatus(),
		"status must stay running when the kill itself failed — no relabel on failure")
}

func TestProcessSession_ToSessionInfo(t *testing.T) {
	session := &ProcessSession{
		ID:        "test-1",
		PID:       12345,
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	}

	info := session.ToSessionInfo()
	require.Equal(t, "test-1", info.ID)
	require.Equal(t, "echo hello", info.Command)
	require.Equal(t, "running", info.Status)
	require.Equal(t, 12345, info.PID)
	require.Equal(t, int64(1000), info.StartedAt)
}
