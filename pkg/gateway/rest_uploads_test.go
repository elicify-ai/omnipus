// rest_uploads_test.go: tests for file uploads and media serving

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// TestServeMedia_WorkspaceRef_LibraryProviderErrors_Returns500Not404 proves a
// wired WorkspaceLibraryProvider that itself returns an error (e.g. the
// owning workspace's library could not be opened — disk error, corrupt
// manifest, permission denied) is reported as a 500 attributable failure,
// never folded into the same 404 a routine absent ref gets.
func TestServeMedia_WorkspaceRef_LibraryProviderErrors_Returns500Not404(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	workspaceID := "ws-media-provider-fails"
	mediaID := "11111111-1111-1111-1111-111111111111"

	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	injected := errors.New("open workspace library: permission denied")
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		if id != workspaceID {
			t.Fatalf("unexpected workspace lookup: %s", id)
		}
		return nil, injected
	})

	path := "/api/v1/media/workspace/" + workspaceID + "/" + mediaID
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.URL.Path = path
	rec := httptest.NewRecorder()
	api.HandleMediaByRef(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.NotEqual(t, http.StatusNotFound, rec.Code,
		"a real provider failure must NOT collapse into the same 404 a routine absent ref gets")

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"], "500 response must carry an attributable error message")
}

// TestServeMedia_WorkspaceRef_NoProviderWired_Returns404 pins the OTHER half
// of the contract: with no WorkspaceLibraryProvider wired at all (the
// legacy-only posture — see SetWorkspaceLibraryProvider's own doc comment),
// a workspace ref is genuinely, routinely unresolvable — this is not a
// resolution-path failure, so it must still map to 404, not 500.
func TestServeMedia_WorkspaceRef_NoProviderWired_Returns404(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	workspaceID := "ws-media-no-provider"
	mediaID := "22222222-2222-2222-2222-222222222222"

	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store
	// Deliberately no SetWorkspaceLibraryProvider call.

	path := "/api/v1/media/workspace/" + workspaceID + "/" + mediaID
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.URL.Path = path
	rec := httptest.NewRecorder()
	api.HandleMediaByRef(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

// TestServeMedia_LegacyRef_UnknownRef_Still404 pins the regression a prior
// fix pass explicitly declined to risk: resolveLegacyWithMeta's absent-ref
// error now wraps media.ErrNotFound instead of being a bare, sentinel-less
// error, but an ordinary unknown LEGACY media ref (the common case — no
// workspace involved at all) must still 404, exactly as before. Without
// this test, a regression that widened serveMedia's failure branch to catch
// the legacy not-found error too would go undetected.
func TestServeMedia_LegacyRef_UnknownRef_Still404(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/does-not-exist", nil)
	rec := httptest.NewRecorder()
	api.HandleMedia(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestServeMedia_WorkspaceRef_Stranded_Returns500NotFound404(t *testing.T) {
	api, _ := newStrandedTestAPI(t)
	workspaceID := "ws-media-serve-stranded"
	mediaID := buildStrandedWorkspaceLibrary(t, api.homePath, workspaceID)

	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	// The handler's own unmodified production path: no fault injection here,
	// just AgentLoop.GetWorkspaceLibrary loading the corrupted-on-disk
	// library the same way the real gateway would on the next request for it.
	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		if id != workspaceID {
			t.Fatalf("unexpected workspace lookup: %s", id)
		}
		return lib, nil
	})

	path := "/api/v1/media/workspace/" + workspaceID + "/" + mediaID
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.URL.Path = path
	rec := httptest.NewRecorder()
	api.HandleMediaByRef(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.NotEqual(t, http.StatusNotFound, rec.Code,
		"a stranded entry must NOT collapse into the same 404 a routine absent ref gets")

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	msg, _ := body["error"].(string)
	assert.Contains(t, msg, "inconsistent state",
		"error body must attribute the failure distinctly, not a generic 404/500")
}

// TestServeMedia_WorkspaceRef_RoutineNotFound_Still404 pins the unchanged
// half of the contract: an ordinary absent media id (no stranded state at
// all) must still 404, exactly as before this fix. Without this, a
// regression that widened the ErrEntryStranded branch to swallow ErrNotFound
// too would go undetected.
func TestServeMedia_WorkspaceRef_RoutineNotFound_Still404(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	workspaceID := "ws-media-serve-routine-404"

	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		return lib, nil
	})

	path := "/api/v1/media/workspace/" + workspaceID + "/" + "00000000-0000-0000-0000-000000000000"
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.URL.Path = path
	rec := httptest.NewRecorder()
	api.HandleMediaByRef(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestHandleMedia_WorkspaceRef_Resolves(t *testing.T) {
	api, _ := newTestRestAPI(t)
	workspaceID := "ws-1"
	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	ref, _, err := lib.Upload("note.txt", gen.MediaLibraryEntrySourceUserUpload, strings.NewReader("workspace bytes"))
	require.NoError(t, err)
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		if id != workspaceID {
			t.Fatalf("unexpected workspace lookup: %s", id)
		}
		return lib, nil
	})
	_, mediaID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/workspace/"+workspaceID+"/"+mediaID, nil)
	req.URL.Path = "/api/v1/media/workspace/" + workspaceID + "/" + mediaID
	rec := httptest.NewRecorder()
	api.HandleMediaByRef(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "workspace bytes", rec.Body.String())
}

func TestHandleMedia_LegacyUUID_StillResolves(t *testing.T) {
	api, _ := newTestRestAPI(t)
	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store
	path := filepath.Join(t.TempDir(), "legacy.txt")
	require.NoError(t, os.WriteFile(path, []byte("legacy bytes"), 0o600))
	ref, err := store.Store(path, media.MediaMeta{ContentType: "text/plain"}, "legacy-test")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+strings.TrimPrefix(ref, "media://"), nil)
	rec := httptest.NewRecorder()
	api.HandleMedia(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "legacy bytes", rec.Body.String())
}

