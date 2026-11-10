package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Quiet re-exec fixture for TestGoalReminderR6CompletionSurvivesManagerCleanup.
// With no helper flag it returns immediately. The child writes no stdout and no
// stderr; the parent learns it is running from a ready file outside the workspace.
func TestR6CarryGapQuietHelperProcess(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--r6-carry-gap-quiet-helper" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	if index+1 >= len(os.Args) {
		fmt.Fprintln(os.Stderr, "missing helper handshake directory")
		os.Exit(2)
	}
	if err := r6CarryGapQuietHelper(os.Args[index+1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func r6CarryGapQuietHelper(dir string) error {
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0o600); err != nil {
		return err
	}
	for {
		_, err := os.Stat(filepath.Join(dir, "release"))
		if err == nil {
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(10 * time.Millisecond) // handshake sampling, never an R6 outcome oracle
	}
}

type r6CarryGapCommand struct {
	ps       *tools.ProcessSession
	dir      string
	finished chan *tools.ToolResult
	joined   bool
}

func (c *r6CarryGapCommand) release() error {
	return os.WriteFile(filepath.Join(c.dir, "release"), []byte("release"), 0o600)
}

func (c *r6CarryGapCommand) join(t *testing.T) *tools.ToolResult {
	t.Helper()
	if c.joined {
		return nil
	}
	select {
	case terminal := <-c.finished:
		c.joined = true
		return terminal
	case <-time.After(grWatchdog):
		t.Fatal("real command completion did not join")
		return nil
	}
}

// TestGoalReminderR6CompletionSurvivesManagerCleanup is founder R6: completion
// of an owned command is progress. The command is quiet while it runs, so the
// only new clock is cmd.Wait. Removing the manager entry before the next
// observation must not erase that clock. E-B27 rebuilds the process map from
// the manager and therefore increments instead of resetting.
func TestGoalReminderR6CompletionSurvivesManagerCleanup(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	turnID := r6CarryGapRequireUnlatchedPending(t, h)
	cmd := r6CarryGapStartQuiet(t, h, h.id)
	require.True(t, cmd.ps.LastActivityAt().IsZero(), "a quiet running command has no activity clock yet")
	require.Equal(t, "running", cmd.ps.GetStatus())

	grSetStreak(t, h, 1)
	r6CarryGapSettleRunning(t, h, cmd, turnID)
	assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "owned running work is busy, not progress, and must not increment")

	require.NoError(t, cmd.release())
	terminal := cmd.join(t)
	require.NotNil(t, terminal)
	assert.False(t, terminal.IsError, "the quiet command must exit naturally")
	assert.Equal(t, "done", cmd.ps.GetStatus(), "completion must be the real terminal status")
	completedAt := cmd.ps.LastActivityAt()
	require.False(t, completedAt.IsZero(), "cmd.Wait must stamp activity even when the command wrote nothing")
	_, stillThere := tools.GetSharedSessionManager().Get(cmd.ps.ID)
	require.NoError(t, stillThere)
	tools.GetSharedSessionManager().Remove(cmd.ps.ID)
	_, gone := tools.GetSharedSessionManager().Get(cmd.ps.ID)
	require.ErrorIs(t, gone, tools.ErrSessionNotFound)
	require.Equal(t, completedAt, cmd.ps.LastActivityAt(), "removing the manager entry must not clear the retained clock")
	r6CarryGapAssertRetained(t, h, cmd.ps)

	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "the retained completion resets this pending reminder; forgetting it would increment to 2")
	r6CarryGapAssertAssessed(t, h, turnID)
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State))
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	r6CarryGapAssertForeignCompletionIgnored(t, h)
}

func r6CarryGapRequireUnlatchedPending(t *testing.T, h *grHarness) string {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	require.NotNil(t, work.pending, "the text reminder must be waiting for assessment")
	require.False(t, work.observedProgress, "no earlier tool, file, or stdout latch may stand in for completion")
	require.NotEmpty(t, work.pending.turnID)
	return work.pending.turnID
}

