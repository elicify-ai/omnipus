// rest_media_stranded_test.go — regression test for serveMedia's
// ErrEntryStranded HTTP mapping (see rest_workspace_media_stranded_test.go's
// doc comment for the underlying "compound rollback-of-rollback" mechanism
// this proves survives HandleMediaByRef, not just the workspace-media
// handlers rest_workspace_media_stranded_test.go already covers).
//
// Before this fix, serveMedia's error handling mapped ANY error other than
// ErrCrossWorkspaceRef/ErrWorkspaceMismatch to a flat 404 "media not found".
// A stranded entry (manifest still claims present, bytes actually
// quarantined at an internal path) collapsed into that same 404 — a
// server-side data-integrity failure reported as "this ref never existed".
// This test builds the real stranded on-disk state via library.go's
// WithFileRenamer/WithManifestWriter fault-injection seams (same helper
// buildStrandedWorkspaceLibrary uses), then drives it through the
// unmodified production resolution path — FileMediaStore.ResolveWithMetaOpts
// -> resolveWorkspaceRef -> library.Library.ResolvePathWithCaller, wired up
// exactly as gateway.go's setupAndStartServices wires it in production (see
// rest_workspace_media_test.go's TestHandleMedia_WorkspaceRef_Resolves for
// the same wiring pattern) — to prove the real handler, not a mocked one,
// now reports 500 with an attributable message instead of 404.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
)

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
