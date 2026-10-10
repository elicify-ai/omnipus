// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"testing"
)

// These tests pin the RESOLVED VALUE a caller actually gets for each new key —
// unset, set globally, and overridden per-plan — plus the round-trip through
// config.json that the resolution depends on. A test that only proved the
// struct field exists would pass against a key nothing ever reads.

func intPtr(v int) *int { return &v }

func TestEffectiveSupervisionTurnTimeoutSeconds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cfg      PlanningConfig
		override *int
		want     int
	}{
		{"unset resolves to the documented default", PlanningConfig{}, nil, DefaultSupervisionTurnTimeoutSeconds},
		{"explicit global wins over the default", PlanningConfig{SupervisionTurnTimeoutSeconds: 45}, nil, 45},
		{"per-plan override wins over the global", PlanningConfig{SupervisionTurnTimeoutSeconds: 45}, intPtr(90), 90},
		{"per-plan override wins over the default", PlanningConfig{}, intPtr(90), 90},
		{"zero global is 'unset', not a zero timeout", PlanningConfig{SupervisionTurnTimeoutSeconds: 0}, nil, DefaultSupervisionTurnTimeoutSeconds},
		{"out-of-range override is ignored, not honored", PlanningConfig{SupervisionTurnTimeoutSeconds: 45}, intPtr(0), 45},
		{"negative override is ignored", PlanningConfig{}, intPtr(-5), DefaultSupervisionTurnTimeoutSeconds},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cfg.EffectiveSupervisionTurnTimeoutSeconds(tc.override); got != tc.want {
				t.Errorf("EffectiveSupervisionTurnTimeoutSeconds() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEffectiveSupervisionMaxAttempts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cfg      PlanningConfig
		override *int
		want     int
	}{
		{"unset resolves to the documented default", PlanningConfig{}, nil, DefaultSupervisionMaxAttempts},
		{"explicit global wins over the default", PlanningConfig{SupervisionMaxAttempts: 7}, nil, 7},
		{"per-plan override wins over the global", PlanningConfig{SupervisionMaxAttempts: 7}, intPtr(2), 2},
		{"per-plan override wins over the default", PlanningConfig{}, intPtr(2), 2},
		{"zero global is 'unset', not 'never retry'", PlanningConfig{SupervisionMaxAttempts: 0}, nil, DefaultSupervisionMaxAttempts},
		{"out-of-range override is ignored", PlanningConfig{SupervisionMaxAttempts: 7}, intPtr(0), 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cfg.EffectiveSupervisionMaxAttempts(tc.override); got != tc.want {
				t.Errorf("EffectiveSupervisionMaxAttempts() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestSupervisionKeys_DocumentedDefaults pins the two default values
// themselves. They were package constants in pkg/agent
// (defaultSupervisionTurnTimeout = 600 s, defaultSupervisionMaxAttempts = 3)
// before this key existed; moving resolution to config must not silently
// change what an install that configures nothing actually gets.
func TestSupervisionKeys_DocumentedDefaults(t *testing.T) {
	t.Parallel()
	if DefaultSupervisionTurnTimeoutSeconds != 600 {
		t.Errorf("DefaultSupervisionTurnTimeoutSeconds = %d, want 600 (the pre-config package constant)",
			DefaultSupervisionTurnTimeoutSeconds)
	}
	if DefaultSupervisionMaxAttempts != 3 {
		t.Errorf("DefaultSupervisionMaxAttempts = %d, want 3 (the pre-config package constant)",
			DefaultSupervisionMaxAttempts)
	}
}

// TestSupervisionKeys_ConfigJSONRoundTrip proves the two keys survive the trip
// through config.json under the names an operator would write, and that an
// absent key still resolves to the default after unmarshalling (the loadConfig
// shape: start from a value, unmarshal operator JSON on top).
func TestSupervisionKeys_ConfigJSONRoundTrip(t *testing.T) {
	t.Parallel()

	var explicit PlanningConfig
	raw := `{"supervision_turn_timeout_seconds":120,"supervision_max_attempts":5}`
	if err := json.Unmarshal([]byte(raw), &explicit); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := explicit.EffectiveSupervisionTurnTimeoutSeconds(nil); got != 120 {
		t.Errorf("explicitly set timeout = %d, want 120", got)
	}
	if got := explicit.EffectiveSupervisionMaxAttempts(nil); got != 5 {
		t.Errorf("explicitly set max attempts = %d, want 5", got)
	}

	// An operator config that mentions neither key.
	var absent PlanningConfig
	if err := json.Unmarshal([]byte(`{"goal_max_rounds":11}`), &absent); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := absent.EffectiveSupervisionTurnTimeoutSeconds(nil); got != DefaultSupervisionTurnTimeoutSeconds {
		t.Errorf("absent timeout key = %d, want the default %d", got, DefaultSupervisionTurnTimeoutSeconds)
	}
	if got := absent.EffectiveSupervisionMaxAttempts(nil); got != DefaultSupervisionMaxAttempts {
		t.Errorf("absent max-attempts key = %d, want the default %d", got, DefaultSupervisionMaxAttempts)
	}

	// omitempty must not drop a value an operator explicitly set.
	out, err := json.Marshal(explicit)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back PlanningConfig
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.SupervisionTurnTimeoutSeconds != 120 || back.SupervisionMaxAttempts != 5 {
		t.Errorf("round-trip lost the configured values: %s", out)
	}
}
