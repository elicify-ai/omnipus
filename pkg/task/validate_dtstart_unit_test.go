// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// validate_dtstart_unit_test.go pins #1771: a recurring trigger whose
// config.dtstart_ms is obviously Unix SECONDS (before 2000-01-01 as
// milliseconds) is refused with an error that names the field and the unit,
// instead of silently anchoring the schedule in January 1970.

package task

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTrigger_DtstartMsLooksLikeSeconds(t *testing.T) {
	rule := "FREQ=WEEKLY;BYDAY=MO"
	tz := "Europe/Berlin"
	build := func(ms int64) *Trigger {
		return &Trigger{Type: TriggerRecurring, Config: TriggerConfig{Rrule: &rule, DtstartMs: &ms, Tz: &tz}}
	}

	t.Run("seconds value from the #1771 report is refused with field and unit", func(t *testing.T) {
		err := ValidateTrigger(build(1794816000))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrValidation))
		assert.Contains(t, err.Error(), "config.dtstart_ms")
		assert.Contains(t, err.Error(), "milliseconds")
		assert.Contains(t, err.Error(), "1794816000")
		assert.Contains(t, err.Error(), "seconds")
	})

	t.Run("just below 2000-01-01 in ms is refused", func(t *testing.T) {
		require.Error(t, ValidateTrigger(build(946684799999)))
	})

	t.Run("exactly 2000-01-01T00:00:00Z in ms is accepted", func(t *testing.T) {
		require.NoError(t, ValidateTrigger(build(946684800000)))
	})

	t.Run("a real millisecond time (2026-11-16 08:00 UTC) is accepted", func(t *testing.T) {
		require.NoError(t, ValidateTrigger(build(1794816000000)))
	})

	t.Run("zero and negative are refused", func(t *testing.T) {
		require.Error(t, ValidateTrigger(build(0)))
		require.Error(t, ValidateTrigger(build(-5)))
	})
}
