// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_executor_workspace_routing_test.go covers DoD item 5: a NATIVE
// (non-external-CLI) task run must root its turn's filesystem at the TASK's
// own WorkspaceID when the assigned agent belongs to 2+ workspaces' core
// team — not at whichever workspace workspace.FindForAgent's
// sort.Strings(matches)[0] fallback happens to pick.
//
// Root cause: processTaskDirect's native-dispatch processOptions literal
// (loop.go, the al.runAgentLoop call inside processTaskDirect) never set
// WorkspaceID, while its external-CLI sibling
// (processTaskDirectExternalCLI) correctly sets
// WorkspaceID: tools.ToolWorkspaceID(ctx). runTurn's re-root block resolves
// the work dir via
// FindForAgentPreferring(home, agentID, ts.opts.WorkspaceID) — reading
// ts.opts, NOT the context directly — so leaving processOptions.WorkspaceID
// unset meant a native task run ALWAYS fell through FindForAgentPreferring's
// preferredWsID fast path to plain FindForAgent, which arbitrarily picks the
// alphabetically-first workspace among the agent's memberships whenever it
// belongs to more than one. The correct value was already on the context
// (task_executor.go's runTask sets tools.WithWorkspaceID(ctx, t.WorkspaceID)
// before calling processTaskDirect) — this test proves the fix threads it
// through processOptions too, so runTurn actually sees it.
//
// This matters now because the S1 plan-gate fix's judge resolves the TASK's
// workspace correctly — so before this fix, when the two disagreed, dispatch
// was the liar and it could be misdiagnosed as a judge regression.

package agent

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
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestExecuteTask_NativeDispatch_RootsAtTaskWorkspaceID_WhenAgentBelongsToMultipleWorkspaces(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	agentHomeDir := filepath.Join(tmpHome, "agents", "main")
	require.NoError(t, os.MkdirAll(agentHomeDir, 0o755))

	// "main" — an ordinary, explicitly-registered agent (below,
	// cfg.Agents.List; no implicit sentinel anymore) — belongs to TWO
	// workspaces' core_team.
	// "ws-aaa" sorts before "ws-zzz" — workspace.FindForAgent's own
	// sorted-first tie-break would pick "ws-aaa" if this dispatch ever fell
	// through to it. The task below names "ws-zzz" as its own WorkspaceID, so
	// a pass here proves the TASK's own workspace won, not the sort order.
	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(workspacesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "ws-aaa.json"),
		[]byte(`{"id":"ws-aaa","core_team":["main"]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workspacesDir, "ws-zzz.json"),
		[]byte(`{"id":"ws-zzz","core_team":["main"]}`), 0o644))

	provider := testutil.NewScenario().
		WithToolCall("write_file", `{"path":"proof.txt","content":"hello-from-ws-zzz","overwrite":true}`).
		WithText(successMarkerBody)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                agentHomeDir,
				DefaultModel:        config.DefaultModel{Model: "scripted-model"},
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "main"}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()
	defaultAgent := al.registry.GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected default agent")
	// No-default-policy model (CLAUDE.md hard constraint 6): write_file needs
	// an explicit agent-level grant, or it fails closed to "deny" and this
	// test never actually exercises the re-rooting behavior under test.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"write_file": "allow"},
	})

	tk := &task.Task{
		Title:       "cross-workspace routing task",
		Prompt:      "please write proof.txt for me",
		Action:      task.ActionLLM,
		AgentID:     defaultAgent.ID,
		Priority:    3,
		WorkspaceID: "ws-zzz",
		Status:      task.StatusNext,
	}
	require.NoError(t, al.taskStore.Create(tk))

	require.NoError(t, al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil))
	final := waitForCompletionContractTerminal(t, al, tk.ID)
	require.Equal(t, task.StatusDone, final.Status, "task did not complete Done (result: %s)", final.Result)

	correctFile := filepath.Join(workspacesDir, "ws-zzz", "work", "proof.txt")
	got, readErr := os.ReadFile(correctFile)
	require.NoError(t, readErr,
		"write_file must have landed in the TASK's own workspace (ws-zzz)'s work/ directory (%s) — "+
			"got a native task run that did not thread WorkspaceID through processOptions", correctFile)
	assert.Equal(t, "hello-from-ws-zzz", string(got))

	wrongFile := filepath.Join(workspacesDir, "ws-aaa", "work", "proof.txt")
	_, wrongStatErr := os.Stat(wrongFile)
	assert.True(t, os.IsNotExist(wrongStatErr),
		"write_file must NOT land in ws-aaa (the alphabetically-first, WRONG workspace) — got %s", wrongFile)
}
