package gateway

// mail_preview_no_redirect_test.go - the mail twin of
// library_preview_no_redirect_test.go: the tripwire for the MC-38
// path-confinement condition. The mail CSP confines the gateway origin to
// /mail-preview/part/ and /mail-preview/img/ (and the html route is the page
// origin), so the confinement is exactly as strong as "nothing under
// /mail-preview/ ever answers with a redirect": CSP3 6.7.2.9 ignores a
// source's path after a redirect, so a single 302 under the prefix would let
// previewed HTML reach the authenticated API with the session cookie
// attached. Nothing else would notice the day that stopped being true: the
// preview still renders, the policy string is byte-identical.
//
// WHAT THIS DRIVES: the real production composition - channels.Manager's own
// mux built by SetupHTTPServer, the production registerMailPreviewRoutes, the
// embedded SPA at "/", a /api/ sentinel, and the production middleware chain
// - over a raw TCP harness that normalises nothing.
//
// POSITIVE CONTROL: the same harness over the stdlib mux DOES see the
// dot-segment redirect whose Location leaves the prefix for /api/v1/state.
import (
	"net/http"
	"net/http/httptest"
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

// useFreshMailPreviewServeLimiter gives this test's traffic its own serving
// limiter, before registration (serveHandler reads the package variable at
// registration time). Without the swap the corpus's 100+ requests from
// 127.0.0.1 would trip the 120/min limiter and turn the corpus into 429s.
func useFreshMailPreviewServeLimiter(t *testing.T) {
	t.Helper()
	previous := mailPreviewServeLimiter
	mailPreviewServeLimiter = newAPIRateLimiter(100_000, time.Minute)
	t.Cleanup(func() { mailPreviewServeLimiter = previous })
}

// newProductionMailPreviewChain builds the production composition and mints
// one live grant carrying a real sanitized-HTML page and an inline part.
func newProductionMailPreviewChain(t *testing.T) (*previewFixture, *mailPreviewTokenStore, string, *httptest.Server, *atomic.Int64) {
	t.Helper()
	useFreshMailPreviewServeLimiter(t)
	f := newPreviewFixture(t)
	cm, err := channels.NewManager(f.api.agentLoop.GetConfig(), credentials.SecretBundle{}, bus.NewMessageBus(), nil)
	require.NoError(t, err)
	cm.SetupHTTPServer("127.0.0.1:0", nil)
	f.api.registerMailPreviewRoutes(cm)
	store := f.api.mailPreviewTokenStoreOf()
	require.NotNil(t, store, "registration must publish the mail token store")
	html := `<p>hi</p><img src="` + mailPreviewPartPrefix + mailTokenPlaceholder + `/0">`
	token, merr := store.mint("c:mail-tripwire", mailPreviewGrant{
		WorkspaceID: f.workspaceID, AgentID: "agent-a", Folder: "inbox", Ref: "<t@x>",
		HTML: mailSanitizePreviewHTML(html, nil, nil), Inline: []mailPreviewInline{
			{ContentType: "image/png", Data: []byte{0x89, 'P', 'N', 'G'}},
		},
	})
	require.NoError(t, merr, "mint must succeed for the tripwire corpus")

	if spa := newSPAHandler(nil); spa != nil {
		cm.RegisterHTTPHandler("/", spa)
	}
	apiHits := &atomic.Int64{}
	cm.RegisterHTTPHandler("/api/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		apiHits.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))

	var mux http.Handler
	require.NoError(t, cm.WrapHTTPHandler(func(inner http.Handler) http.Handler {
		mux = inner
		return inner
	}))
	require.NotNil(t, mux, "the manager's mux was not handed to the wrapper")
	srv := httptest.NewServer(buildProductionMiddlewareChain(f.api, mux))
	t.Cleanup(srv.Close)
	return f, store, token, srv, apiHits
}

// TestMailPreview_NothingUnderThePrefixRedirects is the mail tripwire. Every
// request below targets the prefix. None may answer 3xx, none may carry a
// Location header, and none may reach the API sentinel.
func TestMailPreview_NothingUnderThePrefixRedirects(t *testing.T) {
	f, _, token, srv, apiHits := newProductionMailPreviewChain(t)
	addr := srv.Listener.Addr().String()
	unknown := strings.Repeat("A", 43)
	p := mailPreviewPathPrefix
	targets := []string{
		// Live tokens, ordinary shapes.
		p + "html/" + token,
		p + "part/" + token + "/0",
		p + "img/" + token + "/0",
		// Unknown and expired-equivalent shapes.
		p + "html/" + unknown,
		p + "part/" + unknown + "/0",
		p + "img/" + unknown + "/0",
		p + "html/" + token + "/0",
		p + "part/" + token,
		p + "part/" + token + "/x",
		p + "part/" + token + "/-1",
		// The prefix itself, with and without the slash.
		p,
		strings.TrimSuffix(p, "/"),
		// Shapes a path-cleaning router turns into a redirect.
		p + "html/" + token + "//0",
		p + "html/" + token + "/./0",
		p + "html/" + token + "/../0",
		p + "html/" + token + "/../../api/v1/state",
		p + "../" + "api/v1/state",
		p + "..%2f..%2fapi/v1/state",
		p + "%2e%2e/%2e%2e/api/v1/state",
		p + "%5c..%5capi/v1/state",
		"/mail%2Dpreview/html/" + token,
		// A query that looks like a redirect instruction.
		p + "html/" + token + "?next=/api/v1/state&redirect=/api/v1/state",
	}
	methods := []string{http.MethodGet, http.MethodHead, http.MethodPost}
	type observation struct {
		method, target string
		resp           rawPreviewResponse
	}
	seen := make([]observation, 0, len(targets)*len(methods))
	for _, target := range targets {
		for _, method := range methods {
			seen = append(seen, observation{method, target, sendRawPreviewRequest(t, addr, method, target, nil)})
		}
	}
	require.Len(t, seen, len(targets)*len(methods),
		"every request in the corpus must have produced a response")

	expectedCSP := mailIsolationPolicy(mailPreviewCanonicalOrigin(f.api))
	for _, o := range seen {
		assert.False(t, o.resp.status >= 300 && o.resp.status < 400,
			"%s %s answered %d. A redirect under %s voids the MC-38 path confinement: the browser "+
				"follows it anywhere on the gateway, API included, with the session cookie attached",
			o.method, o.target, o.resp.status, p)
		assert.Empty(t, o.resp.location,
			"%s %s wrote a Location header %q - a redirect in all but status",
			o.method, o.target, o.resp.location)
		if o.method != http.MethodPost {
			assert.NotEmpty(t, o.resp.csp,
				"%s %s answered without the CSP header - refusals carry the policy too (MC-10)",
				o.method, o.target)
		}
		// POSTs are excluded from the CSP pin: the CSRF boundary owns the
		// policy-free 403 for the slash-less spelling (prefix matching needs
		// the trailing slash), exactly as for /library-preview/ - and the
		// sandboxed page cannot issue a POST at all (form-action 'none',
		// connect-src 'none'), so no response it could provoke misses the set.
	}
	assert.Zero(t, apiHits.Load(),
		"a request under %s was dispatched to the /api/ handler", p)

	// NON-VACUITY. "Nothing redirected, all 404" is also what a corpus that
	// never reached the mail routes would report, so the live paths are pinned.
	find := func(method, target string) rawPreviewResponse {
		for _, o := range seen {
			if o.method == method && o.target == target {
				return o.resp
			}
		}
		t.Fatalf("no observation for %s %s", method, target)
		return rawPreviewResponse{}
	}
	live := find(http.MethodGet, p+"html/"+token)
	require.Equal(t, http.StatusOK, live.status,
		"the live html token must be SERVED - otherwise the corpus exercised refusals only")
	assert.Contains(t, live.csp, "sandbox ", "the policy must be the mail isolation policy")
	assert.Equal(t, expectedCSP, live.csp)
	part := find(http.MethodGet, p+"part/"+token+"/0")
	require.Equal(t, http.StatusOK, part.status,
		"a live inline part must be served")
	unknownResp := find(http.MethodGet, p+"html/"+unknown)
	assert.Equal(t, http.StatusNotFound, unknownResp.status,
		"an unknown token must be refused with the bare 404 (MC-43 indistinguishability)")

	// The sentinel is wired: a request that really targets the API reaches it.
	assert.Equal(t, http.StatusTeapot, sendRawPreviewRequest(t, addr, http.MethodGet, "/api/v1/state", nil).status,
		"the /api/ sentinel must answer a real API path")
	assert.Equal(t, int64(1), apiHits.Load(), "and count it")
}

// TestMailPreview_NoRedirectHarnessSeesAStdlibMuxRedirect is the positive
// control: the same harness, same registration, stdlib mux. That mux cleans a
// dot-segment path and redirects to the cleaned one, so the harness MUST see
// a redirect whose Location leaves the prefix for the API.
func TestMailPreview_NoRedirectHarnessSeesAStdlibMuxRedirect(t *testing.T) {
	useFreshMailPreviewServeLimiter(t)
	f := newPreviewFixture(t)
	std := http.NewServeMux()
	f.api.registerMailPreviewRoutes(&testMuxRegistrar{mux: std})
	store := f.api.mailPreviewTokenStoreOf()
	require.NotNil(t, store)
	token, merr := store.mint("c:mail-ctl", mailPreviewGrant{HTML: "<p>x</p>", Inline: nil, RemoteURLs: nil})
	require.NoError(t, merr)

	srv := httptest.NewServer(buildProductionMiddlewareChain(f.api, std))
	t.Cleanup(srv.Close)
	resp := sendRawPreviewRequest(t, srv.Listener.Addr().String(), http.MethodGet,
		mailPreviewPathPrefix+"html/"+token+"/../../../api/v1/state", nil)
	require.True(t, resp.status >= 300 && resp.status < 400,
		"the stdlib mux must redirect a dot-segment path (got %d) - if it does not, the harness cannot "+
			"see redirects and the tripwire above is vacuous", resp.status)
	assert.Equal(t, "/api/v1/state", resp.location,
		"and the redirect leaves the prefix for the API: exactly the hole a path-cleaning router opens")
}
