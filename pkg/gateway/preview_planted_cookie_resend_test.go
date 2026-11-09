// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// ADR-094 S-4.4 / FR-015 (founder decision 2026-11-09, option B): after the
// planted-cookie clear lines, the gateway re-sends the ALREADY-VALIDATED
// genuine session cookie in the same response, so the genuine login survives
// the clear set in a real browser. It never mints a new session.
//
// Oracle (independent of the implementation): RFC 6265 §5.3 step 5 — a
// Domain attribute naming a public suffix identical to the request host makes
// the cookie HOST-ONLY. "localhost" is a public suffix (the list's default
// "*" rule), so on a gateway reached as http://localhost:<port> the FR-015
// "Domain=localhost; Path=/" clear line addresses the same jar entry as the
// genuine host-only Path=/ omnipus-session and deletes it (Chromium and
// Firefox do exactly this: /workspace/e2e-art/probes/cookie-probe-clear.log).
// net/http/cookiejar with the public-suffix list implements that rule, so the
// verdict is the jar's, not a reading of the Set-Cookie text.
//
// Unit boundary: the REAL production middleware chain and auth
// (piRedNewPlantedHarness), a real httptest server reached as "localhost",
// and a real RFC 6265 jar.

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/publicsuffix"

	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

// plantedResendJar is a real RFC 6265 jar plus a client pointed at the
// harness through host "localhost" (as a local install and the E2E shard
// reach the gateway), not the 127.0.0.1 the listener binds.
type plantedResendJar struct {
	base   *url.URL
	jar    *cookiejar.Jar
	client *http.Client
}

func newPlantedResendJar(t *testing.T, h *piRedPlantedHarness) *plantedResendJar {
	t.Helper()
	base, err := url.Parse(strings.Replace(h.srv.URL, "127.0.0.1", "localhost", 1))
	require.NoError(t, err)
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	require.NoError(t, err)
	return &plantedResendJar{base: base, jar: jar, client: &http.Client{Jar: jar}}
}

func (j *plantedResendJar) url(path string) *url.URL {
	u := *j.base
	u.Path = path
	return &u
}

// set stores a host-only cookie at cookiePath (no Domain attribute).
func (j *plantedResendJar) set(cookiePath, value string) {
	j.jar.SetCookies(j.url(cookiePath), []*http.Cookie{{Name: middleware.SessionCookieName, Value: value, Path: cookiePath}})
}

func (j *plantedResendJar) sessionValues(path string) []string {
	var vals []string
	for _, c := range j.jar.Cookies(j.url(path)) {
		if c.Name == middleware.SessionCookieName {
			vals = append(vals, c.Value)
		}
	}
	return vals
}

func (j *plantedResendJar) do(t *testing.T, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, j.url(path).String(), nil)
	require.NoError(t, err)
	resp, err := j.client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// nonEmptySessionSetCookies returns every omnipus-session Set-Cookie line on
// resp that carries a value (clear lines are empty-valued).
func nonEmptySessionSetCookies(t *testing.T, resp *http.Response) []*http.Cookie {
	t.Helper()
	var out []*http.Cookie
	for _, raw := range resp.Header.Values("Set-Cookie") {
		c, err := http.ParseSetCookie(raw)
		require.NoError(t, err, "every Set-Cookie line must parse: %q", raw)
		if c.Name == middleware.SessionCookieName && c.Value != "" {
			out = append(out, c)
		}
	}
	return out
}