func TestHandleMedia_WorkspaceRef_BadWS_403(t *testing.T) {
	api, _ := newTestRestAPI(t)
	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store
	goodWorkspaceID := "ws-good"
	badWorkspaceID := "ws-bad"
	lib := api.agentLoop.GetWorkspaceLibrary(goodWorkspaceID)
	require.NotNil(t, lib)
	ref, _, err := lib.Upload("note.txt", gen.MediaLibraryEntrySourceUserUpload, bytes.NewBufferString("workspace bytes"))
	require.NoError(t, err)
	_, mediaID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok)
	store.SetWorkspaceLibraryProvider(func(string) (media.WorkspaceLibraryResolver, error) {
		return lib, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/workspace/"+badWorkspaceID+"/"+mediaID, nil)
	req.URL.Path = "/api/v1/media/workspace/" + badWorkspaceID + "/" + mediaID
	rec := httptest.NewRecorder()
	api.HandleMediaByRef(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

func TestHandleMedia_Invalid_400(t *testing.T) {
	api, _ := newTestRestAPI(t)
	for _, path := range []string{
		"/api/v1/media/workspace/ws-1/..",
		"/api/v1/media/workspace/ws-1/",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.URL.Path = path
		rec := httptest.NewRecorder()
		api.HandleMediaByRef(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code, path)
	}
}

// TestHandleUpload_RegistersMediaRef is the #254 regression test: an uploaded
// file must be registered in the media store and its media:// ref returned in
// the response so the SPA can thread it into the message frame's "media" array.
// Without the fix the response carries no ref and the agent never sees the file.
func TestHandleUpload_RegistersMediaRef(t *testing.T) {
	api := newUploadTestAPI(t)
	api.mediaStore = media.NewFileMediaStore()
	sessionID := "media-ref-session"

	body, ct := buildMultipart(t, sessionID, map[string]string{"pic.png": "PNGDATA"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)

	// #254: ref must be present and a media:// URI.
	require.NotNil(t, resp.Files[0].Ref, "uploaded file must carry a media:// ref")
	assert.True(t, strings.HasPrefix(*resp.Files[0].Ref, "media://"),
		"ref must be a media:// URI, got %q", *resp.Files[0].Ref)

	// The ref must resolve back to the file on disk.
	localPath, err := api.mediaStore.Resolve(*resp.Files[0].Ref)
	require.NoError(t, err, "media store must resolve the ref")
	data, err := os.ReadFile(localPath)
	require.NoError(t, err)
	assert.Equal(t, "PNGDATA", string(data))
}

// TestHandleUpload_UsesAgentLoopStore verifies the stale-store fix:
// when the agent loop has a store set (simulating post-restartServices state),
// HandleUpload registers the ref in the AGENT LOOP's store, not a.mediaStore.
// This is the key invariant — the agent loop always resolves via GetMediaStore().
//
// Traces to: #254 stale media store (BLOCKER)
func TestHandleUpload_UsesAgentLoopStore(t *testing.T) {
	api := newUploadTestAPI(t)

	// agentLoopStore simulates the store that restartServices installed.
	agentLoopStore := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(agentLoopStore)

	// a.mediaStore is set to a DIFFERENT (old) store to detect if upload
	// uses the stale reference. After the fix, the agentLoopStore must be used.
	staleStore := media.NewFileMediaStore()
	api.mediaStore = staleStore

	sessionID := "agent-loop-store-session"
	body, ct := buildMultipart(t, sessionID, map[string]string{"doc.txt": "content"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	require.NotNil(t, resp.Files[0].Ref, "must have a ref")

	ref := *resp.Files[0].Ref

	// The ref must resolve in the AGENT LOOP store (the current store).
	_, errAgentLoop := agentLoopStore.Resolve(ref)
	assert.NoError(t, errAgentLoop, "ref must resolve in agent loop's store (the live store)")

	// The ref must NOT resolve in the stale store.
	_, errStale := staleStore.Resolve(ref)
	assert.Error(t, errStale, "ref must NOT resolve in the stale a.mediaStore")
}

// --- HandleUpload tests ---

// TestHandleUpload_Success verifies a well-formed POST /api/v1/upload stores the file
// on disk and returns the correct JSON response.
func TestHandleUpload_Success(t *testing.T) {
	api := newUploadTestAPI(t)
	sessionID := "session-abc123"
	fileContent := "hello upload world"

	body, ct := buildMultipart(t, "", map[string]string{"test.txt": fileContent})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload?session_id="+sessionID, body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "expected 201, body: %s", rr.Body.String())

	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))

	files := resp.Files
	require.Len(t, files, 1)
	assert.Equal(t, "test.txt", files[0].Name)
	assert.Equal(t, int64(len(fileContent)), files[0].Size)
	assert.NotEmpty(t, files[0].Path)
	assert.NotEmpty(t, files[0].ContentType)

	// Confirm data was actually written to disk.
	diskPath := filepath.Join(api.homePath, "uploads", sessionID, "test.txt")
	data, err := os.ReadFile(diskPath)
	require.NoError(t, err, "file should exist on disk")
	assert.Equal(t, fileContent, string(data))
}

// TestHandleUpload_SessionIDFromFormField verifies session_id can come from a
// form field before the file parts rather than the query string.
func TestHandleUpload_SessionIDFromFormField(t *testing.T) {
	api := newUploadTestAPI(t)
	sessionID := "form-session-42"
	fileContent := "from form field session"

	body, ct := buildMultipart(t, sessionID, map[string]string{"note.txt": fileContent})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())

	diskPath := filepath.Join(api.homePath, "uploads", sessionID, "note.txt")
	_, err := os.ReadFile(diskPath)
	require.NoError(t, err, "file should exist on disk under form-supplied session")
}

// TestHandleUpload_MultipleFiles verifies multiple files in one request all land on disk.
func TestHandleUpload_MultipleFiles(t *testing.T) {
	api := newUploadTestAPI(t)
	sessionID := "multi-session"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("session_id", sessionID))
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

	require.Equal(t, http.StatusCreated, rr.Code)

	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Len(t, resp.Files, 3)
}

// TestHandleUpload_MissingSessionID verifies a 400 is returned when session_id is absent.
func TestHandleUpload_MissingSessionID(t *testing.T) {
	api := newUploadTestAPI(t)

	body, ct := buildMultipart(t, "", map[string]string{"x.txt": "data"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestHandleUpload_InvalidSessionID verifies a 400 when session_id contains path separators.
func TestHandleUpload_InvalidSessionID(t *testing.T) {
	api := newUploadTestAPI(t)

	body, ct := buildMultipart(t, "", map[string]string{"x.txt": "data"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload?session_id=../evil", body)
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestHandleUpload_MethodNotAllowed verifies only POST is accepted.
func TestHandleUpload_MethodNotAllowed(t *testing.T) {
	api := newUploadTestAPI(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/upload", nil)
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// TestHandleUpload_NoFiles verifies 400 when there are no file parts.
func TestHandleUpload_NoFiles(t *testing.T) {
	api := newUploadTestAPI(t)

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("session_id", "session-abc"))
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestHandleUpload_FilenamePathTraversal verifies that ../evil filenames are sanitized.
func TestHandleUpload_FilenamePathTraversal(t *testing.T) {
	api := newUploadTestAPI(t)
	sessionID := "safe-session"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("session_id", sessionID))
	fw, err := w.CreateFormFile("file", "../../etc/passwd")
	require.NoError(t, err)
	_, _ = io.WriteString(fw, "malicious")
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	// Either it returns 201 with the sanitized filename "passwd", or 400.
	// In any case the file must NOT appear outside the uploads directory.
	if rr.Code == http.StatusCreated {
		var resp gen.UploadFilesResponse
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
		for _, f := range resp.Files {
			assert.False(t, strings.Contains(f.Path, ".."), "path must not contain ..")
			assert.True(t, strings.HasPrefix(f.Path, "uploads/"), "path must be under uploads/")
		}
		// Confirm the file is inside the uploads dir on disk.
		diskPath := filepath.Join(api.homePath, "uploads", sessionID, "passwd")
		_, err := os.ReadFile(diskPath)
		require.NoError(t, err, "sanitized file should be at passwd, not at ../../etc/passwd")
	} else {
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	}
}

// TestHandleUpload_ContentTypePreserved verifies the content_type field in the response.
func TestHandleUpload_ContentTypePreserved(t *testing.T) {
	api := newUploadTestAPI(t)
	sessionID := "ct-session"

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	require.NoError(t, w.WriteField("session_id", sessionID))
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="image.png"`}
	h["Content-Type"] = []string{"image/png"}
	fw, err := w.CreatePart(h)
	require.NoError(t, err)
	_, _ = io.WriteString(fw, "PNG_BYTES")
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()

	api.HandleUpload(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	var resp gen.UploadFilesResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Files, 1)
	assert.Equal(t, "image/png", resp.Files[0].ContentType)
}

// --- HandleServeUpload tests ---

// TestHandleServeUpload_Success verifies that a previously uploaded file can be retrieved.
func TestHandleServeUpload_Success(t *testing.T) {
	api := newUploadTestAPI(t)
	sessionID := "serve-session"
	content := "file content for serving"

	// Plant a file directly in the uploads directory.
	dir := filepath.Join(api.homePath, "uploads", sessionID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "doc.txt"), []byte(content), 0o600))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/"+sessionID+"/doc.txt", nil)
	rr := httptest.NewRecorder()

	api.HandleServeUpload(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, content, rr.Body.String())
}

// TestHandleServeUpload_NotFound verifies 404 when the file does not exist.
func TestHandleServeUpload_NotFound(t *testing.T) {
	api := newUploadTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/some-session/missing.txt", nil)
	rr := httptest.NewRecorder()

	api.HandleServeUpload(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// TestHandleServeUpload_InvalidSessionID verifies 400 for path-traversal session IDs.
func TestHandleServeUpload_InvalidSessionID(t *testing.T) {
	api := newUploadTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/../etc/passwd", nil)
	rr := httptest.NewRecorder()

	api.HandleServeUpload(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestHandleServeUpload_InvalidFilename verifies 400 for filenames with path separators.
func TestHandleServeUpload_InvalidFilename(t *testing.T) {
	api := newUploadTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/session/../../etc/passwd", nil)
	rr := httptest.NewRecorder()

	api.HandleServeUpload(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// TestHandleServeUpload_MethodNotAllowed verifies only GET/HEAD are accepted.
func TestHandleServeUpload_MethodNotAllowed(t *testing.T) {
	api := newUploadTestAPI(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/session/file.txt", nil)
	rr := httptest.NewRecorder()

	api.HandleServeUpload(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// TestHandleServeUpload_MissingPathParts verifies 400 when URL is incomplete.
func TestHandleServeUpload_MissingPathParts(t *testing.T) {
	api := newUploadTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/only-one-part", nil)
	rr := httptest.NewRecorder()

	api.HandleServeUpload(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
