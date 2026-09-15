// config_channels_instance_test.go: tests for the channels instance model - instance-key grammar, identity and kind validation, and extraction of the typed legacy per-channel sub-configs (InstanceTo*)

package config

import (
	"errors"
	"strings"
	"testing"
)

// --- moved from config.go tests 2026-09-15 ---

// TestValidateChannels_SingleInstanceAccepted is the ADR-029 happy path: a map
// with one instance per type passes (no error). A valid single-instance config
// must NOT be rejected.
func TestValidateChannels_SingleInstanceAccepted(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true},
		"discord":  {Type: "discord", Enabled: false},
	}
	if err := ValidateChannels(channels); err != nil {
		t.Fatalf("ValidateChannels rejected a valid one-per-type map: %v", err)
	}
}

// TestValidateChannels_EmptyAccepted confirms an empty/nil map is valid.
func TestValidateChannels_EmptyAccepted(t *testing.T) {
	if err := ValidateChannels(nil); err != nil {
		t.Fatalf("nil channels map should be valid, got: %v", err)
	}
	if err := ValidateChannels(map[string]ChannelInstanceConfig{}); err != nil {
		t.Fatalf("empty channels map should be valid, got: %v", err)
	}
}

// TestValidateChannels_MultipleInstancesPerTypeAllowed verifies that ADR-029
// lifts the old cap-1 restriction: two instances of the same type with valid
// namespaced keys are now permitted.
func TestValidateChannels_MultipleInstancesPerTypeAllowed(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram":    {Type: "telegram", Enabled: true},
		"telegram.eu": {Type: "telegram", Enabled: true},
	}
	if err := ValidateChannels(channels); err != nil {
		t.Fatalf(
			"ValidateChannels rejected two telegram instances with valid namespaced keys (cap-1 was lifted): %v",
			err,
		)
	}
}

// TestNormalizeChannelMap_PopulatesTypeFromKey confirms an instance written
// without an explicit type (e.g. by an early REST write) is normalized so its
// Type equals the map key — which keeps counting correct.
func TestNormalizeChannelMap_PopulatesTypeFromKey(t *testing.T) {
	in := map[string]ChannelInstanceConfig{
		"telegram": {Enabled: true}, // no Type set
	}
	out := normalizeChannelMap(in)
	inst, ok := out["telegram"]
	if !ok {
		t.Fatal("telegram instance dropped by normalizeChannelMap")
	}
	if inst.Type != "telegram" {
		t.Errorf("Type = %q, want 'telegram' (inferred from map key)", inst.Type)
	}
}

// TestValidateChannels_HalfBound_RejectsWorkspaceIDWithNilIdentity verifies
// that ValidateChannels rejects a half-bound instance where WorkspaceID is set
// but Identity is nil (ADR-029 FR-029 half-bound guard).
func TestValidateChannels_HalfBound_RejectsWorkspaceIDWithNilIdentity(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram": {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: "ws-abc",
			Identity:    nil, // half-bound: workspace set, no identity
		},
	}
	err := ValidateChannels(channels)
	if err == nil {
		t.Fatal(
			"ValidateChannels accepted a half-bound instance (WorkspaceID set, Identity nil); want ErrHalfBoundChannelInstance",
		)
	}
	if !errors.Is(err, ErrHalfBoundChannelInstance) {
		t.Fatalf("error does not wrap ErrHalfBoundChannelInstance: %v", err)
	}
}

// TestValidateChannels_HalfBound_RejectsWorkspaceIDWithUserKind verifies
// that ValidateChannels rejects an instance with WorkspaceID set but
// Identity.Kind != "agent" (half-bound: binding requires an agent identity).
func TestValidateChannels_HalfBound_RejectsWorkspaceIDWithUserKind(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram": {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: "ws-abc",
			Identity:    &ChannelIdentity{Kind: "user", ID: "alice"},
		},
	}
	err := ValidateChannels(channels)
	if err == nil {
		t.Fatal(
			"ValidateChannels accepted a half-bound instance (Identity.Kind=user); want ErrHalfBoundChannelInstance",
		)
	}
	if !errors.Is(err, ErrHalfBoundChannelInstance) {
		t.Fatalf("error does not wrap ErrHalfBoundChannelInstance: %v", err)
	}
}

// TestValidateChannels_HalfBound_RejectsWorkspaceIDWithEmptyAgentID verifies
// that ValidateChannels rejects an instance with WorkspaceID set and
// Identity.Kind=="agent" but empty Identity.ID (can't bind without an agent ID).
func TestValidateChannels_HalfBound_RejectsWorkspaceIDWithEmptyAgentID(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram": {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: "ws-abc",
			Identity:    &ChannelIdentity{Kind: "agent", ID: ""},
		},
	}
	err := ValidateChannels(channels)
	if err == nil {
		t.Fatal("ValidateChannels accepted a half-bound instance (Identity.ID empty); want ErrHalfBoundChannelInstance")
	}
	if !errors.Is(err, ErrHalfBoundChannelInstance) {
		t.Fatalf("error does not wrap ErrHalfBoundChannelInstance: %v", err)
	}
}

