// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// FR-015 genuine-session re-send (founder decision 2026-11-09): the refusal
// branches of genuineSessionResend that the gateway-level tests
// (pkg/gateway/preview_planted_cookie_resend_test.go) cannot reach with a
// single-account harness.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func mintUser(t *testing.T, name string, issuedAt time.Time) (string, config.UserConfig) {
	t.Helper()
	tok, hash, err := MintSessionToken()
	require.NoError(t, err)
	return tok, config.UserConfig{
		Username:         name,
		SessionTokenHash: config.BcryptHash(hash),
		SessionIssuedAt:  FormatSessionIssuedAt(issuedAt),
	}
}

func TestGenuineSessionResend_RefusalBranches(t *testing.T) {
	now := time.Now()
	issued := now.Add(-time.Hour)
	tokA, userA := mintUser(t, "alice", issued)
	tokB, userB := mintUser(t, "bob", issued)
	clock := func() time.Time { return now }

	newReq := func(cookie string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/agents", nil)
		r.Header.Set("Cookie", cookie)
		return r
	}

	t.Run("control_one_valid_value_is_resent", func(t *testing.T) {
		c := genuineSessionResend(newReq("omnipus-session="+tokA+"; omnipus-session=PLANTED"),
			[]config.UserConfig{userA, userB}, clock)
		require.NotNil(t, c)
		require.Equal(t, tokA, c.Value)
	})

	t.Run("two_different_valid_sessions_no_resend", func(t *testing.T) {
		c := genuineSessionResend(newReq("omnipus-session="+tokA+"; omnipus-session="+tokB),
			[]config.UserConfig{userA, userB}, clock)
		require.Nil(t, c, "two distinct values validate: there is no single genuine session to re-send")
	})

	t.Run("cross_site_subresource_no_resend", func(t *testing.T) {
		r := newReq("omnipus-session=" + tokA + "; omnipus-session=PLANTED")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Dest", "image")
		require.Nil(t, genuineSessionResend(r, []config.UserConfig{userA}, clock),
			"a cookie ResolveUserFromCookie refuses to evaluate is never re-sent")
	})
}

