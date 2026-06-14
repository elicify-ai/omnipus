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
