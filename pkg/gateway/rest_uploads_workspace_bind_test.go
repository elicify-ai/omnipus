package gateway

// rest_uploads_workspace_bind_test.go — RED (#1428 step 2 option C, qa-lead):
// the /upload routing prefix, pair check, duplicate and late-field rules
// (S-10, spec §4.3.7; SV-14).
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence):
//   - SPEC-C-server-binds-send-to-chat-r8.md §4.3.7 / §4.2 C-7: routing
//     fields are session_id and workspace_id from the query and from form
//     fields BEFORE the first file part; the prefix is frozen and the pair is
//     checked AT the first file part (not found → 404; read error → 500;
//     meta.WorkspaceID != workspace_id → 409 upload_workspace_mismatch);
//     only then is the first file written. Duplicates before the first file:
//     same value → accepted; different → 400 upload_routing_conflict. A late
//     routing field (after a file part) → 400 upload_routing_field_late.
//     Workspace-only and session-only uploads unchanged; existing 503 kept.
//   - §6.1 SV-14 (a)-(e) plus the four controls.
//
// RED STATE (F-h): the handler never checks the pair, accepts a routing form
// field after a file part when that field was empty before, and silently
// ignores a second value — so (a)-(e) fail today on the observed status code
// (201 where the spec demands 409/400/404), while the four controls pass
// today and must keep passing.

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const (
	uploadBindW1  = "ws-uploadbind-w1"
	uploadBindW2  = "ws-uploadbind-w2"
	uploadBindSfx = "SV14"
)

// newUploadBindAPI builds the upload test API plus a chat session S whose
// metadata workspace is W1 (the pair check reads chat metadata).
func newUploadBindAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	api := newUploadTestAPI(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err, "SETUP: create chat S")
	w1 := uploadBindW1
	require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &w1}),
		"SETUP: stamp chat S with workspace W1")
	return api, meta.ID
}

// uploadBindRequest POSTs one multipart /upload request; fields are written
// in the given order relative to the single file part.
func uploadBindRequest(t *testing.T, api *restAPI, query string, fieldsBefore []struct{ name, value string }, files int, fieldsAfter []struct{ name, value string }) *httptest.ResponseRecorder {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	for _, f := range fieldsBefore {
		require.NoError(t, w.WriteField(f.name, f.value))
	}
	for i := 0; i < files; i++ {
		fw, err := w.CreateFormFile("file", uploadBindSfx+"-file-"+string(rune('a'+i))+".txt")
		require.NoError(t, err)
		_, err = io.WriteString(fw, "upload-bind-content")
		require.NoError(t, err)
	}
	for _, f := range fieldsAfter {
		require.NoError(t, w.WriteField(f.name, f.value))
	}
	require.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/upload"+query, body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()
	api.HandleUpload(rr, req)
	return rr
}

// assertNoFileInWorkspaceLibrary proves "nothing written" for the W2 side of
// a refused request: the workspace library manifest carries no entry and no
// media file landed.
func assertNoFileInWorkspaceLibrary(t *testing.T, api *restAPI, workspaceID, phase string) {
	t.Helper()
	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	if lib == nil {
		t.Fatalf("%s: workspace library %s must be resolvable for the nothing-written check", phase, workspaceID)
	}
	entries := lib.List()
	require.Empty(t, entries,
		"%s: nothing may be written into %s's library (found %d entries)", phase, workspaceID, len(entries))
}

