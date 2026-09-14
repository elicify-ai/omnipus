// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// Layer 2 of FIX4 preview-cookie-residual (2026-09-14): the omnipus-session
// cookie does not authenticate a CROSS-SITE SUBRESOURCE request.
//
// WHY. ADR-067 D15.8 measured WebKit attaching the SameSite=Strict session
// cookie to a framed preview's subresource requests, while itself labelling them
// Sec-Fetch-Site: cross-site — in every framed shape, the product's included.
// Chromium and Firefox withhold it everywhere. SameSite cannot help, because
// site-for-cookies is computed from the top-level page and the top-level page is
// Omnipus. Layer 1 (pkg/gateway/library_isolation_policy.go) stops a preview
// reaching the API whenever an origin is known; this layer covers what Layer 1
// cannot — the 'self' fallback on a wildcard bind with no gateway.public_url —
// by refusing to treat such a request's cookie as a credential at all.
//
// WHY IT BREAKS NOTHING LEGITIMATE. The cookie is SameSite=Strict, so Chromium
// and Firefox never send it on a cross-site request: any flow relying on a
// cross-site subresource carrying it already fails on two engines of three. The
// SPA's own subresources are same-origin (measured 2026-09-14:
// Sec-Fetch-Site: same-origin on all three engines), a top-level navigation is
// Sec-Fetch-Dest: document, fetch and XHR are "empty", a WebSocket handshake is
// "websocket", and the Library preview frame itself is a same-origin "iframe" —
// none of which the rule touches. A request with no Fetch Metadata at all (a
// CLI, an older browser, a proxy that strips the headers) is unaffected.

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// crossSitePlaintext is a valid session token for the test user. Validity is
// the point: every refusal below is of a cookie that WOULD authenticate on any
// other request, which is what separates "ignored" from "invalid".
const crossSitePlaintext = "session-token-alice-cross-site-0123456789ab"

// crossSiteSubresourceDestsFromRule is the rule's destination list, typed from
// the fix's written rule rather than read off the implementation: the
// subresource destinations (image, style, script, font, audio, video, track,
// object, embed) plus the two nested-navigation destinations (iframe, frame),
// which a preview under the 'self' fallback could otherwise aim at the API.
var crossSiteSubresourceDestsFromRule = []string{
	"image", "style", "script", "font", "audio", "video", "track", "object", "embed",
	"iframe", "frame",
}

// fetchMetadataRequest builds a GET to an authenticated API path carrying the
// given Fetch Metadata and, when cookieValue is non-empty, the session cookie.
func fetchMetadataRequest(site, dest, cookieValue string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	if site != "" {
		r.Header.Set("Sec-Fetch-Site", site)
	}
	if dest != "" {
		r.Header.Set("Sec-Fetch-Dest", dest)
	}
	if cookieValue != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: cookieValue})
	}
	return r
}

func TestResolveUserFromCookie_IgnoredOnACrossSiteSubresourceRequest(t *testing.T) {
	users := []config.UserConfig{buildUserWithSessionHash(t, "alice", crossSitePlaintext)}

	// POSITIVE CONTROL: the very same cookie authenticates when no Fetch
	// Metadata is present. Without it, every refusal below could just mean the
	// cookie never matched.
	user, err := ResolveUserFromCookie(fetchMetadataRequest("", "", crossSitePlaintext), users)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, "alice", user.Username)

	for _, dest := range crossSiteSubresourceDestsFromRule {
		t.Run(dest, func(t *testing.T) {
			r := fetchMetadataRequest("cross-site", dest, crossSitePlaintext)
			assert.True(t, IsCrossSiteSubresourceRequest(r),
				"Sec-Fetch-Site: cross-site with Sec-Fetch-Dest: %s is a cross-site subresource request", dest)

			user, err := ResolveUserFromCookie(r, users)
			assert.Nil(t, user,
				"a VALID session cookie on a cross-site %s request must not authenticate: this is the request "+
					"WebKit sends from a framed preview, cookie attached (ADR-067 D15.8)", dest)
			assert.ErrorIs(t, err, ErrSessionCookieCrossSiteSubresource,
				"the refusal must be distinguishable from an invalid cookie, for the log and for callers")
			assert.ErrorIs(t, err, ErrSessionNotFound,
				"and it must wrap ErrSessionNotFound, so every existing caller's no-usable-cookie handling "+
					"(401 on withAuth, anonymous on optional auth, frame auth on the WebSockets) applies unchanged")
		})
	}

	// Fetch Metadata values are lower-case tokens, but a proxy or a test client
	// may not keep them that way. Case and surrounding space must not reopen it.
	for _, pair := range [][2]string{{"Cross-Site", "Image"}, {" cross-site ", " script "}, {"CROSS-SITE", "IFRAME"}} {
		r := fetchMetadataRequest(pair[0], pair[1], crossSitePlaintext)
		user, err := ResolveUserFromCookie(r, users)
		assert.Nil(t, user, "site %q dest %q must be ignored like its lower-case form", pair[0], pair[1])
		assert.ErrorIs(t, err, ErrSessionCookieCrossSiteSubresource)
	}
}

