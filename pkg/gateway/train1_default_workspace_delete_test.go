package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Retained lower-layer hardening for #1363. The actual registered BODY is
// called directly; fresh Omnipus is still Deny and cannot invoke it in a turn.
// HTTP and body must preserve the same exact default-workspace refusal.
func TestTrain1WorkspaceDeleteTool_DefaultRefusalMatchesHTTP(t *testing.T) {
	const reason = "cannot delete the default workspace"
	provider := testutil.NewScenario()
	api, reg, tab, _, _ := train1WorkspacePolicyHarness(t, provider)
	defaultID, err := workspace.ResolveDefaultID(api.homePath)
	require.NoError(t, err)
	before, err := workspace.ReadState(api.homePath, defaultID)
	require.NoError(t, err)
	require.True(t, before.Workspace.IsDefault)
	files := train1DefaultDeleteSentinels(t, api.homePath, defaultID)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, defaultID), nil)
	api.HandleWorkspaces(response, withAdminRole(request))
	require.Equal(t, http.StatusConflict, response.Code, "%s", response.Body.String())
	var httpRefusal gen.ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &httpRefusal))
	require.Equal(t, reason, httpRefusal.Error)
	train1AssertDefaultDeleteFilesUnchanged(t, files)

	policy, covered := api.agentLoop.ResolveRegisteredToolPolicy("omnipus", "delete_workspace")
	require.True(t, covered)
	require.Equal(t, "deny", policy, "no test-fixture policy loosening")
	instance, found := api.agentLoop.GetRegistry().GetAgent("omnipus")
	require.True(t, found)
	body, registered := instance.Tools.Get("delete_workspace")
	require.True(t, registered)
	result := body.Execute(tools.WithAgentID(context.Background(), "omnipus"), map[string]any{
		"id": defaultID, "revision": before.Revision, "confirm": true,
	})
	require.NotNil(t, result)
	require.True(t, result.IsError, "the actual tool body must refuse the default workspace")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &payload))
	assert.Equal(t, false, payload["success"])
	assert.Equal(t, map[string]any{
		"code": "CONFLICT", "message": httpRefusal.Error, "suggestion": "",
	}, payload["error"])
	train1AssertDefaultDeleteFilesUnchanged(t, files)
	after, err := workspace.ReadState(api.homePath, defaultID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "default record, team, graph and revision stay unchanged")
	assert.Zero(t, provider.CallCount(), "body hardening is not a claim of agent invocation")
	assert.Empty(t, reg.pendingApprovals(), "direct body refusal must not request approval")
	assert.Empty(t, tab.sendCh, "direct body refusal must not emit an approval frame")
}

// Real cascade targets make a late refusal detectable: checking only the
// workspace record would miss a task or working file removed before it.
func train1DefaultDeleteSentinels(t *testing.T, home, id string) map[string][]byte {
	t.Helper()
	taskPath := filepath.Join(home, "tasks", "train1-default-delete-task.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(taskPath), 0o700))
	task, err := json.Marshal(map[string]any{
		"id": "train1-default-delete-task", "name": "Keep the default workspace's task",
		"status": "inbox", "workspace_id": id,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(taskPath, task, 0o600))
	workPath := filepath.Join(workspace.WorkspaceDir(home, id), "work", "keep.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(workPath), 0o700))
	require.NoError(t, os.WriteFile(workPath, []byte("default workspace working-file sentinel\n"), 0o600))
	graphPath, err := workspace.DelegationStorePath(home, id)
	require.NoError(t, err)
	files := make(map[string][]byte)
	for _, path := range []string{filepath.Join(home, "workspaces", id+".json"), graphPath, taskPath, workPath} {
		data, readErr := os.ReadFile(path)
		require.NoError(t, readErr, "preservation instrument must exist before deletion: %s", path)
		files[path] = data
	}
	return files
}

func train1AssertDefaultDeleteFilesUnchanged(t *testing.T, files map[string][]byte) {
	t.Helper()
	for path, before := range files {
		after, err := os.ReadFile(path)
		if assert.NoError(t, err, "refusal must preserve %s", path) {
			assert.Equal(t, before, after, "refusal must preserve the exact bytes of %s", path)
		}
	}
}
