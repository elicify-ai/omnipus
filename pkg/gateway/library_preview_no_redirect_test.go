// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// library_preview_no_redirect_test.go — the tripwire for the one condition the
// 2026-09-14 path confinement of ADR-067 §10.3 rests on.
//
// WHY A REDIRECT IS THE HOLE. §10.3's six subresource directives name each
// gateway origin confined to /library-preview/, so a previewed page cannot load
// /api/... at all: the browser refuses before any request leaves. But CSP3
// §6.7.2.9 ignores a source's PATH once a request has been redirected. Measured
// 2026-09-14 on Chromium, Firefox and WebKit: an <img> aimed at a
// /library-preview/ URL that answered 302 → /api/v1/state was followed to
// /api/v1/state, and WebKit attached the session cookie. The confinement is
// therefore exactly as strong as "nothing under /library-preview/ ever answers
// with a redirect" — and nothing else would notice the day that stopped being
// true: the preview would still render, the policy would still be
// byte-identical, and the API would be reachable from untrusted HTML again.
//
// WHAT THIS DRIVES. The real production composition, not a stand-in:
//
//   - channels.Manager's own mux (pkg/channels dynamicServeMux), built by
//     SetupHTTPServer exactly as gateway.go builds it;
//   - the production preview registration, registerLibraryPreviewRoutes;
//   - the embedded SPA handler at "/" when this build carries one, registered as
//     gateway.go registers it. "/library-preview" without its slash lands there,
//     and CSP3 §6.7.2.10 lets the source "/library-preview/" admit that URL too;
//   - a sentinel at "/api/", standing in for the authenticated API, which no
//     request under the preview prefix may ever reach;
//   - the production middleware chain (buildProductionMiddlewareChain: CSRF
//     outermost, config snapshot inside).
//
// Requests go out over a raw TCP connection so that neither net/http's client
// nor url.URL normalises a single byte of the hostile request targets.
//
// POSITIVE CONTROL. The same harness against the standard library's
// *http.ServeMux, carrying the same preview registration, DOES see a redirect
// whose Location is /api/v1/state for a dot-segment path. That proves the
// harness can see a redirect and read where it points — and shows the router is
// load-bearing: the stdlib mux cleans paths and redirects, dynamicServeMux does
// not. (Browsers resolve "." and ".." segments, including their %2e spellings,
// before a request is sent, so that exact path never leaves a browser; it is in
// the corpus anyway, because the cost of pinning it is nothing.)

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// rawPreviewResponse is the part of a response this file asserts on.
type rawPreviewResponse struct {
	status   int
	location string
	csp      string
}

// sendRawPreviewRequest writes one HTTP/1.1 request with target sent verbatim
// as the request-target, and reads the response.
func sendRawPreviewRequest(t *testing.T, addr, method, target string, headers map[string]string) rawPreviewResponse {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(30*time.Second)))

	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", method, target, addr)
	for name, value := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", name, value)
	}
	if method == http.MethodPost || method == http.MethodPut {
		b.WriteString("Content-Length: 0\r\n")
	}
	b.WriteString("\r\n")
	_, err = io.WriteString(conn, b.String())
	require.NoError(t, err)

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
	require.NoError(t, err, "%s %s produced no parseable response", method, target)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return rawPreviewResponse{
		status:   resp.StatusCode,
		location: resp.Header.Get("Location"),
		csp:      resp.Header.Get("Content-Security-Policy"),
	}
}

// useFreshPreviewServeLimiter gives this test's traffic its own serving limiter.
//
// The shared one is keyed by client address. Several hundred requests from
// 127.0.0.1 would otherwise either trip it — turning the corpus into 429s that
// exercise nothing, which the non-vacuity checks below would then report as a
// failure — or consume another test's window. It must be called BEFORE the
// routes are registered: serveHandler reads the package variable at
// registration time.
func useFreshPreviewServeLimiter(t *testing.T) {
	t.Helper()
	previous := libraryPreviewServeLimiter
	libraryPreviewServeLimiter = newReadSizedAPIRateLimiter(100_000, time.Minute)
	t.Cleanup(func() { libraryPreviewServeLimiter = previous })
}

