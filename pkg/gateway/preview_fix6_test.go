// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Gate wave-2 fix6 pins (PR #950, #798 ADR-094):
//
//   - TDA-3 (type-design-analyzer): setPreviewStaticHeaders takes the CSP's
//     mode EXPLICITLY - a mode1 bool parameter - never inferred from the
//     request context. The red run proved the pre-change inference real:
//     with a label in the request context the old code emitted the Mode 1
//     literal instead of the Mode 2 template the serving path asked for.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPreviewFix6_SetPreviewStaticHeaders_ExplicitMode pins both CSP modes
// through the explicit parameter: mode1=true gets exactly the Mode 1 literal
// (no source directives); mode1=false gets the byte-stable Mode 2 template
// with sources pinned to mainOrigin+prefix - with a label deliberately left
// OUT of the picture (the function no longer takes a request at all, so the
// context cannot leak into the CSP by construction).
func TestPreviewFix6_SetPreviewStaticHeaders_ExplicitMode(t *testing.T) {
	const mainOrigin = "http://localhost:5000"
	const prefix = "/preview/agent/tok123"

	w1 := httptest.NewRecorder()
	setPreviewStaticHeaders(w1, true, mainOrigin, prefix)
	got1 := w1.Header().Get("Content-Security-Policy")
	if got1 != "frame-ancestors 'none'" {
		t.Fatalf("mode1=true: CSP = %q, want exactly %q (Mode 1 - no source directives)", got1, "frame-ancestors 'none'")
	}

	w2 := httptest.NewRecorder()
	setPreviewStaticHeaders(w2, false, mainOrigin, prefix)
	got2 := w2.Header().Get("Content-Security-Policy")
	if got2 == "frame-ancestors 'none'" {
		t.Fatalf("mode1=false: CSP is the Mode 1 literal - the modes are confused")
	}
	for _, want := range []string{
		"default-src 'none';",
		"script-src http://localhost:5000/preview/agent/tok123 'unsafe-inline' 'unsafe-eval';",
	} {
		if !strings.Contains(got2, want) {
			t.Fatalf("mode1=false: CSP %q missing pinned source %q", got2, want)
		}
	}
}
