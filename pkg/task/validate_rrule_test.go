// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// validate_rrule_test.go covers the RRULE branch of ValidateTrigger added for
// the Calendar Recurrence Redesign
// (docs/internal/specs/calendar-recurrence-redesign-spec.md, normative
// "Validation (ValidateTrigger, RRULE path)" section, §1-6). Test names and
// row coverage follow the spec's "Test Implementation Order" (tests 1-3) and
// "RRULE validation (server, ValidateTrigger)" Test Dataset (rows 1-15).
//
// Helpers reused from sibling test files in this package: ptr[T]
// (coverage_test.go) and mustLoc (rrule_test.go).

package task

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dtstartMsAt is a small helper building a Unix-ms DTSTART anchor from
// calendar fields in the given IANA zone.
func dtstartMsAt(t *testing.T, tz string, y int, mo time.Month, d, h, mi, s int) int64 {
	t.Helper()
	loc := mustLoc(t, tz)
	return time.Date(y, mo, d, h, mi, s, 0, loc).UnixMilli()
}

// ---------------------------------------------------------------------------
// Test 1: TestValidateTrigger_RruleRequiredSiblings
//
// Dataset rows: 2 (cron+rrule both present), 3 (neither), 4 (rrule valid,
// missing tz / missing dtstart_ms), 5 (unloadable tz), 14 (unparseable
// rrule) — plus the §1 "legal only on type: recurring" reject on once/
// every/manual.
// ---------------------------------------------------------------------------

