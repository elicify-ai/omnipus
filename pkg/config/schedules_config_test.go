package config

import "testing"

// TestSchedulesConfig_ApplyDefaults verifies the 8 / 300 defaults and the
// bounds-check that resets non-positive values (#264 FR-003/FR-007).
func TestSchedulesConfig_ApplyDefaults(t *testing.T) {
	cases := []struct {
		name       string
		in         SchedulesConfig
		wantMax    int
		wantTimout int
	}{
		{"zero -> defaults", SchedulesConfig{}, 8, 300},
		{"negative -> defaults", SchedulesConfig{MaxConcurrentRuns: -3, RunTimeoutSeconds: -1}, 8, 300},
		{"explicit kept", SchedulesConfig{MaxConcurrentRuns: 4, RunTimeoutSeconds: 600}, 4, 600},
		{"partial", SchedulesConfig{MaxConcurrentRuns: 2}, 2, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.in
			s.ApplyDefaults()
			if s.MaxConcurrentRuns != tc.wantMax {
				t.Fatalf("MaxConcurrentRuns = %d, want %d", s.MaxConcurrentRuns, tc.wantMax)
			}
			if s.RunTimeoutSeconds != tc.wantTimout {
				t.Fatalf("RunTimeoutSeconds = %d, want %d", s.RunTimeoutSeconds, tc.wantTimout)
			}
			// Idempotent.
			s.ApplyDefaults()
			if s.MaxConcurrentRuns != tc.wantMax || s.RunTimeoutSeconds != tc.wantTimout {
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
	if cfg.Schedules.RunTimeoutSeconds != 300 {
		t.Fatalf("default RunTimeoutSeconds = %d, want 300", cfg.Schedules.RunTimeoutSeconds)
	}
}
