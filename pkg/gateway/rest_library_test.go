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
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
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

// TestLibraryContent_ReservedDeviceNameFollowsActiveRules proves the
// pkg/pathsafe name-shape checks reach PUT .../content, and that they
// follow the BUILD TARGET's rule set rather than applying everywhere.
//
// This test previously asserted a flat 400 on every OS. ADR-067 Stage 0
// changed that deliberately: these are Windows-shape rules, and on Linux or
// macOS a file may legitimately be called "CON.txt" or "bad|name.txt". A
// flat 400 took away a naming freedom the host filesystem grants, for a
// portability scenario that does not exist — a mount stores an immutable
// absolute host path, so a workspace moved to another OS is broken by the
// path, not by the filenames.
//
// The expectation is derived from ActiveRules rather than hardcoded, because
// hardcoding either answer makes the test wrong on half the CI matrix.
func TestLibraryContent_ReservedDeviceNameFollowsActiveRules(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	for _, tc := range []struct{ path, body string }{
		{"CON.txt", `{"path":"CON.txt","content":"x"}`},
		{"bad|name.txt", `{"path":"bad|name.txt","content":"x"}`},
		{"trailing.space ", `{"path":"trailing.space ","content":"x"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", tc.body)
			if pathsafe.ActiveRules().ValidateComponent(tc.path) != nil {
				assert.Equal(t, http.StatusBadRequest, w.Code,
					"the active rule set rejects %q, so the endpoint must 400: %s", tc.path, w.Body.String())
			} else {
				assert.NotEqual(t, http.StatusBadRequest, w.Code,
					"the active rule set allows %q, so the endpoint must not 400: %s", tc.path, w.Body.String())
			}
		})
	}
}

// TestLibraryContent_WindowsRulesStillRejectThoseNames pins the other half:
// the rules did not disappear, they became conditional. Asserted against the
// rule set as a VALUE so it holds on every runner — without it, deleting the
// Windows rules entirely would leave this file green on Linux and macOS.
func TestLibraryContent_WindowsRulesStillRejectThoseNames(t *testing.T) {
	for _, name := range []string{"CON.txt", "bad|name.txt", "trailing.space "} {
		require.Error(t, pathsafe.WindowsRules.ValidateComponent(name),
			"WindowsRules must still reject %q", name)
	}
}

// TestLibraryContent_CaseInsensitiveCollision_409 confirms PUT
// .../content refuses to silently create a duplicate (Linux) or overwrite
// a different file than the caller named (Windows/macOS) when a
// case-different sibling already exists.
func TestLibraryContent_CaseInsensitiveCollision_409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"Report.txt","content":"original"}`).Code)

	w := libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"report.txt","content":"new"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
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

// TestLibraryUpload_CaseInsensitiveCollisionDeduplicates proves the
// de-duplication numbering itself is now case-insensitive: uploading
// "report.txt" after "Report.txt" already exists must NOT silently create
// a second, differently-cased file — it must be numbered exactly like an
// exact-case collision would be.
func TestLibraryUpload_CaseInsensitiveCollisionDeduplicates(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusCreated,
		libUpload(t, api, "/api/v1/library/"+id+"/upload", map[string]string{"Report.txt": "first"}).Code)

	w2 := libUpload(t, api, "/api/v1/library/"+id+"/upload", map[string]string{"report.txt": "second"})
	require.Equal(t, http.StatusCreated, w2.Code, "body: %s", w2.Body.String())
	var resp gen.LibraryUploadResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp))
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "report (1).txt", resp.Entries[0].Path)

	// The original file's content must be untouched.
	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path=Report.txt")
	require.Equal(t, http.StatusOK, gw.Code)
	var contentResp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &contentResp))
	require.NotNil(t, contentResp.Content)
	assert.Equal(t, "first", *contentResp.Content)
}

func TestLibraryUpload_MissingTargetDir_404(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libUpload(t, api, "/api/v1/library/"+id+"/upload?path=nope", map[string]string{"doc.txt": "x"})
	assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
}

// --- POST /library/{id}/mkdir (UAT Issue 4) ---

