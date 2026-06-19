//go:build !cgo

// Tests for GET /api/v1/system/cli-detect and DELETE /api/v1/agents/{id}.
// Both endpoints are part of the Wave-6 closeout:
//   - GET /system/cli-detect probes whether the three external-CLI runner
//     binaries (claude / codex / opencode) are on PATH. Pure-Go probe
//     (no shell-out).
//   - DELETE /agents/{id} is the destructive handler behind the
//     AgentProfile slide-over's Delete button. Hard requirement: locked
//     (core / system) agents MUST be rejected with 403 + code `agent_locked`.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// --- /api/v1/system/cli-detect ---

// TestHandleSystemCliDetect_OK verifies the handler returns 200 with the
// three boolean fields. The probe function is replaced with a deterministic
// stub that reports `claude` present and the others missing, so the test
// does not depend on the developer's local PATH.
//
// BDD: Given the gateway process PATH probe returns {claude: present, codex:
// missing, opencode: missing}, When GET /api/v1/system/cli-detect is called,
// Then the response has status 200 with {hasClaude: true, hasCodex: false,
// hasOpencode: false}.
// Traces to: agent-form-requirements.md §5.1 — "the 3 '+ Add (External)'
// sub-pickers are disabled when their CLI is not installed".
func TestHandleSystemCliDetect_OK(t *testing.T) {
	orig := cliProbeLookPath
	t.Cleanup(func() { cliProbeLookPath = orig })

	cliProbeLookPath = func(binary string) (string, error) {
		switch binary {
		case "claude":
			return "/usr/local/bin/claude", nil
		default:
			return "", &notFoundError{binary: binary}
		}
	}

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.CliDetect
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.HasClaude, "claude should be reported present")
	assert.False(t, resp.HasCodex, "codex should be reported missing")
	assert.False(t, resp.HasOpencode, "opencode should be reported missing")
}

// TestHandleSystemCliDetect_AllPresent verifies the handler returns 200 with
// all three booleans true when every probe succeeds.
//
// BDD: Given all three CLIs are installed, When GET /api/v1/system/cli-detect
// is called, Then all three booleans are true.
func TestHandleSystemCliDetect_AllPresent(t *testing.T) {
	orig := cliProbeLookPath
	t.Cleanup(func() { cliProbeLookPath = orig })

	cliProbeLookPath = func(binary string) (string, error) {
		return "/usr/local/bin/" + binary, nil
	}

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.CliDetect
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.HasClaude)
	assert.True(t, resp.HasCodex)
	assert.True(t, resp.HasOpencode)
}

// TestHandleSystemCliDetect_AllMissing verifies the handler returns 200 with
// all three booleans false when every probe fails.
//
// BDD: Given no CLIs are installed, When GET /api/v1/system/cli-detect is
// called, Then all three booleans are false (the SPA grey-out state).
func TestHandleSystemCliDetect_AllMissing(t *testing.T) {
	orig := cliProbeLookPath
	t.Cleanup(func() { cliProbeLookPath = orig })

	cliProbeLookPath = func(binary string) (string, error) {
		return "", &notFoundError{binary: binary}
	}

	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.CliDetect
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.HasClaude)
	assert.False(t, resp.HasCodex)
	assert.False(t, resp.HasOpencode)
}

// TestHandleSystemCliDetect_MethodNotAllowed verifies the handler rejects
// non-GET methods with 405. The probe function is irrelevant — the method
// guard short-circuits before the probe runs.
func TestHandleSystemCliDetect_MethodNotAllowed(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/system/cli-detect", nil)
	api.HandleSystemCliDetect(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// --- DELETE /api/v1/agents/{id} ---

// TestHandleAgentsDelete_OK verifies DELETE on a custom (non-locked) agent
// returns 204 No Content and removes the agent from the config.
//
// BDD: Given a custom agent exists in config, When DELETE /api/v1/agents/{id}
// is called, Then the response is 204 No Content and the agent no longer
// appears in the list.
// Traces to: agent-form-requirements.md §6.1 — Edit slide-over Delete flow.
func TestHandleAgentsDelete_OK(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Create a custom agent first so we have something to delete. model + soul
	// are required on POST (mirrors the create-agent contract: soul is
	// MANDATORY EVERYWHERE per agent-form-requirements.md §4.7).
	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name": "Deletable", "model": "claude-sonnet-4-6", "soul": "I am deletable."}`))
	createR.Header.Set("Content-Type", "application/json")
	createR.URL.Path = "/api/v1/agents"
	api.HandleAgents(createW, createR)
	require.Equal(t, http.StatusCreated, createW.Code,
		"create step failed: body=%s", createW.Body.String())

	var created gen.Agent
	require.NoError(t, json.Unmarshal(createW.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)

	// Delete the agent.
	delW := httptest.NewRecorder()
	delR := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/"+created.Id, nil)
	delR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(delW, delR)
	assert.Equal(t, http.StatusNoContent, delW.Code,
		"DELETE on a custom agent must return 204 No Content")

	// GET on the same id should now return 404.
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	getR.URL.Path = "/api/v1/agents/" + created.Id
	api.HandleAgents(getW, getR)
	assert.Equal(t, http.StatusNotFound, getW.Code,
		"GET on the deleted agent must return 404")
}

// TestHandleAgentsDelete_LockedForbidden verifies DELETE on a locked core
// agent returns 403 with the `agent_locked` error code.
//
// BDD: Given a locked (core/system) agent exists, When DELETE /api/v1/agents/
// {id} is called, Then the response is 403 with body { error, code: "agent_locked" }.
// Traces to: agent-form-requirements.md §6.1 — "locked core agents cannot be
// deleted". Hard requirement: built-ins MUST be protected.
func TestHandleAgentsDelete_LockedForbidden(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	// `seedTestAgents` (called by newTestRestAPI) seeds the system agent +
	// core agents. `mia` is locked (built-in roster) — use it.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/mia", nil)
	r.URL.Path = "/api/v1/agents/mia"
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusForbidden, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "agent_locked", resp["code"],
		"locked-agent 403 must surface code=agent_locked so the SPA can distinguish it from generic forbidden")
	assert.Contains(t, resp["error"], "locked")
}

// TestHandleAgentsDelete_NotFound verifies DELETE on a non-existent agent
// returns 404.
//
// BDD: Given no agent exists with the given id, When DELETE /api/v1/agents/
// {id} is called, Then the response is 404.
func TestHandleAgentsDelete_NotFound(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/does-not-exist", nil)
	r.URL.Path = "/api/v1/agents/does-not-exist"
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// notFoundError is a minimal os/exec.ErrNotStandin for tests — keeps the
// stub independent of the real os/exec error type so the test does not
// depend on the exact error format returned by the runtime.
type notFoundError struct {
	binary string
}

func (e *notFoundError) Error() string {
	return "executable file not found in $PATH: " + e.binary
}
