// Tests for the ADR-067 stage 1 preview-token route: the authenticated mint
// endpoint (POST /api/v1/library/preview-token) and the bare
// /library-preview/<token>/<relative-path> serving prefix.
//
// ORACLE DISCIPLINE. Every expected value below comes from the specification,
// not from what the handler happens to do:
//
//   - the Content-Security-Policy string is READ OUT OF THE SPEC FILE at test
//     time (§10.3's fenced block) and compared to the constant the handler
//     serves. A test carrying its own hand-typed copy of the policy asserts
//     only that two strings written by the same person agree.
//   - "GET, HEAD" and 405 come from FR-003j; 404-for-all-token-failures from
//     FR-003n; 900 seconds from FR-003d/§10.5; the inline/attachment split
//     from the §10.4 table.
//
// Traces to: FR-003a, FR-003c, FR-003f, FR-003i, FR-003j, FR-003m, FR-003n,
// FR-005, FR-005a, FR-005b, FR-006, FR-006a, FR-006b, FR-008a, FR-019; spec
// §13 tests 65, 66, 90, 113, 114, 116, 117.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// --- the policy string, read from the specification ------------------------

// specIsolationPolicy extracts §10.3's fenced policy block from the spec file
// and returns it verbatim.
//
// WHY THE TEST READS THE SPEC. §10.3 says the header must match "byte for
// byte" and reproduces the string precisely because "a nickname cannot be
// implemented and cannot be tested". A test that repeats the string inline
// would go green on a typo copied into both places at once — which is exactly
// how a policy loses a directive and nobody notices. Reading the source of
// truth makes the two independent.
//
// Failing to find the block is a test FAILURE, never a skip: a silently
// missing oracle is the false-green this suite is audited against.
func specIsolationPolicy(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "docs", "internal", "specs",
		"adr-067-knowledge-base-and-preview-spec.md"))
	require.NoError(t, err, "the ADR-067 spec must be readable — it is this test's oracle")

	// The §10.3 block is the only fenced line in the document that begins
	// with the sandbox directive.
	re := regexp.MustCompile("(?m)^sandbox allow-scripts;.*$")
	matches := re.FindAllString(string(raw), -1)
	require.Len(t, matches, 1,
		"§10.3 must contribute exactly one literal policy line to the spec; found %d", len(matches))
	policy := matches[0]
	require.NotEmpty(t, policy)
	return policy
}

// --- fixture ---------------------------------------------------------------

// previewFixture is one workspace holding one bundle, one loose file, and the
// two traps FR-003i exists for: a secret OUTSIDE the bundle but inside the
// workspace, and symlinks pointing at it and at the host filesystem.
type previewFixture struct {
	api         *restAPI
	workspaceID string
	routes      *libraryPreviewRoutes
	workDir     string
	outsideFile string
}

// previewSecretMarker is the content of every file the preview route must
// never return. Unique, so "the body does not contain it" is a real oracle
// rather than a coincidence.
const previewSecretMarker = "OMNIPUS-ADR067-CONTAINMENT-BREACH-MARKER"

func newPreviewFixture(t *testing.T) *previewFixture {
	t.Helper()
	api, wsID := buildLibraryTestAPI(t)
	work := workDir(api, wsID)
	require.NoError(t, os.MkdirAll(filepath.Join(work, "site", "assets"), 0o700))

	write := func(rel, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(work, filepath.FromSlash(rel)), []byte(body), 0o600))
	}
	write("site/index.html", "<!doctype html><p id=x>bundle entry</p>")
	write("site/assets/app.js", "document.getElementById('x').textContent='ran';")
	write("site/assets/theme.css", "body{color:#E2E8F0}")
	write("site/assets/logo.svg", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`)
	write("site/assets/body.woff2", "not-really-a-font-but-the-extension-is-what-decides")
	write("site/data.bin", "opaque bytes")
	write("loose.txt", "a loose file")
	// Inside the WORKSPACE, outside the BUNDLE. Reaching this through a
	// bundle token is the failure FR-003i names.
	write("secret.txt", previewSecretMarker)

	// A file outside the workspace entirely.
	outside := filepath.Join(t.TempDir(), "host-secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte(previewSecretMarker), 0o600))

	// The symlink the spec says the likely implementation follows: lexically
	// inside the bundle, resolving outside it.
	require.NoError(t, os.Symlink(
		filepath.Join("..", "secret.txt"),
		filepath.Join(work, "site", "escape.txt")))
	require.NoError(t, os.Symlink(outside, filepath.Join(work, "site", "host.txt")))

	return &previewFixture{
		api:         api,
		workspaceID: wsID,
		// newLibraryPreviewRoutes, not a hand-built literal: it is the
		// constructor that PUBLISHES the store on the restAPI, which is the
		// only way the FR-003d revocation events (logout, mount revoke, library
		// delete/move — all handled in other files) can reach it. A literal here
		// would leave every revocation test driving a store nothing revokes.
		routes:      newLibraryPreviewRoutes(api),
		workDir:     work,
		outsideFile: outside,
	}
}

// mint drives the real mint handler and returns the decoded response.
func (f *previewFixture) mint(t *testing.T, scope gen.LibraryPreviewTokenRequestScope, path string) gen.LibraryPreviewTokenResponse {
	t.Helper()
	resp, _ := f.mintWithBody(t, scope, path)
	return resp
}

// mintWithBody returns the decoded response AND the raw response bytes.
//
// The raw bytes matter for exactly one assertion — the RFC3339 UTC offset —
// and only because a decoded time.Time cannot carry it: on a UTC host, an
// instant that was never converted to UTC decodes with Location() == time.UTC
// anyway, so the decoded value agrees with a wrong implementation. The wire
// bytes do not.
func (f *previewFixture) mintWithBody(t *testing.T, scope gen.LibraryPreviewTokenRequestScope, path string) (gen.LibraryPreviewTokenResponse, []byte) {
	t.Helper()
	rec := f.mintRaw(t, map[string]any{
		"workspace_id": f.workspaceID,
		"path":         path,
		"scope":        string(scope),
	}, "session-alpha")
	require.Equal(t, http.StatusCreated, rec.Code, "mint failed: %s", rec.Body.String())
	raw := rec.Body.Bytes()
	var resp gen.LibraryPreviewTokenResponse
	require.NoError(t, json.Unmarshal(raw, &resp))
	return resp, raw
}

// mintedExpiresAtOnTheWire returns the raw serialised expires_at string.
func mintedExpiresAtOnTheWire(t *testing.T, rawBody []byte) string {
	t.Helper()
	var wire struct {
		ExpiresAt string `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal(rawBody, &wire))
	return wire.ExpiresAt
}

