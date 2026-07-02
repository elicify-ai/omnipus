package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestValidateChannelsCap1_SingleInstanceAccepted is the FR-2.3 happy path: a map
// with one instance per type passes (no error). A valid single-instance config
// must NOT be rejected.
func TestValidateChannelsCap1_SingleInstanceAccepted(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true},
		"discord":  {Type: "discord", Enabled: false},
	}
	if err := ValidateChannelsCap1(channels); err != nil {
		t.Fatalf("ValidateChannelsCap1 rejected a valid one-per-type map: %v", err)
	}
}

// TestValidateChannelsCap1_EmptyAccepted confirms an empty/nil map is valid.
func TestValidateChannelsCap1_EmptyAccepted(t *testing.T) {
	if err := ValidateChannelsCap1(nil); err != nil {
		t.Fatalf("nil channels map should be valid, got: %v", err)
	}
	if err := ValidateChannelsCap1(map[string]ChannelInstanceConfig{}); err != nil {
		t.Fatalf("empty channels map should be valid, got: %v", err)
	}
}

// TestValidateChannelsCap1_DuplicateTypeRejected covers FR-2.3 / US-2: two
// instances of the same type violate the cap and the error wraps the sentinel
// (so the gateway can map it to a 422).
func TestValidateChannelsCap1_DuplicateTypeRejected(t *testing.T) {
	channels := map[string]ChannelInstanceConfig{
		"telegram":   {Type: "telegram", Enabled: true},
		"telegram-2": {Type: "telegram", Enabled: true},
	}
	err := ValidateChannelsCap1(channels)
	if err == nil {
		t.Fatal("ValidateChannelsCap1 accepted two telegram instances; want cap-1 violation")
	}
	if !errors.Is(err, ErrChannelsCap1Violated) {
		t.Fatalf("error does not wrap ErrChannelsCap1Violated: %v", err)
	}
}

// TestNormalizeChannelMap_PopulatesTypeFromKey confirms an instance written
// without an explicit type (e.g. by an early REST write) is normalized so its
// Type equals the map key — which keeps cap-1 counting correct.
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
	// And the normalized map passes cap-1 with one instance.
	if err := ValidateChannelsCap1(out); err != nil {
		t.Errorf("normalized single instance failed cap-1: %v", err)
	}
}

// TestLoadConfig_RejectsInvalidInstanceKey is the ADR-029 Gate 0 load-path
// guard: a config.json carrying a channel entry with an invalid instance key
// (here "telegram-2" is not a known bare type and does not follow the
// <type>.<slug> format) MUST be rejected by LoadConfig. The error wraps
// ErrInvalidInstanceKey. (Prior to ADR-029 this tested cap-1 via
// ErrChannelsCap1Violated; the cap is now lifted and key-grammar is enforced
// instead.)
func TestLoadConfig_RejectsInvalidInstanceKey(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(`{
  "version": 1,
  "agents": {"defaults": {"workspace": "./workspace"}},
  "channels": {
    "telegram":   {"type": "telegram", "enabled": true},
    "telegram-2": {"type": "telegram", "enabled": true}
  }
}`),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("LoadConfig accepted a config with an invalid instance key; want ErrInvalidInstanceKey rejection")
	}
	if !errors.Is(err, ErrInvalidInstanceKey) {
		t.Fatalf("LoadConfig error does not wrap ErrInvalidInstanceKey: %v", err)
	}
}

// TestLoadConfig_AcceptsSingleInstancePerType is the FR-2.3 load-path happy
// path: a config with one instance per type loads cleanly (guards against the
// validator over-rejecting valid configs).
func TestLoadConfig_AcceptsSingleInstancePerType(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(
		configPath,
		[]byte(`{
  "version": 1,
  "agents": {"defaults": {"workspace": "./workspace"}},
  "channels": {
    "telegram": {"type": "telegram", "enabled": true},
    "discord":  {"type": "discord",  "enabled": false}
  }
}`),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig rejected a valid one-per-type config: %v", err)
	}
	if len(cfg.Channels) != 2 {
		t.Errorf("expected 2 channel instances, got %d", len(cfg.Channels))
	}
}

// TestChannelInstanceConfig_IdentityRoundTrips confirms the identity field
// (FR-2.5 / US-5) survives a JSON round-trip through the instance config.
func TestChannelInstanceConfig_IdentityRoundTrips(t *testing.T) {
	inst := ChannelInstanceConfig{
		Identity: &ChannelIdentity{Kind: "agent", ID: "concierge"},
	}
	if inst.Identity == nil || inst.Identity.Kind != "agent" || inst.Identity.ID != "concierge" {
		t.Fatalf("identity not retained on the instance: %+v", inst.Identity)
	}
}
