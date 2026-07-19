// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

// ---------------------------------------------------------------------------
// Test 4: TestRruleExpansion_WallClockDST
// ---------------------------------------------------------------------------
//
// Europe/Berlin 2026 transitions (verified against tzdata): spring-forward
// 2026-03-29 (02:00 CET -> 03:00 CEST), fall-back 2026-10-25
// (03:00 CEST -> 02:00 CET).
func TestRruleExpansion_WallClockDST(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")

	t.Run("09:00 stable across spring-forward", func(t *testing.T) {
		dtstart := time.Date(2026, 3, 20, 9, 0, 0, 0, berlin).UnixMilli()
		from := time.Date(2026, 3, 25, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 4, 5, 0, 0, 0, 0, berlin).UnixMilli()
		instants, truncated, err := ExpandRRULE("FREQ=DAILY", dtstart, "Europe/Berlin", from, to, 1000)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 11 { // Mar 25..Apr 4 inclusive
			t.Fatalf("expected 11 daily occurrences, got %d", len(instants))
		}
		var sawCET, sawCEST bool
		for _, ms := range instants {
			lt := time.UnixMilli(ms).In(berlin)
			if lt.Hour() != 9 || lt.Minute() != 0 {
				t.Fatalf("occurrence %v not at 09:00 local", lt)
			}
			_, off := lt.Zone()
			switch off {
			case 3600: // CET +1h
				sawCET = true
			case 7200: // CEST +2h
				sawCEST = true
			}
		}
		if !sawCET || !sawCEST {
			t.Fatalf("expected occurrences on both sides of the transition, sawCET=%v sawCEST=%v", sawCET, sawCEST)
		}
	})

	t.Run("09:00 stable across fall-back", func(t *testing.T) {
		dtstart := time.Date(2026, 10, 15, 9, 0, 0, 0, berlin).UnixMilli()
		from := time.Date(2026, 10, 20, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 10, 31, 0, 0, 0, 0, berlin).UnixMilli()
		instants, truncated, err := ExpandRRULE("FREQ=DAILY", dtstart, "Europe/Berlin", from, to, 1000)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 11 {
			t.Fatalf("expected 11 daily occurrences, got %d", len(instants))
		}
		var sawCET, sawCEST bool
		for _, ms := range instants {
			lt := time.UnixMilli(ms).In(berlin)
			if lt.Hour() != 9 || lt.Minute() != 0 {
				t.Fatalf("occurrence %v not at 09:00 local", lt)
			}
			_, off := lt.Zone()
			switch off {
			case 3600:
				sawCET = true
			case 7200:
				sawCEST = true
			}
		}
		if !sawCET || !sawCEST {
			t.Fatalf("expected occurrences on both sides of the transition, sawCET=%v sawCEST=%v", sawCET, sawCEST)
		}
	})

	t.Run("02:30 nonexistent on spring-forward fires once at normalized 03:30", func(t *testing.T) {
		dtstart := time.Date(2026, 3, 25, 2, 30, 0, 0, berlin).UnixMilli()
		from := time.Date(2026, 3, 29, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 3, 30, 0, 0, 0, 0, berlin).UnixMilli()
		instants, truncated, err := ExpandRRULE("FREQ=DAILY", dtstart, "Europe/Berlin", from, to, 10)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 1 {
			t.Fatalf("expected exactly 1 occurrence on the spring-forward day, got %d: %v", len(instants), instants)
		}
		got := time.UnixMilli(instants[0]).In(berlin)
		want := time.Date(2026, 3, 29, 3, 30, 0, 0, berlin)
		if !got.Equal(want) {
			t.Fatalf("expected normalized 03:30 CEST (%v), got %v", want, got)
		}
	})

	t.Run("02:30 ambiguous on fall-back fires once at the first occurrence", func(t *testing.T) {
		dtstart := time.Date(2026, 10, 20, 2, 30, 0, 0, berlin).UnixMilli()
		from := time.Date(2026, 10, 25, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 10, 26, 0, 0, 0, 0, berlin).UnixMilli()
		instants, truncated, err := ExpandRRULE("FREQ=DAILY", dtstart, "Europe/Berlin", from, to, 10)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 1 {
			t.Fatalf("expected exactly 1 occurrence on the fall-back day, got %d: %v", len(instants), instants)
		}
		got := time.UnixMilli(instants[0])
		// First (pre-transition, CEST +2h) occurrence of 02:30 local on 2026-10-25.
		want := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("expected first (pre-transition) occurrence %v UTC, got %v UTC", want, got.UTC())
		}
	})

	t.Run("BYHOUR=2,3;BYMINUTE=30 collision dedups to a single fire", func(t *testing.T) {
		dtstart := time.Date(2026, 3, 25, 0, 0, 0, 0, berlin).UnixMilli()
		from := time.Date(2026, 3, 29, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 3, 30, 0, 0, 0, 0, berlin).UnixMilli()
		instants, truncated, err := ExpandRRULE(
			"FREQ=DAILY;BYHOUR=2,3;BYMINUTE=30",
			dtstart,
			"Europe/Berlin",
			from,
			to,
			10,
		)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 1 {
			t.Fatalf("expected the BYHOUR=2,3 collision to dedup to 1 occurrence, got %d: %v", len(instants), instants)
		}
		got := time.UnixMilli(instants[0]).In(berlin)
		want := time.Date(2026, 3, 29, 3, 30, 0, 0, berlin)
		if !got.Equal(want) {
			t.Fatalf("expected normalized 03:30 CEST (%v), got %v", want, got)
		}
	})
}

