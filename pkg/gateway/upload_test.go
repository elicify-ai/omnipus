package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newUploadTestAPI returns a restAPI wired to a temp directory for upload tests.
func newUploadTestAPI(t *testing.T) *restAPI {
	t.Helper()
	api, _ := newTestRestAPI(t)
	api.homePath = t.TempDir()
	return api
}

// buildMultipart constructs a multipart body with the given files.
// files is a map of filename -> content.
func buildMultipart(t *testing.T, sessionID string, files map[string]string) (body *bytes.Buffer, contentType string) {
	t.Helper()
	body = &bytes.Buffer{}
	w := multipart.NewWriter(body)
	if sessionID != "" {
		require.NoError(t, w.WriteField("session_id", sessionID))
	}
	for name, content := range files {
		fw, err := w.CreateFormFile("file", name)
		require.NoError(t, err)
		_, err = io.WriteString(fw, content)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return body, w.FormDataContentType()
}

// --- Workspace media library upload tests (ADR-051 Rev 4, FR-001) ---

// TestUpload_Endpoint_TargetsWorkspaceLibrary is spec TDD test #32 (ADR-051
// Rev 4, FR-001): POST /api/v1/upload with a workspace_id writes the file to
// the workspace's persistent media library (workspaces/<ws>/media/) via
// library.Upload — NOT the legacy session-scoped uploads dir. The response
// carries a media://workspace/<ws>/<id> ref.
//
// BDD:
//
//	Given a workspace_id is sent with an upload,
//	When POST /api/v1/upload is called,
//	Then the file is stored under workspaces/<ws>/media/,
//	And the response ref is media://workspace/<ws>/<id>,
//	And no file is written to the legacy uploads dir.
func TestUpload_Endpoint_TargetsWorkspaceLibrary(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-media-upload"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("workspace_id", workspaceID))
	fw, err := w.CreateFormFile("file", "screenshot.png")
	require.NoError(t, err)
	_, _ = io.WriteString(fw, "PNGDATA")
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())

	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)

	// FR-001: the ref must be a workspace media ref, not a legacy media://<uuid>.
	require.NotNil(t, resp.Files[0].Ref, "must carry a media://workspace/ ref")
	ref := *resp.Files[0].Ref
	assert.True(t, strings.HasPrefix(ref, "media://workspace/"+workspaceID+"/"),
		"ref must be workspace-scoped, got %q", ref)

	// The file must be in workspaces/<ws>/media/ — verify via the library.
	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib, "workspace library must be resolvable")
	entries := lib.List()
	require.Len(t, entries, 1, "manifest must have exactly one entry")
	assert.Equal(t, "screenshot.png", entries[0].Filename)

	// The raw bytes must be readable through the library (sha256-verified).
	_, mediaID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok, "ref must parse into workspace + media ID")
	data, _, readErr := lib.Read(mediaID)
	require.NoError(t, readErr, "library must read back the uploaded bytes")
	assert.Equal(t, "PNGDATA", string(data))

	// The legacy session-scoped uploads dir must NOT have been created.
	legacyDir := filepath.Join(api.homePath, "uploads")
	_, statErr := os.Stat(legacyDir)
	assert.True(t, os.IsNotExist(statErr),
		"legacy uploads dir must not exist; workspace path should have been used")
}

// TestUpload_Endpoint_WorkspaceIDFromQueryParam verifies workspace_id can be
// supplied via the query string (matching the session_id pattern), not only as
// a form field.
func TestUpload_Endpoint_WorkspaceIDFromQueryParam(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-query-upload"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", "doc.txt")
	require.NoError(t, err)
	_, _ = io.WriteString(fw, "hello workspace")
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload?workspace_id="+workspaceID, body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	require.NotNil(t, resp.Files[0].Ref)
	assert.True(t, strings.HasPrefix(*resp.Files[0].Ref, "media://workspace/"+workspaceID+"/"),
		"query-param workspace_id must route to the library, got %q", *resp.Files[0].Ref)
}

