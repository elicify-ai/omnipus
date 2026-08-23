// Tests for GET and PUT /api/v1/workspaces/{id}/instructions.
//
// Covered cases:
//  1. GET on a workspace that has no AGENT.md → 200, empty string
//  2. PUT content, then GET → 200 round-trip, file persisted
//  3. PUT empty string → 200, file cleared; subsequent GET returns ""
//  4. GET on unknown workspace → 404
//  5. PUT on unknown workspace → 404
//  6. Oversized PUT body → 400
//  7. Method not allowed (POST) → 405

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// buildWorkspaceInstructionsTestAPI creates a minimal restAPI and a pre-seeded
// workspace, returning the api and the workspace id. Mirrors the pattern used by
// buildWorkspaceDelegationTestAPI.
func buildWorkspaceInstructionsTestAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore},
			},
		},
	}

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}

	// Seed a workspace metadata file so workspace.Exists returns true.
	ws := storedWorkspace{
		ID:        "01J8WSINSTRUCTIONSTEST0001",
		Name:      "Instructions WS",
		Status:    string(gen.WorkspaceStatusActive),
		CoreTeam:  []string{"mia"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, writeWorkspaceFile(tmpDir, ws))
	return api, ws.ID
}

// getInstructions sends GET /api/v1/workspaces/{id}/instructions.
func getInstructions(t *testing.T, api *restAPI, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+id+"/instructions", nil)
	api.HandleWorkspaces(w, r)
	return w
}

// putInstructions sends PUT /api/v1/workspaces/{id}/instructions with the given body.
func putInstructions(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/workspaces/"+id+"/instructions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(w, r)
	return w
}

// decodeInstructions unmarshals the response body into WorkspaceInstructionsResponse.
func decodeInstructions(t *testing.T, body []byte) gen.WorkspaceInstructionsResponse {
	t.Helper()
	var resp gen.WorkspaceInstructionsResponse
	require.NoError(t, json.Unmarshal(body, &resp), "body: %s", string(body))
	return resp
}

// TestWorkspaceInstructions_GetEmpty verifies that a GET on a workspace with no
// AGENT.md returns 200 and an empty content string (absent file is valid state).
func TestWorkspaceInstructions_GetEmpty(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	w := getInstructions(t, api, id)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeInstructions(t, w.Body.Bytes())
	assert.Equal(t, "", resp.Content, "absent AGENT.md must yield empty string")
}

// TestWorkspaceInstructions_PutThenGet verifies the round-trip: PUT stores the
// content and GET returns it. Also confirms the file exists on disk.
func TestWorkspaceInstructions_PutThenGet(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	instructions := "# Project Instructions\n\nBe concise and helpful.\n"
	body := `{"content":` + mustJSON(t, instructions) + `}`

	// PUT
	pw := putInstructions(t, api, id, body)
	require.Equal(t, http.StatusOK, pw.Code, "body: %s", pw.Body.String())
	putResp := decodeInstructions(t, pw.Body.Bytes())
	assert.Equal(t, instructions, putResp.Content)

	// GET round-trip
	gw := getInstructions(t, api, id)
	require.Equal(t, http.StatusOK, gw.Code, "body: %s", gw.Body.String())
	getResp := decodeInstructions(t, gw.Body.Bytes())
	assert.Equal(t, instructions, getResp.Content)

	// File must exist on disk.
	agentMDPath := filepath.Join(api.homePath, "workspaces", id, "AGENT.md")
	data, err := os.ReadFile(agentMDPath)
	require.NoError(t, err, "AGENT.md must be written to disk")
	assert.Equal(t, instructions, string(data))
}

// TestWorkspaceInstructions_PutEmptyClears verifies that a PUT with an empty
// string clears the AGENT.md, and a subsequent GET returns the empty string.
func TestWorkspaceInstructions_PutEmptyClears(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)

	// First write some content.
	require.Equal(t, http.StatusOK,
		putInstructions(t, api, id, `{"content":"some instructions"}`).Code)

	// Confirm the file exists.
	agentMDPath := filepath.Join(api.homePath, "workspaces", id, "AGENT.md")
	_, err := os.Stat(agentMDPath)
	require.NoError(t, err, "AGENT.md must exist after initial write")

	// Now clear with an empty string.
	cw := putInstructions(t, api, id, `{"content":""}`)
	require.Equal(t, http.StatusOK, cw.Code, "body: %s", cw.Body.String())
	clearResp := decodeInstructions(t, cw.Body.Bytes())
	assert.Equal(t, "", clearResp.Content)

	// File must be removed.
	_, statErr := os.Stat(agentMDPath)
	assert.True(t, os.IsNotExist(statErr), "AGENT.md must be removed after clearing")

	// GET must return empty string.
	gw := getInstructions(t, api, id)
	require.Equal(t, http.StatusOK, gw.Code)
	assert.Equal(t, "", decodeInstructions(t, gw.Body.Bytes()).Content)
}

// TestWorkspaceInstructions_GetUnknown verifies that a GET on a non-existent
// workspace returns 404.
func TestWorkspaceInstructions_GetUnknown(t *testing.T) {
	api, _ := buildWorkspaceInstructionsTestAPI(t)
	w := getInstructions(t, api, "01J8WSNONEXISTENT000000001")
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// TestWorkspaceInstructions_PutUnknown verifies that a PUT on a non-existent
// workspace returns 404.
func TestWorkspaceInstructions_PutUnknown(t *testing.T) {
	api, _ := buildWorkspaceInstructionsTestAPI(t)
	w := putInstructions(t, api, "01J8WSNONEXISTENT000000001", `{"content":"hi"}`)
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// TestWorkspaceInstructions_OversizedBody verifies that a PUT whose content
// exceeds 256 KB is rejected with 400.
func TestWorkspaceInstructions_OversizedBody(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	// 263 KB of 'A' — well above the 262144-byte limit.
	oversized := strings.Repeat("A", 263*1024)
	body := `{"content":` + mustJSON(t, oversized) + `}`
	w := putInstructions(t, api, id, body)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestWorkspaceInstructions_MethodNotAllowed verifies that unsupported methods
// (e.g. POST) return 405.
func TestWorkspaceInstructions_MethodNotAllowed(t *testing.T) {
	api, id := buildWorkspaceInstructionsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+id+"/instructions",
		strings.NewReader(`{"content":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code, "body: %s", w.Body.String())
}

// mustJSON marshals v as a compact JSON string. Panics on error (test helper only).
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
