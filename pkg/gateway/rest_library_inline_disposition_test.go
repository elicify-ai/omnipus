// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 stage 1 — GET /api/v1/library/{workspace_id}/inline-disposition
// (FR-080 / Constraint #8, D15, FR-014, FR-015a, FR-015b, FR-017).
//
// The endpoint was fully specified in contracts/openapi.yaml — operationId,
// schema, 200/400/401/403/404/500 — and its Go and TypeScript types were
// generated and committed, with no handler behind any of it. The generated type
// was used only as an internal enum. Contract-first means the contract comes
// first, not instead.
//
// ORACLE DISCIPLINE. Every expected value below comes from ADR-067 §10.4's
// table and FR-014/FR-017's rule about which formats the BROWSER executes —
// never from calling the handler and writing down what it said.

package gateway

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestLibraryInlineDisposition_AnswersTheAllowListTable drives the real handler
// over one file per §10.4 group.
func TestLibraryInlineDisposition_AnswersTheAllowListTable(t *testing.T) {
	api, workspaceID := buildLibraryTestAPI(t)
	dir := workDir(api, workspaceID)
	require.NoError(t, os.MkdirAll(dir, 0o700))

	cases := []struct {
		name            string
		wantDisposition gen.LibraryInlineDispositionDisposition
		wantType        string
		wantRenderer    gen.LibraryInlineDispositionRenderer
		wantSandbox     bool
		why             string
	}{
		{
			name: "report.html", wantDisposition: gen.LibraryInlineDispositionDispositionInline,
			wantType: "text/html; charset=utf-8", wantRenderer: gen.LibraryInlineDispositionRendererHtml,
			wantSandbox: true,
			why:         "§10.4 active documents — the one class the browser executes (FR-014)",
		},
		{
			name: "theme.css", wantDisposition: gen.LibraryInlineDispositionDispositionInline,
			wantType: "text/css; charset=utf-8", wantRenderer: gen.LibraryInlineDispositionRendererCode,
			wantSandbox: false,
			why:         "§10.4 bundle code: inline as a subresource, but not a document on its own",
		},
		{
			name: "logo.svg", wantDisposition: gen.LibraryInlineDispositionDispositionInline,
			wantType: "image/svg+xml", wantRenderer: gen.LibraryInlineDispositionRendererImage,
			wantSandbox: false,
			why: "§10.4's THIRD context: inside the SPA an .svg is classified as an image and " +
				"drawn in an <img>, which never runs its scripts. Answering html/true here would " +
				"tell the SPA to mount an active-document surface for every logo in the Library",
		},
		{
			name: "clip.aac", wantDisposition: gen.LibraryInlineDispositionDispositionInline,
			wantType: "audio/aac", wantRenderer: gen.LibraryInlineDispositionRendererAudio,
			wantSandbox: false,
			why:         "FR-015b: .aac is absent from Go's built-in table, so a host-registry answer differs per machine",
		},
		{
			name: "clip.mp4", wantDisposition: gen.LibraryInlineDispositionDispositionInline,
			wantType: "video/mp4", wantRenderer: gen.LibraryInlineDispositionRendererVideo,
			wantSandbox: false,
			why:         "Go's built-in table types .webm as audio/webm; this table must not defer to it",
		},
		{
			name: "notes.md", wantDisposition: gen.LibraryInlineDispositionDispositionInline,
			wantType: "text/markdown; charset=utf-8", wantRenderer: gen.LibraryInlineDispositionRendererMarkdown,
			wantSandbox: false,
			why:         "§10.4 inert text, drawn by Omnipus's own markdown renderer",
		},
		{
			name: "manual.pdf", wantDisposition: gen.LibraryInlineDispositionDispositionAttachment,
			wantType: "application/pdf", wantRenderer: gen.LibraryInlineDispositionRendererPdf,
			wantSandbox: false,
			why: "§10.4: .pdf is typed but deliberately NOT inline. PDF.js fetches the bytes " +
				"from the authenticated endpoint, so a PDF never becomes a browser document (FR-018)",
		},
		{
			name: "archive.zip", wantDisposition: gen.LibraryInlineDispositionDispositionAttachment,
			wantType: "application/octet-stream", wantRenderer: gen.LibraryInlineDispositionRendererNone,
			wantSandbox: false,
			why:         "US-1 AS-7: an unsupported binary keeps the download card, unchanged",
		},
	}

	for _, tc := range cases {
		require.NoError(t, os.WriteFile(filepath.Join(dir, tc.name), []byte("x"), 0o600))
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := libGet(t, api,
				"/api/v1/library/"+workspaceID+"/inline-disposition?path="+url.QueryEscape(tc.name))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var got gen.LibraryInlineDisposition
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))

			assert.Equal(t, tc.name, got.Path)
			assert.Equal(t, filepath.Ext(tc.name), got.Extension)
			assert.Equal(t, tc.wantDisposition, got.Disposition, tc.why)
			assert.Equal(t, tc.wantType, got.ContentType, tc.why)
			assert.Equal(t, tc.wantRenderer, got.Renderer, tc.why)
			assert.Equal(t, tc.wantSandbox, got.RequiresSandbox,
				"FR-014: only content the BROWSER executes is sandboxed. %s", tc.why)

			if tc.wantDisposition == gen.LibraryInlineDispositionDispositionAttachment {
				require.NotNil(t, got.Reason,
					"the schema says reason is present when disposition is attachment, so the "+
						"SPA can say something better than a blank download card")
				assert.NotEmpty(t, *got.Reason)
				return
			}
			assert.Nil(t, got.Reason,
				"reason is present ONLY for an attachment; on an inline answer it is a field "+
					"the SPA would have to learn to ignore")
		})
	}
}