// TestUpload_Endpoint_WorkspaceMultiFile verifies multiple files in one
// workspace-scoped request all land in the library.
func TestUpload_Endpoint_WorkspaceMultiFile(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-multi"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("workspace_id", workspaceID))
	for i := 1; i <= 3; i++ {
		fw, err := w.CreateFormFile("file", fmt.Sprintf("file%d.txt", i))
		require.NoError(t, err)
		_, _ = fmt.Fprintf(fw, "content %d", i)
	}
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 3)
	for _, f := range resp.Files {
		require.NotNil(t, f.Ref)
		assert.True(t, strings.HasPrefix(*f.Ref, "media://workspace/"+workspaceID+"/"),
			"every file must carry a workspace ref, got %q", *f.Ref)
	}
	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	assert.Len(t, lib.List(), 3, "library manifest must hold all 3 entries")
}

// TestUpload_Endpoint_NoWorkspaceIDStillLegacy verifies that without a
// workspace_id the handler falls back to the legacy session-scoped path
// (backward compat). This guards against regressions where workspace routing
// accidentally fires for plain session uploads.
func TestUpload_Endpoint_NoWorkspaceIDStillLegacy(t *testing.T) {
	api := newUploadTestAPI(t)
	sessionID := "legacy-session"

	body, ct := buildMultipart(t, sessionID, map[string]string{"note.txt": "legacy content"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)

	// Legacy path: file on disk under uploads/<session_id>/.
	diskPath := filepath.Join(api.homePath, "uploads", sessionID, "note.txt")
	data, err := os.ReadFile(diskPath)
	require.NoError(t, err, "file should be in the legacy uploads dir")
	assert.Equal(t, "legacy content", string(data))

	// The ref (if present) must NOT be a workspace ref — legacy path does not
	// use the workspace library.
	if resp.Files[0].Ref != nil {
		assert.False(t, strings.HasPrefix(*resp.Files[0].Ref, "media://workspace/"),
			"legacy upload must not produce a workspace ref, got %q", *resp.Files[0].Ref)
	}
}

// --- D-1 (library-spec, 2026-07-29 UAT) dual-write tests ---
//
// workspaces/<id>/media/ is a SIBLING of work/ — structurally unreachable by
// every agent file tool, which opens an os.Root at work/ and cannot escape
// it by construction (ADR-046). These tests prove the fix: a chat upload
// with a workspace_id ALSO lands as a real, named file inside
// workspaces/<id>/work/.library/, readable through the SAME sandboxed
// file-tool machinery every other agent file operation uses — not just
// present on disk, but ACTUALLY reachable through the rooted tool path.

// TestUpload_Endpoint_DualWritesToWorkspaceLibrary is the core D-1
// regression test: the uploaded bytes must ALSO be staged at
// workspaces/<id>/work/.library/<filename> (not just workspaces/<id>/media/),
// and the exact work-relative path must be recorded in the
// pkg/agent upload-work-path registry for D1b's announcement to use. Proves
// the round trip end-to-end: the file is then read back through
// tools.LibraryReadTool — the SAME sandboxed, os.Root-confined path a real
// agent turn uses — not merely os.ReadFile against a known path.
func TestUpload_Endpoint_DualWritesToWorkspaceLibrary(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-dualwrite"
	fileContent := "PPTX-BYTES-STAND-IN"
	fileName := "Copy of elicify_company_profile.pptx"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("workspace_id", workspaceID))
	fw, err := w.CreateFormFile("file", fileName)
	require.NoError(t, err)
	_, _ = io.WriteString(fw, fileContent)
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
	ref := *resp.Files[0].Ref

	// The dual-write copy must exist on disk, byte-identical to the library copy.
	workDir, err := workspace.SafeWorkDir(api.homePath, workspaceID)
	require.NoError(t, err)
	dualWritePath := filepath.Join(workDir, ".library", fileName)
	data, readErr := os.ReadFile(dualWritePath)
	require.NoError(t, readErr, "dual-write copy must exist at work/.library/<filename>")
	assert.Equal(t, fileContent, string(data))

	// The exact work-relative path must be recorded for D1b's announcement.
	recorded, ok := agent.LookupUploadWorkPath(ref)
	require.True(t, ok, "the upload handler must record the work-relative path for this ref")
	assert.Equal(t, ".library/"+fileName, recorded)

	// Round trip: prove the SAME sandboxed tool path a real agent turn uses
	// (os.Root-confined, restrict=true) can read this exact file back.
	libReadTool := tools.NewLibraryReadTool(workDir, true, tools.MaxReadFileSize)
	result := libReadTool.Execute(context.Background(), map[string]any{"path": fileName})
	require.False(t, result.IsError, "library_read must succeed reading the dual-written file: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, fileContent,
		"library_read must return the exact bytes the upload staged")
}

