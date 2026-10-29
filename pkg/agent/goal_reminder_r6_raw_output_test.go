package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// External process FIXTURE, not an R6 acceptance test. Re-executes the already
// compiled Go test binary: no nested go build/test and no shell script doing
// fake registry work. The parent acceptance test requires its real pipe output.
func TestR6BackgroundPipeHelperProcess(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--r6-pipe-helper" {
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
	if err := grPipeHelperProcess(os.Args[index+1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func grPipeHelperProcess(dir string) error {
	fmt.Println("r6-helper-ready")
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0o600); err != nil {
		return err
	}
	emitted := false
	for {
		if _, err := os.Stat(filepath.Join(dir, "exit")); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !emitted {
			if _, err := os.Stat(filepath.Join(dir, "emit")); err == nil {
				fmt.Println("r6-raw-output-while-still-running")
				emitted = true
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		time.Sleep(10 * time.Millisecond) // process handshake sampling, never an R6 outcome oracle
	}
}

func grCopyProcessHelper(t *testing.T, dir string) string {
	t.Helper()
	source, err := os.Executable()
	require.NoError(t, err)
	name := "r6-process-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	in, err := os.Open(source)
	require.NoError(t, err)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	require.NoError(t, err)
	_, copyErr := io.Copy(out, in)
	require.NoError(t, in.Close())
	require.NoError(t, out.Close())
	require.NoError(t, copyErr)
	return path
}

func grShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

type grProcessActivityReader interface{ LastActivityAt() time.Time }

// Deferred only to the authorized API-only/full proof clones: the interface
// assertion keeps a523 compiling without importing production into this branch.
// A missing API is a SETUP seam, never counted as behavioral RED.
func TestGoalReminderRawBackgroundOutputContract(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t))
	workDir, err := workspace.EnsureWorkDir(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	dir, err := os.MkdirTemp(workDir, "r6-pipe-")
	require.NoError(t, err)
	helper := grCopyProcessHelper(t, workDir)
	command := grShellQuote(helper) + " -test.run=^TestR6BackgroundPipeHelperProcess$ -- --r6-pipe-helper " + grShellQuote(dir)
	if runtime.GOOS == "windows" {
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		command = "& " + quote(helper) + " '-test.run=^TestR6BackgroundPipeHelperProcess$' -- --r6-pipe-helper " + quote(dir)
	}
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	registered, ok := inst.Tools.Get("bash")
	require.True(t, ok)
	execTool, ok := registered.(tools.AsyncExecutor)
	require.True(t, ok, "use the actual registered async bash tool")
	ctx := tools.WithTranscriptSessionID(h.ctx, h.id)
	ctx = tools.WithAgentID(ctx, testDefaultAgentID)
	ctx = tools.WithWorkspaceID(ctx, testHarnessWorkspaceMembershipID)
	ctx = tools.WithTurnWorkspaceDir(ctx, workDir)
	finished := make(chan *tools.ToolResult, 1)
	result := execTool.ExecuteAsync(ctx, map[string]any{"action": "run", "command": command, "cwd": ".", "run_in_background": true}, func(_ context.Context, r *tools.ToolResult) { finished <- r })
	require.False(t, result.IsError, "real background process must start: %s", result.ForLLM)
	var response tools.ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &response))
	require.NotEmpty(t, response.SessionID)
	manager := tools.GetSharedSessionManager()
	ps, err := manager.Get(response.SessionID)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "exit"), []byte("finish"), 0o600))
		select {
		case terminal := <-finished:
			assert.False(t, terminal.IsError, "helper should exit naturally")
		case <-time.After(grWatchdog):
			t.Error("real helper completion writer did not join")
		}
		manager.Remove(ps.ID)
	})
	require.Equal(t, h.id, ps.OwnerSessionID)
	require.Eventually(t, func() bool { _, e := os.Stat(filepath.Join(dir, "ready")); return e == nil }, grWatchdog, 10*time.Millisecond, "helper must positively reach its real pipe writer")
	activity, ok := any(ps).(grProcessActivityReader)
	require.True(t, ok, "SETUP: authorized ProcessSession.LastActivityAt getter belongs in the parent proof scaffold; no timestamp is fabricated here")
	require.Eventually(t, func() bool { return !activity.LastActivityAt().IsZero() }, grWatchdog, 10*time.Millisecond, "actual stdout must stamp activity before any completion")
	require.Equal(t, "running", ps.GetStatus())
	// Establish the actual collector boundary while the process stays live.
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	grSetStreak(t, h, 1)
	before := activity.LastActivityAt()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "emit"), []byte("emit"), 0o600))
	require.Eventually(t, func() bool { return activity.LastActivityAt().After(before) }, grWatchdog, 10*time.Millisecond, "a second stdout write must advance actual activity WHILE still running")
	require.Equal(t, "running", ps.GetStatus(), "completion cannot mask a missing stdout activity writer")
	select {
	case <-finished:
		t.Fatal("helper exited before the raw-output discriminator")
	default:
	}
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	grNoPublishedReminder(t, h.al)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "actual new raw output resets the owning goal while busy; no callback/Read/StartTime substitute")
}