func assertMintedExpiresAtPresent(t *testing.T, rawBody []byte) {
	t.Helper()
	require.NotEmpty(t, mintedExpiresAtOnTheWire(t, rawBody),
		"FR-003m: the SPA drives its expiry notice from this timestamp; absent, the "+
			"reader gets a frame that stops working with no explanation")
}

// mintRaw posts an arbitrary body as the named session.
func (f *previewFixture) mintRaw(t *testing.T, body map[string]any, sessionValue string) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, libraryPreviewMintPath, bytes.NewReader(buf))
	if sessionValue != "" {
		r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sessionValue})
	}
	rec := httptest.NewRecorder()
	f.routes.handleMintPreviewToken(rec, r)
	return rec
}

// serve drives the real serving handler for a gateway-relative URL.
func (f *previewFixture) serve(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.routes.handleServeLibraryPreview(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// tokenURL builds a serving URL for a token and a workspace-relative path
// without going through the mint response, so a test can aim at a path the
// mint would never point at.
func tokenURL(token, rel string) string {
	return libraryPreviewPathPrefix + token + "/" + rel
}

// --- FR-005 / FR-005a / FR-006 — the literal policy, on every response ------

// TestLibraryPreview_PolicyIsTheSpecLiteralOnEveryResponse is spec test 90.
//
// Three properties, and the third is the one that is easy to lose:
//
//	(a) the served header equals §10.3 byte for byte;
//	(b) BOTH mechanisms are present — the sandbox directive AND the source
//	    directives. Measured on three engines, sandbox alone let five of seven
//	    egress vectors out and source directives alone let window.open out,
//	    so an implementation shipping either half satisfies neither FR-005 nor
//	    FR-006 while still passing any requirement that asks for "a policy";
//	(c) it is on EVERY response this route produces, errors included — a
//	    policy that appears only on the happy path leaves every failure frame
//	    unprotected.
func TestLibraryPreview_PolicyIsTheSpecLiteralOnEveryResponse(t *testing.T) {
	want := specIsolationPolicy(t)
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	// (a) + (b), asserted against the constant the handler serves.
	require.Equal(t, want, libraryIsolationPolicy,
		"FR-005a: the served policy must be §10.3's literal string, byte for byte")
	assert.Contains(t, want, "sandbox allow-scripts",
		"FR-005a: the sandbox half is required — source directives alone let window.open out")
	assert.Contains(t, want, "default-src 'none'",
		"FR-005a: the source-directive half is required — sandbox alone let five of seven vectors out")
	assert.Contains(t, want, "connect-src 'none'",
		"FR-006: no fetch, XHR, sendBeacon or WebSocket to any origin, the gateway's included")
	assert.NotContains(t, want, "allow-same-origin",
		"§10.3: granting allow-same-origin beside allow-scripts hands the page the session cookie")
	assert.NotContains(t, want, "frame-ancestors",
		"§10.3: frame-ancestors was never measured here — FR-006b puts the framing control on the SPA shell")

	// (c) every response shape this route can produce.
	responses := map[string]*httptest.ResponseRecorder{
		"served file":     f.serve(t, http.MethodGet, minted.Url),
		"HEAD":            f.serve(t, http.MethodHead, minted.Url),
		"unknown token":   f.serve(t, http.MethodGet, tokenURL(strings.Repeat("a", 43), "site/index.html")),
		"outside scope":   f.serve(t, http.MethodGet, tokenURL(minted.Token, "secret.txt")),
		"missing file":    f.serve(t, http.MethodGet, tokenURL(minted.Token, "site/nope.html")),
		"rejected method": f.serve(t, http.MethodPost, minted.Url),
		"attachment type": f.serve(t, http.MethodGet, tokenURL(minted.Token, "site/data.bin")),
	}
	for name, rec := range responses {
		assert.Equal(t, want, rec.Header().Get("Content-Security-Policy"),
			"%s: §10.3 must be carried byte for byte on this response too", name)
	}
}

// --- FR-003c / FR-015 — the other response-borne headers -------------------

// TestLibraryPreview_ReferrerPolicyAndNosniff is spec test 66.
//
// Referrer-Policy is not cosmetic here: the token is IN the URL, so any
// outbound navigation from a previewed page would otherwise hand the live
// credential to the destination in a Referer header.
func TestLibraryPreview_ReferrerPolicyAndNosniff(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"served file":   f.serve(t, http.MethodGet, minted.Url),
		"unknown token": f.serve(t, http.MethodGet, tokenURL(strings.Repeat("b", 43), "site/index.html")),
	} {
		assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"),
			"%s: FR-003c — the token must not leave in a Referer header", name)
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"),
			"%s: FR-015 — the type comes from the extension table, the browser must not second-guess it", name)
	}
}

