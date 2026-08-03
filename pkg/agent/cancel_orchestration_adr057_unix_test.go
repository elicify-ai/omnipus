//go:build !windows

// Package agent — cancel_orchestration_adr057_unix_test.go
//
// ADR-057 U15 (W9, FR-027) real-OS-process coverage for BDD-28 ("A chat Stop
// kills a child's background shell but not a sibling's") at the pkg/agent
// layer: RequestCancel must resolve the descendant set via the durable
// ParentDurableKey walk (collectDescendantSessionIDs/resolveBackgroundKillSessionIDs,
// cancel.go) and cascade CancelHooks.KillBackgroundSessions over it — not
// just the root id alone. pkg/tools/session_adr057_unix_test.go already
// covers the SAME red/green at the tools.SessionManager layer directly; this
// file proves the pkg/agent wiring on top of it end to end.
//
// Build-tagged !windows because it asserts real PID liveness via
// syscall.Kill(pid, 0), mirroring pkg/tools/session_adr057_unix_test.go and
// pkg/entity/store_crossprocess_test.go/flock_isolation_test.go.
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// u15PidAlive reports whether pid is a live process by sending it signal 0
// (existence/permission check only, no actual signal delivered). Mirrors
// pkg/tools/session_adr057_unix_test.go's pidAlive exactly — an orphaned
// process leaves no error, so liveness must be checked directly against the
// OS, never inferred from an in-memory Status field alone.
func u15PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

// u15SpawnRealSleep starts a REAL, detached `sleep` OS process as its own
// process group leader (Setpgid: true, matching this repo's own background-
// shell precedent — pkg/tools/shell_process_unix.go:14 — since
// killProcessGroup, session_process_unix.go, signals the NEGATIVE pid, i.e.
// the process group). Returns its real PID.
//
// Reaps the child on its OWN goroutine the instant it exits (whether killed
// by this test's own cascade or by t.Cleanup below) — mirrors production's
// own background-process supervisor (shell.go's runBackground goroutine).
// Without this, THIS test process — the real OS parent, since it called
// cmd.Start() itself — never calls Wait(), so a killed child sits as an
// unreaped ZOMBIE for the rest of the test: syscall.Kill(pid, 0) SUCCEEDS
// against an unreaped zombie (its PID slot is still allocated until the
// parent reaps it), which would make u15PidAlive report "still alive"
// indefinitely even after a correct kill — a false negative in the test
// harness, not a defect in RequestCancel's cascade.
func u15SpawnRealSleep(t *testing.T, seconds int) int {
	t.Helper()
	cmd := exec.Command("sleep", fmt.Sprintf("%d", seconds))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start(), "must be able to spawn a real sleep process for this test")
	pid := cmd.Process.Pid
	require.Greater(t, pid, 0)

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	require.True(t, u15PidAlive(pid), "precondition: freshly spawned sleep process must be alive")
	return pid
}

// TestU15Cancel_KillsChildShellsNotSiblings_RealPIDs covers BDD-28 end to end
// against REAL OS processes, through pkg/agent's own RequestCancel — not
// tools.SessionManager directly (see pkg/tools/session_adr057_unix_test.go
// for that layer's own red/green).
//
//   - RED: the OLD, still-compiling single-id
//     tools.SessionManager.KillAllForSession(rootID) call never reaches the
//     child's shell, because ADR-057 stamps a delegated child's
//     ProcessSession.OwnerSessionID from the CHILD's own distinct session id
//     (FR-027), not the shared root id — U16's own red/green proved this at
//     the tools layer; this reproduces the SAME failure directly against a
//     real spawned process before RequestCancel ever runs, to prove it is
//     not merely a stubbed-count assertion.
//   - GREEN: al.RequestCancel(root) resolves {rootID, childID} via the
//     durable ParentDurableKey walk (collectDescendantSessionIDs) and loops
//     CancelHooks.KillBackgroundSessions over that set — the child's real
//     process is actually killed, while an unrelated sibling session's real
//     process (a different chat entirely, no ParentDurableKey relation) is
//     left running.
func TestU15Cancel_KillsChildShellsNotSiblings_RealPIDs(t *testing.T) {
	al := newCancelTestAgentLoop(t)
	lifecycleStore := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(nil, lifecycleStore)

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("u15-shells-root-%d", nonce)
	childID := fmt.Sprintf("u15-shells-child-%d", nonce)
	siblingID := fmt.Sprintf("u15-shells-sibling-%d", nonce) // a wholly unrelated, different chat

	// B is a durable child of the root; C (the sibling) has no lifecycle
	// record at all — an unrelated chat, exactly BDD-28's "under a different
	// chat" framing.
	u15PersistLifecycleRecord(t, lifecycleStore, childID, rootID, session.LifecycleRunning)

	sm := tools.NewSessionManager()
	childPID := u15SpawnRealSleep(t, 30)
	siblingPID := u15SpawnRealSleep(t, 30)
	sm.Add(&tools.ProcessSession{ID: "bg-u15-child", OwnerSessionID: childID, Status: "running", PID: childPID, StartTime: time.Now().Unix()})
	sm.Add(&tools.ProcessSession{ID: "bg-u15-sibling", OwnerSessionID: siblingID, Status: "running", PID: siblingPID, StartTime: time.Now().Unix()})

	// Positive lower bound (Rule 4's spirit): prove both real processes are
	// alive before asserting anything about which the cascade reaches.
	require.True(t, u15PidAlive(childPID), "precondition: child's real PID must be alive")
	require.True(t, u15PidAlive(siblingPID), "precondition: sibling's real PID must be alive")

	// --- RED: the OLD single-id form never reaches the child's shell ---
	killedOld, failedOld := sm.KillAllForSession(rootID)
	assert.Equal(t, 0, killedOld,
		"RED: single-id KillAllForSession(root) must NOT reach the child's own-session-owned shell — "+
			"this is the orphaning bug FR-027 exists to close")
	assert.Equal(t, 0, failedOld)
	assert.True(t, u15PidAlive(childPID),
		"RED proof: the child's real OS process is still alive after the single-id form — silently orphaned")

	// --- GREEN: al.RequestCancel resolves the descendant set and cascades ---
	u15RegisterActiveTurn(t, al, rootID, "turn-u15-shells-root", 0, "")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: rootID},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) (int, int) {
				return sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired)

	require.Eventually(t, func() bool { return !u15PidAlive(childPID) }, 3*time.Second, 50*time.Millisecond,
		"GREEN: RequestCancel's descendant-set cascade must actually terminate the child's real background process")
	assert.True(t, u15PidAlive(siblingPID),
		"GREEN: the sibling's real process (a wholly unrelated chat) must be left running untouched")

	childSession, err := sm.Get("bg-u15-child")
	require.NoError(t, err)
	assert.Equal(t, "canceled", childSession.GetStatus())
	assert.True(t, childSession.IsDone())

	siblingSession, err := sm.Get("bg-u15-sibling")
	require.NoError(t, err)
	assert.Equal(t, "running", siblingSession.GetStatus())

	assert.Equal(t, 1, outcome.BackgroundSessionsKilled,
		"CancelOutcome must report exactly the one background session actually killed")
}
