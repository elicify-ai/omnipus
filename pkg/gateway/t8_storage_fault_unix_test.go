//go:build !windows

package gateway

// r17 15.7 / D-10 row 10: an actual filesystem staging failure on a valid
// person update is a storage 500, not a field-validation 400. The negative
// control proves this process cannot write, while existing entity reads work.
import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8AgentUpdate_RealStorageWriteFailureStays500(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := agentstore.New(api.homePath)
	before, err := store.ReadState("test-agent")
	require.NoError(t, err)
	entityPath := filepath.Join(api.homePath, "entities", "agents", "test-agent.json")
	original, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	dir := filepath.Dir(entityPath)
	req := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(`{"description":"valid changed description"}`))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	require.NoError(t, os.Chmod(dir, 0o500))
	defer func() { require.NoError(t, os.Chmod(dir, 0o700)) }()
	probe, writeErr := os.CreateTemp(dir, ".t8-write-fault-probe-*")
	if writeErr == nil {
		require.NoError(t, probe.Close())
		require.NoError(t, os.Remove(probe.Name()))
		t.Fatal("BLOCKED: process bypasses directory write permissions; genuine staging fault unavailable to this fixture — required by r17 15.7")
	}
	require.ErrorIs(t, writeErr, os.ErrPermission, "instrument: the fault is an actual denied write")
	readable, err := store.ReadState("test-agent")
	require.NoError(t, err, "instrument: the fault does not prevent the preceding entity/revision read")
	require.Equal(t, before, readable)
	w := httptest.NewRecorder()
	api.updateAgent(w, req, "test-agent")
	assert.Equal(t, http.StatusInternalServerError, w.Code, "15.7 actual write failure stays a 500: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "configuration storage failed", "r17 §1c keeps the storage-failure surface")
	afterBytes, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	assert.Equal(t, original, afterBytes, "failed stage publishes no entity change")
	after, err := store.ReadState("test-agent")
	require.NoError(t, err)
	assert.Equal(t, before.Revision, after.Revision)
}
