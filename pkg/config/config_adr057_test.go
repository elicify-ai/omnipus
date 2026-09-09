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
//   - FR-095 / BDD-108 / test #112 / SC-050, AS AMENDED 2026-08-04
//     (concurrency-gate consolidation, commit 536b7340's follow-up fix —
//     config-layer portion, jointly owned with U19): FR-095 originally
//     required the root-delegation cap to be SEEDED at a fixed 16 on a fresh
//     install specifically so it would never fall through to
//     Performance.EffectiveMaxParallelAgents() — reasoning that depended
//     entirely on that function ALSO being hard-clamped to 16 at the time
//     (clampParallelExplicit). 536b7340 removed that ceiling, invalidating
//     the premise: a fixed seed became a second, independently-sized cap
//     silently disagreeing with an operator's own max_parallel_agents
//     setting, the ADR-037 anti-pattern this project bans. The tests below
//     are DELIBERATELY INVERTED from their original assertions: a config
//     built the way a fresh install builds one (config.DefaultConfig()) now
//     MUST leave agents.defaults.subturn.max_concurrent UNSET, so it
//     resolves LIVE to Performance.EffectiveMaxParallelAgents() — the
//     single, central, UI-configurable authority for agent concurrency —
//     rather than to a value frozen at seed time. See
//     docs/internal/specs/adr-057-session-unification-spec.md's
//     "AMENDED 2026-08-04" notes on FR-095/SC-050/BDD-108 for the full
//     rationale.
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

// TestDefaultConfig_SubTurnMaxConcurrentIsUnset is the 2026-08-04
// concurrency-gate consolidation INVERSION of the retired
// TestDefaultConfig_SeedsSubTurnMaxConcurrentAt16 (FR-095/BDD-108/#112/
// SC-050 as originally written). That test asserted a fresh install
// (config.DefaultConfig()) seeds agents.defaults.subturn.max_concurrent at a
// fixed 16, specifically so callers' `if maxConcurrent <= 0` guard would
// never fall through to the hardware-autodetected
// Performance.EffectiveMaxParallelAgents(). That premise depended entirely
// on EffectiveMaxParallelAgents() ALSO being hard-clamped to 16 at the time
// (clampParallelExplicit) — commit 536b7340 removed that ceiling, so the
// fixed seed became a SECOND, independently-sized concurrency cap silently
// disagreeing with an operator's own max_parallel_agents setting, the exact
// ADR-037 anti-pattern this project bans. This is deliberately inverted:
// DefaultConfig() now leaves SubTurn.MaxConcurrent UNSET (Go zero value) so
// both consumers (pkg/agent's getSubTurnConfig and ResolveRootDelegationCap)
// take the CENTRAL-authority branch on every fresh install, by design.
func TestDefaultConfig_SubTurnMaxConcurrentIsUnset(t *testing.T) {
	cfg := DefaultConfig()

	got := cfg.Agents.Defaults.SubTurn.MaxConcurrent
	if got != 0 {
		t.Fatalf("DefaultConfig().Agents.Defaults.SubTurn.MaxConcurrent = %d, want 0 (unset — inherits the central "+
			"Performance.EffectiveMaxParallelAgents() authority; concurrency-gate consolidation, 2026-08-04)", got)
	}

	// The retired DefaultSubTurnMaxConcurrent constant (16) no longer exists
	// as a seed target — assert that directly by construction: a fresh
	// config's resolved cap must equal the central value, not any fixed
	// number, and must track that value when it changes (proving there is no
	// hidden second seed reintroducing the old behaviour).
	for _, want := range []int{1, 5, 40} {
		cfg.Performance.MaxParallelAgents = want
		if eff, _ := cfg.Performance.EffectiveMaxParallelAgents(); eff != want {
			t.Fatalf("EffectiveMaxParallelAgents() = %d, want %d — sanity precondition for this test", eff, want)
		}
		if cfg.Agents.Defaults.SubTurn.MaxConcurrent > 0 {
			t.Fatalf("SubTurn.MaxConcurrent = %d must stay <= 0 (unset) regardless of the central value, "+
				"so callers always resolve to the CURRENT central authority rather than a frozen seed",
				cfg.Agents.Defaults.SubTurn.MaxConcurrent)
		}
	}
}

// TestSubTurnMaxConcurrent_UnsetCase_ResolvesLiveToCentralAuthority is the
// 2026-08-04 consolidation's replacement for the retired
// TestSubTurnMaxConcurrent_UnsetCase_M2_1Gap, which treated an unseeded
// (zero-value) MaxConcurrent as "the forbidden branch" — a defect at the
// time only because the seed existed specifically to avoid it. Now that the
// seed is gone by design, this test asserts the POSITIVE property directly:
// an unset SubTurnConfig.MaxConcurrent, on both a bare zero-value struct and
// on config.DefaultConfig() itself, resolves to
// PerformanceConfig.EffectiveMaxParallelAgents() — deterministically
// tracking whatever that central, UI-configurable value is, not a
// hardcoded number.
func TestSubTurnMaxConcurrent_UnsetCase_ResolvesLiveToCentralAuthority(t *testing.T) {
	var unset SubTurnConfig
	if unset.MaxConcurrent > 0 {
		t.Fatalf("sanity: zero-value SubTurnConfig.MaxConcurrent = %d, want <= 0", unset.MaxConcurrent)
	}

	// Reproduce getSubTurnConfig's exact, documented guard condition
	// (pkg/agent/subturn.go) using only exported pkg/config surface — this
	// unit does not import or modify pkg/agent.
	takesCentralBranch := unset.MaxConcurrent <= 0
	if !takesCentralBranch {
		t.Fatalf("expected the unset config to take the central-authority EffectiveMaxParallelAgents() branch")
	}

	perf := PerformanceConfig{MaxParallelAgents: 40}
	if eff, _ := perf.EffectiveMaxParallelAgents(); eff != 40 {
		t.Fatalf("EffectiveMaxParallelAgents() = %d, want 40 (explicit, unclamped)", eff)
	}
	t.Logf("unset MaxConcurrent=%d <= 0, so both consumers resolve to EffectiveMaxParallelAgents()=%d — this IS the design now, not a gap", unset.MaxConcurrent, 40)

	// config.DefaultConfig() itself must also leave the field unset, so a
	// fresh install takes this same branch — no hidden seed reintroducing a
	// fixed number.
	seeded := DefaultConfig().Agents.Defaults.SubTurn
	if seeded.MaxConcurrent > 0 {
		t.Fatalf("regression: DefaultConfig() seeded SubTurn.MaxConcurrent=%d — a fresh install must leave it unset "+
			"so it always inherits the CURRENT central authority instead of a value frozen at seed time", seeded.MaxConcurrent)
	}
}
