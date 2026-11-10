// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

func wu3devWriteWorkspace(t *testing.T, home, id string) {
	t.Helper()
	data, err := json.Marshal(workspacepkg.Workspace{ID: id, Name: id, Status: "active"})
	require.NoError(t, err)
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600))
	require.NoError(t, workspacepkg.SaveDelegation(home, id, nil))
}

func wu3devCreateArgs(name string) map[string]any {
	return map[string]any{"name": name, "description": "d", "soul": "s", "model": "m"}
}

func wu3devBody(t *testing.T, res *tools.ToolResult) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &m), res.ForLLM)
	return m
}

// Founder decision 2026-10-14: a new agent's id is a generated UUID and the
// name is kept verbatim; two creates with one name give two agents.
func TestWU3Dev_CreateAgent_UUIDIdsAndVerbatimNames(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	ctx := tools.WithAgentID(context.Background(), "omnipus")
	var ids []string
	for i := 0; i < 2; i++ {
		res := systools.NewAgentCreateTool(deps).Execute(ctx, wu3devCreateArgs("Sales Lead  (EU)"))
		require.False(t, res.IsError, res.ForLLM)
		m := wu3devBody(t, res)
		id, _ := m["id"].(string)
		_, err := uuid.Parse(id)
		require.NoError(t, err, "id must be a UUID, got %q", id)
		require.Equal(t, "Sales Lead  (EU)", m["name"])
		a, err := agentstore.New(home).Get(id)
		require.NoError(t, err)
		require.Equal(t, "Sales Lead  (EU)", a.Name)
		ids = append(ids, id)
	}
	require.NotEqual(t, ids[0], ids[1], "the same name must give two distinct agents")
}

// FR-040, FR-041, FR-043: workspace_id picks the team the new agent joins,
// is validated before any write, and only Omnipus may name a foreign one.
func TestWU3Dev_CreateAgent_WorkspaceIDTarget(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	wu3devWriteWorkspace(t, home, "sales")
	wu3devWriteWorkspace(t, home, "default-ws")
	omnipus := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "omnipus"), "default-ws")

	res := systools.NewAgentCreateTool(deps).Execute(omnipus, map[string]any{
		"name": "Rep", "description": "d", "soul": "s", "model": "m", "workspace_id": "sales"})
	require.False(t, res.IsError, res.ForLLM)
	m := wu3devBody(t, res)
	id, ok := m["id"].(string)
	require.True(t, ok, res.ForLLM)
	require.Equal(t, "joined_workspace", m["status"])
	require.Equal(t, "sales", m["workspace_id"])
	sales, err := workspacepkg.ReadState(home, "sales")
	require.NoError(t, err)
	require.Contains(t, sales.Workspace.CoreTeam, id, "the agent joins the target team")
	other, err := workspacepkg.ReadState(home, "default-ws")
	require.NoError(t, err)
	require.NotContains(t, other.Workspace.CoreTeam, id, "the caller's workspace is not touched")

	// Unknown target: nothing saved.
	before, _, _ := agentstore.New(home).List()
	res = systools.NewAgentCreateTool(deps).Execute(omnipus, map[string]any{
		"name": "Ghost", "description": "d", "soul": "s", "model": "m", "workspace_id": "no-such-ws"})
	require.True(t, res.IsError)
	m = wu3devBody(t, res)
	require.Equal(t, "failed: nothing saved", m["outcome"])
	after, _, _ := agentstore.New(home).List()
	require.Len(t, after, len(before), "an unknown target must save nothing")

	// A non-Omnipus caller cannot name another workspace.
	jim := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "jim"), "default-ws")
	res = systools.NewAgentCreateTool(deps).Execute(jim, map[string]any{
		"name": "Sneaky", "description": "d", "soul": "s", "model": "m", "workspace_id": "sales"})
	require.True(t, res.IsError, res.ForLLM)
	errBlock, ok := wu3devBody(t, res)["error"].(map[string]any)
	require.True(t, ok, res.ForLLM)
	require.Equal(t, "WORKSPACE_NOT_AUTHORIZED", errBlock["code"])
}
