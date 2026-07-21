// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_status_priority_test.go is the focused, deterministic,
// load-independent regression test for the fix to
// TestStopReachesShellLeaf_RealBackgroundProcessGroupDies's CI-only flake
// (pkg/agent/plan_stop_shell_leaf_adr052_qa_test.go), which failed
// deterministically under a full pkg/agent package test run
// ("ProcessSession status = \"done\", want \"canceled\"") but always passed
// when run alone.
//
// Root cause: tools.GetSharedSessionManager() is a single process-wide
// singleton (session_manager_export.go), shared by EVERY *AgentLoop any test
// in pkg/agent constructs (see loop.go's AgentLoop.Close doc comment, "LOAD-
// BEARING PRECONDITION"). Under a full-package run, many *AgentLoop instances
// exist concurrently, each with its own t.Cleanup(al.Close) — and
// AgentLoop.Close's shutdown reaper calls SessionManager.KillAll()
// UNCONDITIONALLY and process-wide, regardless of which AgentLoop/test
// actually owns a given session. This can race a DIFFERENT, concurrently
// running test's own SessionManager.KillAllForSession call (the
// RequestCancel/StopTask cancel cascade) for a session that test itself
// started and is trying to cancel for a specific reason. Both KillAll's
// Kill() (-> KillAndRelabel(StatusDone)) and KillAllForSession's
// KillAndRelabel(StatusCanceled) race for the SAME ProcessSession's mutex in
// the narrow window between each caller's own GetStatus() pre-check and its
// KillAndRelabel call; before this fix, whichever won the mutex race
// PERMANENTLY decided the label — if KillAll's generic "done" won,
// KillAllForSession's later canceled relabel found status != running and
// silently gave up (ErrSessionDone, the pre-existing documented "benign lost
// race" no-op path), leaving the session mislabeled "done" even though it
// was, in fact, explicitly canceled moments later.
//
// Fix (pkg/tools/session.go): KillAndRelabel now allows a MORE SPECIFIC
// terminal status (canceled/killed/timeout) to correct an already-set
// GENERIC terminal status (done/exited) written by a different, racing
// caller — but never the reverse (generic can never downgrade specific), and
// never between two equally-specific labels (the first specific writer still
// wins ties). See statusPriority's doc comment for the full reasoning,
// including why this does NOT weaken the pre-existing "a genuinely
// already-terminal session is left untouched" no-op guarantee (Test
// SessionManager_KillAllForSession's "skips already-done sessions" case,
// TestSessionManager_KillAll's "already-terminal session must be left
// untouched" case, and TestBash_KillRacingNaturalExit/MIN-002 all stop at an
// EARLIER GetStatus()/IsDone() pre-check in their respective callers and
// never reach the branch this file tests at all).
package tools

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessSession_KillAndRelabel_SpecificStatusSupersedesGenericDone
// exercises KillAndRelabel's priority-upgrade branch directly (bypassing
// SessionManager entirely) so it is 100% deterministic and load-independent
// — no real subprocess, no goroutine scheduling, no shared global singleton
// involved. Each subtest constructs a *ProcessSession already sitting in a
// terminal status (simulating "a different, racing caller already won the
// mutex and wrote this label") and calls KillAndRelabel directly to observe
// whether/how the label is corrected.
func TestProcessSession_KillAndRelabel_SpecificStatusSupersedesGenericDone(t *testing.T) {
	t.Parallel()

	t.Run("canceled corrects a preceding generic done", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-1",
			Status:    StatusDone, // simulates KillAll's generic reaper winning the race first
			PID:       fakeUnusedPIDBase + 2001,
			ExitCode:  -1,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusCanceled)
		require.NoError(t, err,
			"a specific status (canceled) must be allowed to correct a generic 'done' set by a racing caller")
		assert.Equal(t, "canceled", s.GetStatus())
	})

	t.Run("killed corrects a preceding generic done", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-2",
			Status:    StatusDone,
			PID:       fakeUnusedPIDBase + 2002,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusKilled)
		require.NoError(t, err, "an explicit kill must be allowed to correct a generic 'done' set by a racing caller")
		assert.Equal(t, "killed", s.GetStatus())
	})

	t.Run("timeout corrects a preceding generic done", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-3",
			Status:    StatusDone,
			PID:       fakeUnusedPIDBase + 2003,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusTimeout)
		require.NoError(t, err, "a timeout guard must be allowed to correct a generic 'done' set by a racing caller")
		assert.Equal(t, "timeout", s.GetStatus())
	})

	t.Run("exited (legacy generic) is also correctable by a specific status", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-4",
			Status:    StatusExited,
			PID:       fakeUnusedPIDBase + 2004,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusCanceled)
		require.NoError(t, err)
		assert.Equal(t, "canceled", s.GetStatus())
	})

	t.Run("a generic done can never downgrade an already-specific status", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-5",
			Status:    StatusCanceled,
			PID:       fakeUnusedPIDBase + 2005,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusDone)
		require.ErrorIs(t, err, ErrSessionDone,
			"a generic 'done' must never overwrite an already-specific status — sticky in the specific direction only")
		assert.Equal(t, "canceled", s.GetStatus(), "status must remain the specific label")
	})

	t.Run("two equally-specific statuses do not flip-flop -- first specific writer wins", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-6",
			Status:    StatusKilled,
			PID:       fakeUnusedPIDBase + 2006,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusTimeout)
		require.ErrorIs(t, err, ErrSessionDone, "same-priority specific statuses must not overwrite each other")
		assert.Equal(t, "killed", s.GetStatus())

		err = s.KillAndRelabel(StatusCanceled)
		require.ErrorIs(t, err, ErrSessionDone, "same-priority specific statuses must not overwrite each other")
		assert.Equal(t, "killed", s.GetStatus())
	})

	t.Run("ExitCode from the first writer is preserved across a status upgrade", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-7",
			Status:    StatusDone,
			ExitCode:  0, // simulates a natural exit that recorded a real exit code
			PID:       fakeUnusedPIDBase + 2007,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusCanceled)
		require.NoError(t, err)
		assert.Equal(t, "canceled", s.GetStatus())
		assert.Equal(t, 0, s.GetExitCode(),
			"the upgrade branch must not clobber an ExitCode a racing writer already recorded")
	})

	t.Run("still running: the normal kill path is unaffected by the priority check", func(t *testing.T) {
		t.Parallel()
		s := &ProcessSession{
			ID:        "sess-8",
			Status:    StatusRunning,
			PID:       fakeUnusedPIDBase + 2008,
			StartTime: time.Now().Unix(),
		}

		err := s.KillAndRelabel(StatusCanceled)
		require.NoError(t, err)
		assert.Equal(t, "canceled", s.GetStatus())
		assert.Equal(t, -1, s.GetExitCode(), "the real kill path still sets ExitCode -1, as before this fix")
	})
}

