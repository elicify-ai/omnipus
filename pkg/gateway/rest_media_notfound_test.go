// rest_media_notfound_test.go — regression tests for serveMedia's
// not-found/failure error mapping (media.ErrNotFound / library.ErrNotFound
// vs. a genuine resolution failure).
//
// Before this fix, FileMediaStore.resolveWorkspaceRef's "provider(workspaceID)
// itself returned an error" branch produced a bare
// `fmt.Errorf("media store: workspace library %q unavailable: %w", ...)`
// with no sentinel at all, and resolveLegacyWithMeta's absent-ref branch
// produced a bare `fmt.Errorf("media store: unknown ref: %s", ref)`, also
// with no sentinel. serveMedia's catch-all mapped BOTH of those — a genuine
// disk/library-open failure and a routine absent ref — to the same 404
// "media not found", reporting a real backend fault as "this media never
// existed". A prior fix pass explicitly declined to simply invert the
// catch-all to 500, because doing so would have turned the (sentinel-less)
// legacy not-found path into a false 500 for every ordinary absent legacy
// ref — a real regression.
//
// The fix adds media.ErrNotFound as an explicit sentinel for the genuine
// "no provider wired" / "no resolver for this workspace" / "legacy ref
// absent from the registry" cases, and leaves the provider-returned-an-error
// branch deliberately unwrapped so it is NOT mistaken for ErrNotFound.
// serveMedia now checks for media.ErrNotFound (and library.ErrNotFound, the
// sibling sentinel the workspace library itself returns for an absent
// manifest entry — see rest_media_stranded_test.go for that sentinel's
// existing 404 coverage) and only 404s for those; everything else,
// including the provider-error branch, is a 500.
//
// These three tests pin all three legs of that contract:
//  1. A wired provider that itself errors (real failure) -> 500, not 404.
//  2. No provider wired at all (genuine, routine absence) -> still 404.
//  3. An unknown ref on the LEGACY (non-workspace) route -> still 404 (the
//     regression the prior fix pass was explicitly worried about).

package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media"
)

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
