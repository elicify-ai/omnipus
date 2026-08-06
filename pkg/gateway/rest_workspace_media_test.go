package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
)

func TestHandleMedia_WorkspaceRef_Resolves(t *testing.T) {
	api, _ := newTestRestAPI(t)
	workspaceID := "ws-1"
	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	ref, _, err := lib.Upload("note.txt", gen.UserUpload, strings.NewReader("workspace bytes"))
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
	ref, _, err := lib.Upload("note.txt", gen.UserUpload, bytes.NewBufferString("workspace bytes"))
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
