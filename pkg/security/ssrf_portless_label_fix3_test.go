// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package security — focused fix-round test for the SSRF layer's portless
// label-host admission (gate round fix3, finding SL-F2 / DS-3 row 10). File
// name carries the fix3 marker per the round's dispatch brief.
package security

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fix3Label is a grammar-valid, mint-shaped label (26 lower-case base32
// characters, DS-1 row 9 / FR-005) — the same shape the mint emits.
const fix3Label = "k4m2wpxq5ztn7vrj9eh3as8dyg"

// TestFix3SSRFPortlessLabelHost pins SL-F2: on an implicit-80 gateway the
// mint emits a PORTLESS isolated_url, so the label class admits a portless
// http(s) URL whose scheme-default effective port equals the wired gateway
// port. Deliberately narrow: the exact-host /preview/ exception keeps
// requiring an explicit port, and the DS-6 row 3 pin (portless against an
// explicit-port wiring) keeps holding. RED before the fix: every True row
// fails because extractHostPort returns !ok for a portless URL and the
// prologue refuses before the label class is consulted.
func TestFix3SSRFPortlessLabelHost(t *testing.T) {
	sc80 := NewSSRFChecker(nil)
	sc80.AllowGatewayOrigin("localhost", 80)

	require.True(t, sc80.isAllowedGatewayOrigin("http://"+fix3Label+".localhost/"),
		"SL-F2: the portless isolated_url the mint emits on an implicit-80 gateway must be admitted")
	require.True(t, sc80.isAllowedGatewayOrigin("http://"+fix3Label+".localhost:80/"),
		"an explicit :80 label URL admits the same as the portless form")
	require.True(t, sc80.isAllowedGatewayOrigin("http://"+fix3Label+".localhost/app.js"),
		"the label class is admitted for any path (FR-021, round-2 MAJ-006)")

	// Narrow-scope pin: the exact-host /preview/ exception keeps requiring an
	// explicit port — a portless URL is admitted by the label class only.
	require.False(t, sc80.isAllowedGatewayOrigin("http://localhost/preview/a/b/"),
		"a portless bare-host /preview/ URL stays refused — only the label class admits portless")

	sc5000 := NewSSRFChecker(nil)
	sc5000.AllowGatewayOrigin("localhost", 5000)
	require.False(t, sc5000.isAllowedGatewayOrigin("http://"+fix3Label+".localhost/"),
		"DS-6 row 3 pin: a portless label URL against an explicit-port wiring is still refused")
}