// ---------------------------------------------------------------------------
// TestRruleExpansion_RegularArithmeticDSTDedup
// ---------------------------------------------------------------------------
//
// The dedup at regularOccurrencesInRange's "if ms != lastMs" check (the O(1)
// arithmetic path used for STRUCTURALLY-REGULAR rules, no BY* modifiers) is
// untouched by every DST case in TestRruleExpansion_WallClockDST above: those
// all use either FREQ=DAILY (24h step, never collides with itself across a
// 1h gap) or a BYHOUR-bearing rule (routed through generateIrregular's own,
// separate dedup instead — BYHOUR/BYMINUTE force the isStructurallyRegular
// check to false, see rrule.go). This test exercises the regular-arithmetic
// path directly with a plain FREQ=HOURLY rule (no BY*) stepping straight
// over the Berlin 2026-03-29 spring-forward gap.
//
// Berlin 2026-03-29 (verified against tzdata, see TestRruleExpansion_WallClockDST's
// comment above): clocks jump 02:00 CET -> 03:00 CEST. advanceByPeriods
// steps the wall-clock hour field 00,01,02,...,23; the nonexistent label
// "02:00" is shifted forward by resolveWallClock to 03:00 CEST — the SAME
// instant the following k's literal (already-existing) "03:00" candidate
// produces. Those two candidates are ADJACENT in k, so "if ms != lastMs"
// collapses them to a single fire: 24 raw hourly wall-clock labels collapse
// to 23 unique instants.
func TestRruleExpansion_RegularArithmeticDSTDedup(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")

	t.Run("plain FREQ=HOURLY over the spring-forward day yields 23 unique instants", func(t *testing.T) {
		dtstart := time.Date(2026, 3, 29, 0, 0, 0, 0, berlin).UnixMilli()
		from := dtstart
		to := time.Date(2026, 3, 30, 0, 0, 0, 0, berlin).UnixMilli()

		instants, truncated, err := ExpandRRULE("FREQ=HOURLY", dtstart, "Europe/Berlin", from, to, 100)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 23 {
			t.Fatalf(
				"expected exactly 23 unique hourly instants (24 wall-clock labels minus the "+
					"spring-forward collision), got %d: %v",
				len(instants), instants,
			)
		}

		seen := make(map[int64]bool, len(instants))
		for i, ms := range instants {
			if seen[ms] {
				t.Fatalf("occurrence[%d] = %d is a duplicate instant — dedup failed", i, ms)
			}
			seen[ms] = true
			if i > 0 && ms <= instants[i-1] {
				t.Fatalf("occurrence[%d] = %d is not strictly after occurrence[%d] = %d (not ascending)",
					i, ms, i-1, instants[i-1])
			}
		}

		// The collapsed pair: the nonexistent 02:00 (shifted forward) and the
		// literal, already-existing 03:00 both resolve to the same instant,
		// 2026-03-29 03:00 CEST — present exactly once.
		want := time.Date(2026, 3, 29, 3, 0, 0, 0, berlin)
		var collapsedCount int
		for _, ms := range instants {
			if time.UnixMilli(ms).Equal(want) {
				collapsedCount++
			}
		}
		if collapsedCount != 1 {
			t.Fatalf("expected the collapsed 03:00 CEST instant (%v) exactly once, saw it %d times",
				want, collapsedCount)
		}

		// No other hour label is skipped or duplicated: 00:00/01:00 CET
		// (before the gap) and 03:00..23:00 CEST (the gap onward) are all
		// present, in local-hour order.
		wantHours := []int{0, 1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}
		for i, ms := range instants {
			lt := time.UnixMilli(ms).In(berlin)
			if lt.Hour() != wantHours[i] || lt.Minute() != 0 {
				t.Errorf("occurrence[%d] local time = %v, want hour %d minute 0", i, lt, wantHours[i])
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TestRruleExpansion_MinutelyDSTDedup
// ---------------------------------------------------------------------------
//
// The regular-arithmetic dedup used to be adjacent-only (see the retired
// comment on TestRruleExpansion_RegularArithmeticDSTDedup above), which only
// ever caught DST collisions exactly 1 k-step apart — true for FREQ=HOURLY
// (period == the Berlin 1h gap, so the colliding pair IS adjacent) but FALSE
// for FREQ=MINUTELY (period 1min): the 60 nonexistent 02:00-02:59 wall-clock
// minute labels each shift forward onto a distinct native 03:00-03:59
// label, 60 k-steps later — far outside an adjacent-only dedup's reach, and
// each pair lands in REVERSE generation order once the native labels are
// reached (see regularOccurrencesInRange's doc), so the raw k-ordered output
// is non-monotonic. This test exercises FREQ=MINUTELY directly over the
// Berlin 2026-03-29 spring-forward civil day (verified against tzdata, see
// TestRruleExpansion_WallClockDST's comment) via the full ExpandRRULE entry
// point (sort + full dedup, both the regular-arithmetic and — after this
// fix — the irregular path share the same dedupSortedMs).
func TestRruleExpansion_MinutelyDSTDedup(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")
	// DTSTART precedes both subtests' query ranges (2026-03-24 and
	// 2026-03-29) so both are genuinely in-range, not before DTSTART.
	dtstart := time.Date(2026, 3, 20, 0, 0, 0, 0, berlin).UnixMilli()

	t.Run("spring-forward civil day: 1440 wall-clock labels dedup to 1380 unique instants", func(t *testing.T) {
		from := time.Date(2026, 3, 29, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 3, 30, 0, 0, 0, 0, berlin).UnixMilli()

		instants, truncated, err := ExpandRRULE("FREQ=MINUTELY", dtstart, "Europe/Berlin", from, to, 5000)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 1380 {
			t.Fatalf(
				"expected exactly 1380 unique minutely instants (1440 wall-clock labels minus 60 "+
					"spring-forward collisions), got %d", len(instants),
			)
		}

		seen := make(map[int64]bool, len(instants))
		for i, ms := range instants {
			if seen[ms] {
				t.Fatalf("occurrence[%d] = %d is a duplicate instant — dedup failed", i, ms)
			}
			seen[ms] = true
			if i > 0 && ms <= instants[i-1] {
				t.Fatalf(
					"occurrence[%d] = %d is not strictly after occurrence[%d] = %d (not ascending)",
					i, ms, i-1, instants[i-1],
				)
			}
		}
	})

	t.Run("non-transition civil day: exact 1440, unaffected by the fix", func(t *testing.T) {
		day := time.Date(2026, 3, 24, 0, 0, 0, 0, berlin)
		from := day.UnixMilli()
		to := day.AddDate(0, 0, 1).UnixMilli()

		instants, truncated, err := ExpandRRULE("FREQ=MINUTELY", dtstart, "Europe/Berlin", from, to, 5000)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 1440 {
			t.Fatalf("expected exactly 1440 instants (full day, no DST), got %d", len(instants))
		}
	})
}

// TestCountRegularInRange_MinutelyDSTDedup pins CountRegularInRange's
// spring-forward subtraction (springForwardCollisionCount, rrule.go) for
// FREQ=MINUTELY: the naive k-range count over the Berlin 2026-03-29
// spring-forward civil day is the raw 1440 wall-clock labels; subtracting
// the 60-label collision (see TestRruleExpansion_MinutelyDSTDedup) makes it
// exact at 1380, matching ExpandRRULE's deduped count exactly (not merely
// within a documented ±1). A non-transition day is unaffected (exact 1440,
// the plain no-DST case, matching CountRegularInRange's pre-existing
// behavior).
func TestCountRegularInRange_MinutelyDSTDedup(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")
	dtstart := time.Date(2026, 3, 20, 0, 0, 0, 0, berlin).UnixMilli()

	t.Run("spring-forward civil day: exact 1380, not the raw 1440", func(t *testing.T) {
		from := time.Date(2026, 3, 29, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 3, 30, 0, 0, 0, 0, berlin).UnixMilli()

		count, firstMs, hasAny, err := CountRegularInRange("FREQ=MINUTELY", dtstart, "Europe/Berlin", from, to)
		if err != nil {
			t.Fatalf("CountRegularInRange: %v", err)
		}
		if !hasAny {
			t.Fatalf("expected hasAny=true")
		}
		if count != 1380 {
			t.Fatalf("expected count=1380, got %d", count)
		}
		if firstMs != from {
			t.Fatalf("expected firstMs=%d (dtstart is minute-aligned), got %d", from, firstMs)
		}
	})

	t.Run("non-transition civil day: exact 1440", func(t *testing.T) {
		from := time.Date(2026, 3, 24, 0, 0, 0, 0, berlin).UnixMilli()
		to := time.Date(2026, 3, 25, 0, 0, 0, 0, berlin).UnixMilli()

		count, _, hasAny, err := CountRegularInRange("FREQ=MINUTELY", dtstart, "Europe/Berlin", from, to)
		if err != nil {
			t.Fatalf("CountRegularInRange: %v", err)
		}
		if !hasAny {
			t.Fatalf("expected hasAny=true")
		}
		if count != 1440 {
			t.Fatalf("expected count=1440 (a full 24h day at 1/min, no DST), got %d", count)
		}
	})
}

// TestNextOccurrenceAfter_MinutelyDSTProgress pins the REQUIRED property
// documented on NextOccurrenceAfter (rrule.go): even though seekK's binary
// search assumes regularOccurrenceAt(k) is monotonic in k — briefly false
// inside a spring-forward collision window — every returned occurrence is
// still strictly greater than the afterMs it was asked for, by construction
// (seekK re-verifies its predicate at the k it settles on, it never assumes
// it). A scheduler repeatedly re-arming off this function's own result must
// therefore always advance: never stall, never repeat, never go backward,
// even stepping straight across the Berlin 2026-03-29 transition.
func TestNextOccurrenceAfter_MinutelyDSTProgress(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")
	dtstart := time.Date(2026, 3, 29, 0, 0, 0, 0, berlin).UnixMilli()

	after := time.Date(2026, 3, 29, 1, 30, 0, 0, berlin).UnixMilli() // shortly before the gap
	end := time.Date(2026, 3, 29, 5, 0, 0, 0, berlin).UnixMilli()    // comfortably past the gap

	prev := after
	steps := 0
	for prev < end {
		got, ok, err := NextOccurrenceAfter("FREQ=MINUTELY", dtstart, "Europe/Berlin", prev)
		if err != nil {
			t.Fatalf("NextOccurrenceAfter(%d): %v", prev, err)
		}
		if !ok {
			t.Fatalf("NextOccurrenceAfter(%d): expected ok=true (MINUTELY never exhausts)", prev)
		}
		if got <= prev {
			t.Fatalf("NextOccurrenceAfter(%d) = %d did not strictly advance (stall/repeat/backward)", prev, got)
		}
		prev = got
		steps++
		if steps > 1000 {
			t.Fatalf("exceeded 1000 steps without reaching %d — looks like a stall loop", end)
		}
	}
	if steps == 0 {
		t.Fatalf("test setup error: loop never ran")
	}
}

// ---------------------------------------------------------------------------
// Test 5: TestRruleExpansion_Monthly31Clamp
// ---------------------------------------------------------------------------

func TestRruleExpansion_Monthly31Clamp(t *testing.T) {
	dtstart := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC).UnixMilli()
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	instants, truncated, err := ExpandRRULE(
		"FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1",
		dtstart,
		"UTC",
		from,
		to,
		10,
	)
	if err != nil {
		t.Fatalf("ExpandRRULE: %v", err)
	}
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	wantDays := []int{31, 31, 30, 31}
	wantMonths := []time.Month{time.July, time.August, time.September, time.October}
	if len(instants) != len(wantDays) {
		t.Fatalf("expected %d occurrences, got %d: %v", len(wantDays), len(instants), instants)
	}
	for i, ms := range instants {
		got := time.UnixMilli(ms).UTC()
		if got.Day() != wantDays[i] || got.Month() != wantMonths[i] {
			t.Errorf("occurrence %d: expected %s %d, got %s %d", i, wantMonths[i], wantDays[i], got.Month(), got.Day())
		}
		if got.Hour() != 9 {
			t.Errorf("occurrence %d: expected hour 9, got %d", i, got.Hour())
		}
	}
}

// ---------------------------------------------------------------------------
// Test 6: TestRruleExpansion_EndConditions
// ---------------------------------------------------------------------------

func TestRruleExpansion_EndConditions(t *testing.T) {
	t.Run("COUNT exhausted (regular)", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		from := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC).UnixMilli() // after the 3rd (Jan 3) occurrence
		to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		instants, truncated, err := ExpandRRULE("FREQ=DAILY;COUNT=3", dtstart, "UTC", from, to, 100)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 0 {
			t.Fatalf("expected no occurrences beyond COUNT exhaustion, got %v", instants)
		}
	})

	t.Run("past UNTIL (regular)", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		from := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC).UnixMilli()
		to := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		instants, truncated, err := ExpandRRULE("FREQ=DAILY;UNTIL=20260105T090000Z", dtstart, "UTC", from, to, 100)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 0 {
			t.Fatalf("expected no occurrences past UNTIL, got %v", instants)
		}
	})

	t.Run("COUNT exhausted (irregular)", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC).UnixMilli() // a Monday
		from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		instants, truncated, err := ExpandRRULE("FREQ=WEEKLY;BYDAY=MO;COUNT=2", dtstart, "UTC", from, to, 100)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 0 {
			t.Fatalf("expected no occurrences beyond COUNT exhaustion, got %v", instants)
		}
	})

	t.Run("past UNTIL (irregular)", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC).UnixMilli()
		from := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		to := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		instants, truncated, err := ExpandRRULE(
			"FREQ=WEEKLY;BYDAY=MO;UNTIL=20260112T090000Z",
			dtstart,
			"UTC",
			from,
			to,
			100,
		)
		if err != nil {
			t.Fatalf("ExpandRRULE: %v", err)
		}
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 0 {
			t.Fatalf("expected no occurrences past UNTIL, got %v", instants)
		}
	})
}

