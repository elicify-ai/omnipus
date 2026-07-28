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

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/clidetect"
)

// --- /api/v1/system/cli-detect ---

// TestHandleSystemCliDetect_OK verifies the handler returns 200 with the new
// per-CLI {installed, path, source} shape. The detection function is replaced
// with a deterministic stub (claude present via $PATH, the others missing) so
// the test does not depend on the developer's local host layout.
//
// BDD: Given detection returns {claude: installed on PATH, codex: missing,
// opencode: missing}, When GET /api/v1/system/cli-detect is called, Then the
// response is 200 with claude.installed=true (path + source="path") and the
// others installed=false with null path/source.
// Traces to: external-executor-cli-path-detection-spec.md FR-001/FR-011.
func TestHandleSystemCliDetect_OK(t *testing.T) {
	orig := cliDetectAll
	t.Cleanup(func() { cliDetectAll = orig })

	cliDetectAll = func() map[string]clidetect.Result {
		return map[string]clidetect.Result{
			"claude-code": {Installed: true, Path: "/usr/local/bin/claude", Source: clidetect.SourcePath},
			"codex":       {},
			"opencode":    {},
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
	assert.True(t, resp.Claude.Installed, "claude should be reported installed")
	require.NotNil(t, resp.Claude.Path)
	assert.Equal(t, "/usr/local/bin/claude", *resp.Claude.Path)
	require.NotNil(t, resp.Claude.Source)
	assert.Equal(t, gen.CliDetectClaudeSource("path"), *resp.Claude.Source)

	assert.False(t, resp.Codex.Installed, "codex should be reported missing")
	assert.Nil(t, resp.Codex.Path, "missing CLI must have null path")
	assert.Nil(t, resp.Codex.Source, "missing CLI must have null source")
	assert.False(t, resp.Opencode.Installed, "opencode should be reported missing")
	assert.Nil(t, resp.Opencode.Path)
	assert.Nil(t, resp.Opencode.Source)
}

// TestHandleSystemCliDetect_AllPresent verifies all three CLIs are reported
// installed, exercising both source variants ("path" and "well-known").
func TestHandleSystemCliDetect_AllPresent(t *testing.T) {
	orig := cliDetectAll
	t.Cleanup(func() { cliDetectAll = orig })

	cliDetectAll = func() map[string]clidetect.Result {
		return map[string]clidetect.Result{
			"claude-code": {Installed: true, Path: "/usr/local/bin/claude", Source: clidetect.SourcePath},
			"codex":       {Installed: true, Path: "/home/dev/.local/bin/codex", Source: clidetect.SourceWellKnown},
			"opencode":    {Installed: true, Path: "/opt/homebrew/bin/opencode", Source: clidetect.SourceWellKnown},
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
	assert.True(t, resp.Claude.Installed)
	assert.True(t, resp.Codex.Installed)
	assert.True(t, resp.Opencode.Installed)
	require.NotNil(t, resp.Codex.Source)
	assert.Equal(t, gen.CliDetectCodexSource("well-known"), *resp.Codex.Source)
	require.NotNil(t, resp.Opencode.Path)
	assert.Equal(t, "/opt/homebrew/bin/opencode", *resp.Opencode.Path)
}

// TestHandleSystemCliDetect_AllMissing verifies the SPA grey-out state: every
// CLI installed=false with null path/source.
func TestHandleSystemCliDetect_AllMissing(t *testing.T) {
	orig := cliDetectAll
	t.Cleanup(func() { cliDetectAll = orig })

	cliDetectAll = func() map[string]clidetect.Result {
		return map[string]clidetect.Result{
			"claude-code": {},
			"codex":       {},
			"opencode":    {},
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
	assert.False(t, resp.Claude.Installed)
	assert.Nil(t, resp.Claude.Path)
	assert.False(t, resp.Codex.Installed)
	assert.False(t, resp.Opencode.Installed)
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
	// ADR-054: deleteAgent removes the agent's entity record directly via
	// agentstore (entities/agents/<id>.json), bypassing config.json entirely,
	// then calls a.triggerReloadAndWait to refresh the in-memory
	// cfg.Agents.List (populateAgentsListFromStore) so the deletion is
	// reflected before the handler returns. newTestRestAPIWithHome's bare
	// AgentLoop never wires SetReloadFunc (mirrors production: AgentLoop.Run()
	// is never started in these unit tests), so without this,
	// triggerReloadAndWait's "reload not configured" branch treats the reload
	// as an intentional no-op (see rest_auth.go) and cfg.Agents.List is never
	// refreshed — the deleted agent would keep appearing to GET requests even
	// though its entity record is genuinely gone from disk. Wire the real
	// reload path (same as gateway.go's boot-time SetReloadFunc(reloadTrigger))
	// so this test exercises the actual delete-then-404 contract, matching
	// the established pattern already used elsewhere in this package (e.g.
	// rest_test.go, rest_auth_test.go) for tests that care about reload
	// semantics rather than wiring a no-op.
	api.agentLoop.SetReloadFunc(func() error {
		// Mirror gateway.go's real reloadTrigger: clear the pending flag once
		// this reload completes (defer runs even on error) so
		// triggerReloadAndWait's poll loop returns immediately instead of
		// spinning for its full 5-second deadline waiting for a flag that
		// TriggerReload itself only clears on the ERROR path.
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})

	// Create a custom agent first so we have something to delete. model + soul
	// are required on POST (mirrors the create-agent contract: soul is
	// MANDATORY EVERYWHERE per agent-form-requirements.md §4.7).
	createW := httptest.NewRecorder()
	createR := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name": "Deletable", "type": "Main", "model": "claude-sonnet-4-6", "soul": "I am deletable."}`,
		),
	)
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