func TestValidateTrigger_RruleRequiredSiblings(t *testing.T) {
	validRrule := "FREQ=DAILY"
	validCron := "0 9 * * *"
	dtstart := dtstartMsAt(t, "UTC", 2026, 1, 5, 9, 0, 0)
	tz := "UTC"

	t.Run("row2: both cron_expr and rrule present -> 400", func(t *testing.T) {
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			CronExpr:  &validCron,
			Rrule:     &validRrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "cron_expr + rrule together must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row3: neither cron_expr nor rrule -> 400", func(t *testing.T) {
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "recurring trigger with neither cron_expr nor rrule must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row4: rrule valid, missing tz -> 400", func(t *testing.T) {
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &validRrule,
			DtstartMs: &dtstart,
			// Tz omitted.
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "rrule without tz must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row4b: rrule valid, missing dtstart_ms -> 400", func(t *testing.T) {
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule: &validRrule,
			Tz:    &tz,
			// DtstartMs omitted.
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "rrule without dtstart_ms must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row5: unloadable tz -> 400", func(t *testing.T) {
		badTz := "Not/AZone"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &validRrule,
			DtstartMs: &dtstart,
			Tz:        &badTz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "unloadable tz must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row14: unparseable rrule -> 400", func(t *testing.T) {
		bogus := "FREQ=BOGUS"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &bogus,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "an rrule that fails to parse must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("rrule/dtstart_ms/tz illegal on type=once", func(t *testing.T) {
		atMs := int64(1234567890000)
		tr := &Trigger{Type: TriggerOnce, Config: TriggerConfig{
			AtMs:      &atMs,
			Rrule:     &validRrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "rrule/dtstart_ms/tz keys must be rejected on a once trigger")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("rrule/dtstart_ms/tz illegal on type=every", func(t *testing.T) {
		everyMs := int64(60000)
		tr := &Trigger{Type: TriggerEvery, Config: TriggerConfig{
			EveryMs:   &everyMs,
			Rrule:     &validRrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "rrule/dtstart_ms/tz keys must be rejected on an every trigger")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("rrule/dtstart_ms/tz illegal on type=manual", func(t *testing.T) {
		tr := &Trigger{Type: TriggerManual, Config: TriggerConfig{
			Rrule:     &validRrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "rrule/dtstart_ms/tz keys must be rejected on a manual trigger")
		assert.True(t, errors.Is(err, ErrValidation))
	})
}

// ---------------------------------------------------------------------------
// Test 2: TestValidateTrigger_RruleBoundedMinGap
//
// Dataset rows: 6 (MINUTELY, exactly 60s apart -> accept), 13 (COUNT=1 ->
// trivially accept, <2 occurrences). The adversarial sub-minute rules (rows
// 7-8) are §2's job and are asserted in TestValidateTrigger_RruleInputBounds.
// ---------------------------------------------------------------------------

func TestValidateTrigger_RruleBoundedMinGap(t *testing.T) {
	tz := "UTC"
	dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 0)

	t.Run("row6: FREQ=MINUTELY (60s apart) accepted at the floor", func(t *testing.T) {
		rrule := "FREQ=MINUTELY"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.NoError(t, err, "a rule exactly 60s apart must be accepted")
	})

	t.Run("row13: COUNT=1 trivially accepted (fewer than 2 occurrences)", func(t *testing.T) {
		rrule := "FREQ=DAILY;COUNT=1"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.NoError(t, err, "COUNT=1 yields <2 occurrences and must trivially pass the min-gap floor")
	})
}

// ---------------------------------------------------------------------------
// Test 3: TestValidateTrigger_RruleInputBounds
//
// Dataset rows: 1 (minimal valid -> accept), 7 & 8 (foreign BYSECOND -> 400),
// 8a (COUNT=100001 -> 400), 9 (FREQ=SECONDLY -> 400), 10 (never-matching
// Feb-31 -> 400 within a bounded time), 11 (>512-char -> 400), 12 (UNTIL <
// DTSTART -> 400), 15 (legacy cron_expr alone -> accept, unchanged).
// ---------------------------------------------------------------------------

func TestValidateTrigger_RruleInputBounds(t *testing.T) {
	tz := "UTC"

	t.Run("row1: minimal valid FREQ=WEEKLY;BYDAY=MO accepted", func(t *testing.T) {
		dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 0) // 2026-01-05 is a Monday
		rrule := "FREQ=WEEKLY;BYDAY=MO"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.NoError(t, err, "minimal weekly-Monday rrule must be accepted")
	})

	t.Run("row9: FREQ=SECONDLY rejected outright", func(t *testing.T) {
		dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 0)
		rrule := "FREQ=SECONDLY"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "FREQ=SECONDLY must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row7: foreign BYSECOND rejected (first pair looks ~24h apart, defeats a naive first-pair check)", func(t *testing.T) {
		// DTSTART second is 15; BYSECOND=0,30 shares neither value.
		dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 15)
		rrule := "FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0,30"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "BYSECOND values foreign to the DTSTART second must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row8: foreign BYSECOND rejected (30s gaps)", func(t *testing.T) {
		// DTSTART second is 0; BYSECOND=0,30 includes a foreign value (30).
		dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 0)
		rrule := "FREQ=MINUTELY;BYSECOND=0,30"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "BYSECOND=0,30 with DTSTART second 0 must be rejected via the foreign value 30")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row10: never-matching Feb-31 rule rejected within a bounded time", func(t *testing.T) {
		dtstart := dtstartMsAt(t, tz, 2026, 1, 1, 0, 0, 0)
		rrule := "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		start := time.Now()
		err := ValidateTrigger(tr)
		elapsed := time.Since(start)
		require.Error(t, err, "a rule that can never fire (Feb 31) must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
		assert.Lessf(t, elapsed, 2*time.Second,
			"liveness bound must complete quickly, not stall; took %s", elapsed)
	})

	t.Run("row11: rrule longer than 512 characters rejected", func(t *testing.T) {
		dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 0)
		huge := "FREQ=DAILY;X-PAD=" + strings.Repeat("A", 10*1024)
		require.Greater(t, len(huge), 512)
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &huge,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "an rrule over 512 characters must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row12: UNTIL before DTSTART rejected", func(t *testing.T) {
		dtstart := dtstartMsAt(t, tz, 2026, 1, 10, 9, 0, 0)
		rrule := "FREQ=DAILY;UNTIL=20260109T090000Z" // 1 day before dtstart
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "UNTIL preceding DTSTART must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row8a: COUNT over the 100,000 cap rejected", func(t *testing.T) {
		dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 0)
		rrule := "FREQ=DAILY;COUNT=100001"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.Error(t, err, "COUNT=100001 exceeds the 100,000 cap and must be rejected")
		assert.True(t, errors.Is(err, ErrValidation))
	})

	t.Run("row8a-boundary: COUNT at exactly the 100,000 cap accepted", func(t *testing.T) {
		dtstart := dtstartMsAt(t, tz, 2026, 1, 5, 9, 0, 0)
		rrule := "FREQ=DAILY;COUNT=100000"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{
			Rrule:     &rrule,
			DtstartMs: &dtstart,
			Tz:        &tz,
		}}
		err := ValidateTrigger(tr)
		require.NoError(t, err, "COUNT=100000 is exactly at the cap and must be accepted")
	})

	t.Run("row15: legacy cron_expr alone accepted unchanged", func(t *testing.T) {
		cron := "0 9 * * MON"
		tr := &Trigger{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &cron}}
		err := ValidateTrigger(tr)
		require.NoError(t, err, "legacy cron_expr-only trigger must continue to validate unchanged")
	})
}
