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

// TestBrowserStartPage_IsSelfContained — a start page that FETCHES anything at
// render time defeats its own purpose the moment the network is what is broken,
// and would render blank in an air-gapped install.
//
// The check is deliberately about SUBRESOURCE LOADS, not about the mere presence
// of a URL-shaped string. Two things in this page contain "http" and are
// perfectly fine:
//
//   - xmlns="http://www.w3.org/2000/svg" on the inline logo — an XML namespace
//     IDENTIFIER. It is never resolved over the network; it is required for the
//     SVG to parse at all.
//   - the search form's action="https://…" — a USER-INITIATED navigation that
//     happens only when someone presses Search. Nothing loads at render time,
//     and searching the web inherently needs the web.
//
// An earlier version of this test banned the substrings "http://"/"https://"
// outright, which flagged both of the above as violations. That would have
// forced either a broken logo or a search box that cannot search — the check
// was measuring the wrong thing, so it is now written against the property that
// actually matters.
func TestBrowserStartPage_IsSelfContained(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBrowserStartPage(rec, httptest.NewRequest(http.MethodGet, browserStartPagePath, nil))
	body := rec.Body.String()

	// Tags/constructs that cause the browser to FETCH something while rendering.
	for _, forbidden := range []string{
		"<script",  // no JS at all, remote or otherwise
		"<img",     // images must be inline SVG
		"<link",    // no external stylesheets/icons
		"@import",  // no CSS-imported stylesheets
		"//fonts.", // no webfonts
		"src=",     // any subresource src, on any element
	} {
		assert.NotContains(t, body, forbidden,
			"the start page must not reference %q — it has to render offline and pre-onboarding", forbidden)
	}

	// CSS url() would fetch at render time (background-image, font-face, …).
	assert.NotContains(t, body, "url(",
		"CSS url() fetches at render time; inline the asset instead")

	// Any remote reference that DOES appear must be one of the two benign kinds
	// above — never a fetch. Assert positively so a future edit that adds, say,
	// a tracking pixel host is caught even though it uses none of the tags above.
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "http://") && !strings.Contains(line, "https://") {
			continue
		}
		isNamespace := strings.Contains(line, `xmlns="http://www.w3.org/2000/svg"`)
		isFormAction := strings.Contains(line, "<form") && strings.Contains(line, "action=")
		assert.True(
			t,
			isNamespace || isFormAction,
			"unexpected remote reference on line %q — only the SVG xmlns and the search form action may contain a URL",
			line,
		)
	}
}

// TestBrowserStartPage_RendersTheOmnipusLogo — the mark must be the project's
// real logo asset (src/assets/logo/omnipus-logo.svg, inlined), not an ad-hoc
// approximation. An earlier version drew a hand-made octopus path, which the
// operator correctly flagged as "not the omnipus logo".
func TestBrowserStartPage_RendersTheOmnipusLogo(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBrowserStartPage(rec, httptest.NewRequest(http.MethodGet, browserStartPagePath, nil))
	body := rec.Body.String()

	assert.Contains(t, body, `viewBox="0 0 640 570"`,
		"the inlined mark must be the real omnipus-logo.svg (its viewBox), not a substitute drawing")
	assert.Contains(t, body, `fill="#D4AF37"`, "the mark must render in Forge Gold")
	assert.Contains(t, body, `aria-label="Omnipus"`, "the mark must be labeled for screen readers")
}

// TestBrowserStartPage_HasSearchBox — the operator asked for a Google-style
// search box on the start page. It must be a real, submittable form so pressing
// Enter searches, and it must not depend on JavaScript.
func TestBrowserStartPage_HasSearchBox(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBrowserStartPage(rec, httptest.NewRequest(http.MethodGet, browserStartPagePath, nil))
	body := rec.Body.String()

	assert.Contains(t, body, `<form`, "a real form, so Enter submits without JS")
	assert.Contains(t, body, `method="GET"`, "search must be a plain GET navigation")
	assert.Contains(t, body, `name="q"`, "the query parameter the search engine expects")
	assert.Contains(t, body, `role="search"`, "landmark role so assistive tech can find it")
	assert.Contains(t, body, "autofocus",
		"the caret should already be in the box — the point of a start page is to start typing")
	assert.NotContains(t, body, "<script",
		"search must work with JavaScript disabled")
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
