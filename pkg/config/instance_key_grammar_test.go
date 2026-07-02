package config

// instance_key_grammar_test.go — ADR-029 TDD #3 and TDD #1.
//
// TDD #3 — F-3 / DS-2: ValidateInstanceKey covers all DS-2 rows from the spec.
// TDD #1 — F-4 / FR-016: normalizeChannelMap keeps namespaced keys (MAJ-002 regression).
//
// Traces to: channel-instance-workspace-binding-spec.md §7 TDD #3, TDD #1,
//            FR-017, FR-016, DS-2, BDD "Namespaced keys survive config load".

import (
	"errors"
	"strings"
	"testing"
)

// ── TDD #3 / F-3 / DS-2: ValidateInstanceKey table test ─────────────────────

// TestInstanceKeyGrammar_DS2_Table is the DS-2 key-grammar table test covering
// all 8 rows from the spec (FR-017, locked: delimiter='.', slug=[a-z0-9-]{1,32}).
//
// BDD: Given a channel instance key,
// When ValidateInstanceKey is called,
// Then it accepts well-formed keys and rejects malformed ones with ErrInvalidInstanceKey.
func TestInstanceKeyGrammar_DS2_Table(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
		// errContains is checked only when wantErr=true.
		errContains string
	}{
		// DS-2 row 1: "whatsapp.eu" — well-formed namespaced key.
		// Traces to: DS-2/1, US-6/AC-1, FR-017.
		{
			name:    "DS-2/1 whatsapp.eu: well-formed",
			key:     "whatsapp.eu",
			wantErr: false,
		},
		// DS-2 row 2: "whatsapp.US" — uppercase slug → must reject.
		// Traces to: DS-2/2, FR-017 (lowercase enforced, uppercase → 422).
		{
			name:        "DS-2/2 whatsapp.US: uppercase slug rejected",
			key:         "whatsapp.US",
			wantErr:     true,
			errContains: "invalid",
		},
		// DS-2 row 3: "whatsapp." — empty slug → must reject.
		// Traces to: DS-2/3, FR-017 boundary.
		//
		// BUG DETECTED (reported, not fixed — qa-lead policy): ValidateInstanceKey
		// treats "whatsapp." as a bare type key because ParseInstanceKey returns
		// slug="" when the trailing dot produces an empty suffix, and the
		// slug=="" branch returns nil ("Bare type key — valid").  The dot is
		// present in the key string, so "whatsapp." is NOT a bare type key —
		// it is a malformed namespaced key with an empty slug that must be
		// rejected per DS-2 row 3 and FR-017.
		// Fix required in ValidateInstanceKey: distinguish "bare type" (no dot
		// at all) from "namespaced with empty slug" (trailing dot).
		// Tracked as a production bug; this test is expected to FAIL until fixed.
		{
			name:        "DS-2/3 whatsapp.: empty slug rejected",
			key:         "whatsapp.",
			wantErr:     true,
			errContains: "invalid",
		},
		// DS-2 row 4: "whatsapp.eu.2" — second dot in slug → must reject.
		// The slug is everything after the FIRST dot; "eu.2" contains a dot,
		// which is not in [a-z0-9-].
		// Traces to: DS-2/4, FR-017 boundary.
		{
			name:        "DS-2/4 whatsapp.eu.2: second dot rejected",
			key:         "whatsapp.eu.2",
			wantErr:     true,
			errContains: "invalid",
		},
		// DS-2 row 5: "telegram.main-bot" — hyphen is legal in slug.
		// Traces to: DS-2/5, US-6.
		{
			name:    "DS-2/5 telegram.main-bot: hyphen in slug valid",
			key:     "telegram.main-bot",
			wantErr: false,
		},
		// DS-2 row 6: "google-chat.sales" — hyphenated channel type; split on first dot.
		// Traces to: DS-2/6, FR-017 (irregular type with hyphen).
		{
			name:    "DS-2/6 google-chat.sales: hyphenated type valid",
			key:     "google-chat.sales",
			wantErr: false,
		},
		// DS-2 row 7: "whatsapp" — bare type key (legacy single instance).
		// Traces to: DS-2/7, back-compat.
		{
			name:    "DS-2/7 whatsapp: bare type key valid",
			key:     "whatsapp",
			wantErr: false,
		},
		// DS-2 row 8: slug exceeding 32 chars → must reject.
		// "whatsapp.<33 chars>" — slug is 33 characters.
		// Traces to: DS-2/8, FR-017 boundary (max 32 chars).
		{
			name:        "DS-2/8 whatsapp.<33-char-slug>: slug exceeds 32-char max",
			key:         "whatsapp." + strings.Repeat("a", 33),
			wantErr:     true,
			errContains: "invalid",
		},
		// Bonus edge: slug exactly 32 chars → must accept.
		{
			name:    "slug exactly 32 chars: valid boundary",
			key:     "telegram." + strings.Repeat("a", 32),
			wantErr: false,
		},
		// Bonus: slug = "a" (1 char, minimal) → valid.
		{
			name:    "slug 1 char: valid minimum",
			key:     "discord.a",
			wantErr: false,
		},
		// Bonus: unknown base type → rejected (even if slug is fine).
		{
			name:        "unknown base type: rejected",
			key:         "maixcam.eu",
			wantErr:     true,
			errContains: "invalid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateInstanceKey(tc.key)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateInstanceKey(%q) = nil, want error wrapping ErrInvalidInstanceKey", tc.key)
				}
				if !errors.Is(err, ErrInvalidInstanceKey) {
					t.Errorf("ValidateInstanceKey(%q) error does not wrap ErrInvalidInstanceKey: %v", tc.key, err)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("ValidateInstanceKey(%q) error = %q; want it to contain %q", tc.key, err.Error(), tc.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateInstanceKey(%q) = %v, want nil", tc.key, err)
				}
			}
		})
	}
}

