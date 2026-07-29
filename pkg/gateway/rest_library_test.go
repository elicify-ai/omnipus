//go:build !cgo

// Tests for /api/v1/library* (library-spec.md). Covers every operation's
// happy path plus the error/status-code table each operation documents in
// contracts/openapi.yaml, with particular emphasis on path-safety
// adversarial cases (traversal, absolute paths, symlink escape) — the
// highest-risk part of this surface.

package gateway

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// buildLibraryTestAPI creates a minimal restAPI plus one pre-seeded
// workspace, mirroring buildWorkspaceInstructionsTestAPI's pattern.
func buildLibraryTestAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: tmpDir, ModelName: "test-model", MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCore}},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, allowedOrigin: "http://localhost:3000", homePath: tmpDir}

	id := seedLibraryWorkspace(t, api, "Library WS")
	return api, id
}

// seedLibraryWorkspace writes a new workspace metadata file (so
// workspace.Exists returns true) and returns its id.
func seedLibraryWorkspace(t *testing.T, api *restAPI, name string) string {
	t.Helper()
	// Workspace IDs must not contain path separators/dots for
	// validateEntityID — use a short synthetic id, not the human name.
	id := ulidLikeID(t)
	ws := storedWorkspace{
		ID:        id,
		Name:      name,
		Status:    string(gen.WorkspaceStatusActive),
		CoreTeam:  []string{"mia"},
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	require.NoError(t, writeWorkspaceFile(api.homePath, ws))
	return id
}

var ulidCounter int

// ulidLikeID returns a short, unique, path-safe id for test workspaces.
func ulidLikeID(t *testing.T) string {
	t.Helper()
	ulidCounter++
	return time.Now().Format("20060102150405") + "X" + string(rune('A'+ulidCounter%26))
}

func workDir(api *restAPI, workspaceID string) string {
	return filepath.Join(api.homePath, "workspaces", workspaceID, "work")
}

// --- request helpers ---

func libGet(t *testing.T, api *restAPI, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	api.HandleLibrary(w, r)
	return w
}

func libDelete(t *testing.T, api *restAPI, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, target, nil)
	api.HandleLibrary(w, r)
	return w
}

func libPostJSON(t *testing.T, api *restAPI, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, target, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleLibrary(w, r)
	return w
}

func libPutJSON(t *testing.T, api *restAPI, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, target, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleLibrary(w, r)
	return w
}

// buildLibraryMultipart builds a multipart/form-data body with one or more
// "files" parts, named distinctly from upload_test.go's buildMultipart to
// avoid a same-package symbol collision.
func buildLibraryMultipart(t *testing.T, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for name, content := range files {
		fw, err := mw.CreateFormFile("files", name)
		require.NoError(t, err)
		_, err = fw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	return body, mw.FormDataContentType()
}

func libUpload(t *testing.T, api *restAPI, target string, files map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := buildLibraryMultipart(t, files)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, target, body)
	r.Header.Set("Content-Type", contentType)
	api.HandleLibrary(w, r)
	return w
}

func decodeEntries(t *testing.T, body []byte) []gen.LibraryEntry {
	t.Helper()
	var entries []gen.LibraryEntry
	require.NoError(t, json.Unmarshal(body, &entries), "body: %s", string(body))
	return entries
}

func decodeEntry(t *testing.T, body []byte) gen.LibraryEntry {
	t.Helper()
	var entry gen.LibraryEntry
	require.NoError(t, json.Unmarshal(body, &entry), "body: %s", string(body))
	return entry
}

// --- GET /library/workspaces ---

func TestLibraryWorkspaces_ListsWithEntryCounts(t *testing.T) {
	api, id := buildLibraryTestAPI(t)

	w := libGet(t, api, "/api/v1/library/workspaces")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var nodes []gen.LibraryWorkspaceNode
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &nodes))
	require.Len(t, nodes, 1)
	assert.Equal(t, id, nodes[0].Id)
	assert.Equal(t, int32(0), nodes[0].EntryCount, "fresh workspace has no work/ tree yet")

	// Confirm listing did NOT create the work/ directory as a side effect.
	_, statErr := os.Stat(workDir(api, id))
	assert.True(t, os.IsNotExist(statErr))

	// Write a file via PUT content, then list again — count must reflect it.
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"a.txt","content":"hi"}`).Code)
	w2 := libGet(t, api, "/api/v1/library/workspaces")
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &nodes))
	require.Len(t, nodes, 1)
	assert.Equal(t, int32(1), nodes[0].EntryCount)
}

// --- GET/DELETE /library/{id}/entries ---

func TestLibraryEntries_ListRoot_EmptyThenPopulated(t *testing.T) {
	api, id := buildLibraryTestAPI(t)

	w := libGet(t, api, "/api/v1/library/"+id+"/entries")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, []gen.LibraryEntry{}, decodeEntries(t, w.Body.Bytes()))

	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"report.md","content":"# hi"}`).Code)

	w2 := libGet(t, api, "/api/v1/library/"+id+"/entries")
	entries := decodeEntries(t, w2.Body.Bytes())
	require.Len(t, entries, 1)
	assert.Equal(t, "report.md", entries[0].Path)
	assert.False(t, entries[0].IsDir)
	assert.False(t, entries[0].IsHidden)
	assert.True(t, entries[0].IsTextEditable)
}