// TestLibraryInlineDisposition_AgreesWithWhatIsActuallyServed is the property
// that makes this endpoint worth having.
//
// Two independent code paths answer "how will these bytes be served" — this
// endpoint, and applyLibraryByteHeaders on the real serving route. If they ever
// disagree, the SPA mounts a surface for a response it will not get, which is
// the type confusion FR-015 exists to prevent, arriving from the inside. So the
// answer is compared against the REAL preview-token response for the same file,
// header by header.
func TestLibraryInlineDisposition_AgreesWithWhatIsActuallyServed(t *testing.T) {
	for _, name := range []string{
		"report.html", "theme.css", "logo.svg", "clip.aac",
		"notes.md", "manual.pdf", "archive.zip",
	} {
		t.Run(name, func(t *testing.T) {
			api, workspaceID := buildLibraryTestAPI(t)
			dir := workDir(api, workspaceID)
			require.NoError(t, os.MkdirAll(dir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))

			rec := libGet(t, api,
				"/api/v1/library/"+workspaceID+"/inline-disposition?path="+url.QueryEscape(name))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var answer gen.LibraryInlineDisposition
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &answer))

			served := serveViaLibraryPreviewRoute(t, name, []byte("x"))
			require.Equal(t, http.StatusOK, served.Code, served.Body.String())

			assert.Equal(t, answer.ContentType, served.Header().Get("Content-Type"),
				"the announced Content-Type must be the one the bytes actually arrive with")

			wantInline := answer.Disposition == gen.LibraryInlineDispositionDispositionInline
			// Parse the disposition TYPE rather than string-comparing the
			// whole header: an inline response carries the filename too
			// (`inline; filename="report.html"`), so `== "inline"` silently
			// answered false for every case once the shared helper started
			// preserving it — which made the endpoint look like it disagreed
			// with what was actually served.
			gotType, _, parseErr := mime.ParseMediaType(served.Header().Get("Content-Disposition"))
			require.NoError(t, parseErr, "Content-Disposition must be a well-formed RFC 6266 value")
			gotInline := gotType == "inline"
			assert.Equal(t, wantInline, gotInline,
				"the announced disposition must be the one the serving route chooses")
		})
	}
}

// TestLibraryInlineDisposition_RefusalsMatchTheContract covers the statuses the
// operation declares.
func TestLibraryInlineDisposition_RefusalsMatchTheContract(t *testing.T) {
	api, workspaceID := buildLibraryTestAPI(t)
	dir := workDir(api, workspaceID)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "folder"), 0o700))

	t.Run("missing path is 400", func(t *testing.T) {
		rec := libGet(t, api, "/api/v1/library/"+workspaceID+"/inline-disposition")
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})

	t.Run("traversal is 400", func(t *testing.T) {
		rec := libGet(t, api, "/api/v1/library/"+workspaceID+
			"/inline-disposition?path="+url.QueryEscape("../../etc/passwd"))
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})

	t.Run("a file that does not exist is 404", func(t *testing.T) {
		// Not a plausible-looking answer for a fictional file: the SPA would
		// mount a renderer for it and only then discover the 404.
		rec := libGet(t, api, "/api/v1/library/"+workspaceID+
			"/inline-disposition?path=nowhere.html")
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})

	t.Run("a directory is 404", func(t *testing.T) {
		rec := libGet(t, api, "/api/v1/library/"+workspaceID+
			"/inline-disposition?path=folder")
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})

	t.Run("an unknown workspace is 404", func(t *testing.T) {
		rec := libGet(t, api, "/api/v1/library/wsdoesnotexist/inline-disposition?path=x.html")
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})

	t.Run("a non-GET method is 405", func(t *testing.T) {
		rec := libDelete(t, api, "/api/v1/library/"+workspaceID+"/inline-disposition?path=x.html")
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, rec.Body.String())
	})
}

// TestLibraryInlineDisposition_IsRoutedNotJust404 pins the dispatcher entry.
//
// HandleLibrary's switch ends in `default: http.NotFound(w, r)`, so a handler
// that exists but is never dispatched answers 404 for every request — which is
// what shipped: the operation was in the contract, the types were generated, and
// the sub-segment had no case. A 404 for a REAL file is the discriminator; a 404
// for a missing one is indistinguishable from the unrouted state.
func TestLibraryInlineDisposition_IsRoutedNotJust404(t *testing.T) {
	api, workspaceID := buildLibraryTestAPI(t)
	dir := workDir(api, workspaceID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "routed.html"), []byte("x"), 0o600))

	rec := libGet(t, api,
		"/api/v1/library/"+workspaceID+"/inline-disposition?path=routed.html")
	require.Equal(t, http.StatusOK, rec.Code,
		"a 404 here means the sub-segment reached HandleLibrary's default branch — the "+
			"handler is unrouted, which is how this operation shipped as contract-only; body: %s",
		rec.Body.String())
}