// ---------------------------------------------------------------------------
// NextOccurrenceAfter
// ---------------------------------------------------------------------------

func TestNextOccurrenceAfter_Regular(t *testing.T) {
	dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	after := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC).UnixMilli() // exactly an occurrence instant
	got, ok, err := NextOccurrenceAfter("FREQ=DAILY", dtstart, "UTC", after)
	if err != nil {
		t.Fatalf("NextOccurrenceAfter: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	want := time.Date(2026, 1, 6, 9, 0, 0, 0, time.UTC).UnixMilli()
	if got != want {
		t.Fatalf("expected %d (%v), got %d (%v)", want, time.UnixMilli(want).UTC(), got, time.UnixMilli(got).UTC())
	}
}

func TestNextOccurrenceAfter_CountExhausted(t *testing.T) {
	dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	after := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC).UnixMilli()
	_, ok, err := NextOccurrenceAfter("FREQ=DAILY;COUNT=3", dtstart, "UTC", after)
	if err != nil {
		t.Fatalf("NextOccurrenceAfter: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false (series exhausted)")
	}
}

func TestNextOccurrenceAfter_PastUntil(t *testing.T) {
	dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	after := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC).UnixMilli()
	_, ok, err := NextOccurrenceAfter("FREQ=DAILY;UNTIL=20260105T090000Z", dtstart, "UTC", after)
	if err != nil {
		t.Fatalf("NextOccurrenceAfter: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false (past UNTIL)")
	}
}

