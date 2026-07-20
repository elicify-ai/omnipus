// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"testing"
	"time"
)

// TestRrule_SubSecondDtstartConsistency is a regression test for the finding
// that a non-second-aligned dtstart_ms (e.g. an editor anchor of `new Date()`,
// which carries milliseconds) made the liveness probe report "never fires"
// while the scheduler's NextOccurrenceAfter found the occurrence — a
// validator/scheduler disagreement that produced a false 400 on a valid rule.
// parseOption now truncates the anchor to whole seconds (RFC 5545 has no
// sub-second grammar), so all functions agree.
func TestRrule_SubSecondDtstartConsistency(t *testing.T) {
	// 2027-06-15 09:00:15.500 UTC — deliberately NOT second-aligned.
	dtstartMs := time.Date(2027, 6, 15, 9, 0, 15, 500*int(time.Millisecond), time.UTC).UnixMilli()
	const tz = "UTC"

	cases := []struct {
		name  string
		rrule string
	}{
		{"count1_daily", "FREQ=DAILY;COUNT=1"},
		{"count1_weekly", "FREQ=WEEKLY;BYDAY=TU;COUNT=1"},
		{"sparse_yearly", "FREQ=YEARLY;COUNT=2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Liveness probe MUST see the occurrence within 5 years.
			live, err := HasOccurrenceWithinYears(tc.rrule, dtstartMs, tz, 5)
			if err != nil {
				t.Fatalf("HasOccurrenceWithinYears error: %v", err)
			}
			if !live {
				t.Fatalf("HasOccurrenceWithinYears = false; a rule anchored now must fire within 5 years")
			}

			// Scheduler MUST find the next occurrence from just before the anchor.
			next, ok, err := NextOccurrenceAfter(tc.rrule, dtstartMs, tz, dtstartMs-1000)
			if err != nil {
				t.Fatalf("NextOccurrenceAfter error: %v", err)
			}
			if !ok {
				t.Fatalf("NextOccurrenceAfter ok=false; the first occurrence is after (dtstart-1s)")
			}

			// The two must AGREE that the series is live, and the first
			// occurrence must be second-aligned (sub-second dropped).
			if next%1000 != 0 {
				t.Errorf("NextOccurrenceAfter = %d, want a whole-second instant (sub-second must be truncated)", next)
			}

			// Validation-support expander must also surface the occurrence.
			inst, err := ExpandForValidation(tc.rrule, dtstartMs, tz)
			if err != nil {
				t.Fatalf("ExpandForValidation error: %v", err)
			}
			if len(inst) == 0 {
				t.Errorf("ExpandForValidation returned no instants for a live rule")
			}
		})
	}
}
