package gateway

// Regression coverage for the e2e breakage commit de7a497a7 introduced
// (FIX4, 2026-09-14). That commit's D-105 and D-106 fixes are kept; these
// tests pin the two places where they reached further than the defects they
// fix, driven through a real http.ServeMux populated by the production
// registration function rather than by calling the handler directly.
//
//  1. D-106 refused EVERY top-level navigation (Sec-Fetch-Dest: document),
//     including one to a file the token path serves as an ATTACHMENT. An
//     attachment never becomes a document — the browser downloads it — so
//     there is no viewport, tab title or top-window navigation for D-106 to
//     protect, and refusing it only broke the download ADR-067 test 58
//     measures (tests/e2e/preview-pdf-toplevel.spec.ts 58a/58d).
//  2. D-105's bundle web-asset list omitted three §10.4 media extensions
//     (.aac, .opus, .mov) that ADR-067 names as in scope, so a bundle page's
//     own <audio>/<video> element answered 404.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// previewRegressionHarness is a real mux carrying the production preview
// registrations, plus the store those registrations minted into.
type previewRegressionHarness struct {
	f      *previewFixture
	mux    *http.ServeMux
	routes *libraryPreviewRoutes
}

func newPreviewRegressionHarness(t *testing.T) *previewRegressionHarness {
	t.Helper()
	f := newPreviewFixture(t)
	mux := http.NewServeMux()
	store := f.api.registerLibraryPreviewRoutes(&testMuxRegistrar{mux: mux})
	require.NotNil(t, store)
	return &previewRegressionHarness{
		f:   f,
		mux: mux,
		// The mint handler is behind withAuth on the mux; the test drives the
		// same handler against the SAME store the mux's serving route reads.
		routes: &libraryPreviewRoutes{api: f.api, tokens: store},
	}
}

func (h *previewRegressionHarness) write(t *testing.T, rel string, body []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(h.f.workDir, filepath.FromSlash(rel)), body, 0o600))
}

// mint posts a mint request shaped exactly like the e2e specs' own
// (workspace_id, path, scope, and entry_path for a bundle).
func (h *previewRegressionHarness) mint(t *testing.T, scope, path, entry string) gen.LibraryPreviewTokenResponse {
	t.Helper()
	body := map[string]any{"workspace_id": h.f.workspaceID, "path": path, "scope": scope}
	if entry != "" {
		body["entry_path"] = entry
	}
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, libraryPreviewMintPath, bytes.NewReader(buf))
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: "session-fix4"})
	rec := httptest.NewRecorder()
	h.routes.handleMintPreviewToken(rec, r)
	require.Equal(t, http.StatusCreated, rec.Code, "mint %s %s: %s", scope, path, rec.Body.String())
	var resp gen.LibraryPreviewTokenResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

// get sends a GET through the mux. dest is the Sec-Fetch-Dest a browser
// would send: "document" for a top-level navigation, "" for Playwright's
// APIRequestContext (which sends none).
func (h *previewRegressionHarness) get(target, dest string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if dest != "" {
		r.Header.Set("Sec-Fetch-Dest", dest)
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, r)
	return rec
}

// The spec's HTML payload under a .pdf name, a genuine PDF, and HTML under an
// extension on no table — the three token-path attachments tests 58a, 58d
// and preview-svg's "extension on no table" case request.
var (
	fix4ConfusedPDF = []byte("<!doctype html><title>EVIL_EXECUTED</title><p id=evil-marker>FIX4-CONFUSED</p>")
	fix4GenuinePDF  = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")
	fix4UnknownExt  = []byte("<!doctype html><p id=evil-marker>FIX4-UNKNOWN</p>")
)

