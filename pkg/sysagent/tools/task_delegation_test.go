// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// §4 behavioral-parity gap (HIGH): the cross-workspace task tools
// (create_task_in_workspace / update_task_in_workspace / delete_task_in_workspace)
// must enforce the SAME FR-6.2 delegation policy + ownership gate the plain
// same-workspace create_task / update_task / delete_task tools enforce. This file
// proves the privileged cross-workspace surface is at least as restrictive as the
// same-workspace surface — never less.
//
// The gate is resolved dynamically via Deps.DelegationDeny (wired in production by
// the gateway to a closure over the live config). These tests install a stub
// resolver that denies delegation to a specific "forbidden" target, mirroring the
// FR-6.2 trust-set denial shape.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/task"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// denyDelegationTo returns a Deps.DelegationDeny stub that denies any delegation
// whose targetAgentID matches forbidden (and allows everything else, including
// self-assignment). It mirrors the FR-6.2 trust-set denial the production gate
// produces.
func denyDelegationTo(forbidden string) func(ctx context.Context, caller, target string) *tools.DelegationDenial {
	return func(_ context.Context, _ string, target string) *tools.DelegationDenial {
		if target == forbidden {
			return &tools.DelegationDenial{
				Reason:        "agent is not in this agent's delegation trust set ('to' allowlist)",
				Policy:        tools.DenyTrustSet,
				TargetAgentID: target,
			}
		}
		return nil
	}
}

// writeTask writes a task.Task directly to the home's tasks dir for setup.
func writeTask(t *testing.T, home string, tk task.Task) {
	t.Helper()
	tasksDir := filepath.Join(home, "tasks")
	require.NoError(t, os.MkdirAll(tasksDir, 0o700))
	if tk.CreatedAt == "" {
		tk.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if tk.UpdatedAt == "" {
		tk.UpdatedAt = tk.CreatedAt
	}
	if tk.Action == "" {
		tk.Action = task.ActionLLM
	}
	data, err := json.Marshal(tk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tasksDir, tk.ID+".json"), data, 0o600))
}

