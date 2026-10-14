package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-040 / BDD-4.8 / WU3 ruling Q1. Real public asynchronous execution, real
// storage adapter and real child. A terminal callback is awaited before cleanup.
func TestWU3EnvironmentSetupWorkspaceIDUsesTargetWithoutMovingAnchor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	for _, id := range []string{"default", "sales"} {
		require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: id, Name: id, Status: "active"}))
		_, err := workspace.EnsureWorkDir(home, id)
		require.NoError(t, err)
	}
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{Home: home, AgentWorkDir: workspace.WorkDir(home, "default"), SetupAuthority: true, GodMode: true, Store: storageSeamStore{}})
	ctx := WithTranscriptSessionID(WithTurnWorkspaceDir(WithWorkspaceID(WithAgentID(context.Background(), "omnipus"), "default"), workspace.WorkDir(home, "default")), "wu3-setup")
	done := make(chan *ToolResult, 1)
	start := tool.ExecuteAsync(ctx, map[string]any{"command": "echo wu3-environment-control", "purpose": "WU3 target setup proof", "scope": "workspace", "workspace_id": "sales"}, func(_ context.Context, result *ToolResult) { done <- result })
	require.False(t, start.IsError, "real setup must start: %s", start.ForLLM)
	var completed *ToolResult
	select {
	case completed = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("real setup completion was never delivered")
	}
	require.False(t, completed.IsError, "real child must succeed, not a false target failure: %s", completed.ForLLM)
	assert.Contains(t, completed.ForLLM, "wu3-environment-control", "positive child execution marker")
	targetReal, err := filepath.EvalSymlinks(workspace.WorkDir(home, "sales"))
	require.NoError(t, err)
	assert.Contains(t, start.ForLLM, targetReal, "explicit workspace_id must select Sales storage by filesystem identity")
	runtime, err := environmentsetup.RuntimeEnvPaths(home, workspace.WorkDir(home, "sales"))
	require.NoError(t, err)
	assert.NotEmpty(t, runtime.WorkspacePrefix, "target runtime has the real installed workspace layer")
	_, err = os.Stat(filepath.Join(workspace.WorkDir(home, "default"), ".omnipus", "env"))
	assert.ErrorIs(t, err, os.ErrNotExist, "caller anchor gets no install")
	assert.Equal(t, "default", ToolWorkspaceID(ctx), "caller execution scope unchanged")
	assert.Contains(t, completed.ForLLM, "command success only", "exit zero must not claim dependency readiness")
}

func TestWU3EnvironmentSetupUnknownWorkspaceIDRefusedBeforeAllocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_HOME", home)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "default", Name: "Default", Status: "active"}))
	_, err := workspace.EnsureWorkDir(home, "default")
	require.NoError(t, err)
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{Home: home, AgentWorkDir: workspace.WorkDir(home, "default"), SetupAuthority: true, GodMode: true, Store: storageSeamStore{}})
	ctx := WithTurnWorkspaceDir(WithWorkspaceID(context.Background(), "default"), workspace.WorkDir(home, "default"))
	done := make(chan *ToolResult, 1)
	result := tool.ExecuteAsync(ctx, map[string]any{"command": "echo should-not-run", "purpose": "unknown target", "workspace_id": "missing-workspace"}, func(_ context.Context, result *ToolResult) { done <- result })
	if !result.IsError {
		// Never leak a started background child on a failing baseline.
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("unexpected started child never completed")
		}
	}
	assert.True(t, result.IsError, "unknown explicit workspace must be refused, not installed into the caller anchor: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "missing-workspace", "specific invalid target visible")
	_, err = os.Stat(filepath.Join(workspace.WorkDir(home, "default"), ".omnipus", "env"))
	assert.ErrorIs(t, err, os.ErrNotExist, "refused target allocates no storage")
}