// --- FR-003j — GET and HEAD only -------------------------------------------

// bodyReadRecorder counts how many times a request body is read.
type bodyReadRecorder struct {
	reads int
	data  *bytes.Reader
}

func (b *bodyReadRecorder) Read(p []byte) (int, error) {
	b.reads++
	return b.data.Read(p)
}

// TestPreviewTokenPath_VerbsGetHeadOnly is spec test 114.
//
// The spec says this must be asserted rather than assumed, and says why: a
// handler registered on a BARE PREFIX accepts every method by default. The
// prefix is CSRF-exempt (middleware.defaultExemptPrefixes) so that a
// cookie-less POST reaches this 405 with the §10.3 policy rather than the CSRF
// gate's policy-free 403 — an exemption that is only safe while the route
// really does answer nothing but GET and HEAD, so the property has to be
// asserted rather than argued. TestLibraryPreviewChain_NonGetVerbsGet405NotACsrf403
// asserts the same thing through the production middleware chain, which this
// direct call cannot see.
//
// The body-read count is the second half of FR-003j — "without reading a
// body". A handler that decoded the body before rejecting the method would
// pass a status-only assertion.
func TestPreviewTokenPath_VerbsGetHeadOnly(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	t.Run("accepted verbs", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := f.serve(t, method, minted.Url)
			require.Equal(t, http.StatusOK, rec.Code, "%s must be served", method)
			assert.Empty(t, rec.Header().Get("Allow"),
				"%s: a served response must not advertise an Allow header", method)
		}
	})

	t.Run("rejected verbs", func(t *testing.T) {
		for _, method := range []string{
			http.MethodPost, http.MethodPut, http.MethodPatch,
			http.MethodDelete, http.MethodOptions,
		} {
			body := &bodyReadRecorder{data: bytes.NewReader([]byte(`{"anything":"at all"}`))}
			r := httptest.NewRequest(method, minted.Url, body)
			rec := httptest.NewRecorder()
			f.routes.handleServeLibraryPreview(rec, r)

			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
				"FR-003j: %s must be 405 on the token path", method)
			assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"),
				"FR-003j: %s must carry the literal Allow value", method)
			assert.Zero(t, body.reads,
				"FR-003j: %s must be refused WITHOUT reading a body", method)
		}
	})

	t.Run("the verb gate runs before the token is consulted", func(t *testing.T) {
		// Otherwise a 405 would depend on holding a good token, and the
		// CSRF argument would only hold for authorised callers.
		rec := f.serve(t, http.MethodPost, tokenURL(strings.Repeat("z", 43), "site/index.html"))
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
	})
}

// --- FR-003i — containment at the syscall boundary -------------------------

// TestPreviewTokenPath_ContainedAtSyscall is spec test 113.
//
// Four refusals plus a positive control. The spec names the likely
// implementation and what it gets wrong: a filepath.Clean + Join design
// refuses the first three string-level cases and FOLLOWS THE SYMLINK, which
// is why rows 4 and 5 exist and why they point at a file inside the same
// workspace as well as outside it — confinement is to the TOKEN'S OWN SCOPE
// ROOT, not merely to the workspace.
//
// The oracle for every refusal is two-part: a 404 AND the absence of the
// secret marker from the response. A status-only assertion would pass against
// an implementation that returned the bytes with the wrong status.
func TestPreviewTokenPath_ContainedAtSyscall(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	t.Run("positive control: an ordinary nested file IS served", func(t *testing.T) {
		// Without this, "404 for everything" passes every refusal below.
		rec := f.serve(t, http.MethodGet, tokenURL(minted.Token, "site/assets/app.js"))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "getElementById",
			"the bundle's own script must actually be served")
	})

	for name, rel := range map[string]string{
		"traversal out of the bundle":       "site/../secret.txt",
		"percent-encoded traversal":         "site/%2e%2e/secret.txt",
		"absolute path":                     "/etc/passwd",
		"symlink to a workspace file":       "site/escape.txt",
		"symlink to the host filesystem":    "site/host.txt",
		"named directly outside the bundle": "secret.txt",
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.serve(t, http.MethodGet, tokenURL(minted.Token, rel))
			assert.Equal(t, http.StatusNotFound, rec.Code,
				"FR-003i: %q must be refused", rel)
			assert.NotContains(t, rec.Body.String(), previewSecretMarker,
				"FR-003i: %q returned content from outside the token's scope root", rel)
		})
	}

	t.Run("a file grant is exactly its own file", func(t *testing.T) {
		fileTok := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "loose.txt")
		ok := f.serve(t, http.MethodGet, tokenURL(fileTok.Token, "loose.txt"))
		require.Equal(t, http.StatusOK, ok.Code, "positive control: the granted file is served")
		assert.Contains(t, ok.Body.String(), "a loose file")

		sibling := f.serve(t, http.MethodGet, tokenURL(fileTok.Token, "secret.txt"))
		assert.Equal(t, http.StatusNotFound, sibling.Code,
			"FR-003b: a file grant covers one file, never its directory")
		assert.NotContains(t, sibling.Body.String(), previewSecretMarker)
	})
}

