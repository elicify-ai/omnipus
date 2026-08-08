// Regression coverage for #609: the /api/v1/workspaces/{ws}/media* dispatch
// inside HandleWorkspaces. The release parent (41e16237) routed media
// subpaths to HandleWorkspaceMedia; the #597 merge resolution (ab1c1aad)
// dropped that block, orphaning the handler entirely — every media-library
// REST call 404'd while the handler, its direct-invocation tests, the
// OpenAPI contract, and the live SPA consumer (ComposerMediaLibrary) all
// survived. The existing rest_workspace_media_*_test.go files call
// HandleWorkspaceMedia directly, so they stayed green across the breakage;
// THIS test goes through HandleWorkspaces so the wiring itself is what is
// guarded.
package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
)

func TestHandleWorkspaces_MediaSubpath_DispatchesToWorkspaceMedia(t *testing.T) {
	api, _ := newTestRestAPI(t)
	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store

	const workspaceID = "ws-dispatch-regression"
	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	_, _, err := lib.Upload("dispatch-probe.txt", gen.UserUpload, bytes.NewBufferString("media dispatch bytes"))
	require.NoError(t, err)
	store.SetWorkspaceLibraryProvider(func(string) (media.WorkspaceLibraryResolver, error) {
		return lib, nil
	})

	// THROUGH the workspaces dispatcher — not HandleWorkspaceMedia directly.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/media", nil)
	rec := httptest.NewRecorder()
	api.HandleWorkspaces(rec, req)

	require.NotEqual(t, http.StatusNotFound, rec.Code,
		"BUG REGRESSION (#609): HandleWorkspaces must route /workspaces/{ws}/media to "+
			"HandleWorkspaceMedia — a 404 here means the dispatch block was dropped again "+
			"(the direct-invocation media tests cannot catch that)")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "dispatch-probe.txt",
		"the media list reached through the dispatcher must contain the uploaded item")
}

// TestHandleWorkspaces_MediaSubpath_AttachmentsRouteReachable pins the POST
// /workspaces/{ws}/media/attachments leg of the same dispatch: reaching the
// handler yields a handler-level verdict (anything but the dispatcher's
// 404), proving the third path segment also survives the segment check.
func TestHandleWorkspaces_MediaSubpath_AttachmentsRouteReachable(t *testing.T) {
	api, _ := newTestRestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-x/media/attachments", nil)
	rec := httptest.NewRecorder()
	api.HandleWorkspaces(rec, req)

	// GET on the POST-only attachments route: the handler answers 405.
	// The pre-#609 dispatcher answered 404 without ever reaching it.
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"BUG REGRESSION (#609): /media/attachments must reach HandleWorkspaceMedia "+
			"(405 for GET), not die in HandleWorkspaces' unknown-subpath 404")
}