// TestNextOccurrenceAfter_AgedDtstartRegular pins the Aged DTSTART policy for
// a provably-regular rule: DTSTART is 6 years before `after`, FREQ=MINUTELY —
// a naive walk-from-DTSTART would evaluate millions of candidates. The
// arithmetic (seekK) path must be effectively instantaneous.
func TestNextOccurrenceAfter_AgedDtstartRegular(t *testing.T) {
	dtstart := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	after := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC).UnixMilli() // exact minute boundary

	start := time.Now()
	got, ok, err := NextOccurrenceAfter("FREQ=MINUTELY", dtstart, "UTC", after)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("NextOccurrenceAfter: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("expected O(1)-bounded arithmetic, took %v (looks like a DTSTART walk)", elapsed)
	}
	want := after + 60*1000 // next whole minute after an exact minute boundary
	if got != want {
		t.Fatalf("expected %d (%v), got %d (%v)", want, time.UnixMilli(want).UTC(), got, time.UnixMilli(got).UTC())
	}
}

// TestNextOccurrenceAfter_AgedDtstartIrregular pins the Aged DTSTART
// fast-forward for a COUNT-free irregular (BY*-bearing) rule: DTSTART is 6
// years before `after`. The fast-forward anchor must land rrule-go's
// iterator near `after`, not walk from the original DTSTART. Correctness is
// cross-checked against ExpandRRULE over a narrow forward window (the two
// entry points must agree — Timezone Semantics §4).
func TestNextOccurrenceAfter_AgedDtstartIrregular(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")
	dtstart := time.Date(2020, 6, 15, 9, 0, 0, 0, berlin).UnixMilli()
	after := time.Date(2026, 7, 19, 0, 0, 0, 0, berlin).UnixMilli()

	start := time.Now()
	got, ok, err := NextOccurrenceAfter("FREQ=WEEKLY;BYDAY=MO,WE,FR", dtstart, "Europe/Berlin", after)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("NextOccurrenceAfter: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected a bounded fast-forward, took %v (looks like a DTSTART walk)", elapsed)
	}
	lt := time.UnixMilli(got).In(berlin)
	if lt.Hour() != 9 || lt.Minute() != 0 {
		t.Fatalf("expected 09:00 local, got %v", lt)
	}
	switch lt.Weekday() {
	case time.Monday, time.Wednesday, time.Friday:
	default:
		t.Fatalf("expected Mon/Wed/Fri, got %v", lt.Weekday())
	}
	if got <= after {
		t.Fatalf("expected strictly after %d, got %d", after, got)
	}

	// Cross-check: ExpandRRULE over a narrow forward window must agree.
	window := int64(9 * 24 * 60 * 60 * 1000) // 9 days, comfortably covers Mon/Wed/Fri
	instants, _, err := ExpandRRULE("FREQ=WEEKLY;BYDAY=MO,WE,FR", dtstart, "Europe/Berlin", after, after+window, 10)
	if err != nil {
		t.Fatalf("ExpandRRULE: %v", err)
	}
	if len(instants) == 0 {
		t.Fatalf("ExpandRRULE returned no occurrences in the cross-check window")
	}
	if instants[0] != got {
		t.Fatalf("NextOccurrenceAfter (%d) disagrees with ExpandRRULE's first result (%d)", got, instants[0])
	}
}

