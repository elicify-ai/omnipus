// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// workspace_team_reroot_test.go — drives runTurn end-to-end (via ProcessDirect)
// to prove the CoreTeam-membership-driven filesystem re-rooting in runTurn
// (pkg/agent/loop.go): an agent that belongs to a Workspace's core_team writes
// into that Workspace's own shared directory instead of its private per-agent
// directory, regardless of whether the turn carries a channel-bound
// workspace_id (ts.opts.WorkspaceID is empty here — ProcessDirect is not
// channel-bound — which is exactly the divergence case the re-rooting design
// is meant to cover: CoreTeam membership, not the turn-carried workspace_id,
// drives the re-root).
//
// Uses the ScenarioProvider harness (already bridged into runTurn by
// scenario_runturn_test.go) to script a single write_file tool call, avoiding
// any real LLM call — the effective root is asserted purely from where the
// file actually landed on disk.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestRunTurn_CoreTeamMember_WritesToWorkspaceSharedDir proves the operator
// requirement: an agent that belongs to a Workspace's core_team runs its
// turn rooted at that Workspace's shared directory, not its own agents/<id>/
// directory — even for a top-level (non-delegated) turn with no channel
// binding at all.
func TestRunTurn_CoreTeamMember_WritesToWorkspaceSharedDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	// Persist a real workspace record whose core_team includes "main" (the
	// default agent ID — routing.DefaultAgentID — assigned when cfg.Agents.List
	// carries no explicit entries).
	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(workspacesDir, 0o755))
	wsJSON := `{"id":"team-ws","core_team":["main"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "team-ws.json"), []byte(wsJSON), 0o644))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"hello-from-team-workspace","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         agentWorkspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// Sandboxed (os.Root-relative) file resolution — required for a
				// relative write_file path to resolve against the workspace root
				// (fixed or re-rooted) instead of the process CWD.
				RestrictToWorkspace: true,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	const sessionKey = "test-session-coreteam-reroot"
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "please write proof.txt for me", sessionKey)
	require.NoError(t, err, "ProcessDirect must succeed")
	assert.Equal(t, "done", finalContent)

	sharedFile := filepath.Join(workspacesDir, "team-ws", "proof.txt")
	got, readErr := os.ReadFile(sharedFile)
	require.NoError(t, readErr,
		"write_file must have landed in the workspace's SHARED directory (%s) — CoreTeam membership must re-root the turn's filesystem", sharedFile)
	assert.Equal(t, "hello-from-team-workspace", string(got))

	// The agent's own private directory must NOT have received the file.
	privateFile := filepath.Join(agentWorkspaceDir, "proof.txt")
	_, statErr := os.Stat(privateFile)
	assert.True(t, os.IsNotExist(statErr),
		"write_file must NOT land in the agent's own private directory (%s) when it is a CoreTeam member", privateFile)
}

// TestRunTurn_NotCoreTeamMember_WritesToOwnDir confirms the negative case:
// with no workspace claiming "main" in its core_team, write_file lands in the
// agent's own directory exactly as before the CoreTeam-based re-rooting was
// introduced.
func TestRunTurn_NotCoreTeamMember_WritesToOwnDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	// No workspaces/ directory at all — "main" is not a CoreTeam member of
	// anything.
	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"solo-agent-write","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         agentWorkspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				// Sandboxed (os.Root-relative) file resolution — required for a
				// relative write_file path to resolve against the workspace root
				// (fixed or re-rooted) instead of the process CWD.
				RestrictToWorkspace: true,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	const sessionKey = "test-session-no-coreteam"
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "please write proof.txt for me", sessionKey)
	require.NoError(t, err, "ProcessDirect must succeed")
	assert.Equal(t, "done", finalContent)

	privateFile := filepath.Join(agentWorkspaceDir, "proof.txt")
	got, readErr := os.ReadFile(privateFile)
	require.NoError(t, readErr, "write_file must land in the agent's own directory when it is not a CoreTeam member")
	assert.Equal(t, "solo-agent-write", string(got))
}
