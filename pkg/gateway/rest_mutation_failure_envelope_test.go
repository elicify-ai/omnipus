package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// The failure contract permits absent revision, but never null changed_fields.
func TestConfigurationMutationFailureContract(t *testing.T) {
	revision := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name    string
		result  agentstore.MutationResult
		changed []any
	}{
		{"create_entity_stage", agentstore.MutationResult{PersistenceStatus: agentstore.PersistenceNone, ErrorStage: "stage_entity"}, []any{}},
		{"create_soul_stage", agentstore.MutationResult{PersistenceStatus: agentstore.PersistenceNone, ErrorStage: "stage_soul"}, []any{}},
		{"update_before_write", agentstore.MutationResult{PersistenceStatus: agentstore.PersistenceNone, Revision: revision, ErrorStage: "stage_entity"}, []any{}},
		{"partial_soul", agentstore.MutationResult{PersistenceStatus: agentstore.PersistencePartial, Revision: revision, ChangedFields: []string{"entity"}, ErrorStage: "replace_soul"}, []any{"entity"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeConfigurationMutationFailure(w, tc.result)
			require.Equal(t, http.StatusInternalServerError, w.Code)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
			expected := map[string]any{
				"persistence_status": string(tc.result.PersistenceStatus),
				"activation_status":  "not_attempted",
				"changed_fields":     tc.changed,
				"error_stage":        tc.result.ErrorStage,
				"message":            "configuration storage failed before completion; read the resource again before retrying",
			}
			if tc.result.Revision != "" {
				expected["revision"] = revision
			}
			require.Equal(t, expected, payload, "wire contract: omitted unknown revision and non-null array")
		})
	}
}

func TestWorkspaceGraphOnlyFailureHasEmptyChangedFieldsArray(t *testing.T) {
	api, id := buildWorkspaceDelegationTestAPI(t)
	unlock := workspace.LockID(id)
	seedErr := workspace.SaveDelegation(api.homePath, id, []workspace.DelegationEdge{
		{FromAgent: "jim", ToAgent: "ava", Modes: []workspace.DelegationMode{workspace.ModeDirect}},
	})
	unlock()
	require.NoError(t, seedErr)
	before, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)
	original := workspaceSaveDelegationFn
	workspaceSaveDelegationFn = func(string, string, []workspace.DelegationEdge) error {
		return errors.New("injected graph write failure")
	}
	t.Cleanup(func() { workspaceSaveDelegationFn = original })
	w := putWorkspaceGraph(t, api, id, `,"delegation":[]`)
	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, []any{}, payload["changed_fields"], "failed graph-only publication has no saved requested fields; [] is required")
	require.Equal(t, "partial", payload["persistence_status"])
	require.Equal(t, "not_attempted", payload["activation_status"])
	require.Equal(t, "delegation", payload["error_stage"])
	require.Equal(t, before.Revision, payload["revision"], "failed graph publication retains the reviewed live state")
	after, err := workspace.ReadState(api.homePath, id)
	require.NoError(t, err)
	require.Equal(t, before.Delegation, after.Delegation)
	require.Equal(t, before.Revision, after.Revision)
}