// ---------------------------------------------------------------------------
// ProjectEveryMs (FR-008a)
// ---------------------------------------------------------------------------

func TestProjectEveryMs(t *testing.T) {
	const everyMs = 30 * 60 * 1000 // 30 minutes
	firstMs := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()

	t.Run("first entry matches the armed engine state", func(t *testing.T) {
		to := firstMs + 3*everyMs
		instants, truncated := ProjectEveryMs(firstMs, everyMs, firstMs, to, 100)
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) != 3 {
			t.Fatalf("expected 3 occurrences, got %d: %v", len(instants), instants)
		}
		if instants[0] != firstMs {
			t.Fatalf("first entry must equal firstMs (NextRunAtMS): got %d want %d", instants[0], firstMs)
		}
		for k, ms := range instants {
			want := firstMs + int64(k)*everyMs
			if ms != want {
				t.Fatalf("entry %d: want %d got %d", k, want, ms)
			}
		}
	})

	t.Run("forward-only: occurrences before fromMs are omitted", func(t *testing.T) {
		from := firstMs + everyMs + 1 // just past the 2nd occurrence
		to := firstMs + 5*everyMs
		instants, truncated := ProjectEveryMs(firstMs, everyMs, from, to, 100)
		if truncated {
			t.Fatalf("unexpected truncation")
		}
		if len(instants) == 0 {
			t.Fatalf("expected at least one occurrence")
		}
		if instants[0] < from {
			t.Fatalf("first returned occurrence %d is before fromMs %d", instants[0], from)
		}
		if instants[0] != firstMs+2*everyMs {
			t.Fatalf("expected the first in-range occurrence to be the 3rd (k=2), got %d", instants[0])
		}
	})

	t.Run("cap truncates", func(t *testing.T) {
		to := firstMs + 100*everyMs
		instants, truncated := ProjectEveryMs(firstMs, everyMs, firstMs, to, 5)
		if !truncated {
			t.Fatalf("expected truncated=true")
		}
		if len(instants) != 5 {
			t.Fatalf("expected exactly cap(5) occurrences, got %d", len(instants))
		}
	})
}