// TestUploadWorkspaceBind_SV14 drives SV-14 (a)-(e): the routing prefix is
// frozen at the first file part and the chat/workspace pair is checked before
// any file of that request is written.
func TestUploadWorkspaceBind_SV14(t *testing.T) {
	t.Run("a query pair mismatch refused 409 before any write", func(t *testing.T) {
		api, chatID := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api,
			"?session_id="+chatID+"&workspace_id="+uploadBindW2,
			nil, 1, nil)
		require.Equal(t, http.StatusConflict, rr.Code,
			"SV-14a violated: chat S is in W1, the request names W2 — the pair must be refused "+
				"409 upload_workspace_mismatch before the first file is written; got %d body=%s",
			rr.Code, rr.Body.String())
		require.Contains(t, rr.Body.String(), "upload_workspace_mismatch",
			"409 body must name upload_workspace_mismatch")
		assertNoFileInWorkspaceLibrary(t, api, uploadBindW2, "SV-14a")
	})

	t.Run("b form-field pair before the file refused 409", func(t *testing.T) {
		api, chatID := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api, "",
			[]struct{ name, value string }{
				{"session_id", chatID},
				{"workspace_id", uploadBindW2},
			}, 1, nil)
		require.Equal(t, http.StatusConflict, rr.Code,
			"SV-14b violated: the same pair as form fields before the file must be refused 409; "+
				"got %d body=%s", rr.Code, rr.Body.String())
		require.Contains(t, rr.Body.String(), "upload_workspace_mismatch")
		assertNoFileInWorkspaceLibrary(t, api, uploadBindW2, "SV-14b")
	})

	t.Run("c late routing field after the file refused 400", func(t *testing.T) {
		api, chatID := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api, "",
			[]struct{ name, value string }{{"workspace_id", uploadBindW2}}, 1,
			[]struct{ name, value string }{{"session_id", chatID}})
		require.Equal(t, http.StatusBadRequest, rr.Code,
			"SV-14c violated: a session_id form field AFTER the file part must be refused "+
				"400 upload_routing_field_late; today it is silently accepted; got %d body=%s",
			rr.Code, rr.Body.String())
		require.Contains(t, rr.Body.String(), "upload_routing_field_late",
			"400 body must name upload_routing_field_late")
	})

	t.Run("d two different routing values before the file refused 400", func(t *testing.T) {
		api, _ := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api, "",
			[]struct{ name, value string }{
				{"workspace_id", uploadBindW1},
				{"workspace_id", uploadBindW2},
			}, 1, nil)
		require.Equal(t, http.StatusBadRequest, rr.Code,
			"SV-14d violated: two different workspace_id values before the file must be refused "+
				"400 upload_routing_conflict; today the second is silently ignored; got %d body=%s",
			rr.Code, rr.Body.String())
		require.Contains(t, rr.Body.String(), "upload_routing_conflict")
	})

	t.Run("e unknown chat refused 404 before any write", func(t *testing.T) {
		api, _ := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api,
			"?session_id=ws-uploadbind-unknown-chat&workspace_id="+uploadBindW1,
			nil, 1, nil)
		require.Equal(t, http.StatusNotFound, rr.Code,
			"SV-14e violated: an unknown chat id in the routing prefix must be refused 404; "+
				"got %d body=%s", rr.Code, rr.Body.String())
		assertNoFileInWorkspaceLibrary(t, api, uploadBindW1, "SV-14e")
	})
}

// TestUploadWorkspaceBind_SV14_Controls pins the four unchanged behaviours:
// a matching pair lands 201 in W1; workspace-only and session-only uploads
// are unchanged; the workspace library being unavailable keeps the existing
// 503. These pass today and must keep passing after the pair check lands.
func TestUploadWorkspaceBind_SV14_Controls(t *testing.T) {
	t.Run("matching pair accepted into W1", func(t *testing.T) {
		api, chatID := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api,
			"?session_id="+chatID+"&workspace_id="+uploadBindW1, nil, 1, nil)
		require.Equal(t, http.StatusCreated, rr.Code,
			"control: the matching W1/W1 pair must be accepted 201; got %d body=%s", rr.Code, rr.Body.String())
		lib := api.agentLoop.GetWorkspaceLibrary(uploadBindW1)
		require.NotNil(t, lib)
		require.Len(t, lib.List(), 1, "control: the file must land in W1's library")
	})

	t.Run("workspace-only upload unchanged", func(t *testing.T) {
		api, _ := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api, "?workspace_id="+uploadBindW2, nil, 1, nil)
		require.Equal(t, http.StatusCreated, rr.Code,
			"control: workspace-only upload stays 201; got %d body=%s", rr.Code, rr.Body.String())
	})

	t.Run("session-only legacy upload unchanged", func(t *testing.T) {
		api, chatID := newUploadBindAPI(t)
		rr := uploadBindRequest(t, api, "?session_id="+chatID, nil, 1, nil)
		require.Equal(t, http.StatusCreated, rr.Code,
			"control: session-only legacy upload stays 201; got %d body=%s", rr.Code, rr.Body.String())
	})

	t.Run("unavailable workspace library keeps 503", func(t *testing.T) {
		// The harness that keeps restAPI.homePath and the agent loop's home
		// in the same directory (the corrupt-manifest trigger resolves
		// through the agent loop's home).
		api := newWorkspaceLibraryTestAPI(t)
		// A corrupt manifest.json makes library.New fail deterministically
		// (the same trigger
		// TestUpload_WorkspaceLibraryLoadFailure_Returns503NotSilentFallback
		// uses for the existing explicit-503 branch, #1449 C-I1/FR-017).
		mediaDir := filepath.Join(api.homePath, "workspaces", uploadBindW2, "media")
		require.NoError(t, os.MkdirAll(mediaDir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(mediaDir, "manifest.json"), []byte("{not valid json"), 0o600))
		rr := uploadBindRequest(t, api, "?workspace_id="+uploadBindW2, nil, 1, nil)
		require.Equal(t, http.StatusServiceUnavailable, rr.Code,
			"control: an unavailable workspace library keeps the existing 503; got %d body=%s",
			rr.Code, strings.TrimSpace(rr.Body.String()))
	})
}