func TestLibraryEntries_HiddenFiltering(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(workDir(api, id), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(workDir(api, id), ".secret"), []byte("x"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(workDir(api, id), "visible.txt"), []byte("x"), 0o600))

	w := libGet(t, api, "/api/v1/library/"+id+"/entries")
	entries := decodeEntries(t, w.Body.Bytes())
	require.Len(t, entries, 1, "hidden entry must be excluded by default")
	assert.Equal(t, "visible.txt", entries[0].Path)

	w2 := libGet(t, api, "/api/v1/library/"+id+"/entries?include_hidden=true")
	entries2 := decodeEntries(t, w2.Body.Bytes())
	require.Len(t, entries2, 2)
}

func TestLibraryEntries_UnknownWorkspace_404(t *testing.T) {
	api, _ := buildLibraryTestAPI(t)
	w := libGet(t, api, "/api/v1/library/ws-does-not-exist/entries")
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

func TestLibraryEntries_InvalidWorkspaceID_400(t *testing.T) {
	api, _ := buildLibraryTestAPI(t)
	// A workspace id containing ".." (no path separator) reaches
	// validateEntityID and is rejected as a malformed id — 400.
	w := libGet(t, api, "/api/v1/library/..a../entries")
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestLibraryEntries_WorkspaceIDWithSlashes_404NotFound verifies that a
// workspace-id segment smuggling extra path components (e.g. via an
// unencoded "/" or a decoded "%2f") never reaches validateEntityID at all —
// HandleLibrary's own routing (2-segment shape required for
// {workspace_id}/entries) fails to match first and 404s. Either 400 or 404
// is an acceptably SAFE outcome here; the property under test is that the
// request is rejected before any filesystem access — not a specific code.
func TestLibraryEntries_WorkspaceIDWithSlashes_404NotFound(t *testing.T) {
	api, _ := buildLibraryTestAPI(t)
	w := libGet(t, api, "/api/v1/library/..%2f..%2fetc/entries")
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

func TestLibraryEntryDelete_RoundTrip(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"a.txt","content":"x"}`).Code)

	dw := libDelete(t, api, "/api/v1/library/"+id+"/entries?path=a.txt")
	require.Equal(t, http.StatusNoContent, dw.Code, "body: %s", dw.Body.String())

	// Second delete of the same path is now 404.
	dw2 := libDelete(t, api, "/api/v1/library/"+id+"/entries?path=a.txt")
	assert.Equal(t, http.StatusNotFound, dw2.Code)

	w := libGet(t, api, "/api/v1/library/"+id+"/entries")
	assert.Equal(t, []gen.LibraryEntry{}, decodeEntries(t, w.Body.Bytes()))
}

func TestLibraryEntryDelete_MissingPath_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libDelete(t, api, "/api/v1/library/"+id+"/entries")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// --- adversarial path-safety cases (highest-risk part of this task) ---

func TestLibrary_PathTraversal_DotDot_Rejected(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	for _, target := range []string{
		"/api/v1/library/" + id + "/entries?path=../../etc",
		"/api/v1/library/" + id + "/content?path=../../etc/passwd",
		"/api/v1/library/" + id + "/download?path=../../etc/passwd",
	} {
		w := libGet(t, api, target)
		assert.Equal(t, http.StatusBadRequest, w.Code, "target=%s body=%s", target, w.Body.String())
	}
}

func TestLibrary_PathTraversal_URLEncodedDotDotSlash_Rejected(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	// "..%2fescape" — net/url decodes %2f to "/" before this handler ever
	// sees the query value, so this must be rejected exactly like a literal
	// "../escape".
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/library/"+id+"/content?path=..%2fescape", nil)
	w := httptest.NewRecorder()
	api.HandleLibrary(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

func TestLibrary_AbsolutePath_Rejected(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libGet(t, api, "/api/v1/library/"+id+"/content?path=/etc/passwd")
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

func TestLibrary_EmptyAndDotPath_Rejected(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	for _, target := range []string{
		"/api/v1/library/" + id + "/content?path=",
		"/api/v1/library/" + id + "/content?path=.",
		"/api/v1/library/" + id + "/download?path=.",
	} {
		w := libGet(t, api, target)
		assert.Equal(t, http.StatusBadRequest, w.Code, "target=%s body=%s", target, w.Body.String())
	}
}

func TestLibrary_DeeplyNestedValidPath_Accepted(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	nested := "a/b/c/d/e/f"
	require.NoError(t, os.MkdirAll(filepath.Join(workDir(api, id), filepath.FromSlash(nested)), 0o700))

	body := `{"path":"` + nested + `/g.txt","content":"deep"}`
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path="+nested+"/g.txt")
	require.Equal(t, http.StatusOK, gw.Code)
}

func TestLibrary_SymlinkEscape_Rejected(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(workDir(api, id), 0o700))

	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("top secret"), 0o600))
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(workDir(api, id), "escape")))

	for _, target := range []string{
		"/api/v1/library/" + id + "/entries?path=escape",
		"/api/v1/library/" + id + "/content?path=escape/secret.txt",
		"/api/v1/library/" + id + "/download?path=escape/secret.txt",
	} {
		w := libGet(t, api, target)
		assert.Equal(t, http.StatusForbidden, w.Code, "target=%s body=%s", target, w.Body.String())
	}
}

func TestLibrary_SymlinkEscape_ToSiblingWorkspace_Rejected(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	otherID := seedLibraryWorkspace(t, api, "Other WS")
	require.NoError(t, os.MkdirAll(workDir(api, id), 0o700))
	require.NoError(t, os.MkdirAll(workDir(api, otherID), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(workDir(api, otherID), "private.txt"), []byte("shh"), 0o600))

	require.NoError(t, os.Symlink(workDir(api, otherID), filepath.Join(workDir(api, id), "peek")))

	w := libGet(t, api, "/api/v1/library/"+id+"/content?path=peek/private.txt")
	assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
}

// --- GET/PUT /library/{id}/content ---

func TestLibraryContent_RoundTrip(t *testing.T) {
	api, id := buildLibraryTestAPI(t)

	pw := libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"notes.md","content":"# Notes\nhello"}`)
	require.Equal(t, http.StatusOK, pw.Code, "body: %s", pw.Body.String())
	entry := decodeEntry(t, pw.Body.Bytes())
	assert.Equal(t, "notes.md", entry.Path)

	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path=notes.md")
	require.Equal(t, http.StatusOK, gw.Code, "body: %s", gw.Body.String())
	var resp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &resp))
	require.NotNil(t, resp.Content)
	assert.Equal(t, "# Notes\nhello", *resp.Content)
	assert.True(t, resp.IsText)
	assert.False(t, resp.TooLarge)
}

