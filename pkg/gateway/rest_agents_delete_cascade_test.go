// rest_agents_delete_cascade_test.go — FR-037/C-DELETE order proof for the REST
// DELETE /api/v1/agents/{id} handler (E-DELETE).

package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteAgent_CleanupFailure_KeepsRecordVisibleAndReturnsPartial proves the
// FR-037/C-DELETE record-last order on the REST path — the gap the delete_agent
// tool closed but deleteAgent still had reversed.
//
// When an owned-data cleanup step fails, the agent's entity record is
// deliberately NOT removed (record-last): it stays visible and the response is
// the honest partly-deleted failure state (persistence_status: partial) that the
// SPA's DeleteAgentControl keeps the row for and retries. The cleanup-first half
// is proven too: a sole-owned session is deleted by the earlier session step
// BEFORE the later workspace step fails, so owned data is already partly gone
// while the record survives — exactly the "partial means the record still
// exists" contract the FE was written against.
func TestDeleteAgent_CleanupFailure_KeepsRecordVisibleAndReturnsPartial(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	// Wire the real reload path (mirrors gateway.go's boot-time
	// SetReloadFunc(reloadTrigger)) so the create step below repopulates
	// cfg.Agents.List — same pattern TestHandleAgentsDelete_OK uses.
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})

	// Create a custom agent to delete.
	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name": "Partly Deletable", "type": "Main", "model": "claude-sonnet-4-6", "soul": "I am partly deletable."}`,
		),
	)
	createR.Header.Set("Content-Type", "application/json")
	createR.URL.Path = "/api/v1/agents"
	api.HandleAgents(createW, createR)
	require.Equal(t, http.StatusCreated, createW.Code, "create step failed: body=%s", createW.Body.String())

	var created gen.Agent
	require.NoError(t, json.Unmarshal(createW.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)
	require.NotEmpty(t, created.Revision)

	// A sole-owned session: the session-cleanup step (which runs FIRST) must
	// delete it even though the later workspace step fails.
	sessStore, err := session.NewUnifiedStore(filepath.Join(api.homePath, "sessions"))
	require.NoError(t, err)
	meta, err := sessStore.NewSession(session.SessionTypeChat, "webchat", created.Id)
	require.NoError(t, err)
	require.NoError(t, sessStore.Close())

	// Force a cleanup failure in the workspace step: a corrupt workspace record
	// makes cascadeCleanAgentWorkspaceReferences report a warning, so the
	// record delete (record-last) is skipped and the record stays on disk.
	wsDir := filepath.Join(api.homePath, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "broken-ws.json"), []byte("{not valid json"), 0o600))

	delW := httptest.NewRecorder()
	delR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id+"?revision="+created.Revision, nil)
	delR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(delW, delR)

	require.Equal(t, http.StatusInternalServerError, delW.Code, "delete body=%s", delW.Body.String())
	var failure gen.ConfigurationMutationFailureState
	require.NoError(t, json.Unmarshal(delW.Body.Bytes(), &failure))
	assert.Equal(t, gen.ConfigurationMutationFailureStatePersistenceStatusPartial, failure.PersistenceStatus,
		"a cleanup failure must report persistence_status=partial so the SPA keeps the row and retries")
	assert.Equal(t, gen.ConfigurationMutationFailureStateActivationStatusNotAttempted, failure.ActivationStatus)

	// Record-last: the record must remain visible — a GET on the same id still
	// resolves, so the same Delete retries with its current revision.
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	getR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(getW, getR)
	assert.Equal(t, http.StatusOK, getW.Code,
		"record-last: a failed cleanup must leave the agent record visible (body=%s)", getW.Body.String())

	// Cleanup-first: the sole-owned session was already deleted by the earlier
	// session step, before the failed workspace step.
	if _, err := os.Stat(filepath.Join(api.homePath, "sessions", meta.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session %s must be cleaned before the record is (stat err=%v)", meta.ID, err)
	}
}