// ── TDD #1 / F-4 / FR-016: normalizeChannelMap keeps namespaced keys ─────────

// TestNormalizeChannelMap_KeepsNamespacedKeys verifies FR-016 / TDD #1:
// a namespaced key "whatsapp.eu" MUST survive normalizeChannelMap with its key
// preserved and its Type set to "whatsapp".
//
// This is the MAJ-002 regression guard: before the fix, normalizeChannelMap
// matched on the raw map key ("whatsapp.eu" is not in knownChannelTypes) and
// silently dropped the entry.
//
// BDD: Given config channels keyed "whatsapp.eu" with type "whatsapp",
// When normalizeChannelMap is called,
// Then "whatsapp.eu" key is present in the output,
// And the entry's Type == "whatsapp",
// And the key is NOT converted to "whatsapp".
func TestNormalizeChannelMap_KeepsNamespacedKeys(t *testing.T) {
	in := map[string]ChannelInstanceConfig{
		"whatsapp.eu": {Type: "whatsapp", Enabled: true},
	}
	out := normalizeChannelMap(in)

	// The key must be preserved under its original namespaced form.
	inst, ok := out["whatsapp.eu"]
	if !ok {
		t.Fatal("normalizeChannelMap dropped 'whatsapp.eu' — this is the MAJ-002 regression; namespaced keys must be kept (FR-016)")
	}
	if inst.Type != "whatsapp" {
		t.Errorf("Type = %q, want 'whatsapp' (effective type from pre-dot segment)", inst.Type)
	}

	// The key must NOT have been remapped to the bare type.
	if _, bare := out["whatsapp"]; bare {
		t.Error("normalizeChannelMap added a bare 'whatsapp' key — the namespaced key must be stored under its original key")
	}
}

// TestNormalizeChannelMap_KeepsNamespacedKey_TypeBackfilled verifies that when
// Type is absent, normalizeChannelMap backfills it from the pre-dot segment.
//
// BDD: Given "whatsapp.eu" with no explicit Type,
// When normalizeChannelMap is called,
// Then the output entry has Type=="whatsapp".
func TestNormalizeChannelMap_KeepsNamespacedKey_TypeBackfilled(t *testing.T) {
	in := map[string]ChannelInstanceConfig{
		"whatsapp.eu": {Enabled: true}, // no explicit Type
	}
	out := normalizeChannelMap(in)

	inst, ok := out["whatsapp.eu"]
	if !ok {
		t.Fatal("normalizeChannelMap dropped 'whatsapp.eu' when Type is absent — must backfill Type and keep entry")
	}
	if inst.Type != "whatsapp" {
		t.Errorf("Type = %q, want 'whatsapp' (backfilled from pre-dot segment)", inst.Type)
	}
}

// TestNormalizeChannelMap_DropsUnknownBareKey verifies that an unknown bare-type
// key (e.g. "maixcam") is dropped (warn-and-drop for legacy/unsupported sections).
//
// BDD: Given "maixcam" key with no explicit type,
// When normalizeChannelMap is called,
// Then "maixcam" is NOT in the output (dropped with a WARN).
func TestNormalizeChannelMap_DropsUnknownBareKey(t *testing.T) {
	in := map[string]ChannelInstanceConfig{
		"maixcam": {Enabled: true},
	}
	out := normalizeChannelMap(in)

	if _, ok := out["maixcam"]; ok {
		t.Error("normalizeChannelMap kept 'maixcam' — unknown channel types must be dropped (warn-and-drop)")
	}
}

// TestNormalizeChannelMap_DifferentiationTest_TwoTypes verifies both a namespaced
// key and a bare key survive or are dropped as expected when both are present.
// This is the differentiation test: different inputs → different treatment.
//
// Traces to: FR-016, DS-2, anti-hardcoding requirement.
func TestNormalizeChannelMap_DifferentiationTest_TwoTypes(t *testing.T) {
	in := map[string]ChannelInstanceConfig{
		"whatsapp.eu": {Type: "whatsapp", Enabled: true},
		"telegram":    {Type: "telegram", Enabled: true},
		"maixcam":     {Enabled: true}, // unknown type → should be dropped
	}
	out := normalizeChannelMap(in)

	// namespaced key must survive
	if _, ok := out["whatsapp.eu"]; !ok {
		t.Error("normalizeChannelMap dropped 'whatsapp.eu'; namespaced known-type keys must survive")
	}
	// bare known type must survive
	if _, ok := out["telegram"]; !ok {
		t.Error("normalizeChannelMap dropped 'telegram'; bare known-type keys must survive")
	}
	// unknown type must be dropped
	if _, ok := out["maixcam"]; ok {
		t.Error("normalizeChannelMap kept 'maixcam'; unknown-type keys must be dropped")
	}
	// result must have exactly 2 entries
	if len(out) != 2 {
		t.Errorf("normalizeChannelMap output has %d entries, want 2", len(out))
	}
}