// ---------------------------------------------------------------------------
// IsRegular + arithmetic derivation
// ---------------------------------------------------------------------------

func TestIsRegular(t *testing.T) {
	dtstartSafe := time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC).UnixMilli()
	dtstartMonthly31 := time.Date(2026, 1, 31, 9, 0, 0, 0, time.UTC).UnixMilli()
	dtstartFeb29 := time.Date(2024, 2, 29, 9, 0, 0, 0, time.UTC).UnixMilli() // 2024 is a leap year

	cases := []struct {
		name      string
		rrule     string
		dtstartMs int64
		want      bool
	}{
		{"plain MINUTELY", "FREQ=MINUTELY", dtstartSafe, true},
		{"plain DAILY", "FREQ=DAILY;INTERVAL=3", dtstartSafe, true},
		{"plain WEEKLY (no BYDAY)", "FREQ=WEEKLY;INTERVAL=2", dtstartSafe, true},
		{"WEEKLY with explicit BYDAY", "FREQ=WEEKLY;BYDAY=MO", dtstartSafe, false},
		{"MONTHLY on day 15", "FREQ=MONTHLY", dtstartSafe, true},
		{"MONTHLY on day 31 (unsafe)", "FREQ=MONTHLY", dtstartMonthly31, false},
		{"YEARLY on a normal date", "FREQ=YEARLY", dtstartSafe, true},
		{"YEARLY on Feb 29 (unsafe)", "FREQ=YEARLY", dtstartFeb29, false},
		{"monthly-31 clamp form", "FREQ=MONTHLY;BYMONTHDAY=28,29,30,31;BYSETPOS=-1", dtstartMonthly31, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := IsRegular(c.rrule, c.dtstartMs, "UTC")
			if err != nil {
				t.Fatalf("IsRegular: %v", err)
			}
			if got != c.want {
				t.Fatalf("IsRegular(%q) = %v, want %v", c.rrule, got, c.want)
			}
		})
	}
}

