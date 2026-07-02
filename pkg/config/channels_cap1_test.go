package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
		t.Fatalf("ValidateChannels rejected two telegram instances with valid namespaced keys (cap-1 was lifted): %v", err)
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

// TestIsWorkspaceBound_FullyBound confirms the predicate returns true when all
// three workspace-binding conditions are met.
func TestIsWorkspaceBound_FullyBound(t *testing.T) {
	inst := ChannelInstanceConfig{
		WorkspaceID: "ws-1",
		Identity:    &ChannelIdentity{Kind: "agent", ID: "concierge"},
	}
	if !inst.IsWorkspaceBound() {
		t.Fatal("IsWorkspaceBound returned false for a fully-bound instance")
	}
}

// TestIsWorkspaceBound_NoWorkspaceID returns false when WorkspaceID is empty.
func TestIsWorkspaceBound_NoWorkspaceID(t *testing.T) {
	inst := ChannelInstanceConfig{
		Identity: &ChannelIdentity{Kind: "agent", ID: "concierge"},
	}
	if inst.IsWorkspaceBound() {
		t.Fatal("IsWorkspaceBound returned true when WorkspaceID is empty")
	}
}

// TestIsWorkspaceBound_NilIdentity returns false when Identity is nil.
func TestIsWorkspaceBound_NilIdentity(t *testing.T) {
	inst := ChannelInstanceConfig{
		WorkspaceID: "ws-1",
		Identity:    nil,
	}
	if inst.IsWorkspaceBound() {
		t.Fatal("IsWorkspaceBound returned true when Identity is nil")
	}
}

// TestIsWorkspaceBound_WrongKind returns false when Identity.Kind is not "agent".
func TestIsWorkspaceBound_WrongKind(t *testing.T) {
	inst := ChannelInstanceConfig{
		WorkspaceID: "ws-1",
		Identity:    &ChannelIdentity{Kind: "user", ID: "alice"},
	}
	if inst.IsWorkspaceBound() {
		t.Fatal("IsWorkspaceBound returned true when Identity.Kind is 'user'")
	}
}

// TestIsWorkspaceBound_EmptyAgentID returns false when Identity.ID is empty.
func TestIsWorkspaceBound_EmptyAgentID(t *testing.T) {
	inst := ChannelInstanceConfig{
		WorkspaceID: "ws-1",
		Identity:    &ChannelIdentity{Kind: "agent", ID: ""},
	}
	if inst.IsWorkspaceBound() {
		t.Fatal("IsWorkspaceBound returned true when Identity.ID is empty")
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
		t.Fatal("ValidateChannels accepted a half-bound instance (WorkspaceID set, Identity nil); want ErrHalfBoundChannelInstance")
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
		t.Fatal("ValidateChannels accepted a half-bound instance (Identity.Kind=user); want ErrHalfBoundChannelInstance")
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
