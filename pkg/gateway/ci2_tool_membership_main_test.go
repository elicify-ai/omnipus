// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// L1 hands-on finding (2026-10-14, stage-1 code): after Omnipus added an
// EXISTING agent to a NEW workspace through its workspace tools, the member's
// main chat in that workspace was "unavailable" and the sidebar did not list
// the member, although Team and the API showed the membership. The sidebar
// lists a member only when member_configs carries main_session_id, which is
// advertised only for a main that already exists (mainSessionAddress resolves,
// never creates). A membership change through create_workspace /
// update_workspace must therefore provision the member's main, as a REST
// workspace save and create_agent already do (FR-002, final-gate H2).

package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func ci2AssertMemberMainListed(t *testing.T, env *u1Env, wsID, agentID string) {
	t.Helper()
	got := env.getWorkspace(t, wsID)
	team, _ := got["core_team"].([]any)
	require.Contains(t, team, agentID, "instrument check: the agent is on the team")
	members, _ := got["member_configs"].(map[string]any)
	member, _ := members[agentID].(map[string]any)
	assert.Equal(t, u1MainID(wsID, agentID), member["main_session_id"],
		"the member must be listed with its main (the sidebar's source); member_configs=%v", members)
	_, err := env.store(t).GetMeta(u1MainID(wsID, agentID))
	assert.NoError(t, err, "the member's main session must exist once the tool returns")
}

func TestCI2_UpdateWorkspaceTool_AddedExistingAgentGetsItsMainListed(t *testing.T) {
	env := u1NewEnv(t, false)
	const ws = "01JGWCI2UPDATEWS000000001"
	env.seedWorkspace(t, ws, false, "mia")
	state, err := workspace.ReadState(env.home, ws)
	require.NoError(t, err)

	res := systools.NewWorkspaceUpdateTool(newAgentCreateToolDeps(env)).Execute(context.Background(), map[string]any{
		"id": ws, "revision": state.Revision, "core_team": []any{"mia", "jim"},
	})
	require.False(t, res.IsError, "update failed: %s", res.ForLLM)

	ci2AssertMemberMainListed(t, env, ws, "jim")
}

func TestCI2_CreateWorkspaceTool_TeamMembersGetTheirMainsListed(t *testing.T) {
	env := u1NewEnv(t, false)

	res := systools.NewWorkspaceCreateTool(newAgentCreateToolDeps(env)).Execute(context.Background(), map[string]any{
		"name": "Research", "core_team": []any{"jim"},
	})
	require.False(t, res.IsError, "create failed: %s", res.ForLLM)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &created), "create result: %s", res.ForLLM)
	require.NotEmpty(t, created.ID, "create result names the workspace: %s", res.ForLLM)

	ci2AssertMemberMainListed(t, env, created.ID, "jim")
}
