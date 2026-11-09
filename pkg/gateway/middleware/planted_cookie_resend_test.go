// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

// FR-015 genuine-session re-send (founder decision 2026-11-09): the refusal
// branches of genuineSessionResend that the gateway-level tests
// (pkg/gateway/preview_planted_cookie_resend_test.go) cannot reach with a
// single-account harness.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

	newReq := func(cookie string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/agents", nil)
		r.Header.Set("Cookie", cookie)
		return r
	}

	t.Run("control_one_valid_value_is_resent", func(t *testing.T) {
		c := genuineSessionResend(newReq("omnipus-session="+tokA+"; omnipus-session=PLANTED"),
			[]config.UserConfig{userA, userB}, now)
		require.NotNil(t, c)
		require.Equal(t, tokA, c.Value)
	})

	t.Run("two_different_valid_sessions_no_resend", func(t *testing.T) {
		c := genuineSessionResend(newReq("omnipus-session="+tokA+"; omnipus-session="+tokB),
			[]config.UserConfig{userA, userB}, now)
		require.Nil(t, c, "two distinct values validate: there is no single genuine session to re-send")
	})

	t.Run("cross_site_subresource_no_resend", func(t *testing.T) {
		r := newReq("omnipus-session=" + tokA + "; omnipus-session=PLANTED")
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		r.Header.Set("Sec-Fetch-Dest", "image")
		require.Nil(t, genuineSessionResend(r, []config.UserConfig{userA}, now),
			"a cookie ResolveUserFromCookie refuses to evaluate is never re-sent")
	})
}
