package config

// repair_stale_bindings_test.go — ADR-029 FR-029/OBS-001 two-representation
// rule: RepairStaleChannelWildcardBindings must normalize configs where an
// instance carries BOTH a bound Identity AND a stale wildcard AgentBinding.
// TDD plan item #23c.

import (
	"testing"
)

// TestRepairStaleChannelWildcardBindings_DropsStaleWildcard verifies that an
// instance with BOTH WorkspaceID+Identity (bound) AND a wildcard binding in
// cfg.Bindings has the wildcard binding dropped (Identity wins, OBS-001).
func TestRepairStaleChannelWildcardBindings_DropsStaleWildcard(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelInstanceConfig{
			"whatsapp.eu": {
				Type:        "whatsapp",
				Enabled:     true,
				WorkspaceID: "sales",
				Identity:    &ChannelIdentity{Kind: "agent", ID: "ray"},
			},
		},
		Bindings: []AgentBinding{
			// Stale wildcard binding for the same instance.
			{
				AgentID: "ray",
				Match: BindingMatch{
					Channel:   "whatsapp.eu",
					AccountID: "*",
				},
			},
			// A peer binding for a different channel — must NOT be removed.
			{
				AgentID: "mia",
				Match: BindingMatch{
					Channel:   "telegram",
					AccountID: "*",
					Peer:      &PeerMatch{Kind: "direct", ID: "user123"},
				},
			},
		},
	}

	RepairStaleChannelWildcardBindings(cfg)

	// The stale wildcard for "whatsapp.eu" must be gone.
	for _, b := range cfg.Bindings {
		if b.Match.Channel == "whatsapp.eu" && b.Match.AccountID == "*" &&
			b.Match.Peer == nil && b.Match.GuildID == "" && b.Match.TeamID == "" {
			t.Errorf("stale wildcard binding for 'whatsapp.eu' was not removed: %+v", b)
		}
	}
	// The telegram peer binding must survive.
	found := false
	for _, b := range cfg.Bindings {
		if b.Match.Channel == "telegram" && b.Match.Peer != nil {
			found = true
		}
	}
	if !found {
		t.Error("non-stale telegram peer binding was incorrectly removed")
	}
}

// TestRepairStaleChannelWildcardBindings_KeepsUnboundWildcard verifies that
// a wildcard binding for an UNBOUND instance (no WorkspaceID) is preserved.
func TestRepairStaleChannelWildcardBindings_KeepsUnboundWildcard(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelInstanceConfig{
			"telegram": {
				Type:    "telegram",
				Enabled: true,
				// WorkspaceID is empty → unbound
			},
		},
		Bindings: []AgentBinding{
			{
				AgentID: "mia",
				Match: BindingMatch{
					Channel:   "telegram",
					AccountID: "*",
				},
			},
		},
	}

	RepairStaleChannelWildcardBindings(cfg)

	if len(cfg.Bindings) != 1 {
		t.Errorf("unbound telegram wildcard binding should be preserved, got %d bindings", len(cfg.Bindings))
	}
}

// TestRepairStaleChannelWildcardBindings_NilConfig is a nil-safety check.
func TestRepairStaleChannelWildcardBindings_NilConfig(t *testing.T) {
	// Must not panic.
	RepairStaleChannelWildcardBindings(nil)
}

// TestRepairStaleChannelWildcardBindings_NoChannels is a no-op for empty channels.
func TestRepairStaleChannelWildcardBindings_NoChannels(t *testing.T) {
	cfg := &Config{
		Bindings: []AgentBinding{
			{
				AgentID: "mia",
				Match: BindingMatch{
					Channel:   "telegram",
					AccountID: "*",
				},
			},
		},
	}
	RepairStaleChannelWildcardBindings(cfg)
	if len(cfg.Bindings) != 1 {
		t.Errorf("bindings changed with no channels; want 1, got %d", len(cfg.Bindings))
	}
}

// TestRepairStaleChannelWildcardBindings_IdentityAgentRequired verifies that
// an instance with WorkspaceID but WITHOUT an agent identity does NOT trigger
// the repair (both fields must be present for a bound representation).
func TestRepairStaleChannelWildcardBindings_IdentityAgentRequired(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelInstanceConfig{
			"whatsapp.eu": {
				Type:        "whatsapp",
				Enabled:     true,
				WorkspaceID: "sales",
				// Identity intentionally nil — not a complete bound representation.
			},
		},
		Bindings: []AgentBinding{
			{
				AgentID: "ray",
				Match: BindingMatch{
					Channel:   "whatsapp.eu",
					AccountID: "*",
				},
			},
		},
	}

	RepairStaleChannelWildcardBindings(cfg)

	// Binding must be preserved (instance is not fully bound without Identity).
	if len(cfg.Bindings) != 1 {
		t.Errorf("binding removed for partially-bound instance; want 1 binding, got %d", len(cfg.Bindings))
	}
}
