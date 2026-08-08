// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_messaging_kill_switch_test.go is the regression suite for fix-wave
// finding #4: the ADR-053 §8 session-messaging kill-switch trio
// (enabled/wake_enabled/adjudication_enabled) used to be plain bools with
// `omitempty` JSON tags, validated by a validateBootConfig heuristic that
// treated "all three false" as "the section was never set" and forced all
// three back to true. That heuristic could never actually distinguish an
// operator's genuine full-off config from an absent/legacy section, because
// a plain bool has no way to represent "unset" separately from "explicitly
// false" — both are the same Go zero value. The fields are now *bool (nil =
// unset, PlanBounds *int convention); this file proves the fix end to end.
package config

import (
	"encoding/json"
	"testing"
)

// TestSessionMessaging_AllThreeExplicitFalse_StaysOffAcrossLoadValidateRemarshal
// is the exact scenario the fix-wave finding named: an operator's config.json
// explicitly turns off all three kill switches. That state must survive
// unmarshal, validateBootConfig, a re-marshal back to JSON, and a second
// unmarshal of that re-marshaled JSON — at every step, EffectiveEnabled/
// EffectiveWakeEnabled/EffectiveAdjudicationEnabled must all report false,
// and the JSON must retain the explicit `false` values rather than omitting
// them (omitempty on a *bool omits only a nil pointer, never a pointer to
// false).
func TestSessionMessaging_AllThreeExplicitFalse_StaysOffAcrossLoadValidateRemarshal(t *testing.T) {
	raw := []byte(`{"enabled": false, "wake_enabled": false, "adjudication_enabled": false}`)

	var sm SessionMessagingConfig
	if err := json.Unmarshal(raw, &sm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sm.Enabled == nil || *sm.Enabled {
		t.Fatalf("Enabled must unmarshal to a non-nil pointer to false, got %v", sm.Enabled)
	}
	if sm.EffectiveEnabled() {
		t.Fatal("EffectiveEnabled() must be false right after unmarshaling an explicit false")
	}
	if sm.EffectiveWakeEnabled() {
		t.Fatal("EffectiveWakeEnabled() must be false right after unmarshaling an explicit false")
	}
	if sm.EffectiveAdjudicationEnabled() {
		t.Fatal("EffectiveAdjudicationEnabled() must be false right after unmarshaling an explicit false")
	}

	// Boot-time validation must not overrule the operator's explicit choice.
	full := DefaultConfig()
	full.SessionMessaging = sm
	if err := validateBootConfig(full); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}
	if full.SessionMessaging.EffectiveEnabled() {
		t.Fatal("validateBootConfig must NOT re-enable an explicit all-three-false session_messaging section " +
			"(this is the exact bug the old 'all three false means unset' heuristic caused)")
	}
	if full.SessionMessaging.EffectiveWakeEnabled() {
		t.Fatal("validateBootConfig must not re-enable wake_enabled either")
	}
	if full.SessionMessaging.EffectiveAdjudicationEnabled() {
		t.Fatal("validateBootConfig must not re-enable adjudication_enabled either")
	}

	// Re-marshal: the explicit false values must survive (not be dropped by
	// omitempty, which only ever applies to a NIL *bool, never a non-nil
	// pointer to false).
	remarshaled, err := json.Marshal(full.SessionMessaging)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(remarshaled, &asMap); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, key := range []string{"enabled", "wake_enabled", "adjudication_enabled"} {
		v, present := asMap[key]
		if !present {
			t.Errorf("re-marshaled JSON dropped %q entirely — an explicit false must round-trip, not disappear", key)
			continue
		}
		if v != false {
			t.Errorf("re-marshaled JSON has %q = %v, want false", key, v)
		}
	}

	// A second full unmarshal cycle of the re-marshaled bytes must still be
	// OFF — the fix must be stable under repeated load/save cycles, not just
	// the first one.
	var sm2 SessionMessagingConfig
	if err := json.Unmarshal(remarshaled, &sm2); err != nil {
		t.Fatalf("second unmarshal: %v", err)
	}
	if sm2.EffectiveEnabled() || sm2.EffectiveWakeEnabled() || sm2.EffectiveAdjudicationEnabled() {
		t.Fatal("second load/validate cycle of the re-marshaled config must still report all three OFF")
	}
	full2 := DefaultConfig()
	full2.SessionMessaging = sm2
	if err := validateBootConfig(full2); err != nil {
		t.Fatalf("validateBootConfig (second cycle): %v", err)
	}
	if full2.SessionMessaging.EffectiveEnabled() || full2.SessionMessaging.EffectiveWakeEnabled() ||
		full2.SessionMessaging.EffectiveAdjudicationEnabled() {
		t.Fatal("validateBootConfig must still leave all three OFF on the second cycle")
	}
}