func TestLibraryContent_BinaryFile_NoTextLie(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(workDir(api, id), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(workDir(api, id), "blob.bin"),
		[]byte{0x00, 0x01, 0x02, 0xff}, 0o600))

	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path=blob.bin")
	require.Equal(t, http.StatusOK, gw.Code)
	var resp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &resp))
	assert.False(t, resp.IsText)
	assert.Nil(t, resp.Content)
}

func TestLibraryContent_GetOnDirectory_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(filepath.Join(workDir(api, id), "adir"), 0o700))
	w := libGet(t, api, "/api/v1/library/"+id+"/content?path=adir")
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

func TestLibraryContent_MissingParentDir_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"nope/report.md","content":"x"}`)
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

func TestLibraryContent_OversizedBody_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	oversized := make([]byte, 10*1024*1024+1)
	for i := range oversized {
		oversized[i] = 'A'
	}
	payload, err := json.Marshal(gen.LibraryContentRequest{Path: "big.txt", Content: string(oversized)})
	require.NoError(t, err)
	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", string(payload))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLibraryContent_UnknownWorkspace_404(t *testing.T) {
	api, _ := buildLibraryTestAPI(t)
	w := libGet(t, api, "/api/v1/library/ws-nope/content?path=a.txt")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestLibraryContent_InboundSchemaValidation_MissingField proves
// PUT .../content is actually wired to the generated LibraryContentRequest
// inbound schema (pkg/gateway/inboundschemas/LibraryContentRequest.yaml) —
// a body missing the required "path" field must 400 under
// Gateway.ValidateInbound=true.
func TestLibraryContent_InboundSchemaValidation_MissingField(t *testing.T) {
	api := newTestRestAPIWithValidation(t)
	id := seedLibraryWorkspace(t, api, "Validated WS")

	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"content":"no path field"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// --- POST /library/{id}/upload ---

func TestLibraryUpload_RoundTrip(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libUpload(t, api, "/api/v1/library/"+id+"/upload", map[string]string{"doc.txt": "hello upload"})
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	var resp gen.LibraryUploadResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "doc.txt", resp.Entries[0].Path)

	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path=doc.txt")
	require.Equal(t, http.StatusOK, gw.Code)
}

func TestLibraryUpload_CollisionDeduplicates(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusCreated,
		libUpload(t, api, "/api/v1/library/"+id+"/upload", map[string]string{"doc.txt": "first"}).Code)

	w2 := libUpload(t, api, "/api/v1/library/"+id+"/upload", map[string]string{"doc.txt": "second"})
	require.Equal(t, http.StatusCreated, w2.Code, "body: %s", w2.Body.String())
	var resp gen.LibraryUploadResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "doc (1).txt", resp.Entries[0].Path)
}

func TestLibraryUpload_MissingTargetDir_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libUpload(t, api, "/api/v1/library/"+id+"/upload?path=nope", map[string]string{"doc.txt": "x"})
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// --- GET /library/{id}/download ---

func TestLibraryDownload_RoundTrip(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"file.txt","content":"downloadme"}`).Code)

	w := libGet(t, api, "/api/v1/library/"+id+"/download?path=file.txt")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "downloadme", w.Body.String())
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "file.txt")
}

