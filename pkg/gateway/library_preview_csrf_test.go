// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 stage 1 — FR-003j, MV-13 and §10.3 asserted THROUGH THE PRODUCTION
// MIDDLEWARE CHAIN rather than against the handler in isolation.
//
// WHY A SEPARATE FILE. rest_library_preview_test.go calls
// handleServeLibraryPreview directly, and TestLibraryPreviewRoutes_AreReachable-
// OnTheRealMux builds a bare http.NewServeMux with no middleware. Both are
// correct about the handler and blind to the chain wrapped around it: CSRF runs
// gateway-wide, ahead of the router, and until this branch /library-preview/
// was not one of its exempt prefixes. The consequences were reasoned about only
// for blocking:
//
//   - a cookie-less POST — the commonest browser case — was answered by the
//     CSRF gate with a 403 JSON body carrying NO Content-Security-Policy and no
//     `Allow` header. FR-003j says every non-GET/HEAD method returns 405 with
//     that Allow header; §10.3/MV-13 say every response on this path carries
//     the policy byte for byte. Neither held for the case that actually occurs.
//
//   - worse structurally: a previewed document has an OPAQUE origin, so every
//     subresource it fetches is cross-site, so the SameSite=Strict CSRF cookie
//     is never sent, so the middleware's safe-method re-mint branch fired on
//     EVERY stylesheet, script, font, image and media request a previewed page
//     made — stamping a fresh `Set-Cookie` onto each preview response, at a
//     rate the attacker-authored page chooses. That is the exact hazard
//     /preview/ has been prefix-exempt for since ADR-044.
//
// Every assertion below is a property of the CHAIN. Each one passes trivially
// against the bare handler, which is why it could not live in the other file.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// newPreviewChainMux builds the real route table and wraps it in the same
// middleware chain gateway.go installs (buildProductionMiddlewareChain, shared
// with preview_csrf_realmux_test.go so the two cannot disagree about the order).
func newPreviewChainMux(t *testing.T) http.Handler {
	t.Helper()
	api := newTestRestAPIWithHome(t)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	return buildProductionMiddlewareChain(api, mux)
}

// previewChainURL is a well-formed token URL. The token is unknown to the
// store, which is deliberate: every property under test here is decided before
// the token is looked up, and an unknown token gives the same shape of response
// as an expired one (FR-003n).
func previewChainURL() string {
	return tokenURL(strings.Repeat("c", PreviewTokenEncodedLen), "site/index.html")
}

// TestLibraryPreviewChain_NonGetVerbsGet405NotACsrf403 is FR-003j and MV-13
// through the production chain.
//
// The discriminator is precise. A CSRF rejection is 403 with
// `Content-Type: application/json` and no `Allow` header; our handler's refusal
// is 405 with `Allow: GET, HEAD`, `Content-Type: text/html` and the §10.3
// policy. Asserting only "not 200" would pass on either.
func TestLibraryPreviewChain_NonGetVerbsGet405NotACsrf403(t *testing.T) {
	want := specIsolationPolicy(t)
	handler := newPreviewChainMux(t)

	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			// No CSRF cookie and no header — a browser navigating or a page
			// posting to a preview URL has neither, because the cookie is
			// SameSite=Strict and the previewed document's origin is opaque.
			req := httptest.NewRequest(method, previewChainURL(), strings.NewReader("{}"))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusMethodNotAllowed, rec.Code,
				"FR-003j: a 403 here means the CSRF gate answered first — its response carries "+
					"no isolation policy and no Allow header; body: %s", rec.Body.String())
			assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"),
				"FR-003j: the literal Allow value")
			assert.Equal(t, want, rec.Header().Get("Content-Security-Policy"),
				"§10.3/MV-13: EVERY response on this path carries the policy byte for byte, "+
					"including the ones the route produces by refusing")
			assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"),
				"FR-003c: the reader sees a human-readable page, not a JSON error the "+
					"cross-origin frame cannot render")
		})
	}
}

// TestLibraryPreviewChain_SafeRequestsMintNoCookie is the structural half.
//
// A preview page's subresource requests arrive with no cookies at all. The CSRF
// middleware's safe-method branch re-mints a cookie for exactly that case, and
// on any non-exempt prefix it would fire on every single one of them.
//
// The positive control is what makes this test meaningful: the SAME chain, the
// SAME cookie-less GET, aimed at a non-exempt path, MUST produce a Set-Cookie.
// Without it, a chain that had simply stopped issuing cookies at all — a broken
// entropy source, a middleware that was never wired — would pass.
func TestLibraryPreviewChain_SafeRequestsMintNoCookie(t *testing.T) {
	handler := newPreviewChainMux(t)

	t.Run("positive control: a non-exempt path DOES re-mint", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/state", nil))
		require.NotEmpty(t, rec.Header().Values("Set-Cookie"),
			"the re-mint branch must be live, or the assertion below proves nothing")
	})

	for _, target := range []string{
		previewChainURL(),
		tokenURL(strings.Repeat("c", PreviewTokenEncodedLen), "site/assets/theme.css"),
		tokenURL(strings.Repeat("c", PreviewTokenEncodedLen), "site/assets/body.woff2"),
	} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

			assert.Empty(t, rec.Header().Values("Set-Cookie"),
				"a preview response must not carry a Set-Cookie. The previewed document's "+
					"origin is opaque, so every subresource it fetches is cross-site and sends "+
					"no cookie — which means the re-mint branch would fire once per stylesheet, "+
					"script, font, image and media element, rotating the operator's live CSRF "+
					"cookie at a rate the previewed page chooses")
		})
	}
}

// TestLibraryPreviewChain_MintEndpointIsStillCsrfProtected is the boundary in
// the other direction, and it is the reason the exemption is a prefix rather
// than a wildcard over everything with "preview" in the name.
//
// /library-preview/ is a read surface that mutates nothing. The MINT endpoint
// is a genuine POST that issues a credential, it lives under /api/v1/, and it
// must keep full CSRF enforcement — an exemption that swallowed it would let
// any site the operator visits mint preview tokens against their own workspace.
func TestLibraryPreviewChain_MintEndpointIsStillCsrfProtected(t *testing.T) {
	handler := newPreviewChainMux(t)

	req := httptest.NewRequest(http.MethodPost, libraryPreviewMintPath, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"the mint endpoint is a state-changing POST on /api/v1/ and must stay CSRF-gated; body: %s",
		rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
		"the CSRF gate's own error shape, proving it — and not the handler — refused")
}

// TestLibraryPreviewPrefix_IsTheMiddlewareConstant pins the two spellings
// together.
//
// The router serves this prefix and the CSRF middleware exempts it. If they
// ever became two independent literals, a rename on one side would leave the
// middleware exempting a path nothing serves and the router serving a path
// nothing exempts — with no compile error and no test failure anywhere else.
func TestLibraryPreviewPrefix_IsTheMiddlewareConstant(t *testing.T) {
	assert.Equal(t, middleware.LibraryPreviewPathPrefix, libraryPreviewPathPrefix)
	assert.Equal(t, "/library-preview/", libraryPreviewPathPrefix,
		"ADR-067 §10.5 names this exact prefix")
}
