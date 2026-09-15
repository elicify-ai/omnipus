package gateway

// Regression coverage for UAT 2026-09-13 D-105 (a bundle token admitted every
// sibling file), D-106 (a preview URL opened as a top-level page dropped every
// presentation control) and D-110 (closing a preview did not revoke its token).

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestLibraryPreview_D105_BundleTokenServesEntryAndWebAssetsOnly(t *testing.T) {
	f := newPreviewFixture(t)
	work := workDir(f.api, f.workspaceID)
	// A sibling NOTE inside the bundle directory — the D-105 case verbatim
	// (…/Dashboards/Overview.md answered 200 with the note's real content).
	const noteMarker = "D105-SIBLING-NOTE-CONTENT"
	require.NoError(t, os.WriteFile(filepath.Join(work, "site", "Overview.md"), []byte(noteMarker), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(work, "site", "about.html"), []byte("<p>about page</p>"), 0o600))

	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	entry := f.serve(t, http.MethodGet, minted.Url)
	require.Equal(t, http.StatusOK, entry.Code, "positive control: the entry document is served")

	for _, asset := range []string{"site/assets/app.js", "site/assets/theme.css", "site/assets/logo.svg", "site/assets/body.woff2", "site/about.html"} {
		rec := f.serve(t, http.MethodGet, tokenURL(minted.Token, asset))
		assert.Equal(t, http.StatusOK, rec.Code, "%s is a web asset the page may load", asset)
	}

	for _, doc := range []string{"site/Overview.md", "site/data.bin"} {
		rec := f.serve(t, http.MethodGet, tokenURL(minted.Token, doc))
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s is a sibling document, not an asset of the page", doc)
		assert.NotContains(t, rec.Body.String(), noteMarker)
	}

	t.Run("a file grant still serves exactly its own file", func(t *testing.T) {
		tok := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "site/Overview.md")
		rec := f.serve(t, http.MethodGet, tokenURL(tok.Token, "site/Overview.md"))
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestLibraryPreview_D106_TopLevelNavigationIsRefused(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	serveWithDest := func(dest string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, minted.Url, nil)
		if dest != "" {
			r.Header.Set("Sec-Fetch-Dest", dest)
		}
		f.routes.handleServeLibraryPreview(rec, r)
		return rec
	}

	top := serveWithDest("document")
	assert.Equal(t, http.StatusForbidden, top.Code, "a top-level navigation must not render the file")
	assert.Contains(t, top.Body.String(), "only opens inside Omnipus")
	assert.NotContains(t, top.Body.String(), "bundle entry", "the file's own content must not be served top-level")
	assert.NotEmpty(t, top.Header().Get("Content-Security-Policy"), "the interstitial carries the policy like every other response")

	for _, dest := range []string{"iframe", "script", "style", "image", "font", ""} {
		rec := serveWithDest(dest)
		assert.Equal(t, http.StatusOK, rec.Code, "Sec-Fetch-Dest=%q must still be served", dest)
	}
}

func TestLibraryPreview_D110_RevokeOnClose(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")
	require.Equal(t, http.StatusOK, f.serve(t, http.MethodGet, minted.Url).Code, "positive control: live before revoke")

	revoke := func(token string, method string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		f.routes.handleRevokePreviewToken(rec, httptest.NewRequest(method, libraryPreviewMintPath+"/"+token, nil))
		return rec
	}

	assert.Equal(t, http.StatusNoContent, revoke(minted.Token, http.MethodDelete).Code)
	after := f.serve(t, http.MethodGet, minted.Url)
	assert.Equal(t, http.StatusNotFound, after.Code, "the URL must stop working the moment the preview is closed")
	assert.Contains(t, after.Body.String(), "no longer valid")

	assert.Equal(t, http.StatusNoContent, revoke(minted.Token, http.MethodDelete).Code, "idempotent")
	assert.Equal(t, http.StatusNoContent, revoke("never-minted-token", http.MethodDelete).Code, "FR-003n: no oracle for whether a token existed")
	assert.Equal(t, http.StatusMethodNotAllowed, revoke(minted.Token, http.MethodGet).Code)
	assert.Equal(t, http.StatusBadRequest, revoke("", http.MethodDelete).Code)
}