// guardResponse runs one request with cookieHeader through the real
// PlantedCookieGuard wired with users, and returns the recorded response.
func guardResponse(t *testing.T, users []config.UserConfig, cookieHeader string) *http.Response {
	t.Helper()
	guard := PlantedCookieGuard(WithPlantedCookieSessionUsers(func() []config.UserConfig { return users }))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/agents", nil)
	r.Header.Set("Cookie", cookieHeader)
	w := httptest.NewRecorder()
	guard(next).ServeHTTP(w, r)
	resp := w.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func resentSessionCookies(t *testing.T, resp *http.Response) []*http.Cookie {
	t.Helper()
	var out []*http.Cookie
	for _, raw := range resp.Header.Values("Set-Cookie") {
		c, err := http.ParseSetCookie(raw)
		require.NoError(t, err, raw)
		if c.Name == SessionCookieName && c.Value != "" {
			out = append(out, c)
		}
	}
	return out
}

func clearLineCount(t *testing.T, resp *http.Response) int {
	t.Helper()
	n := 0
	for _, raw := range resp.Header.Values("Set-Cookie") {
		c, err := http.ParseSetCookie(raw)
		require.NoError(t, err, raw)
		if c.Name == SessionCookieName && c.Value == "" {
			n++
		}
	}
	return n
}

// countComparisons installs the BcryptHash.Verify seam with a counting
// wrapper around the real bcrypt compare.
func countComparisons(t *testing.T) *int {
	t.Helper()
	n := 0
	restore := config.SetCompareHashAndPasswordForTest(func(h, p []byte) error {
		n++
		return bcrypt.CompareHashAndPassword(h, p)
	})
	t.Cleanup(restore)
	return &n
}

// TestPlantedCookieResend_VerificationBudget — security review CWE-400: the
// re-send must not turn a pre-authentication request into unbounded bcrypt
// work. Repeated copies of one value are verified once; a request presenting
// more distinct values than the budget is not verified at all (fail-closed:
// the clear lines are still sent, nothing is re-sent).
func TestPlantedCookieResend_VerificationBudget(t *testing.T) {
	now := time.Now()
	tok, user := mintUser(t, "alice", now.Add(-time.Hour))
	users := []config.UserConfig{user}

	t.Run("control_genuine_plus_one_plant", func(t *testing.T) {
		n := countComparisons(t)
		resp := guardResponse(t, users, "omnipus-session="+tok+"; omnipus-session=PLANTED")
		require.Len(t, resentSessionCookies(t, resp), 1, "the genuine session is re-sent")
		require.LessOrEqual(t, *n, 2, "at most one comparison per distinct value")
	})

	t.Run("many_duplicates_of_one_invalid_value_compared_once", func(t *testing.T) {
		n := countComparisons(t)
		resp := guardResponse(t, users, strings.TrimSuffix(strings.Repeat("omnipus-session=BAD; ", 50), "; "))
		require.Positive(t, clearLineCount(t, resp), "instrument check: the duplicate was detected")
		require.Empty(t, resentSessionCookies(t, resp))
		require.Equal(t, 1, *n, "50 copies of one value cost one comparison, not 50")
	})

	t.Run("many_distinct_values_over_budget_not_verified", func(t *testing.T) {
		n := countComparisons(t)
		parts := []string{"omnipus-session=" + tok}
		for i := 0; i < 50; i++ {
			parts = append(parts, fmt.Sprintf("omnipus-session=PLANT-%d", i))
		}
		resp := guardResponse(t, users, strings.Join(parts, "; "))
		require.Positive(t, clearLineCount(t, resp), "fail-closed: the clear lines are still sent")
		require.Empty(t, resentSessionCookies(t, resp), "over budget: nothing is re-sent")
		require.Equal(t, 0, *n, "over budget: no bcrypt comparison runs at all")
	})
}

// TestPlantedCookieResend_ExpiryCheckedAfterVerification — security review
// CWE-613: the original expiry is checked with a clock read AFTER the bcrypt
// work, and the re-send carries only the absolute Expires (no Max-Age, which
// a jar would count from receipt and so could outlive the original).
func TestPlantedCookieResend_ExpiryCheckedAfterVerification(t *testing.T) {
	issued := time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC)
	deadline := issued.Add(SessionCookieMaxAge * time.Second)
	tok, user := mintUser(t, "alice", issued)
	users := []config.UserConfig{user}

	fake := deadline.Add(-2 * time.Second)
	prevNow := plantedResendNow
	plantedResendNow = func() time.Time { return fake }
	t.Cleanup(func() { plantedResendNow = prevNow })

	t.Run("verification_crosses_the_original_deadline_no_resend", func(t *testing.T) {
		fake = deadline.Add(-2 * time.Second)
		restore := config.SetCompareHashAndPasswordForTest(func(h, p []byte) error {
			fake = deadline.Add(5 * time.Second) // the comparison takes us past the deadline
			return bcrypt.CompareHashAndPassword(h, p)
		})
		t.Cleanup(restore)
		resp := guardResponse(t, users, "omnipus-session="+tok+"; omnipus-session=PLANTED")
		require.Empty(t, resentSessionCookies(t, resp),
			"the original expiry passed during verification: no re-send")
	})

	t.Run("expires_only_at_the_original_expiry", func(t *testing.T) {
		fake = deadline.Add(-time.Hour)
		restore := config.SetCompareHashAndPasswordForTest(nil)
		t.Cleanup(restore)
		resp := guardResponse(t, users, "omnipus-session="+tok+"; omnipus-session=PLANTED")
		resent := resentSessionCookies(t, resp)
		require.Len(t, resent, 1)
		require.True(t, resent[0].Expires.Equal(deadline),
			"Expires is issued_at + 24 h to the second: want %s, got %s", deadline, resent[0].Expires)
		require.Zero(t, resent[0].MaxAge, "no Max-Age attribute on the re-send")
		for _, raw := range resp.Header.Values("Set-Cookie") {
			if strings.HasPrefix(raw, SessionCookieName+"="+tok) {
				require.NotContains(t, strings.ToLower(raw), "max-age", "re-send line: %q", raw)
			}
		}
	})
}
