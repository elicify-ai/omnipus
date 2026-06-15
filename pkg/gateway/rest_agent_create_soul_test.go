//go:build !cgo

// Regression tests for the createAgent soul + worker-must-have-executor fixes.
//
// Background:
//   1. createAgent previously write-dropped req.Soul — the contract accepted
//      it, the FE sent it, but nothing landed on disk. A "draft" agent created
//      without a soul stayed in the draft state forever on the soul-empty path.
//   2. createAgent also did not enforce the FE-only rule that a worker must
//      declare an executor (workers run via delegation, not native).
//
// These tests prove:
//   - Worker create with `soul:"X"` → 201, response soul="X",
//     <workspace>/SOUL.md contains "X".
//   - Custom create with `soul:"X"` → 201, response soul="X",
//     <workspace>/SOUL.md contains "X".
//   - Worker create with no executor → 400.
//   - Worker create with executor omitted in custom creates (regression guard).

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readSoulMDForAgent returns the on-disk SOUL.md contents for the given agentID.
func readSoulMDForAgent(t *testing.T, api *restAPI, agentID string) string {
	t.Helper()
	workspace := filepath.Join(api.homePath, "agents", agentID)
	data, err := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	require.NoError(t, err, "expected SOUL.md at %s/SOUL.md", workspace)
	return string(data)
}

// TestCreateAgent_Worker_PersistsSoul writes a worker with an initial soul and
// asserts both the response and the on-disk SOUL.md carry the value (the
// create-time write was previously dropped on the floor).
func TestCreateAgent_Worker_PersistsSoul(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Soulful Worker","type":"worker","executor":{"kind":"native"},"soul":"worker-soul-X"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, "worker", string(created.Type))
	assert.Equal(t, "worker-soul-X", created.Soul, "response soul must echo the persisted value")

	// On disk — must match the value the caller sent.
	got := readSoulMDForAgent(t, api, created.Id)
	assert.Equal(t, "worker-soul-X", got, "SOUL.md must be persisted at create time")
}

// TestCreateAgent_Custom_PersistsSoul is the same round-trip for a custom
// agent — custom creates may also carry an initial soul (the FE profile
// flow writes it later; an initial soul is permitted).
func TestCreateAgent_Custom_PersistsSoul(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Soulful Custom","type":"custom","soul":"custom-soul-X"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, "custom", string(created.Type))
	assert.Equal(t, "custom-soul-X", created.Soul, "response soul must echo the persisted value")

	got := readSoulMDForAgent(t, api, created.Id)
	assert.Equal(t, "custom-soul-X", got, "SOUL.md must be persisted at create time")
}

// TestCreateAgent_Worker_RequiresExecutor is the write-time mirror of the FE
// guard: a worker with no executor is 400. Workers run via delegation and need
// a target runtime; the previously-write-dropped gap would have left a draft
// the delegation path could not actually run.
func TestCreateAgent_Worker_RequiresExecutor(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker No Exec","type":"worker"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "worker",
		"the rejection must reference the worker tier")
	assert.Contains(t, w.Body.String(), "executor",
		"the rejection must name the missing executor")
}

// TestCreateAgent_Worker_AllowsAnyExecutorKind is the control: the new
// "worker must declare an executor" rule accepts any kind (native,
// external-cli, remote-a2a). Kind validation is the executor block's job;
// this rule is about presence, not type.
func TestCreateAgent_Worker_AllowsAnyExecutorKind(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker Remote A2A","type":"worker","executor":{"kind":"remote-a2a"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, "worker", string(created.Type))
	require.NotNil(t, created.Executor)
	assert.Equal(t, "remote-a2a", string(created.Executor.Kind))
}
