// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package common

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func withGuardEdition(t *testing.T, e string) {
	t.Helper()
	restore := config.SetEdition(e)
	t.Cleanup(func() {
		restore()
		SetEndpointAllowlist(nil)
	})
}

// TestEndpointGuard_OnlyOnHosted: the guard is decided by the stamped
// edition, never by config.
func TestEndpointGuard_OnlyOnHosted(t *testing.T) {
	for _, e := range []string{config.EditionCore, config.EditionDesktop} {
		withGuardEdition(t, e)
		assert.Nil(t, EndpointGuard(), e)
		assert.False(t, CatalogBaseOverrideRefused("https://elsewhere.example.com/v1"), e)
		assert.Nil(t, GuardedHTTPClient(), e)
	}
	withGuardEdition(t, config.EditionHosted)
	require.NotNil(t, EndpointGuard())
	assert.Error(t, EndpointGuard().CheckIP([]byte{127, 0, 0, 1}), "strict default refuses loopback")
}

// TestEndpointExplicitlyAllowed_Table: the allow list exempts what it NAMES —
// an exact host, an exact IP, or an IP literal inside a CIDR — and nothing it
// does not, with no DNS involved.
func TestEndpointExplicitlyAllowed_Table(t *testing.T) {
	withGuardEdition(t, config.EditionHosted)
	SetEndpointAllowlist([]string{" Mock.Internal ", "10.9.0.0/16", "::1", ""})

	cases := []struct {
		url  string
		want bool
	}{
		{"http://mock.internal:9000/v1", true},
		{"http://MOCK.internal/v1", true},
		{"http://mock.internal.evil.com/v1", false},
		{"http://10.9.0.1/v1", true},
		{"http://10.9.255.255/v1", true},
		{"http://10.10.0.1/v1", false},
		{"http://[::1]:8080/v1", true},
		{"http://127.0.0.1/v1", false},
		{"not a url %%", false},
		{"", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, EndpointExplicitlyAllowed(tc.url), tc.url)
		assert.Equal(t, !tc.want && tc.url != "", CatalogBaseOverrideRefused(tc.url), "refused: %s", tc.url)
	}

	SetEndpointAllowlist(nil)
	assert.False(t, EndpointExplicitlyAllowed("http://mock.internal/v1"), "clearing the list removes exemptions")
}

// TestGuardTransport_AllowlistedLoopbackReachable: on hosted, the dial guard
// refuses loopback by default and lets it through once the allow list names
// it — the path CI's end-to-end mock provider takes.
func TestGuardTransport_AllowlistedLoopbackReachable(t *testing.T) {
	withGuardEdition(t, config.EditionHosted)
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)

	c, err := NewHTTPClient("")
	require.NoError(t, err)
	refused, err := c.Get(srv.URL)
	if refused != nil {
		_ = refused.Body.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSRF")
	assert.Equal(t, int64(0), hits.Load())

	SetEndpointAllowlist([]string{"127.0.0.1"})
	c, err = NewHTTPClient("")
	require.NoError(t, err)
	resp, err := c.Get(srv.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, int64(1), hits.Load())
}