func r6CarryGapStartQuiet(t *testing.T, h *grHarness, owner string) *r6CarryGapCommand {
	t.Helper()
	workDir, err := workspace.EnsureWorkDir(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	handshake := t.TempDir() // outside the workspace, so the ready file cannot be a goal diff
	helper := grCopyProcessHelper(t, workDir)
	command := r6CarryGapQuietCommand(helper, handshake)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	registered, ok := inst.Tools.Get("bash")
	require.True(t, ok)
	execTool, ok := registered.(tools.AsyncExecutor)
	require.True(t, ok, "the registered async bash tool must start the real background command")
	ctx := tools.WithTranscriptSessionID(h.ctx, owner)
	ctx = tools.WithAgentID(ctx, testDefaultAgentID)
	ctx = tools.WithWorkspaceID(ctx, testHarnessWorkspaceMembershipID)
	ctx = tools.WithTurnWorkspaceDir(ctx, workDir)
	finished := make(chan *tools.ToolResult, 1)
	result := execTool.ExecuteAsync(ctx, map[string]any{
		"action": "run", "command": command, "cwd": ".", "run_in_background": true,
	}, func(_ context.Context, terminal *tools.ToolResult) { finished <- terminal })
	require.False(t, result.IsError, "real background command must start: %s", result.ForLLM)
	var response tools.ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &response))
	require.NotEmpty(t, response.SessionID)
	ps, err := tools.GetSharedSessionManager().Get(response.SessionID)
	require.NoError(t, err)
	require.Equal(t, owner, ps.OwnerSessionID)
	cmd := &r6CarryGapCommand{ps: ps, dir: handshake, finished: finished}
	t.Cleanup(func() {
		require.NoError(t, cmd.release())
		cmd.join(t)
		tools.GetSharedSessionManager().Remove(ps.ID)
	})
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(filepath.Join(handshake, "ready"))
		return statErr == nil && ps.GetStatus() == "running"
	}, grWatchdog, 10*time.Millisecond, "the quiet command must be running before it is released")
	return cmd
}

func r6CarryGapQuietCommand(helper, handshake string) string {
	if runtime.GOOS == "windows" {
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		return "& " + quote(helper) + " '-test.run=^TestR6CarryGapQuietHelperProcess$' -- --r6-carry-gap-quiet-helper " + quote(handshake)
	}
	return grShellQuote(helper) + " -test.run=^TestR6CarryGapQuietHelperProcess$ -- --r6-carry-gap-quiet-helper " + grShellQuote(handshake)
}

func r6CarryGapSettleRunning(t *testing.T, h *grHarness, cmd *r6CarryGapCommand, turnID string) {
	t.Helper()
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	require.True(t, cmd.ps.LastActivityAt().IsZero(), "the running observation must not invent an activity clock")
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	require.Equal(t, cmd.ps, work.processes[cmd.ps.ID], "the running observation must retain this process itself")
	assert.False(t, work.observedProgress, "a quiet running command must not create a progress latch")
	require.NotNil(t, work.pending)
	assert.Equal(t, turnID, work.pending.turnID)
}

func r6CarryGapAssertRetained(t *testing.T, h *grHarness, ps *tools.ProcessSession) {
	t.Helper()
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	assert.Equal(t, ps, work.processes[ps.ID], "manager removal must leave the already retained process in place until the next observation")
}

func r6CarryGapAssertForeignCompletionIgnored(t *testing.T, h *grHarness) {
	t.Helper()
	foreignOwner := newTestSteeringSession(t, h.al, testHarnessWorkspaceMembershipID)
	require.NotEqual(t, h.id, foreignOwner)
	foreign := r6CarryGapStartQuiet(t, h, foreignOwner)
	require.True(t, foreign.ps.LastActivityAt().IsZero())
	require.NoError(t, foreign.release())
	terminal := foreign.join(t)
	require.NotNil(t, terminal)
	assert.False(t, terminal.IsError)
	require.False(t, foreign.ps.LastActivityAt().IsZero(), "the foreign completion clock must be real")
	_, stillThere := tools.GetSharedSessionManager().Get(foreign.ps.ID)
	require.NoError(t, stillThere, "the foreign command stays listed, so an owner-blind scan could see it")
	select {
	case msg := <-h.al.bus.InboundChan():
		require.Equal(t, h.id, msg.AsyncTranscriptSessionID)
		grTextReminder(t, h, msg)
		grSetStreak(t, h, 2)
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "neither the old owned completion nor the foreign command may reset a later text outcome")
	default:
		grSetStreak(t, h, 2)
		h.age(t)
		h.al.goalQuietWindowSettle(time.Now())
		assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "the foreign completion must not reset this goal, and the owned completion is not fresh again")
	}
}