// --- FR-003n — expired, revoked and unknown are indistinguishable ----------

// TestPreviewToken_ExpiredResponseIsPolicyCarrying is spec test 117.
//
// The spec states the trap directly: "a 410-vs-404 split is a working oracle
// for whether a token ever existed". So the assertion is not merely that each
// is a 404 — it is that the three responses are IDENTICAL: same status, same
// content type, same body bytes, same policy. Comparing them to each other is
// what makes a future "helpful" 410-for-expired fail.
func TestPreviewToken_ExpiredResponseIsPolicyCarrying(t *testing.T) {
	want := specIsolationPolicy(t)

	// An injected clock, so the 15-minute boundary is crossed without sleeping.
	now := time.Now()
	clock := func() time.Time { return now }
	api, wsID := buildLibraryTestAPI(t)
	work := workDir(api, wsID)
	require.NoError(t, os.MkdirAll(filepath.Join(work, "site"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(work, "site", "index.html"), []byte("<p>hi</p>"), 0o600))

	routes := &libraryPreviewRoutes{
		api:    api,
		tokens: NewPreviewTokenStore(WithPreviewTokenClock(clock)),
	}
	f := &previewFixture{api: api, workspaceID: wsID, routes: routes, workDir: work}

	expired := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")
	revoked := f.mint(t, gen.LibraryPreviewTokenRequestScopeFile, "site/index.html")

	// Sanity: both work right now. Without this the three "refused" results
	// below could all be refusals for a reason that has nothing to do with
	// expiry or revocation.
	require.Equal(t, http.StatusOK, f.serve(t, http.MethodGet, expired.Url).Code)
	require.Equal(t, http.StatusOK, f.serve(t, http.MethodGet, revoked.Url).Code)

	require.True(t, routes.tokens.InvalidateToken(revoked.Token))
	now = now.Add(PreviewTokenTTL) // exactly at expiry: refused (see Lookup)

	cases := map[string]string{
		"expired": tokenURL(expired.Token, "site/index.html"),
		"revoked": tokenURL(revoked.Token, "site/index.html"),
		"unknown": tokenURL(strings.Repeat("q", PreviewTokenEncodedLen), "site/index.html"),
	}
	bodies := map[string]string{}
	for name, target := range cases {
		rec := f.serve(t, http.MethodGet, target)
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s: FR-003n requires 404, never 410", name)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"), "%s", name)
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), "%s", name)
		assert.Equal(t, want, rec.Header().Get("Content-Security-Policy"), "%s", name)
		assert.NotEmpty(t, rec.Body.String(),
			"%s: FR-003c — a blank frame is the failure this replaces", name)
		bodies[name] = rec.Body.String()
	}
	assert.Equal(t, bodies["expired"], bodies["unknown"],
		"FR-003n: expired and unknown must be indistinguishable")
	assert.Equal(t, bodies["revoked"], bodies["unknown"],
		"FR-003n: revoked and unknown must be indistinguishable")
}

// --- FR-003a / FR-003f / FR-005b — mint, and what it returns ---------------