// TestPreviewPlantedCookieResendKeepsGenuineSession — S-4.4 as a unit: the
// user is signed in, a preview planted omnipus-session at /api/, the SPA's
// next request carries both values and meets the guard; once the jar has
// applied the response, exactly the genuine session remains, at Path=/.
func TestPreviewPlantedCookieResendKeepsGenuineSession(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			h := piRedNewPlantedHarness(t)
			j := newPlantedResendJar(t, h)
			// The session was issued an hour ago, so its cookie expires at
			// issuedAt + 24 h (SessionCookieMaxAge).
			issuedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
			h.api.agentLoop.GetConfig().Gateway.Users[0].SessionIssuedAt = issuedAt.Format(time.RFC3339)
			wantExpiry := issuedAt.Add(middleware.SessionCookieMaxAge * time.Second)
			hashBefore := h.api.agentLoop.GetConfig().Gateway.Users[0].SessionTokenHash

			j.set("/", h.sessionTok)
			j.set("/api/", "PLANTED")
			require.Len(t, j.sessionValues("/api/v1/agents"), 2,
				"instrument check: an /api/ request must carry both values before the guard")

			resp := j.do(t, method, "/api/v1/agents")
			t.Cleanup(func() { _ = resp.Body.Close() })
			require.NotEmpty(t, resp.Header.Values("Set-Cookie"),
				"instrument check: the guard must have detected the duplicate and emitted clear lines")

			require.Equal(t, []string{h.sessionTok}, j.sessionValues("/api/v1/agents"),
				"FR-015/S-4.4: after the response is applied by an RFC 6265 jar, exactly the genuine "+
					"omnipus-session must remain for /api/ requests (empty = the genuine login was "+
					"deleted by the Domain=localhost; Path=/ clear line, host-only on host localhost)")
			require.Equal(t, []string{h.sessionTok}, j.sessionValues("/"),
				"FR-015: the genuine host-only Path=/ session must survive the clear")

			resent := nonEmptySessionSetCookies(t, resp)
			require.Len(t, resent, 1, "exactly one session cookie is re-sent")
			c := resent[0]
			require.Equal(t, h.sessionTok, c.Value, "the re-sent value is the genuine one, never a new session")
			require.Equal(t, "/", c.Path, "re-sent at the genuine cookie's Path=/")
			require.Empty(t, c.Domain, "re-sent host-only, like the original")
			require.True(t, c.HttpOnly, "re-sent HttpOnly, like the original")
			require.Equal(t, http.SameSiteStrictMode, c.SameSite, "re-sent SameSite=Strict, like the original")
			require.False(t, c.Secure, "Secure mirrors the plain-HTTP request posture, like the original")

			// Same expiry as the original: issue time + 24 h as an absolute
			// Expires, and NO Max-Age (a jar counts Max-Age from receipt, so
			// it could outlive the original — security review CWE-613).
			require.True(t, c.Expires.Equal(wantExpiry),
				"re-sent Expires must be the original expiry %s, got %s", wantExpiry, c.Expires)
			require.Zero(t, c.MaxAge, "the re-send carries no Max-Age attribute")

			// Order: the re-send comes after every clear line, so a browser
			// applying the lines in order ends with the genuine cookie.
			resendIdx, lastClearIdx := -1, -1
			for i, raw := range resp.Header.Values("Set-Cookie") {
				sc, err := http.ParseSetCookie(raw)
				require.NoError(t, err)
				if sc.Name != middleware.SessionCookieName {
					continue
				}
				if sc.Value == "" {
					lastClearIdx = i
				} else {
					resendIdx = i
				}
			}
			require.Greater(t, lastClearIdx, -1, "instrument check: omnipus-session clear lines are present")
			require.Greater(t, resendIdx, lastClearIdx, "the re-send comes after every omnipus-session clear line")

			require.Equal(t, hashBefore, h.api.agentLoop.GetConfig().Gateway.Users[0].SessionTokenHash,
				"no new session is minted: the stored session hash is unchanged")
		})
	}
}

// TestPreviewPlantedCookieResendNegatives — a value that does not validate is
// never re-sent, and no session is ever minted in its place.
func TestPreviewPlantedCookieResendNegatives(t *testing.T) {
	cases := []struct {
		name    string
		cookies string
	}{
		{"genuine_invalid", "omnipus-session=NOT-A-SESSION; omnipus-session=PLANTED"},
		{"genuine_absent_two_plants", "omnipus-session=PLANTED-1; omnipus-session=PLANTED-2"},
	}
	for _, tc := range cases {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(tc.name+"_"+method, func(t *testing.T) {
				h := piRedNewPlantedHarness(t)
				hashBefore := h.api.agentLoop.GetConfig().Gateway.Users[0].SessionTokenHash

				resp := h.piRedDoPlanted(t, method, "/api/v1/agents", tc.cookies, "", "")
				t.Cleanup(func() { _ = resp.Body.Close() })
				n := piRedAssertClearSet(t, resp, "omnipus-session", "/api/v1/agents", false)
				require.Equal(t, 15, n, "instrument check: the clear set is still emitted")
				require.Empty(t, nonEmptySessionSetCookies(t, resp),
					"no omnipus-session value is re-sent when none of the presented values validates")
				require.Equal(t, hashBefore, h.api.agentLoop.GetConfig().Gateway.Users[0].SessionTokenHash,
					"no new session is minted")
			})
		}
	}
}

