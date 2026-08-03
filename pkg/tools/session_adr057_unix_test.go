//go:build !windows

package tools

import (
	"context"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-057 U16 (W9b) — Background shells, real-process half.
//
// Covers FR-027 end to end against REAL OS processes, per the spec's
// "Agent loop <-> background process manager" contract (Development: "real
// processes with real PIDs; assert the PID is gone") and BDD-28 ("A chat
// Stop kills a child's background shell but not a sibling's" — Real PID
// gone / sibling's process still alive). Build-tagged !windows because it
// asserts real PID liveness via syscall.Kill(pid, 0), mirroring this
// repo's own precedent for exactly this shape of test (see
// pkg/entity/store_crossprocess_test.go / flock_isolation_test.go, both
// !windows-gated per CLAUDE.md).
//
// Rule 1 (every acceptance criterion verified against REAL store-backed
// state): these tests drive the real ExecTool.Execute path
// (run_in_background=true), which is the actual shell.go code that stamps
// ProcessSession.OwnerSessionID from ToolTranscriptSessionID(ctx) and
// registers it with the real, process-wide SessionManager singleton
// (getSessionManager()) — not a hand-built ProcessSession and not a mock
// that merely records an invocation. An orphaned process is a silent
// failure (it leaves no error), so "the process is gone" is checked
// directly against the OS via pidAlive, never inferred solely from the
// in-memory Status field.

// pidAlive reports whether pid is a live process by sending it signal 0 (no
// actual signal delivered — existence/permission check only). Used instead
// of trusting ProcessSession.Status alone: a bug that flips Status to
// "canceled" without actually terminating the OS process would pass a
// Status-only assertion while still leaking a real orphaned process — the
// exact silent failure mode this unit exists to close.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

// adr057TranscriptCtx builds a context carrying transcriptID as the
// ToolTranscriptSessionID — the value shell.go's runBackground stamps onto
// ProcessSession.OwnerSessionID at spawn time (shell.go:571-572 -> :1035).
func adr057TranscriptCtx(t *testing.T, transcriptID string) context.Context {
	t.Helper()
	return WithTranscriptSessionID(bashCtx(t), transcriptID)
}

// startRealBackgroundSleep starts a real, detached `sleep` background
// session via the actual ExecTool.Execute(run_in_background=true) path and
// returns its exec-tool session id and its real OS PID. Registers a
// t.Cleanup that force-kills the session so a failing assertion never
// leaks a live sleep process past the test.
func startRealBackgroundSleep(t *testing.T, tool *ExecTool, ctx context.Context, sleepSeconds int) (sessionID string, pid int) {
	t.Helper()
	result := tool.Execute(ctx, map[string]any{
		"command":           fmt.Sprintf("sleep %d", sleepSeconds),
		"run_in_background": true,
	})
	require.False(t, result.IsError, "background start must succeed, got ForLLM=%q", result.ForLLM)
	sessionID = mustSessionID(t, result)

	sess, err := tool.sessionManager.Get(sessionID)
	require.NoError(t, err)
	pid = sess.PID
	require.Greater(t, pid, 0, "spawned background session must carry a real positive PID")
	require.True(t, pidAlive(pid), "precondition: freshly spawned background process must be alive")

	t.Cleanup(func() {
		tool.Execute(context.Background(), map[string]any{"action": "kill", "session_id": sessionID})
	})
	return sessionID, pid
}

// TestBash_OwnerSessionID_StampedFromChildsOwnSessionID covers FR-027's
// first half: "ProcessSession.OwnerSessionID MUST be stamped from the
// child's own id." Two real background shells are started under two
// DISTINCT transcript session ids (simulating a root chat turn and a
// delegated child's own turn, per the spec's "distinct ids everywhere"
// corollary) — each spawned session's OwnerSessionID must equal ITS OWN
// context's id, never the other's. A regression that stamped both from some
// shared/ambient value (the pre-ADR-057 defect this closes: a child sharing
// the root's transcript id) would fail the cross-check below even though
// each individual value might look plausible in isolation.
func TestBash_OwnerSessionID_StampedFromChildsOwnSessionID(t *testing.T) {
	tool, _ := newBashTool(t, false)
	tool.godMode = true

	nonce := time.Now().UnixNano()
	rootTranscriptID := fmt.Sprintf("adr057-root-%d", nonce)
	childTranscriptID := fmt.Sprintf("adr057-child-%d", nonce+1)
	require.NotEqual(t, rootTranscriptID, childTranscriptID,
		"test fixture must use distinct parent/child ids (spec's distinct-ids-everywhere corollary)")

	rootCtx := adr057TranscriptCtx(t, rootTranscriptID)
	childCtx := adr057TranscriptCtx(t, childTranscriptID)

	rootSessionID, rootPID := startRealBackgroundSleep(t, tool, rootCtx, 20)
	childSessionID, childPID := startRealBackgroundSleep(t, tool, childCtx, 20)
	require.NotEqual(t, rootPID, childPID, "the two spawned background processes must be distinct real OS processes")

	rootSession, err := tool.sessionManager.Get(rootSessionID)
	require.NoError(t, err)
	childSession, err := tool.sessionManager.Get(childSessionID)
	require.NoError(t, err)

	assert.Equal(t, rootTranscriptID, rootSession.OwnerSessionID,
		"the root turn's background shell must be stamped with the ROOT's own transcript session id")
	assert.Equal(t, childTranscriptID, childSession.OwnerSessionID,
		"the child turn's background shell must be stamped with the CHILD's own distinct transcript session id")
	assert.NotEqual(t, rootSession.OwnerSessionID, childSession.OwnerSessionID,
		"FR-027: OwnerSessionID must come from the child's OWN id, never inherited/shared with the parent's")
}

// TestSessionManager_KillAllForSessions_CascadesOverDescendantSetRealPIDs
// covers FR-027's second half and BDD-28 directly against real OS
// processes: a chat-level Stop on the root session must reach a live
// descendant's (child's) background shell via the resolved descendant set
// {rootID, childID} — a single match against rootID alone would miss it,
// since ADR-057 stamps the child's OwnerSessionID from its own distinct id
// — while an unrelated sibling session's real background process MUST
// survive untouched.
func TestSessionManager_KillAllForSessions_CascadesOverDescendantSetRealPIDs(t *testing.T) {
	tool, _ := newBashTool(t, false)
	tool.godMode = true

	nonce := time.Now().UnixNano()
	rootID := fmt.Sprintf("adr057-cascade-root-%d", nonce)
	childID := fmt.Sprintf("adr057-cascade-child-%d", nonce)
	siblingID := fmt.Sprintf("adr057-cascade-sibling-%d", nonce)

	childSessionID, childPID := startRealBackgroundSleep(t, tool, adr057TranscriptCtx(t, childID), 30)
	siblingSessionID, siblingPID := startRealBackgroundSleep(t, tool, adr057TranscriptCtx(t, siblingID), 30)

	childSession, err := tool.sessionManager.Get(childSessionID)
	require.NoError(t, err)
	siblingSession, err := tool.sessionManager.Get(siblingSessionID)
	require.NoError(t, err)

	// Positive lower bound (binding rule 4's spirit): prove both real
	// processes are alive and their sessions "running" BEFORE asserting
	// anything about which one the cascade reaches — a broken spawn (or a
	// broken pidAlive helper) must not let the assertions below pass
	// vacuously.
	require.True(t, pidAlive(childPID), "precondition: child's real PID must be alive before the cascade runs")
	require.True(t, pidAlive(siblingPID), "precondition: sibling's real PID must be alive before the cascade runs")
	require.Equal(t, "running", childSession.GetStatus())
	require.Equal(t, "running", siblingSession.GetStatus())

	// The descendant set a session-hierarchy walk resolves is {rootID,
	// childID} — deliberately excludes siblingID.
	killed, failed := tool.sessionManager.KillAllForSessions([]string{rootID, childID})
	assert.Equal(t, 1, killed, "exactly the child's background shell must be killed")
	assert.Equal(t, 0, failed)

	// The child's real OS process must actually be gone — not merely
	// relabeled. This is the crux of the orphaned-process failure mode: an
	// orphaned process leaves no error, so it must be checked directly
	// against the OS rather than trusted from the Status field alone.
	require.Eventually(t, func() bool { return !pidAlive(childPID) }, 3*time.Second, 50*time.Millisecond,
		"child's real PID must actually be terminated by the cascade, or it is orphaned as a detached process")
	assert.Equal(t, "canceled", childSession.GetStatus())
	assert.True(t, childSession.IsDone())

	// The sibling's real OS process must be left running, untouched by a
	// cascade that named only {rootID, childID}.
	assert.True(t, pidAlive(siblingPID), "sibling's real PID must still be alive — the cascade must not reach an unrelated session")
	assert.Equal(t, "running", siblingSession.GetStatus(), "sibling's background shell must be left running")
}