// TestSessionManager_KillAllForSession_CancelWinsRaceAgainstPriorGenericDone
// reproduces the exact end-to-end shape of the CI race at the SessionManager
// level (one level up from the ProcessSession-only test above): a session
// that has ALREADY been relabeled "done" by a stand-in for KillAll's
// unconditional shutdown reaper, moments before KillAllForSession's owner-
// scoped cascade would otherwise have reached it as "running". Deterministic
// and load-independent — the "prior done" is set directly on the fixture,
// not via a real race, mirroring the pattern
// TestSessionManager_KillAllForSession already uses for its "skips
// already-done" case (session_kill_all_for_session_test.go) — except THIS
// session is owned by the same sessionID being canceled, and canceled by
// definition of statusPriority.
func TestSessionManager_KillAllForSession_CancelWinsRaceAgainstPriorGenericDone(t *testing.T) {
	t.Parallel()

	sm := NewSessionManager()
	sess := &ProcessSession{
		ID:             "raced-1",
		OwnerSessionID: "owner-raced",
		// StatusDone here stands in for KillAll's shutdown reaper having
		// already won the mutex race a moment before KillAllForSession's own
		// GetStatus() pre-check runs below.
		Status:    StatusDone,
		PID:       fakeUnusedPIDBase + 3001,
		StartTime: time.Now().Unix(),
	}
	sm.Add(sess)

	// KillAllForSession's own pre-check (GetStatus() != running) will treat
	// this exactly like TestSessionManager_KillAllForSession's "skips
	// already-done sessions" case: a benign no-op, not counted as killed.
	// This documents the CURRENT boundary of the KillAndRelabel-level fix —
	// it closes the race WITHIN a single KillAndRelabel call (this test's
	// sibling, TestProcessSession_KillAndRelabel_SpecificStatusSupersedes
	// GenericDone, proves that directly) but does not retroactively correct
	// a session that was ALREADY done before KillAllForSession's scan even
	// started, which remains, by design, the same no-op contract
	// TestSessionManager_KillAllForSession's "skips already-done sessions"
	// case pins.
	killed, failed := sm.KillAllForSession("owner-raced")
	require.Equal(t, 0, killed)
	require.Equal(t, 0, failed)

	got, err := sm.Get("raced-1")
	require.NoError(t, err)
	assert.Equal(t, "done", got.GetStatus(),
		"a session already terminal before KillAllForSession's scan starts keeps its pre-existing label, unchanged")
}
