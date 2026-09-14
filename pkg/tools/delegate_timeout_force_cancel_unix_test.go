//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT A-17 — a delegation that reaches its time limit is reported to the
// delegator TRUTHFULLY and its OS-level work is stopped.
//
// DOCUMENTED INTENT (DelegateTool.Description and the timeout_seconds schema):
// "A delegation is force-cancelled after timeout_seconds (default 300s / 5 min)
// if it has not finished by then." Force-cancelled means dead, so:
//   - the delegator is told the child is STOPPED and there is nothing to
//     cancel — never the old "Delegate failed: turn timed out: context deadline
//     exceeded" / "Delegate execution failed" wording an orchestrator read as
//     "possibly still running";
//   - the durable lifecycle record says timed_out (not failed, not cancelled);
//   - the child's background shells are killed, exactly as delegate
//     action="cancel" kills them, so a backgrounded command cannot keep
//     writing after the delegator was told the delegation ended.
//
// Build-tagged !windows because it asserts real PID liveness via syscall,
// mirroring delegate_adr057_unix_test.go and reusing its real-process helpers
// (newBashTool, startRealBackgroundSleep, adr057TranscriptCtx, pidAlive).

package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// a17TimeoutErr mirrors the error shape pkg/agent's spawnSubTurn returns for a
// force-cancelled sub-turn: it wraps ErrDelegationTimedOut and
// context.DeadlineExceeded.
func a17TimeoutErr() error {
	return fmt.Errorf("%w: reached its 1s time limit and was force-cancelled (turn timed out: %w)",
		ErrDelegationTimedOut, context.DeadlineExceeded)
}

// a17TimedOutSpawner starts a REAL background shell owned by the child's own
// session (as a child that ran `cmd &` would), records the child's session id
// and PID, waits for release, then returns the force-cancel outcome.
type a17TimedOutSpawner struct {
	t        *testing.T
	bash     *ExecTool
	started  chan struct{}
	release  chan struct{}
	childID  string
	childPID int
}

func (s *a17TimedOutSpawner) SpawnSubTurn(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	s.childID = cfg.DelegateSessionID
	_, s.childPID = startRealBackgroundSleep(s.t, s.bash, adr057TranscriptCtx(s.t, cfg.DelegateSessionID), 60)
	close(s.started)
	<-s.release
	err := a17TimeoutErr()
	return &ToolResult{ForLLM: "SubTurn timed out: it reached its 1s time limit and was force-cancelled.", IsError: true, Err: err}, err
}

func newA17DelegateTool(t *testing.T, spawner SubTurnSpawner) (*DelegateTool, *session.LifecycleStore) {
	t.Helper()
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	lc := session.NewLifecycleStore(t.TempDir())
	tool.SetLifecycleStore(lc)
	tool.SetSessionManager(GetSharedSessionManager())
	tool.SetSpawner(spawner)
	// Registered after t.TempDir so LIFO cleanup drains the async goroutine's
	// lifecycle writes before the temp dir is removed.
	t.Cleanup(tool.WaitForAsyncTasks)
	return tool, lc
}

// assertA17TimedOutReport checks the delegator-facing text against the
// documented intent: stopped, nothing to cancel, partial work may remain —
// and none of the old ambiguous "failed" wordings.
func assertA17TimedOutReport(t *testing.T, res *ToolResult) {
	t.Helper()
	require.NotNil(t, res)
	assert.True(t, res.IsError, "a timed-out delegation did not deliver its task — it must not read as a success")
	assert.Contains(t, res.ForLLM, "TIMED OUT and was force-cancelled")
	assert.Contains(t, res.ForLLM, "STOPPED: it will make no further tool calls or file changes")
	assert.Contains(t, res.ForLLM, "nothing left to cancel")
	assert.Contains(t, res.ForLLM, "may still be on disk")
	for _, ambiguous := range []string{"Delegate execution failed", "Delegate failed:", "Task canceled during execution"} {
		assert.NotContains(t, res.ForLLM, ambiguous,
			"the timeout must not be reported with the ambiguous wording that led A-17's orchestrator to "+
				"believe the child might still be running")
	}
}