// TestLibraryPreview_MintRoundTrip covers spec test 65's happy half and
// FR-003a end to end: the minted url, used verbatim, serves the bundle entry
// INLINE — which is the whole reason the token path exists, since a sandboxed
// document's own subresource requests carry neither cookie nor Authorization
// header.
func TestLibraryPreview_MintRoundTrip(t *testing.T) {
	f := newPreviewFixture(t)
	minted, mintedRaw := f.mintWithBody(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	assert.Len(t, minted.Token, PreviewTokenEncodedLen,
		"FR-003h/§10.5: 32 bytes base64url without padding is 43 characters")
	assert.Equal(t, libraryPreviewPathPrefix+minted.Token+"/site/index.html", minted.Url,
		"the url must point at the bundle's default entry document")
	assert.True(t, strings.HasPrefix(minted.Url, libraryPreviewPathPrefix),
		"FR-005b: the SPA needs a real URL for <iframe src>; srcdoc has no response to carry the policy")
	assert.Equal(t, "site", minted.ScopeRoot)
	assert.Equal(t, gen.LibraryPreviewTokenResponseScopeBundle, minted.Scope)
	require.NotNil(t, minted.WorkspaceId)
	assert.Equal(t, f.workspaceID, *minted.WorkspaceId)
	assert.Equal(t, int(PreviewTokenTTL/time.Second), minted.ExpiresInSeconds,
		"§10.5: the lifetime is 15 minutes — 900 seconds")
	assert.Equal(t, 900, minted.ExpiresInSeconds,
		"the named constant must still be the 15 minutes the spec fixed")
	// expires_at's UTC requirement is asserted by
	// TestLibraryPreview_ExpiresAtIsUTCOnTheWire, which injects a non-UTC clock.
	// It cannot be asserted here: this fixture's clock is time.Now, so on a UTC
	// host the value is already UTC whether or not the handler converts it, and
	// CI runs on UTC. What is checked here is only that the field is on the wire
	// at all.
	assertMintedExpiresAtPresent(t, mintedRaw)

	rec := f.serve(t, http.MethodGet, minted.Url)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, "inline", rec.Header().Get("Content-Disposition"),
		"§10.4: an allow-listed type is served INLINE on the token path, never as an attachment")
	assert.Contains(t, rec.Body.String(), "bundle entry")

	t.Run("an explicit entry_path is honoured", func(t *testing.T) {
		rec := f.mintRaw(t, map[string]any{
			"workspace_id": f.workspaceID,
			"path":         "site",
			"scope":        "bundle",
			"entry_path":   "assets/app.js",
		}, "session-alpha")
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var resp gen.LibraryPreviewTokenResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, libraryPreviewPathPrefix+resp.Token+"/site/assets/app.js", resp.Url)
	})

	t.Run("minting is authenticated", func(t *testing.T) {
		// FR-003b. A grant with no session behind it has nothing to revoke on
		// logout, which is the omission FR-003d says turns a token into a
		// standing unauthenticated read grant.
		rec := f.mintRaw(t, map[string]any{
			"workspace_id": f.workspaceID, "path": "site", "scope": "bundle",
		}, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("a token never widens access", func(t *testing.T) {
		// FR-003b: the path must be one the caller can already read. A path
		// that does not exist never becomes a token.
		rec := f.mintRaw(t, map[string]any{
			"workspace_id": f.workspaceID, "path": "site/not-here.html", "scope": "file",
		}, "session-alpha")
		assert.Equal(t, http.StatusNotFound, rec.Code)

		traversal := f.mintRaw(t, map[string]any{
			"workspace_id": f.workspaceID, "path": "../../etc/passwd", "scope": "file",
		}, "session-alpha")
		assert.Equal(t, http.StatusBadRequest, traversal.Code)

		wholeWorkspace := f.mintRaw(t, map[string]any{
			"workspace_id": f.workspaceID, "path": ".", "scope": "bundle",
		}, "session-alpha")
		assert.Equal(t, http.StatusBadRequest, wholeWorkspace.Code,
			"FR-003b: there is no whole-workspace scope")
	})
}

// TestLibraryPreview_MintRequestMatchesTheContract runs the mint handler with
// inbound schema validation ENABLED, so the request body is checked against
// the committed contracts/ schema rather than against the handler's own
// tolerance (Constraint #8).
//
// CHOOSING THE NEGATIVE BODY IS THE WHOLE DIFFICULTY, and the first version of
// this test got it wrong. It used `"scope":"whole-workspace"` — which the
// handler's own previewScopeFromWire switch 400s unconditionally, schema or no
// schema. Setting validateEnabled to a hard-coded false left the test passing:
// it was asserting the handler's tolerance, which is precisely what its doc
// comment claimed it was not doing.
//
// The body below is instead one the HANDLER would happily accept — encoding/json
// ignores unknown fields, and nothing downstream reads `unexpected` — and the
// SCHEMA refuses, because LibraryPreviewTokenRequest.yaml declares
// `additionalProperties: false`. So a 400 here can only have come from the
// committed contract.
func TestLibraryPreview_MintRequestMatchesTheContract(t *testing.T) {
	f := newPreviewFixture(t)
	cfg := f.api.agentLoop.GetConfig()
	cfg.Gateway.ValidateInbound = true

	ok := f.mintRaw(t, map[string]any{
		"workspace_id": f.workspaceID, "path": "site", "scope": "bundle",
	}, "session-alpha")
	require.Equal(t, http.StatusCreated, ok.Code,
		"a well-formed body must validate against LibraryPreviewTokenRequest: %s", ok.Body.String())

	undeclaredField := map[string]any{
		"workspace_id": f.workspaceID, "path": "site", "scope": "bundle",
		"unexpected": "a field the contract does not declare",
	}

	bad := f.mintRaw(t, undeclaredField, "session-alpha")
	require.Equal(t, http.StatusBadRequest, bad.Code,
		"LibraryPreviewTokenRequest declares additionalProperties: false, so this body is "+
			"contract-invalid; the handler itself would accept it: %s", bad.Body.String())
	assert.Contains(t, bad.Body.String(), "LibraryPreviewTokenRequest",
		"the refusal must name the schema, proving the CONTRACT rejected it rather than "+
			"some later handler check")

	t.Run("control: the same body is accepted with validation off", func(t *testing.T) {
		// This is what makes the assertion above load-bearing. If the handler
		// refused this body on its own, the test would pass with inbound
		// validation entirely disabled — which is exactly how the previous
		// version of it managed to assert nothing.
		off := newPreviewFixture(t)
		offCfg := off.api.agentLoop.GetConfig()
		offCfg.Gateway.ValidateInbound = false

		rec := off.mintRaw(t, map[string]any{
			"workspace_id": off.workspaceID, "path": "site", "scope": "bundle",
			"unexpected": "a field the contract does not declare",
		}, "session-alpha")
		require.Equal(t, http.StatusCreated, rec.Code,
			"with validation off the handler accepts this body — so the 400 above came from "+
				"the schema and nothing else: %s", rec.Body.String())
	})
}

// TestLibraryPreview_ExpiresAtIsUTCOnTheWire pins §10.5's RFC3339 UTC
// requirement with an oracle that does not depend on the machine's time zone.
//
// THIS IS THE MACHINE-DEPENDENT-ORACLE TRAP, and it is worth spelling out
// because the obvious two forms of this test both fail to fail:
//
//	assert.Equal(t, time.UTC, minted.ExpiresAt.Location())
//
// A time.Time marshals with a "Z" suffix whenever its offset is zero, and
// unmarshals such a value into time.UTC. On a UTC host — which is what CI runs
// — an unconverted local instant already has a zero offset, so this assertion
// agrees with a handler that never calls .UTC(). Measured: with .UTC() removed,
// the test failed under Asia/Jakarta and passed under TZ=UTC.
//
//	assert.True(t, strings.HasSuffix(raw, "Z"))
//
// Reading the serialised bytes instead is necessary but not sufficient: the
// bytes are produced from the same host-local instant, so they carry "Z" on a
// UTC host too. It moves the trap; it does not remove it.
//
// What removes it is controlling the input. The store's clock is injected and
// returns an instant in a fixed +07:00 zone, so the two implementations produce
// visibly different bytes on EVERY host: "…Z" with the conversion, "…+07:00"
// without it.
func TestLibraryPreview_ExpiresAtIsUTCOnTheWire(t *testing.T) {
	api, wsID := buildLibraryTestAPI(t)
	work := workDir(api, wsID)
	require.NoError(t, os.MkdirAll(work, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(work, "note.txt"), []byte("x"), 0o600))

	// A zone with a non-zero offset, chosen so the wrong answer is unmistakable
	// rather than merely different.
	zone := time.FixedZone("UTC+7", 7*60*60)
	fixed := time.Date(2026, 8, 23, 9, 30, 0, 0, zone)

	routes := &libraryPreviewRoutes{
		api:    api,
		tokens: NewPreviewTokenStore(WithPreviewTokenClock(func() time.Time { return fixed })),
	}
	f := &previewFixture{api: api, workspaceID: wsID, routes: routes, workDir: work}

	_, raw := f.mintWithBody(t, gen.LibraryPreviewTokenRequestScopeFile, "note.txt")
	got := mintedExpiresAtOnTheWire(t, raw)

	// Derived from the spec, not from the code: 15 minutes after the injected
	// instant, expressed in UTC. 09:30+07:00 + 15m == 02:45Z.
	assert.Equal(t, "2026-08-23T02:45:00Z", got,
		"§10.5 / the contract: expires_at is RFC3339 UTC. A +07:00 offset names the same "+
			"instant and still parses, so nothing downstream complains — it is simply not "+
			"what the contract says, and every consumer that does naive string work on it "+
			"is wrong by seven hours")
}

// --- FR-003m — re-minting rotates ------------------------------------------

// TestLibraryPreviewRoute_ReMintRotatesValue is spec test 116 at the ROUTE level
// (pkg/gateway/preview_token_test.go asserts the same property at the store
// level; this one proves the served path honours it).
//
// The spec names the precise copy-the-precedent mistake: pkg/agent's
// served_subdirs returns the SAME token string when the same directory is
// re-registered, so the credential survives as long as the tab is open —
// which passes any "renewal works" assertion while making the 15-minute
// lifetime meaningless. Both halves are asserted: a new value, AND the old
// one refused.
func TestLibraryPreviewRoute_ReMintRotatesValue(t *testing.T) {
	f := newPreviewFixture(t)
	first := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")
	require.Equal(t, http.StatusOK, f.serve(t, http.MethodGet, first.Url).Code)

	second := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")
	assert.NotEqual(t, first.Token, second.Token,
		"FR-003m: re-minting must return a NEW value")
	assert.Equal(t, http.StatusOK, f.serve(t, http.MethodGet, second.Url).Code,
		"the new token must work")
	assert.Equal(t, http.StatusNotFound, f.serve(t, http.MethodGet, first.Url).Code,
		"FR-003m: the previous token must be invalidated")
}

// --- FR-008 / FR-008a — the §10.4 allow-list decides the disposition -------

// TestLibraryPreview_DispositionFollowsTheAllowList covers US-2 AS-5 and
// FR-008a on the token path.
//
// .svg is on the list deliberately: the token path applies ONE policy to
// every byte it serves, so an SVG there gets the same opaque origin and zero
// egress an HTML document gets. .bin is not on the list and is served as an
// attachment — still policy-carrying, still correctly typed.
func TestLibraryPreview_DispositionFollowsTheAllowList(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	for _, tc := range []struct {
		rel         string
		contentType string
		inline      bool
	}{
		{"site/index.html", "text/html; charset=utf-8", true},
		{"site/assets/app.js", "text/javascript; charset=utf-8", true},
		{"site/assets/theme.css", "text/css; charset=utf-8", true},
		{"site/assets/logo.svg", "image/svg+xml", true},
		{"site/assets/body.woff2", "font/woff2", true},
		{"site/data.bin", libraryDefaultContentType, false},
	} {
		rec := f.serve(t, http.MethodGet, tokenURL(minted.Token, tc.rel))
		require.Equal(t, http.StatusOK, rec.Code, tc.rel)
		assert.Equal(t, tc.contentType, rec.Header().Get("Content-Type"),
			"%s: FR-015b — the type comes from the compiled-in table", tc.rel)
		if tc.inline {
			assert.Equal(t, "inline", rec.Header().Get("Content-Disposition"),
				"%s: §10.4 allow-listed types are served inline", tc.rel)
		} else {
			assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment",
				"%s: FR-008 — everything off the allow-list stays an attachment", tc.rel)
		}
	}
}

// --- FR-019 — fonts need CORS ----------------------------------------------

// TestLibraryPreview_FontsCarryAccessControlAllowOrigin covers FR-019.
//
// A sandboxed document has an OPAQUE origin, so its font fetch is
// cross-origin and arrives with "Origin: null". Without the header the
// browser discards the response and the page silently renders in a fallback
// face — the failure the experiment measured. The negative half matters as
// much: the header must not be sprayed across every response, or it stops
// being a decision.
func TestLibraryPreview_FontsCarryAccessControlAllowOrigin(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	font := f.serve(t, http.MethodGet, tokenURL(minted.Token, "site/assets/body.woff2"))
	require.Equal(t, http.StatusOK, font.Code)
	assert.Equal(t, "*", font.Header().Get("Access-Control-Allow-Origin"),
		"FR-019: a webfont in a sandboxed bundle does not resolve without this")

	page := f.serve(t, http.MethodGet, tokenURL(minted.Token, "site/index.html"))
	require.Equal(t, http.StatusOK, page.Code)
	assert.Empty(t, page.Header().Get("Access-Control-Allow-Origin"),
		"FR-019 names font responses; a blanket header is not the requirement")

	// The four §10.4 font extensions and nothing else.
	for _, ext := range []string{".woff2", ".woff", ".ttf", ".otf"} {
		assert.True(t, libraryPreviewNeedsCORS(ext), "%s is a §10.4 font", ext)
	}
	for _, ext := range []string{".css", ".js", ".html", ".png", ".svg", ""} {
		assert.False(t, libraryPreviewNeedsCORS(ext), "%s is not a font", ext)
	}
}

// --- FR-003g — the authenticated path is unchanged -------------------------

// TestLibraryPreview_AuthenticatedPathCarriesNoPolicy is the negative half of
// spec test 90. §10.5's two-URL table only means something if the two URLs
// really do behave differently: the authenticated route keeps attachment and
// carries no isolation policy, because it never produces a browser document.
func TestLibraryPreview_AuthenticatedPathCarriesNoPolicy(t *testing.T) {
	f := newPreviewFixture(t)
	rec := libGet(t, f.api, "/api/v1/library/"+f.workspaceID+"/download?path=site/index.html")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment",
		"FR-003g: the authenticated Library path keeps attachment, unchanged")
	assert.Empty(t, rec.Header().Get("Content-Security-Policy"),
		"§10.5: only the token path carries the isolation policy")
}

