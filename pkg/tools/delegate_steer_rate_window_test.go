// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 fix-lane 7 (Gap 5): the WP-G classification table
// (docs/internal/specs/adr-091-wp-g-fixtures-and-suites-spec.md) marks
// TestSteerRateWindows_EvictsExpiredOtherSessionKeys **retain**, with its
// reason spelled out in full ("pure steerRateWindows map-hygiene test, no
// ParentDurableKey/Async at all"). The file it lived in
// (pkg/tools/delegate_signoff14_test.go) was deleted anyway, taking this
// coverage with it. steerRateWindows is live production code
// (delegate_followup.go::checkSteerCaps): it backs the steer/respond rate
// cap AND an opportunistic eviction sweep whose own comment warns that
// without it "every distinct session_id ever steered accumulates a
// permanent entry for the life of the process" — an unbounded-memory-growth
// defect. Neither branch had any coverage before this file.
package tools

import (
	"strings"
	"testing"
	"time"
)

// TestSteerRateCap_RefusesBeyondLimitAndEvictsOtherSessions drives
// checkSteerCaps directly (the real production rate-cap/eviction body, not
// a reimplementation): limit+1 steers on one session must refuse the last
// with the rate-exceeded message, and a second session's window — seeded
// entirely older than the 1-minute eviction cutoff — must be swept out of
// the map entirely by the very next call, regardless of which session that
// call names.
func TestSteerRateCap_RefusesBeyondLimitAndEvictsOtherSessions(t *testing.T) {
	tool := NewDelegateTool("test-model", 4096, 0.5)
	now := time.Now()
	tool.SetClock(func() time.Time { return now })

	const sessionA = "adr091-steer-rate-session-a"
	limit := tool.steerRatePerMin
	if limit <= 0 {
		t.Fatalf("steerRatePerMin = %d, want > 0 (DefaultSteerRatePerMinute wiring broken)", limit)
	}

	for i := 0; i < limit; i++ {
		if err := tool.checkSteerCaps(sessionA, "hi"); err != nil {
			t.Fatalf("steer %d/%d within the %d/min cap must be allowed, got: %v", i+1, limit, limit, err)
		}
	}
	if err := tool.checkSteerCaps(sessionA, "one too many"); err == nil {
		t.Fatalf("steer %d (beyond the %d/min cap) must be refused, got: nil error", limit+1, limit)
	} else if !strings.Contains(err.Error(), "rate exceeded") {
		t.Fatalf("steer beyond the %d/min cap must be refused with the rate-exceeded message, got: %v", limit, err)
	}

	// Seed a SECOND session's window entirely older than the 1-minute
	// eviction cutoff — the exact shape checkSteerCaps's own opportunistic
	// sweep targets ("every OTHER session's window for entries older than
	// the rate window").
	const sessionB = "adr091-steer-rate-session-b"
	tool.steerRateMu.Lock()
	tool.steerRateWindows[sessionB] = []time.Time{now.Add(-2 * time.Minute)}
	tool.steerRateMu.Unlock()

	// One more steer (on session A again — sessionID == sessionB is
	// deliberately never true, so the sweep loop's `if sid == sessionID {
	// continue}` guard never skips session B). checkSteerCaps unconditionally
	// runs its sweep before deciding session A's own (still-refused) request.
	_ = tool.checkSteerCaps(sessionA, "trigger the opportunistic sweep")

	tool.steerRateMu.Lock()
	_, stillPresent := tool.steerRateWindows[sessionB]
	tool.steerRateMu.Unlock()
	if stillPresent {
		t.Fatal("session B's fully-expired rate window is still present after the next call's opportunistic " +
			"sweep — every distinct session_id ever steered would accumulate a permanent map entry for the " +
			"life of the process")
	}
}
