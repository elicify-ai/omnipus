// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "testing"

// TestTokenBudget_SetCap_OneShot verifies sec-MINOR-2/#538: FR-177 documents
// SetCap as "called ONCE at boot from config" but the code previously applied
// every call live, with nothing in production actually calling it a second
// time to catch the latent N-15 hazard (a live cap change straddling two
// budgets). This pins the enforced guard: the first SetCap call takes effect,
// a second call is rejected as a no-op, and the first call's value survives.
func TestTokenBudget_SetCap_OneShot(t *testing.T) {
	tb := NewTokenBudget(0, nil)

	tb.SetCap(1000)
	if got := tb.Cap(); got != 1000 {
		t.Fatalf("first SetCap(1000): Cap()=%d, want 1000", got)
	}

	// Second call must be rejected (no-op) — the first value survives.
	tb.SetCap(500)
	if got := tb.Cap(); got != 1000 {
		t.Fatalf("second SetCap(500) must be a no-op: Cap()=%d, want unchanged 1000", got)
	}

	// A third call (any value, including 0/unbounded) is likewise rejected.
	tb.SetCap(0)
	if got := tb.Cap(); got != 1000 {
		t.Fatalf("third SetCap(0) must be a no-op: Cap()=%d, want unchanged 1000", got)
	}
}

// TestTokenBudget_SetCap_OneShot_NegativeFirstCallClampsToZero verifies the
// existing negative-clamp behavior (0 = unbounded sentinel) still applies on
// the one allowed (first) SetCap call, and that the clamped value then
// freezes exactly like any other first call.
func TestTokenBudget_SetCap_OneShot_NegativeFirstCallClampsToZero(t *testing.T) {
	tb := NewTokenBudget(0, nil)

	tb.SetCap(-5)
	if got := tb.Cap(); got != 0 {
		t.Fatalf("first SetCap(-5): Cap()=%d, want clamped 0", got)
	}

	tb.SetCap(2000)
	if got := tb.Cap(); got != 0 {
		t.Fatalf("second SetCap(2000) must be a no-op after frozen 0: Cap()=%d, want unchanged 0", got)
	}
}

// TestTokenBudget_SetCap_OneShot_ConsumedPreservedAcrossRejectedCall verifies
// a rejected second SetCap call does not disturb the consumed counter (the
// no-op is scoped to capTok only).
func TestTokenBudget_SetCap_OneShot_ConsumedPreservedAcrossRejectedCall(t *testing.T) {
	tb := NewTokenBudget(0, nil)
	tb.SetCap(1000)
	tb.Debit(300)

	tb.SetCap(9999) // rejected no-op
	if got := tb.Cap(); got != 1000 {
		t.Fatalf("Cap()=%d, want unchanged 1000", got)
	}
	if got := tb.Consumed(); got != 300 {
		t.Fatalf("Consumed()=%d, want unchanged 300", got)
	}
}
