package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceToWireFromUsesSnapshotWhenDiskGraphChanges(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)

	unlock := workspace.LockID(id)
	require.NoError(t, workspace.SaveDelegation(api.homePath, id, []workspace.DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{workspace.ModeDirect}},
	}))
	unlock()

	snapshot, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)
	require.NotEmpty(t, snapshot.Revision)

	unlock = workspace.LockID(id)
	require.NoError(t, workspace.SaveDelegation(api.homePath, id, []workspace.DelegationEdge{
		{FromAgent: "jim", ToAgent: "planner", Modes: []workspace.DelegationMode{workspace.ModeTask}},
	}))
	unlock()

	wire := workspaceToWireFrom(api.homePath, snapshot.Workspace, 0, &snapshot)
	require.Equal(t, snapshot.Revision, wire.Revision)
	require.NotNil(t, wire.Delegation)
	require.Equal(t, "ava", (*wire.Delegation)[0].ToAgent)

	reread := workspaceToWire(api.homePath, snapshot.Workspace, 0)
	require.NotEqual(t, wire.Revision, reread.Revision, "LoadDelegation fallback must see the later disk graph")
	require.NotNil(t, reread.Delegation)
	require.Equal(t, "planner", (*reread.Delegation)[0].ToAgent)
}

func TestHandleWorkspaceGetRevisionMatchesReadStateSnapshot(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	unlock := workspace.LockID(id)
	require.NoError(t, workspace.SaveDelegation(api.homePath, id, []workspace.DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{workspace.ModeDirect}},
	}))
	unlock()

	state, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id, nil)
	api.HandleWorkspaces(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, state.Revision, payload["revision"])
	edges, ok := payload["delegation"].([]any)
	require.True(t, ok)
	require.Len(t, edges, 1)
	first, ok := edges[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ava", first["to_agent"])
}