func TestLibraryMkdir_SingleLevel_Created(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"subfolder"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	entry := decodeEntry(t, w.Body.Bytes())
	assert.Equal(t, "subfolder", entry.Path)
	assert.True(t, entry.IsDir)

	// The directory is now listable.
	lw := libGet(t, api, "/api/v1/library/"+id+"/entries")
	require.Equal(t, http.StatusOK, lw.Code)
	entries := decodeEntries(t, lw.Body.Bytes())
	require.Len(t, entries, 1)
	assert.Equal(t, "subfolder", entries[0].Name)
}

func TestLibraryMkdir_Nested_CreatesIntermediates(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"a/b/c"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	lw := libGet(t, api, "/api/v1/library/"+id+"/entries?path=a/b")
	require.Equal(t, http.StatusOK, lw.Code, "body: %s", lw.Body.String())
	entries := decodeEntries(t, lw.Body.Bytes())
	require.Len(t, entries, 1)
	assert.Equal(t, "c", entries[0].Name)
}

func TestLibraryMkdir_Idempotent_ReturnsOK(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusCreated,
		libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"again"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"again"}`)
	assert.Equal(t, http.StatusOK, w.Code, "re-creating an existing directory must be idempotent, not an error")
}

func TestLibraryMkdir_AlreadyExistsAsFile_409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"taken.txt","content":"x"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"taken.txt"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
}

func TestLibraryMkdir_UnknownWorkspace_404(t *testing.T) {
	api, _ := buildLibraryTestAPI(t)
	w := libPostJSON(t, api, "/api/v1/library/ws-nope/mkdir", `{"path":"a"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestLibraryMkdir_InvalidPath_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	for _, body := range []string{
		`{"path":"../escape"}`,
		`{"path":"/absolute"}`,
		`{"path":""}`,
	} {
		w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s resp=%s", body, w.Body.String())
	}
}

// TestLibraryMkdir_ReservedDeviceNameFollowsActiveRules is the mkdir half of
// the same contract — see the note on the content test above for why a flat
// 400 on every OS was wrong. Note "notes/COM1": the endpoint checks EVERY
// segment, not just the leaf, so an illegal intermediate directory is caught
// where the active rule set has one.
func TestLibraryMkdir_ReservedDeviceNameFollowsActiveRules(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	for _, tc := range []struct{ path, seg string }{
		{"CON", "CON"},
		{"nul.txt", "nul.txt"},
		{"notes/COM1", "COM1"},
		{"bad<name", "bad<name"},
		{"trailing.", "trailing."},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"`+tc.path+`"}`)
			if pathsafe.ActiveRules().ValidateComponent(tc.seg) != nil {
				assert.Equal(t, http.StatusBadRequest, w.Code,
					"the active rule set rejects %q, so mkdir must 400: %s", tc.seg, w.Body.String())
			} else {
				assert.NotEqual(t, http.StatusBadRequest, w.Code,
					"the active rule set allows %q, so mkdir must not 400: %s", tc.seg, w.Body.String())
			}
		})
	}
}

func TestLibraryMkdir_CaseInsensitiveExistingDirectory_Idempotent(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusCreated,
		libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"Folder"}`).Code)

	// "folder" differs only in case from the existing "Folder" — on
	// Windows/macOS these are literally the same directory, so this must
	// be the same idempotent 200 an exact-case repeat mkdir already gets,
	// not a second, colliding directory.
	w := libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"folder"}`)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// TestLibraryMkdirThenMove_NestedDestination_ClosesUAT4Gap reproduces the
// exact UAT-4 scenario end to end at the REST layer: a nested Move
// destination whose parent doesn't exist yet 404s with a message naming the
// missing directory, then succeeds once that directory is created via the
// new mkdir endpoint.
func TestLibraryMkdirThenMove_NestedDestination_ClosesUAT4Gap(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"test.txt","content":"x"}`).Code)

	body := `{"from_workspace_id":"` + id + `","from_path":"test.txt","to_workspace_id":"` + id + `","to_path":"subfolder/test.txt"}`
	failW := libPostJSON(t, api, "/api/v1/library/move", body)
	require.Equal(t, http.StatusNotFound, failW.Code, "body: %s", failW.Body.String())
	assert.Contains(t, failW.Body.String(), "subfolder",
		"the 404 must name the specific missing directory, not a bare 'not found'")

	require.Equal(t, http.StatusCreated,
		libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"subfolder"}`).Code)

	okW := libPostJSON(t, api, "/api/v1/library/move", body)
	require.Equal(t, http.StatusOK, okW.Code, "body: %s", okW.Body.String())

	dstW := libGet(t, api, "/api/v1/library/"+id+"/content?path=subfolder/test.txt")
	assert.Equal(t, http.StatusOK, dstW.Code)
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

// TestLibraryRename_MissingDestinationParent_NamesDirectory is UAT Issue 4:
// a bare 404 gave no clue which directory to create. The message must name
// the specific missing directory rather than a generic "not found".
func TestLibraryRename_MissingDestinationParent_NamesDirectory(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"a.txt","content":"a"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"a.txt","to":"newfolder/a.txt"}`)
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "newfolder")
}

