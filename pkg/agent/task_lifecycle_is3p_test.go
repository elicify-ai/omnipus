// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_lifecycle_is3p_test.go pins the THIRD producer of the is-3p runtime
// fact: pkg/agent/task_executor.go::mintTaskLifecycleRecord. The launcher
// paths (steer_launcher.go::launchOrdinaryRoot/launchSteered) stamp
// LifecycleRecord.Is3P from the target's resolved executor kind; this producer
// (the task/plan-member dispatch chokepoint, createTaskSessionSync and
// StartTaskNow) did not — so an EXTERNAL-CLI task-mode session ran the CLI
// while carrying is_3p=false, and every 3P refusal in pkg/tools read the wrong
// value for it. Worse, mint logged-and-continued on a Persist failure, so a
// run could BEGIN with no durable classification at all.
//
// These tests drive the REAL production chokepoint (createTaskSessionSync /
// ExecuteTask), never a hand-built LifecycleRecord, mirroring
// task_lifecycle_producer_test.go's own governing discipline.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// externalCLIAgentConfig is a worker agent whose executor resolves to
// runner.DispatchKindExternalCLI (subagent_3p) — the exact shape
// SteerLauncher.Launch resolves for a steered child.
func externalCLIAgentConfig(id, home string) config.AgentConfig {
	return config.AgentConfig{
		ID: id, Name: "External CLI Agent", Type: config.AgentTypeWorker, Home: home,
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		},
	}
}

// unresolvableExecutorAgentConfig is a worker whose executor kind is the
// reserved remote-a2a — accepted in the schema, refused at dispatch by
// runner.ResolveDispatch (ErrRemoteA2AReserved). It is the "resolver failure"
// input requirement 2 of the brief is about.
func unresolvableExecutorAgentConfig(id, home string) config.AgentConfig {
	return config.AgentConfig{
		ID: id, Name: "Remote A2A Agent", Type: config.AgentTypeWorker, Home: home,
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindRemoteA2A},
		},
	}
}

// TestMintTaskLifecycleRecord_ExternalTask_StampsIs3P (brief requirement 3i):
// an EXTERNAL-CLI task's durable record must carry is_3p == true. Drives the
// real createTaskSessionSync chokepoint and reads the persisted record back.
func TestMintTaskLifecycleRecord_ExternalTask_StampsIs3P(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Agents.List = append(cfg.Agents.List, externalCLIAgentConfig("ext-agent", t.TempDir()))
	})

	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	al.taskExecutor.SetLifecycleStore(ls)

	tk := &task.Task{
		Title: "external lifecycle", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "ext-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	require.NoError(t, al.taskStore.Create(tk))

	sessionID, err := al.taskExecutor.createTaskSessionSync(tk)
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)

	rec, err := ls.Load(sessionID)
	require.NoError(t, err)
	assert.True(t, rec.Is3P,
		"an external-CLI task's durable record must carry is_3p=true (got false — the producer "+
			"did not stamp the resolved runtime)")
}

// TestMintTaskLifecycleRecord_NativeTask_StampsIs3PFalse is the honesty control
// for the test above: a native task must NOT be mislabelled external. Without
// it, a resolver that returned a constant true would pass the external test.
func TestMintTaskLifecycleRecord_NativeTask_StampsIs3PFalse(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	al.taskExecutor.SetLifecycleStore(ls)

	tk := &task.Task{
		Title: "native lifecycle", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	require.NoError(t, al.taskStore.Create(tk))

	sessionID, err := al.taskExecutor.createTaskSessionSync(tk)
	require.NoError(t, err)

	rec, err := ls.Load(sessionID)
	require.NoError(t, err)
	assert.False(t, rec.Is3P, "a native task must carry is_3p=false")
}

// failingLifecycleStoreDir returns a LifecycleStore whose Persist CANNOT
// succeed: a regular file occupies the parent of the store's directory, so
// every write into it fails (MkdirAll/open under a non-directory). It is the
// persistence-failure injection for the two refusal tests below.
func failingLifecycleStoreDir(t *testing.T) *session.LifecycleStore {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a directory"), 0o600))
	return session.NewLifecycleStore(filepath.Join(blocker, "session_lifecycle"))
}

// TestMintTaskLifecycleRecord_PersistFailure_RefusesRun (brief requirement
// 3ii): a Persist failure must REFUSE the run, never start it on an
// unpersisted classification. Drives the real ExecuteTask dispatch and asserts
// the task lands Failed (the truthful pre-dispatch failure) with the run never
// started.
func TestMintTaskLifecycleRecord_PersistFailure_RefusesRun(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	al.taskExecutor.SetLifecycleStore(failingLifecycleStoreDir(t))

	tk := &task.Task{
		Title: "persist will fail", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	require.NoError(t, al.taskStore.Create(tk))

	err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil)
	require.Error(t, err, "a durable-record persist failure must refuse the dispatch, not log-and-continue")

	got, gerr := al.taskStore.Get(tk.ID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusFailed, got.Status,
		"the task must be settled Failed by the pre-dispatch failure path, never left running")
}

// TestMintTaskLifecycleRecord_ResolverFailure_RefusesRun (brief requirement 2,
// resolver half): an unresolvable executor kind must REFUSE the run rather than
// minting a mismatched classification. Uses a remote-a2a agent (the reserved,
// at-dispatch-refused kind) and drives ExecuteTask.
func TestMintTaskLifecycleRecord_ResolverFailure_RefusesRun(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Agents.List = append(cfg.Agents.List, unresolvableExecutorAgentConfig("ra2a-agent", t.TempDir()))
	})

	ls := session.NewLifecycleStore(filepath.Join(t.TempDir(), "session_lifecycle"))
	al.taskExecutor.SetLifecycleStore(ls)

	tk := &task.Task{
		Title: "unresolvable executor", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "ra2a-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
	}
	require.NoError(t, al.taskStore.Create(tk))

	err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil)
	require.Error(t, err, "an unresolvable executor kind must refuse the dispatch, not mint a mismatched record")

	got, gerr := al.taskStore.Get(tk.ID)
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusFailed, got.Status,
		"the task must be settled Failed by the pre-dispatch failure path, never left running")
}