// --- FR-006a — the accepted residual rests on the cookie -------------------

// TestSessionCookie_IsSameSiteStrict covers FR-006a.
//
// FR-006a ACCEPTS that a previewed page can reach same-origin gateway paths,
// on ONE stated condition: those requests arrive UNAUTHENTICATED, because the
// document's origin is opaque (making the request cross-site) and the session
// cookie is SameSite=Strict. The spec says the condition must be asserted and
// not assumed — one edit to the cookie's mode turns an accepted residual into
// authenticated API calls issued by untrusted content, with no other symptom.
//
// Asserted through the real emitter, not by reading the source.
func TestSessionCookie_IsSameSiteStrict(t *testing.T) {
	rec := httptest.NewRecorder()
	middleware.WriteSessionCookie(rec, httptest.NewRequest(http.MethodGet, "/", nil), "a-token")

	cookies := (&http.Response{Header: rec.Header()}).Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == middleware.SessionCookieName {
			session = c
		}
	}
	require.NotNil(t, session, "the session cookie must actually be emitted")
	assert.Equal(t, http.SameSiteStrictMode, session.SameSite,
		"FR-006a: the whole accepted-residual argument rests on SameSite=Strict")
	assert.True(t, session.HttpOnly,
		"a previewed page must not be able to read the cookie even where scripting reaches it")
}

