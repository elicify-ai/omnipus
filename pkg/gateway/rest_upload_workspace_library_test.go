// rest_upload_workspace_library_test.go — FIX-1 regression test.
//
// POST /api/v1/upload's workspace-media-library path
// (rest.go's HandleUpload, ADR-051 Rev 4 FR-001) called
// a.agentLoop.GetWorkspaceLibrary(workspaceID), which returns nil on ANY
// internal failure of library.New (corrupt manifest, permission error,
// disk I/O) — not just "not configured". Before this fix, a nil there
// unconditionally fell back to the legacy session-scoped upload path and
// still returned 201: the file silently never entered the workspace media
// library (no GET /workspaces/{id}/media coverage, no refcount, no
// orphan-GC, no cascade-delete, no media://workspace/ resolution),
// defeating the whole feature this branch ships while looking successful
// to the caller.

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// newWorkspaceLibraryTestAPI builds a restAPI whose homePath field and its
// AgentLoop's own internal homePath resolve to the SAME directory —
// matching production wiring (gateway.go threads one shared homePath into
// both agent.NewAgentLoop, via cfg.Agents.Defaults.Home =
// filepath.Join(homePath, "agents") — see config.go's
// EnsureConfigDefaults — and the restAPI struct literal's homePath field).
//
// newUploadTestAPI (upload_test.go) does NOT have this property: it
// reassigns api.homePath to a brand-new temp dir AFTER the AgentLoop has
// already been constructed against a different (Defaults.Home-derived)
// directory, so a raw filesystem write under api.homePath is invisible to
// a.agentLoop.GetWorkspaceLibrary (which resolves against the AgentLoop's
// own homePath, not the restAPI's). Existing workspace-library tests never
// noticed because they always verify through
// api.agentLoop.GetWorkspaceLibrary(...) itself rather than a raw path —
// this test needs to plant a corrupt manifest.json exactly where the real
// code looks for it, so it needs the two to actually agree.
func newWorkspaceLibraryTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	home := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	seedTestAgents(cfg)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      home,
	}
}

// TestUpload_WorkspaceLibraryLoadFailure_Returns500NotSilentFallback plants
// a corrupt manifest.json so library.New (re-derived inside HandleUpload
// when the cached accessor returns nil) deterministically fails with a real
// error rather than "not configured". The upload must hard-fail with 500,
// never silently degrade to a 201 in the legacy uploads dir.
func TestUpload_WorkspaceLibraryLoadFailure_Returns500NotSilentFallback(t *testing.T) {
	api := newWorkspaceLibraryTestAPI(t)
	workspaceID := "ws-corrupt-manifest"

	mediaDir := filepath.Join(api.homePath, "workspaces", workspaceID, "media")
	require.NoError(t, os.MkdirAll(mediaDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(mediaDir, "manifest.json"), []byte("{not valid json"), 0o600))

	// Re-review FIX 1: the "workspace library load failed" log used to be a
	// bare slog.Error, invisible on a real (backgrounded) gateway. Capture
	// via pkg/logger's file sink (the one gateway.go's own
	// logger.EnableFileLogging call wires to gateway.log) to prove it is now
	// discoverable there.
	logFile := filepath.Join(t.TempDir(), "upload-workspace-library.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("workspace_id", workspaceID))
	fw, err := w.CreateFormFile("file", "doc.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(fw, "must not be silently stored via the legacy path")
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(
		t,
		http.StatusInternalServerError,
		rr.Code,
		"a workspace library that exists but fails to load must be a hard error, not a silent legacy fallback; body: %s",
		rr.Body.String(),
	)

	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &errResp))
	assert.NotEmpty(t, errResp.Error)

	// The file must NOT have silently landed in the legacy session-scoped
	// uploads dir — that would be exactly the masked-failure behavior being
	// fixed (a plausible 201 with the data going somewhere the workspace
	// media APIs never look).
	legacyDir := filepath.Join(api.homePath, "uploads")
	_, statErr := os.Stat(legacyDir)
	assert.True(t, os.IsNotExist(statErr),
		"a corrupt workspace library must not silently fall back to the legacy uploads path")

	// FIX 1: the failure must be discoverable in the gateway.log-equivalent
	// file sink, not just returned to the caller.
	logged, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Contains(t, string(logged), "upload: workspace library load failed")
	assert.Contains(t, string(logged), workspaceID)
}

// TestUpload_WorkspaceLibraryHealthy_StillSucceeds is the control case
// alongside the failure test above: a HEALTHY workspace library (no
// pre-existing manifest — the common "first upload for this workspace"
// case) must still succeed exactly as before this fix. Guards against the
// fix over-correcting into treating every nil as a hard failure.
func TestUpload_WorkspaceLibraryHealthy_StillSucceeds(t *testing.T) {
	api := newWorkspaceLibraryTestAPI(t)
	workspaceID := "ws-healthy"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("workspace_id", workspaceID))
	fw, err := w.CreateFormFile("file", "doc.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(fw, "healthy workspace upload")
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	require.NotNil(t, resp.Files[0].Ref)
	assert.Contains(t, *resp.Files[0].Ref, "media://workspace/"+workspaceID+"/")
}
