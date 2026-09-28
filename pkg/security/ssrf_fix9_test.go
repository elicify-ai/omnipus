// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package security — gate wave-3 F1 proof tests (ADR-094 preview isolation,
// spec: docs/internal/specs/adr-094-preview-isolation-spec.md). Pins the
// portless exact-host /preview/ admission: on an implicit-80/443 canonical
// origin both the Mode 1 isolated_url AND the Mode 2 URL are minted portless
// (middleware/origin_canonical.go::canonicalGatewayOrigin drops the port at
// 80/443; pkg/tools/web_serve.go::executeStatic builds origin + "/preview/…"),
// so the gateway-origin SSRF exception admits the portless /preview/ form
// when the wired gateway port equals the scheme default — under the same
// fail-closed rules as the explicit-port branch. File name carries the fix9
// marker per the round's dispatch brief.
package security

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFix9SSRFPortlessPreviewException pins gate wave-3 F1. RED before the
// fix: the portless branch of isAllowedGatewayOrigin admitted only the label
// class, so every "admitted" assertion below failed on the pre-fix code while
// the "refused" ones passed.
func TestFix9SSRFPortlessPreviewException(t *testing.T) {
	ctx := context.Background()

	// Implicit-80 wiring — the exact origin shape the F1 failure scenario
	// names: canonicalGatewayOrigin returns http://localhost (no port), so
	// web_serve mints http://localhost/preview/<agent>/<token>/ (portless)
	// and the agent's own browser was SSRF-refused from opening it.
	sc80 := NewSSRFChecker(nil)
	sc80.AllowGatewayOrigin("localhost", 80)

	// The unit the fix touches: the portless branch now mirrors the
	// explicit-port branch's exact-host rules — scheme http(s), scheme-
	// default port equal to the wired gateway port, exact host match
	// including the localhost-only resolved-loopback literals, and the
	// ADR-073 /preview/ path scope.
	require.True(t, sc80.isAllowedGatewayOrigin("http://localhost/preview/agent-a/tok123/"),
		"F1: the minted portless Mode 2 URL must be admitted on an implicit-80 gateway")
	require.True(t, sc80.isAllowedGatewayOrigin("http://localhost/preview/agent-a/tok123/index.html"),
		"subpaths under /preview/ are admitted with the portless origin")
	require.True(t, sc80.isAllowedGatewayOrigin("http://127.0.0.1/preview/agent-a/tok123/"),
		"the resolved-loopback literal admits portless exactly as the explicit-port branch does")
	require.False(t, sc80.isAllowedGatewayOrigin("https://localhost/preview/agent-a/tok123/"),
		"a portless https URL's scheme-default port (443) does not equal the wired :80 — refused")

	// Implicit-443 wiring admits the https portless form by the same rule.
	sc443 := NewSSRFChecker(nil)
	sc443.AllowGatewayOrigin("localhost", 443)
	require.True(t, sc443.isAllowedGatewayOrigin("https://localhost/preview/agent-a/tok123/"),
		"an implicit-443 gateway admits the portless https /preview/ form")

	// Fail-closed guards — the portless admission mirrors, never widens:
	// ADR-073's path scope holds portless (the gateway's own REST API stays
	// out of the browser's reach — the exact hole ADR-073 closed).
	require.False(t, sc80.isAllowedGatewayOrigin("http://localhost/api/v1/config"),
		"ADR-073: the portless admission stays scoped to /preview/")
	require.False(t, sc80.isAllowedGatewayOrigin("http://localhost/"),
		"the origin root is not /preview/ and stays refused portless")

	// The scheme-default port must equal the wired gateway port: a portless
	// URL against an explicit-port wiring (the exact-host twin of DS-6 row 3)
	// is refused.
	sc5000 := NewSSRFChecker(nil)
	sc5000.AllowGatewayOrigin("localhost", 5000)
	require.False(t, sc5000.isAllowedGatewayOrigin("http://localhost/preview/agent-a/tok123/"),
		"F1's refused case: the wired gateway port (5000) is not the http scheme default")

	// Non-http(s) schemes carry no scheme-default port — refused portless.
	require.False(t, sc80.isAllowedGatewayOrigin("ftp://localhost/preview/agent-a/tok123/"),
		"non-http(s) schemes are refused in the portless branch")

	// No gateway origin configured — the fail-closed prologue is unchanged.
	require.False(t, NewSSRFChecker(nil).isAllowedGatewayOrigin("http://localhost/preview/agent-a/tok123/"),
		"no configured exception — portless refused regardless of shape")

	// The exception stays scoped to the configured gateway host: a foreign
	// host is refused even when the port agrees.
	require.False(t, sc80.isAllowedGatewayOrigin("http://192.0.2.1/preview/agent-a/tok123/"),
		"host mismatch — the portless admission never leaves the configured gateway host")

	// End-to-end through CheckURL. The exception is evaluated BEFORE the
	// resolver, so every case below is DNS-free: admitted forms short-circuit
	// at the exception; refusals fall through only to literal-IP checks.
	assert.NoError(t, sc80.CheckURL(ctx, "http://localhost/preview/agent-a/tok123/"),
		"CheckURL admits the minted portless Mode 2 URL end-to-end")
	assert.NoError(t, sc80.CheckURL(ctx, "http://127.0.0.1/preview/agent-a/tok123/"),
		"CheckURL admits the portless resolved-loopback form end-to-end")
	assert.Error(t, sc80.CheckURL(ctx, "http://127.0.0.1/api/v1/config"),
		"CheckURL still refuses the gateway REST API portless (ADR-073)")
}
