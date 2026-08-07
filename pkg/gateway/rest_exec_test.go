// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleExecAllowlist_GET returns the current (empty) allowlist and
// approval mode from the config. This is the Wave 2 operator-facing read
// path for SEC-05.
func TestHandleExecAllowlist_GET(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/security/exec-allowlist", nil)
	api.HandleExecAllowlist(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	_, hasBinaries := body["allowed_binaries"]
	_, hasApproval := body["approval"]
	assert.True(t, hasBinaries, "response must include 'allowed_binaries' field")
	assert.True(t, hasApproval, "response must include 'approval' field")
}

// TestHandleExecAllowlist_PUT updates the allowlist atomically via
// safeUpdateConfigJSON and persists the change to config.json on disk.
func TestHandleExecAllowlist_PUT(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	payload := `{"allowed_binaries":["git *","npm run *"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/exec-allowlist", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	api.HandleExecAllowlist(w, r)

	require.Equal(t, http.StatusOK, w.Code, "PUT must return 200, got %d: %s", w.Code, w.Body.String())

	// The PUT handler echoes the persisted allowlist directly.
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	allowed, _ := resp["allowed_binaries"].([]any)
	require.Len(t, allowed, 2, "allowlist in response must contain 2 patterns after PUT")
	assert.Equal(t, "git *", allowed[0])
	assert.Equal(t, "npm run *", allowed[1])

	// The handler must surface `restart_required: true` so the UI can tell
	// operators the change is not yet enforced (SEC-12).
	restartRequired, _ := resp["restart_required"].(bool)
	assert.True(t, restartRequired, "PUT response must include restart_required: true")

	// Confirm the file on disk was updated via safeUpdateConfigJSON.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var onDisk map[string]any
	require.NoError(t, json.Unmarshal(raw, &onDisk))
	toolsDisk, _ := onDisk["tools"].(map[string]any)
	execDisk, _ := toolsDisk["exec"].(map[string]any)
	allowDisk, _ := execDisk["allowed_binaries"].([]any)
	require.Len(t, allowDisk, 2, "config.json on disk must be updated")
}

// TestHandleExecAllowlist_PUT_SanitisesInput verifies the server trims
// whitespace, rejects empty patterns, dedupes, and enforces length caps.
func TestHandleExecAllowlist_PUT_SanitisesInput(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantStatus int
	}{
		{
			name:       "rejects empty pattern",
			payload:    `{"allowed_binaries":["git *",""]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects whitespace-only pattern",
			payload:    `{"allowed_binaries":["git *","   "]}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "rejects pattern over length cap",
			payload:    `{"allowed_binaries":["` + strings.Repeat("a", 257) + `"]}`,
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithHome(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/security/exec-allowlist", strings.NewReader(tc.payload))
			r.Header.Set("Content-Type", "application/json")
			api.HandleExecAllowlist(w, r)
			assert.Equal(t, tc.wantStatus, w.Code, "body: %s", w.Body.String())
		})
	}
}

// TestHandleExecAllowlist_PUT_TrimsAndDedupes verifies whitespace trimming
// and duplicate removal. These are intentional normalisation behaviors so
// the frontend's add-pattern UX stays permissive.
func TestHandleExecAllowlist_PUT_TrimsAndDedupes(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	payload := `{"allowed_binaries":["  git * ","npm run *","git *"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/exec-allowlist", strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/json")
	api.HandleExecAllowlist(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	allowed, _ := resp["allowed_binaries"].([]any)
	require.Len(t, allowed, 2, "dedupe must reduce 3 patterns to 2")
	assert.Equal(t, "git *", allowed[0], "leading/trailing whitespace must be trimmed")
	assert.Equal(t, "npm run *", allowed[1])
}

// TestHandleExecAllowlist_PUT_InvalidJSON returns 400 on malformed body.
func TestHandleExecAllowlist_PUT_InvalidJSON(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/exec-allowlist", strings.NewReader(`not-json`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleExecAllowlist(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestHandleExecAllowlist_MethodNotAllowed returns 405 for DELETE/POST.
func TestHandleExecAllowlist_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/security/exec-allowlist", nil)
	api.HandleExecAllowlist(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
