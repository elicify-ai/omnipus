package config

import (
	"strings"
	"testing"
)

func TestChannelIdentity_Validate(t *testing.T) {
	tests := []struct {
		name    string
		id      ChannelIdentity
		wantErr bool
	}{
		{"empty kind (back-compat user default)", ChannelIdentity{Kind: ""}, false},
		{"user kind", ChannelIdentity{Kind: "user"}, false},
		{"agent kind with id", ChannelIdentity{Kind: "agent", ID: "mia"}, false},
		{"agent kind without id", ChannelIdentity{Kind: "agent"}, true},
		{"typo'd kind", ChannelIdentity{Kind: "agnet", ID: "mia"}, true},
		{"unknown kind", ChannelIdentity{Kind: "robot"}, true},
		// CRITICAL-1 regression: the API write path (validateChannelIdentity) and
		// route.go both lowercase+trim the kind, so Validate MUST accept exactly
		// those values or an API-constructible config bricks the next load.
		{"mixed-case Agent kind with id", ChannelIdentity{Kind: "Agent", ID: "mia"}, false},
		{"upper AGENT kind with id", ChannelIdentity{Kind: "AGENT", ID: "mia"}, false},
		{"padded user kind", ChannelIdentity{Kind: " user ", ID: ""}, false},
		{"mixed-case User kind", ChannelIdentity{Kind: "User"}, false},
		{"padded mixed-case Agent without id still rejected", ChannelIdentity{Kind: " Agent "}, true},
		{"whitespace-only kind (back-compat empty)", ChannelIdentity{Kind: "   "}, false},
		{"mixed-case unknown still rejected", ChannelIdentity{Kind: "Robot"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.id.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestAgentRef_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     AgentRef
		wantErr bool
	}{
		{"empty kind (back-compat local default)", AgentRef{Kind: "", ID: "x"}, false},
		{"local kind", AgentRef{Kind: "local", ID: "x"}, false},
		{"remote-a2a kind", AgentRef{Kind: "remote-a2a", ID: "x"}, false},
		{"typo'd kind", AgentRef{Kind: "locol", ID: "x"}, true},
		{"unknown kind", AgentRef{Kind: "remote", ID: "x"}, true},
		// CRITICAL-1 regression: mixed-case / whitespace kinds the case-tolerant
		// paths accept must also pass Validate.
		{"mixed-case Local kind", AgentRef{Kind: "Local", ID: "x"}, false},
		{"upper LOCAL kind", AgentRef{Kind: "LOCAL", ID: "x"}, false},
		{"padded local kind", AgentRef{Kind: " local ", ID: "x"}, false},
		{"mixed-case remote-a2a kind", AgentRef{Kind: "Remote-A2A", ID: "x"}, false},
		{"whitespace-only kind (back-compat empty)", AgentRef{Kind: "   ", ID: "x"}, false},
		{"mixed-case unknown still rejected", AgentRef{Kind: "Robot", ID: "x"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateIdentityAndAgentRefKinds_RejectsTypos proves the load-time wiring:
// a typo'd channel identity kind or delegation ref kind fails loudly rather than
// silently downgrading routing.
func TestValidateIdentityAndAgentRefKinds_RejectsTypos(t *testing.T) {
	t.Run("typo'd channel identity kind", func(t *testing.T) {
		cfg := &Config{
			Channels: map[string]ChannelInstanceConfig{
				"telegram": {
					Type:     "telegram",
					Identity: &ChannelIdentity{Kind: "agnet", ID: "mia"},
				},
			},
		}
		err := validateIdentityAndAgentRefKinds(cfg)
		if err == nil || !strings.Contains(err.Error(), "telegram") {
			t.Fatalf("expected typo'd identity kind to be rejected with channel name, got %v", err)
		}
	})

	t.Run("typo'd agent delegation ref kind", func(t *testing.T) {
		cfg := &Config{}
		cfg.Agents.List = []AgentConfig{
			{
				ID: "mia",
				DelegationPolicy: &DelegationPolicy{
					To: []AgentRef{{Kind: "locol", ID: "jim"}},
				},
			},
		}
		err := validateIdentityAndAgentRefKinds(cfg)
		if err == nil || !strings.Contains(err.Error(), "mia") {
			t.Fatalf("expected typo'd delegation ref kind to be rejected with agent id, got %v", err)
		}
	})

	t.Run("valid kinds pass", func(t *testing.T) {
		cfg := &Config{
			Channels: map[string]ChannelInstanceConfig{
				"telegram": {Type: "telegram", Identity: &ChannelIdentity{Kind: "agent", ID: "mia"}},
				"discord":  {Type: "discord", Identity: &ChannelIdentity{Kind: "user"}},
			},
		}
		cfg.Agents.Defaults.DelegationPolicy = &DelegationPolicy{
			To:         []AgentRef{{Kind: "local", ID: "jim"}},
			AcceptFrom: []AgentRef{{Kind: "remote-a2a", ID: "*"}},
		}
		cfg.Agents.List = []AgentConfig{
			{ID: "mia", DelegationPolicy: &DelegationPolicy{To: []AgentRef{{Kind: "", ID: "ray"}}}},
		}
		if err := validateIdentityAndAgentRefKinds(cfg); err != nil {
			t.Fatalf("expected valid kinds to pass, got %v", err)
		}
	})
}

// TestValidateIdentityAndAgentRefKinds_NormalizesKinds proves the CRITICAL-1 fix
// end-to-end: a mixed-case/whitespace kind the API write path accepts (and that
// route.go routes) passes load-time validation AND is rewritten in place to the
// canonical lowercase+trimmed form, so persisted configs never drift from the
// case-tolerant accept paths.
func TestValidateIdentityAndAgentRefKinds_NormalizesKinds(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelInstanceConfig{
			"telegram": {Type: "telegram", Identity: &ChannelIdentity{Kind: "Agent", ID: "mia"}},
			"discord":  {Type: "discord", Identity: &ChannelIdentity{Kind: " User "}},
		},
	}
	cfg.Agents.Defaults.DelegationPolicy = &DelegationPolicy{
		To:         []AgentRef{{Kind: "Local", ID: "jim"}},
		AcceptFrom: []AgentRef{{Kind: " Remote-A2A ", ID: "*"}},
	}
	cfg.Agents.List = []AgentConfig{
		{ID: "mia", DelegationPolicy: &DelegationPolicy{To: []AgentRef{{Kind: "LOCAL", ID: "ray"}}}},
	}

	if err := validateIdentityAndAgentRefKinds(cfg); err != nil {
		t.Fatalf("expected mixed-case kinds to pass load-time validation, got %v", err)
	}

	if got := cfg.Channels["telegram"].Identity.Kind; got != "agent" {
		t.Errorf("channel telegram identity kind = %q, want canonical %q", got, "agent")
	}
	if got := cfg.Channels["discord"].Identity.Kind; got != "user" {
		t.Errorf("channel discord identity kind = %q, want canonical %q", got, "user")
	}
	if got := cfg.Agents.Defaults.DelegationPolicy.To[0].Kind; got != "local" {
		t.Errorf("defaults delegation to[0] kind = %q, want canonical %q", got, "local")
	}
	if got := cfg.Agents.Defaults.DelegationPolicy.AcceptFrom[0].Kind; got != "remote-a2a" {
		t.Errorf("defaults delegation accept_from[0] kind = %q, want canonical %q", got, "remote-a2a")
	}
	if got := cfg.Agents.List[0].DelegationPolicy.To[0].Kind; got != "local" {
		t.Errorf("agent mia delegation to[0] kind = %q, want canonical %q", got, "local")
	}
}

// TestValidateIdentityAndAgentRefKinds_RejectsUnknownEvenMixedCase proves
// normalization does NOT widen the accepted set: a genuinely-unknown kind ("robot")
// is still rejected regardless of casing.
func TestValidateIdentityAndAgentRefKinds_RejectsUnknownEvenMixedCase(t *testing.T) {
	cfg := &Config{
		Channels: map[string]ChannelInstanceConfig{
			"telegram": {Type: "telegram", Identity: &ChannelIdentity{Kind: "Robot", ID: "mia"}},
		},
	}
	if err := validateIdentityAndAgentRefKinds(cfg); err == nil {
		t.Fatal("expected mixed-case unknown identity kind \"Robot\" to be rejected, got nil")
	}
}
