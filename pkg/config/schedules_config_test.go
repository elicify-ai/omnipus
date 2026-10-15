package config

import "testing"

// TestSchedulesConfig_ApplyDefaults verifies the 8-run default and the
// bounds-check that resets a non-positive value (#264 FR-007). There is no
// run-timeout default any more: scheduled runs have no time limit (#1306).
func TestSchedulesConfig_ApplyDefaults(t *testing.T) {
	cases := []struct {
		name    string
		in      SchedulesConfig
		wantMax int
	}{
		{"zero -> defaults", SchedulesConfig{}, 8},
		{"negative -> defaults", SchedulesConfig{MaxConcurrentRuns: -3}, 8},
		{"explicit kept", SchedulesConfig{MaxConcurrentRuns: 4}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.in
			s.ApplyDefaults()
			if s.MaxConcurrentRuns != tc.wantMax {
				t.Fatalf("MaxConcurrentRuns = %d, want %d", s.MaxConcurrentRuns, tc.wantMax)
			}
			// Idempotent.
			s.ApplyDefaults()
			if s.MaxConcurrentRuns != tc.wantMax {
				t.Fatalf("ApplyDefaults not idempotent: %+v", s)
			}
		})
	}
}

// TestDefaultConfig_SchedulesSeeded verifies a fresh DefaultConfig carries the
// schedules guardrail defaults.
func TestDefaultConfig_SchedulesSeeded(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Schedules.MaxConcurrentRuns != 8 {
		t.Fatalf("default MaxConcurrentRuns = %d, want 8", cfg.Schedules.MaxConcurrentRuns)
	}
}