func TestLibraryPreview_TopLevelNavigationToAnAttachmentDownloads(t *testing.T) {
	h := newPreviewRegressionHarness(t)
	h.write(t, "site/confused.pdf", fix4ConfusedPDF)
	h.write(t, "site/genuine.pdf", fix4GenuinePDF)
	h.write(t, "site/payload.omnipusunknown", fix4UnknownExt)

	cases := []struct {
		rel, wantType string
		body          []byte
	}{
		// §10.4 / FR-015: the extension decides — never the bytes.
		{"site/confused.pdf", "application/pdf", fix4ConfusedPDF},
		{"site/genuine.pdf", "application/pdf", fix4GenuinePDF},
		// FR-015c: an extension on no table is octet-stream and an attachment.
		{"site/payload.omnipusunknown", "application/octet-stream", fix4UnknownExt},
	}
	for _, tc := range cases {
		tok := h.mint(t, "file", tc.rel, "")
		for _, dest := range []string{"document", "iframe", ""} {
			rec := h.get(tokenURL(tok.Token, tc.rel), dest)
			require.Equal(t, http.StatusOK, rec.Code,
				"%s with Sec-Fetch-Dest=%q: an attachment cannot become a document, so a top-level navigation must be allowed to download it (ADR-067 test 58); body: %s",
				tc.rel, dest, rec.Body.String())
			hdr := rec.Header()
			assert.Equal(t, tc.wantType, hdr.Get("Content-Type"), "%s dest=%q", tc.rel, dest)
			assert.Equal(t, "nosniff", hdr.Get("X-Content-Type-Options"), "%s dest=%q", tc.rel, dest)
			assert.True(t, strings.HasPrefix(hdr.Get("Content-Disposition"), "attachment"),
				"%s dest=%q: Content-Disposition=%q", tc.rel, dest, hdr.Get("Content-Disposition"))
			assert.Equal(t, libraryIsolationPolicy(), hdr.Get("Content-Security-Policy"),
				"§10.3: every token-path response carries the policy, an attachment included")
			assert.Equal(t, "no-referrer", hdr.Get("Referrer-Policy"))
			assert.Equal(t, tc.body, rec.Body.Bytes(), "%s dest=%q: the file's own bytes", tc.rel, dest)
		}
	}

	// D-106 still holds for everything that WOULD render: HTML, SVG and inert
	// text answered inline are refused top-level with the interstitial, and
	// their content never reaches the response.
	h.write(t, "site/scripted.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><title>FIX4-SVG</title></svg>`))
	h.write(t, "site/note.md", []byte("FIX4-NOTE"))
	for _, rel := range []string{"site/index.html", "site/scripted.svg", "site/note.md", "site/assets/logo.svg"} {
		tok := h.mint(t, "file", rel, "")
		top := h.get(tokenURL(tok.Token, rel), "document")
		assert.Equal(t, http.StatusForbidden, top.Code, "%s renders inline, so D-106 refuses it top-level", rel)
		assert.Contains(t, top.Body.String(), "only opens inside Omnipus", rel)
		assert.NotContains(t, top.Body.String(), "FIX4-", rel)
		assert.NotContains(t, top.Body.String(), "bundle entry", rel)
		// Path forms that normalise to the same inline file must not slip past
		// the refusal by hiding the extension from a naive last-segment check.
		for _, variant := range []string{rel + "/", rel + "/.", strings.Replace(rel, "site/", "site/./", 1)} {
			rec := h.get(libraryPreviewPathPrefix+tok.Token+"/"+variant, "document")
			assert.NotEqual(t, http.StatusOK, rec.Code, "%q must not render top-level", variant)
			assert.NotContains(t, rec.Body.String(), "FIX4-", variant)
			assert.NotContains(t, rec.Body.String(), "bundle entry", variant)
		}
		// Behind http.ServeMux the variants above are redirected to their
		// clean form before the handler runs. The handler must not rely on
		// that: "<file>/x/.." cleans to <file>, whose extension a raw
		// last-segment check (".." here) would miss.
		direct := httptest.NewRecorder()
		dr := httptest.NewRequest(http.MethodGet, libraryPreviewPathPrefix+tok.Token+"/"+rel+"/x/..", nil)
		dr.Header.Set("Sec-Fetch-Dest", "document")
		h.routes.handleServeLibraryPreview(direct, dr)
		assert.Equal(t, http.StatusForbidden, direct.Code,
			"%s/x/.. cleans to %s, so the handler itself must refuse it top-level", rel, rel)
		assert.NotContains(t, direct.Body.String(), "FIX4-", rel)
	}

	// The bundle entry, minted the way the SPA and every spec mint it.
	bundle := h.mint(t, "bundle", "site", "index.html")
	assert.Equal(t, http.StatusForbidden, h.get(bundle.Url, "document").Code, "D-106's own case")
}

func TestLibraryPreview_D105_BundleTokenRefusesAttachmentSiblingsEvenTopLevel(t *testing.T) {
	h := newPreviewRegressionHarness(t)
	h.write(t, "site/confused.pdf", fix4ConfusedPDF)
	h.write(t, "site/genuine.pdf", fix4GenuinePDF)
	h.write(t, "site/payload.omnipusunknown", fix4UnknownExt)

	// Minted the way the e2e specs mint theirs: one bundle token for the
	// whole fixture directory, entry index.html.
	bundle := h.mint(t, "bundle", "site", "index.html")
	for _, rel := range []string{"site/confused.pdf", "site/genuine.pdf", "site/payload.omnipusunknown"} {
		for _, dest := range []string{"", "document", "iframe"} {
			rec := h.get(tokenURL(bundle.Token, rel), dest)
			if dest == "document" {
				// The one request D-106's narrowing now lets past its early
				// refusal. Whatever answers it, it must not be the file.
				assert.NotEqual(t, http.StatusOK, rec.Code,
					"D-105: narrowing D-106 to inline types must not open a side door to %s", rel)
			} else {
				assert.Equal(t, http.StatusNotFound, rec.Code,
					"D-105: %s is not a web asset of the page, so the bundle token must not serve it (dest=%q)", rel, dest)
			}
			assert.NotContains(t, rec.Body.String(), "FIX4-", rel)
			assert.NotContains(t, rec.Body.String(), "%PDF", rel)
		}
	}
}

func TestLibraryPreview_D105_BundleServesEveryMediaExtensionTheADRNames(t *testing.T) {
	h := newPreviewRegressionHarness(t)
	// ADR-067 "Audio extensions in scope: .mp3, .m4a, .aac, .ogg, .opus, .wav,
	// .flac", and §10.4's three video rows. A bundle page's <audio>/<video>
	// requests these with Sec-Fetch-Dest audio/video.
	media := map[string]string{
		".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".ogg": "audio/ogg",
		".opus": "audio/ogg", ".wav": "audio/wav", ".flac": "audio/flac",
		".mp4": "video/mp4", ".webm": "video/webm", ".mov": "video/quicktime",
	}
	for ext := range media {
		h.write(t, "site/assets/clip"+ext, []byte("media-bytes"))
	}
	bundle := h.mint(t, "bundle", "site", "index.html")
	for ext, wantType := range media {
		rel := "site/assets/clip" + ext
		rec := h.get(tokenURL(bundle.Token, rel), "audio")
		assert.Equal(t, http.StatusOK, rec.Code, "%s is a §10.4 media asset a bundle page plays", rel)
		assert.Equal(t, wantType, rec.Header().Get("Content-Type"), rel)
	}
}
