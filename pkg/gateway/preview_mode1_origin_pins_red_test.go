// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — PIN test for ADR-094 preview isolation, order 25
// (S-2.2, round-2 MAJ-004; spec:
// docs/internal/specs/adr-094-preview-isolation-spec.md): the controls that
// HOLD today are pinned so mutations M-1/M-2 must flip this row red.
//
// Test plan (elicify-test-writing step 1, embedded):
//
//   - Behaviour under test: today's CORS and WebSocket origin controls
//     already refuse a label Origin — the pin that makes GREEN's Mode 1
//     origin work observable: any widening must be surgical (admit label
//     origins ONLY where the spec demands), and these pins hold.
//   - Specification source: order 25 / S-2.2 / round-2 MAJ-004 (the pin row
//     is the spec); the control rows come from the functions' same-origin
//     contract.
//   - Unit boundary: pure unit calls — rest.go::isAllowedOrigin and
//     websocket.go::wsCheckOrigin directly, no harness.
//   - Shape: PASSES today (a pin, not a RED). Both refusal rows tell
//     M-1/M-2's story; the same-origin admission control rows guard the pin's
//     other direction (a "return false always" mutant must not pass).
//   - Mutations: M-1 (widen isAllowedOrigin to admit label origins) and
//     M-2 (widen wsCheckOrigin likewise) MUST flip the refusal rows red —
//     verified in this RED pack by scratch-clone mutation runs (see the
//     dispatch report's evidence table).

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// piRedPinLabel is a grammar-valid 26-char label for the pin rows.
const piRedPinLabel = "p1nr3dl4b3l0y7v2q9k5x8w4ze"

// TestMode1OriginPins pins the order-25 controls: both origin controls refuse
// a label origin today and must KEEP refusing any non-surgical widening
// (round-2 MAJ-004).
func TestMode1OriginPins(t *testing.T) {
	const (
		port        = "5000"
		labelOrigin = "http://" + piRedPinLabel + ".localhost:5000"
		genuine     = "http://localhost:5000"
	)

	t.Run("rest_isAllowedOrigin_refuses_label_origin", func(t *testing.T) {
		got := isAllowedOrigin(labelOrigin, "localhost:"+port, genuine)
		require.False(t, got,
			"PIN (order 25, round-2 MAJ-004): rest.go::isAllowedOrigin refuses "+
				"http://<label>.localhost:<port> today and must keep refusing under any non-surgical "+
				"widening — mutation M-1 must flip this row red")
	})

	t.Run("rest_isAllowedOrigin_admits_genuine_same_origin", func(t *testing.T) {
		got := isAllowedOrigin(genuine, "localhost:"+port, "")
		require.True(t, got,
			"control row: the same-origin admission must keep working — a blanket-refusal "+
				"mutant must not pass this file")
	})

	t.Run("ws_wsCheckOrigin_refuses_label_origin", func(t *testing.T) {
		check := wsCheckOrigin(genuine)
		r := httptest.NewRequest(http.MethodGet, "http://localhost:"+port+"/api/ws", nil)
		r.Header.Set("Origin", labelOrigin)
		require.False(t, check(r),
			"PIN (order 25, round-2 MAJ-004): websocket.go::wsCheckOrigin refuses a label Origin "+
				"today and must keep refusing under any non-surgical widening — mutation M-2 must "+
				"flip this row red")
	})

	t.Run("ws_wsCheckOrigin_admits_genuine_same_origin", func(t *testing.T) {
		check := wsCheckOrigin("")
		r := httptest.NewRequest(http.MethodGet, "http://localhost:"+port+"/api/ws", nil)
		r.Header.Set("Origin", genuine)
		require.True(t, check(r),
			"control row: wsCheckOrigin's localhost/loopback admission keeps working — a "+
				"blanket-refusal mutant must not pass this file")
	})
}
