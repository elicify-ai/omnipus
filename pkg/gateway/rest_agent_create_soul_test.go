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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
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

	// AgentCreateRequestSubagent has no executor property at all (native is
	// always server-derived for a Subagent with no executor block) — sending
	// one is now rejected 400 by createAgent's strict decode.
	body := `{"name":"Soulful Worker","type":"Subagent","description":"persists soul regression","soul":"worker-soul-X"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)
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

	body := `{"name":"Soulful Custom","type":"Main","soul":"custom-soul-X"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeMain, created.Type)
	assert.Equal(t, "custom-soul-X", created.Soul, "response soul must echo the persisted value")

	got := readSoulMDForAgent(t, api, created.Id)
	assert.Equal(t, "custom-soul-X", got, "SOUL.md must be persisted at create time")
}

// TestCreateAgent_Worker_RequiresExecutor verifies that a Subagent create
// without an executor is accepted and derives kind=native server-side.
func TestCreateAgent_Worker_DerivesNativeExecutor(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker No Exec","type":"Subagent","description":"missing executor regression","soul":"worker-soul-noexec"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)
	require.NotNil(t, created.Executor, "derived native executor must be echoed")
	require.NotNil(t, created.Executor.Kind, "derived executor.kind must be present")
	assert.Equal(t, gen.AgentExecutorKindNative, *created.Executor.Kind)
}

// TestCreateAgent_Worker_ExecutorFieldRejected proves the unconditional
// strict-decode enforcement: AgentCreateRequestSubagent has no `executor`
// property at all — a Subagent create can no longer request remote-a2a (or
// any other) executor kind directly at create time (the field matrix marks
// Subagent's executor row "— (native)"). With a DEFAULT config
// (ValidateInbound off — buildExecutorTestAPI does not opt in), an
// `executor` key present in the JSON body is now rejected 400 by
// createAgent's strict decode, not silently dropped. (remote-a2a is still
// reachable via PUT — AgentUpdateRequest remains one flat type shared by
// every agent type; see TestUpdateAgent_WorkerAllowsRemoteA2AExecutor in
// rest_agent_executor_test.go.)
func TestCreateAgent_Worker_ExecutorFieldRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker Remote A2A","type":"Subagent","description":"remote-a2a regression","executor":{"kind":"remote-a2a"},"soul":"remote-a2a-soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "executor")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent")
}
