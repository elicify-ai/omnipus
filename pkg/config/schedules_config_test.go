package config

import (
	"reflect"
	"testing"
)

// TestSchedulesConfig_ApplyDefaults verifies the retry-backoff default and that
// an explicit schedule is kept. There is no run-timeout default (scheduled runs
// have no time limit, #1306) and no concurrency default: the
// schedules.max_concurrent_runs setting was removed (T10 spec r6 FR-07; the
// absence is asserted by t10_schedules_red_test.go).
func TestSchedulesConfig_ApplyDefaults(t *testing.T) {
	cases := []struct {
		name string
		in   SchedulesConfig
		want []int64
	}{
		{"zero -> defaults", SchedulesConfig{}, []int64{60000, 120000, 300000}},
		{"explicit kept", SchedulesConfig{RetryBackoffMs: []int64{1000, 2000}}, []int64{1000, 2000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.in
			s.ApplyDefaults()
			if !reflect.DeepEqual(s.RetryBackoffMs, tc.want) {
				t.Fatalf("RetryBackoffMs = %v, want %v", s.RetryBackoffMs, tc.want)
			}
			// Idempotent.
			s.ApplyDefaults()
			if !reflect.DeepEqual(s.RetryBackoffMs, tc.want) {
				t.Fatalf("ApplyDefaults not idempotent: %+v", s)
			}
		})
	}
}

// TestDefaultConfig_SchedulesSeeded verifies a fresh DefaultConfig carries the
// schedules retry-backoff default.
func TestDefaultConfig_SchedulesSeeded(t *testing.T) {
	cfg := DefaultConfig()
	if want := []int64{60000, 120000, 300000}; !reflect.DeepEqual(cfg.Schedules.RetryBackoffMs, want) {
		t.Fatalf("default RetryBackoffMs = %v, want %v", cfg.Schedules.RetryBackoffMs, want)
	}
}