// TestSessionMessaging_AbsentSection_DefaultsAllThreeToTrue covers the OTHER
// half of the *bool contract: a config that never mentions session_messaging
// at all (e.g. a config.json predating ADR-053, or a bare struct literal) has
// Enabled/WakeEnabled/AdjudicationEnabled at their Go zero value — nil, not
// false — and validateBootConfig must seed all three to their documented
// defaults (true), exactly matching a fresh install's DefaultConfig seeding.
func TestSessionMessaging_AbsentSection_DefaultsAllThreeToTrue(t *testing.T) {
	raw := []byte(`{}`)
	var sm SessionMessagingConfig
	if err := json.Unmarshal(raw, &sm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sm.Enabled != nil || sm.WakeEnabled != nil || sm.AdjudicationEnabled != nil {
		t.Fatalf("an absent section must unmarshal to nil pointers, got Enabled=%v WakeEnabled=%v AdjudicationEnabled=%v",
			sm.Enabled, sm.WakeEnabled, sm.AdjudicationEnabled)
	}
	if !sm.EffectiveEnabled() || !sm.EffectiveWakeEnabled() || !sm.EffectiveAdjudicationEnabled() {
		t.Fatal("EffectiveEnabled/EffectiveWakeEnabled/EffectiveAdjudicationEnabled must default to true " +
			"(fail-open) when the operator never set the key at all")
	}

	full := DefaultConfig()
	full.SessionMessaging = sm // overwrite DefaultConfig's own seeding with the "absent section" zero value
	if err := validateBootConfig(full); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}
	if full.SessionMessaging.Enabled == nil || !*full.SessionMessaging.Enabled {
		t.Error("validateBootConfig must seed a nil Enabled to a non-nil pointer to true")
	}
	if full.SessionMessaging.WakeEnabled == nil || !*full.SessionMessaging.WakeEnabled {
		t.Error("validateBootConfig must seed a nil WakeEnabled to a non-nil pointer to true")
	}
	if full.SessionMessaging.AdjudicationEnabled == nil || !*full.SessionMessaging.AdjudicationEnabled {
		t.Error("validateBootConfig must seed a nil AdjudicationEnabled to a non-nil pointer to true")
	}
}

// TestSessionMessaging_MixedExplicitValues_EachFieldIndependent proves the
// fields are defaulted INDEPENDENTLY (no cross-field heuristic survives):
// one explicit false alongside two unset fields must leave the false field
// false and default only the unset ones — the old heuristic's cross-field
// "all three false means unset" logic must be gone entirely.
func TestSessionMessaging_MixedExplicitValues_EachFieldIndependent(t *testing.T) {
	raw := []byte(`{"enabled": false}`)
	var sm SessionMessagingConfig
	if err := json.Unmarshal(raw, &sm); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	full := DefaultConfig()
	full.SessionMessaging = sm
	if err := validateBootConfig(full); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}
	if full.SessionMessaging.EffectiveEnabled() {
		t.Error("the explicitly-false enabled field must stay false")
	}
	if !full.SessionMessaging.EffectiveWakeEnabled() {
		t.Error("wake_enabled was never set — it must default to true independently of enabled's explicit false")
	}
	if !full.SessionMessaging.EffectiveAdjudicationEnabled() {
		t.Error("adjudication_enabled was never set — it must default to true independently of enabled's explicit false")
	}
}
