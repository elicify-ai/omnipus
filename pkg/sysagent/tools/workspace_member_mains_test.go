// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// recordingMains stands in for the gateway's main provisioner: it records each
// workspace it is asked to provision and returns err (nil = success).
type recordingMains struct {
	calls []string
	err   error
}

func (r *recordingMains) ensure(workspaceID string) error {
	r.calls = append(r.calls, workspaceID)
	return r.err
}

func decodeToolJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &out), "tool result is JSON: %s", raw)
	return out
}

// Oracle: omnipus-system-agent FR-002 (a member owns its main) and the L1
// hands-on finding of 2026-10-14: a team change through create_workspace /
// update_workspace provisions the members' mains, exactly once per change,
// and a provisioning failure is reported in the result, never silent.
func TestWorkspaceTools_TeamChangeProvisionsMembersMains(t *testing.T) {
	// mia and jim are real agents: WU-3 (#1057 F2) refuses a core_team id
	// that names no agent, so the members must exist for the team to save.
	deps, home := newTestDepsWithHomeAndAgents(t, "mia", "jim")
	mains := &recordingMains{}
	deps.EnsureWorkspaceMains = mains.ensure

	created := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name": "Research", "core_team": []any{"mia"},
	})
	require.False(t, created.IsError, "create: %s", created.ForLLM)
	createdOut := decodeToolJSON(t, created.ForLLM)
	wsID, _ := createdOut["id"].(string)
	require.NotEmpty(t, wsID)
	assert.Equal(t, []string{wsID}, mains.calls, "create with a team provisions that workspace's mains once")
	assert.NotContains(t, createdOut, "main_chat_warning", "no warning when provisioning succeeds")

	empty := systools.NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{"name": "Empty"})
	require.False(t, empty.IsError, "create without a team: %s", empty.ForLLM)
	assert.Equal(t, []string{wsID}, mains.calls, "create without a team provisions nothing")

	state, err := workspace.ReadState(home, wsID)
	require.NoError(t, err)
	renamed := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": wsID, "revision": state.Revision, "name": "Research Lab",
	})
	require.False(t, renamed.IsError, "rename: %s", renamed.ForLLM)
	assert.Equal(t, []string{wsID}, mains.calls, "an update that leaves the team alone provisions nothing")

	mains.err = errors.New("session storage is full")
	state, err = workspace.ReadState(home, wsID)
	require.NoError(t, err)
	grown := systools.NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": wsID, "revision": state.Revision, "core_team": []any{"mia", "jim"},
	})
	require.False(t, grown.IsError, "the team change is saved even when a main cannot be prepared: %s", grown.ForLLM)
	assert.Equal(t, []string{wsID, wsID}, mains.calls, "a team change provisions the workspace's mains")
	grownOut := decodeToolJSON(t, grown.ForLLM)
	warning, _ := grownOut["main_chat_warning"].(string)
	assert.Contains(t, warning, "session storage is full", "the provisioning failure is reported to the agent")
	assert.Equal(t, []any{"mia", "jim"}, grownOut["core_team"], "the saved team is reported")
}
