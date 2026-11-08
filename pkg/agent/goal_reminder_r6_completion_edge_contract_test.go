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

// Process fixture for TestGoalReminderR6CompletionAfterObservedOutput. It is
// not an acceptance test: with no helper flag it returns immediately. The
// parent re-executes the test binary so the real bash completion path can
// join cmd.Wait. The child prints one stdout line, then waits, then exits
// without writing anything else.
func TestR6SignalGapCompletionHelperProcess(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--r6-signal-gap-completion-helper" {
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
	if err := r6SignalGapCompletionHelper(os.Args[index+1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func r6SignalGapCompletionHelper(dir string) error {
	fmt.Println("r6-signal-gap-stdout-while-running")
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

type r6SignalGapCommand struct {
	h        *grHarness
	ps       *tools.ProcessSession
	dir      string
	finished chan *tools.ToolResult
	joined   bool
}

func (c *r6SignalGapCommand) release() error {
	return os.WriteFile(filepath.Join(c.dir, "release"), []byte("release"), 0o600)
}

func (c *r6SignalGapCommand) join(t *testing.T) *tools.ToolResult {
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

func r6SignalGapStartOwnedCommand(t *testing.T, h *grHarness) *r6SignalGapCommand {
	t.Helper()
	workDir, err := workspace.EnsureWorkDir(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	dir, err := os.MkdirTemp(workDir, "r6-signal-gap-completion-")
	require.NoError(t, err)
	helper := grCopyProcessHelper(t, workDir)
	command := grShellQuote(helper) + " -test.run=^TestR6SignalGapCompletionHelperProcess$ -- --r6-signal-gap-completion-helper " + grShellQuote(dir)
	if runtime.GOOS == "windows" {
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		command = "& " + quote(helper) + " '-test.run=^TestR6SignalGapCompletionHelperProcess$' -- --r6-signal-gap-completion-helper " + quote(dir)
	}
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	registered, ok := inst.Tools.Get("bash")
	require.True(t, ok)
	execTool, ok := registered.(tools.AsyncExecutor)
	require.True(t, ok, "the registered async bash tool must start the real background command")
	ctx := tools.WithTranscriptSessionID(h.ctx, h.id)
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
	require.Equal(t, h.id, ps.OwnerSessionID, "the command must belong to this goal session, not another owner")
	cmd := &r6SignalGapCommand{h: h, ps: ps, dir: dir, finished: finished}
	t.Cleanup(func() {
		require.NoError(t, cmd.release())
		cmd.join(t)
		tools.GetSharedSessionManager().Remove(ps.ID)
	})
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(filepath.Join(dir, "ready"))
		return statErr == nil && !ps.LastActivityAt().IsZero() && ps.GetStatus() == "running"
	}, grWatchdog, 10*time.Millisecond, "stdout must stamp activity while the command is still running")
	return cmd
}

func r6SignalGapSettleBusy(t *testing.T, h *grHarness) {
	t.Helper()
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
}

// r6SignalGapConsumeStdoutWindow marks the stdout observation consumed. The
// busy settle advances the watermark but retains the outcome latch so a
// still-pending reminder can count work seen while the command was busy.
// Leaving that latch would let the already-consumed stdout stand in for the
// later completion. This is the existing consume function, not a new hook.
func r6SignalGapConsumeStdoutWindow(t *testing.T, h *grHarness, stdoutAt time.Time) {
	t.Helper()
	mark := goalReminderWatermark(h.goalID)
	require.False(t, mark.IsZero())
	require.True(t, mark.After(stdoutAt), "the keeper must move the activity window past observed stdout while the command is still running")
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	consumeGoalReminderProgress(h.goalID, work, time.Now().UTC())
	work.mu.Unlock()
}

// TestGoalReminderR6CompletionAfterObservedOutput: real stdout is progress
// while an owned command is busy. After that window is consumed, an unchanged
// poll is not progress. The command's own completion, with no new stdout, is
// progress and resets the streak. Founder R6: output or completion is
// progress; owned running work does not publish or increment.
func TestGoalReminderR6CompletionAfterObservedOutput(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	cmd := r6SignalGapStartOwnedCommand(t, h)
	stdoutAt := cmd.ps.LastActivityAt()
	require.False(t, stdoutAt.IsZero())
	require.Equal(t, "running", cmd.ps.GetStatus())

	grSetStreak(t, h, 1)
	r6SignalGapSettleBusy(t, h)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "real stdout resets the owning goal while its command is still running")
	assert.Equal(t, "running", cmd.ps.GetStatus(), "stdout progress must not require the command to finish")
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	r6SignalGapConsumeStdoutWindow(t, h, stdoutAt)

	grSetStreak(t, h, 2)
	r6SignalGapSettleBusy(t, h)
	assert.Equal(t, stdoutAt, cmd.ps.LastActivityAt(), "an unchanged poll must not invent a new activity stamp")
	assert.Equal(t, "running", cmd.ps.GetStatus())
	assert.Equal(t, 2, goalRecordForSession(t, h.id).ZeroOutputPushes, "an unchanged poll of a busy owned command is not progress and must not increment")
	select {
	case <-cmd.finished:
		t.Fatal("the command exited before the completion discriminator")
	default:
	}

	require.NoError(t, cmd.release())
	terminal := cmd.join(t)
	require.NotNil(t, terminal)
	assert.False(t, terminal.IsError, "the command must exit naturally with no further stdout")
	assert.Equal(t, "done", cmd.ps.GetStatus(), "completion must be the real terminal status, not a status substitute")
	assert.True(t, cmd.ps.LastActivityAt().After(stdoutAt), "command completion must advance activity past the stdout stamp the keeper already consumed")

	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "owned command completion resets the seeded streak; it must not count as another unproductive outcome")
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State), "completion progress must not end the goal")
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine))
	grTextReminder(t, h, next)
}
