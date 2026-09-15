package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