func TestLibraryDownload_Directory_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.NoError(t, os.MkdirAll(filepath.Join(workDir(api, id), "adir"), 0o700))
	w := libGet(t, api, "/api/v1/library/"+id+"/download?path=adir")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- POST /library/{id}/rename ---

func TestLibraryRename_RoundTrip(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"old.txt","content":"x"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"old.txt","to":"new.txt"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	entry := decodeEntry(t, w.Body.Bytes())
	assert.Equal(t, "new.txt", entry.Path)

	gwOld := libGet(t, api, "/api/v1/library/"+id+"/content?path=old.txt")
	assert.Equal(t, http.StatusNotFound, gwOld.Code)
	gwNew := libGet(t, api, "/api/v1/library/"+id+"/content?path=new.txt")
	assert.Equal(t, http.StatusOK, gwNew.Code)
}

func TestLibraryRename_DestinationExists_409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"a.txt","content":"a"}`).Code)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"b.txt","content":"b"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"a.txt","to":"b.txt"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

func TestLibraryRename_MissingFrom_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"nope.txt","to":"new.txt"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- POST /library/move, POST /library/copy ---

func TestLibraryCopy_CrossWorkspace_RoundTrip(t *testing.T) {
	api, fromID := buildLibraryTestAPI(t)
	toID := seedLibraryWorkspace(t, api, "Dest WS")
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+fromID+"/content", `{"path":"shared.txt","content":"shared"}`).Code)

	body := `{"from_workspace_id":"` + fromID + `","from_path":"shared.txt","to_workspace_id":"` + toID + `","to_path":"copied.txt"}`
	w := libPostJSON(t, api, "/api/v1/library/copy", body)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	entry := decodeEntry(t, w.Body.Bytes())
	assert.Equal(t, "copied.txt", entry.Path)

	// Source remains (copy is non-destructive).
	srcW := libGet(t, api, "/api/v1/library/"+fromID+"/content?path=shared.txt")
	assert.Equal(t, http.StatusOK, srcW.Code)
	// Destination now has it too.
	dstW := libGet(t, api, "/api/v1/library/"+toID+"/content?path=copied.txt")
	assert.Equal(t, http.StatusOK, dstW.Code)
}

func TestLibraryMove_CrossWorkspace_RemovesSource(t *testing.T) {
	api, fromID := buildLibraryTestAPI(t)
	toID := seedLibraryWorkspace(t, api, "Dest WS 2")
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+fromID+"/content", `{"path":"movable.txt","content":"m"}`).Code)

	body := `{"from_workspace_id":"` + fromID + `","from_path":"movable.txt","to_workspace_id":"` + toID + `","to_path":"moved.txt"}`
	w := libPostJSON(t, api, "/api/v1/library/move", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	srcW := libGet(t, api, "/api/v1/library/"+fromID+"/content?path=movable.txt")
	assert.Equal(t, http.StatusNotFound, srcW.Code, "move must remove the source")
	dstW := libGet(t, api, "/api/v1/library/"+toID+"/content?path=moved.txt")
	assert.Equal(t, http.StatusOK, dstW.Code)
}

func TestLibraryMove_SameWorkspace_Sugar(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"x.txt","content":"x"}`).Code)

	body := `{"from_workspace_id":"` + id + `","from_path":"x.txt","to_workspace_id":"` + id + `","to_path":"y.txt"}`
	w := libPostJSON(t, api, "/api/v1/library/move", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

func TestLibraryTransfer_UnknownWorkspace_404(t *testing.T) {
	api, fromID := buildLibraryTestAPI(t)
	body := `{"from_workspace_id":"` + fromID + `","from_path":"a.txt","to_workspace_id":"ws-does-not-exist","to_path":"b.txt"}`
	w := libPostJSON(t, api, "/api/v1/library/move", body)
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

func TestLibraryTransfer_DestinationExists_409(t *testing.T) {
	api, fromID := buildLibraryTestAPI(t)
	toID := seedLibraryWorkspace(t, api, "Dest WS 3")
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+fromID+"/content", `{"path":"a.txt","content":"a"}`).Code)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+toID+"/content", `{"path":"b.txt","content":"b"}`).Code)

	body := `{"from_workspace_id":"` + fromID + `","from_path":"a.txt","to_workspace_id":"` + toID + `","to_path":"b.txt"}`
	w := libPostJSON(t, api, "/api/v1/library/copy", body)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