// TestRegularArithmetic_LongSpanComplete pins dataset row 17: a valid plain
// FREQ=MINUTELY rule renders a complete (non-truncated) result over a long
// span, in bounded server time — no iteration proportional to the span.
func TestRegularArithmetic_LongSpanComplete(t *testing.T) {
	dtstart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC).UnixMilli() // 10 days = 14,400 minutes

	start := time.Now()
	instants, truncated, err := ExpandRRULE("FREQ=MINUTELY", dtstart, "UTC", from, to, 20000)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ExpandRRULE: %v", err)
	}
	if truncated {
		t.Fatalf("expected truncated=false for a cap well above the in-range count")
	}
	wantCount := 10 * 24 * 60
	if len(instants) != wantCount {
		t.Fatalf("expected %d occurrences, got %d", wantCount, len(instants))
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected bounded server time, took %v", elapsed)
	}
}

// TestCountRegularInRange_AgedDtstartLongSpan pins the O(1)-arithmetic
// bucket/count derivation (dataset row 17d combined with row 17): DTSTART 2
// years before a 400-day queried span, regular dense (MINUTELY) rule ->
// correct in-range count in bounded server time, no walk from DTSTART and no
// per-occurrence iteration across the 400-day span.
func TestCountRegularInRange_AgedDtstartLongSpan(t *testing.T) {
	dtstart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	to := time.Date(2027, 2, 4, 0, 0, 0, 0, time.UTC).UnixMilli() // ~400 days

	start := time.Now()
	count, firstMs, hasAny, err := CountRegularInRange("FREQ=MINUTELY", dtstart, "UTC", from, to)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("CountRegularInRange: %v", err)
	}
	if !hasAny {
		t.Fatalf("expected hasAny=true")
	}
	if firstMs != from {
		t.Fatalf("expected first occurrence to equal fromMs (dtstart is minute-aligned), got %d want %d", firstMs, from)
	}
	wantCount := int((to - from) / (60 * 1000))
	if count != wantCount {
		t.Fatalf("expected count %d, got %d", wantCount, count)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf(
			"expected O(1)-bounded arithmetic, took %v (looks like an iteration proportional to span/history)",
			elapsed,
		)
	}

	periodMs, ok, err := RegularPeriodMs("FREQ=MINUTELY", dtstart, "UTC")
	if err != nil {
		t.Fatalf("RegularPeriodMs: %v", err)
	}
	if !ok || periodMs != 60*1000 {
		t.Fatalf("expected periodMs=60000 ok=true, got %d ok=%v", periodMs, ok)
	}
}