// --- FR-006b — the SPA shell refuses to be framed --------------------------

// TestSpaShell_RefusesFraming covers FR-006b.
//
// The measured §10.3 policy contains `frame-src 'self'`, so a previewed page
// MAY embed any gateway page — including the real SPA. The nested context
// reads no cookie, so this is interface deception rather than data
// disclosure, and the control belongs on the FRAMED resource: narrowing
// frame-src on the preview side instead would invalidate the three-engine
// measurement.
//
// NOT ASSERTED HERE, and deliberately: FR-019b's full §10.7 directive floor.
// §10.7 is explicitly "a proposal, not a measurement" and the spec forbids
// freezing it before a headed three-engine run. This test asserts only the
// framing control, which is a measured requirement in its own right.
func TestSpaShell_RefusesFraming(t *testing.T) {
	handler := newSPAHandler()
	require.NotNil(t, handler,
		"the embedded SPA must be present — skipping here would make this assertion vacuous")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'",
		"FR-006b: a previewed page could otherwise render genuine Omnipus chrome inside attacker content")
	assert.Len(t, rec.Header().Values("Content-Security-Policy"), 1,
		"two CSP headers are intersected, making the effective policy something neither string states")
}

// --- HEAD semantics ---------------------------------------------------------

// TestLibraryPreview_HeadSendsHeadersWithoutBody keeps HEAD honest: it must
// carry every header a GET carries — the policy included — and no body.
func TestLibraryPreview_HeadSendsHeadersWithoutBody(t *testing.T) {
	f := newPreviewFixture(t)
	minted := f.mint(t, gen.LibraryPreviewTokenRequestScopeBundle, "site")

	get := f.serve(t, http.MethodGet, minted.Url)
	head := f.serve(t, http.MethodHead, minted.Url)

	require.Equal(t, http.StatusOK, head.Code)
	assert.Empty(t, head.Body.String(), "HEAD must send no body")
	assert.Equal(t, get.Header().Get("Content-Type"), head.Header().Get("Content-Type"))
	assert.Equal(t, get.Header().Get("Content-Length"), head.Header().Get("Content-Length"),
		"HEAD must report the length the GET would have sent")
	assert.Equal(t, get.Header().Get("Content-Security-Policy"), head.Header().Get("Content-Security-Policy"))
}