// newProductionPreviewChain builds the production composition described in the
// file header and serves it.
func newProductionPreviewChain(t *testing.T) (*previewFixture, *PreviewTokenStore, *httptest.Server, *atomic.Int64) {
	t.Helper()
	useFreshPreviewServeLimiter(t)

	f := newPreviewFixture(t)
	cm, err := channels.NewManager(f.api.agentLoop.GetConfig(), credentials.SecretBundle{}, bus.NewMessageBus(), nil)
	require.NoError(t, err)
	cm.SetupHTTPServer("127.0.0.1:0", nil)

	store := f.api.registerLibraryPreviewRoutes(cm)
	if spa := newSPAHandler(nil); spa != nil {
		cm.RegisterHTTPHandler("/", spa)
	}
	apiHits := &atomic.Int64{}
	cm.RegisterHTTPHandler("/api/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiHits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))

	// The manager does not export its mux; WrapHTTPHandler hands it to a
	// middleware, which is the same seam gateway.go wraps it through.
	var mux http.Handler
	require.NoError(t, cm.WrapHTTPHandler(func(inner http.Handler) http.Handler {
		mux = inner
		return inner
	}))
	require.NotNil(t, mux, "the manager's mux was not handed to the wrapper")

	srv := httptest.NewServer(buildProductionMiddlewareChain(f.api, mux))
	t.Cleanup(srv.Close)
	return f, store, srv, apiHits
}

// TestLibraryPreview_NothingUnderThePrefixRedirects is the tripwire.
//
// Every request below targets the preview prefix. None may answer 3xx, none
// may carry a Location header, and none may be dispatched to the API.
func TestLibraryPreview_NothingUnderThePrefixRedirects(t *testing.T) {
	f, store, srv, apiHits := newProductionPreviewChain(t)
	addr := srv.Listener.Addr().String()

	// A DIRECTORY whose name carries a web-asset extension. A bundle token
	// refuses an extension-less path before anything is opened (D-105), so
	// without this the handler's "a directory is not a document" branch — the
	// branch a file server traditionally answers with a redirect — is never
	// reached by any request below. Found by mutation: a redirect planted there
	// survived a corpus that lacked this directory.
	require.NoError(t, os.MkdirAll(filepath.Join(f.workDir, "site", "styles.css"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(f.workDir, "site", "styles.css", "inner.css"), []byte("p{}"), 0o600))

	const session = "c:no-redirect-tripwire"
	bundle, _, err := store.MintWithEntry(session, f.workspaceID, "site", PreviewScopeBundle, "site/index.html")
	require.NoError(t, err)
	file, _, err := store.MintWithEntry(session, f.workspaceID, "loose.txt", PreviewScopeFile, "")
	require.NoError(t, err)
	unknown := strings.Repeat("A", PreviewTokenEncodedLen)

	p := libraryPreviewPathPrefix
	targets := []string{
		// Live tokens, ordinary shapes.
		p + bundle + "/site/index.html",
		p + bundle + "/site/assets/app.js",
		p + bundle + "/site/assets/theme.css",
		p + bundle + "/site/assets/logo.svg",
		p + bundle + "/site/assets/body.woff2",
		p + file + "/loose.txt",
		// Directories, with and without the slash — the shape a file server
		// traditionally answers with a redirect.
		p + bundle + "/site",
		p + bundle + "/site/",
		p + bundle + "/site/assets",
		p + bundle + "/site/assets/",
		p + bundle + "/site/styles.css",
		p + bundle + "/site/styles.css/",
		p + bundle + "/site/styles.css/inner.css",
		p + bundle,
		p + bundle + "/",
		p,
		strings.TrimSuffix(p, "/"),
		// Refusals the route must answer without redirecting.
		p + bundle + "/site/escape.txt",
		p + bundle + "/site/host.txt",
		p + bundle + "/site/nope.html",
		p + bundle + "/site/data.bin",
		p + unknown + "/site/index.html",
		// Shapes a path-cleaning router turns into a redirect.
		p + bundle + "//site//index.html",
		p + "/api/v1/state",
		p + bundle + "/site/./index.html",
		p + bundle + "/site/../site/index.html",
		p + bundle + "/../../api/v1/state",
		p + "..",
		p + bundle + "/site/%2e%2e/secret.txt",
		p + "..%2f..%2fapi/v1/state",
		p + "%2e%2e/%2e%2e/api/v1/state",
		p + "%5c..%5capi/v1/state",
		"/library%2Dpreview/" + bundle + "/site/index.html",
		// A query that looks like a redirect instruction.
		p + bundle + "/site/index.html?next=/api/v1/state&redirect=/api/v1/state",
	}
	dests := []string{"", "document", "iframe", "image", "script", "style", "font", "video"}
	readMethods := []string{http.MethodGet, http.MethodHead}
	otherMethods := []string{http.MethodPost, http.MethodPut, http.MethodOptions}

	type observation struct {
		method, target, dest string
		resp                 rawPreviewResponse
	}
	var seen []observation
	for _, target := range targets {
		for _, method := range readMethods {
			for _, dest := range dests {
				headers := map[string]string{}
				if dest != "" {
					headers["Sec-Fetch-Dest"] = dest
				}
				seen = append(seen, observation{method, target, dest, sendRawPreviewRequest(t, addr, method, target, headers)})
			}
		}
		for _, method := range otherMethods {
			seen = append(seen, observation{method, target, "", sendRawPreviewRequest(t, addr, method, target, nil)})
		}
	}
	require.Len(t, seen, len(targets)*(len(readMethods)*len(dests)+len(otherMethods)),
		"every request in the corpus must have produced a response")

	for _, o := range seen {
		assert.False(t, o.resp.status >= 300 && o.resp.status < 400,
			"%s %s (Sec-Fetch-Dest %q) answered %d. A redirect under %s voids the 2026-09-14 path "+
				"confinement: CSP3 §6.7.2.9 ignores a source's path after a redirect, so the browser "+
				"follows it anywhere on the gateway — API included, with WebKit attaching the session "+
				"cookie", o.method, o.target, o.dest, o.resp.status, p)
		assert.Empty(t, o.resp.location,
			"%s %s (Sec-Fetch-Dest %q) wrote a Location header %q — a redirect in all but status",
			o.method, o.target, o.dest, o.resp.location)
	}
	assert.Zero(t, apiHits.Load(),
		"a request under %s was dispatched to the /api/ handler", p)

	// NON-VACUITY. "Nothing redirected" is also what a corpus that never reached
	// the preview handler would report, so the handler's own answers are pinned.
	find := func(method, target, dest string) rawPreviewResponse {
		for _, o := range seen {
			if o.method == method && o.target == target && o.dest == dest {
				return o.resp
			}
		}
		t.Fatalf("no observation for %s %s (Sec-Fetch-Dest %q)", method, target, dest)
		return rawPreviewResponse{}
	}
	framed := find(http.MethodGet, p+bundle+"/site/index.html", "iframe")
	require.Equal(t, http.StatusOK, framed.status,
		"the live bundle entry, framed, must be SERVED — otherwise the corpus exercised a refusal path only")
	assert.Equal(t, libraryIsolationPolicy(), framed.csp,
		"and it must carry the frozen isolation policy")
	assert.Equal(t, http.StatusOK, find(http.MethodGet, p+bundle+"/site/assets/app.js", "script").status,
		"a bundle asset must be served")
	assert.Equal(t, http.StatusOK, find(http.MethodGet, p+file+"/loose.txt", "").status,
		"a file token must be served")
	assert.Equal(t, http.StatusForbidden, find(http.MethodGet, p+bundle+"/site/index.html", "document").status,
		"the D-106 top-level refusal must have been exercised")
	assert.Equal(t, http.StatusNotFound, find(http.MethodGet, p+bundle+"/site/styles.css", "style").status,
		"the directory branch must have been reached and refused as not-a-document")
	assert.Equal(t, http.StatusOK, find(http.MethodGet, p+bundle+"/site/styles.css/inner.css", "style").status,
		"and the file inside that directory must be served, proving the path resolved")
	assert.Equal(t, http.StatusNotFound, find(http.MethodGet, p+bundle+"/site/escape.txt", "image").status,
		"the symlink escape must have been refused")
	assert.Equal(t, http.StatusNotFound, find(http.MethodGet, p+unknown+"/site/index.html", "iframe").status,
		"an unknown token must have been refused")
	assert.Equal(t, http.StatusMethodNotAllowed, find(http.MethodPost, p+bundle+"/site/index.html", "").status,
		"a non-read method must reach the route's own 405, past the CSRF middleware")

	// The sentinel is wired: a request that really targets the API reaches it.
	// Without this, apiHits == 0 above could mean the sentinel was never
	// registered.
	assert.Equal(t, http.StatusTeapot, sendRawPreviewRequest(t, addr, http.MethodGet, "/api/v1/state", nil).status,
		"the /api/ sentinel must answer a real API path")
	assert.Equal(t, int64(1), apiHits.Load(), "and count it")
}

// TestLibraryPreview_NoRedirectHarnessSeesAStdlibMuxRedirect is the positive
// control for the tripwire above: the same raw-request harness, the same
// preview registration and the same middleware chain, but the standard
// library's *http.ServeMux as the router. That mux cleans a dot-segment path
// and redirects to the cleaned one, so the harness MUST observe a redirect whose
// Location leaves the preview prefix for the API. If it did not, "nothing
// redirected" above would prove nothing about the harness's ability to see one.
func TestLibraryPreview_NoRedirectHarnessSeesAStdlibMuxRedirect(t *testing.T) {
	useFreshPreviewServeLimiter(t)
	f := newPreviewFixture(t)

	std := http.NewServeMux()
	store := f.api.registerLibraryPreviewRoutes(&testMuxRegistrar{mux: std})
	token, _, err := store.MintWithEntry("c:no-redirect-control", f.workspaceID, "site", PreviewScopeBundle, "site/index.html")
	require.NoError(t, err)

	srv := httptest.NewServer(buildProductionMiddlewareChain(f.api, std))
	t.Cleanup(srv.Close)

	resp := sendRawPreviewRequest(t, srv.Listener.Addr().String(), http.MethodGet,
		libraryPreviewPathPrefix+token+"/../../api/v1/state", map[string]string{"Sec-Fetch-Dest": "image"})
	require.True(t, resp.status >= 300 && resp.status < 400,
		"the stdlib mux must redirect a dot-segment path (got %d) — if it does not, the harness cannot "+
			"see redirects and the tripwire above is vacuous", resp.status)
	assert.Equal(t, "/api/v1/state", resp.location,
		"and the redirect leaves the preview prefix for the API: exactly the hole a path-cleaning router opens")
}
