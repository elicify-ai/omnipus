// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — ADR-057 U28 unit tests (work items W24b, W17b).
//
// New tests for this unit go in this NEW file per the ADR-057 spec's ownership
// Rule 5 (docs/internal/specs/adr-057-session-unification-spec.md, "Ownership
// derivation" §5) — U28 does not add tests to any existing config test file.
//
// Covers:
//   - FR-067 / test #105 / SC-048 (config-layer portion): the stats-flush
//     interval key exists, defaults to 5s, and a non-default value is
//     honoured — both as a Go value and across a JSON round-trip.
package config

import (
	"encoding/json"
	"testing"
	"time"
)

// TestDefaultConfig_SeedsStatsFlushIntervalAt5s covers FR-067 / #105 / SC-048
// (config-layer portion, U28): a config built the way a fresh install builds
// one seeds session.stats_flush_interval to 5s explicitly, and
// EffectiveStatsFlushInterval resolves the same value.
func TestDefaultConfig_SeedsStatsFlushIntervalAt5s(t *testing.T) {
	cfg := DefaultConfig()

	if got, want := time.Duration(cfg.Session.StatsFlushInterval), DefaultSessionStatsFlushInterval; got != want {
		t.Fatalf("DefaultConfig().Session.StatsFlushInterval = %v, want %v (FR-067 default)", got, want)
	}
	if want := 5 * time.Second; DefaultSessionStatsFlushInterval != want {
		t.Fatalf("DefaultSessionStatsFlushInterval = %v, want exactly %v per FR-067/operator decision 2", DefaultSessionStatsFlushInterval, want)
	}
	if got := cfg.Session.EffectiveStatsFlushInterval(); got != DefaultSessionStatsFlushInterval {
		t.Fatalf("EffectiveStatsFlushInterval() on the seeded config = %v, want %v", got, DefaultSessionStatsFlushInterval)
	}
}

// TestSessionConfig_StatsFlushInterval_UnsetDefaultsTo5s covers the "key
// exists, defaults to 5s" clause of FR-067/#105 independent of DefaultConfig's
// explicit seed: an unset (zero-value) SessionConfig — e.g. an operator's
// config.json predating this key, or one that explicitly zeroes it out —
// still resolves to the 5s default via EffectiveStatsFlushInterval.
func TestSessionConfig_StatsFlushInterval_UnsetDefaultsTo5s(t *testing.T) {
	var unset SessionConfig
	if unset.StatsFlushInterval != 0 {
		t.Fatalf("sanity: zero-value SessionConfig.StatsFlushInterval = %v, want 0", unset.StatsFlushInterval)
	}
	if got := unset.EffectiveStatsFlushInterval(); got != 5*time.Second {
		t.Fatalf("EffectiveStatsFlushInterval() on an unset SessionConfig = %v, want 5s", got)
	}
}

// TestSessionConfig_StatsFlushInterval_NonDefaultHonoured covers the "a
// non-default value is honoured end to end" clause of FR-067/#105/SC-048 at
// the config layer: an explicitly configured value overrides the 5s default,
// both as a Go value and across a JSON round-trip (proving the key is a real,
// wire-visible config.json field, not just an in-memory Go field). The
// `duration` type's two accepted JSON shapes (human string, bare-number
// seconds) are both exercised, matching SessionMessagingConfig's existing
// convention (session_messaging.go).
func TestSessionConfig_StatsFlushInterval_NonDefaultHonoured(t *testing.T) {
	sc := SessionConfig{StatsFlushInterval: duration(10 * time.Second)}
	if got := sc.EffectiveStatsFlushInterval(); got != 10*time.Second {
		t.Fatalf("EffectiveStatsFlushInterval() with an explicit 10s override = %v, want 10s (override not honoured)", got)
	}

	cases := []struct {
		name string
		json string
		want time.Duration
	}{
		{"human string", `{"stats_flush_interval":"10s"}`, 10 * time.Second},
		{"bare number seconds", `{"stats_flush_interval":10}`, 10 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got SessionConfig
			if err := json.Unmarshal([]byte(tc.json), &got); err != nil {
				t.Fatalf("json.Unmarshal(%q) failed: %v", tc.json, err)
			}
			if d := got.EffectiveStatsFlushInterval(); d != tc.want {
				t.Fatalf("after unmarshalling %q, EffectiveStatsFlushInterval() = %v, want %v", tc.json, d, tc.want)
			}
		})
	}

	// Round-trip: the seeded default itself must marshal back out as a
	// present, human-readable key rather than being dropped by omitempty
	// (DefaultSessionStatsFlushInterval is non-zero, so it survives
	// `omitempty`) — proving stats_flush_interval is a real key any operator
	// reading config.json will see, not silently absent on a fresh install.
	cfg := DefaultConfig()
	raw, err := json.Marshal(cfg.Session)
	if err != nil {
		t.Fatalf("json.Marshal(cfg.Session) failed: %v", err)
	}
	var roundTripped map[string]any
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("json.Unmarshal of marshalled Session failed: %v", err)
	}
	if _, ok := roundTripped["stats_flush_interval"]; !ok {
		t.Fatalf("marshalled DefaultConfig().Session is missing \"stats_flush_interval\": %s", raw)
	}
}