// TestLibraryRename_DotDotPrefixedDestination_400 is UAT Issue 6: a rename
// destination beginning with ".." (e.g. a URL-encoded-slash smuggling
// attempt like "..%2fdana-pwned-encoded.txt", which arrives as a LITERAL
// filename since it's a JSON body field, not a URL path segment) must be
// rejected outright — not silently succeed and then vanish from the default
// listing because the hidden heuristic also matches a ".."-prefixed name.
func TestLibraryRename_DotDotPrefixedDestination_400(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"a.txt","content":"a"}`).Code)

	for _, to := range []string{
		"..dana-pwned-encoded.txt",
		"..%2fdana-pwned-encoded.txt",
	} {
		w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"a.txt","to":"`+to+`"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code, "to=%q body=%s", to, w.Body.String())
	}

	// The source must still exist — the rejected rename must not have
	// partially applied.
	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path=a.txt")
	assert.Equal(t, http.StatusOK, gw.Code)
}

// TestLibraryRename_CaseInsensitiveCollision_409 is the one gap in this
// whole feature that can actually lose data (not just fail loudly): an
// exact-case Stat check alone would MISS a destination sibling that
// differs only in case on this (case-sensitive, Linux) test machine, but
// the identical rename would silently overwrite that sibling's content the
// moment the same workspace is opened on Windows or default macOS. The
// REST layer must reject it here, not just at the pkg/library unit level.
func TestLibraryRename_CaseInsensitiveCollision_409(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"Report.txt","content":"original"}`).Code)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"draft.txt","content":"draft"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"draft.txt","to":"report.txt"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())

	// The original, differently-cased file must be untouched.
	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path=Report.txt")
	require.Equal(t, http.StatusOK, gw.Code)
	var resp gen.LibraryContentResponse
	require.NoError(t, json.Unmarshal(gw.Body.Bytes(), &resp))
	require.NotNil(t, resp.Content)
	assert.Equal(t, "original", *resp.Content)
}

// TestLibraryRename_CaseOnlyRelabel_Allowed confirms the fix above does not
// overreach: renaming a file to a different case OF ITSELF is a legitimate
// relabel (e.g. fixing a typo'd case), not a collision, and must succeed.
func TestLibraryRename_CaseOnlyRelabel_Allowed(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"Report.txt","content":"hello"}`).Code)

	w := libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"Report.txt","to":"report.txt"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	gw := libGet(t, api, "/api/v1/library/"+id+"/content?path=report.txt")
	assert.Equal(t, http.StatusOK, gw.Code)
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

// TestLibraryCopy_MissingDestinationParent_NamesDirectory is UAT Issue 4's
// cross-workspace counterpart to the rename case: Copy/Move deliberately do
// NOT auto-create missing destination directories, but the 404 must name
// the specific missing directory.
func TestLibraryCopy_MissingDestinationParent_NamesDirectory(t *testing.T) {
	api, fromID := buildLibraryTestAPI(t)
	toID := seedLibraryWorkspace(t, api, "Dest WS 4")
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+fromID+"/content", `{"path":"shared.txt","content":"shared"}`).Code)

	body := `{"from_workspace_id":"` + fromID + `","from_path":"shared.txt","to_workspace_id":"` + toID + `","to_path":"deep/nested/copied.txt"}`
	w := libPostJSON(t, api, "/api/v1/library/copy", body)
	require.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "deep/nested")
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