// TestPreviewPlantedCookieResendNeedsIssueTime — the genuine value validates,
// but the session has no usable issue time, so its original expiry is unknown
// and it is NOT re-sent (founder decision 2026-11-09: such a user signs in
// once, as before). A session whose original expiry has already passed is not
// re-sent either: a re-send can never lengthen a session.
func TestPreviewPlantedCookieResendNeedsIssueTime(t *testing.T) {
	cases := []struct {
		name     string
		issuedAt string
	}{
		{"legacy_session_no_issue_time_not_resent", ""},
		{"unparseable_issue_time", "yesterday"},
		{"original_expiry_passed", time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := piRedNewPlantedHarness(t)
			h.api.agentLoop.GetConfig().Gateway.Users[0].SessionIssuedAt = tc.issuedAt
			hashBefore := h.api.agentLoop.GetConfig().Gateway.Users[0].SessionTokenHash

			resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/agents",
				"omnipus-session=PLANTED; omnipus-session="+h.sessionTok, "", "")
			t.Cleanup(func() { _ = resp.Body.Close() })
			n := piRedAssertClearSet(t, resp, "omnipus-session", "/api/v1/agents", false)
			require.Equal(t, 15, n, "instrument check: the clear set is still emitted")
			require.Empty(t, nonEmptySessionSetCookies(t, resp),
				"no re-send without a known, still-future original expiry")
			require.Equal(t, hashBefore, h.api.agentLoop.GetConfig().Gateway.Users[0].SessionTokenHash,
				"no new session is minted")
		})
	}
}

// TestPreviewPlantedCookieLegacySessionSignedOutOnce states the REAL behaviour
// for a session issued before session_issued_at existed: it is not re-sent,
// so on a planted-cookie event the Domain=localhost; Path=/ clear line
// (host-only on host localhost under RFC 6265, as in Chromium and Firefox)
// still deletes it — that user is signed out ONCE, exactly as before this
// change, and signs in again (which stores an issue time). A legacy session
// is at most 24 h old in the browser, so this lasts at most a day after the
// upgrade.
func TestPreviewPlantedCookieLegacySessionSignedOutOnce(t *testing.T) {
	h := piRedNewPlantedHarness(t)
	require.Empty(t, h.api.agentLoop.GetConfig().Gateway.Users[0].SessionIssuedAt,
		"instrument check: the harness session is a legacy one (no issue time)")
	j := newPlantedResendJar(t, h)
	j.set("/", h.sessionTok)
	j.set("/api/", "PLANTED")

	resp := j.do(t, http.MethodGet, "/api/v1/agents")
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Empty(t, nonEmptySessionSetCookies(t, resp), "a legacy session is never re-sent")
	require.Empty(t, j.sessionValues("/"),
		"real behaviour, unchanged from before: the legacy genuine session is deleted by the clear line")
}

// TestLegacySessionWithoutIssueTime_StillAuthenticates — upgrade case: a user
// record written before session_issued_at existed loads with the field empty
// and its session cookie keeps authenticating normally; nothing is re-sent or
// re-issued on an ordinary request.
func TestLegacySessionWithoutIssueTime_StillAuthenticates(t *testing.T) {
	h := piRedNewPlantedHarness(t)
	require.Empty(t, h.api.agentLoop.GetConfig().Gateway.Users[0].SessionIssuedAt,
		"instrument check: the harness session is a legacy one (no issue time)")

	resp := h.piRedDoPlanted(t, http.MethodGet, "/api/v1/agents", "omnipus-session="+h.sessionTok, "", "")
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode, "a legacy session cookie still authenticates")
	for _, raw := range resp.Header.Values("Set-Cookie") {
		c, err := http.ParseSetCookie(raw)
		require.NoError(t, err)
		require.NotEqual(t, middleware.SessionCookieName, c.Name,
			"an ordinary request neither clears nor re-issues the session cookie: %q", raw)
	}
}
