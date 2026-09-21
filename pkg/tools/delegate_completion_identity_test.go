package tools

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const maxToolIterationsNotice = "I've reached `max_tool_iterations` without a final response. Increase `max_tool_iterations` in config.json if this task needs more tool steps."

func TestDelegateAsyncCompletion_MaxToolIterationsNamesTask(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	spawned := make(chan SubTurnConfig, 1)
	tool.SetSpawner(spawnerFunc(func(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
		spawned <- cfg
		return &ToolResult{ForLLM: maxToolIterationsNotice}, nil
	}))
	t.Cleanup(tool.WaitForAsyncTasks)

	completed := make(chan *ToolResult, 1)
	ack := tool.ExecuteAsync(
		WithAgentID(context.Background(), "jim"),
		map[string]any{
			"task":  "render the document",
			"label": "build-docs-renderer",
		},
		func(_ context.Context, result *ToolResult) { completed <- result },
	)
	require.NotNil(t, ack)
	require.False(t, ack.IsError, "the background delegation must start successfully: %s", ack.ForLLM)
	taskID := extractTaskID(t, ack.ForLLM)
	childSessionID := extractDelegateSessionIDFromAck(t, ack.ForLLM)

	var spawnCfg SubTurnConfig
	select {
	case spawnCfg = <-spawned:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the delegated spawner call")
	}
	assert.Equal(t, taskID, spawnCfg.TaskID)
	assert.Equal(t, "build-docs-renderer", spawnCfg.TaskLabel)
	assert.Equal(t, childSessionID, spawnCfg.DelegateSessionID)

	var result *ToolResult
	select {
	case result = <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the delegated completion callback")
	}
	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "Label: build-docs-renderer")
	assert.Contains(t, result.ForLLM, "Task ID: "+taskID)
	assert.Contains(t, result.ForLLM, "Session: "+childSessionID)
	assert.Contains(t, result.ForLLM, maxToolIterationsNotice,
		"the delegator still needs to know which limit fired")
}

func TestDelegateSync_ThreadsGeneratedTaskIDToSpawner(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerAwait(func(context.Context, string) *DelegationDenial { return nil })
	tool.SetLifecycleStore(session.NewLifecycleStore(t.TempDir()))
	spawned := make(chan SubTurnConfig, 1)
	tool.SetSpawner(spawnerFunc(func(_ context.Context, cfg SubTurnConfig) (*ToolResult, error) {
		spawned <- cfg
		return &ToolResult{ForLLM: "done", ForUser: "done"}, nil
	}))

	result := tool.Execute(
		WithAgentID(context.Background(), "jim"),
		map[string]any{
			"task":  "render the document",
			"label": "build-docs-renderer",
			"async": false,
		},
	)
	require.NotNil(t, result)
	require.False(t, result.IsError, "sync delegation failed: %s", result.ForLLM)

	var spawnCfg SubTurnConfig
	select {
	case spawnCfg = <-spawned:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the synchronous delegated spawner call")
	}
	require.NotEmpty(t, spawnCfg.TaskID)
	assert.Regexp(t, `^delegate-[0-9]+$`, spawnCfg.TaskID)
	assert.Equal(t, "build-docs-renderer", spawnCfg.TaskLabel)

	tool.mu.Lock()
	state, ok := tool.tasks[spawnCfg.TaskID]
	tool.mu.Unlock()
	require.True(t, ok, "spawned task ID must identify the delegate tool's task record")
	assert.Equal(t, "render the document", state.Task)
}