// TestUpload_Endpoint_DualWriteDeduplicatesOnFilenameCollision verifies the
// numeric-suffix de-duplication D-1 requires: uploading two DIFFERENT files
// with the SAME name to the same workspace must not let the second upload
// clobber the first — the second lands at ".library/name (1).ext".
func TestUpload_Endpoint_DualWriteDeduplicatesOnFilenameCollision(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-dedup"

	upload := func(content string) gen.UploadedFile {
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		require.NoError(t, w.WriteField("workspace_id", workspaceID))
		fw, err := w.CreateFormFile("file", "report.txt")
		require.NoError(t, err)
		_, _ = io.WriteString(fw, content)
		require.NoError(t, w.Close())

		req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rr := httptest.NewRecorder()
		api.HandleUpload(rr, req)
		require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
		var resp gen.UploadFilesResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		require.Len(t, resp.Files, 1)
		return resp.Files[0]
	}

	first := upload("first version")
	second := upload("second version")

	workDir, err := workspace.SafeWorkDir(api.homePath, workspaceID)
	require.NoError(t, err)

	firstData, err := os.ReadFile(filepath.Join(workDir, ".library", "report.txt"))
	require.NoError(t, err)
	assert.Equal(t, "first version", string(firstData))

	secondData, err := os.ReadFile(filepath.Join(workDir, ".library", "report (1).txt"))
	require.NoError(t, err, "second upload with a colliding name must be de-duplicated with a numeric suffix")
	assert.Equal(t, "second version", string(secondData))

	require.NotNil(t, first.Ref)
	require.NotNil(t, second.Ref)
	firstRecorded, ok := agent.LookupUploadWorkPath(*first.Ref)
	require.True(t, ok)
	assert.Equal(t, ".library/report.txt", firstRecorded)
	secondRecorded, ok := agent.LookupUploadWorkPath(*second.Ref)
	require.True(t, ok)
	assert.Equal(t, ".library/report (1).txt", secondRecorded)
}

// TestUpload_Endpoint_DualWriteFailureRollsBackLibraryEntry verifies that if
// the D-1 dual-write cannot be staged (here: a plain file already occupies
// the .library/ path, so os.MkdirAll fails), the WHOLE upload is treated as
// a failure — including rolling back the media-library entry that had
// already been created — rather than silently leaving an entry the agent
// still cannot read.
func TestUpload_Endpoint_DualWriteFailureRollsBackLibraryEntry(t *testing.T) {
	api := newUploadTestAPI(t)
	workspaceID := "ws-stage-fail"

	workDir, err := workspace.SafeWorkDir(api.homePath, workspaceID)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(workDir, 0o755))
	// Occupy the .library/ path with a plain file so MkdirAll(libraryDir) fails.
	require.NoError(t, os.WriteFile(filepath.Join(workDir, ".library"), []byte("blocker"), 0o644))

	body, ct := func() (*bytes.Buffer, string) {
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		require.NoError(t, w.WriteField("workspace_id", workspaceID))
		fw, err := w.CreateFormFile("file", "blocked.txt")
		require.NoError(t, err)
		_, _ = io.WriteString(fw, "should not persist")
		require.NoError(t, w.Close())
		return body, w.FormDataContentType()
	}()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code, "body: %s", rr.Body.String())

	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	assert.Empty(t, lib.List(),
		"the media-library entry must be rolled back when the dual-write fails — "+
			"an entry the agent still cannot read must not survive as a false success")
}
