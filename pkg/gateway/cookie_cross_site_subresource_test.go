// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Layer 2 of FIX4 preview-cookie-residual (2026-09-14), proven through the live
// gateway auth paths rather than the middleware helper alone.
//
// middleware/session_cookie_cross_site_test.go pins the rule:
// ResolveUserFromCookie ignores the omnipus-session cookie on a request marked
// Sec-Fetch-Site: cross-site with a subresource Sec-Fetch-Dest. This file proves
// the gateway's cookie-auth call sites actually inherit it — checkBearerAuth
// (every withAuth route) and withOptionalAuth — and that a same-origin request
// carrying the same valid cookie is unaffected on each. A rule that only the
// helper's own tests exercise would be the documented-but-unwired shape.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

const crossSiteGatewaySessionToken = "the-real-session-token-for-cross-site-tests"

func crossSiteGatewayConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Gateway: config.GatewayConfig{
			Users: []config.UserConfig{
				{Username: "real-user", SessionTokenHash: mustBcryptHash(t, crossSiteGatewaySessionToken)},
			},
			AuthMismatchLogLevel: "warn",
		},
	}
}

func crossSiteGatewayRequest(site, dest string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	r.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: crossSiteGatewaySessionToken})
	if site != "" {
		r.Header.Set("Sec-Fetch-Site", site)
	}
	if dest != "" {
		r.Header.Set("Sec-Fetch-Dest", dest)
	}
	return r
}

// TestCheckBearerAuth_IgnoresSessionCookieOnCrossSiteSubresource is the
// withAuth path: an authenticated API GET that a framed preview's <img> could
// aim at, carrying a VALID session cookie the way WebKit sends it.
func TestCheckBearerAuth_IgnoresSessionCookieOnCrossSiteSubresource(t *testing.T) {
	cfg := crossSiteGatewayConfig(t)

	// POSITIVE CONTROL: the same cookie authenticates the SPA's own same-origin
	// image request. Without it, the 401 below could just be a bad cookie.
	okRec := httptest.NewRecorder()
	ok := checkBearerAuth(context.Background(), okRec, crossSiteGatewayRequest("same-origin", "image"), cfg)
	require.True(t, ok.Authenticated, "the valid session cookie must authenticate a same-origin request")
	require.NotNil(t, ok.User)
	require.Equal(t, "real-user", ok.User.Username)

	recorder := installCookieAuthLogRecorder(t)
	for _, dest := range []string{"image", "script", "style", "iframe"} {
		t.Run(dest, func(t *testing.T) {
			w := httptest.NewRecorder()
			got := checkBearerAuth(context.Background(), w, crossSiteGatewayRequest("cross-site", dest), cfg)
			assert.False(t, got.Authenticated,
				"a cross-site %s request must not be authenticated by the session cookie — this is the request "+
					"WebKit sends from a framed preview (ADR-067 D15.8)", dest)
			assert.Nil(t, got.User)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
	assert.True(t, recorder.contains("ignored on a cross-site subresource request"),
		"the refusal must be logged under its own name")
	assert.False(t, recorder.contains("cookie present but invalid"),
		"and must not be logged as an invalid cookie: the cookie is valid")

	// Top-level navigation is deliberately outside the rule.
	navRec := httptest.NewRecorder()
	nav := checkBearerAuth(context.Background(), navRec, crossSiteGatewayRequest("cross-site", "document"), cfg)
	assert.True(t, nav.Authenticated, "a top-level document request keeps cookie auth")
}

// TestWithOptionalAuth_CrossSiteSubresourceCookieIsAnonymous is the optional
// auth path: the handler still runs, but with no user in context.
func TestWithOptionalAuth_CrossSiteSubresourceCookieIsAnonymous(t *testing.T) {
	cfg := crossSiteGatewayConfig(t)
	a := &restAPI{}
	var seen []*config.UserConfig
	handler := a.withOptionalAuth(func(w http.ResponseWriter, r *http.Request) {
		user, _ := r.Context().Value(UserContextKey{}).(*config.UserConfig)
		seen = append(seen, user)
		w.WriteHeader(http.StatusOK)
	})

	serve := func(site, dest string) {
		r := crossSiteGatewayRequest(site, dest)
		r = r.WithContext(context.WithValue(r.Context(), configContextKey{}, cfg))
		handler(httptest.NewRecorder(), r)
	}
	serve("same-origin", "image")
	serve("cross-site", "image")

	require.Len(t, seen, 2, "the optional-auth handler must run for both requests")
	require.NotNil(t, seen[0], "positive control: a same-origin request carries the cookie's user")
	assert.Equal(t, "real-user", seen[0].Username)
	assert.Nil(t, seen[1],
		"a cross-site subresource request must reach an optional-auth handler as anonymous, not as the user")
}
