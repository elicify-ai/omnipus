// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — tests for the unified /preview/ route (rest_preview.go).
//
// Coverage:
//   - GET /preview/<agent>/<token>/ for a registered static handle → 200.
//   - GET /preview/<agent>/<token>/ with unknown token → 404.
//   - GET with expired registration → 404.
//   - Cross-agent token reuse → 403 or 404.
//   - Malformed URL → 400.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newPreviewRouteTestAPI returns a minimal restAPI with real ServedSubdirs wired.
func newPreviewRouteTestAPI(t *testing.T) (*restAPI, *agent.ServedSubdirs) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	ss := agent.NewServedSubdirs()
	t.Cleanup(ss.Stop)

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		servedSubdirs: ss,
	}
	return api, ss
}

// TestHandlePreview_StaticRegistration verifies that a registered static
// directory is served correctly via /preview/<agent>/<token>/.
func TestHandlePreview_StaticRegistration(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)
	workDir := t.TempDir()

	// Write a test file.
	indexPath := filepath.Join(workDir, "index.html")
	require.NoError(t, os.WriteFile(indexPath, []byte("<html>hello</html>"), 0o644))

	// Register the directory.
	token, _, err := ss.Register("agent1", workDir, time.Hour)
	require.NoError(t, err)

	// GET /preview/agent1/<token>/
	req := httptest.NewRequest(http.MethodGet, "/preview/agent1/"+token+"/", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "registered static token must return 200")
	assert.Contains(t, rec.Body.String(), "hello", "response must contain file content")
}

// TestHandlePreview_StaticFile verifies a specific subpath within the registered dir.
func TestHandlePreview_StaticFile(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)
	workDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(workDir, "app.js"), []byte("console.log(1)"), 0o644))

	token, _, err := ss.Register("agent2", workDir, time.Hour)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/preview/agent2/"+token+"/app.js", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "console.log")
}

// TestHandlePreview_UnknownToken verifies that an unregistered token returns 404.
func TestHandlePreview_UnknownToken(t *testing.T) {
	api, _ := newPreviewRouteTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/preview/agent1/unknowntoken123/", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "unknown token must return 404")
}

// TestHandlePreview_ExpiredRegistration verifies that an expired registration
// returns 404.
func TestHandlePreview_ExpiredRegistration(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "index.html"), []byte("x"), 0o644))

	// Register with minimal duration then let it expire.
	token, _, err := ss.Register("agent3", workDir, time.Millisecond)
	require.NoError(t, err)

	// Wait for expiry — ServedSubdirs janitor runs every 30 s, but the Lookup
	// checks the deadline directly so we just need to wait past it.
	time.Sleep(5 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/preview/agent3/"+token+"/", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	// After expiry, Lookup returns nil — should be 404.
	assert.Equal(t, http.StatusNotFound, rec.Code, "expired registration must return 404")
}

// TestHandlePreview_CrossAgentToken verifies that a token registered for
// agent A cannot be used under agent B's URL segment.
func TestHandlePreview_CrossAgentToken(t *testing.T) {
	api, ss := newPreviewRouteTestAPI(t)
	workDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "index.html"), []byte("x"), 0o644))

	token, _, err := ss.Register("agent-a", workDir, time.Hour)
	require.NoError(t, err)

	// Use the token under agent-b's path.
	req := httptest.NewRequest(http.MethodGet, "/preview/agent-b/"+token+"/", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	// Should be 403 (token_agent_mismatch) or 404.
	assert.NotEqual(t, http.StatusOK, rec.Code, "cross-agent token must not return 200")
}

// TestHandlePreview_MalformedURL verifies that /preview/ with no agent/token
// segment returns 400.
func TestHandlePreview_MalformedURL(t *testing.T) {
	api, _ := newPreviewRouteTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/preview/", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code, "bare /preview/ must return 400")
}

// TestHandlePreview_OptionsPreflightReturns204 verifies CORS preflight handling.
func TestHandlePreview_OptionsPreflightReturns204(t *testing.T) {
	api, _ := newPreviewRouteTestAPI(t)

	req := httptest.NewRequest(http.MethodOptions, "/preview/agent1/tok/", nil)
	rec := httptest.NewRecorder()
	api.HandlePreview(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code, "OPTIONS must return 204")
}
