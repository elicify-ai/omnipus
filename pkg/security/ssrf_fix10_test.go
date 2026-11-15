// Omnipus - Ultra-lightweight hardening proof for the SSRF gateway-origin
// exception's path scope (ADR-094 fix10, security-lead MINOR).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFix10SSRFGatewayOriginDotSegments pins the fix10 hardening: the
// gateway-origin SSRF exception's path scope must decide on the NORMALISED
// path, so a dot-segment escape cannot satisfy the literal /preview/ prefix
// check. RED before the fix — the "refused" rows failed on pre-fix code
// while the "admitted" rows passed.
func TestFix10SSRFGatewayOriginDotSegments(t *testing.T) {
	ctx := context.Background()

	sc := NewSSRFChecker(nil)
	sc.AllowGatewayOrigin("localhost", 5000)

	// Dot-segment escapes OUT of /preview/ — refused after the fix.
	require.False(t, sc.isAllowedGatewayOrigin("http://localhost:5000/preview/../api/v1/config"),
		"fix10: escape via .. must not pass the literal prefix check")
	require.False(t, sc.isAllowedGatewayOrigin("http://localhost:5000/preview/x/../../api/v1/config"),
		"fix10: deep escape must be refused after normalisation")
	require.False(t, sc.isAllowedGatewayOrigin("http://localhost:5000/preview/agent/tok/../../../api/v1/config"),
		"fix10: over-deep escape clamps at root and stays refused")

	// A traversal that normalises back INSIDE /preview/ stays admitted —
	// the effective path is under the prefix, which is all the exception
	// ever allowed.
	require.True(t, sc.isAllowedGatewayOrigin("http://localhost:5000/preview/../preview/agent/tok/"),
		"dot-segment path normalising back under /preview/ is admitted")

	// The admission contract is unchanged for dot-segment-free URLs.
	require.True(t, sc.isAllowedGatewayOrigin("http://localhost:5000/preview/agent/tok/"),
		"the minted Mode 2 URL is admitted exactly as before the fix")
	require.True(t, sc.isAllowedGatewayOrigin("http://localhost:5000/previews/../preview/agent/tok/"),
		"dot-segment path normalising INTO /preview/ is admitted by the same rule")

	// Prefix boundary: the trailing slash decides /preview/ vs /preview.
	require.True(t, sc.isAllowedGatewayOrigin("http://localhost:5000/preview/"),
		"the /preview/ root keeps its trailing slash through normalisation and stays admitted")
	require.False(t, sc.isAllowedGatewayOrigin("http://localhost:5000/preview"),
		"the bare prefix without the slash is not under /preview/ and stays refused")

	// End-to-end through CheckURL: the escape must not reach the API.
	assert.Error(t, sc.CheckURL(ctx, "http://localhost:5000/preview/../api/v1/config"),
		"CheckURL refuses the dot-segment escape end-to-end")
}

// TestFix10SSRFExtractPathNormalisation unit-tests the helper the fix
// touches: dot segments resolved, query/fragment still stripped, and the
// trailing slash preserved where it matters (the /preview/ prefix boundary).
func TestFix10SSRFExtractPathNormalisation(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "no path stays empty", in: "http://g", want: ""},
		{name: "bare root", in: "http://g/", want: "/"},
		{name: "root with query", in: "http://g/?q=1", want: "/"},
		{name: "dot-segment escape", in: "http://g/preview/../api/v1/config", want: "/api/v1/config"},
		{name: "escape with query", in: "http://g/preview/../api/v1/config?x=1", want: "/api/v1/config"},
		{name: "escape with fragment", in: "http://g/preview/../api#f", want: "/api"},
		{name: "single dot segment collapses", in: "http://g/preview/./x", want: "/preview/x"},
		{name: "double slashes collapse", in: "http://g/preview//a", want: "/preview/a"},
		{name: "trailing slash preserved at prefix boundary", in: "http://g/preview/", want: "/preview/"},
		{name: "trailing slash on subpath", in: "http://g/preview/a/", want: "/preview/a/"},
		{name: "escape to root clamps", in: "http://g/..", want: "/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractPath(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}