// ---------------------------------------------------------------------------
// Validation-support expander + liveness probe
// ---------------------------------------------------------------------------

func TestExpandForValidation(t *testing.T) {
	t.Run("weekly rule bounded by the 366-day window", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC).UnixMilli() // a Monday
		instants, err := ExpandForValidation("FREQ=WEEKLY;BYDAY=MO", dtstart, "UTC")
		if err != nil {
			t.Fatalf("ExpandForValidation: %v", err)
		}
		// ~366/7 ≈ 52 weekly occurrences fit before the 60-occurrence cap binds.
		if len(instants) < 50 || len(instants) > 54 {
			t.Fatalf("expected roughly 52 occurrences within 366 days, got %d", len(instants))
		}
		for i := 1; i < len(instants); i++ {
			if instants[i] <= instants[i-1] {
				t.Fatalf("occurrences must be strictly ascending: [%d]=%d [%d]=%d", i-1, instants[i-1], i, instants[i])
			}
		}
		windowEnd := time.UnixMilli(dtstart).AddDate(0, 0, 366).UnixMilli()
		if instants[len(instants)-1] >= windowEnd {
			t.Fatalf("last occurrence %d exceeds the 366-day window end %d", instants[len(instants)-1], windowEnd)
		}
	})

	t.Run("60-occurrence cap binds for a dense rule", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		instants, err := ExpandForValidation("FREQ=DAILY", dtstart, "UTC")
		if err != nil {
			t.Fatalf("ExpandForValidation: %v", err)
		}
		if len(instants) != 60 {
			t.Fatalf("expected exactly 60 occurrences (cap), got %d", len(instants))
		}
	})

	t.Run("never-matching Feb-31 rule returns empty quickly", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		start := time.Now()
		instants, err := ExpandForValidation("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31", dtstart, "UTC")
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("ExpandForValidation: %v", err)
		}
		if len(instants) != 0 {
			t.Fatalf("expected no occurrences for a never-matching rule, got %v", instants)
		}
		if elapsed > time.Second {
			t.Fatalf("expected completion within 1s (liveness bound), took %v", elapsed)
		}
	})
}

func TestHasOccurrenceWithinYears(t *testing.T) {
	t.Run("normal rule is live", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		ok, err := HasOccurrenceWithinYears("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", dtstart, "UTC", 5)
		if err != nil {
			t.Fatalf("HasOccurrenceWithinYears: %v", err)
		}
		if !ok {
			t.Fatalf("expected a Feb-29 yearly rule to fire within 5 years (a leap year must occur)")
		}
	})

	t.Run("Feb-31 never fires, returns quickly", func(t *testing.T) {
		dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
		start := time.Now()
		ok, err := HasOccurrenceWithinYears("FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31", dtstart, "UTC", 5)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("HasOccurrenceWithinYears: %v", err)
		}
		if ok {
			t.Fatalf("expected false for a never-matching rule")
		}
		if elapsed > time.Second {
			t.Fatalf("expected completion within 1s (liveness bound), took %v", elapsed)
		}
	})
}

// ---------------------------------------------------------------------------
// Misc contract checks
// ---------------------------------------------------------------------------

func TestExpandRRULE_InvalidRange(t *testing.T) {
	dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	if _, _, err := ExpandRRULE("FREQ=DAILY", dtstart, "UTC", 1000, 1000, 10); err == nil {
		t.Fatalf("expected an error for from_ms == to_ms")
	}
	if _, _, err := ExpandRRULE("FREQ=DAILY", dtstart, "UTC", 2000, 1000, 10); err == nil {
		t.Fatalf("expected an error for from_ms > to_ms")
	}
}

func TestExpandRRULE_UnknownTimezone(t *testing.T) {
	dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	if _, _, err := ExpandRRULE("FREQ=DAILY", dtstart, "Not/AZone", 0, 1000, 10); err == nil {
		t.Fatalf("expected an error for an unloadable timezone")
	}
}

func TestExpandRRULE_ParseError(t *testing.T) {
	dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	if _, _, err := ExpandRRULE("FREQ=BOGUS", dtstart, "UTC", 0, 1000, 10); err == nil {
		t.Fatalf("expected an error for an unparseable rrule")
	}
}