func TestResolveUserFromCookie_HonouredOutsideTheCrossSiteSubresourceRule(t *testing.T) {
	users := []config.UserConfig{buildUserWithSessionHash(t, "alice", crossSitePlaintext)}

	cases := []struct {
		name, site, dest string
	}{
		{"no Fetch Metadata (CLI, older browser, stripping proxy)", "", ""},
		{"the SPA's own image (measured same-origin on all three engines)", "same-origin", "image"},
		{"a same-origin script", "same-origin", "script"},
		{"a same-site subresource", "same-site", "image"},
		{"a user-typed navigation", "none", "document"},
		{"a same-origin top-level navigation", "same-origin", "document"},
		{"a cross-site top-level navigation (kept unaffected by design)", "cross-site", "document"},
		{"a same-origin fetch or XHR", "same-origin", "empty"},
		{"a cross-site fetch (connect-src 'none' and SameSite cover it)", "cross-site", "empty"},
		{"a WebSocket handshake", "same-origin", "websocket"},
		{"a same-origin worker", "same-origin", "worker"},
		{"the Library preview frame itself", "same-origin", "iframe"},
		{"cross-site with no destination", "cross-site", ""},
		{"a subresource destination with no site", "", "image"},
		{"an unknown site value", "cross-origin", "image"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := fetchMetadataRequest(tc.site, tc.dest, crossSitePlaintext)
			assert.False(t, IsCrossSiteSubresourceRequest(r))
			user, err := ResolveUserFromCookie(r, users)
			require.NoError(t, err, "the session cookie must still authenticate here")
			require.NotNil(t, user)
			assert.Equal(t, "alice", user.Username)
		})
	}
}

// TestResolveUserFromCookie_CrossSiteSubresourceWithoutCookieIsPlainNotFound
// keeps the two "no user" answers apart. A request that carries no cookie at
// all is the routine case and must not be reported as a refused cookie.
func TestResolveUserFromCookie_CrossSiteSubresourceWithoutCookieIsPlainNotFound(t *testing.T) {
	users := []config.UserConfig{buildUserWithSessionHash(t, "alice", crossSitePlaintext)}
	user, err := ResolveUserFromCookie(fetchMetadataRequest("cross-site", "image", ""), users)
	assert.Nil(t, user)
	assert.ErrorIs(t, err, ErrSessionNotFound)
	assert.NotErrorIs(t, err, ErrSessionCookieCrossSiteSubresource)
}

// TestLogInvalidSessionCookiePresent_NamesTheCrossSiteSubresourceCase pins the
// observability half. The refused cookie may be perfectly valid, so logging it
// as "present but invalid" would send an operator chasing a replay attack that
// is not happening, while hiding the thing that is.
func TestLogInvalidSessionCookiePresent_NamesTheCrossSiteSubresourceCase(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{AuthMismatchLogLevel: "warn"}}

	rec := &slogRecorder{}
	old := slog.Default()
	slog.SetDefault(slog.New(rec))
	t.Cleanup(func() { slog.SetDefault(old) })

	LogInvalidSessionCookiePresent(fetchMetadataRequest("cross-site", "image", crossSitePlaintext), cfg)
	require.Len(t, rec.records, 1, "a refused cross-site subresource cookie must be logged exactly once")
	assert.Contains(t, rec.records[0].Message, "ignored on a cross-site subresource request")
	assert.NotContains(t, rec.records[0].Message, "cookie present but invalid")

	// POSITIVE CONTROL: an invalid cookie on an ordinary request still gets the
	// original message, so the branch above is not simply a renamed log line.
	rec.records = nil
	LogInvalidSessionCookiePresent(fetchMetadataRequest("same-origin", "empty", "not-a-real-token"), cfg)
	require.Len(t, rec.records, 1)
	assert.Contains(t, rec.records[0].Message, "cookie present but invalid")

	// And no cookie at all stays silent, cross-site subresource or not.
	rec.records = nil
	LogInvalidSessionCookiePresent(fetchMetadataRequest("cross-site", "image", ""), cfg)
	assert.Empty(t, rec.records)
}

// TestRequireSessionCookieOrBearer_CrossSiteSubresourceCookieIs401 drives the
// middleware form of cookie auth end to end: the same valid cookie is admitted
// on a same-origin request and refused on a cross-site subresource one.
func TestRequireSessionCookieOrBearer_CrossSiteSubresourceCookieIs401(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{
		Users:                []config.UserConfig{buildUserWithSessionHash(t, "alice", crossSitePlaintext)},
		AuthMismatchLogLevel: "debug",
	}}
	reached := 0
	h := RequireSessionCookieOrBearer(func() *config.Config { return cfg })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached++
			w.WriteHeader(http.StatusOK)
		}))

	sameOrigin := httptest.NewRecorder()
	h.ServeHTTP(sameOrigin, fetchMetadataRequest("same-origin", "image", crossSitePlaintext))
	require.Equal(t, http.StatusOK, sameOrigin.Code, "positive control: the SPA's own image request is admitted")

	crossSite := httptest.NewRecorder()
	h.ServeHTTP(crossSite, fetchMetadataRequest("cross-site", "image", crossSitePlaintext))
	assert.Equal(t, http.StatusUnauthorized, crossSite.Code,
		"a framed preview's image request must not be admitted on the strength of the session cookie")
	assert.Equal(t, 1, reached, "the protected handler must run for the same-origin request only")
}
