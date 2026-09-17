package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func putWorkspaceGraph(t *testing.T, api *restAPI, id, fields string) *httptest.ResponseRecorder {
	t.Helper()
	state, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)
	body := `{"revision":"` + state.Revision + `"` + fields + `}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleWorkspaces(w, r)
	return w
}

func workspaceDeleteURL(t *testing.T, api *restAPI, id string) string {
	t.Helper()
	revision := strings.Repeat("0", 64)
	if state, err := workspace.ReadState(api.homePath, id); err == nil {
		revision = state.Revision
	}
	return "/api/v1/workspaces/" + id + "?revision=" + revision
}

func withWorkspaceRevisionJSON(t *testing.T, api *restAPI, id, body string) string {
	t.Helper()
	state, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &value))
	value["revision"] = state.Revision
	out, err := json.Marshal(value)
	require.NoError(t, err)
	return string(out)
}

func TestWorkspacePutDelegationOmissionPreservesPrunesAndSeedsNewMembers(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	ws, err := readWorkspaceFile(api.homePath, id)
	require.NoError(t, err)
	ws.CoreTeam = []string{"jim", "ava"}
	require.NoError(t, writeWorkspaceFile(api.homePath, ws))
	unlock := workspace.LockID(id)
	require.NoError(t, workspace.SaveDelegation(api.homePath, id, []workspace.DelegationEdge{
		{FromAgent: "jim", ToAgent: "jim", Modes: []workspace.DelegationMode{workspace.ModeDirect}},
		{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{workspace.ModeDirect}},
	}))
	unlock()

	w := putWorkspaceGraph(t, api, id, `,"core_team":["jim","planner"]`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	edges, ok := workspace.LoadDelegation(api.homePath, id)
	require.True(t, ok)
	require.Contains(t, edges, workspace.DelegationEdge{FromAgent: "jim", ToAgent: "jim", Modes: []workspace.DelegationMode{workspace.ModeDirect}})
	for _, edge := range edges {
		require.NotEqual(t, "ava", edge.ToAgent)
	}
	require.Contains(t, edges, workspace.DelegationEdge{FromAgent: "jim", ToAgent: "planner", Modes: []workspace.DelegationMode{workspace.ModeTask, workspace.ModeDirect}})
}

func TestWorkspacePutExplicitEmptyDelegationClearsWithoutReseeding(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	unlock := workspace.LockID(id)
	require.NoError(t, workspace.SaveDelegation(api.homePath, id, []workspace.DelegationEdge{{FromAgent: "jim", ToAgent: "ava"}}))
	unlock()
	w := putWorkspaceGraph(t, api, id, `,"delegation":[]`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	edges, ok := workspace.LoadDelegation(api.homePath, id)
	require.True(t, ok)
	require.Empty(t, edges)
}

func TestWorkspacePutNullDelegationRejectedWithoutWrites(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	before, err := readWorkspaceFile(api.homePath, id)
	require.NoError(t, err)
	w := putWorkspaceGraph(t, api, id, `,"name":"must-not-write","delegation":null`)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	after, err := readWorkspaceFile(api.homePath, id)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestWorkspacePutSecondStoreFailureReportsPartialCurrentState(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	unlock := workspace.LockID(id)
	require.NoError(t, workspace.SaveDelegation(api.homePath, id, []workspace.DelegationEdge{{FromAgent: "jim", ToAgent: "ava"}}))
	unlock()
	original := workspaceSaveDelegationFn
	workspaceSaveDelegationFn = func(string, string, []workspace.DelegationEdge) error { return errors.New("injected graph failure") }
	t.Cleanup(func() { workspaceSaveDelegationFn = original })
	w := putWorkspaceGraph(t, api, id, `,"name":"persisted-name","delegation":[]`)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	var state gen.ConfigurationMutationState
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &state))
	require.Equal(t, gen.ConfigurationMutationStatePersistenceStatusPartial, state.PersistenceStatus)
	require.Equal(t, []string{"name"}, state.ChangedFields)
	require.Len(t, state.Revision, 64)
	stored, err := readWorkspaceFile(api.homePath, id)
	require.NoError(t, err)
	require.Equal(t, "persisted-name", stored.Name)
}
