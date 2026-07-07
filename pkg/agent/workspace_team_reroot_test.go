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

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): write_file needs
	// an explicit agent-level grant, or it fails closed to "deny" and the
	// re-rooting behavior under test never gets exercised.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	const sessionKey = "test-session-coreteam-reroot"
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "please write proof.txt for me", sessionKey)
	require.NoError(t, err, "ProcessDirect must succeed")
	assert.Equal(t, "done", finalContent)

	sharedFile := filepath.Join(workspacesDir, "team-ws", "work", "proof.txt")
	got, readErr := os.ReadFile(sharedFile)
	require.NoError(
		t,
		readErr,
		"write_file must have landed in the workspace's dedicated work/ directory (%s) — CoreTeam membership must re-root the turn's filesystem there",
		sharedFile,
	)
	assert.Equal(t, "hello-from-team-workspace", string(got))

	// The agent's own private directory must NOT have received the file.
	privateFile := filepath.Join(agentWorkspaceDir, "proof.txt")
	_, statErr := os.Stat(privateFile)
	assert.True(t, os.IsNotExist(statErr),
		"write_file must NOT land in the agent's own private directory (%s) when it is a CoreTeam member", privateFile)

	// The workspace root itself (one level up from work/) must NOT have
	// received the file either — proving the re-root target is the
	// dedicated work/ subdirectory, not the workspace's own directory (which
	// also holds AGENT.md and the shared memory room).
	workspaceRootFile := filepath.Join(workspacesDir, "team-ws", "proof.txt")
	_, rootStatErr := os.Stat(workspaceRootFile)
	assert.True(
		t,
		os.IsNotExist(rootStatErr),
		"write_file must NOT land directly in the workspace's own directory (%s) — only in its work/ subdirectory",
		workspaceRootFile,
	)
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
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	// No-default-policy model (CLAUDE.md hard constraint 6): write_file needs
	// an explicit agent-level grant, or it fails closed to "deny" and the
	// non-CoreTeam write-path under test never gets exercised.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

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

// TestRunTurn_CoreTeamMember_CannotEscapeWorkToWorkspaceRoot proves the
// security property motivating the work/ subdirectory design: re-rooting a
// CoreTeam member's file tools to workspaces/<id>/work/ (not
// workspaces/<id>/ itself) means AGENT.md and the shared memory room, one
// level up, are structurally unreachable — a relative "../" escape attempt is
// rejected by the os.Root confinement itself, not by a guard that has to
// separately recognize the workspaces/<id>/ layout (pkg/tools/metadata_guard.go's
// app-level guard only recognizes agents/<id>/AGENT.md and does NOT match
// workspaces/<id>/AGENT.md — this test does not rely on that guard at all).
func TestRunTurn_CoreTeamMember_CannotEscapeWorkToWorkspaceRoot(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentWorkspaceDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentWorkspaceDir, 0o755))

	workspacesDir := filepath.Join(tmpHome, "workspaces")
	wsDir := filepath.Join(workspacesDir, "team-ws")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	wsJSON := `{"id":"team-ws","core_team":["main"]}`
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "team-ws.json"), []byte(wsJSON), 0o644))

	// A real AGENT.md at the workspace root, with known content, that the
	// re-rooted turn must never be able to touch.
	agentMdPath := filepath.Join(wsDir, "AGENT.md")
	require.NoError(t, os.WriteFile(agentMdPath, []byte("original project instructions"), 0o600))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"../AGENT.md","content":"pwned","overwrite":true}`).
		WithText("done")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:           agentWorkspaceDir,
				ModelName:           "scripted-model",
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	const sessionKey = "test-session-coreteam-escape-attempt"
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "please write ../AGENT.md for me", sessionKey)
	require.NoError(
		t,
		err,
		"ProcessDirect must succeed (the tool call itself is expected to be rejected, not the turn)",
	)
	assert.Equal(t, "done", finalContent)

	// AGENT.md must be completely untouched.
	got, readErr := os.ReadFile(agentMdPath)
	require.NoError(t, readErr, "AGENT.md must still exist")
	assert.Equal(
		t,
		"original project instructions",
		string(got),
		"a CoreTeam member's write_file must NOT be able to reach workspaces/<id>/AGENT.md via a relative escape from work/",
	)
}