// TestDelegateSync_TimedOut_ReportsStoppedAndKillsChildShells: async=false.
func TestDelegateSync_TimedOut_ReportsStoppedAndKillsChildShells(t *testing.T) {
	bash, _ := newBashTool(t, false)
	bash.godMode = true
	spawner := &a17TimedOutSpawner{t: t, bash: bash, started: make(chan struct{}), release: make(chan struct{})}
	close(spawner.release) // sync: return the timeout as soon as the shell is up
	tool, lc := newA17DelegateTool(t, spawner)

	parentID := fmt.Sprintf("a17-sync-parent-%d", time.Now().UnixNano())
	ctx := WithTranscriptSessionID(WithAgentID(context.Background(), "ops-lead"), parentID)

	res := tool.Execute(ctx, map[string]any{
		"task": "write link_checker.py", "label": "builder", "async": false, "timeout_seconds": 1,
	})
	assertA17TimedOutReport(t, res)
	require.NotEmpty(t, spawner.childID)
	assert.Contains(t, res.ForLLM, spawner.childID, "the report must name the stopped session")

	rec, err := lc.Load(spawner.childID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleTimedOut, rec.State,
		"the durable record must say timed_out — not failed (ambiguous) and not cancelled (a user action)")

	require.Eventually(t, func() bool { return !pidAlive(spawner.childPID) }, 3*time.Second, 50*time.Millisecond,
		"the timed-out child's background shell must be killed, or it keeps running (and writing) after the "+
			"delegator was told the delegation ended")

	status := tool.Execute(ctx, map[string]any{"action": "status", "session_id": spawner.childID})
	require.False(t, status.IsError, "status lookup failed: %s", status.ForLLM)
	assert.True(t, strings.Contains(status.ForLLM, "status=timed_out"),
		"delegate status must report timed_out, got: %s", status.ForLLM)
}

// TestDelegateAsync_TimedOutAfterParentTurnEnded_ReportsStoppedNotCanceled:
// the default async=true path, with the delegating parent's tool context
// already cancelled before the child times out — the ordinary situation for a
// background delegation, and the one where the old ctx.Err() branch reported a
// timeout as "Task canceled during execution".
func TestDelegateAsync_TimedOutAfterParentTurnEnded_ReportsStoppedNotCanceled(t *testing.T) {
	bash, _ := newBashTool(t, false)
	bash.godMode = true
	spawner := &a17TimedOutSpawner{t: t, bash: bash, started: make(chan struct{}), release: make(chan struct{})}
	tool, lc := newA17DelegateTool(t, spawner)

	parentID := fmt.Sprintf("a17-async-parent-%d", time.Now().UnixNano())
	parentCtx, parentTurnEnded := context.WithCancel(
		WithTranscriptSessionID(WithAgentID(context.Background(), "ops-lead"), parentID))

	delivered := make(chan *ToolResult, 1)
	ack := tool.ExecuteAsync(parentCtx, map[string]any{
		"task": "write link_checker.py", "label": "builder", "timeout_seconds": 1,
	}, func(_ context.Context, r *ToolResult) { delivered <- r })
	require.False(t, ack.IsError, "async dispatch failed: %s", ack.ForLLM)
	require.True(t, ack.Async)

	select {
	case <-spawner.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the async spawner never started")
	}
	parentTurnEnded() // the parent's turn moved on before the child timed out
	close(spawner.release)

	var res *ToolResult
	select {
	case res = <-delivered:
	case <-time.After(10 * time.Second):
		t.Fatal("the async timeout result was never delivered to the delegator")
	}
	tool.WaitForAsyncTasks()

	assertA17TimedOutReport(t, res)

	rec, err := lc.Load(spawner.childID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleTimedOut, rec.State,
		"a background delegation that timed out after its parent turn ended must still be recorded "+
			"timed_out, never cancelled/stopped_by_user")

	require.Eventually(t, func() bool { return !pidAlive(spawner.childPID) }, 3*time.Second, 50*time.Millisecond,
		"the timed-out background child's shell must be killed")
}