// TestValidateChannels_FullyBound_Accepted verifies that a fully-bound instance
// (WorkspaceID + Identity.Kind=="agent" + non-empty Identity.ID) is accepted.
func TestValidateChannels_FullyBound_Accepted(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram": {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: "ws-abc",
			Identity:    &ChannelIdentity{Kind: "agent", ID: "concierge"},
		},
	}
	if err := ValidateChannels(channels); err != nil {
		t.Fatalf("ValidateChannels rejected a fully-bound instance: %v", err)
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
		t.Fatal(
			"normalizeChannelMap dropped 'whatsapp.eu' — this is the MAJ-002 regression; namespaced keys must be kept (FR-016)",
		)
	}
	if inst.Type != "whatsapp" {
		t.Errorf("Type = %q, want 'whatsapp' (effective type from pre-dot segment)", inst.Type)
	}

	// The key must NOT have been remapped to the bare type.
	if _, bare := out["whatsapp"]; bare {
		t.Error(
			"normalizeChannelMap added a bare 'whatsapp' key — the namespaced key must be stored under its original key",
		)
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

// TestValidateIdentityKinds_RejectsTypos proves the load-time wiring: a typo'd
// channel identity kind fails loudly rather than silently downgrading routing.
//
// ADR-037 renamed validateIdentityAndAgentRefKinds to validateIdentityKinds and
// dropped its agent-delegation-ref-kind validation entirely — that ran against
// AgentConfig.DelegationPolicy / AgentDefaults.DelegationPolicy, both of which
// no longer exist (the per-workspace delegation graph is the sole delegation
// mechanism now). The "typo'd agent delegation ref kind" and delegation-policy
// assertions this test used to carry are removed with it; AgentRef.Validate
// itself is still covered directly by TestAgentRef_Validate above.
func TestValidateIdentityKinds_RejectsTypos(t *testing.T) {
	t.Run("typo'd channel identity kind", func(t *testing.T) {
		cfg := &Config{
			Channels: map[string]ChannelInstanceConfig{
				"telegram": {
					Type:     "telegram",
					Identity: &ChannelIdentity{Kind: "agnet", ID: "mia"},
				},
			},
		}
		err := validateIdentityKinds(cfg)
		if err == nil || !strings.Contains(err.Error(), "telegram") {
			t.Fatalf("expected typo'd identity kind to be rejected with channel name, got %v", err)
		}
	})

	t.Run("valid kinds pass", func(t *testing.T) {
		cfg := &Config{
			Channels: map[string]ChannelInstanceConfig{
				"telegram": {Type: "telegram", Identity: &ChannelIdentity{Kind: "agent", ID: "mia"}},
				"discord":  {Type: "discord", Identity: &ChannelIdentity{Kind: "user"}},
			},
		}
		if err := validateIdentityKinds(cfg); err != nil {
			t.Fatalf("expected valid kinds to pass, got %v", err)
		}
	})
}

// TestValidateIdentityKinds_NormalizesKinds proves the CRITICAL-1 fix
// end-to-end: a mixed-case/whitespace kind the API write path accepts (and that
// route.go routes) passes load-time validation AND is rewritten in place to the
// canonical lowercase+trimmed form, so persisted configs never drift from the
// case-tolerant accept paths.
func TestValidateIdentityKinds_NormalizesKinds(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelInstanceConfig{
			"telegram": {Type: "telegram", Identity: &ChannelIdentity{Kind: "Agent", ID: "mia"}},
			"discord":  {Type: "discord", Identity: &ChannelIdentity{Kind: " User "}},
		},
	}

	if err := validateIdentityKinds(cfg); err != nil {
		t.Fatalf("expected mixed-case kinds to pass load-time validation, got %v", err)
	}

	if got := cfg.Channels["telegram"].Identity.Kind; got != "agent" {
		t.Errorf("channel telegram identity kind = %q, want canonical %q", got, "agent")
	}
	if got := cfg.Channels["discord"].Identity.Kind; got != "user" {
		t.Errorf("channel discord identity kind = %q, want canonical %q", got, "user")
	}
}

// TestValidateIdentityKinds_RejectsUnknownEvenMixedCase proves normalization
// does NOT widen the accepted set: a genuinely-unknown kind ("robot") is still
// rejected regardless of casing.
func TestValidateIdentityKinds_RejectsUnknownEvenMixedCase(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelInstanceConfig{
			"telegram": {Type: "telegram", Identity: &ChannelIdentity{Kind: "Robot", ID: "mia"}},
		},
	}
	if err := validateIdentityKinds(cfg); err == nil {
		t.Fatal("expected mixed-case unknown identity kind \"Robot\" to be rejected, got nil")
	}
}
