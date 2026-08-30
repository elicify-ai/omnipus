//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U14 (Wave F), W9a — delegate action="cancel" kills that child's
// OWN background shells (FR-028/BDD-29). Build-tagged !windows because it
// asserts real PID liveness via syscall, mirroring
// pkg/tools/session_adr057_unix_test.go's own precedent (U16, BDD-28) —
// this file is the delegate-tool-path analogue of that one.
//
// Deliberately reuses the SAME real-process helpers session_adr057_unix_test.go
// already defines in this package (pidAlive, adr057TranscriptCtx,
// startRealBackgroundSleep) rather than re-deriving a parallel walk or a
// second real-PID mechanism, per the task's explicit "don't build a second,
// parallel walk" guidance.

package tools

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestDelegateCancel_KillsThatChildsShells is test #44 (BDD-29, FR-028):
// `delegate action="cancel"` targeting a child with a real, running
// background shell must actually terminate that shell's real OS process —
// before this fix, no such call existed on the delegate-tool cancel path
// at all (only U15's RequestCancel/Stop-button cascade reached background
// shells, and only via a chat-wide Stop).
func TestDelegateCancel_KillsThatChildsShells(t *testing.T) {
	bashTool, _ := newBashTool(t, false)
	bashTool.godMode = true

	nonce := time.Now().UnixNano()
	childID := fmt.Sprintf("u14-w9a-child-%d", nonce)

	// Real background `sleep` under the CHILD's own transcript/session id —
	// shell.go's runBackground stamps ProcessSession.OwnerSessionID from
	// exactly this context value.
	_, childPID := startRealBackgroundSleep(t, bashTool, adr057TranscriptCtx(t, childID), 30)
	require.True(t, pidAlive(childPID), "precondition: child's real PID must be alive before cancel runs")

	// Wire the delegate tool against the SAME shared, process-wide
	// SessionManager the real ExecTool above registered its background
	// session with (shell.go's ExecTool also sources getSessionManager()) —
	// SetSessionManager is the FR-028 mechanism this unit adds.
	delegateTool := NewDelegateTool("test-model", 0, 0)
	delegateTool.SetSessionMessagingEnabled(func() bool { return true })
	delegateTool.SetSessionManager(GetSharedSessionManager())

	lc := session.NewLifecycleStore(t.TempDir())
	delegateTool.SetLifecycleStore(lc)
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: childID, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		ParentDurableKey: "u14-w9a-parent", WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	// Cancel hooks stubbed to simulate a successful turn-level cancel — this
	// test's focus is the SHELL-KILL side effect FR-028 adds, not the
	// turn-cancel/steering mechanism itself (covered elsewhere).
	delegateTool.SetCancelHooks(
		func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
		func(sessionID, hint string) ([]string, error) { return []string{sessionID}, nil },
	)

	callerCtx := WithTranscriptSessionID(context.Background(), "u14-w9a-parent")
	result := delegateTool.Execute(callerCtx, map[string]any{
		"action": "cancel", "session_id": childID, "hard": true,
	})
	if result.IsError {
		t.Fatalf("delegate cancel failed: %s", result.ForLLM)
	}

	require.Eventually(t, func() bool { return !pidAlive(childPID) }, 3*time.Second, 50*time.Millisecond,
		"BDD-29: the child's real background shell PID must actually be terminated by "+
			"delegate action=cancel, or it is orphaned as a detached process")
}
