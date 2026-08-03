package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The start page exists because a fresh tab opening about:blank was
// indistinguishable from a broken panel (operator report, 2026-08-03): on a
// surface whose real failures also render as an empty rectangle, "blank" reads
// as "broken". These tests pin the properties that make it useful — it renders,
// it is self-contained, and it never needs auth.

func TestBrowserStartPage_ServesBrandedHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBrowserStartPage(rec, httptest.NewRequest(http.MethodGet, browserStartPagePath, nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"a cached copy surviving an upgrade would show an old build's start page indefinitely")

	body := rec.Body.String()
	assert.Contains(t, body, "<!doctype html>")
	assert.Contains(t, body, "omnipus", "the page must identify itself — that is its whole job")
	assert.Contains(t, body, "#0A0A0B", "Deep Space Black per the brand guidelines")
	assert.Contains(t, body, "#D4AF37", "Forge Gold accent per the brand guidelines")
}

// TestBrowserStartPage_IsSelfContained — a start page that fetches anything
// over the network defeats its own purpose the moment the network is what is
// broken, and would render as a blank/failed page in an air-gapped install.
func TestBrowserStartPage_IsSelfContained(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBrowserStartPage(rec, httptest.NewRequest(http.MethodGet, browserStartPagePath, nil))
	body := rec.Body.String()

	for _, forbidden := range []string{
		"http://", "https://", "//fonts.", "<script", "<img", "<link",
	} {
		assert.NotContains(t, body, forbidden,
			"the start page must not reference %q — it has to render offline and pre-onboarding", forbidden)
	}
}

// TestBrowserStartPage_MentionsSharedControl — the page is the natural place to
// state the model the panel actually implements: the human and the agent both
// drive. Documenting it here is cheap and prevents the "am I allowed to touch
// this?" hesitation the old exclusive-control UI created.
func TestBrowserStartPage_MentionsSharedControl(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBrowserStartPage(rec, httptest.NewRequest(http.MethodGet, browserStartPagePath, nil))
	body := strings.ToLower(rec.Body.String())

	assert.Contains(t, body, "both drive",
		"the start page should tell the user they and the agent share the browser")
}

func TestBrowserStartPage_HeadRequestHasNoBody(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBrowserStartPage(rec, httptest.NewRequest(http.MethodHead, browserStartPagePath, nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Body.String(), "a HEAD response must carry no body")
}

func TestBrowserStartPage_RejectsNonReadMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleBrowserStartPage(rec, httptest.NewRequest(method, browserStartPagePath, nil))
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
			assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
		})
	}
}

// TestBrowserStartPage_RegisteredOnMux — the handler must actually be reachable
// at the path the browser config points at, and WITHOUT auth: the client is the
// managed headless Chrome, which carries no session cookie, so an authenticated
// route would make every new tab fail to load.
func TestBrowserStartPage_RegisteredOnMux(t *testing.T) {
	mux := http.NewServeMux()
	api := &restAPI{}
	api.registerBrowserStartPage(&testMuxRegistrar{mux: mux})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, browserStartPagePath, nil))

	require.Equal(t, http.StatusOK, rec.Code,
		"the start page must be reachable unauthenticated at %s", browserStartPagePath)
	assert.Contains(t, rec.Body.String(), "omnipus")
}
