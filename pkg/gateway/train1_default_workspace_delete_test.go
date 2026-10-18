package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Founder follow-up 2026-10-18: approving delete_workspace must not bypass
// the HTTP handler's default-workspace refusal. Both paths are real; the
// exact reason is pinned independently, not read from a production helper.
func TestTrain1OmnipusWorkspaceTools_DefaultDeleteRefusedAfterApproval(t *testing.T) {
	const reason = "cannot delete the default workspace"
	provider := testutil.NewScenario()
	api, reg, tab, msgBus, mainID := train1WorkspacePolicyHarness(t, provider)
	defaultID, err := workspace.ResolveDefaultID(api.homePath)
	require.NoError(t, err)
	before, err := workspace.ReadState(api.homePath, defaultID)
	require.NoError(t, err)
	require.True(t, before.Workspace.IsDefault, "the target must be the actual default, not a similarly named workspace")
	files := train1DefaultDeleteSentinels(t, api.homePath, defaultID)

	// Positive refusal control: exercise the existing HTTP handler against
	// this same record and current revision before attempting the tool call.
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, workspaceDeleteURL(t, api, defaultID), nil)
	api.HandleWorkspaces(response, withAdminRole(request))
	require.Equal(t, http.StatusConflict, response.Code, "%s", response.Body.String())
	var httpRefusal gen.ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &httpRefusal))
	require.Equal(t, reason, httpRefusal.Error)
	train1AssertDefaultDeleteFilesUnchanged(t, files)

	args := map[string]any{"id": defaultID, "revision": before.Revision, "confirm": true}
	encoded, err := json.Marshal(args)
	require.NoError(t, err)
	provider.WithToolCall("ToolSearch", `{"names":["delete_workspace"]}`).
		WithToolCall("delete_workspace", string(encoded)).
		WithText("Deletion request handled.")
	ctx := train1StartMainTurn(t, api, msgBus, mainID, "Delete the default workspace")

	var frame gen.ToolApprovalRequiredFrame
	select {
	case raw := <-tab.sendCh:
		require.NoError(t, json.Unmarshal(raw, &frame))
	case reply := <-msgBus.OutboundChan():
		t.Fatalf("default-workspace delete finished without raising its approval: %s", reply.Content)
	case <-ctx.Done():
		t.Fatal("default-workspace delete neither raised an approval nor finished")
	}
	require.Equal(t, string(gen.WsFrameTypeToolApprovalRequired), frame.Type)
	require.Equal(t, "delete_workspace", frame.ToolName)
	require.Equal(t, "omnipus", frame.AgentId)
	require.Equal(t, mainID, frame.SessionId)
	require.Equal(t, args, frame.Args)
	pending := reg.pendingApprovals()
	require.Len(t, pending, 1)
	require.Equal(t, pending[0].ApprovalID, frame.ApprovalId)
	require.Equal(t, 2, provider.CallCount(), "the delete is waiting on a real approval")
	train1AssertDefaultDeleteFilesUnchanged(t, files)

	resolved, gone := reg.resolve(frame.ApprovalId, ApprovalActionApprove, false)
	require.True(t, resolved, "explicitly approve the destructive call; denial would not test the tool's own protection")
	require.False(t, gone)
	select {
	case reply := <-msgBus.OutboundChan():
		require.Equal(t, "Deletion request handled.", reply.Content)
		require.Equal(t, mainID, reply.SessionID)
	case <-ctx.Done():
		t.Fatal("the approved deletion call did not return")
	}
	require.Equal(t, 3, provider.CallCount())
	assert.Empty(t, reg.pendingApprovals())

	var result map[string]any
	results := 0
	for _, message := range provider.LastMessages() {
		if message.Role == "tool" && message.ToolCallID == "delete_workspace-0" {
			results++
			require.NoError(t, json.Unmarshal([]byte(message.Content), &result))
		}
	}
	require.Equal(t, 1, results, "the real tool's result must reach the model")
	assert.Equal(t, false, result["success"], "approval cannot make the default workspace deletable")
	assert.Equal(t, map[string]any{
		"code": "CONFLICT", "message": httpRefusal.Error, "suggestion": "",
	}, result["error"], "the tool must return the HTTP handler's exact refusal reason")
	train1AssertDefaultDeleteFilesUnchanged(t, files)
	after, err := workspace.ReadState(api.homePath, defaultID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "default record, team, graph and revision stay unchanged after approval")
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