// assertDelegationDenied asserts that a tool result is a structured delegation
// denial (the JSON-encoded DelegationFailure with the trust_set policy axis).
func assertDelegationDenied(t *testing.T, result *tools.ToolResult) {
	t.Helper()
	require.True(t, result.IsError, "expected a delegation-denied error result; got: %s", result.ForLLM)
	var failure struct {
		Error  string `json:"error"`
		Reason string `json:"reason"`
		Policy string `json:"policy"`
		Tool   string `json:"tool"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &failure),
		"denial result must be JSON DelegationFailure; got: %s", result.ForLLM)
	assert.Equal(t, "delegation_denied", failure.Error)
	assert.Equal(t, string(tools.DenyTrustSet), failure.Policy)
	assert.NotEmpty(t, failure.Reason)
}

// --- create_task_in_workspace ---

// TestCreateTaskInWorkspace_DelegationDenied proves that assigning a cross-workspace
// task to an agent the caller may NOT delegate to is rejected by the FR-6.2 gate.
func TestCreateTaskInWorkspace_DelegationDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("forbidden-agent")

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := create.Execute(ctx, map[string]any{
		"name":         "Privileged cross-workspace task",
		"workspace_id": testWorkspaceID,
		"agent_id":     "forbidden-agent",
	})
	assertDelegationDenied(t, result)

	// No task file must have been written.
	entries, _ := os.ReadDir(filepath.Join(home, "tasks"))
	assert.Empty(t, entries, "a denied create must not persist a task")
}

// TestCreateTaskInWorkspace_DelegationAllowed proves permitted delegation and
// self-assignment both succeed.
func TestCreateTaskInWorkspace_DelegationAllowed(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("forbidden-agent")

	create := systools.NewTaskCreateTool(deps)

	// Permitted target (not the forbidden one).
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	r1 := create.Execute(ctx, map[string]any{
		"name":         "Allowed delegation",
		"workspace_id": testWorkspaceID,
		"agent_id":     "trusted-agent",
	})
	require.False(t, r1.IsError, "permitted delegation must succeed; got: %s", r1.ForLLM)

	// Self-assignment: caller assigns to itself — never delegation, always allowed,
	// even though the stub would deny "forbidden-agent". (Caller != forbidden.)
	r2 := create.Execute(ctx, map[string]any{
		"name":         "Self-assigned",
		"workspace_id": testWorkspaceID,
		"agent_id":     "caller-agent",
	})
	require.False(t, r2.IsError, "self-assignment must succeed; got: %s", r2.ForLLM)
}

// TestCreateTaskInWorkspace_NilGateFailOpen proves that when the hook is unwired
// (tests/standalone) the gate is skipped — matching the plain tool's no-op when
// its checker is unset. (Production always wires it.)
func TestCreateTaskInWorkspace_NilGateFailOpen(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	// deps.DelegationDeny left nil.

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := create.Execute(ctx, map[string]any{
		"name":         "Unwired gate",
		"workspace_id": testWorkspaceID,
		"agent_id":     "any-agent",
	})
	require.False(t, result.IsError, "unwired gate must fail-open; got: %s", result.ForLLM)
}

// TestCreateTaskInWorkspace_BlockerCrossWorkspaceRejected proves a blocked_by
// edge pointing at a task in a DIFFERENT workspace is rejected (parity with the
// plain tool's validateBlockersWorkspace).
func TestCreateTaskInWorkspace_BlockerCrossWorkspaceRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	const otherWS = "01JXOTHER_WORKSPACE00001"
	seedWorkspace(t, home, otherWS)

	// Blocker task lives in the OTHER workspace.
	writeTask(t, home, task.Task{
		ID:          "01JXBLOCKER0000000000001",
		Title:       "Blocker in other workspace",
		Status:      task.StatusNext,
		WorkspaceID: otherWS,
	})

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := create.Execute(ctx, map[string]any{
		"name":         "Dependent task",
		"workspace_id": testWorkspaceID,
		"blocked_by":   []any{"01JXBLOCKER0000000000001"},
	})
	require.True(t, result.IsError, "cross-workspace blocker must be rejected; got: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "different workspace")
}

// --- update_task_in_workspace ---

// TestUpdateTaskInWorkspace_OwnershipDenied proves a caller cannot mutate another
// agent's task when delegation to that agent is NOT permitted.
func TestUpdateTaskInWorkspace_OwnershipDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("forbidden-agent")

	// Task owned by (assigned to) the forbidden agent; caller is a different agent.
	writeTask(t, home, task.Task{
		ID:          "01JXOWNED00000000000001",
		Title:       "Owned by forbidden agent",
		Status:      task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "forbidden-agent",
		CreatedBy:   "forbidden-agent",
	})

	update := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := update.Execute(ctx, map[string]any{
		"id":     "01JXOWNED00000000000001",
		"status": "in_progress",
	})
	assertDelegationDenied(t, result)
}

// TestUpdateTaskInWorkspace_OwnershipAllowedForOwner proves the assignee/creator
// can always mutate its own task.
func TestUpdateTaskInWorkspace_OwnershipAllowedForOwner(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("forbidden-agent")

	writeTask(t, home, task.Task{
		ID:          "01JXOWNED00000000000002",
		Title:       "Owned by caller",
		Status:      task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "caller-agent",
		CreatedBy:   "caller-agent",
	})

	update := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := update.Execute(ctx, map[string]any{
		"id":     "01JXOWNED00000000000002",
		"status": "in_progress",
	})
	require.False(t, result.IsError, "owner must be able to update its own task; got: %s", result.ForLLM)
}

// TestUpdateTaskInWorkspace_ReassignmentDenied proves reassigning a task to a
// forbidden agent (re-delegation) is rejected even when the caller owns the task.
func TestUpdateTaskInWorkspace_ReassignmentDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("forbidden-agent")

	writeTask(t, home, task.Task{
		ID:          "01JXOWNED00000000000003",
		Title:       "Reassign me",
		Status:      task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "caller-agent",
		CreatedBy:   "caller-agent",
	})

	update := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := update.Execute(ctx, map[string]any{
		"id":       "01JXOWNED00000000000003",
		"agent_id": "forbidden-agent",
	})
	assertDelegationDenied(t, result)
}

// TestUpdateTaskInWorkspace_BlockerCrossWorkspaceRejected proves blocked_by edges
// must be same-workspace on update too.
func TestUpdateTaskInWorkspace_BlockerCrossWorkspaceRejected(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	const otherWS = "01JXOTHER_WORKSPACE00002"
	seedWorkspace(t, home, otherWS)

	writeTask(t, home, task.Task{
		ID:          "01JXBLOCKER0000000000002",
		Title:       "Blocker elsewhere",
		Status:      task.StatusNext,
		WorkspaceID: otherWS,
	})
	writeTask(t, home, task.Task{
		ID:          "01JXDEPENDENT00000000001",
		Title:       "Dependent",
		Status:      task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "caller-agent",
		CreatedBy:   "caller-agent",
	})

	update := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := update.Execute(ctx, map[string]any{
		"id":         "01JXDEPENDENT00000000001",
		"blocked_by": []any{"01JXBLOCKER0000000000002"},
	})
	require.True(t, result.IsError, "cross-workspace blocker must be rejected; got: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "different workspace")
}

// --- delete_task_in_workspace ---

// TestDeleteTaskInWorkspace_OwnershipDenied proves a caller cannot delete another
// agent's task when delegation to that agent is NOT permitted.
func TestDeleteTaskInWorkspace_OwnershipDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("forbidden-agent")

	writeTask(t, home, task.Task{
		ID:          "01JXDELETE0000000000001",
		Title:       "Owned by forbidden agent",
		Status:      task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "forbidden-agent",
		CreatedBy:   "forbidden-agent",
	})

	del := systools.NewTaskDeleteTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := del.Execute(ctx, map[string]any{
		"id":      "01JXDELETE0000000000001",
		"confirm": true,
	})
	assertDelegationDenied(t, result)

	// The task must still exist.
	_, statErr := os.Stat(filepath.Join(home, "tasks", "01JXDELETE0000000000001.json"))
	assert.NoError(t, statErr, "denied delete must not remove the task file")
}

// TestDeleteTaskInWorkspace_OwnershipAllowedForOwner proves the owner can delete
// its own task.
func TestDeleteTaskInWorkspace_OwnershipAllowedForOwner(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("forbidden-agent")

	writeTask(t, home, task.Task{
		ID:          "01JXDELETE0000000000002",
		Title:       "Owned by caller",
		Status:      task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "caller-agent",
		CreatedBy:   "caller-agent",
	})

	del := systools.NewTaskDeleteTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := del.Execute(ctx, map[string]any{
		"id":      "01JXDELETE0000000000002",
		"confirm": true,
	})
	require.False(t, result.IsError, "owner must be able to delete its own task; got: %s", result.ForLLM)
}

// --- subagent_3p (external-CLI) worker task-assignment guard (SEC) ---
//
// Closes the gap where a legitimate FR-6.2 delegation ALLOW (a real, permitted
// trust-set/mode/depth edge to a subagent_3p target) was sufficient on its own
// to let create_task_in_workspace/update_task_in_workspace persist
// agent_id=<subagent_3p>, even though TaskExecutor.processTaskDirect routes
// every task run through the native engine unconditionally (pkg/agent/loop.go)
// — silently mis-executing on the wrong engine with full system-level tool
// access instead of the configured external CLI. This guard is independent
// of, and checked before, the delegation-policy gate above.

const externalCLIWorkerID = "external-worker"

// seedExternalCLIWorker registers a subagent_3p (external-CLI) worker with the
// given ID directly on deps' live config, so (*config.Config).IsExternalCLIWorkerID
// resolves it via deps.GetCfg — the exact lookup path
// create_task_in_workspace/update_task_in_workspace use in production.
func seedExternalCLIWorker(t *testing.T, deps *systools.Deps, id string) {
	t.Helper()
	require.NotNil(t, deps.GetCfg, "seedExternalCLIWorker: deps.GetCfg must be wired")
	cfg := deps.GetCfg()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:   id,
		Name: id,
		Type: config.AgentTypeWorker,
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{
				Kind: config.ExecutorKindExternalCLI,
				CLI:  "claude-code",
			},
		},
	})
}

// TestCreateTaskInWorkspace_RejectsSubagent3pWorker_EvenWhenDelegationAllows
// proves the subagent_3p guard blocks task creation even when the FR-6.2
// delegation gate ALLOWS the target — the exploit this closes is specifically
// that a real, permitted delegation edge was previously sufficient on its own.
func TestCreateTaskInWorkspace_RejectsSubagent3pWorker_EvenWhenDelegationAllows(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	seedExternalCLIWorker(t, deps, externalCLIWorkerID)
	deps.DelegationDeny = func(context.Context, string, string) *tools.DelegationDenial {
		return nil // delegation policy ALLOWS — a real, permitted edge exists
	}

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "jim")
	result := create.Execute(ctx, map[string]any{
		"name":         "Leaks to external CLI",
		"workspace_id": testWorkspaceID,
		"agent_id":     externalCLIWorkerID,
	})

	require.True(t, result.IsError, "expected the subagent_3p target to be rejected; got: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "subagent_3p", "the error must name subagent_3p/external-CLI")

	entries, _ := os.ReadDir(filepath.Join(home, "tasks"))
	assert.Empty(t, entries, "a rejected create must not persist a task")
}

// TestUpdateTaskInWorkspace_RejectsSubagent3pWorkerReassignment_EvenWhenDelegationAllows
// mirrors the create-path test for update_task_in_workspace's reassignment path.
func TestUpdateTaskInWorkspace_RejectsSubagent3pWorkerReassignment_EvenWhenDelegationAllows(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	seedExternalCLIWorker(t, deps, externalCLIWorkerID)
	deps.DelegationDeny = func(context.Context, string, string) *tools.DelegationDenial {
		return nil // delegation policy ALLOWS
	}

	writeTask(t, home, task.Task{
		ID:          "01JXSUBAGENT3P00000000001",
		Title:       "Reassign to external CLI",
		Status:      task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "caller-agent",
		CreatedBy:   "caller-agent",
	})

	update := systools.NewTaskUpdateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := update.Execute(ctx, map[string]any{
		"id":       "01JXSUBAGENT3P00000000001",
		"agent_id": externalCLIWorkerID,
	})

	require.True(t, result.IsError,
		"expected the subagent_3p reassignment to be rejected; got: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "subagent_3p", "the error must name subagent_3p/external-CLI")

	store := task.New(filepath.Join(home, "tasks"))
	got, err := store.Get("01JXSUBAGENT3P00000000001")
	require.NoError(t, err)
	assert.Equal(t, "caller-agent", got.AgentID, "agent_id must be unchanged after a rejected reassignment")
}

// TestCreateTaskInWorkspace_ExternalCLICheck_NilGetCfgFailsOpen proves that when
// deps.GetCfg is unwired (nil) the subagent_3p guard is skipped — matching every
// other GetCfg-gated check in this package (e.g. get_usage's exclude filter).
// The production gateway always wires GetCfg (pkg/gateway/gateway.go).
func TestCreateTaskInWorkspace_ExternalCLICheck_NilGetCfgFailsOpen(t *testing.T) {
	home := t.TempDir()
	seedWorkspace(t, home, testWorkspaceID)
	deps := &systools.Deps{Home: home} // GetCfg intentionally left nil

	create := systools.NewTaskCreateTool(deps)
	ctx := tools.WithAgentID(context.Background(), "caller-agent")
	result := create.Execute(ctx, map[string]any{
		"name":         "Unwired GetCfg",
		"workspace_id": testWorkspaceID,
		"agent_id":     externalCLIWorkerID,
	})
	require.False(t, result.IsError, "unwired GetCfg must fail-open; got: %s", result.ForLLM)
}