// compile-time guard: bodyReadRecorder is an io.Reader.
var _ io.Reader = (*bodyReadRecorder)(nil)

// --- routing: the registration is real ------------------------------------

// TestLibraryPreviewRoutes_AreReachableOnTheRealMux drives the PRODUCTION
// registration — registerAdditionalEndpoints, the exact call gateway.go makes
// at boot — onto a real Go mux.
//
// Why this exists as its own test: every other test in this file calls the
// handlers directly. A handler that is perfectly correct and never registered
// passes all of them and serves nothing, which is the same shape of
// false-green as a test file that never runs.
//
// The two assertions are chosen to DISCRIMINATE, not merely to be true:
//
//   - the mint path answers 405 with "Allow: POST" for a GET. If it had fallen
//     through to the "/api/v1/library/" subtree dispatcher instead, that
//     dispatcher answers a one-segment path with a plain 404 — so 405-vs-404
//     is what proves the exact pattern outranks the subtree.
//   - the serving prefix answers text/html carrying the isolation policy. An
//     unregistered path on a bare Go mux answers 404 text/plain with no
//     policy at all, so the content type and the header are the discriminator
//     rather than the status code, which is 404 either way.
func TestLibraryPreviewRoutes_AreReachableOnTheRealMux(t *testing.T) {
	want := specIsolationPolicy(t)
	api := newTestRestAPIWithHome(t)
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	t.Run("the mint endpoint outranks the library subtree", func(t *testing.T) {
		// dev_mode_bypass lets withAuth through so the request reaches the
		// handler being routed to; the routing question is what is under test,
		// not the auth chain (which TestLibraryPreview_MintRoundTrip covers).
		bypassCfg := &config.Config{}
		bypassCfg.Gateway.DevModeBypass = true
		req := httptest.NewRequest(http.MethodGet, libraryPreviewMintPath, nil)
		req.Header.Set("Authorization", "Bearer dev-mode-bypass-sentinel")
		req = req.WithContext(context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, bypassCfg))

		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusMethodNotAllowed, rec.Code,
			"a 404 here means the request reached HandleLibrary, not the mint handler; body: %s",
			rec.Body.String())
		assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
	})

	t.Run("the serving prefix is registered bare", func(t *testing.T) {
		// No credentials of any kind: FR-003a's whole point is that a
		// sandboxed document's subresource request carries none.
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			tokenURL(strings.Repeat("m", PreviewTokenEncodedLen), "site/index.html"), nil))

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"),
			"an unrouted path answers text/plain — this must be OUR handler")
		assert.Equal(t, want, rec.Header().Get("Content-Security-Policy"))
	})

	t.Run("the serving prefix rejects state-changing verbs", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost,
			tokenURL(strings.Repeat("m", PreviewTokenEncodedLen), "site/index.html"), nil))
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
			"FR-003j: a bare prefix accepts every method by default, and the CSRF exemption "+
				"on this prefix is only safe while this 405 is what a non-GET request gets")
		assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
	})
}
