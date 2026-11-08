// Issue #716 (founder ruling 2026-09-15): a chat-upload address and a
// workspace-media address require the same login as every other API route.
// An unauthenticated request is 401 and the stored bytes are not in the body.
// A signed-in session cookie still receives the exact bytes (the SPA sends
// that cookie on same-origin image loads).
//
// The legacy /api/v1/media/{uuid} route is deliberately not covered: #716
// leaves it on optional auth if the UUID's randomness is the accepted
// secret-link, and says to document that choice rather than require login.

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// Sentinels are the file bytes #716 says must not leave the server without a
// login. They are not derived from a response.
const (
	uploadSecretBytes         = "UPLOAD-SECRET-BYTES-716"
	workspaceMediaSecretBytes = "WORKSPACE-MEDIA-SECRET-BYTES-716"
	ownerSessionCookie        = "owner-session-cookie-716"
)

func seedOwnerSession(t *testing.T, api *restAPI) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(ownerSessionCookie), bcrypt.MinCost)
	require.NoError(t, err)
	api.agentLoop.GetConfig().Gateway.Users = []config.UserConfig{{
		Username:         "owner",
		SessionTokenHash: config.BcryptHash(hash),
	}}
}

func ownerCookie() *http.Cookie {
	return &http.Cookie{Name: middleware.SessionCookieName, Value: ownerSessionCookie}
}

// serveRegistered drives the production route table (registerAdditionalEndpoints)
// through the production middleware chain, so a registration change from
// withOptionalAuth to withAuth is what this test observes.
func serveRegistered(t *testing.T, api *restAPI, method, path string, header map[string]string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = uniqueTestSourceIP()
	for name, value := range header {
		req.Header.Set(name, value)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, api.agentLoop.GetConfig())
	rec := httptest.NewRecorder()
	buildProductionMiddlewareChain(api, mux).ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func plantChatUpload(t *testing.T, api *restAPI) string {
	t.Helper()
	const sessionID = "serve-session"
	const filename = "doc.txt"
	dir := filepath.Join(api.homePath, "uploads", sessionID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(uploadSecretBytes), 0o600))
	return "/api/v1/uploads/" + sessionID + "/" + filename
}

func plantWorkspaceMedia(t *testing.T, api *restAPI) string {
	t.Helper()
	const workspaceID = "ws-1"
	store := media.NewFileMediaStore()
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store
	lib := api.agentLoop.GetWorkspaceLibrary(workspaceID)
	require.NotNil(t, lib)
	ref, _, err := lib.Upload("note.txt", gen.MediaLibraryEntrySourceUserUpload, strings.NewReader(workspaceMediaSecretBytes))
	require.NoError(t, err)
	store.SetWorkspaceLibraryProvider(func(id string) (media.WorkspaceLibraryResolver, error) {
		if id != workspaceID {
			t.Fatalf("unexpected workspace lookup: %s", id)
		}
		return lib, nil
	})
	_, mediaID, ok := media.ParseWorkspaceRef(ref)
	require.True(t, ok)
	return "/api/v1/media/workspace/" + workspaceID + "/" + mediaID
}

func assertUnauthorizedNoBytes(t *testing.T, rec *httptest.ResponseRecorder, secret string) {
	t.Helper()
	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"issue #716: unauthenticated fetch is 401; body=%s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), secret,
		"issue #716: the stored bytes must not be in an unauthenticated response")
}

func TestUploads_Unauthenticated_Is401_AndReturnsNoBytes(t *testing.T) {
	api := newUploadTestAPI(t)
	seedOwnerSession(t, api)
	path := plantChatUpload(t, api)

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			rec := serveRegistered(t, api, method, path, nil, nil)
			assertUnauthorizedNoBytes(t, rec, uploadSecretBytes)
		})
	}
}

func TestUploads_WrongBearer_Is401_AndReturnsNoBytes(t *testing.T) {
	api := newUploadTestAPI(t)
	seedOwnerSession(t, api)
	path := plantChatUpload(t, api)

	rec := serveRegistered(t, api, http.MethodGet, path, map[string]string{
		"Authorization": "Bearer not-the-owner-token",
	}, nil)
	assertUnauthorizedNoBytes(t, rec, uploadSecretBytes)
}

func TestUploads_InvalidSessionCookie_Is401_AndReturnsNoBytes(t *testing.T) {
	api := newUploadTestAPI(t)
	seedOwnerSession(t, api)
	path := plantChatUpload(t, api)

	rec := serveRegistered(t, api, http.MethodGet, path, nil, []*http.Cookie{{
		Name:  middleware.SessionCookieName,
		Value: "not-the-session",
	}})
	assertUnauthorizedNoBytes(t, rec, uploadSecretBytes)
}

func TestUploads_SessionCookie_ReturnsExactBytes(t *testing.T) {
	api := newUploadTestAPI(t)
	seedOwnerSession(t, api)
	path := plantChatUpload(t, api)

	rec := serveRegistered(t, api, http.MethodGet, path, nil, []*http.Cookie{ownerCookie()})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, uploadSecretBytes, rec.Body.String(),
		"issue #716: a signed-in session still receives the exact uploaded bytes")
}

func TestWorkspaceMedia_Unauthenticated_Is401_AndReturnsNoBytes(t *testing.T) {
	api := newUploadTestAPI(t)
	seedOwnerSession(t, api)
	path := plantWorkspaceMedia(t, api)

	rec := serveRegistered(t, api, http.MethodGet, path, nil, nil)
	assertUnauthorizedNoBytes(t, rec, workspaceMediaSecretBytes)
}

func TestWorkspaceMedia_SessionCookie_ReturnsExactBytes(t *testing.T) {
	api := newUploadTestAPI(t)
	seedOwnerSession(t, api)
	path := plantWorkspaceMedia(t, api)

	rec := serveRegistered(t, api, http.MethodGet, path, nil, []*http.Cookie{ownerCookie()})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, workspaceMediaSecretBytes, rec.Body.String(),
		"issue #716: a signed-in session still receives the exact workspace media bytes")
}
