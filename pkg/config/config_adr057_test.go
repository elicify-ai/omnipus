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
//   - FR-095 / BDD-108 / test #112 / SC-050 (config-layer portion, jointly
//     owned with U19): the root-delegation cap is SEEDED at 16 on a config
//     built the way a fresh install builds one (config.DefaultConfig(), the
//     exact function loadConfigInternal returns when no config.json exists —
//     see config.go's os.IsNotExist branch), and the grill #2 M2-1 gap this
//     seed closes is reproduced concretely: an UNSEEDED SubTurnConfig (the
//     pre-fix shape, matching `rg SubTurn pkg/config/defaults.go` returning
//     zero matches before this change) leaves MaxConcurrent <= 0, which is
//     the exact guard `getSubTurnConfig` (pkg/agent/subturn.go:64-69, out of
//     this unit's scope — U7 owns it, read-only here) uses to fall through
//     to the hardware-autodetected, non-deterministic
//     PerformanceConfig.EffectiveMaxParallelAgents() value instead of the
//     documented, deterministic 16 FR-095 mandates for the W17 root-
//     delegation gate.
//
// U19's admission.go gate (Wave G) and U7's getSubTurnConfig (already shipped,
// read-only here) are out of scope for this unit — this file exercises only
// the pkg/config surface U28 owns.
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

// TestDefaultConfig_SeedsSubTurnMaxConcurrentAt16 covers FR-095 / BDD-108 /
// #112 / SC-050 (config-layer portion, U28): a config built the way a fresh
// install builds one — config.DefaultConfig(), the exact value
// loadConfigInternal returns on os.IsNotExist(err) with no config.json on
// disk (config.go's os.ReadFile branch) — seeds
// agents.defaults.subturn.max_concurrent at 16, matching operator decision 4
// and closing the branch FR-095 forbids.
func TestDefaultConfig_SeedsSubTurnMaxConcurrentAt16(t *testing.T) {
	cfg := DefaultConfig()

	got := cfg.Agents.Defaults.SubTurn.MaxConcurrent
	if got != 16 {
		t.Fatalf("DefaultConfig().Agents.Defaults.SubTurn.MaxConcurrent = %d, want 16 (FR-095 seeded default, operator decision 4)", got)
	}
	if got != DefaultSubTurnMaxConcurrent {
		t.Fatalf("DefaultConfig().Agents.Defaults.SubTurn.MaxConcurrent = %d, want DefaultSubTurnMaxConcurrent = %d", got, DefaultSubTurnMaxConcurrent)
	}
	if DefaultSubTurnMaxConcurrent <= 0 {
		t.Fatalf("DefaultSubTurnMaxConcurrent = %d must be > 0, else getSubTurnConfig's `if maxConcurrent <= 0` guard (pkg/agent/subturn.go:64-69) fires on every fresh install — exactly the branch FR-095 forbids", DefaultSubTurnMaxConcurrent)
	}

	// SC-050's "the resolution did not pass through
	// Performance.EffectiveMaxParallelAgents()" clause, asserted at the
	// config layer: the seeded value alone must already be positive, so the
	// `<= 0` guard external callers (getSubTurnConfig, and U19's future W17
	// admission gate) apply never has a reason to invoke the auto-detected
	// fallback for a fresh install.
	if cfg.Agents.Defaults.SubTurn.MaxConcurrent <= 0 {
		t.Fatalf("seeded SubTurn.MaxConcurrent = %d is <= 0; a caller applying the documented `if maxConcurrent <= 0` guard would take the forbidden EffectiveMaxParallelAgents() branch on a fresh install", cfg.Agents.Defaults.SubTurn.MaxConcurrent)
	}
}

// TestSubTurnMaxConcurrent_UnsetCase_M2_1Gap reproduces grill #2 finding M2-1
// (BDD-108, #112, SC-050) concretely: BEFORE this unit's seed existed, a
// fresh install's config.Agents.Defaults.SubTurn (unset, Go zero value —
// verified historically by `rg SubTurn pkg/config/defaults.go` returning zero
// matches) left MaxConcurrent <= 0. Every external caller applying
// getSubTurnConfig's documented guard (pkg/agent/subturn.go:64-69,
// `if maxConcurrent <= 0 { maxConcurrent =
// al.cfg.Performance.EffectiveMaxParallelAgents() }`, owned by U7 — read-only
// here, not modified by this unit) would therefore fall through to
// PerformanceConfig.EffectiveMaxParallelAgents(): a value that is
// hardware-autodetected (autoDetectMaxParallel, config.go) and NOT
// deterministically 16 — every v2 test papered over this by setting the key
// explicitly to 24, so the shipped default configuration itself had zero
// coverage.
//
// This test is the negative control that proves the gap was real and proves
// this unit's fix (TestDefaultConfig_SeedsSubTurnMaxConcurrentAt16) actually
// closes it, rather than both tests coincidentally agreeing on a hardcoded
// number.
func TestSubTurnMaxConcurrent_UnsetCase_M2_1Gap(t *testing.T) {
	// The pre-fix shape: SubTurnConfig as it existed with no seed in
	// defaults.go, i.e. an untouched zero value.
	var unseeded SubTurnConfig
	if unseeded.MaxConcurrent > 0 {
		t.Fatalf("sanity: zero-value SubTurnConfig.MaxConcurrent = %d, want <= 0 for this scenario to apply", unseeded.MaxConcurrent)
	}

	// Reproduce getSubTurnConfig's exact, documented guard condition
	// (pkg/agent/subturn.go:64-69) using only exported pkg/config surface —
	// this unit does not import or modify pkg/agent.
	forbiddenBranchTaken := unseeded.MaxConcurrent <= 0
	if !forbiddenBranchTaken {
		t.Fatalf("expected the unseeded config to take the forbidden EffectiveMaxParallelAgents() branch")
	}
	forbiddenValue := PerformanceConfig{}.EffectiveMaxParallelAgents()
	t.Logf("M2-1 gap (BEFORE this unit's fix): unseeded MaxConcurrent=%d <= 0, so the resolver would have fallen through to PerformanceConfig.EffectiveMaxParallelAgents() = %d (hardware-autodetected on this machine via autoDetectMaxParallel; NOT guaranteed to be 16, and empirically was NOT 16 in the environment this fix was written on)", unseeded.MaxConcurrent, forbiddenValue)

	// The fix: DefaultConfig()'s seed makes the guard's condition false on
	// every fresh install, regardless of hardware.
	seeded := DefaultConfig().Agents.Defaults.SubTurn
	seededTakesForbiddenBranch := seeded.MaxConcurrent <= 0
	if seededTakesForbiddenBranch {
		t.Fatalf("FR-095 regression: DefaultConfig() seeded SubTurn.MaxConcurrent=%d, which still is <= 0 — a fresh install would take the forbidden EffectiveMaxParallelAgents() branch again", seeded.MaxConcurrent)
	}
	t.Logf("M2-1 gap (AFTER this unit's fix): seeded MaxConcurrent=%d > 0, deterministic across all hardware — the forbidden branch is never entered", seeded.MaxConcurrent)

	if seeded.MaxConcurrent != 16 {
		t.Fatalf("seeded SubTurn.MaxConcurrent = %d, want the FR-095/operator-decision-4 value of 16", seeded.MaxConcurrent)
	}
}