// --- method-not-allowed / not-found routing ---

func TestLibrary_MethodNotAllowed(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/entries", `{}`)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestLibrary_UnknownSubPath_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libGet(t, api, "/api/v1/library/"+id+"/unknown")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestLibrary_CurlSmokeTest is the "would curl actually work" proof: it
// stands up a REAL listening HTTP server (httptest.NewServer) wrapping
// HandleLibrary at exactly the routes the parent's registration line will
// use, then shells out to the real `curl` binary to exercise write, read,
// list, an adversarial path-traversal attempt, and delete over the wire —
// not just an in-process httptest.ResponseRecorder call. Skips gracefully
// if curl is not on PATH (CI-safety), but this environment has it.
func TestLibrary_CurlSmokeTest(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available in this environment")
	}
	api, id := buildLibraryTestAPI(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/library/", api.HandleLibrary)
	mux.HandleFunc("/api/v1/library", api.HandleLibrary)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	runCurl := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("curl", args...).CombinedOutput()
		require.NoError(t, err, "curl failed: %s", string(out))
		return string(out)
	}

	putOut := runCurl("-sS", "-X", "PUT", srv.URL+"/api/v1/library/"+id+"/content",
		"-H", "Content-Type: application/json",
		"-d", `{"path":"curl-proof.md","content":"# hello from curl"}`)
	t.Logf("curl PUT content -> %s", putOut)
	assert.Contains(t, putOut, `"path":"curl-proof.md"`)

	getOut := runCurl("-sS", srv.URL+"/api/v1/library/"+id+"/content?path=curl-proof.md")
	t.Logf("curl GET content -> %s", getOut)
	assert.Contains(t, getOut, "hello from curl")

	listOut := runCurl("-sS", srv.URL+"/api/v1/library/"+id+"/entries")
	t.Logf("curl GET entries -> %s", listOut)
	assert.Contains(t, listOut, "curl-proof.md")

	travStatus := runCurl("-sS", "-o", "/dev/null", "-w", "%{http_code}",
		srv.URL+"/api/v1/library/"+id+"/content?path=../../etc/passwd")
	t.Logf("curl GET path-traversal attempt -> HTTP %s", travStatus)
	assert.Equal(t, "400", travStatus)

	delStatus := runCurl("-sS", "-o", "/dev/null", "-w", "%{http_code}",
		"-X", "DELETE", srv.URL+"/api/v1/library/"+id+"/entries?path=curl-proof.md")
	t.Logf("curl DELETE entry -> HTTP %s", delStatus)
	assert.Equal(t, "204", delStatus)

	afterDeleteStatus := runCurl("-sS", "-o", "/dev/null", "-w", "%{http_code}",
		srv.URL+"/api/v1/library/"+id+"/content?path=curl-proof.md")
	t.Logf("curl GET after delete -> HTTP %s", afterDeleteStatus)
	assert.Equal(t, "404", afterDeleteStatus)
}
